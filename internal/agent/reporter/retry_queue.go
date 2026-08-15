package reporter

import (
	"sort"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

const retryIsolateAfterAttempts = 2

type retryItem struct {
	entry    protocol.ReportedUsage
	attempts int
	nextAt   time.Time
	bytes    int
	degrade  int
}

type retryValue struct {
	Entry   protocol.ReportedUsage
	Degrade int
	Bytes   int
}

type retryQueue struct {
	queue   *deliveryqueue.Queue[retryValue]
	logger  *zap.Logger
	metrics GenericAPIUsageMetrics
}

func newRetryQueue(limit int, logger *zap.Logger, metric ...GenericAPIUsageMetrics) *retryQueue {
	var metrics GenericAPIUsageMetrics
	if len(metric) > 0 {
		metrics = metric[0]
	}
	return &retryQueue{
		queue: deliveryqueue.New(
			deliveryqueue.Limits{MaxEntries: limit},
			func(value retryValue) int64 {
				if value.Bytes > 0 {
					return int64(value.Bytes)
				}
				return int64(entrySize(value.Entry))
			},
			nil,
		),
		logger: logger, metrics: metrics,
	}
}

func (q *retryQueue) pushReported(entries []protocol.ReportedUsage, attempts int, nextAt time.Time) {
	if len(entries) == 0 {
		return
	}
	dropped := 0
	apiDropped := 0
	for _, entry := range entries {
		if !entry.Valid() {
			q.logger.Error("invalid retry usage entry", zap.String("request_id", entry.RequestID()))
			continue
		}
		_, evicted := q.queue.PutWithEvicted(deliveryqueue.Item[retryValue]{ID: entry.QueueID(),
			Value: retryValue{Entry: entry}, Attempts: attempts, NextAttempt: nextAt,
		}, deliveryqueue.Retry)
		dropped += len(evicted)
		for _, item := range evicted {
			if item.Value.Entry.IsAPI() {
				apiDropped++
			}
		}
	}
	q.logDrops(dropped, apiDropped)
}

func (q *retryQueue) dueReported(now time.Time, max int, includeAPI bool) []retryItem {
	if max <= 0 {
		return nil
	}
	items := q.queue.Items(deliveryqueue.Retry)
	due := make([]retryItem, 0, max)
	ids := make([]string, 0, max)
	for _, item := range items {
		if len(due) == max {
			break
		}
		if item.NextAttempt.After(now) {
			continue
		}
		if !includeAPI && item.Value.Entry.IsAPI() {
			continue
		}
		due = append(due, retryItemFromQueue(item))
		ids = append(ids, item.ID)
	}
	q.queue.Remove(ids...)
	if len(due) == 0 {
		return nil
	}
	return due
}

func (q *retryQueue) push(entries []protocol.UsageLogEntry, attempts int, nextAt time.Time) {
	reported := make([]protocol.ReportedUsage, 0, len(entries))
	for i := range entries {
		entry := entries[i]
		reported = append(reported, protocol.ReportedUsage{LLM: &entry})
	}
	q.pushReported(reported, attempts, nextAt)
}

func (q *retryQueue) due(now time.Time, max int) []retryItem {
	return q.dueReported(now, max, true)
}

func (q *retryQueue) drainAll() []retryItem {
	items := q.queue.Items(deliveryqueue.Retry)
	if len(items) == 0 {
		return nil
	}
	ids := make([]string, len(items))
	out := make([]retryItem, len(items))
	for i, item := range items {
		ids[i] = item.ID
		out[i] = retryItemFromQueue(item)
	}
	q.queue.Remove(ids...)
	return out
}

func (q *retryQueue) Len() int { return q.queue.Stats().Retry }

func (q *retryQueue) pushItem(item retryItem) {
	_, evicted := q.queue.PutWithEvicted(deliveryqueue.Item[retryValue]{ID: item.entry.QueueID(),
		Value:       retryValue{Entry: item.entry, Degrade: item.degrade, Bytes: item.bytes},
		Attempts:    item.attempts,
		NextAttempt: item.nextAt,
	}, deliveryqueue.Retry)
	apiDropped := 0
	for _, dropped := range evicted {
		if dropped.Value.Entry.IsAPI() {
			apiDropped++
		}
	}
	q.logDrops(len(evicted), apiDropped)
}

func (q *retryQueue) totalBytes() int { return int(q.queue.Stats().Bytes) }

func (q *retryQueue) Dropped() uint64 { return q.queue.Stats().Dropped }

func (q *retryQueue) oldestTimestamp() int64 {
	var oldest int64
	for _, item := range q.queue.Items(deliveryqueue.Retry) {
		timestamp := item.Value.Entry.Timestamp()
		if timestamp != 0 && (oldest == 0 || timestamp < oldest) {
			oldest = timestamp
		}
	}
	return oldest
}

func (q *retryQueue) snapshotTop(n int) []retryItem {
	items := q.snapshotAll()
	sort.Slice(items, func(i, j int) bool { return items[i].bytes > items[j].bytes })
	if len(items) > n {
		items = items[:n]
	}
	return items
}

func (q *retryQueue) snapshotAll() []retryItem {
	items := q.queue.Items(deliveryqueue.Retry)
	out := make([]retryItem, len(items))
	for i, item := range items {
		out[i] = retryItemFromQueue(item)
	}
	return out
}

func matchIDs(ids []string) func(string) bool {
	if ids == nil {
		return func(string) bool { return true }
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return func(id string) bool { _, ok := set[id]; return ok }
}

func (q *retryQueue) retryNow(ids []string) int {
	match := matchIDs(ids)
	return q.queue.SetNextAttemptMatching(
		func(item deliveryqueue.Item[retryValue]) bool { return match(item.ID) },
		time.Time{},
	)
}

func (q *retryQueue) remove(ids []string) int {
	match := matchIDs(ids)
	return q.queue.RemoveMatching(func(item deliveryqueue.Item[retryValue]) bool {
		return match(item.ID)
	})
}

func (q *retryQueue) degrade(ids []string, level int) int {
	match := matchIDs(ids)
	return q.queue.UpdateMatching(
		func(item deliveryqueue.Item[retryValue]) bool {
			return match(item.ID) && item.Value.Degrade < level
		},
		func(item *deliveryqueue.Item[retryValue]) {
			applyUsageDegrade(&item.Value.Entry, level)
			item.Value.Degrade = level
			item.Value.Bytes = 0
		},
	)
}

func (q *retryQueue) logDrops(dropped, apiDropped int) {
	if dropped > 0 {
		if q.metrics != nil && apiDropped > 0 {
			q.metrics.AddUsageDropped(uint64(apiDropped))
		}
		after := q.queue.Stats()
		q.logger.Error("retry queue overflow, dropped oldest entries",
			zap.Int("dropped", dropped), zap.Int("api_dropped", apiDropped), zap.Int("pending_len", after.Retry))
	}
}

func retryItemFromQueue(item deliveryqueue.Item[retryValue]) retryItem {
	return retryItem{
		entry: item.Value.Entry, attempts: item.Attempts, nextAt: item.NextAttempt,
		bytes: int(item.Bytes), degrade: item.Value.Degrade,
	}
}
