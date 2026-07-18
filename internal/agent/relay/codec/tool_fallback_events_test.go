package codec

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

func TestAdaptFunctionFallbackEventsNonStreamRestoresCustomToolCall(t *testing.T) {
	events := eventChannel(Event{
		Type: EventToolCallDelta,
		Delta: &DeltaPayload{ToolCall: &ToolCallDelta{
			ID:        "call_patch",
			Name:      "apply_patch",
			Arguments: `{"input":"*** Begin Patch\n*** End Patch"}`,
		}},
	})

	got := collectEvents(AdaptFunctionFallbackEvents(
		context.Background(),
		events,
		functionFallbackTestTools(),
	))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	assertCustomToolCallEvent(t, got[0], "call_patch", "apply_patch", "*** Begin Patch\n*** End Patch")
}

func TestAdaptFunctionFallbackEventsStreamAggregatesArguments(t *testing.T) {
	events := eventChannel(
		Event{Type: EventToolCallStart, ToolCall: &StreamingToolCall{CallID: "call_patch", Name: "apply_patch"}},
		Event{Type: EventToolCallArgumentsDelta, ToolCall: &StreamingToolCall{CallID: "call_patch", Arguments: `{"input":"*** Begin `}},
		Event{Type: EventToolCallArgumentsDelta, ToolCall: &StreamingToolCall{CallID: "call_patch", Arguments: `Patch"}`}},
		Event{Type: EventToolCallEnd, ToolCall: &StreamingToolCall{CallID: "call_patch"}},
	)

	got := collectEvents(AdaptFunctionFallbackEvents(
		context.Background(),
		events,
		functionFallbackTestTools(),
	))
	if len(got) != 1 {
		t.Fatalf("got %d events, want one aggregated custom tool call", len(got))
	}
	assertCustomToolCallEvent(t, got[0], "call_patch", "apply_patch", "*** Begin Patch")
}

func TestAdaptFunctionFallbackEventsPreservesOtherFunctions(t *testing.T) {
	want := Event{
		Type: EventToolCallDelta,
		Delta: &DeltaPayload{ToolCall: &ToolCallDelta{
			ID:        "call_exec",
			Name:      "exec_command",
			Arguments: `{"cmd":"pwd"}`,
		}},
	}

	got := collectEvents(AdaptFunctionFallbackEvents(
		context.Background(),
		eventChannel(want),
		functionFallbackTestTools(),
	))
	if !reflect.DeepEqual(got, []Event{want}) {
		t.Fatalf("non-fallback function changed: got %#v, want %#v", got, []Event{want})
	}
}

func TestAdaptFunctionFallbackEventsPreservesMalformedArguments(t *testing.T) {
	events := eventChannel(Event{
		Type: EventToolCallDelta,
		Delta: &DeltaPayload{ToolCall: &ToolCallDelta{
			ID:        "call_patch",
			Name:      "apply_patch",
			Arguments: `{"input":`,
		}},
	})

	got := collectEvents(AdaptFunctionFallbackEvents(
		context.Background(),
		events,
		functionFallbackTestTools(),
	))
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	assertCustomToolCallEvent(t, got[0], "call_patch", "apply_patch", `{"input":`)
}

func TestAdaptFunctionFallbackEventsCancellationClosesOutputBeforeSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan Event)
	out := AdaptFunctionFallbackEvents(ctx, events, map[string]FunctionFallbackTool{
		"apply_patch": functionFallbackTestTools()["apply_patch"],
	})

	cancel()
	select {
	case _, ok := <-out:
		if ok {
			t.Fatal("output channel remained open after cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("output channel did not close promptly after cancellation")
	}

	// The adapter must keep draining until the decoder closes its source so a
	// decoder blocked on send can observe the response-body close and exit.
	sourceDrained := make(chan struct{})
	go func() {
		events <- Event{Type: EventDone}
		close(events)
		close(sourceDrained)
	}()
	select {
	case <-sourceDrained:
	case <-time.After(time.Second):
		t.Fatal("cancelled adapter stopped draining source events")
	}
}

func functionFallbackTestTools() map[string]FunctionFallbackTool {
	return map[string]FunctionFallbackTool{
		"apply_patch": {
			Name:         "apply_patch",
			ArgumentName: "input",
		},
	}
}

func eventChannel(events ...Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, event := range events {
		ch <- event
	}
	close(ch)
	return ch
}

func collectEvents(events <-chan Event) []Event {
	var collected []Event
	for event := range events {
		collected = append(collected, event)
	}
	return collected
}

func assertCustomToolCallEvent(t *testing.T, event Event, callID, name, input string) {
	t.Helper()
	if event.Type != EventRawPassthrough || event.RawPassthrough == nil {
		t.Fatalf("event = %#v, want raw Responses custom tool call", event)
	}
	if event.RawPassthrough.EventName != customToolCallEvent {
		t.Fatalf("event name = %q, want %q", event.RawPassthrough.EventName, customToolCallEvent)
	}
	var payload struct {
		Item struct {
			Type   string `json:"type"`
			CallID string `json:"call_id"`
			Name   string `json:"name"`
			Input  string `json:"input"`
		} `json:"item"`
	}
	if err := json.Unmarshal([]byte(event.RawPassthrough.Data), &payload); err != nil {
		t.Fatalf("decode raw event: %v", err)
	}
	if payload.Item.Type != "custom_tool_call" || payload.Item.CallID != callID || payload.Item.Name != name || payload.Item.Input != input {
		t.Fatalf("custom tool call = %#v, want type=custom_tool_call call_id=%q name=%q input=%q", payload.Item, callID, name, input)
	}
}
