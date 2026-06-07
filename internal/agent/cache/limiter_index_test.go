package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func names(ls []*models.RequestLimiter) map[string]int64 {
	m := map[string]int64{}
	for _, l := range ls {
		m[l.Name] = l.Capacity
	}
	return m
}

func TestLimiterIndex_RequestLevel_NearestOverride(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "global-user-rate", Enabled: true, Metric: "rate", Capacity: 60, WindowMs: 60000, KeyBy: "per_user"},
		{ID: 2, Name: "vip-user-rate", Enabled: true, Metric: "rate", Capacity: 600, WindowMs: 60000, KeyBy: "per_user"},
		{ID: 3, Name: "group-total", Enabled: true, Metric: "concurrency", Capacity: 50, KeyBy: "per_group"},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true},
		{ID: 11, LimiterID: 2, TargetType: "user_group", TargetID: 7, Enabled: true}, // VIP 组
		{ID: 12, LimiterID: 3, TargetType: "user_group", TargetID: 7, Enabled: true},
	})

	// VIP 组用户(group=7)：per_user 维度 group(11) 覆盖 global(10) → 600；外加 group-total。
	got := idx.EffectiveRequestLimiters(123, 7)
	require.Equal(t, map[string]int64{"vip-user-rate": 600, "group-total": 50}, names(got))

	// 普通用户(group=1)：只命中 global → 60。
	got = idx.EffectiveRequestLimiters(456, 1)
	require.Equal(t, map[string]int64{"global-user-rate": 60}, names(got))
}

func TestLimiterIndex_AttemptLevel_ChannelDefaultAndScope(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "ch-default", Enabled: true, Metric: "concurrency", Capacity: 20, KeyBy: "per_channel", ChannelScope: "admin"},
		{ID: 2, Name: "ch45-tight", Enabled: true, Metric: "concurrency", Capacity: 5, KeyBy: "per_channel", ChannelScope: "admin"},
		{ID: 3, Name: "all-scope", Enabled: true, Metric: "concurrency", Capacity: 9, KeyBy: "per_channel", ChannelScope: "all"},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true},
		{ID: 11, LimiterID: 2, TargetType: "channel", TargetID: 45, Enabled: true},
		{ID: 12, LimiterID: 3, TargetType: "global", Enabled: true},
	})

	// admin 渠道 45：per_channel 维度 channel(11) 覆盖 global(10) → 5；all-scope(3) 另一条同维度？
	// 注意 ch-default / ch45-tight / all-scope 同属 (concurrency, per_channel) 维度，
	// 精细度 channel(2) > global(0)，ch45-tight 胜出 → 5。
	got := idx.EffectiveAttemptLimiters(123, 1, "admin", 45)
	require.Equal(t, map[string]int64{"ch45-tight": 5}, names(got))

	// admin 渠道 99（无专属）：global 候选里 ch-default(admin) 与 all-scope(all) 同维度，
	// 都是 global 精细度，按 Priority/ID tie-break，ID 小的 ch-default 胜 → 20。
	got = idx.EffectiveAttemptLimiters(123, 1, "admin", 99)
	require.Equal(t, map[string]int64{"ch-default": 20}, names(got))

	// private 渠道 99：ch-default 是 admin scope 不命中，只剩 all-scope → 9。
	got = idx.EffectiveAttemptLimiters(123, 1, "private", 99)
	require.Equal(t, map[string]int64{"all-scope": 9}, names(got))
}

func TestLimiterIndex_DisabledAndDelete(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "x", Enabled: false, Metric: "rate", Capacity: 60, WindowMs: 60000, KeyBy: "per_user"},
	})
	idx.LoadBindings([]models.LimiterBinding{{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true}})
	require.Empty(t, idx.EffectiveRequestLimiters(1, 1)) // disabled limiter 不命中

	idx.PutLimiter(&models.RequestLimiter{ID: 1, Name: "x", Enabled: true, Metric: "rate", Capacity: 60, WindowMs: 60000, KeyBy: "per_user"})
	require.Len(t, idx.EffectiveRequestLimiters(1, 1), 1)

	idx.DeleteBinding(10)
	require.Empty(t, idx.EffectiveRequestLimiters(1, 1)) // 删绑定后不命中
}

// TestLimiterIndex_RequestLevel_ThreeLayerSpecificity 验证同维度三层(global/group/user)
// 同时命中时取最具体的 user 那条（specificity user(3) > channel(2) > group(1) > global(0)）。
func TestLimiterIndex_RequestLevel_ThreeLayerSpecificity(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "g", Enabled: true, Metric: "rate", Capacity: 10, WindowMs: 60000, KeyBy: "per_user"},
		{ID: 2, Name: "grp", Enabled: true, Metric: "rate", Capacity: 20, WindowMs: 60000, KeyBy: "per_user"},
		{ID: 3, Name: "usr", Enabled: true, Metric: "rate", Capacity: 30, WindowMs: 60000, KeyBy: "per_user"},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true},
		{ID: 11, LimiterID: 2, TargetType: "user_group", TargetID: 7, Enabled: true},
		{ID: 12, LimiterID: 3, TargetType: "user", TargetID: 123, Enabled: true},
	})

	// 用户 123（属组 7）三层全命中同一 (rate, per_user) 维度 → user 最具体 → 30。
	got := idx.EffectiveRequestLimiters(123, 7)
	require.Equal(t, map[string]int64{"usr": 30}, names(got))

	// 用户 999（属组 7）：无 user 绑定 → group 覆盖 global → 20。
	got = idx.EffectiveRequestLimiters(999, 7)
	require.Equal(t, map[string]int64{"grp": 20}, names(got))
}

// TestLimiterIndex_AttemptLevel_ScopeAllMatchesBothSources 验证 ChannelScope=all
// 对 admin 与 private 来源都命中；admin/private scope 仅各自来源命中。
func TestLimiterIndex_AttemptLevel_ScopeAllMatchesBothSources(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "all", Enabled: true, Metric: "concurrency", Capacity: 9, KeyBy: "per_channel", ChannelScope: "all"},
		{ID: 2, Name: "adm", Enabled: true, Metric: "rate", Capacity: 8, WindowMs: 1000, KeyBy: "per_channel", ChannelScope: "admin"},
		{ID: 3, Name: "priv", Enabled: true, Metric: "rate", Capacity: 7, WindowMs: 1000, KeyBy: "per_channel", ChannelScope: "private"},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true},
		{ID: 11, LimiterID: 2, TargetType: "global", Enabled: true},
		{ID: 12, LimiterID: 3, TargetType: "global", Enabled: true},
	})

	// admin 渠道：all + adm 命中（不同维度，各自一条），priv 不命中。
	got := idx.EffectiveAttemptLimiters(1, 1, "admin", 5)
	require.Equal(t, map[string]int64{"all": 9, "adm": 8}, names(got))

	// private 渠道：all + priv 命中，adm 不命中。
	got = idx.EffectiveAttemptLimiters(1, 1, "private", 5)
	require.Equal(t, map[string]int64{"all": 9, "priv": 7}, names(got))
}

func TestLimiterIndex_Limiter(t *testing.T) {
	li := NewLimiterIndex()
	li.LoadLimiters([]models.RequestLimiter{{ID: 7, Name: "free", Metric: "concurrency", KeyBy: "per_user", Capacity: 3}})
	l := li.Limiter(7)
	if l == nil || l.Name != "free" || l.Capacity != 3 {
		t.Fatalf("got %+v", l)
	}
	if li.Limiter(999) != nil {
		t.Fatal("missing id should be nil")
	}
}

// TestLimiterIndex_RequestLevel_TieByPriorityThenID 验证同维度同精细度并列时
// 先按 Priority 高者胜，再按 ID 小者胜。
func TestLimiterIndex_RequestLevel_TieByPriorityThenID(t *testing.T) {
	idx := NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "lo-prio", Enabled: true, Metric: "rate", Capacity: 10, WindowMs: 60000, KeyBy: "per_user", Priority: 1},
		{ID: 2, Name: "hi-prio", Enabled: true, Metric: "rate", Capacity: 20, WindowMs: 60000, KeyBy: "per_user", Priority: 5},
		{ID: 3, Name: "hi-prio-bigid", Enabled: true, Metric: "rate", Capacity: 30, WindowMs: 60000, KeyBy: "per_user", Priority: 5},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 10, LimiterID: 1, TargetType: "global", Enabled: true},
		{ID: 11, LimiterID: 2, TargetType: "global", Enabled: true},
		{ID: 12, LimiterID: 3, TargetType: "global", Enabled: true},
	})

	// 三条同 (rate, per_user) 全 global 精细度并列：Priority 5 > 1，淘汰 lo-prio；
	// hi-prio(ID=2) 与 hi-prio-bigid(ID=3) 同 Priority，ID 小者胜 → hi-prio(20)。
	got := idx.EffectiveRequestLimiters(1, 1)
	require.Equal(t, map[string]int64{"hi-prio": 20}, names(got))
}
