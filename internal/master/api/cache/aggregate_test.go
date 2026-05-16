package cache

import (
	"math"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-6
}

func TestAggregate_TwoOnlineAgentsSumLRUEntities(t *testing.T) {
	inputs := []AgentSnapshot{
		{
			AgentID: "a1",
			Online:  true,
			CacheStats: map[string]protocol.CacheEntityStats{
				"token": {Hits: 100, Misses: 10, Evictions: 1, NegativeHits: 2, Size: 80, Capacity: 100},
				"user":  {Hits: 50, Misses: 5, Evictions: 0, NegativeHits: 0, Size: 30, Capacity: 50},
			},
		},
		{
			AgentID: "a2",
			Online:  true,
			CacheStats: map[string]protocol.CacheEntityStats{
				"token": {Hits: 200, Misses: 20, Evictions: 4, NegativeHits: 1, Size: 90, Capacity: 100},
				"user":  {Hits: 70, Misses: 5, Evictions: 0, NegativeHits: 0, Size: 35, Capacity: 50},
			},
		},
	}

	got := Aggregate(inputs)

	tokenAgg, ok := got["token"]
	if !ok {
		t.Fatalf("token aggregate missing")
	}
	if tokenAgg.Hits != 300 || tokenAgg.Misses != 30 || tokenAgg.Evictions != 5 || tokenAgg.NegativeHits != 3 {
		t.Fatalf("token sums wrong: %+v", tokenAgg)
	}
	if tokenAgg.Size != 170 || tokenAgg.Capacity != 200 {
		t.Fatalf("token size/cap wrong: %+v", tokenAgg)
	}
	if tokenAgg.HitRate == nil || !almostEqual(*tokenAgg.HitRate, 300.0/330.0) {
		t.Fatalf("token hit_rate wrong: %+v", tokenAgg.HitRate)
	}
	if tokenAgg.Util == nil || !almostEqual(*tokenAgg.Util, 170.0/200.0) {
		t.Fatalf("token util wrong: %+v", tokenAgg.Util)
	}
	userAgg, ok := got["user"]
	if !ok {
		t.Fatalf("user aggregate missing")
	}
	if userAgg.Hits != 120 || userAgg.Misses != 10 || userAgg.Size != 65 || userAgg.Capacity != 100 {
		t.Fatalf("user sums wrong: %+v", userAgg)
	}
	if userAgg.HitRate == nil || !almostEqual(*userAgg.HitRate, 120.0/130.0) {
		t.Fatalf("user hit_rate wrong: %+v", userAgg.HitRate)
	}
	if userAgg.Util == nil || !almostEqual(*userAgg.Util, 65.0/100.0) {
		t.Fatalf("user util wrong: %+v", userAgg.Util)
	}
}

func TestAggregate_OfflineAgentSkipped(t *testing.T) {
	inputs := []AgentSnapshot{
		{AgentID: "online", Online: true, CacheStats: map[string]protocol.CacheEntityStats{
			"token": {Hits: 100, Misses: 0, Size: 1, Capacity: 10},
		}},
		{AgentID: "offline", Online: false, CacheStats: map[string]protocol.CacheEntityStats{
			"token": {Hits: 999, Misses: 999, Size: 999, Capacity: 999},
		}},
	}
	got := Aggregate(inputs)
	if got["token"].Hits != 100 {
		t.Fatalf("expected only online agent counted, got %+v", got["token"])
	}
	tok := got["token"]
	if tok.Size != 1 || tok.Capacity != 10 {
		t.Fatalf("offline agent size/capacity leaked into aggregate: %+v", tok)
	}
}

func TestAggregate_FullSyncEntityHitRateNull(t *testing.T) {
	inputs := []AgentSnapshot{
		{AgentID: "a1", Online: true, CacheStats: map[string]protocol.CacheEntityStats{
			"channel": {Hits: 0, Misses: 0, Size: 42, Capacity: 0},
		}},
	}
	got := Aggregate(inputs)
	ch := got["channel"]
	if ch.Size != 42 {
		t.Fatalf("channel size wrong: %+v", ch)
	}
	if ch.Capacity != 0 {
		t.Fatalf("channel capacity should remain 0: %+v", ch)
	}
	if ch.HitRate != nil {
		t.Fatalf("channel hit_rate must be nil for full-sync entity, got %v", *ch.HitRate)
	}
	if ch.Util != nil {
		t.Fatalf("channel util must be nil for full-sync entity, got %v", *ch.Util)
	}
}

func TestAggregate_NoSnapshotsReturnsAllSixEntitiesZero(t *testing.T) {
	got := Aggregate(nil)
	for _, name := range EntityNames {
		e, ok := got[name]
		if !ok {
			t.Fatalf("entity %s missing", name)
		}
		if e.Hits != 0 || e.Misses != 0 || e.Size != 0 {
			t.Fatalf("entity %s should be zero, got %+v", name, e)
		}
	}
}

func TestAggregate_RoutingEntities(t *testing.T) {
	inputs := []AgentSnapshot{
		{
			AgentID: "a1",
			Online:  true,
			CacheStats: map[string]protocol.CacheEntityStats{
				"model_routing": {Size: 3, Capacity: 0}, // FullCache
				"user_routings": {Hits: 100, Misses: 20, Evictions: 5, Size: 40, Capacity: 100},
			},
		},
		{
			AgentID: "a2",
			Online:  true,
			CacheStats: map[string]protocol.CacheEntityStats{
				"model_routing": {Size: 3, Capacity: 0},
				"user_routings": {Hits: 50, Misses: 10, Evictions: 2, Size: 30, Capacity: 100},
			},
		},
	}

	got := Aggregate(inputs)

	mr, ok := got["model_routing"]
	if !ok {
		t.Fatalf("missing 'model_routing' aggregate")
	}
	if mr.Size != 6 {
		t.Fatalf("model_routing size want 6, got %d", mr.Size)
	}
	if mr.Util != nil {
		t.Fatalf("model_routing util should be nil (FullCache, capacity=0), got %v", *mr.Util)
	}

	ur, ok := got["user_routings"]
	if !ok {
		t.Fatalf("missing 'user_routings' aggregate")
	}
	if ur.Hits != 150 || ur.Misses != 30 || ur.Evictions != 7 || ur.Size != 70 || ur.Capacity != 200 {
		t.Fatalf("user_routings sums wrong: %+v", ur)
	}
	if ur.HitRate == nil || !almostEqual(*ur.HitRate, 150.0/180.0) {
		t.Fatalf("user_routings hit_rate wrong: %+v", ur.HitRate)
	}
}
