package reporter

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// PendingUsageStore keeps usage entries until master acknowledges delivery.
// The Agent adapter retains the historical peek/ack contract while delegating
// capacity, byte accounting and synchronization to deliveryqueue.Queue.
type PendingUsageStore interface {
	Append(entries []protocol.UsageLogEntry)
	PeekBatch(max int) []protocol.UsageLogEntry
	Ack(entries []protocol.UsageLogEntry)
	Len() int
	Bytes() int
	OldestTimestamp() int64
}

type MemPendingUsageStore struct {
	queue  *deliveryqueue.Queue[protocol.UsageLogEntry]
	logger *zap.Logger
}

var _ PendingUsageStore = (*MemPendingUsageStore)(nil)

func NewMemPendingUsageStore(limit int, logger *zap.Logger) *MemPendingUsageStore {
	return NewMemPendingUsageStoreWithLimits(deliveryqueue.Limits{MaxEntries: limit}, logger)
}

func NewMemPendingUsageStoreWithLimits(limits deliveryqueue.Limits, logger *zap.Logger) *MemPendingUsageStore {
	return &MemPendingUsageStore{
		queue: deliveryqueue.New(
			limits,
			func(entry protocol.UsageLogEntry) int64 { return int64(entrySize(entry)) },
			nil,
		),
		logger: logger,
	}
}

func entrySize(entry protocol.UsageLogEntry) int {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func (s *MemPendingUsageStore) Append(entries []protocol.UsageLogEntry) {
	if len(entries) == 0 {
		return
	}
	before := s.queue.Stats().Dropped
	for _, entry := range entries {
		s.queue.Enqueue(entry)
	}
	after := s.queue.Stats()
	if dropped := int(after.Dropped - before); dropped > 0 {
		s.logger.Error("pending usage store overflow, dropped oldest entries",
			zap.Int("dropped", dropped), zap.Int("pending_len", after.Pending))
	}
}

func (s *MemPendingUsageStore) PeekBatch(max int) []protocol.UsageLogEntry {
	if max <= 0 {
		return nil
	}
	items := s.queue.Items(deliveryqueue.Pending)
	if len(items) > max {
		items = items[:max]
	}
	if len(items) == 0 {
		return nil
	}
	out := make([]protocol.UsageLogEntry, len(items))
	for i := range items {
		out[i] = items[i].Value
	}
	return out
}

func (s *MemPendingUsageStore) Ack(entries []protocol.UsageLogEntry) {
	if len(entries) == 0 {
		return
	}
	requestIDs := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		requestIDs[entry.RequestID] = struct{}{}
	}
	items := s.queue.Items(deliveryqueue.Pending)
	ids := make([]string, 0, len(entries))
	for _, item := range items {
		if _, ok := requestIDs[item.Value.RequestID]; ok {
			ids = append(ids, item.ID)
		}
	}
	s.queue.Remove(ids...)
}

func (s *MemPendingUsageStore) Len() int {
	return s.queue.Stats().Pending
}

func (s *MemPendingUsageStore) Bytes() int {
	return int(s.queue.Stats().Bytes)
}

func (s *MemPendingUsageStore) OldestTimestamp() int64 {
	var oldest int64
	for _, item := range s.queue.Items(deliveryqueue.Pending) {
		timestamp := item.Value.Timestamp
		if timestamp != 0 && (oldest == 0 || timestamp < oldest) {
			oldest = timestamp
		}
	}
	return oldest
}
