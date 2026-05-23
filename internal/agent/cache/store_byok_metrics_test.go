package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// TestStore_GetVisiblePrivateChannelsForUser_EmitsHitMetric 验证 LRU 命中时
// byok_visible_set_cache_hit_total +1，且 byok_visible_set_cache_miss_total
// 不动（不能 hit/miss 双计）。
func TestStore_GetVisiblePrivateChannelsForUser_EmitsHitMetric(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 42, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: 1}, OwnerID: 42, Models: []string{"gpt-4o"}},
	})

	beforeHit := readMetricCounter(t, metrics.BYOKVisibleSetCacheHit)
	beforeMiss := readMetricCounter(t, metrics.BYOKVisibleSetCacheMiss)

	_ = s.GetVisiblePrivateChannelsForUser(42, "gpt-4o")

	if got := readMetricCounter(t, metrics.BYOKVisibleSetCacheHit) - beforeHit; got != 1 {
		t.Fatalf("hit delta want 1, got %v", got)
	}
	if got := readMetricCounter(t, metrics.BYOKVisibleSetCacheMiss) - beforeMiss; got != 0 {
		t.Fatalf("miss must not change on hit; delta = %v", got)
	}
}

// TestStore_GetVisiblePrivateChannelsForUser_EmitsMissMetric 验证 LRU 未命中
// (loader=nil 时直接 miss）miss_total +1, hit 不动。
func TestStore_GetVisiblePrivateChannelsForUser_EmitsMissMetric(t *testing.T) {
	s := newTestStoreNoClient(t)

	beforeHit := readMetricCounter(t, metrics.BYOKVisibleSetCacheHit)
	beforeMiss := readMetricCounter(t, metrics.BYOKVisibleSetCacheMiss)

	if got := s.GetVisiblePrivateChannelsForUser(999, "gpt-4o"); got != nil {
		t.Fatalf("expected nil on miss, got %+v", got)
	}

	if got := readMetricCounter(t, metrics.BYOKVisibleSetCacheMiss) - beforeMiss; got != 1 {
		t.Fatalf("miss delta want 1, got %v", got)
	}
	if got := readMetricCounter(t, metrics.BYOKVisibleSetCacheHit) - beforeHit; got != 0 {
		t.Fatalf("hit must not change on miss; delta = %v", got)
	}
}

// TestStore_VisiblePrivateChannels_GaugeReflectsLRUSize 验证 cache size gauge
// 在 Get 与 Invalidate 后反映当前 Len。
func TestStore_VisiblePrivateChannels_GaugeReflectsLRUSize(t *testing.T) {
	s := newTestStoreNoClient(t)
	setVisiblePrivateChannelsForTest(s, 1, nil)
	setVisiblePrivateChannelsForTest(s, 2, nil)

	// 触发 Get → 内部 Set(Len()=2)
	_ = s.GetVisiblePrivateChannelsForUser(1, "gpt-4o")
	if got := readMetricGauge(t, metrics.BYOKVisibleSetCacheSize); got != 2 {
		t.Fatalf("after Get with 2 entries, size = %v, want 2", got)
	}

	s.InvalidateVisiblePrivateChannels(2)
	if got := readMetricGauge(t, metrics.BYOKVisibleSetCacheSize); got != 1 {
		t.Fatalf("after Invalidate, size = %v, want 1", got)
	}
}

func readMetricCounter(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetCounter().GetValue()
}

func readMetricGauge(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return m.GetGauge().GetValue()
}
