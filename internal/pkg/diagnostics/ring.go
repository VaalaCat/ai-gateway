package diagnostics

import (
	"sync"
	"time"
)

const DefaultRingCapacity = 20

type Event struct {
	Code    string    `json:"code"`
	Stage   string    `json:"stage"`
	Message string    `json:"message"`
	URI     string    `json:"uri,omitempty"`
	At      time.Time `json:"at"`
}

type Ring struct {
	mu       sync.Mutex
	capacity int
	entries  []Event
}

func NewRing(capacity int) *Ring {
	if capacity <= 0 || capacity > DefaultRingCapacity {
		capacity = DefaultRingCapacity
	}
	return &Ring{capacity: capacity, entries: make([]Event, 0, capacity)}
}

func (r *Ring) Record(event Event) bool {
	if r == nil || event.Code == "" {
		return false
	}
	event.Message = SanitizeText(event.Message)
	event.URI = RedactURI(event.URI)
	r.mu.Lock()
	if len(r.entries) == r.capacity {
		copy(r.entries, r.entries[1:])
		r.entries[len(r.entries)-1] = event
	} else {
		r.entries = append(r.entries, event)
	}
	r.mu.Unlock()
	return true
}

func (r *Ring) Snapshot() []Event {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	result := append([]Event(nil), r.entries...)
	r.mu.Unlock()
	return result
}
