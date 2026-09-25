package upstream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestMonitorEvents_FinalSnapshotAfterNaturalDrain(t *testing.T) {
	ctx := context.Background()
	events := make(chan llmkit.Event, 4)
	events <- llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "hello"},
	}
	events <- llmkit.Event{
		Type: llmkit.EventUsage,
		Usage: &llmkit.Usage{
			PromptTokens: 7, CompletionTokens: 11,
			CacheReadTokens: 3, CacheWriteTokens: 5,
		},
	}
	events <- llmkit.Event{Type: llmkit.EventDone, FinishReason: "stop"}
	close(events)

	monitored, monitor := MonitorEvents(ctx, events)
	for range monitored {
	}
	snapshot := monitor.FinalSnapshot()

	if snapshot.Usage.PromptTokens != 7 || snapshot.Usage.CompletionTokens != 11 ||
		snapshot.Usage.CacheReadTokens != 3 || snapshot.Usage.CacheWriteTokens != 5 {
		t.Fatalf("final usage = %+v, want prompt=7 completion=11 cache_read=3 cache_write=5", snapshot.Usage)
	}
	if snapshot.ResponseText != "hello" {
		t.Fatalf("final response text = %q, want hello", snapshot.ResponseText)
	}
	if snapshot.FinishReason != "stop" {
		t.Fatalf("final finish reason = %q, want stop", snapshot.FinishReason)
	}
	if snapshot.EventErr != nil {
		t.Fatalf("final event error = %v, want nil", snapshot.EventErr)
	}
}

func TestMonitorEvents_CancelUnblocksUndrainedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan llmkit.Event, 70)
	events <- llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "observed"},
	}
	for range 69 {
		events <- llmkit.Event{Type: llmkit.EventStreamStart}
	}

	monitored, monitor := MonitorEvents(ctx, events)
	if event := <-monitored; event.Delta == nil || event.Delta.Text != "observed" {
		t.Fatalf("first monitored event = %+v, want observed content", event)
	}
	cancel()

	select {
	case <-monitor.Done():
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after attempt context cancellation")
	}
	if got := monitor.FinalSnapshot().ResponseText; got != "observed" {
		t.Fatalf("final response text = %q, want observed event before blocked forwarding", got)
	}
}

func TestMonitorEvents_EmptyStreamHasZeroFinalSnapshot(t *testing.T) {
	events := make(chan llmkit.Event)
	close(events)

	monitored, monitor := MonitorEvents(context.Background(), events)
	for range monitored {
	}
	snapshot := monitor.FinalSnapshot()

	if snapshot.Usage != (llmkit.Usage{}) || snapshot.ResponseText != "" ||
		snapshot.FinishReason != "" || snapshot.EventErr != nil {
		t.Fatalf("empty final snapshot = %+v, want zero value", snapshot)
	}
	select {
	case <-monitor.Done():
	default:
		t.Fatal("monitor Done must be closed after the source stream closes")
	}
}

func TestMonitorEvents_EventErrorIsIncludedInFinalSnapshot(t *testing.T) {
	events := make(chan llmkit.Event, 1)
	events <- llmkit.Event{
		Type:  llmkit.EventError,
		Error: &llmkit.ErrorPayload{Message: "stream broke"},
	}
	close(events)

	monitored, monitor := MonitorEvents(context.Background(), events)
	for range monitored {
	}
	got := monitor.FinalSnapshot().EventErr
	if got == nil || !errors.Is(got, monitor.EventError()) || got.Error() != "stream broke" {
		t.Fatalf("final event error = %v, want stream broke", got)
	}
}
