package diagnostics

import (
	"container/list"
	"sync"
	"time"
)

const (
	DefaultSuppressionWindow = time.Minute
	DefaultSuppressionKeys   = 4096
)

type SuppressionKey struct {
	Source     string
	Target     string
	PathKind   string
	Stage      string
	ReasonCode string
}

type SuppressionSummary struct {
	Key             SuppressionKey
	Kind            string
	SuppressedCount uint64
}

type SuppressionDecision struct {
	Allow   bool
	Summary *SuppressionSummary
}

type SuppressorOptions struct {
	Window  time.Duration
	MaxKeys int
}

type suppressionState struct {
	key         SuppressionKey
	windowStart time.Time
	lastSeen    time.Time
	suppressed  uint64
	lru         *list.Element
}

type Suppressor struct {
	mu         sync.Mutex
	window     time.Duration
	maxKeys    int
	states     map[SuppressionKey]*suppressionState
	lru        list.List
	nextExpiry time.Time
	lastNow    time.Time
	sweepCount uint64
}

func NewSuppressor(opts SuppressorOptions) *Suppressor {
	if opts.Window <= 0 {
		opts.Window = DefaultSuppressionWindow
	}
	if opts.MaxKeys <= 0 || opts.MaxKeys > DefaultSuppressionKeys {
		opts.MaxKeys = DefaultSuppressionKeys
	}
	return &Suppressor{window: opts.Window, maxKeys: opts.MaxKeys, states: make(map[SuppressionKey]*suppressionState, opts.MaxKeys)}
}

func (s *Suppressor) Observe(key SuppressionKey, now time.Time) SuppressionDecision {
	if s == nil {
		return SuppressionDecision{Allow: true}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now = s.advanceNowLocked(now)
	s.expireIfDueLocked(now)
	state := s.states[key]
	if state == nil {
		state = &suppressionState{key: key, windowStart: now, lastSeen: now}
		state.lru = s.lru.PushFront(key)
		s.states[key] = state
		s.scheduleExpiryLocked(state.lastSeen.Add(2 * s.window))
		s.trimLocked()
		return SuppressionDecision{Allow: true}
	}
	if now.After(state.lastSeen) {
		state.lastSeen = now
	}
	s.scheduleExpiryLocked(state.lastSeen.Add(2 * s.window))
	s.lru.MoveToFront(state.lru)
	if !now.Before(state.windowStart.Add(s.window)) {
		suppressed := state.suppressed
		state.windowStart = now
		state.suppressed = 0
		if suppressed == 0 {
			return SuppressionDecision{Allow: true}
		}
		return SuppressionDecision{Summary: &SuppressionSummary{Key: key, Kind: "window_end", SuppressedCount: suppressed}}
	}
	state.suppressed++
	return SuppressionDecision{}
}

func (s *Suppressor) Recover(key SuppressionKey, now time.Time) *SuppressionSummary {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	now = s.advanceNowLocked(now)
	s.expireIfDueLocked(now)
	state := s.states[key]
	if state == nil {
		s.mu.Unlock()
		return nil
	}
	s.removeLocked(state)
	s.mu.Unlock()
	if state.suppressed == 0 {
		return nil
	}
	return &SuppressionSummary{Key: key, Kind: "recovery", SuppressedCount: state.suppressed}
}

func (s *Suppressor) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	length := len(s.states)
	s.mu.Unlock()
	return length
}

func (s *Suppressor) Contains(key SuppressionKey) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	_, ok := s.states[key]
	s.mu.Unlock()
	return ok
}

func (s *Suppressor) advanceNowLocked(now time.Time) time.Time {
	if s.lastNow.IsZero() || now.After(s.lastNow) {
		s.lastNow = now
	}
	return s.lastNow
}

func (s *Suppressor) scheduleExpiryLocked(expiry time.Time) {
	if s.nextExpiry.IsZero() || expiry.Before(s.nextExpiry) {
		s.nextExpiry = expiry
	}
}

func (s *Suppressor) expireIfDueLocked(now time.Time) {
	if s.nextExpiry.IsZero() || now.Before(s.nextExpiry) {
		return
	}
	s.sweepCount++
	cutoff := now.Add(-2 * s.window)
	s.nextExpiry = time.Time{}
	for _, state := range s.states {
		if !state.lastSeen.After(cutoff) {
			s.removeLocked(state)
			continue
		}
		s.scheduleExpiryLocked(state.lastSeen.Add(2 * s.window))
	}
}

func (s *Suppressor) trimLocked() {
	for len(s.states) > s.maxKeys {
		element := s.lru.Back()
		if element == nil {
			return
		}
		if state := s.states[element.Value.(SuppressionKey)]; state != nil {
			s.removeLocked(state)
		}
	}
}

func (s *Suppressor) removeLocked(state *suppressionState) {
	delete(s.states, state.key)
	s.lru.Remove(state.lru)
	if len(s.states) == 0 {
		s.nextExpiry = time.Time{}
	}
}
