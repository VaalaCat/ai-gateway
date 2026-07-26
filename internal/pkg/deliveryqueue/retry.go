package deliveryqueue

import "time"

// PoisonIsolationAttempts is the number of failed deliveries after which an
// item is delivered alone. It remains retryable and never enters a dead-letter
// collection, so one bad item cannot block later work or grow storage forever.
const PoisonIsolationAttempts = 3

func (q *Queue[T]) TakeReady(limit int) []Item[T] {
	if limit <= 0 {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	now := q.clock.Now()
	if pending := q.takeStateLocked(Pending, limit, now); len(pending) > 0 {
		return pending
	}
	if retry := q.takeStateLocked(Retry, limit, now); len(retry) > 0 {
		return retry
	}
	for _, item := range q.items {
		if item.state == Retry && !item.NextAttempt.After(now) && item.Attempts >= PoisonIsolationAttempts {
			item.state = Inflight
			q.generation++
			return []Item[T]{item.Item}
		}
	}
	return nil
}

func (q *Queue[T]) takeStateLocked(state State, limit int, now time.Time) []Item[T] {
	items := make([]Item[T], 0, limit)
	for _, item := range q.items {
		if len(items) == limit {
			break
		}
		if item.state != state || (state == Retry && item.NextAttempt.After(now)) {
			continue
		}
		if state == Retry && item.Attempts >= PoisonIsolationAttempts {
			continue
		}
		item.state = Inflight
		items = append(items, item.Item)
	}
	if len(items) == 0 {
		return nil
	}
	q.generation++
	return items
}

func (q *Queue[T]) Retry(ids []string, cause error) {
	q.RetryWithBackoff(ids, cause, defaultBackoffBase, defaultBackoffMax)
}

// RetryWithBackoff lets a delivery worker apply runtime retry settings while
// preserving the queue's deterministic jitter and poison isolation rules.
func (q *Queue[T]) RetryWithBackoff(ids []string, cause error, base, max time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	wanted := idSet(ids)
	if cause != nil {
		q.lastErr = cause.Error()
	}
	now := q.clock.Now()
	updated := false
	isolated := make(map[string]struct{})
	for _, item := range q.items {
		if item.state != Inflight {
			continue
		}
		if _, ok := wanted[item.ID]; !ok {
			continue
		}
		item.state = Retry
		item.Attempts++
		item.NextAttempt = now.Add(Backoff(item.ID, item.Attempts, base, max, defaultJitter))
		if item.Attempts >= PoisonIsolationAttempts {
			isolated[item.ID] = struct{}{}
		}
		updated = true
	}
	if updated {
		q.moveToTailLocked(isolated)
		q.generation++
	}
}

func (q *Queue[T]) moveToTailLocked(ids map[string]struct{}) {
	if len(ids) == 0 {
		return
	}
	kept := make([]*queueItem[T], 0, len(q.items))
	tail := make([]*queueItem[T], 0, len(ids))
	for _, item := range q.items {
		if _, ok := ids[item.ID]; ok {
			tail = append(tail, item)
			continue
		}
		kept = append(kept, item)
	}
	q.items = append(kept, tail...)
}

func (q *Queue[T]) RetryNow() {
	q.SetNextAttemptMatching(func(Item[T]) bool { return true }, q.clock.Now())
}

// SetNextAttemptMatching updates retry scheduling metadata without exposing a
// payload mutation path that could bypass byte-limit enforcement.
func (q *Queue[T]) SetNextAttemptMatching(match func(Item[T]) bool, next time.Time) int {
	if match == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	updated := 0
	for _, item := range q.items {
		if item.state == Retry && match(item.Item) {
			item.NextAttempt = next
			updated++
		}
	}
	if updated > 0 {
		q.generation++
	}
	return updated
}
