package deliveryqueue

import (
	"errors"
	"math"
	"testing"
	"time"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

func TestQueueDropsOldestPendingBeforeNewest(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 2, MaxBytes: 100}, func(v string) int64 { return int64(len(v)) }, clock)
	q.Enqueue("old")
	clock.now = clock.now.Add(time.Second)
	q.Enqueue("middle")
	clock.now = clock.now.Add(time.Second)
	q.Enqueue("new")

	items := q.TakeReady(10)
	if len(items) != 2 || items[0].Value != "middle" || items[1].Value != "new" {
		t.Fatalf("ready = %+v, want middle,new", items)
	}
	if got := q.Stats().Dropped; got != 1 {
		t.Fatalf("dropped = %d, want 1", got)
	}
}

func TestPutRejectsDuplicateIDWithoutMutation(t *testing.T) {
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(1, 0)})
	first := q.Put(Item[string]{ID: "stable", Value: "first"}, Pending)
	duplicate := q.Put(Item[string]{ID: "stable", Value: "second"}, Retry)
	if !first.Accepted || !duplicate.Conflict || duplicate.Error == "" {
		t.Fatalf("first=%+v duplicate=%+v", first, duplicate)
	}
	items := q.Items()
	if len(items) != 1 || items[0].Value != "first" {
		t.Fatalf("items after duplicate = %+v", items)
	}
}

func TestCallerOwnedGeneratedLookingIDsDoNotAdvanceCursor(t *testing.T) {
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(1, 0)})
	callerMax := q.Put(Item[string]{ID: "q-18446744073709551615", Value: "caller-max"}, Pending)
	generated := q.Enqueue("generated")
	if !callerMax.Accepted || generated.ID != "q-1" || !generated.Accepted {
		t.Fatalf("callerMax=%+v generated=%+v", callerMax, generated)
	}

	q2 := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(1, 0)})
	q2.Put(Item[string]{ID: "q-1", Value: "caller-one"}, Pending)
	if got := q2.Enqueue("generated"); got.ID != "q-2" || !got.Accepted {
		t.Fatalf("generated collision result = %+v, want q-2", got)
	}
	q2.RemoveMatching(func(item Item[string]) bool { return item.Value == "caller-one" })
	if got := q2.Enqueue("after-delete"); got.ID != "q-3" || !got.Accepted {
		t.Fatalf("generated after caller deletion = %+v, want q-3", got)
	}
}

func TestGeneratedIDCursorWrapsAndSkipsCollisions(t *testing.T) {
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(1, 0)})
	q.nextID = math.MaxUint64
	q.Put(Item[string]{ID: "q-0", Value: "caller-zero"}, Pending)
	if got := q.Enqueue("wrapped"); got.ID != "q-1" || !got.Accepted {
		t.Fatalf("wrapped result = %+v, want q-1 after skipping caller q-0", got)
	}

	q2 := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(1, 0)})
	q2.nextID = math.MaxUint64
	if got := q2.Enqueue("wrapped-first"); got.ID != "q-0" || !got.Accepted {
		t.Fatalf("empty wrap result = %+v, want q-0", got)
	}
}

func TestRestoreValidationIsAtomic(t *testing.T) {
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, &fakeClock{now: time.Unix(100, 0)})
	snapshot := Snapshot[string]{Version: 1, SavedAt: time.Unix(100, 0), NextID: 2, Items: []SnapshotItem[string]{
		{Item: Item[string]{ID: "q-1", Value: "first", Bytes: 1}, State: Pending, CreatedAt: time.Unix(90, 0)},
		{Item: Item[string]{ID: "q-1", Value: "duplicate", Bytes: 1}, State: Retry, CreatedAt: time.Unix(91, 0)},
	}}
	if err := q.Restore(snapshot); err == nil {
		t.Fatal("duplicate snapshot restore must fail")
	}
	if items := q.Items(); len(items) != 0 {
		t.Fatalf("restore partially mutated queue: %+v", items)
	}
}

func TestQueueDropsOldestAcrossPendingAndRetry(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 2}, func(string) int64 { return 1 }, clock)
	q.Put(Item[string]{ID: "retry", Value: "old-retry", Attempts: 1, NextAttempt: clock.now.Add(time.Hour)}, Retry)
	clock.now = clock.now.Add(time.Second)
	q.Enqueue("pending")
	clock.now = clock.now.Add(time.Second)
	q.Enqueue("new")

	items := q.Items()
	if len(items) != 2 || items[0].Value != "pending" || items[1].Value != "new" {
		t.Fatalf("items = %+v, want pending,new", items)
	}
}

func TestQueueDropsIncomingWhenInflightConsumesCapacity(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 1, MaxBytes: 10}, func(string) int64 { return 1 }, clock)
	q.Enqueue("inflight")
	taken := q.TakeReady(1)
	if got := q.Enqueue("incoming"); got.Accepted || !got.Dropped {
		t.Fatalf("enqueue result = %+v, want incoming dropped", got)
	}
	stats := q.Stats()
	if stats.Inflight != 1 || stats.Dropped != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	q.Ack(taken[0].ID)
}

func TestQueueEnforcesEntryAndByteLimits(t *testing.T) {
	tests := []struct {
		name   string
		limits Limits
	}{
		{name: "entries", limits: Limits{MaxEntries: 2, MaxBytes: 100}},
		{name: "bytes", limits: Limits{MaxEntries: 10, MaxBytes: 4}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := New(tt.limits, func(v string) int64 { return int64(len(v)) }, &fakeClock{})
			q.Enqueue("aa")
			q.Enqueue("bb")
			q.Enqueue("cc")
			if stats := q.Stats(); stats.Pending != 2 || stats.Bytes != 4 || stats.Dropped != 1 {
				t.Fatalf("stats = %+v", stats)
			}
		})
	}
}

func TestUpdateResizeReenforcesByteLimit(t *testing.T) {
	q := New(Limits{MaxEntries: 10, MaxBytes: 4}, func(v string) int64 { return int64(len(v)) }, &fakeClock{now: time.Unix(1, 0)})
	first := q.Enqueue("aa")
	second := q.Enqueue("bb")
	if !q.Update(second.ID, func(item *Item[string]) { item.Value = "bbbb" }) {
		t.Fatal("update did not match")
	}
	items := q.Items()
	if len(items) != 1 || items[0].ID != second.ID || q.Stats().Bytes != 4 {
		t.Fatalf("items=%+v stats=%+v; oldest item should be evicted", items, q.Stats())
	}
	if first.ID == second.ID {
		t.Fatal("generated IDs must be unique")
	}
}

func TestPutUsesSizeFunctionInsteadOfCallerBytes(t *testing.T) {
	q := New(Limits{MaxEntries: 10, MaxBytes: 4}, func(v string) int64 { return int64(len(v)) }, &fakeClock{now: time.Unix(1, 0)})
	result := q.Put(Item[string]{ID: "lied", Value: "oversized", Bytes: 1}, Pending)
	if !result.Dropped || result.Accepted || len(q.Items()) != 0 {
		t.Fatalf("result=%+v items=%+v", result, q.Items())
	}
}

func TestByteAccountingSaturatesWithoutWrapping(t *testing.T) {
	sizes := map[string]int64{"negative": -1, "max-a": math.MaxInt64, "max-b": math.MaxInt64, "max-c": math.MaxInt64}
	q := New(Limits{MaxEntries: 10}, func(v string) int64 { return sizes[v] }, &fakeClock{now: time.Unix(1, 0)})
	q.Enqueue("negative")
	q.Enqueue("max-a")
	q.Enqueue("max-b")
	q.Enqueue("max-c")
	if got := q.Stats().Bytes; got != math.MaxUint64 {
		t.Fatalf("bytes = %d, want saturated MaxUint64", got)
	}
	cleared := q.ClearBacklog()
	if cleared.Bytes != math.MaxInt64 {
		t.Fatalf("clear bytes = %d, want saturated MaxInt64", cleared.Bytes)
	}

	limited := New(Limits{MaxEntries: 10, MaxBytes: math.MaxInt64}, func(string) int64 { return math.MaxInt64 }, &fakeClock{now: time.Unix(1, 0)})
	limited.Enqueue("first")
	limited.Enqueue("second")
	if stats := limited.Stats(); stats.Pending != 1 || stats.Dropped != 1 {
		t.Fatalf("overflow-safe limit stats = %+v", stats)
	}
}

func TestQueueEmptyZeroByteAndOversized(t *testing.T) {
	q := New(Limits{MaxEntries: 2, MaxBytes: 1}, func(v string) int64 {
		if v == "zero" {
			return 0
		}
		return int64(len(v))
	}, &fakeClock{})
	if got := q.TakeReady(1); got != nil {
		t.Fatalf("empty take = %+v", got)
	}
	if !q.Enqueue("zero").Accepted {
		t.Fatal("zero-byte item should be accepted")
	}
	result := q.Enqueue("oversized")
	if result.Accepted || !result.Dropped {
		t.Fatalf("oversized result = %+v", result)
	}
	if stats := q.Stats(); stats.Pending != 0 || stats.Dropped != 2 {
		t.Fatalf("stats = %+v, oversized must not evict old then remain over limit", stats)
	}
}

func TestQueueRetryBackoffAndPoisonIsolation(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 10, MaxBytes: 100}, func(string) int64 { return 1 }, clock)
	q.Enqueue("poison")
	poison := q.TakeReady(1)
	for attempt := 0; attempt < PoisonIsolationAttempts; attempt++ {
		q.Retry([]string{poison[0].ID}, errors.New("bad"))
		q.RetryNow()
		poison = q.TakeReady(1)
	}
	q.Retry([]string{poison[0].ID}, errors.New("bad"))
	q.RetryNow()
	q.Enqueue("healthy")

	first := q.TakeReady(10)
	if len(first) != 1 || first[0].Value != "healthy" {
		t.Fatalf("first batch = %+v, poison must not block or join healthy", first)
	}
	q.Ack(first[0].ID)
	second := q.TakeReady(10)
	if len(second) != 1 || second[0].Value != "poison" {
		t.Fatalf("second batch = %+v, want isolated poison", second)
	}
	if q.Stats().LastError != "bad" {
		t.Fatalf("last error = %q", q.Stats().LastError)
	}
}

func TestPoisonRetryRotatesBehindOtherIsolatedItems(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, clock)
	q.Put(Item[string]{ID: "poison-a", Value: "a", Attempts: PoisonIsolationAttempts, NextAttempt: clock.now}, Retry)
	q.Put(Item[string]{ID: "poison-b", Value: "b", Attempts: PoisonIsolationAttempts, NextAttempt: clock.now}, Retry)

	first := q.TakeReady(1)
	if len(first) != 1 || first[0].Value != "a" {
		t.Fatalf("first = %+v, want a", first)
	}
	q.RetryWithBackoff([]string{first[0].ID}, errors.New("still bad"), time.Nanosecond, time.Nanosecond)
	clock.now = clock.now.Add(time.Nanosecond)

	second := q.TakeReady(1)
	if len(second) != 1 || second[0].Value != "b" {
		t.Fatalf("second = %+v, want b before retried a", second)
	}
}

func TestPoisonRetryDoesNotBlockYoungerRetry(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 10}, func(string) int64 { return 1 }, clock)
	q.Put(Item[string]{Value: "poison", Attempts: PoisonIsolationAttempts, NextAttempt: clock.now}, Retry)
	q.Put(Item[string]{Value: "young", Attempts: 1, NextAttempt: clock.now}, Retry)

	items := q.TakeReady(10)
	if len(items) != 1 || items[0].Value != "young" {
		t.Fatalf("first retry batch = %+v, want young item", items)
	}
	q.Ack(items[0].ID)
	items = q.TakeReady(10)
	if len(items) != 1 || items[0].Value != "poison" {
		t.Fatalf("isolated retry batch = %+v, want poison item", items)
	}
}

func TestBackoffIsJitteredAndCapped(t *testing.T) {
	first := Backoff("a", 1, time.Second, 10*time.Second, 0.2)
	if first < 800*time.Millisecond || first > 1200*time.Millisecond {
		t.Fatalf("first backoff = %s, want bounded jitter around 1s", first)
	}
	if got := Backoff("a", 100, time.Second, 10*time.Second, 0.2); got != 10*time.Second {
		t.Fatalf("capped backoff = %s, want 10s", got)
	}
	if got := Backoff("a", -1, time.Second, 10*time.Second, 0); got != time.Second {
		t.Fatalf("negative-attempt backoff = %s, want 1s", got)
	}
}

func TestClearBacklogKeepsInflight(t *testing.T) {
	q := New(Limits{MaxEntries: 4, MaxBytes: 10}, func(string) int64 { return 1 }, &fakeClock{})
	q.Enqueue("flying")
	inflight := q.TakeReady(1)
	q.Enqueue("pending")
	cleared := q.ClearBacklog()
	if cleared.Items != 1 || q.Stats().Inflight != 1 {
		t.Fatalf("cleared=%+v stats=%+v", cleared, q.Stats())
	}
	q.Ack(inflight[0].ID)
}

func TestQueueUpdateLimitsEvictsOldestNonInflight(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	q := New(Limits{MaxEntries: 3}, func(value string) int64 { return int64(len(value)) }, clock)
	first := q.Enqueue("first")
	clock.now = clock.now.Add(time.Second)
	second := q.Enqueue("second")
	clock.now = clock.now.Add(time.Second)
	third := q.Enqueue("third")
	q.TakeReady(1)

	q.UpdateLimits(Limits{MaxEntries: 2, MaxBytes: 100})

	items := q.Items()
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != third.ID {
		t.Fatalf("items = %+v, want inflight first and newest third; evicted=%s", items, second.ID)
	}
}
