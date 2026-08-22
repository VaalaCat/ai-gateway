package convert

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit/ir"
)

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
