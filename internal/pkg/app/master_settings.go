package app

import (
	"maps"
	"strings"
	"sync/atomic"
)

// MasterSettingsSnapshot stores master-only settings for request hot paths.
// Published maps are immutable; readers load one pointer without taking a lock.
type MasterSettingsSnapshot struct {
	values atomic.Pointer[map[string]string]
}

func NewMasterSettingsSnapshot() *MasterSettingsSnapshot {
	snapshot := &MasterSettingsSnapshot{}
	snapshot.Replace(nil)
	return snapshot
}

// Replace atomically publishes a complete clone of values.
func (s *MasterSettingsSnapshot) Replace(values map[string]string) {
	replacement := maps.Clone(values)
	if replacement == nil {
		replacement = make(map[string]string)
	}
	s.values.Store(&replacement)
}

// Update atomically merges values into the current snapshot. CAS preserves
// disjoint updates when multiple writers publish concurrently.
func (s *MasterSettingsSnapshot) Update(values map[string]string) {
	if len(values) == 0 {
		return
	}
	update := maps.Clone(values)
	for {
		current := s.values.Load()
		merged := make(map[string]string, len(update))
		if current != nil {
			merged = make(map[string]string, len(*current)+len(update))
			maps.Copy(merged, *current)
		}
		maps.Copy(merged, update)
		if s.values.CompareAndSwap(current, &merged) {
			return
		}
	}
}

func (s *MasterSettingsSnapshot) Lookup(key string) (string, bool) {
	if s == nil {
		return "", false
	}
	values := s.values.Load()
	if values == nil {
		return "", false
	}
	value, ok := (*values)[key]
	return value, ok
}

func (s *MasterSettingsSnapshot) LookupBool(key string, fallback bool) bool {
	raw, ok := s.Lookup(key)
	if !ok {
		return fallback
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return fallback
	}
}
