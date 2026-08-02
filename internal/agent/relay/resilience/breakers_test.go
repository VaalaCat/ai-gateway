package resilience

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

func testCfg() Config {
	return Config{MaxRetries: 2, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 2, BreakerCooldownMs: 50, BreakerEnabled: true}
}

func adminKey(id uint) BreakerKey { return BreakerKey{Source: state.SourceAdmin, ID: id} }

func TestRegistry_SameChannelSameInstance(t *testing.T) {
	r := NewRegistry()
	a := r.Get(adminKey(7), testCfg())
	b := r.Get(adminKey(7), testCfg())
	if a != b {
		t.Fatal("same key must return the same breaker instance")
	}
	if r.Len() != 1 {
		t.Fatalf("want 1 entry, got %d", r.Len())
	}
}

func TestRegistry_DifferentChannels(t *testing.T) {
	r := NewRegistry()
	r.Get(adminKey(1), testCfg())
	r.Get(adminKey(2), testCfg())
	if r.Len() != 2 {
		t.Fatalf("want 2 entries, got %d", r.Len())
	}
}

// TestRegistry_AdminPrivateSameIDDistinct 锁住:admin 与 BYOK private 的同号渠道
// 必须拿到【不同】的熔断器(ID 空间独立,不得串号)。
func TestRegistry_AdminPrivateSameIDDistinct(t *testing.T) {
	r := NewRegistry()
	admin := r.Get(BreakerKey{Source: state.SourceAdmin, ID: 5}, testCfg())
	priv := r.Get(BreakerKey{Source: state.SourcePrivate, ID: 5}, testCfg())
	if admin == priv {
		t.Fatal("admin #5 and private #5 must NOT share a circuit breaker")
	}
	if r.Len() != 2 {
		t.Fatalf("want 2 distinct breakers, got %d", r.Len())
	}
}

func TestRegistry_OpensAfterThreshold(t *testing.T) {
	r := NewRegistry()
	cb := r.Get(adminKey(1), testCfg()) // threshold=2
	cb.RecordFailure()
	cb.RecordFailure()
	if !cb.IsOpen() {
		t.Fatal("breaker should open after 2 consecutive failures")
	}
}

func TestRegistry_SweepEvictsIdle(t *testing.T) {
	r := NewRegistry()
	r.Get(adminKey(1), testCfg())
	// 把 entry 的过期点推到过去再 sweep。
	r.sweep(time.Now().Add(2 * idleTTL))
	if r.Len() != 0 {
		t.Fatalf("idle breaker should be evicted, got %d", r.Len())
	}
}

func TestRegistry_ReplacesBreakerWhenThresholdChanges(t *testing.T) {
	r := NewRegistry()
	firstCfg := testCfg()
	firstCfg.BreakerThreshold = 5
	first := r.Get(adminKey(7), firstCfg)
	first.RecordFailure()

	secondCfg := firstCfg
	secondCfg.BreakerThreshold = 2
	second := r.Get(adminKey(7), secondCfg)
	if first == second {
		t.Fatal("threshold change must replace the breaker")
	}
	second.RecordFailure()
	if second.IsOpen() {
		t.Fatal("replacement breaker opened before new threshold")
	}
	second.RecordFailure()
	if !second.IsOpen() {
		t.Fatal("replacement breaker must use threshold=2")
	}
}

func TestRegistry_ReplacesBreakerWhenCooldownOrEnabledChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "cooldown", mutate: func(cfg *Config) { cfg.BreakerCooldownMs++ }},
		{name: "enabled", mutate: func(cfg *Config) { cfg.BreakerEnabled = !cfg.BreakerEnabled }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := NewRegistry()
			cfg := testCfg()
			first := r.Get(adminKey(7), cfg)
			test.mutate(&cfg)
			second := r.Get(adminKey(7), cfg)
			if first == second {
				t.Fatal("changed breaker config must replace the breaker")
			}
			if !second.IsClosed() {
				t.Fatal("replacement breaker must start closed")
			}
		})
	}
}
