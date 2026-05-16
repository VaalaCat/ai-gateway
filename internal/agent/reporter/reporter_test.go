package reporter

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

func TestReporterBuffersAndFlushes(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	// Reporter with no WS client — will buffer but not send
	r := New(bus, nil, logger, 5, 100*time.Millisecond, "test-agent")
	r.Start(context.Background())
	defer r.Stop()

	// Publish 3 usage events
	for i := 0; i < 3; i++ {
		entry := protocol.UsageLogEntry{
			RequestID: "req-" + string(rune('a'+i)),
			UserID:    1,
			TokenID:   1,
			ChannelID: 1,
			ModelName: "gpt-4o",
		}
		if err := events.PublishUsageCompleted(context.Background(), bus, entry); err != nil {
			t.Fatalf("publish usage.completed: %v", err)
		}
	}

	// Wait for events to be processed
	time.Sleep(50 * time.Millisecond)

	if r.PendingCount() != 3 {
		t.Errorf("pending = %d, want 3", r.PendingCount())
	}

	// Wait for flush timer
	time.Sleep(150 * time.Millisecond)

	// After flush with nil client, buffer should still be 0 (dropped with warning)
	if r.PendingCount() != 0 {
		t.Errorf("after flush pending = %d, want 0", r.PendingCount())
	}
}

func TestReporterFlushOnBufferFull(t *testing.T) {
	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()

	r := New(bus, nil, logger, 2, 10*time.Second, "test-agent") // large interval, small buffer
	r.Start(context.Background())
	defer r.Stop()

	// Publish 3 events — buffer size is 2, should trigger flush after 2nd
	for i := 0; i < 3; i++ {
		entry := protocol.UsageLogEntry{
			RequestID: "req-" + string(rune('a'+i)),
			UserID:    1,
		}
		if err := events.PublishUsageCompleted(context.Background(), bus, entry); err != nil {
			t.Fatalf("publish usage.completed: %v", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	// After buffer-full flush (no client -> drop) + 1 remaining
	pending := r.PendingCount()
	if pending > 2 {
		t.Errorf("pending = %d, should be <= 2 (some flushed)", pending)
	}
}
