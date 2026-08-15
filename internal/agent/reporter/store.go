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
	AppendReported(entries []protocol.ReportedUsage)
	PeekReportedBatch(max int, includeAPI bool) []protocol.ReportedUsage
	AckReported(entries []protocol.ReportedUsage)
	Len() int
	Bytes() int
	OldestTimestamp() int64
}

type GenericAPIUsageMetrics interface {
	AddUsageDropped(uint64)
	AddTraceSlimmed(uint64)
}

type MemPendingUsageStore struct {
	queue   *deliveryqueue.Queue[protocol.ReportedUsage]
	logger  *zap.Logger
	metrics GenericAPIUsageMetrics
}

var _ PendingUsageStore = (*MemPendingUsageStore)(nil)

func NewMemPendingUsageStore(limit int, logger *zap.Logger) *MemPendingUsageStore {
	return NewMemPendingUsageStoreWithLimits(deliveryqueue.Limits{MaxEntries: limit}, logger)
}

func NewMemPendingUsageStoreWithLimits(
	limits deliveryqueue.Limits,
	logger *zap.Logger,
	metric ...GenericAPIUsageMetrics,
) *MemPendingUsageStore {
	var metrics GenericAPIUsageMetrics
	if len(metric) > 0 {
		metrics = metric[0]
	}
	return &MemPendingUsageStore{
		queue: deliveryqueue.New(
			limits,
			func(entry protocol.ReportedUsage) int64 { return int64(entrySize(entry)) },
			nil,
		),
		logger: logger, metrics: metrics,
	}
}

func entrySize(entry protocol.ReportedUsage) int {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return 0
	}
	return len(encoded)
}

func (s *MemPendingUsageStore) AppendReported(entries []protocol.ReportedUsage) {
	if len(entries) == 0 {
		return
	}
	dropped := 0
	apiDropped := 0
	for _, entry := range entries {
		if !entry.Valid() {
			s.logger.Error("invalid pending usage entry", zap.String("request_id", entry.RequestID()))
			continue
		}
		result, evicted := s.queue.PutWithEvicted(
			deliveryqueue.Item[protocol.ReportedUsage]{ID: entry.QueueID(), Value: entry},
			deliveryqueue.Pending,
		)
		dropped += len(evicted)
		for _, item := range evicted {
			if item.Value.IsAPI() {
				apiDropped++
			}
		}
		if result.Conflict {
			s.logger.Warn("duplicate pending usage entry ignored", zap.String("queue_id", entry.QueueID()))
		}
	}
	if dropped > 0 {
		if s.metrics != nil && apiDropped > 0 {
			s.metrics.AddUsageDropped(uint64(apiDropped))
		}
		after := s.queue.Stats()
		s.logger.Error("pending usage store overflow, dropped oldest entries",
			zap.Int("dropped", dropped), zap.Int("api_dropped", apiDropped), zap.Int("pending_len", after.Pending))
	}
}

func (s *MemPendingUsageStore) Dropped() uint64 {
	if s == nil || s.queue == nil {
		return 0
	}
	return s.queue.Stats().Dropped
}

func (s *MemPendingUsageStore) PeekReportedBatch(max int, includeAPI bool) []protocol.ReportedUsage {
	if max <= 0 {
		return nil
	}
	items := s.queue.Items(deliveryqueue.Pending)
	out := make([]protocol.ReportedUsage, 0, min(max, len(items)))
	for i := range items {
		if !includeAPI && items[i].Value.IsAPI() {
			continue
		}
		out = append(out, items[i].Value)
		if len(out) == max {
			break
		}
	}
	return out
}

func (s *MemPendingUsageStore) AckReported(entries []protocol.ReportedUsage) {
	if len(entries) == 0 {
		return
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Valid() {
			ids = append(ids, entry.QueueID())
		}
	}
	s.queue.Remove(ids...)
}

func (s *MemPendingUsageStore) Append(entries []protocol.UsageLogEntry) {
	reported := make([]protocol.ReportedUsage, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		reported = append(reported, protocol.ReportedUsage{LLM: &entry})
	}
	s.AppendReported(reported)
}

func (s *MemPendingUsageStore) PeekBatch(max int) []protocol.UsageLogEntry {
	reported := s.PeekReportedBatch(max, false)
	if len(reported) == 0 {
		return nil
	}
	logs := make([]protocol.UsageLogEntry, 0, len(reported))
	for _, item := range reported {
		if item.LLM != nil {
			logs = append(logs, *item.LLM)
		}
	}
	return logs
}

func (s *MemPendingUsageStore) Ack(entries []protocol.UsageLogEntry) {
	reported := make([]protocol.ReportedUsage, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		reported = append(reported, protocol.ReportedUsage{LLM: &entry})
	}
	s.AckReported(reported)
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
		timestamp := item.Value.Timestamp()
		if timestamp != 0 && (oldest == 0 || timestamp < oldest) {
			oldest = timestamp
		}
	}
	return oldest
}
