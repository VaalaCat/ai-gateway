package codec_test

import (
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

func TestClaude2Chat_SimpleText(t *testing.T) {
	body := `{"model":"claude-sonnet-4","max_tokens":1024,"messages":[{"role":"user","content":"Hello"}]}`
	result := roundTripRequest(t, codec.ProtocolClaude, codec.ProtocolOpenAIChat, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	msg := msgs[len(msgs)-1].(map[string]any)
	if msg["role"] != "user" {
		t.Errorf("role = %v, want user", msg["role"])
	}
}

func TestClaude2Chat_SystemPrompt(t *testing.T) {
	body := `{"model":"claude-sonnet-4","max_tokens":1024,"system":"Be helpful","messages":[{"role":"user","content":"Hi"}]}`
	result := roundTripRequest(t, codec.ProtocolClaude, codec.ProtocolOpenAIChat, body)

	// Claude top-level "system" → OpenAI {role:"system"} message
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

func TestClaude2Chat_StreamText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Hello"},
		{Type: codec.EventContentDelta, Text: " world"},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_text.sse")
}

func TestClaude2Chat_StreamToolCall(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_tool_call.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "Let me check."},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventToolCallStart},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_tool_call.sse")
}

func TestClaude2Chat_StreamMultiTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_multi_tool.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventContentDelta, Text: "I'll check both."},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventToolCallStart},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventToolCallStart},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_multi_tool.sse")
}

func TestClaude2Chat_StreamThinkingText(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_thinking_text.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventThinkingDelta, Text: "Let me analyze this step by step."},
		{Type: codec.EventSignatureDelta, Signature: "sig_abc123"},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventContentDelta, Text: "The answer is 42."},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_thinking_text.sse")
}

func TestClaude2Chat_StreamThinkingTool(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_thinking_tool.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventThinkingDelta, Text: "I need to call the weather API."},
		{Type: codec.EventSignatureDelta, Signature: "sig_def456"},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventContentDelta, Text: "Let me check the weather."},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventToolCallStart},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallArgumentsDelta},
		{Type: codec.EventToolCallEnd},
		{Type: codec.EventContentBlockStop},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "tool_calls"},
	})

	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_thinking_tool.sse")
}

func TestClaude2Chat_StreamStopReasons(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	tests := []struct {
		name    string
		fixture string
		golden  string
		reason  string
	}{
		{"max_tokens", "claude/stream_max_tokens.txt", "claude2chat/stream_max_tokens.sse", "length"},
		{"stop_sequence", "claude/stream_stop_sequence.txt", "claude2chat/stream_stop_sequence.sse", "stop"},
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

func TestClaude2Chat_StreamMixed(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_mixed.txt",
		outCodec, inCodec, true, true)

	// Should have text content and tool call
	var hasText, hasTool bool
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text != "" {
			hasText = true
		}
		if ev.Type == codec.EventToolCallStart {
			hasTool = true
		}
	}
	if !hasText {
		t.Error("expected text content in mixed stream")
	}
	if !hasTool {
		t.Error("expected tool call in mixed stream")
	}
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_mixed.sse")
}

func TestClaude2Chat_StreamEmpty(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_empty.txt",
		outCodec, inCodec, true, true)

	assertEventSequence(t, events, []expectedEvent{
		{Type: codec.EventStreamStart},
		{Type: codec.EventUsage},
		{Type: codec.EventDone, FinishReason: "stop"},
	})
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_empty.sse")
}

func TestClaude2Chat_StreamUsageCached(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/stream_usage_cached.txt",
		outCodec, inCodec, true, true)

	for _, ev := range events {
		if ev.Type == codec.EventUsage && ev.Usage != nil {
			if ev.Usage.CacheReadTokens == 0 {
				t.Error("expected non-zero CacheReadTokens")
			}
		}
	}
	assertSSEFormat(t, sse, codec.ProtocolOpenAIChat)
	assertGoldenSSE(t, sse, "claude2chat/stream_usage_cached.sse")
}

func TestClaude2Chat_StreamError(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, _ := roundTripStream(t,
		"claude/stream_error.txt",
		outCodec, inCodec, true, true)

	// The Claude decoder currently silently drops the error SSE event.
	// Verify partial content before the error is still decoded.
	var hasContent bool
	for _, ev := range events {
		if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text == "Partial" {
			hasContent = true
		}
	}
	if !hasContent {
		t.Error("expected partial content before mid-stream error")
	}
}

func TestClaude2Chat_SystemPromptArray(t *testing.T) {
	body := `{"model":"claude-sonnet-4","max_tokens":1024,"system":[{"type":"text","text":"Be helpful"},{"type":"text","text":"Be concise"}],"messages":[{"role":"user","content":"Hi"}]}`
	result := roundTripRequest(t, codec.ProtocolClaude, codec.ProtocolOpenAIChat, body)

	msgs := mustGetArray(t, result, "messages")
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages, got %d", len(msgs))
	}
	sys := msgs[0].(map[string]any)
	content, _ := sys["content"].(string)
	if !strings.Contains(content, "Be helpful") || !strings.Contains(content, "Be concise") {
		t.Errorf("system content should contain both parts, got: %s", content)
	}
}

func TestClaude2Chat_ThinkingConfig(t *testing.T) {
	body := `{"model":"claude-sonnet-4","max_tokens":20000,"thinking":{"type":"enabled","budget_tokens":16000},"messages":[{"role":"user","content":"Think about this"}]}`
	result := roundTripRequest(t, codec.ProtocolClaude, codec.ProtocolOpenAIChat, body)

	if result["model"] == nil {
		t.Error("model should be present in output")
	}
}

func TestClaude2Chat_NonStreamToStream(t *testing.T) {
	outCodec := codec.GetOutbound(codec.ProtocolClaude)
	inCodec := codec.GetInbound(codec.ProtocolOpenAIChat)

	events, sse := roundTripStream(t,
		"claude/response_text.json",
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
	assertGoldenSSE(t, sse, "claude2chat/nonstream_to_stream.sse")
}

func TestClaude2Chat_ToolChoice(t *testing.T) {
	body := `{"model":"claude-sonnet-4","max_tokens":1024,"messages":[{"role":"user","content":"Hi"}],"tools":[{"name":"test","description":"test","input_schema":{}}],"tool_choice":{"type":"any"}}`
	result := roundTripRequest(t, codec.ProtocolClaude, codec.ProtocolOpenAIChat, body)

	// Claude tool_choice {type: "any"} → OpenAI "required"
	tc, ok := result["tool_choice"]
	if !ok {
		t.Fatal("tool_choice missing from Chat output")
	}
	if tc != "required" {
		t.Errorf("tool_choice = %v, want required", tc)
	}
}
