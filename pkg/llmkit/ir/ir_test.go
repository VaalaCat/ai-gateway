package ir

import (
	"bytes"
	"encoding/json"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

func TestRequest_NewFields_JSONRoundTrip(t *testing.T) {
	store := true
	parallel := true
	req := Request{
		Model:             "gpt-5",
		ToolChoice:        &ToolChoice{Type: "auto"},
		ParallelToolCalls: &parallel,
		ReasoningEffort:   "xhigh",
		Store:             &store,
		Extras: map[string]any{
			"include":          []any{"reasoning.encrypted_content"},
			"prompt_cache_key": "abc123",
		},
		Tools: []Tool{
			{Name: "exec_command", Description: "Run cmd", InputSchema: map[string]any{"type": "object"}, Type: "function", Strict: boolPtr(false)},
			{Name: "web_search", Type: "web_search", RawConfig: map[string]any{"external_web_access": false}},
			{Name: "apply_patch", Type: "custom", Description: "Edit files", RawConfig: map[string]any{"format": map[string]any{"type": "grammar"}}},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Request
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.ToolChoice == nil || decoded.ToolChoice.Type != "auto" {
		t.Error("ToolChoice lost or wrong after roundtrip")
	}
	if decoded.ParallelToolCalls == nil || !*decoded.ParallelToolCalls {
		t.Error("ParallelToolCalls lost after roundtrip")
	}
	if decoded.ReasoningEffort != "xhigh" {
		t.Errorf("ReasoningEffort = %q, want xhigh", decoded.ReasoningEffort)
	}
	if decoded.Store == nil || !*decoded.Store {
		t.Error("Store lost after roundtrip")
	}
	if decoded.Extras["prompt_cache_key"] != "abc123" {
		t.Error("Extras lost after roundtrip")
	}
	if len(decoded.Tools) != 3 {
		t.Fatalf("Tools len = %d, want 3", len(decoded.Tools))
	}
	if decoded.Tools[1].Type != "web_search" {
		t.Errorf("Tools[1].Type = %q, want web_search", decoded.Tools[1].Type)
	}
	if decoded.Tools[2].RawConfig == nil {
		t.Error("Tools[2].RawConfig lost after roundtrip")
	}
}

func TestTextMessage(t *testing.T) {
	msg := TextMessage(RoleUser, "hello world")

	if msg.Role != RoleUser {
		t.Errorf("expected role %q, got %q", RoleUser, msg.Role)
	}
	if len(msg.Content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(msg.Content))
	}
	if msg.Content[0].Type != ContentTypeText {
		t.Errorf("expected content type %q, got %q", ContentTypeText, msg.Content[0].Type)
	}
	if msg.Content[0].Text != "hello world" {
		t.Errorf("expected text %q, got %q", "hello world", msg.Content[0].Text)
	}
	if len(msg.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(msg.ToolCalls))
	}
	if msg.ToolCallID != "" {
		t.Errorf("expected empty tool call ID, got %q", msg.ToolCallID)
	}
}

func TestEventToolCallStreamingTypes(t *testing.T) {
	// 三个新 event type 必须互不相同且与既有事件不冲突
	seen := map[EventType]string{
		EventStreamStart:            "EventStreamStart",
		EventContentDelta:           "EventContentDelta",
		EventToolCallDelta:          "EventToolCallDelta",
		EventThinkingDelta:          "EventThinkingDelta",
		EventUsage:                  "EventUsage",
		EventDone:                   "EventDone",
		EventError:                  "EventError",
		EventRawPassthrough:         "EventRawPassthrough",
		EventContentBlockStop:       "EventContentBlockStop",
		EventSignatureDelta:         "EventSignatureDelta",
		EventToolCallStart:          "EventToolCallStart",
		EventToolCallArgumentsDelta: "EventToolCallArgumentsDelta",
		EventToolCallEnd:            "EventToolCallEnd",
	}
	if len(seen) != 13 {
		t.Errorf("expected 13 unique EventType values, got %d", len(seen))
	}
}

func TestStreamingToolCallStruct(t *testing.T) {
	tc := StreamingToolCall{
		CallID:    "call_x",
		Index:     0,
		Name:      "exec",
		Arguments: `{"a":1}`,
	}
	if tc.CallID != "call_x" {
		t.Errorf("CallID = %q, want call_x", tc.CallID)
	}
}

func TestEventToolCallField(t *testing.T) {
	ev := Event{
		Type:     EventToolCallStart,
		ToolCall: &StreamingToolCall{CallID: "call_x", Name: "exec"},
	}
	if ev.ToolCall == nil {
		t.Fatal("ToolCall is nil")
	}
	if ev.ToolCall.CallID != "call_x" {
		t.Errorf("ToolCall.CallID = %q, want call_x", ev.ToolCall.CallID)
	}
}

func TestRequestInboundProtocolSerialization(t *testing.T) {
	req := Request{
		Model:           "gpt-5",
		Messages:        []Message{},
		InboundProtocol: ProtocolOpenAIResponses,
	}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(data, []byte(`"inbound_protocol":"openai_responses"`)) {
		t.Errorf("expected inbound_protocol in JSON, got: %s", data)
	}

	// 零值应被 omitempty 省略
	req2 := Request{Model: "x", Messages: []Message{}}
	data2, _ := json.Marshal(req2)
	if bytes.Contains(data2, []byte(`"inbound_protocol"`)) {
		t.Errorf("expected inbound_protocol omitted when empty, got: %s", data2)
	}
}

func TestRequestZeroValue(t *testing.T) {
	var req Request
	if req.Model != "" || req.Stream || len(req.Messages) != 0 {
		t.Fatalf("unexpected zero value: %#v", req)
	}
}

func TestEventErrorAllowsNilPayload(t *testing.T) {
	event := Event{Type: EventError}
	if event.Type != EventError {
		t.Fatalf("unexpected event type: %v", event.Type)
	}
}
