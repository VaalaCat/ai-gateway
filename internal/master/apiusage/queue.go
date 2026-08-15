package apiusage

import (
	"context"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// QueueOptions controls the bounded, best-effort API usage admission buffer.
// A full buffer drops the incoming record; it never evicts a record already
// accepted for settlement.
type QueueOptions struct {
	Capacity      int
	DedupCapacity int
	Metrics       Metrics
}

type QueueStats struct {
	Accepted   uint64
	Dropped    uint64
	Duplicates uint64
}

type QueuedAPIUsage struct {
	SourceAgentID string
	Entry         protocol.APIUsageEntry
	Attempts      int
}

// APIUsageQueue is intentionally small: HTTP ingest only admits records and
// workers only take records. The queue does not make acknowledgement depend on
// downstream database availability.
type APIUsageQueue struct {
	mu        sync.Mutex
	capacity  int
	dedupCap  int
	items     []QueuedAPIUsage
	seen      map[string]struct{}
	order     []string
	stats     QueueStats
	metrics   Metrics
	accepting bool
}

func NewQueue(options QueueOptions) *APIUsageQueue {
	if options.Metrics == nil {
		options.Metrics = noopMetrics{}
	}
	return &APIUsageQueue{
		capacity:  options.Capacity,
		dedupCap:  options.DedupCapacity,
		seen:      make(map[string]struct{}),
		metrics:   options.Metrics,
		accepting: true,
	}
}

// Accept is best effort by contract: a full or duplicate record is accounted
// for locally and still returns nil so the source Agent can receive its ack.
func (q *APIUsageQueue) Accept(ctx context.Context, sourceAgentID string, entries []protocol.APIUsageEntry) error {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.accepting {
		return context.Canceled
	}
	for _, entry := range entries {
		if entry.RequestID == "" {
			q.stats.Dropped++
			q.metrics.InvalidDropped()
			continue
		}
		if _, duplicate := q.seen[entry.RequestID]; duplicate {
			q.stats.Duplicates++
			q.metrics.Duplicate()
			continue
		}
		if q.capacity <= 0 || len(q.items) >= q.capacity {
			q.stats.Dropped++
			q.metrics.FullDropped()
			continue
		}
		entry.SourceAgentID = sourceAgentID
		q.items = append(q.items, QueuedAPIUsage{SourceAgentID: sourceAgentID, Entry: entry})
		q.rememberLocked(entry.RequestID)
		q.stats.Accepted++
		q.metrics.Accepted()
		q.metrics.QueueDepth(len(q.items))
	}
	return nil
}

func (q *APIUsageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *APIUsageQueue) Stats() QueueStats {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.stats
}

func (q *APIUsageQueue) Take() (QueuedAPIUsage, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return QueuedAPIUsage{}, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	q.metrics.QueueDepth(len(q.items))
	return item, true
}

func (q *APIUsageQueue) CloseAdmission() { q.mu.Lock(); q.accepting = false; q.mu.Unlock() }

func (q *APIUsageQueue) UpdateCapacity(capacity int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.capacity = capacity
	for q.capacity >= 0 && len(q.items) > q.capacity {
		q.items = q.items[:len(q.items)-1]
		q.stats.Dropped++
		q.metrics.CapacityShrinkDropped()
	}
	q.metrics.QueueDepth(len(q.items))
}

func (q *APIUsageQueue) rememberLocked(requestID string) {
	if q.dedupCap <= 0 {
		return
	}
	q.seen[requestID] = struct{}{}
	q.order = append(q.order, requestID)
	for len(q.order) > q.dedupCap {
		oldest := q.order[0]
		q.order = q.order[1:]
		delete(q.seen, oldest)
	}
}
