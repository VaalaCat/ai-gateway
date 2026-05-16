package codec_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestResponses2Claude_SimpleText(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hello"}],"stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolClaude, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := msgs[len(msgs)-1].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestResponses2Claude_Instructions(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"instructions":"Be helpful","stream":false}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolClaude, body)

	// Responses "instructions" → IR system message → Claude top-level "system"
	sysRaw, ok := result["system"]
	if !ok {
		t.Fatal("system field missing from Claude output")
	}
	var sysStr string
	b, _ := json.Marshal(sysRaw)
	if err := json.Unmarshal(b, &sysStr); err != nil {
		t.Fatalf("system field is not a string: %v", err)
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

func TestResponses2Claude_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/stream_text.sse")
}

func TestResponses2Claude_StreamDashScope(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_responses/stream_dashscope_text.txt",
		outCodec, inCodec, true, true)

	// Layer 1: Event verification - DashScope text deltas
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "{\""},
		{Type: codec.EventContentDelta, Text: "title"},
		{Type: codec.EventContentDelta, Text: "\":"},
		{Type: codec.EventContentDelta, Text: " \""},
		{Type: codec.EventContentDelta, Text: "Hello\"}"},
		// Note: empty delta is skipped by decoder
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	// Layer 2: SSE format compliance
	assertSSEFormat(t, sse, codec.ProtocolClaude)

	// Layer 3: Golden file
	assertGoldenSSE(t, sse, "responses2claude/stream_dashscope_text.sse")
}

func TestResponses2Claude_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/stream_tool_call.sse")
}

func TestResponses2Claude_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/stream_multi_tool.sse")
}

func TestResponses2Claude_StreamThinkingText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_responses/stream_reasoning_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Why did the chicken cross the road?"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/stream_thinking_text.sse")
}

func TestResponses2Claude_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"incomplete", "openai_responses/stream_incomplete.txt", "responses2claude/stream_incomplete.sse", "length"},
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

func TestResponses2Claude_StreamFailed(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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

func TestResponses2Claude_StreamIncomplete(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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
	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/stream_incomplete.sse")
}

func TestResponses2Claude_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

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
	assertSSEFormat(t, sse, codec.ProtocolClaude)
	assertGoldenSSE(t, sse, "responses2claude/nonstream_to_stream.sse")
}

func TestResponses2Claude_StreamTextNoTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolOpenAIResponses)
	inCodec := codec.GetInbound(codec.ProtocolClaude)

	events, sse := roundTripStream(t,
		"openai_responses/stream_text_no_tool.txt",
		outCodec, inCodec, true, true)

	// Layer 1: Event verification - text only, no tool calls
	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "系统运行"},
		{Type: codec.EventContentDelta, Text: "正常，"},
		{Type: codec.EventContentDelta, Text: "无异常。"},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	// Layer 2: SSE format compliance
	assertSSEFormat(t, sse, codec.ProtocolClaude)

	// Layer 3: Golden file
	assertGoldenSSE(t, sse, "responses2claude/stream_text_no_tool.sse")
}

func TestResponses2Claude_ToolChoice(t *testing.T) {
	body := `{"model":"gpt-4o","input":[{"role":"user","content":"Hi"}],"stream":false,"tools":[{"type":"function","name":"test","description":"test","parameters":{}}],"tool_choice":"required"}`
	result := roundTripRequest(t, codec.ProtocolOpenAIResponses, codec.ProtocolClaude, body)

	// Responses "required" → IR "required" → Claude {type:"any"}
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
