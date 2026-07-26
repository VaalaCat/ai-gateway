package deliveryqueue

import (
	"errors"
	"fmt"
	"math"
	"sync"
	"time"
)

type Limits struct {
	MaxEntries int
	MaxBytes   int64
}

type State string

const (
	Pending  State = "pending"
	Retry    State = "retry"
	Inflight State = "inflight"
)

type Clock interface {
	Now() time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

type Item[T any] struct {
	ID          string    `json:"id"`
	Value       T         `json:"value"`
	Bytes       int64     `json:"bytes"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt,omitempty"`
}

type EnqueueResult struct {
	ID       string
	Accepted bool
	Dropped  bool
	Conflict bool
	Error    string
}

type ClearResult struct {
	Items   int
	Pending int
	Retry   int
	Bytes   int64
}

type SnapshotItem[T any] struct {
	Item      Item[T]   `json:"item"`
	State     State     `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

type Snapshot[T any] struct {
	Version    int               `json:"version"`
	SavedAt    time.Time         `json:"saved_at"`
	NextID     uint64            `json:"next_id"`
	Generation uint64            `json:"generation,omitempty"`
	Items      []SnapshotItem[T] `json:"items"`
}

type queueItem[T any] struct {
	Item[T]
	state     State
	createdAt time.Time
}

type Queue[T any] struct {
	mu         sync.Mutex
	limits     Limits
	sizeOf     func(T) int64
	clock      Clock
	nextID     uint64
	items      []*queueItem[T]
	dropped    uint64
	lastErr    string
	generation uint64
}

func New[T any](limits Limits, sizeOf func(T) int64, clock Clock) *Queue[T] {
	if sizeOf == nil {
		sizeOf = func(T) int64 { return 0 }
	}
	if clock == nil {
		clock = realClock{}
	}
	return &Queue[T]{limits: limits, sizeOf: sizeOf, clock: clock}
}

func (q *Queue[T]) Enqueue(value T) EnqueueResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.putLocked(Item[T]{Value: value}, Pending, q.clock.Now())
}

// UpdateLimits applies runtime capacity settings and evicts the oldest
// pending/retry items until the queue fits. Inflight deliveries remain owned
// by their current writer and are never canceled.
func (q *Queue[T]) UpdateLimits(limits Limits) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits = limits
	q.enforceLimitsLocked()
}

// Put restores or adapts an item with caller-owned delivery metadata. New
// producers should normally use Enqueue; adapters use Put to preserve stable
// IDs, retry attempts and retry deadlines from an existing protocol.
func (q *Queue[T]) Put(item Item[T], state State) EnqueueResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	if state != Pending && state != Retry {
		state = Pending
	}
	return q.putLocked(item, state, q.clock.Now())
}

func (q *Queue[T]) putLocked(item Item[T], state State, now time.Time) EnqueueResult {
	if item.Attempts < 0 {
		return EnqueueResult{ID: item.ID, Error: "delivery queue item attempts must not be negative"}
	}
	id := item.ID
	if id == "" {
		var ok bool
		id, ok = q.nextGeneratedIDLocked()
		if !ok {
			return EnqueueResult{Error: "delivery queue generated ID space exhausted"}
		}
	} else if q.hasIDLocked(id) {
		return EnqueueResult{ID: id, Conflict: true, Error: fmt.Sprintf("delivery queue item ID %q already exists", id)}
	}
	size := q.sizeOf(item.Value)
	if size < 0 {
		size = 0
	}
	item.ID = id
	item.Bytes = size
	incoming := &queueItem[T]{Item: item, state: state, createdAt: now}
	q.items = append(q.items, incoming)
	q.generation++
	for q.overLimitLocked() {
		idx := q.oldestEvictableLocked()
		if idx < 0 {
			q.removeByIDLocked(id)
			q.dropped++
			q.generation++
			return EnqueueResult{ID: id, Dropped: true}
		}
		droppedID := q.items[idx].ID
		q.items = append(q.items[:idx], q.items[idx+1:]...)
		q.dropped++
		q.generation++
		if droppedID == id {
			return EnqueueResult{ID: id, Dropped: true}
		}
	}
	return EnqueueResult{ID: id, Accepted: true}
}

// Items returns a stable copy in enqueue order. With no states it returns all
// states; otherwise it returns only the requested states.
func (q *Queue[T]) Items(states ...State) []Item[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	wanted := make(map[State]struct{}, len(states))
	for _, state := range states {
		wanted[state] = struct{}{}
	}
	out := make([]Item[T], 0, len(q.items))
	for _, it := range q.items {
		if len(wanted) > 0 {
			if _, ok := wanted[it.state]; !ok {
				continue
			}
		}
		out = append(out, it.Item)
	}
	return out
}

// Remove deletes matching non-inflight items. Inflight work remains protected
// from cancellation by capacity and management operations.
func (q *Queue[T]) Remove(ids ...string) int {
	set := idSet(ids)
	return q.RemoveMatching(func(item Item[T]) bool {
		_, ok := set[item.ID]
		return ok
	})
}

func (q *Queue[T]) RemoveMatching(match func(Item[T]) bool) int {
	if match == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.items[:0]
	removed := 0
	for _, it := range q.items {
		if it.state != Inflight && match(it.Item) {
			removed++
			continue
		}
		kept = append(kept, it)
	}
	q.items = kept
	if removed > 0 {
		q.generation++
	}
	return removed
}

// Update applies a small adapter-specific value/metadata update while keeping
// queue synchronization internal.
func (q *Queue[T]) Update(id string, update func(*Item[T])) bool {
	return q.UpdateMatching(func(item Item[T]) bool { return item.ID == id }, update) > 0
}

func (q *Queue[T]) UpdateMatching(match func(Item[T]) bool, update func(*Item[T])) int {
	if match == nil || update == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	updated := 0
	for _, it := range q.items {
		if it.state == Inflight || !match(it.Item) {
			continue
		}
		id := it.ID
		update(&it.Item)
		it.ID = id
		if it.Attempts < 0 {
			it.Attempts = 0
		}
		it.Bytes = q.sizeOf(it.Value)
		if it.Bytes < 0 {
			it.Bytes = 0
		}
		updated++
	}
	if updated > 0 {
		q.generation++
		q.enforceLimitsLocked()
	}
	return updated
}

func (q *Queue[T]) Ack(ids ...string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	set := idSet(ids)
	kept := q.items[:0]
	for _, it := range q.items {
		if it.state == Inflight {
			if _, ok := set[it.ID]; ok {
				continue
			}
		}
		kept = append(kept, it)
	}
	if len(kept) != len(q.items) {
		q.items = kept
		q.generation++
		return
	}
	q.items = kept
}

func (q *Queue[T]) ClearBacklog() ClearResult {
	q.mu.Lock()
	defer q.mu.Unlock()
	kept := q.items[:0]
	var result ClearResult
	for _, it := range q.items {
		if it.state == Inflight {
			kept = append(kept, it)
			continue
		}
		result.Items++
		switch it.state {
		case Pending:
			result.Pending++
		case Retry:
			result.Retry++
		}
		result.Bytes = saturatingAddInt64(result.Bytes, it.Bytes)
	}
	q.items = kept
	q.dropped += uint64(result.Items)
	if result.Items > 0 {
		q.generation++
	}
	return result
}

func (q *Queue[T]) Snapshot() Snapshot[T] {
	q.mu.Lock()
	defer q.mu.Unlock()
	snapshot := Snapshot[T]{Version: 1, SavedAt: q.clock.Now(), NextID: q.nextID, Generation: q.generation, Items: make([]SnapshotItem[T], 0, len(q.items))}
	for _, it := range q.items {
		snapshot.Items = append(snapshot.Items, SnapshotItem[T]{Item: it.Item, State: it.state, CreatedAt: it.createdAt})
	}
	return snapshot
}

func (q *Queue[T]) Generation() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.generation
}

func (q *Queue[T]) Restore(snapshot Snapshot[T]) error {
	if snapshot.Version != 1 {
		return fmt.Errorf("delivery queue snapshot version %d is unsupported", snapshot.Version)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) != 0 {
		return errors.New("delivery queue restore requires an empty queue")
	}
	if err := validateSnapshot(snapshot); err != nil {
		return err
	}
	q.nextID = snapshot.NextID
	q.generation = snapshot.Generation
	if q.generation == 0 {
		q.generation = 1
	}
	normalizedInflight := false
	for _, saved := range snapshot.Items {
		state := saved.State
		if state == Inflight {
			state = Pending
			normalizedInflight = true
		}
		item := saved.Item
		item.Bytes = q.sizeOf(item.Value)
		if item.Bytes < 0 {
			item.Bytes = 0
		}
		q.items = append(q.items, &queueItem[T]{Item: item, state: state, createdAt: saved.CreatedAt})
	}
	if normalizedInflight {
		q.generation++
	}
	for q.overLimitLocked() {
		idx := q.oldestEvictableLocked()
		if idx < 0 {
			break
		}
		q.items = append(q.items[:idx], q.items[idx+1:]...)
		q.dropped++
		q.generation++
	}
	return nil
}

func (q *Queue[T]) overLimitLocked() bool {
	if q.limits.MaxEntries > 0 && len(q.items) > q.limits.MaxEntries {
		return true
	}
	if q.limits.MaxBytes <= 0 {
		return false
	}
	remaining := q.limits.MaxBytes
	for _, item := range q.items {
		if item.Bytes > remaining {
			return true
		}
		remaining -= item.Bytes
	}
	return false
}

func (q *Queue[T]) oldestEvictableLocked() int {
	idx := -1
	for i, it := range q.items {
		if it.state == Inflight {
			continue
		}
		if idx < 0 || it.createdAt.Before(q.items[idx].createdAt) {
			idx = i
		}
	}
	return idx
}

func (q *Queue[T]) removeByIDLocked(id string) {
	for i, it := range q.items {
		if it.ID == id {
			q.items = append(q.items[:i], q.items[i+1:]...)
			return
		}
	}
}

func (q *Queue[T]) enforceLimitsLocked() {
	for q.overLimitLocked() {
		idx := q.oldestEvictableLocked()
		if idx < 0 {
			return
		}
		q.items = append(q.items[:idx], q.items[idx+1:]...)
		q.dropped++
		q.generation++
	}
}

func (q *Queue[T]) hasIDLocked(id string) bool {
	for _, item := range q.items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func (q *Queue[T]) nextGeneratedIDLocked() (string, bool) {
	first := nextGeneratedCursor(q.nextID)
	candidate := first
	for {
		id := fmt.Sprintf("q-%d", candidate)
		if !q.hasIDLocked(id) {
			q.nextID = candidate
			return id, true
		}
		candidate = nextGeneratedCursor(candidate)
		if candidate == first {
			return "", false
		}
	}
}

func nextGeneratedCursor(current uint64) uint64 {
	return current + 1
}

func validateSnapshot[T any](snapshot Snapshot[T]) error {
	if snapshot.SavedAt.IsZero() {
		return errors.New("delivery queue snapshot has zero saved_at")
	}
	seen := make(map[string]struct{}, len(snapshot.Items))
	for _, saved := range snapshot.Items {
		item := saved.Item
		if saved.State != Pending && saved.State != Retry && saved.State != Inflight {
			return fmt.Errorf("delivery queue snapshot has invalid state %q", saved.State)
		}
		if item.ID == "" {
			return errors.New("delivery queue snapshot has empty item ID")
		}
		if _, ok := seen[item.ID]; ok {
			return fmt.Errorf("delivery queue snapshot has duplicate item ID %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		if item.Attempts < 0 {
			return fmt.Errorf("delivery queue snapshot item %q has negative attempts", item.ID)
		}
		if item.Bytes < 0 {
			return fmt.Errorf("delivery queue snapshot item %q has negative bytes", item.ID)
		}
		if saved.CreatedAt.IsZero() || saved.CreatedAt.After(snapshot.SavedAt) {
			return fmt.Errorf("delivery queue snapshot item %q has invalid created_at", item.ID)
		}
	}
	return nil
}

func saturatingAddUint64(total uint64, value int64) uint64 {
	if value <= 0 {
		return total
	}
	add := uint64(value)
	if math.MaxUint64-total < add {
		return math.MaxUint64
	}
	return total + add
}

func saturatingAddInt64(total, value int64) int64 {
	if value <= 0 {
		return total
	}
	if math.MaxInt64-total < value {
		return math.MaxInt64
	}
	return total + value
}

func idSet(ids []string) map[string]struct{} {
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}
