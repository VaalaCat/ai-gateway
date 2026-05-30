package resilience

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
)

func testCfg() Config {
	return Config{MaxRetries: 2, BackoffBaseMs: 1, BackoffMaxMs: 2, BreakerThreshold: 2, BreakerCooldownMs: 50}
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
