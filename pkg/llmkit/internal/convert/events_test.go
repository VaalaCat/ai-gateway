package convert

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
	"github.com/stretchr/testify/require"
)

func TestEncodeReasoningBlockOpenAIChatRoundTripThroughResponses(t *testing.T) {
	source := &ir.ReasoningContent{
		Summary: []string{"Need inspect files."},
		Content: []string{"Need inspect files."},
		RawJSON: json.RawMessage(`{"reasoning_content":"Need inspect files."}`),
	}

	responsesRaw := EncodeReasoningBlock(source, ReasoningProtocolResponses)
	var responsesItem struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
		Summary          []struct {
			Text string `json:"text"`
		} `json:"summary"`
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	require.NoError(t, json.Unmarshal(responsesRaw, &responsesItem))
	require.Equal(t, "reasoning", responsesItem.Type)
	require.Equal(t, "Need inspect files.", responsesItem.Summary[0].Text)
	require.Equal(t, "Need inspect files.", responsesItem.Content[0].Text)
	require.True(t, strings.HasPrefix(responsesItem.EncryptedContent, reasoningEnvelopePrefix))

	returned := &ir.ReasoningContent{
		Summary:   []string{"Need inspect files."},
		Content:   []string{"Need inspect files."},
		Encrypted: responsesItem.EncryptedContent,
		RawJSON:   responsesRaw,
	}
	chatRaw := EncodeReasoningBlock(returned, ReasoningProtocolOpenAIChat)
	var chatBlock map[string]any
	require.NoError(t, json.Unmarshal(chatRaw, &chatBlock))
	require.Equal(t, "Need inspect files.", chatBlock["reasoning_content"])
}

func TestEncodeReasoningBlockOpenAIChatStructuredTextPresence(t *testing.T) {
	source := &ir.ReasoningContent{
		Content: []string{"old"},
		RawJSON: json.RawMessage(`{"reasoning_content":"old","future":"kept"}`),
	}
	responsesRaw := EncodeReasoningBlock(source, ReasoningProtocolResponses)
	var responsesItem map[string]any
	require.NoError(t, json.Unmarshal(responsesRaw, &responsesItem))

	tests := []struct {
		name    string
		content []string
		summary []string
		want    string
	}{
		{name: "absent readable fields preserve original", want: "old"},
		{name: "empty arrays have no readable representation", content: []string{}, summary: []string{}, want: "old"},
		{name: "content overrides original", content: []string{"new"}, summary: []string{"summary"}, want: "new"},
		{name: "summary overrides original", summary: []string{"summary"}, want: "summary"},
		{name: "explicit empty content overrides original and summary", content: []string{""}, summary: []string{"summary"}, want: ""},
		{name: "explicit empty summary overrides original", summary: []string{""}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			for _, sourceKind := range []string{"envelope", "raw"} {
				t.Run(sourceKind, func(t *testing.T) {
					returned := &ir.ReasoningContent{Content: test.content, Summary: test.summary}
					if sourceKind == "envelope" {
						returned.Encrypted = responsesItem["encrypted_content"].(string)
					} else {
						returned.RawJSON = source.RawJSON
					}
					chatRaw := EncodeReasoningBlock(returned, ReasoningProtocolOpenAIChat)
					var chatBlock map[string]any
					require.NoError(t, json.Unmarshal(chatRaw, &chatBlock))
					require.Equal(t, test.want, chatBlock["reasoning_content"])
					require.Equal(t, "kept", chatBlock["future"])
				})
			}
		})
	}
}

func TestEncodeReasoningBlockOpenAIChatKeepsEmptyReasoningContent(t *testing.T) {
	raw := EncodeReasoningBlock(&ir.ReasoningContent{
		Content: []string{""},
		RawJSON: json.RawMessage(`{"reasoning_content":""}`),
	}, ReasoningProtocolOpenAIChat)
	var chatBlock map[string]any
	require.NoError(t, json.Unmarshal(raw, &chatBlock))
	value, exists := chatBlock["reasoning_content"]
	require.True(t, exists)
	require.Equal(t, "", value)
}

func TestEncodeReasoningBlockOpenAIChatBadEnvelopeFallsBackToReadable(t *testing.T) {
	for _, encrypted := range []string{"llmkit:v1:%%%", "llmkit:v2:opaque"} {
		t.Run(encrypted, func(t *testing.T) {
			raw := EncodeReasoningBlock(&ir.ReasoningContent{
				Summary:   []string{"readable"},
				Encrypted: encrypted,
			}, ReasoningProtocolOpenAIChat)
			var chatBlock map[string]any
			require.NoError(t, json.Unmarshal(raw, &chatBlock))
			require.Equal(t, "readable", chatBlock["reasoning_content"])
			require.NotContains(t, string(raw), "llmkit:")
		})
	}
}

func TestEncodeReasoningBlockSameProtocolKeepsCrossProtocolEnvelopeStable(t *testing.T) {
	source := json.RawMessage(`{"protocol":"responses","data":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"short"}],"content":[{"type":"reasoning_text","text":"deep"}],"encrypted_content":"enc-native"}}`)
	envelope := "llmkit:v1:" + base64.RawURLEncoding.EncodeToString(source)
	reasoning := &ir.ReasoningContent{
		Content:   []string{"deep"},
		Encrypted: envelope,
		RawJSON:   json.RawMessage(`{"type":"thinking","thinking":"deep","signature":"` + envelope + `","cache_control":{"type":"ephemeral"}}`),
	}

	first := EncodeReasoningBlock(reasoning, ReasoningProtocolClaude)
	second := EncodeReasoningBlock(reasoning, ReasoningProtocolClaude)
	if string(first) != string(second) {
		t.Fatalf("same history encoded differently:\n%s\n%s", first, second)
	}
	var block map[string]any
	if err := json.Unmarshal(first, &block); err != nil {
		t.Fatal(err)
	}
	if block["signature"] != envelope || block["cache_control"] == nil {
		t.Fatalf("same-protocol envelope/cache metadata changed: %#v", block)
	}
}

func TestAssertStreamingToolCallInvariant(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		events := []ir.Event{{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c"}}, {Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c", Arguments: "{}"}}, {Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c"}}}
		if err := AssertStreamingToolCallInvariant(events); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("missing start", func(t *testing.T) {
		err := AssertStreamingToolCallInvariant([]ir.Event{{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c"}}})
		if err == nil || !strings.Contains(err.Error(), "End without Start") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("empty", func(t *testing.T) {
		if err := AssertStreamingToolCallInvariant(nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAssertStreamingToolCallInvariantFailuresAndParallelCalls(t *testing.T) {
	tests := []struct {
		name      string
		events    []ir.Event
		wantError string
	}{
		{
			name: "duplicate start",
			events: []ir.Event{
				{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
				{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
			},
			wantError: "duplicate Start",
		},
		{
			name:      "unterminated call",
			events:    []ir.Event{{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c"}}},
			wantError: "Start without End",
		},
		{
			name: "arguments after end",
			events: []ir.Event{
				{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
				{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
				{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
			},
			wantError: "ArgumentsDelta after End",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := AssertStreamingToolCallInvariant(tt.events)
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
		})
	}

	t.Run("parallel calls may interleave", func(t *testing.T) {
		events := []ir.Event{
			{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "a"}},
			{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "b"}},
			{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "b"}},
			{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "a"}},
			{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "b"}},
			{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "a"}},
		}
		if err := AssertStreamingToolCallInvariant(events); err != nil {
			t.Fatal(err)
		}
	})
}

func TestAdaptFunctionFallbackEvents(t *testing.T) {
	tools := map[string]FunctionFallbackTool{"apply_patch": {Name: "apply_patch", ArgumentName: "input"}}
	t.Run("restores streamed function", func(t *testing.T) {
		input := eventChannel(
			ir.Event{Type: ir.EventToolCallStart, ToolCall: &ir.StreamingToolCall{CallID: "c", Name: "apply_patch"}},
			ir.Event{Type: ir.EventToolCallArgumentsDelta, ToolCall: &ir.StreamingToolCall{CallID: "c", Arguments: `{"input":"patch"}`}},
			ir.Event{Type: ir.EventToolCallEnd, ToolCall: &ir.StreamingToolCall{CallID: "c"}},
		)
		got := collectEvents(AdaptFunctionFallbackEvents(context.Background(), input, tools))
		if len(got) != 1 || got[0].RawPassthrough == nil {
			t.Fatalf("events = %#v", got)
		}
		var payload struct {
			Item struct{ Type, CallID, Name, Input string } `json:"item"`
		}
		if err := json.Unmarshal([]byte(got[0].RawPassthrough.Data), &payload); err != nil {
			t.Fatal(err)
		}
		if payload.Item.Type != "custom_tool_call" || payload.Item.Input != "patch" {
			t.Fatalf("payload = %#v", payload)
		}
	})
	t.Run("preserves other function", func(t *testing.T) {
		want := ir.Event{Type: ir.EventToolCallDelta, Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{Name: "other"}}}
		got := collectEvents(AdaptFunctionFallbackEvents(context.Background(), eventChannel(want), tools))
		if len(got) != 1 || got[0].Delta.ToolCall.Name != "other" {
			t.Fatalf("events = %#v", got)
		}
	})
	t.Run("empty event channel", func(t *testing.T) {
		empty := make(chan ir.Event)
		close(empty)
		if got := collectEvents(AdaptFunctionFallbackEvents(context.Background(), empty, tools)); len(got) != 0 {
			t.Fatalf("events = %#v", got)
		}
	})
}

func TestAdaptFunctionFallbackEventsCompatibility(t *testing.T) {
	tools := map[string]FunctionFallbackTool{"apply_patch": {Name: "apply_patch", ArgumentName: "input"}}

	t.Run("restores non-stream function call", func(t *testing.T) {
		got := collectEvents(AdaptFunctionFallbackEvents(context.Background(), eventChannel(ir.Event{
			Type:  ir.EventToolCallDelta,
			Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: "call", Name: "apply_patch", Arguments: `{"input":"patch"}`}},
		}), tools))
		assertCustomToolCall(t, got, "call", "apply_patch", "patch")
	})

	t.Run("malformed arguments are preserved", func(t *testing.T) {
		got := collectEvents(AdaptFunctionFallbackEvents(context.Background(), eventChannel(ir.Event{
			Type:  ir.EventToolCallDelta,
			Delta: &ir.DeltaPayload{ToolCall: &ir.ToolCallDelta{ID: "call", Name: "apply_patch", Arguments: `{"input":`}},
		}), tools))
		assertCustomToolCall(t, got, "call", "apply_patch", `{"input":`)
	})

	t.Run("no mappings return original channel", func(t *testing.T) {
		input := eventChannel(ir.Event{Type: ir.EventDone})
		if got := AdaptFunctionFallbackEvents(context.Background(), input, nil); got != input {
			t.Fatal("adapter replaced channel without fallback mappings")
		}
	})

	t.Run("cancellation closes output and drains source", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		events := make(chan ir.Event)
		out := AdaptFunctionFallbackEvents(ctx, events, tools)
		cancel()
		select {
		case _, ok := <-out:
			if ok {
				t.Fatal("output remained open")
			}
		case <-time.After(time.Second):
			t.Fatal("output did not close promptly")
		}
		drained := make(chan struct{})
		go func() {
			events <- ir.Event{Type: ir.EventDone}
			close(events)
			close(drained)
		}()
		select {
		case <-drained:
		case <-time.After(time.Second):
			t.Fatal("source was not drained")
		}
	})
}

func assertCustomToolCall(t *testing.T, events []ir.Event, callID, name, input string) {
	t.Helper()
	if len(events) != 1 || events[0].Type != ir.EventRawPassthrough || events[0].RawPassthrough == nil {
		t.Fatalf("events = %#v", events)
	}
	var payload struct {
		Item struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Input  string `json:"input"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(events[0].RawPassthrough.Data), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Item.Type != "custom_tool_call" || payload.Item.CallID != callID || payload.Item.Name != name || payload.Item.Input != input {
		t.Fatalf("item = %#v", payload.Item)
	}
}

func eventChannel(events ...ir.Event) <-chan ir.Event {
	ch := make(chan ir.Event, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func collectEvents(events <-chan ir.Event) []ir.Event {
	var out []ir.Event
	for event := range events {
		out = append(out, event)
	}
	return out
}
