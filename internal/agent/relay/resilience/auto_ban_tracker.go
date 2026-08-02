package resilience

import (
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

type AutoBanObservation struct {
	Key       BreakerKey
	Enabled   bool
	Threshold int
	Revision  uint64
	Result    state.AttemptResult
}

type autoBanEntry struct {
	mu        sync.Mutex
	threshold int
	revision  uint64
	streak    int
	reported  bool
	exp       int64
}

type AutoBanTracker struct {
	mu      sync.Mutex
	entries utils.SyncMap[BreakerKey, *autoBanEntry]
}

func NewAutoBanTracker() *AutoBanTracker { return &AutoBanTracker{} }

func (t *AutoBanTracker) Observe(observation AutoBanObservation) *attemptproxy.ChannelAutoDisableTrigger {
	if !observation.Enabled {
		t.mu.Lock()
		t.entries.Delete(observation.Key)
		t.mu.Unlock()
		return nil
	}
	if observation.Threshold < 1 {
		observation.Threshold = 1
	}
	if t.Len() >= maxBreakers {
		t.sweep(time.Now())
	}

	now := time.Now().UnixNano()
	t.mu.Lock()
	entry, ok := t.entries.Load(observation.Key)
	if !ok {
		entry = &autoBanEntry{threshold: observation.Threshold, revision: observation.Revision}
		t.entries.Store(observation.Key, entry)
	}
	entry.mu.Lock()
	entry.exp = now + int64(idleTTL)
	t.mu.Unlock()
	defer entry.mu.Unlock()
	if entry.threshold != observation.Threshold || entry.revision != observation.Revision {
		entry.threshold = observation.Threshold
		entry.revision = observation.Revision
		entry.streak = 0
		entry.reported = false
	}
	decision := Classify(observation.Result)
	if observation.Result.Err == nil {
		entry.streak = 0
		return nil
	}
	if !decision.CountToBreaker || entry.reported {
		return nil
	}
	entry.streak++
	if entry.streak < entry.threshold {
		return nil
	}
	entry.reported = true
	return &attemptproxy.ChannelAutoDisableTrigger{
		Source:    observation.Key.Source,
		ChannelID: observation.Key.ID,
		Revision:  observation.Revision,
		Reason:    attemptproxy.ChannelAutoDisableReasonConsecutiveErrors,
	}
}

func (t *AutoBanTracker) Len() int { return t.entries.Len() }

func (t *AutoBanTracker) sweep(now time.Time) {
	cutoff := now.UnixNano()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries.Range(func(key BreakerKey, _ *autoBanEntry) bool {
		entry, ok := t.entries.Load(key)
		if !ok {
			return true
		}
		entry.mu.Lock()
		expired := entry.exp <= cutoff
		entry.mu.Unlock()
		if expired {
			t.entries.Delete(key)
		}
		return true
	})
}
