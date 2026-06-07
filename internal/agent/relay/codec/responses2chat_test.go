package codec_test

import (
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestResponses2Chat_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hello"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIChat, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := msgs[len(msgs)-1].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestResponses2Chat_Instructions(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"instructions":"Be helpful","stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIChat, body)

	// Responses "instructions" → OpenAI Chat {role:"system"} message
	msgs := mustGetArray(t, result, "messages")
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (system + user), got %d", len(msgs))
	}
	sys := msgs[0].(map[string]any)
	if sys["role"] != "system" {
		t.Errorf("first message role = %v, want system", sys["role"])
	}
	if sys["content"] != "Be helpful" {
		t.Errorf("system content = %v, want 'Be helpful'", sys["content"])
	}
}

func TestResponses2Chat_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Hello"},
		{Type: codec.EventContentDelta, Text: " world"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_text.sse")
}

// TestResponses2Chat_StreamNoEventLines covers an upstream that streams Responses
// API events as `data: {"type":...}` lines WITHOUT SSE `event:` lines (the event
// type lives only in the JSON payload). The decoder must fall back to the payload
// `type` field; otherwise every event is misclassified as a raw passthrough and the
// content never reaches the chat client (see Clouder Pro regression: empty reply,
// completion_tokens=0).
func TestResponses2Chat_StreamNoEventLines(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_no_event_lines.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Hello"},
		{Type: codec.EventContentDelta, Text: " world"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_no_event_lines.sse")
}

// TestResponses2Chat_StreamDummyThenNoEvent reproduces the exact Clouder Pro shape:
// a leading non-standard `chatcmpl-dummy` chat.completion.chunk followed by
// event-line-less Responses events. The dummy chunk has no payload `type`, so it
// stays an unrecognized passthrough and must be dropped by the chat encoder, while
// the real content + usage + finish still reach the client.
func TestResponses2Chat_StreamDummyThenNoEvent(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_dummy_then_no_event.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Hello"},
		{Type: codec.EventContentDelta, Text: " world"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	// The upstream's junk framing must NOT leak into the chat client stream.
	if strings.Contains(sse, "chatcmpl-dummy") {
		t.Errorf("upstream dummy chunk leaked into chat output:\n%s", sse)
	}
	if strings.Contains(sse, "response.created") || strings.Contains(sse, "output_text.delta") {
		t.Errorf("raw Responses event leaked into chat output:\n%s", sse)
	}

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_dummy_then_no_event.sse")
}

func TestResponses2Chat_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_tool_call.txt",
		outCodec, inCodec, true, true)

	// Task 8: responses decoder now emits Start/ArgsDelta/End for function_call items.
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_tool_call.sse")
}

func TestResponses2Chat_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_multi_tool.txt",
		outCodec, inCodec, true, true)

	// Task 8: responses decoder now emits Start/ArgsDelta/End for function_call items.
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventToolCallStart, ToolName: "get_weather"},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_multi_tool.sse")
}

func TestResponses2Chat_StreamThinkingText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_reasoning_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Why did the chicken cross the road?"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_thinking_text.sse")
}

func TestResponses2Chat_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"incomplete", "openai_responses/stream_incomplete.txt", "responses2chat/stream_incomplete.sse", "length"},
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

func TestResponses2Chat_StreamFailed(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, _ := roundTripStream(t,
		"openai_responses/stream_failed.txt",
		outCodec, inCodec, true, true)

	var hasError bool
	for _, ev := range events {
		if ev.Type == codec.EventError {
			hasError = true
		}
	}
	if !hasError {
		t.Error("expected error event from failed response fixture")
	}
}

func TestResponses2Chat_StreamIncomplete(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/stream_incomplete.txt",
		outCodec, inCodec, true, true)

	var gotReason string
	for _, ev := range events {
		if ev.FinishReason != "" {
			gotReason = ev.FinishReason
		}
	}
	if gotReason != "length" {
		t.Errorf("expected finish_reason 'length' for incomplete, got %q", gotReason)
	}
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "responses2chat/stream_incomplete.sse")
}

func TestResponses2Chat_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"openai_responses/response_text.json",
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
	assertGoldenSSE(t, sse, "responses2chat/nonstream_to_stream.sse")
}

func TestResponses2Chat_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","name":"test","description":"test","parameters":{}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIChat, body)

	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from Chat output")
	}
	if tc != "required" {
		t.Errorf("tool_choice = %v, want required", tc)
	}
}
