package codec_test

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestResponses2Responses_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hello"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIResponses, body)

	input := mustGetArray(t, result, "input")
	if len(input) == 0 {
		t.Fatal("expected at least one input item")
	}
	msg := input[len(input)-1].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestResponses2Responses_Instructions(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"instructions":"Be helpful","stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIResponses, body)

	instr, ok := result["instructions"]
	if !ok {
		t.Fatal("instructions field missing from round-tripped output")
	}
	if instr != "Be helpful" {
		t.Errorf("instructions = %v, want 'Be helpful'", instr)
	}
}

func TestResponses2Responses_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/stream_text.sse")
}

func TestResponses2Responses_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/stream_tool_call.sse")
}

func TestResponses2Responses_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/stream_multi_tool.sse")
}

func TestResponses2Responses_StreamThinkingText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

	events, sse := roundTripStream(t,
		"openai_responses/stream_reasoning_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Why did the chicken cross the road?"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/stream_thinking_text.sse")
}

func TestResponses2Responses_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"incomplete", "openai_responses/stream_incomplete.txt", "responses2responses/stream_incomplete.sse", "length"},
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

func TestResponses2Responses_StreamFailed(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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

func TestResponses2Responses_StreamIncomplete(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/stream_incomplete.sse")
}

func TestResponses2Responses_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIResponses)

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
	assertSSEFormat(t, sse, codec.ProtocolOpenAIResponses)
	assertGoldenSSE(t, sse, "responses2responses/nonstream_to_stream.sse")
}

func TestResponses2Responses_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","name":"test","description":"test","parameters":{}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolOpenAIResponses, body)

	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from output")
	}
	if tc != "required" {
		t.Errorf("tool_choice = %v, want required", tc)
	}
}
