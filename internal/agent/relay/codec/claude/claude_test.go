package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

// ---------------------------------------------------------------------------
// DecodeRequest
// ---------------------------------------------------------------------------

func TestDecodeRequest_WithSystemPrompt(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"system": "You are helpful.",
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "hello"}]}
		],
		"stream": true
	}`

	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	if req.Model != "claude-sonnet-4-20250514" {
		t.Errorf("model = %q, want %q", req.Model, "claude-sonnet-4-20250514")
	}
	if req.MaxTokens != 1024 {
		t.Errorf("max_tokens = %d, want 1024", req.MaxTokens)
	}
	if !req.Stream {
		t.Error("stream should be true")
	}

	// System should be first message
	if len(req.Messages) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(req.Messages))
	}

	if req.Messages[0].Role != codec.RoleSystem {
		t.Errorf("first message role = %q, want %q", req.Messages[0].Role, codec.RoleSystem)
	}
	if req.Messages[0].Content[0].Text != "You are helpful." {
		t.Errorf("system text = %q, want %q", req.Messages[0].Content[0].Text, "You are helpful.")
	}

	if req.Messages[1].Role != codec.RoleUser {
		t.Errorf("second message role = %q, want %q", req.Messages[1].Role, codec.RoleUser)
	}
	if req.Messages[1].Content[0].Text != "hello" {
		t.Errorf("user text = %q, want %q", req.Messages[1].Content[0].Text, "hello")
	}
}

func TestDecodeRequest_StringContent(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 512,
		"messages": [
			{"role": "user", "content": "just a string"}
		]
	}`

	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	if len(req.Messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(req.Messages))
	}
	if req.Messages[0].Content[0].Text != "just a string" {
		t.Errorf("text = %q, want %q", req.Messages[0].Content[0].Text, "just a string")
	}
}

func TestDecodeRequest_ToolUseAndToolResult(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-20250514",
		"max_tokens": 1024,
		"messages": [
			{"role": "user", "content": "What is the weather?"},
			{"role": "assistant", "content": [
				{"type": "tool_use", "id": "call_1", "name": "get_weather", "input": {"city": "Paris"}}
			]},
			{"role": "user", "content": [
				{"type": "tool_result", "tool_use_id": "call_1", "content": "Sunny, 25C"}
			]}
		],
		"tools": [{"name": "get_weather", "description": "Get weather", "input_schema": {"type": "object"}}]
	}`

	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest failed: %v", err)
	}

	// Second message (assistant) should have tool calls
	if len(req.Messages[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(req.Messages[1].ToolCalls))
	}
	tc := req.Messages[1].ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "get_weather" {
		t.Errorf("tool call = %+v", tc)
	}

	// Third message should be tool role with tool_call_id
	if req.Messages[2].Role != codec.RoleTool {
		t.Errorf("tool result role = %q, want %q", req.Messages[2].Role, codec.RoleTool)
	}
	if req.Messages[2].ToolCallID != "call_1" {
		t.Errorf("tool_call_id = %q, want %q", req.Messages[2].ToolCallID, "call_1")
	}

	// Tools
	if len(req.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(req.Tools))
	}
	if req.Tools[0].Name != "get_weather" {
		t.Errorf("tool name = %q", req.Tools[0].Name)
	}
}

// ---------------------------------------------------------------------------
// EncodeRequest
// ---------------------------------------------------------------------------

func TestEncodeRequest_URLAndHeaders(t *testing.T) {
	c := &ClaudeCodec{}
	req := &codec.Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Stream:    true,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleSystem, "Be helpful"),
			codec.TextMessage(codec.RoleUser, "hi"),
		},
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-ant-test",
		Model:   "claude-sonnet-4-20250514",
	}

	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	// URL
	if httpReq.URL.String() != "https://api.anthropic.com/v1/messages" {
		t.Errorf("url = %q", httpReq.URL.String())
	}

	// Headers
	if httpReq.Header.Get("x-api-key") != "sk-ant-test" {
		t.Errorf("x-api-key = %q", httpReq.Header.Get("x-api-key"))
	}
	if httpReq.Header.Get("anthropic-version") != "2023-06-01" {
		t.Errorf("anthropic-version = %q", httpReq.Header.Get("anthropic-version"))
	}
	if httpReq.Header.Get("Content-Type") != "application/json" {
		t.Errorf("content-type = %q", httpReq.Header.Get("Content-Type"))
	}

	// Body: system should be top-level, not in messages
	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	if raw["system"] != "Be helpful" {
		t.Errorf("system = %v", raw["system"])
	}

	msgs, ok := raw["messages"].([]any)
	if !ok {
		t.Fatal("messages is not an array")
	}
	// Should have 1 message (user only, system extracted)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message in body, got %d", len(msgs))
	}
}

func TestEncodeRequest_ToolsUseInputSchema(t *testing.T) {
	c := &ClaudeCodec{}
	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}
	req := &codec.Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 1024,
		Messages:  []codec.Message{codec.TextMessage(codec.RoleUser, "weather?")},
		Tools: []codec.Tool{
			{Name: "get_weather", Description: "Get weather", InputSchema: schema},
		},
	}
	cfg := &codec.ChannelConfig{
		BaseURL: "https://api.anthropic.com",
		APIKey:  "sk-ant-test",
		Model:   "claude-sonnet-4-20250514",
	}

	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest failed: %v", err)
	}

	bodyBytes, _ := io.ReadAll(httpReq.Body)
	var raw map[string]any
	json.Unmarshal(bodyBytes, &raw)

	tools, ok := raw["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("tools = %v", raw["tools"])
	}
	tool := tools[0].(map[string]any)
	if tool["name"] != "get_weather" {
		t.Errorf("tool name = %v", tool["name"])
	}
	if tool["input_schema"] == nil {
		t.Error("input_schema should be set")
	}
	// Should NOT have "parameters" key
	if _, exists := tool["parameters"]; exists {
		t.Error("should not have 'parameters' key, should use 'input_schema'")
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse non-stream
// ---------------------------------------------------------------------------

func TestDecodeResponse_NonStream(t *testing.T) {
	respBody := `{
		"id": "msg_1",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "Hello!"}],
		"stop_reason": "end_turn",
		"usage": {
			"input_tokens": 10,
			"output_tokens": 3,
			"cache_read_input_tokens": 2,
			"cache_creation_input_tokens": 1
		}
	}`

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(respBody)),
	}

	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, false)
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: StreamStart, ContentDelta, FinishReason, Usage, Done
	if len(events) < 4 {
		t.Fatalf("expected at least 4 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != codec.EventStreamStart {
		t.Errorf("event[0] type = %v, want StreamStart", events[0].Type)
	}

	if events[1].Type != codec.EventContentDelta || events[1].Delta.Text != "Hello!" {
		t.Errorf("event[1] = %+v", events[1])
	}

	// Find usage event
	var usageEvent *codec.Event
	for i := range events {
		if events[i].Type == codec.EventUsage {
			usageEvent = &events[i]
			break
		}
	}
	if usageEvent == nil {
		t.Fatal("no usage event found")
	}
	if usageEvent.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens = %d, want 10", usageEvent.Usage.PromptTokens)
	}
	if usageEvent.Usage.CompletionTokens != 3 {
		t.Errorf("completion_tokens = %d, want 3", usageEvent.Usage.CompletionTokens)
	}
	if usageEvent.Usage.CacheReadTokens != 2 {
		t.Errorf("cache_read_tokens = %d, want 2", usageEvent.Usage.CacheReadTokens)
	}
	if usageEvent.Usage.CacheWriteTokens != 1 {
		t.Errorf("cache_write_tokens = %d, want 1", usageEvent.Usage.CacheWriteTokens)
	}
	if usageEvent.Usage.TotalTokens != 13 {
		t.Errorf("total_tokens = %d, want 13", usageEvent.Usage.TotalTokens)
	}

	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event type = %v, want Done", events[len(events)-1].Type)
	}
}

// ---------------------------------------------------------------------------
// DecodeResponse stream
// ---------------------------------------------------------------------------

func TestDecodeResponse_Stream(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Expect: StreamStart, ContentDelta("Hello"), ContentDelta(" world"),
	//         FinishReason, Usage, Done
	if len(events) < 5 {
		t.Fatalf("expected at least 5 events, got %d: %+v", len(events), events)
	}

	if events[0].Type != codec.EventStreamStart {
		t.Errorf("event[0] type = %v, want StreamStart", events[0].Type)
	}

	// Find text deltas
	var texts []string
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil {
			texts = append(texts, ev.Delta.Text)
		}
	}
	if len(texts) != 2 || texts[0] != "Hello" || texts[1] != " world" {
		t.Errorf("text deltas = %v, want [Hello, ' world']", texts)
	}

	// Find usage event - should merge input from message_start and output from message_delta
	var usageEvent *codec.Event
	for i := range events {
		if events[i].Type == codec.EventUsage {
			usageEvent = &events[i]
			break
		}
	}
	if usageEvent == nil {
		t.Fatal("no usage event found")
	}
	if usageEvent.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens = %d, want 10", usageEvent.Usage.PromptTokens)
	}
	if usageEvent.Usage.CompletionTokens != 5 {
		t.Errorf("completion_tokens = %d, want 5", usageEvent.Usage.CompletionTokens)
	}
	if usageEvent.Usage.CacheReadTokens != 5 {
		t.Errorf("cache_read_tokens = %d, want 5", usageEvent.Usage.CacheReadTokens)
	}
	if usageEvent.Usage.CacheWriteTokens != 3 {
		t.Errorf("cache_write_tokens = %d, want 3", usageEvent.Usage.CacheWriteTokens)
	}
	if usageEvent.Usage.TotalTokens != 15 {
		t.Errorf("total_tokens = %d, want 15", usageEvent.Usage.TotalTokens)
	}

	if events[len(events)-1].Type != codec.EventDone {
		t.Errorf("last event type = %v, want Done", events[len(events)-1].Type)
	}
}

func TestDecodeResponse_Stream_CumulativeUsage(t *testing.T) {
	// Test that cumulative token values from message_delta override message_start values
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":10,"output_tokens":0,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":5,"cache_creation_input_tokens":3}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	var usageEvent *codec.Event
	for i := range events {
		if events[i].Type == codec.EventUsage {
			usageEvent = &events[i]
			break
		}
	}
	if usageEvent == nil {
		t.Fatal("no usage event found")
	}
	if usageEvent.Usage.PromptTokens != 10 {
		t.Errorf("prompt_tokens = %d, want 10", usageEvent.Usage.PromptTokens)
	}
	if usageEvent.Usage.CompletionTokens != 5 {
		t.Errorf("completion_tokens = %d, want 5", usageEvent.Usage.CompletionTokens)
	}
	if usageEvent.Usage.CacheReadTokens != 5 {
		t.Errorf("cache_read_tokens = %d, want 5", usageEvent.Usage.CacheReadTokens)
	}
	if usageEvent.Usage.CacheWriteTokens != 3 {
		t.Errorf("cache_write_tokens = %d, want 3", usageEvent.Usage.CacheWriteTokens)
	}
}

func TestDecodeResponse_Stream_ToolUse(t *testing.T) {
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_2","model":"claude-sonnet-4-20250514","usage":{"input_tokens":8,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call_1","name":"get_weather"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"Paris\"}"}}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":12}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	resp := &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(sse)),
	}

	c := &ClaudeCodec{}
	ch, err := c.DecodeResponse(resp, true)
	if err != nil {
		t.Fatalf("DecodeResponse failed: %v", err)
	}

	var events []codec.Event
	for ev := range ch {
		events = append(events, ev)
	}

	// Should have Start + ArgsDelta(s) + End for the tool_use block
	var startEvents []codec.Event
	var argDeltas []codec.Event
	var endEvents []codec.Event
	for _, ev := range events {
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
		t.Fatalf("expected at least 1 EventToolCallStart, got %d", len(startEvents))
	}
	if len(argDeltas) < 2 {
		t.Fatalf("expected at least 2 EventToolCallArgumentsDelta, got %d", len(argDeltas))
	}
	// Note: this fixture has no content_block_stop, so EventToolCallEnd is not emitted.
	_ = endEvents

	if startEvents[0].ToolCall == nil || startEvents[0].ToolCall.CallID != "call_1" {
		t.Errorf("Start CallID = %v", startEvents[0].ToolCall)
	}
	if startEvents[0].ToolCall == nil || startEvents[0].ToolCall.Name != "get_weather" {
		t.Errorf("Start Name = %v", startEvents[0].ToolCall)
	}

	// Check finish reason mapped to tool_calls
	var finishReason string
	for _, ev := range events {
		if ev.FinishReason != "" {
			finishReason = ev.FinishReason
		}
	}
	if finishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want %q", finishReason, "tool_calls")
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse stream
// ---------------------------------------------------------------------------

func TestEncodeResponse_Stream(t *testing.T) {
	events := make(chan codec.Event, 10)
	events <- codec.Event{Type: codec.EventStreamStart}
	events <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hello",
		},
	}
	events <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}
	events <- codec.Event{Type: codec.EventDone, FinishReason: "stop"}
	close(events)

	w := httptest.NewRecorder()
	// httptest.ResponseRecorder implements Flusher
	c := &ClaudeCodec{}
	err := c.EncodeResponse(events, w, true)
	if err != nil {
		t.Fatalf("EncodeResponse stream failed: %v", err)
	}

	body := w.Body.String()

	// Verify SSE format with event: lines
	if !strings.Contains(body, "event: message_start\n") {
		t.Error("missing event: message_start")
	}
	if !strings.Contains(body, "event: content_block_start\n") {
		t.Error("missing event: content_block_start")
	}
	if !strings.Contains(body, "event: content_block_delta\n") {
		t.Error("missing event: content_block_delta")
	}
	if !strings.Contains(body, "event: message_delta\n") {
		t.Error("missing event: message_delta")
	}
	if !strings.Contains(body, "event: message_stop\n") {
		t.Error("missing event: message_stop")
	}
	if !strings.Contains(body, "text_delta") {
		t.Error("missing text_delta in content")
	}
	if !strings.Contains(body, "Hello") {
		t.Error("missing text content 'Hello'")
	}

	// Content-Type should be text/event-stream
	if ct := w.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}
}

// ---------------------------------------------------------------------------
// EncodeResponse non-stream
// ---------------------------------------------------------------------------

func TestEncodeResponse_NonStream(t *testing.T) {
	events := make(chan codec.Event, 10)
	events <- codec.Event{Type: codec.EventStreamStart}
	events <- codec.Event{
		Type: codec.EventContentDelta,
		Delta: &codec.DeltaPayload{
			ContentType: codec.ContentTypeText,
			Text:        "Hello!",
		},
	}
	events <- codec.Event{
		Type: codec.EventUsage,
		Usage: &codec.Usage{
			PromptTokens:     10,
			CompletionTokens: 3,
			TotalTokens:      13,
		},
	}
	events <- codec.Event{Type: codec.EventDone}
	close(events)

	w := httptest.NewRecorder()
	c := &ClaudeCodec{}
	err := c.EncodeResponse(events, w, false)
	if err != nil {
		t.Fatalf("EncodeResponse non-stream failed: %v", err)
	}

	var resp claudeResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if resp.Type != "message" {
		t.Errorf("type = %q, want message", resp.Type)
	}
	if resp.Role != "assistant" {
		t.Errorf("role = %q, want assistant", resp.Role)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "Hello!" {
		t.Errorf("content = %+v", resp.Content)
	}
	if resp.Usage == nil || resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 3 {
		t.Errorf("usage = %+v", resp.Usage)
	}
}

// ---------------------------------------------------------------------------
// EncodeError
// ---------------------------------------------------------------------------

func TestEncodeError(t *testing.T) {
	w := httptest.NewRecorder()
	c := &ClaudeCodec{}
	c.EncodeError(w, http.StatusInternalServerError, errors.New("something went wrong"))

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}

	var resp claudeErrorResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if resp.Type != "error" {
		t.Errorf("type = %q, want error", resp.Type)
	}
	if resp.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", resp.Error.Type)
	}
	if resp.Error.Message != "something went wrong" {
		t.Errorf("error.message = %q", resp.Error.Message)
	}
}

// ---------------------------------------------------------------------------
// Interface compliance
// ---------------------------------------------------------------------------

func TestInterfaceCompliance(t *testing.T) {
	var _ codec.InboundCodec = (*ClaudeCodec)(nil)
	var _ codec.OutboundCodec = (*ClaudeCodec)(nil)
}

// ---------------------------------------------------------------------------
// RawJSON content block preservation tests
// ---------------------------------------------------------------------------

func TestClaudeDecodeRequest_UnknownContentBlockType_PreservedAsRawJSON(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-20250514",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hello"},
				{"type": "document", "source": {"type": "url", "url": "https://example.com/doc.pdf"}}
			]}
		],
		"max_tokens": 100
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ClaudeCodec{}
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
	if raw["type"] != "document" {
		t.Errorf("RawJSON type = %v, want document", raw["type"])
	}
}

func TestClaudeEncodeRequest_UnknownContentBlock_RoundTrip(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-20250514",
		"messages": [
			{"role": "user", "content": [
				{"type": "text", "text": "hello"},
				{"type": "document", "source": {"type": "url", "url": "https://example.com/doc.pdf"}}
			]}
		],
		"max_tokens": 100
	}`
	r := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")

	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(r)
	if err != nil {
		t.Fatalf("DecodeRequest: %v", err)
	}

	cfg := &codec.ChannelConfig{BaseURL: "https://api.anthropic.com", APIKey: "k", Model: "claude-sonnet-4-20250514"}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}

	outBody, _ := io.ReadAll(httpReq.Body)
	var out map[string]any
	json.Unmarshal(outBody, &out)

	msgs := out["messages"].([]any)
	msg := msgs[0].(map[string]any)
	content := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content len = %d, want 2", len(content))
	}
	unknown := content[1].(map[string]any)
	if unknown["type"] != "document" {
		t.Errorf("type = %v, want document", unknown["type"])
	}
}

func TestClaudeEncodeRequest_SkipsRawJSONMessages(t *testing.T) {
	req := &codec.Request{
		Model:     "claude-sonnet-4-20250514",
		MaxTokens: 100,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "hello"),
			{RawJSON: json.RawMessage(`{"type":"unknown"}`)},
		},
	}

	c := &ClaudeCodec{}
	cfg := &codec.ChannelConfig{BaseURL: "https://api.anthropic.com", APIKey: "k", Model: "claude-sonnet-4-20250514"}
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

func TestClaudeEncodeRequest_CustomEndpointPath(t *testing.T) {
	c := &ClaudeCodec{}
	req := &codec.Request{
		Model:     "claude-sonnet-4-20250514",
		Stream:    false,
		MaxTokens: 100,
		Messages: []codec.Message{
			codec.TextMessage(codec.RoleUser, "hello"),
		},
	}
	cfg := &codec.ChannelConfig{
		BaseURL:      "https://custom.api.com",
		APIKey:       "sk-test",
		Model:        "claude-sonnet-4-20250514",
		EndpointPath: "/custom/messages",
	}
	httpReq, err := c.EncodeRequest(req, cfg)
	if err != nil {
		t.Fatalf("EncodeRequest: %v", err)
	}
	want := "https://custom.api.com/custom/messages"
	if httpReq.URL.String() != want {
		t.Errorf("URL = %q, want %q", httpReq.URL.String(), want)
	}
}

func TestClaudeEncodeRequest_BetaQuery(t *testing.T) {
	tests := []struct {
		name         string
		endpointPath string
		beta         bool
		want         string
	}{
		{name: "disabled", beta: false, want: "https://api.anthropic.com/v1/messages"},
		{name: "enabled", beta: true, want: "https://api.anthropic.com/v1/messages?beta=true"},
		{name: "preserves existing query", endpointPath: "/custom/messages?region=us", beta: true, want: "https://api.anthropic.com/custom/messages?beta=true&region=us"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &ClaudeCodec{}
			req := &codec.Request{Model: "claude", Messages: []codec.Message{codec.TextMessage(codec.RoleUser, "hello")}}
			cfg := &codec.ChannelConfig{
				BaseURL:         "https://api.anthropic.com",
				APIKey:          "sk-test",
				Model:           "claude",
				EndpointPath:    tt.endpointPath,
				ClaudeBetaQuery: tt.beta,
			}
			httpReq, err := c.EncodeRequest(req, cfg)
			if err != nil {
				t.Fatal(err)
			}
			if got := httpReq.URL.String(); got != tt.want {
				t.Fatalf("URL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeRequestFieldsRoundTrip(t *testing.T) {
	inbound, err := http.NewRequest(http.MethodPost, "http://client/v1/messages", strings.NewReader(`{
		"model":"claude","max_tokens":128,"messages":[],
		"service_tier":"auto","inference_geo":"us"
	}`))
	if err != nil {
		t.Fatal(err)
	}
	c := &ClaudeCodec{}
	req, err := c.DecodeRequest(inbound)
	if err != nil {
		t.Fatal(err)
	}
	if req.ServiceTier != "auto" || req.InferenceGeo != "us" {
		t.Fatalf("request fields were not decoded: %#v", req)
	}

	outbound, err := c.EncodeRequest(req, &codec.ChannelConfig{BaseURL: "https://api.anthropic.com", APIKey: "k", Model: req.Model})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(outbound.Body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["service_tier"] != "auto" || decoded["inference_geo"] != "us" {
		t.Fatalf("request fields were not encoded: %#v", decoded)
	}
}

// Suppress unused import warnings
var _ = bytes.NewReader
var _ = fmt.Sprintf
