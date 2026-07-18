package tunnel

import (
	"sync"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type tombstoneEntry struct {
	id      wire.StreamID
	expires time.Time
}

type tombstoneStore struct {
	mu      sync.Mutex
	limit   int
	ttl     time.Duration
	now     func() time.Time
	entries []tombstoneEntry
}

func newTombstoneStore(limit int, ttl time.Duration, now func() time.Time) *tombstoneStore {
	if limit < 0 {
		limit = 0
	}
	if limit > 512 {
		limit = 512
	}
	return &tombstoneStore{limit: limit, ttl: ttl, now: now}
}

func (s *tombstoneStore) Add(id wire.StreamID) {
	if s.limit == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for i := range s.entries {
		if s.entries[i].id == id {
			s.entries[i].expires = s.now().Add(s.ttl)
			return
		}
	}
	if len(s.entries) == s.limit {
		copy(s.entries, s.entries[1:])
		s.entries = s.entries[:len(s.entries)-1]
	}
	s.entries = append(s.entries, tombstoneEntry{id: id, expires: s.now().Add(s.ttl)})
}

func (s *tombstoneStore) Contains(id wire.StreamID) bool {
	if s.limit == 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked()
	for _, entry := range s.entries {
		if entry.id == id {
			return true
		}
	}
	return false
}

func (s *tombstoneStore) pruneLocked() {
	now := s.now()
	kept := s.entries[:0]
	for _, entry := range s.entries {
		if now.Before(entry.expires) {
			kept = append(kept, entry)
		}
	}
	s.entries = kept
}
