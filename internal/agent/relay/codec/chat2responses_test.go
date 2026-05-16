package codec_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestChat2Responses_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hello"}],"stream":false,"max_tokens":1024}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, body)

	input := mustGetArray(t, result, "input")
	if len(input) == 0 {
		t.Fatal("expected at least one input item")
	}
	msg := input[len(input)-1].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestChat2Responses_SystemPrompt(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"system","content":"Be helpful"},{"role":"user","content":"Hi"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, body)

	// OpenAI Chat system message should become Responses "instructions" field
	instr, ok := result["instructions"]
	if !ok {
		t.Fatal("instructions field missing from Responses output")
	}
	if instr != "Be helpful" {
		t.Errorf("instructions = %v, want 'Be helpful'", instr)
	}

	// input should not contain a system message
	input := mustGetArray(t, result, "input")
	for i, m := range input {
		msg := m.(map[string]any)
		if msg["role"] == "system" {
			t.Errorf("input[%d] has role=system; system should be in instructions", i)
		}
	}
}

func TestChat2Responses_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "chat2responses/stream_text.sse")
}

func TestChat2Responses_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "chat2responses/stream_tool_call.sse")
}

func TestChat2Responses_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

	events, sse := roundTripStream(t,
		"openai_chat/stream_multi_tool.txt",
		outCodec, inCodec, true, true)

	// Updated for Task 4: EventToolCallDelta is filtered; use new event types.
	// Note: ToolCallEnd events at finish_reason come from map iteration (non-deterministic order),
	// so we only assert the stable prefix and suffix.
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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "chat2responses/stream_multi_tool.sse")
}

func TestChat2Responses_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"max_tokens", "openai_chat/stream_max_tokens.txt", "chat2responses/stream_max_tokens.sse", "length"},
		{"content_filter", "openai_chat/stream_content_filter.txt", "chat2responses/stream_content_filter.sse", "content_filter"},
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
			assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
			assertGoldenSSE(t, sse, tt.golden)
		})
	}
}

func TestChat2Responses_StreamRefusal(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "chat2responses/stream_refusal.sse")
}

func TestChat2Responses_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIChat)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "chat2responses/nonstream_to_stream.sse")
}

func TestChat2Responses_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","function":{"name":"test","description":"test","parameters":{}}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIChat, codec.ProtocolOpenAIResponses, body)

	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from Responses output")
	}
	if tc != "required" {
		t.Errorf("tool_choice = %v, want required", tc)
	}
}
