package apiusage

import (
	"context"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type recordingUsageMetrics struct {
	mu                                                                               sync.Mutex
	accepted, fullDropped, invalidDropped, duplicates, retryExhausted, shrinkDropped int
	depth                                                                            int
}

func (m *recordingUsageMetrics) Accepted()    { m.mu.Lock(); defer m.mu.Unlock(); m.accepted++ }
func (m *recordingUsageMetrics) FullDropped() { m.mu.Lock(); defer m.mu.Unlock(); m.fullDropped++ }
func (m *recordingUsageMetrics) InvalidDropped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalidDropped++
}
func (m *recordingUsageMetrics) Duplicate() { m.mu.Lock(); defer m.mu.Unlock(); m.duplicates++ }
func (m *recordingUsageMetrics) RetryExhausted() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retryExhausted++
}
func (m *recordingUsageMetrics) CapacityShrinkDropped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shrinkDropped++
}
func (m *recordingUsageMetrics) QueueDepth(depth int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.depth = depth
}

// Production break caught: a successfully admitted terminal API usage record
// must remain available to the worker, while an empty batch must be a harmless
// no-op for callers that have nothing to report.
func TestAPIUsageQueueAcceptsEntriesAndEmptyBatch(t *testing.T) {
	queue := NewQueue(QueueOptions{Capacity: 2, DedupCapacity: 2})

	require.NoError(t, queue.Accept(context.Background(), "agent-a", nil))
	require.Zero(t, queue.Len())

	entry := protocol.APIUsageEntry{RequestID: "request-accepted", SourceAgentID: "agent-a"}
	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{entry}))
	require.Equal(t, 1, queue.Len(), "an accepted usage record must be retained for settlement")
}

// Production break caught: retrying one RequestID must not enqueue a second
// charge while that ID remains inside the bounded deduplication window.
func TestAPIUsageQueueDropsDuplicateRequestID(t *testing.T) {
	queue := NewQueue(QueueOptions{Capacity: 2, DedupCapacity: 2})
	entry := protocol.APIUsageEntry{RequestID: "request-duplicate"}

	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{entry}))
	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{entry}))
	require.Equal(t, 1, queue.Len(), "a duplicate terminal report must settle only once")
}

// Production break caught: queue saturation must drop the incoming best-effort
// record, count that drop, and still let the HTTP ingest path acknowledge it.
func TestAPIUsageQueueDropsIncomingEntryWhenFull(t *testing.T) {
	metrics := &recordingUsageMetrics{}
	queue := NewQueue(QueueOptions{Capacity: 1, DedupCapacity: 2, Metrics: metrics})

	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{{RequestID: "request-kept"}}))
	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{{RequestID: "request-dropped"}}))
	require.Equal(t, 1, queue.Len(), "a full best-effort queue must not grow beyond its capacity")
	require.Equal(t, uint64(1), queue.Stats().Dropped, "a full queue must expose its drop for the ingest metric")
	require.Equal(t, 1, metrics.accepted)
	require.Equal(t, 1, metrics.fullDropped)
	require.Equal(t, 0, metrics.invalidDropped)
	require.Equal(t, 1, metrics.depth)
}

func TestAPIUsageQueueReportsInvalidDuplicateAndCapacityShrink(t *testing.T) {
	metrics := &recordingUsageMetrics{}
	queue := NewQueue(QueueOptions{Capacity: 2, DedupCapacity: 2, Metrics: metrics})
	require.NoError(t, queue.Accept(t.Context(), "agent", []protocol.APIUsageEntry{{}, {RequestID: "one"}, {RequestID: "one"}, {RequestID: "two"}}))
	queue.UpdateCapacity(1)
	require.Equal(t, 1, metrics.invalidDropped)
	require.Equal(t, 1, metrics.duplicates)
	require.Equal(t, 1, metrics.shrinkDropped)
	require.Equal(t, 1, metrics.depth)
}

// Production break caught: deduplication must be bounded; once the oldest ID
// is evicted, it is admissible again instead of being permanently suppressed.
func TestAPIUsageQueueReadmitsRequestIDAfterDedupWindowEviction(t *testing.T) {
	queue := NewQueue(QueueOptions{Capacity: 4, DedupCapacity: 2})

	for _, requestID := range []string{"request-one", "request-two", "request-three"} {
		require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{{RequestID: requestID}}))
	}
	require.NoError(t, queue.Accept(context.Background(), "agent-a", []protocol.APIUsageEntry{{RequestID: "request-one"}}))

	require.Equal(t, 4, queue.Len(), "the oldest dedup key must be accepted again after bounded-window eviction")
}
