package openai

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

// ---------------------------------------------------------------------------
// DecodeRequest tests
// ---------------------------------------------------------------------------

func TestDecodeRequest_Basic(t *testing.T) {
	body := `{
		"model": "gpt-4",
		"messages": [
			{"role": "system", "content": "You are helpful."},
			{"role": "user", "content": "Hello"}
		],
		"stream": true,
		"max_tokens": 100,
		"temperature": 0.7,
		"top_p": 0.9,
		"stop": ["\n"]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if req.Model != "gpt-4" {
		t.Errorf("model = %q, want gpt-4", req.Model)
	}
	if !req.Stream {
		t.Error("stream = false, want true")
	}
	if req.MaxTokens != 100 {
		t.Errorf("max_tokens = %d, want 100", req.MaxTokens)
	}
	if req.Temperature == nil || *req.Temperature != 0.7 {
		t.Errorf("temperature = %v, want 0.7", req.Temperature)
	}
	if req.TopP == nil || *req.TopP != 0.9 {
		t.Errorf("top_p = %v, want 0.9", req.TopP)
	}
	if len(req.StopWords) != 1 || req.StopWords[0] != "\n" {
		t.Errorf("stop = %v, want [\\n]", req.StopWords)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("messages len = %d, want 2", len(req.Messages))
	}
	if req.Messages[0].Role != codec.RoleSystem {
		t.Errorf("msg[0].role = %q, want system", req.Messages[0].Role)
	}
	if len(req.Messages[0].Content) != 1 || req.Messages[0].Content[0].Text != "You are helpful." {
		t.Errorf("msg[0].content = %v, want text 'You are helpful.'", req.Messages[0].Content)
	}
	if req.Messages[1].Role != codec.RoleUser {
		t.Errorf("msg[1].role = %q, want user", req.Messages[1].Role)
	}
	if len(req.Messages[1].Content) != 1 || req.Messages[1].Content[0].Text != "Hello" {
		t.Errorf("msg[1].content = %v, want text 'Hello'", req.Messages[1].Content)
	}
}

func TestDecodeRequest_MultiContent(t *testing.T) {
	body := `{
		"model": "gpt-4-vision",
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "What is in this image?"},
					{"type": "image_url", "image_url": {"url": "https://example.com/img.png"}}
				]
			}
		],
		"stream": false
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))

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
	if msg.Content[0].Type != codec.ContentTypeText || msg.Content[0].Text != "What is in this image?" {
		t.Errorf("block[0] = %+v, want text block", msg.Content[0])
	}
	if msg.Content[1].Type != codec.ContentTypeImage || msg.Content[1].MediaURL != "https://example.com/img.png" {
		t.Errorf("block[1] = %+v, want image block", msg.Content[1])
	}
}

func TestDecodeRequest_ToolCalls(t *testing.T) {
	body := `{
		"model": "gpt-4",
		"messages": [
			{"role": "user", "content": "What is the weather?"},
			{
				"role": "assistant",
				"content": null,
				"tool_calls": [
					{
						"id": "call_abc",
						"type": "function",
						"function": {
							"name": "get_weather",
							"arguments": "{\"city\":\"London\"}"
						}
					}
				]
			},
			{
				"role": "tool",
				"tool_call_id": "call_abc",
				"content": "22C sunny"
			}
		],
		"tools": [
			{
				"type": "function",
				"function": {
					"name": "get_weather",
					"description": "Get weather for a city",
					"parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
				}
			}
		],
		"stream": false
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	// Check assistant message tool_calls
	assistantMsg := req.Messages[1]
	if len(assistantMsg.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(assistantMsg.ToolCalls))
	}
	tc := assistantMsg.ToolCalls[0]
	if tc.ID != "call_abc" || tc.Name != "get_weather" || tc.Arguments != `{"city":"London"}` {
		t.Errorf("tool_call = %+v", tc)
	}

	// Check tool message
	toolMsg := req.Messages[2]
	if toolMsg.Role != codec.RoleTool || toolMsg.ToolCallID != "call_abc" {
		t.Errorf("tool msg = role:%q id:%q", toolMsg.Role, toolMsg.ToolCallID)
	}
	if len(toolMsg.Content) != 1 || toolMsg.Content[0].Text != "22C sunny" {
		t.Errorf("tool content = %v", toolMsg.Content)
	}

	// Check tools
	if len(req.Tools) != 1 {
		t.Fatalf("tools len = %d, want 1", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" || req.Tools[0].Description != "Get weather for a city" {
		t.Errorf("tool = %+v", req.Tools[0])
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest tests
// ---------------------------------------------------------------------------

func TestEncodeRequest(t *testing.T) {
	temp := 0.5
	req := &codec.Request{
		Model: "gpt-4",
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "Hi there"),
		},
		Stream:      true,
		MaxTokens:   200,
		Temperature: &temp,
	}

	cfg := &codec.ChannelConfig{
		BaseURL:      "https://api.openai.com",
		APIKey:       "sk-test123",
		Model:        "gpt-4-turbo",
		Organization: "org-abc",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	// Verify URL
	if httpReq.URL.String() != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("url = %q", httpReq.URL.String())
	}

	// Verify headers
	if httpReq.Header.Get("Authorization") != "Bearer sk-test123" {
		t.Errorf("auth = %q", httpReq.Header.Get("Authorization"))
	}
	if httpReq.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", httpReq.Header.Get("Content-Type"))
	}
	if httpReq.Header.Get("OpenAI-Organization") != "org-abc" {
		t.Errorf("org = %q", httpReq.Header.Get("OpenAI-Organization"))
	}

	// Verify body uses cfg.Model override
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	if raw["model"] != "gpt-4-turbo" {
		t.Errorf("body model = %v, want gpt-4-turbo", raw["model"])
	}
	if raw["stream"] != true {
		t.Errorf("body stream = %v, want true", raw["stream"])
	}
	if raw["max_tokens"].(float64) != 200 {
		t.Errorf("body max_tokens = %v", raw["max_tokens"])
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse non-stream tests
// ---------------------------------------------------------------------------

func TestDecodeResponse_NonStream(t *testing.T) {
	respBody := `{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {"role": "assistant", "content": "Hello there!"},
			"finish_reason": "stop"
		}],
		"usage": {
			"prompt_tokens": 10,
			"completion_tokens": 5,
			"total_tokens": 15
		}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: StreamStart, ContentDelta, (finish_reason event), Usage, Done
	if len(events) < 4 {
		t.Fatalf("events count = %d, want >= 4, events: %+v", len(events), events)
	}

	if events[0].Type != codec.EventStreamStart {
		t.Errorf("event[0] type = %v, want StreamStart", events[0].Type)
	}

	// Find content delta
	found := false
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Hello there!" {
			found = true
			break
		}
	}
	if !found {
		t.Error("missing content delta with 'Hello there!'")
	}

	// Find usage
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 10 && ev.Usage.CompletionTokens == 5 && ev.Usage.TotalTokens == 15 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event")
	}

	// Last event should be Done
	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event = %v, want Done", events[len(events)-1].Type)
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse stream tests
// ---------------------------------------------------------------------------

func TestDecodeResponse_Stream(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}

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

	// Expect: StreamStart, ContentDelta("Hello"), ContentDelta(" world"), Usage, Done
	if events[0].Type != codec.EventStreamStart {
		t.Errorf("event[0] type = %v, want StreamStart", events[0].Type)
	}

	// Collect text deltas
	var text strings.Builder
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil {
			text.WriteString(ev.Delta.Text)
		}
	}
	if text.String() != "Hello world" {
		t.Errorf("concatenated text = %q, want 'Hello world'", text.String())
	}

	// Check usage
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 5 && ev.Usage.CompletionTokens == 2 && ev.Usage.TotalTokens == 7 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event in stream")
	}

	// Last event should be Done
	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event = %v, want Done", events[len(events)-1].Type)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse non-stream tests
// ---------------------------------------------------------------------------

func TestEncodeResponse_NonStream(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Test response",
		},
	}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     8,
			CompletionTokens: 3,
			TotalTokens:      11,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", w.Header().Get("Content-Type"))
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if !strings.HasPrefix(resp.ID, "chatcmpl-") {
		t.Errorf("id = %q, want chatcmpl- prefix", resp.ID)
	}
	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "Test response" {
		t.Errorf("content = %v", resp.Choices[0].Message)
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %v", resp.Choices[0].FinishReason)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 11 {
		t.Errorf("usage = %v", resp.Usage)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse stream tests
// ---------------------------------------------------------------------------

// flushRecorder implements http.Flusher over httptest.ResponseRecorder.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushRecorder) Flush() {
	f.flushed++
	f.ResponseRecorder.Flush()
}

func TestEncodeResponse_Stream(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hi",
		},
	}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        " there",
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}

	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream: %v", err)
	}

	if rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Errorf("content-type = %q", rec.Header().Get("Content-Type"))
	}

	output := rec.Body.String()

	// Verify SSE format: lines start with "data: "
	lines := strings.Split(output, "\n")
	dataLines := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLines++
		}
	}

	// We should have: StreamStart chunk, content "Hi", content " there", Done chunk, [DONE]
	if dataLines < 4 {
		t.Errorf("data lines = %d, want >= 4, output:\n%s", dataLines, output)
	}

	// Must end with data: [DONE]
	if !strings.Contains(output, "data: [DONE]") {
		t.Errorf("missing data: [DONE] in output:\n%s", output)
	}

	// Verify flush was called
	if w.flushed == 0 {
		t.Error("Flush was never called")
	}

	// Verify content chunks are valid JSON
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			var chunk oaiResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				t.Errorf("invalid JSON chunk: %v, data: %s", err, data)
			}
			if chunk.Object != "chat.completion.chunk" {
				t.Errorf("chunk object = %q, want chat.completion.chunk", chunk.Object)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// EncodeError test
// ---------------------------------------------------------------------------

func TestEncodeError(t *testing.T) {
	w := httptest.NewRecorder()
	c := &ChatCodec{}
	c.EncodeError(w, http.StatusBadRequest, fmt.Errorf("invalid model"))

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", w.Header().Get("Content-Type"))
	}

	var resp oaiErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Message != "invalid model" {
		t.Errorf("error message = %q", resp.Error.Message)
	}
	if resp.Error.Type != "server_error" {
		t.Errorf("error type = %q", resp.Error.Type)
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse stream with tool calls
// ---------------------------------------------------------------------------

func TestDecodeResponse_StreamToolCalls(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"get_time","arguments":""}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"","type":"function","function":{"name":"","arguments":"{\"tz\":"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"tool_calls":[{"id":"","type":"function","function":{"name":"","arguments":"\"UTC\"}"}}]},"finish_reason":null}]}

data: {"id":"chatcmpl-2","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}

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

	var startEvents []codec.Event
	var argDeltaEvents []codec.Event
	for ev := range ch {
		switch ev.Type {
		case codec.EventToolCallStart:
			startEvents = append(startEvents, ev)
		case codec.EventToolCallArgumentsDelta:
			argDeltaEvents = append(argDeltaEvents, ev)
		}
	}

	if len(startEvents) < 1 {
		t.Fatalf("EventToolCallStart events = %d, want >= 1", len(startEvents))
	}
	first := startEvents[0]
	if first.ToolCall == nil {
		t.Fatal("first tool call Start has nil ToolCall")
	}
	if first.ToolCall.CallID != "call_1" {
		t.Errorf("tool call id = %q, want call_1", first.ToolCall.CallID)
	}
	if first.ToolCall.Name != "get_time" {
		t.Errorf("tool call name = %q, want get_time", first.ToolCall.Name)
	}

	var args strings.Builder
	for _, ev := range argDeltaEvents {
		if ev.ToolCall != nil {
			args.WriteString(ev.ToolCall.Arguments)
		}
	}
	if args.String() != `{"tz":"UTC"}` {
		t.Errorf("concatenated args = %q, want {\"tz\":\"UTC\"}", args.String())
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest with extended IR fields
// ---------------------------------------------------------------------------

func TestChatEncodeRequest_WithExtendedIRFields(t *testing.T) {
	parallel := true
	store := false
	irReq := &codec.Request{
		Model:             "gpt-5",
		Stream:            true,
		ToolChoice:        &codec.ToolChoice{Type: "auto"},
		ParallelToolCalls: &parallel,
		Store:             &store,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "hello"),
		},
		Tools: []codec.Tool{
			{Name: "test", Description: "test tool", InputSchema: map[string]any{"type": "object"}, Type: "function"},
		},
	}

	c := &ChatCodec{}
	cfg := &codec.ChannelConfig{BaseURL: "https://api.example.com", APIKey: "sk-test", Model: "gpt-5"}
	httpReq, err := c.EncodeRequest(irReq, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(httpReq.Body)
	var outMap map[string]any
	json.Unmarshal(outBody, &outMap)

	if outMap["tool_choice"] != "auto" {
		t.Errorf("tool_choice = %v, want auto", outMap["tool_choice"])
	}
	if outMap["parallel_tool_calls"] != true {
		t.Errorf("parallel_tool_calls = %v, want true", outMap["parallel_tool_calls"])
	}
	if outMap["store"] != false {
		t.Errorf("store = %v, want false", outMap["store"])
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest with no organization header
// ---------------------------------------------------------------------------

func TestEncodeRequest_NoOrg(t *testing.T) {
	req := &codec.Request{
		Model:    "gpt-3.5-turbo",
		Messages: []codec.Message{codec.TextMessage(codec.RoleUser, "test")},
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-key",
		Model:   "gpt-3.5-turbo",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	if httpReq.Header.Get("OpenAI-Organization") != "" {
		t.Errorf("should not have org header, got %q", httpReq.Header.Get("OpenAI-Organization"))
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse non-stream with tool calls
// ---------------------------------------------------------------------------

func TestDecodeResponse_NonStreamToolCalls(t *testing.T) {
	respBody := `{
		"id": "chatcmpl-tc",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"content": null,
				"tool_calls": [{
					"id": "call_abc",
					"type": "function",
					"function": {"name": "get_weather", "arguments": "{\"city\":\"London\"}"}
				}]
			},
			"finish_reason": "tool_calls"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
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
			if tc.ID != "call_abc" {
				t.Errorf("tool call id = %q, want call_abc", tc.ID)
			}
			if tc.Name != "get_weather" {
				t.Errorf("tool call name = %q, want get_weather", tc.Name)
			}
			if tc.Arguments != `{"city":"London"}` {
				t.Errorf("tool call args = %q, want {\"city\":\"London\"}", tc.Arguments)
			}
			foundTC = true
		}
	}
	if !foundTC {
		t.Error("missing tool call delta event")
	}

	// Find usage
	foundUsage := false
	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.PromptTokens == 10 && ev.Usage.CompletionTokens == 8 && ev.Usage.TotalTokens == 18 {
				foundUsage = true
			}
		}
	}
	if !foundUsage {
		t.Error("missing usage event")
	}

	// Last event should be Done
	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event = %v, want Done", events[len(events)-1].Type)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse non-stream with tool calls
// ---------------------------------------------------------------------------

func TestEncodeResponse_NonStreamToolCalls(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventToolCallDelta,
		Delta: &codec.DeltaPayload{
			ToolCall: &codec.ToolCallDelta{
				ID:        "call_1",
				Name:      "get_time",
				Arguments: `{"tz":"UTC"}`,
			},
		},
	}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     5,
			CompletionTokens: 3,
			TotalTokens:      8,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	choice := resp.Choices[0]
	if choice.Message == nil {
		t.Fatal("message is nil")
	}
	if len(choice.Message.ToolCalls) != 1 {
		t.Fatalf("tool_calls len = %d, want 1", len(choice.Message.ToolCalls))
	}
	tc := choice.Message.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Errorf("tool call id = %q, want call_1", tc.ID)
	}
	if tc.Function.Name != "get_time" {
		t.Errorf("tool call name = %q, want get_time", tc.Function.Name)
	}
	if tc.Function.Arguments != `{"tz":"UTC"}` {
		t.Errorf("tool call args = %q, want {\"tz\":\"UTC\"}", tc.Function.Arguments)
	}
	if choice.FinishReason == nil || *choice.FinishReason != "tool_calls" {
		t.Errorf("finish_reason = %v, want tool_calls", choice.FinishReason)
	}

	// Verify body is valid JSON
	if !json.Valid(w.Body.Bytes()) {
		t.Error("response body is not valid JSON")
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse stream with tool calls
// ---------------------------------------------------------------------------

func TestEncodeResponse_StreamToolCalls(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{Type: codec.EventToolCallStart, ToolCall: &codec.StreamingToolCall{CallID: "call_2", Index: 0, Name: "search"}}
	ch <- codec.Event{Type: codec.EventToolCallArgumentsDelta, ToolCall: &codec.StreamingToolCall{CallID: "call_2", Arguments: `{"q":"test"}`}}
	ch <- codec.Event{Type: codec.EventToolCallEnd, ToolCall: &codec.StreamingToolCall{CallID: "call_2"}}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}

	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream: %v", err)
	}

	output := rec.Body.String()

	// Verify SSE contains tool_calls
	if !strings.Contains(output, "tool_calls") {
		t.Errorf("output missing tool_calls, output:\n%s", output)
	}

	// Must end with data: [DONE]
	if !strings.Contains(output, "data: [DONE]") {
		t.Errorf("missing data: [DONE] in output:\n%s", output)
	}

	// Verify data lines are valid JSON
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				continue
			}
			var chunk oaiResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				t.Errorf("invalid JSON chunk: %v, data: %s", err, data)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// DecodeRequest stop as single string
// ---------------------------------------------------------------------------

func TestDecodeRequest_StopString(t *testing.T) {
	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hi"}],"stop":"END"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.StopWords) != 1 || req.StopWords[0] != "END" {
		t.Errorf("stop = %v, want [END]", req.StopWords)
	}
}

// ---------------------------------------------------------------------------
// DecodeRequest empty messages
// ---------------------------------------------------------------------------

func TestDecodeRequest_EmptyMessages(t *testing.T) {
	body := `{"model":"gpt-4","messages":[]}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	if len(req.Messages) != 0 {
		t.Errorf("messages len = %d, want 0", len(req.Messages))
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse error body
// ---------------------------------------------------------------------------

func TestDecodeResponse_ErrorBody(t *testing.T) {
	respBody := `{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have at least StreamStart and Done without panicking
	if len(events) < 2 {
		t.Fatalf("events count = %d, want >= 2", len(events))
	}
	if events[0].Type != codec.EventStreamStart {
		t.Errorf("event[0] type = %v, want StreamStart", events[0].Type)
	}
	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event = %v, want Done", events[len(events)-1].Type)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse finish_reason length
// ---------------------------------------------------------------------------

func TestEncodeResponse_FinishReasonLength(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "partial output",
		},
	}
	ch <- codec.Event{FinishReason: "length"}
	ch <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     10,
			CompletionTokens: 50,
			TotalTokens:      60,
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "length" {
		t.Errorf("finish_reason = %v, want length", resp.Choices[0].FinishReason)
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest with tools
// ---------------------------------------------------------------------------

func TestEncodeRequest_WithTools(t *testing.T) {
	req := &codec.Request{
		Model:    "gpt-4",
		Messages: []codec.Message{codec.TextMessage(codec.RoleUser, "search for cats")},
		Tools: []codec.Tool{
			{
				Name:        "search",
				Description: "Search the web",
				InputSchema: map[string]any{"type": "object"},
			},
		},
	}

	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-test",
		Model:   "gpt-4",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v, want array of length 1", raw["tools"])
	}

	tool := tools[0].(map[string]any)
	if tool["type"] != "function" {
		t.Errorf("tool type = %v, want function", tool["type"])
	}

	fn, ok := tool["function"].(map[string]any)
	if !ok {
		t.Fatal("tool function is not an object")
	}
	if fn["name"] != "search" {
		t.Errorf("function name = %v, want search", fn["name"])
	}
	if fn["description"] != "Search the web" {
		t.Errorf("function description = %v, want 'Search the web'", fn["description"])
	}
	params, ok := fn["parameters"].(map[string]any)
	if !ok {
		t.Fatal("function parameters is not an object")
	}
	if params["type"] != "object" {
		t.Errorf("parameters type = %v, want object", params["type"])
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest with tool calls in messages
// ---------------------------------------------------------------------------

func TestEncodeRequest_WithToolCalls(t *testing.T) {
	req := &codec.Request{
		Model: "gpt-4",
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "What time is it?"),
			{
				Role: codec.RoleAssistant,
				ToolCalls: []codec.ToolCall{
					{
						ID:        "call_t1",
						Name:      "get_time",
						Arguments: `{"tz":"UTC"}`,
					},
				},
			},
			{
				Role:       codec.RoleTool,
				ToolCallID: "call_t1",
				Content: []codec.ContentBlock{
					{Type: codec.ContentTypeText, Text: "14:30 UTC"},
				},
			},
		},
	}

	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.openai.com",
		APIKey:  "sk-test",
		Model:   "gpt-4",
	}

	c := &ChatCodec{}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	if err := json.Unmarshal(bodyBytes, &raw); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}

	messages, ok := raw["messages"].([]any)
	if !ok || len(messages) != 3 {
		t.Fatalf("messages = %v, want array of length 3", raw["messages"])
	}

	// Check assistant message has tool_calls
	assistantMsg := messages[1].(map[string]any)
	tcs, ok := assistantMsg["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Fatalf("assistant tool_calls = %v, want array of length 1", assistantMsg["tool_calls"])
	}
	tc := tcs[0].(map[string]any)
	if tc["id"] != "call_t1" {
		t.Errorf("tool call id = %v, want call_t1", tc["id"])
	}
	if tc["type"] != "function" {
		t.Errorf("tool call type = %v, want function", tc["type"])
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "get_time" {
		t.Errorf("function name = %v, want get_time", fn["name"])
	}
	if fn["arguments"] != `{"tz":"UTC"}` {
		t.Errorf("function arguments = %v, want {\"tz\":\"UTC\"}", fn["arguments"])
	}

	// Check tool message has tool_call_id
	toolMsg := messages[2].(map[string]any)
	if toolMsg["tool_call_id"] != "call_t1" {
		t.Errorf("tool_call_id = %v, want call_t1", toolMsg["tool_call_id"])
	}
	if toolMsg["role"] != "tool" {
		t.Errorf("tool msg role = %v, want tool", toolMsg["role"])
	}
}

// ---------------------------------------------------------------------------
// RawJSON content block preservation tests
// ---------------------------------------------------------------------------

func TestChatDecodeRequest_UnknownContentBlockType_PreservedAsRawJSON(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hello"},
				{"type": "input_audio", "input_audio": {"data": "base64...", "format": "wav"}}
			]}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	m := req.Messages[0]
	if len(m.Content) != 2 {
		t.Fatalf("content len = %d, want 2", len(m.Content))
	}

	if m.Content[1].RawJSON == nil {
		t.Fatal("content[1].RawJSON should not be nil")
	}
	var raw map[string]any
	json.Unmarshal(m.Content[1].RawJSON, &raw)
	if raw["type"] != "input_audio" {
		t.Errorf("RawJSON type = %v, want input_audio", raw["type"])
	}
}

func TestChatEncodeRequest_UnknownContentBlock_RoundTrip(t *testing.T) {
	body := `{
		"model": "gpt-4o",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hello"},
				{"type": "input_audio", "input_audio": {"data": "base64data", "format": "wav"}}
			]}
		]
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ChatCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	cfg := &codec.ChannelConfig{BaseURL: "https://api.example.com", APIKey: "k", Model: "gpt-4o"}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(httpReq.Body)
	var out map[string]any
	json.Unmarshal(outBody, &out)

	msgs := out["messages"].([]any)
	msg := msgs[0].(map[string]any)

	content, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content is not array: %T", msg["content"])
	}
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}

	unknown := content[1].(map[string]any)
	if unknown["type"] != "input_audio" {
		t.Errorf("type = %v, want input_audio", unknown["type"])
	}
}

func TestChatEncodeRequest_SkipsRawJSONMessages(t *testing.T) {
	req := &codec.Request{
		Model:  "gpt-4o",
		Stream: false,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "hello"),
			{RawJSON: json.RawMessage(`{"type":"unknown"}`)},
		},
	}

	c := &ChatCodec{}
	cfg := &codec.ChannelConfig{BaseURL: "https://api.example.com", APIKey: "k", Model: "gpt-4o"}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(httpReq.Body)
	var out map[string]any
	json.Unmarshal(outBody, &out)

	msgs := out["messages"].([]any)
	if len(msgs) != 1 {
		t.Errorf("messages len = %d, want 1 (RawJSON should be skipped)", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// Reasoning content (thinking) tests
// ---------------------------------------------------------------------------

func TestDecodeResponse_StreamReasoningContent(t *testing.T) {
	sseData := `data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"\n"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Okay"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":", the user said hello."},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{"role":"assistant","content":"Hello"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{"role":"assistant","content":"! How can I help?"},"finish_reason":null}]}

data: {"id":"chatcmpl-1","object":"chat.completion.chunk","created":1773572841,"model":"qwen/qwen3-32b","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

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

	// Collect thinking deltas
	var thinking strings.Builder
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil {
			thinking.WriteString(ev.Delta.Text)
		}
	}
	if thinking.String() != "\nOkay, the user said hello." {
		t.Errorf("thinking text = %q, want %q", thinking.String(), "\nOkay, the user said hello.")
	}

	// Collect content deltas
	var content strings.Builder
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil {
			content.WriteString(ev.Delta.Text)
		}
	}
	if content.String() != "Hello! How can I help?" {
		t.Errorf("content text = %q, want %q", content.String(), "Hello! How can I help?")
	}

	// Verify thinking deltas have correct ContentType
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil {
			if ev.Delta.ContentType != codec.ContentTypeThinking {
				t.Errorf("thinking delta ContentType = %q, want %q", ev.Delta.ContentType, codec.ContentTypeThinking)
			}
		}
	}
}

func TestDecodeResponse_NonStreamReasoningContent(t *testing.T) {
	respBody := `{
		"id": "chatcmpl-test",
		"object": "chat.completion",
		"choices": [{
			"index": 0,
			"message": {
				"role": "assistant",
				"reasoning_content": "The user said hello. I should respond friendly.",
				"content": "Hello! How can I help?"
			},
			"finish_reason": "stop"
		}],
		"usage": {"prompt_tokens": 10, "completion_tokens": 15, "total_tokens": 25}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	c := &ChatCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Find thinking delta
	foundThinking := false
	for _, ev := range events {
		if ev.Type == codec.EventThinkingDelta && ev.Delta != nil {
			if ev.Delta.Text == "The user said hello. I should respond friendly." {
				foundThinking = true
			}
		}
	}
	if !foundThinking {
		t.Error("missing thinking delta with reasoning_content")
	}

	// Find content delta
	foundContent := false
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil {
			if ev.Delta.Text == "Hello! How can I help?" {
				foundContent = true
			}
		}
	}
	if !foundContent {
		t.Error("missing content delta")
	}
}

func TestEncodeResponse_StreamReasoningContent(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventThinkingDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeThinking,
			Text:        "Let me think...",
		},
	}
	ch <- codec.Event{
		Type: codec.EventThinkingDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeThinking,
			Text:        " The user wants help.",
		},
	}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hello! How can I help?",
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}

	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream: %v", err)
	}

	output := rec.Body.String()

	// Verify SSE contains reasoning_content
	if !strings.Contains(output, "reasoning_content") {
		t.Errorf("output missing reasoning_content, output:\n%s", output)
	}

	// Parse all chunks and verify reasoning_content appears in delta
	lines := strings.Split(output, "\n")
	foundReasoning := false
	foundContent := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			continue
		}
		var chunk oaiResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			t.Errorf("invalid JSON chunk: %v", err)
			continue
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			if chunk.Choices[0].Delta.ReasoningContent != "" {
				foundReasoning = true
			}
			if chunk.Choices[0].Delta.Content != "" {
				foundContent = true
			}
		}
	}
	if !foundReasoning {
		t.Error("no chunk with reasoning_content found in stream output")
	}
	if !foundContent {
		t.Error("no chunk with content found in stream output")
	}
}

func TestEncodeResponse_NonStreamReasoningContent(t *testing.T) {
	ch := make(chan codec.Event, 10)
	ch <- codec.Event{Type: codec.EventStreamStart}
	ch <- codec.Event{
		Type: codec.EventThinkingDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeThinking,
			Text:        "Thinking about the answer.",
		},
	}
	ch <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "The answer is 42.",
		},
	}
	ch <- codec.Event{Type: codec.EventDone}
	close(ch)

	w := httptest.NewRecorder()
	c := &ChatCodec{}
	err := c.EncodeResponse(ch, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	var resp oaiResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	msg := resp.Choices[0].Message
	if msg == nil {
		t.Fatal("message is nil")
	}
	if msg.ReasoningContent != "Thinking about the answer." {
		t.Errorf("reasoning_content = %q, want %q", msg.ReasoningContent, "Thinking about the answer.")
	}
	if msg.Content != "The answer is 42." {
		t.Errorf("content = %q, want %q", msg.Content, "The answer is 42.")
	}
}

// TestDecodeResponse_StreamReasoningContentRoundTrip verifies that reasoning_content
// survives a full decode → encode round trip through the codec.
func TestDecodeResponse_StreamReasoningContentRoundTrip(t *testing.T) {
	// Simulate upstream SSE with reasoning_content followed by content
	sseData := `data: {"id":"chatcmpl-rt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}

data: {"id":"chatcmpl-rt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":"Think step 1."},"finish_reason":null}]}

data: {"id":"chatcmpl-rt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"reasoning_content":" Think step 2."},"finish_reason":null}]}

data: {"id":"chatcmpl-rt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"content":"Answer."},"finish_reason":null}]}

data: {"id":"chatcmpl-rt","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}

data: [DONE]

`

	// Decode
	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sseData)),
	}
	c := &ChatCodec{}
	decodedCh, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse: %v", err)
	}

	// Collect events and re-encode
	var allEvents []codec.Event
	for ev := range decodedCh {
		allEvents = append(allEvents, ev)
	}

	// Feed into encode
	encodeCh := make(chan codec.Event, len(allEvents))
	for _, ev := range allEvents {
		encodeCh <- ev
	}
	close(encodeCh)

	rec := httptest.NewRecorder()
	w := &flushRecorder{ResponseRecorder: rec}
	err = c.EncodeResponse(encodeCh, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse: %v", err)
	}

	output := rec.Body.String()

	// Verify reasoning_content appears in encoded output
	if !strings.Contains(output, "reasoning_content") {
		t.Errorf("round-trip output missing reasoning_content:\n%s", output)
	}
	if !strings.Contains(output, "Answer.") {
		t.Errorf("round-trip output missing content:\n%s", output)
	}
}

func TestChatEncodeRequest_CustomEndpointPath(t *testing.T) {
	c := &ChatCodec{}
	req := &codec.Request{
		Model:  "gpt-4o",
		Stream: false,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "hello"),
		},
	}
	cfg := &codec.ChannelConfig{
		BaseURL:      "https://custom.api.com",
		APIKey:       "sk-test",
		Model:        "gpt-4o",
		EndpointPath: "/custom/chat",
	}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	want := "https://custom.api.com/custom/chat"
	if httpReq.URL.String() != want {
		t.Errorf("URL = %q, want %q", httpReq.URL.String(), want)
	}
}
