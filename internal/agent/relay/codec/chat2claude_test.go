package codec_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestChat2Claude_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":false,"max_tokens":1024}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolClaude, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := msgs[0].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestChat2Claude_SystemPrompt(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hi"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolClaude, body)

	// OpenAI system message should become Claude top-level "system" field
	sysRaw, ok := result["system"]
	if !ok {
		t.Fatal("system field missing from Claude output")
	}
	// Claude system can be a JSON string
	var sysStr string
	b, _ := json.Marshal(sysRaw)
	if err := json.Unmarshal(b, &sysStr); err != nil {
		t.Fatalf("system field is not a string: %v (raw: %v)", err, sysRaw)
	}
	if sysStr != "Be helpful" {
		t.Errorf("system = %q, want 'Be helpful'", sysStr)
	}

	// Messages should not contain a system message
	msgs := mustGetArray(t, result, "messages")
	for i, m := range msgs {
		msg := m.(map[string]any)
		if msg["role"] == "system" {
			t.Errorf("messages[%d] has role=system; system should be top-level field in Claude", i)
		}
	}
}

func TestChat2Claude_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_chat/stream_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta}, // model/created metadata chunk
		{Type: codec.EventContentDelta, Text: "Hello"},
		{Type: codec.EventContentDelta, Text: " world"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "chat2claude/stream_text.sse")
}

func TestChat2Claude_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_chat/stream_tool_calls.txt",
		outCodec, inCodec, true, true)

	// Updated for Task 4: EventToolCallDelta is filtered; use new event types.
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta, ToolArgs: "{\"city\":"},
		{Type: codec.EventToolCallArgumentsDelta, ToolArgs: "\"Tokyo\"}"},
		{Type: codec.EventToolCallEnd, ToolArgs: "{\"city\":\"Tokyo\"}"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "chat2claude/stream_tool_call.sse")
}

func TestChat2Claude_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_chat/stream_multi_tool.txt",
		outCodec, inCodec, true, true)

	// Updated for Task 4: EventToolCallDelta is filtered; use new event types.
	// Note: ToolCallEnd events at finish_reason come from map iteration (non-deterministic order).
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta, ToolArgs: "{\"city\":\"Tokyo\"}"},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta, ToolArgs: "{\"city\":\"London\"}"},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "chat2claude/stream_multi_tool.sse")
}

func TestChat2Claude_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"max_tokens", "openai_chat/stream_max_tokens.txt", "chat2claude/stream_max_tokens.sse", "length"},
		{"content_filter", "openai_chat/stream_content_filter.txt", "chat2claude/stream_content_filter.sse", "content_filter"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events, sse := roundTripStream(t, tt.fixture, outCodec, inCodec, true, true)
			var gotReason string
			for _, ev := range events {
				if ev.FinishReason != "" {
					gotReason = ev.FinishReason
				}
			}
			if gotReason != tt.reason {
				t.Errorf("finish_reason: got %q, want %q", gotReason, tt.reason)
			}
			assertSSEFormat(t, sse, codec.ProtocolClaude)
			assertGoldenSSE(t, sse, tt.golden)
		})
	}
}

func TestChat2Claude_StreamRefusal(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_chat/stream_refusal.txt",
		outCodec, inCodec, true, true)

	var hasRefusal bool
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Refusal != "" {
			hasRefusal = true
		}
	}
	if !hasRefusal {
		t.Error("expected refusal content in events")
	}
	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "chat2claude/stream_refusal.sse")
}

func TestChat2Claude_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_chat/response_text.json",
		outCodec, inCodec, false, true)

	if len(events) == 0 {
		t.Fatal("no events from non-stream decode")
	}
	var hasContent, hasDone bool
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta {
			hasContent = true
		}
		if ev.Type == codec.EventDone {
			hasDone = true
		}
	}
	if !hasContent {
		t.Error("expected content delta events")
	}
	if !hasDone {
		t.Error("expected done event")
	}
	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "chat2claude/nonstream_to_stream.sse")
}

func TestChat2Claude_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","function":{"name":"test","description":"test","parameters":{}}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolClaude, body)

	// OpenAI "required" → Claude tool_choice {type: "any"}
	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from Claude output")
	}
	tcMap, ok := tc.(map[string]any)
	if !ok {
		t.Fatalf("tool_choice should be an object, got %T", tc)
	}
	if tcMap["type"] != "any" {
		t.Errorf("tool_choice.type = %v, want 'any'", tcMap["type"])
	}
}
