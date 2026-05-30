package resilience

import "testing"

func TestResolve_NilOverrideUsesGlobal(t *testing.T) {
	g := Config{MaxRetries: 2, BackoffBaseMs: 100, BackoffMaxMs: 1000, BreakerThreshold: 5, BreakerCooldownMs: 30000}
	got := Resolve(g, nil)
	if got != g {
		t.Fatalf("nil override should equal global, got %+v", got)
	}
}

func TestResolve_PartialOverride(t *testing.T) {
	g := Config{MaxRetries: 2, BackoffBaseMs: 100, BackoffMaxMs: 1000, BreakerThreshold: 5, BreakerCooldownMs: 30000}
	three := 3
	got := Resolve(g, &ChannelResilience{MaxRetries: &three})
	if got.MaxRetries != 3 {
		t.Fatalf("MaxRetries override want 3 got %d", got.MaxRetries)
	}
	if got.BreakerThreshold != 5 {
		t.Fatalf("non-overridden field should stay global 5, got %d", got.BreakerThreshold)
	}
}

func TestResolve_ZeroPointerStillOverrides(t *testing.T) {
	// 指针非 nil 即覆盖,即便值为 0(显式关掉重试)。
	g := Config{MaxRetries: 2}
	zero := 0
	got := Resolve(g, &ChannelResilience{MaxRetries: &zero})
	if got.MaxRetries != 0 {
		t.Fatalf("explicit zero override want 0 got %d", got.MaxRetries)
	}
}
