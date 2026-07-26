package deliveryqueue

import "time"

type Stats struct {
	Pending   int
	Retry     int
	Inflight  int
	Bytes     uint64
	Dropped   uint64
	OldestAge time.Duration
	LastError string
}

func (q *Queue[T]) Stats() Stats {
	q.mu.Lock()
	defer q.mu.Unlock()
	stats := Stats{Dropped: q.dropped, LastError: q.lastErr}
	now := q.clock.Now()
	var oldest time.Time
	for _, it := range q.items {
		switch it.state {
		case Pending:
			stats.Pending++
		case Retry:
			stats.Retry++
		case Inflight:
			stats.Inflight++
		}
		stats.Bytes = saturatingAddUint64(stats.Bytes, it.Bytes)
		if oldest.IsZero() || it.createdAt.Before(oldest) {
			oldest = it.createdAt
		}
	}
	if !oldest.IsZero() && now.After(oldest) {
		stats.OldestAge = now.Sub(oldest)
	}
	return stats
}
