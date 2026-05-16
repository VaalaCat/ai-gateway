package codec_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestChat2Chat_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":false,"max_tokens":1024}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "user" {
		t.Errorf("role = %v, want user", last["role"])
	}
	if last["content"] != "Hello" {
		t.Errorf("content = %v, want Hello", last["content"])
	}
}

func TestChat2Chat_SystemPrompt(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hi"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("first message role = %v, want system", sys["role"])
	}
	if sys["content"] != "Be helpful" {
		t.Errorf("system content = %v, want 'Be helpful'", sys["content"])
	}
}

func TestChat2Chat_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "chat2chat/stream_text.sse")
}

func TestChat2Chat_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "chat2chat/stream_tool_call.sse")
}

func TestChat2Chat_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "chat2chat/stream_multi_tool.sse")
}

func TestChat2Chat_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"max_tokens", "openai_chat/stream_max_tokens.txt", "chat2chat/stream_max_tokens.sse", "length"},
		{"content_filter", "openai_chat/stream_content_filter.txt", "chat2chat/stream_content_filter.sse", "content_filter"},
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
			assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
			assertGoldenSSE(t, sse, tt.golden)
		})
	}
}

func TestChat2Chat_StreamRefusal(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "chat2chat/stream_refusal.sse")
}

func TestChat2Chat_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "chat2chat/nonstream_to_stream.sse")
}

func TestChat2Chat_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","function":{"name":"test","description":"test","parameters":{}}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIChat, body)

	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from output")
	}
	if tc != "required" {
		t.Errorf("tool_choice = %v, want required", tc)
	}
}
