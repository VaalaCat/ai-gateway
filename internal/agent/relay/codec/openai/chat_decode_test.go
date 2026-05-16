package openai

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/stretchr/testify/require"
)

// collectStreamEvents runs the chat stream decoder on the given SSE string and
// returns all emitted events. This is a test helper used by streaming tests.
func collectStreamEvents(t *testing.T, sseData string) []codec.Event {
	t.Helper()
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}
	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}
	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}
	return events
}

// ---------------------------------------------------------------------------
// DecodeRequest from fixture files
// ---------------------------------------------------------------------------

func TestChatDecodeRequest_Simple(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "openai_chat", "request_simple.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if req.Model != "gpt-4o" {
		t.Errorf("model = %q, want gpt-4o", req.Model)
	}
	if req.Stream {
		t.Error("stream = true, want false")
	}
	if req.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", req.MaxTokens)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != codec.RoleSystem {
		t.Errorf("msg[0].role = %q, want system", req.Messages[0].Role)
	}
	if req.Messages[1].Role != codec.RoleUser {
		t.Errorf("msg[1].role = %q, want user", req.Messages[1].Role)
	}
	if len(req.Messages[1].Content) != 1 || req.Messages[1].Content[0].Text != "Hello!" {
		t.Errorf("msg[1].content = %v, want text 'Hello!'", req.Messages[1].Content)
	}
}

func TestChatDecodeRequest_Tools(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "openai_chat", "request_tools.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(data)))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q, want get_weather", req.Tools[0].Name)
	}
	if req.Tools[0].Description != "Get current weather" {
		t.Errorf("tool description = %q, want 'Get current weather'", req.Tools[0].Description)
	}
	if req.ToolChoice == nil || req.ToolChoice.Type != "auto" {
		t.Errorf("tool_choice = %v, want auto", req.ToolChoice)
	}
}

// ---------------------------------------------------------------------------
// O6: DecodeRequest must parse data: URIs into MediaB64 + MimeType
// ---------------------------------------------------------------------------

func TestChatDecodeRequest_DataURI(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "What is this?"},
					{"type": "image_url", "image_url": {"url": "data:image/png;base64,abc123"}}
				]
			}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(req.Messages))
	}
	msg := req.Messages[0]
	if len(msg.Content) != 2 {
		t.Fatalf("content blocks = %d, want 2", len(msg.Content))
	}

	img := msg.Content[1]
	if img.Type != codec.ContentTypeImage {
		t.Errorf("type = %q, want image", img.Type)
	}
	if img.MediaB64 != "abc123" {
		t.Errorf("MediaB64 = %q, want abc123", img.MediaB64)
	}
	if img.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", img.MimeType)
	}
	if img.MediaURL != "" {
		t.Errorf("MediaURL should be empty for data: URI, got %q", img.MediaURL)
	}
}

// ---------------------------------------------------------------------------
// O1: Stream decode must emit FinishReason from choice
// ---------------------------------------------------------------------------

func TestChatDecodeStream_FinishReason(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hi"},"finish_reason":null}]}

data: {"id":"chatcmpl-fr","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// There must be an event with FinishReason == "stop"
	found := false
	for _, ev := range events {
		if ev.FinishReason == "stop" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no event with FinishReason='stop' found; events: %+v", events)
	}
}

func TestChatDecodeStream_FinishReason_ToolCalls(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"fn","arguments":"{}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-tc","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

data: [DONE]

`
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	found := false
	for _, ev := range events {
		if ev.FinishReason == "tool_calls" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no event with FinishReason='tool_calls' found; events: %+v", events)
	}
}

// ---------------------------------------------------------------------------
// DecodeStream tool calls
// ---------------------------------------------------------------------------

func TestChatDecodeStream_ToolCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "openai_chat", "stream_tool_calls.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &ChatCodec{}
	ch, errDec := c.DecodeResponse(resp, true)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var startEvents []codec.Event
	var argDeltas []codec.Event
	var endEvents []codec.Event
	for ev := range ch {
		switch ev.Type {
		case codec.EventToolCallStart:
			startEvents = append(startEvents, ev)
		case codec.EventToolCallArgumentsDelta:
			argDeltas = append(argDeltas, ev)
		case codec.EventToolCallEnd:
			endEvents = append(endEvents, ev)
		}
	}

	if len(startEvents) < 1 {
		t.Fatal("expected at least one EventToolCallStart")
	}
	first := startEvents[0]
	if first.ToolCall == nil {
		t.Fatal("first Start event missing ToolCall")
	}
	if first.ToolCall.CallID != "call_abc" {
		t.Errorf("tool call ID = %q, want call_abc", first.ToolCall.CallID)
	}
	if first.ToolCall.Name != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", first.ToolCall.Name)
	}

	var args strings.Builder
	for _, ev := range argDeltas {
		if ev.ToolCall != nil {
			args.WriteString(ev.ToolCall.Arguments)
		}
	}
	if args.String() != `{"city":"Tokyo"}` {
		t.Errorf("concatenated args = %q, want {\"city\":\"Tokyo\"}", args.String())
	}
	if len(endEvents) < 1 {
		t.Error("expected at least one EventToolCallEnd")
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse non-stream from fixtures
// ---------------------------------------------------------------------------

func TestChatDecodeResponse_Text(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "openai_chat", "response_text.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &ChatCodec{}
	ch, errDec := c.DecodeResponse(resp, false)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have StreamStart, ContentDelta, FinishReason, Usage, Done
	if events[0].Type != codec.EventStreamStart {
		t.Errorf("first event type = %v, want StreamStart", events[0].Type)
	}
	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event type = %v, want Done", events[len(events)-1].Type)
	}

	// Find content
	foundContent := false
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Hello! How can I help?" {
			foundContent = true
		}
	}
	if !foundContent {
		t.Error("missing content delta 'Hello! How can I help?'")
	}

	// Find usage with cached_tokens
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 20 && ev.Usage.CompletionTokens == 10 && ev.Usage.CachedTokens == 5 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event with cached_tokens")
	}
}

func TestChatDecodeResponse_ToolCalls(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "testdata", "openai_chat", "response_tool_calls.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(string(data))),
	}

	c := &ChatCodec{}
	ch, errDec := c.DecodeResponse(resp, false)
	if errDec != nil {
		t.Fatalf("DecodeResponse: %v", errDec)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Find tool call delta
	foundTC := false
	for _, ev := range events {
		if ev.Type == codec.EventToolCallDelta && ev.Delta != nil && ev.Delta.ToolCall != nil {
			tc := ev.Delta.ToolCall
			if tc.ID == "call_abc" && tc.Name == "get_weather" && tc.Arguments == `{"city":"Tokyo"}` {
				foundTC = true
			}
		}
	}
	if !foundTC {
		t.Error("missing tool call delta from fixture")
	}

	// Find finish_reason == "tool_calls"
	foundFR := false
	for _, ev := range events {
		if ev.FinishReason == "tool_calls" {
			foundFR = true
		}
	}
	if !foundFR {
		t.Error("missing FinishReason='tool_calls'")
	}
}

// ---------------------------------------------------------------------------
// TestChatCodecSetsInboundProtocol — Task 7: InboundProtocol field
// ---------------------------------------------------------------------------

func TestChatCodecSetsInboundProtocol(t *testing.T) {
	body := `{"model":"gpt-5","messages":[{"role":"user","content":"hi"}]}`
	httpReq, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.InboundProtocol != codec.ProtocolOpenAIChat {
		t.Errorf("want %q, got %q", codec.ProtocolOpenAIChat, req.InboundProtocol)
	}
}

// ---------------------------------------------------------------------------
// Task 3: streaming tool_call events + empty content filter
// ---------------------------------------------------------------------------

func TestChatStreamDecode_ToolCallEventsCorrectShape(t *testing.T) {
	// Upstream chat-SSE: 1 chunk with id+name + 2 args fragment chunks + final
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_x","type":"function","function":{"name":"exec","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":":1}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")

	events := collectStreamEvents(t, sse)

	var start, end *codec.Event
	var deltas []*codec.Event
	for i := range events {
		switch events[i].Type {
		case codec.EventToolCallStart:
			start = &events[i]
		case codec.EventToolCallArgumentsDelta:
			deltas = append(deltas, &events[i])
		case codec.EventToolCallEnd:
			end = &events[i]
		}
	}
	require.NotNil(t, start)
	require.Equal(t, "call_x", start.ToolCall.CallID)
	require.Equal(t, "exec", start.ToolCall.Name)
	require.Len(t, deltas, 2)
	require.Equal(t, `{"a"`, deltas[0].ToolCall.Arguments)
	require.Equal(t, `:1}`, deltas[1].ToolCall.Arguments)
	require.NotNil(t, end)
	require.Equal(t, `{"a":1}`, end.ToolCall.Arguments)

	require.NoError(t, codec.AssertStreamingToolCallInvariant(events))
}

func TestChatStreamDecode_NoEmptyContentDelta(t *testing.T) {
	// delta.content == null should not emit EventContentDelta
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"role":"assistant","content":null,"reasoning_content":""}}]}`,
		`data: {"choices":[{"delta":{"content":""}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
		``,
	}, "\n\n")
	events := collectStreamEvents(t, sse)
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta {
			require.NotEmpty(t, ev.Delta.Text, "should not emit EventContentDelta with empty Text, got %+v", ev)
		}
	}
}

func TestChatDecode_RequestHistoryReasoningContentRoundtrip(t *testing.T) {
	body := `{
		"model": "deepseek-v4",
		"messages": [
			{"role": "user", "content": "hi"},
			{"role": "assistant", "content": "answer", "reasoning_content": "let me think..."}
		]
	}`

	httpReq := httptest.NewRequest("POST", "/v1/chat/completions", bytes.NewReader([]byte(body)))
	httpReq.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(httpReq)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Messages) != 2 {
		t.Fatalf("messages count = %d, want 2", len(req.Messages))
	}
	asst := req.Messages[1]
	if asst.Role != codec.RoleAssistant {
		t.Fatalf("role = %q, want assistant", asst.Role)
	}
	// 第一个 block 应为 thinking（prepend 到首位）
	if len(asst.Content) < 1 || asst.Content[0].Type != codec.ContentTypeThinking {
		t.Fatalf("first content block type = %q, want thinking", asst.Content[0].Type)
	}
	if asst.Content[0].Text != "let me think..." {
		t.Fatalf("thinking text = %q, want %q", asst.Content[0].Text, "let me think...")
	}
	// 后续 block 应为 text
	if asst.Content[1].Type != codec.ContentTypeText || asst.Content[1].Text != "answer" {
		t.Fatalf("second block: type=%q text=%q", asst.Content[1].Type, asst.Content[1].Text)
	}
}
