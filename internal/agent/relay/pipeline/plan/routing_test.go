package plan

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func newTestStore() *cache.Store {
	return cache.NewStore(nil, config.AgentCacheConfig{})
}

func TestResolve_NoRouting(t *testing.T) {
	s := newTestStore()
	ctx := NewResolveCtx()
	if got := resolveToRealModel(s, "gpt-4o", 0, ctx); got != "gpt-4o" {
		t.Errorf("no routing → return original ref, got %q", got)
	}
}

func TestResolve_FlatGlobal(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 0, Weight: 3},
			{Ref: "b", Priority: 0, Weight: 1},
		},
	})
	countA, countB := 0, 0
	for i := 0; i < 1000; i++ {
		ctx := NewResolveCtx()
		result := resolveToRealModel(s, "smart", 0, ctx)
		switch result {
		case "a":
			countA++
		case "b":
			countB++
		default:
			t.Fatalf("unexpected result: %q", result)
		}
	}
	// 期望 ~75/25 比例，宽容 10% 偏差
	if countA < 650 || countA > 850 {
		t.Errorf("a should be ~75%%, got %d/1000", countA)
	}
}

func TestResolve_PrioritySkip(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 10, Weight: 1},
			{Ref: "b", Priority: 5, Weight: 1},
		},
	})
	// 一直选 a（高 priority）
	for i := 0; i < 100; i++ {
		ctx := NewResolveCtx()
		if got := resolveToRealModel(s, "smart", 0, ctx); got != "a" {
			t.Fatalf("high priority should win, got %q", got)
		}
	}
}

func TestResolve_AllExhausted(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 0, Weight: 1},
			{Ref: "b", Priority: 0, Weight: 1},
		},
	})
	ctx := NewResolveCtx()
	ctx.excluded[1] = map[string]bool{"a": true, "b": true}
	if got := resolveToRealModel(s, "smart", 0, ctx); got != "" {
		t.Errorf("all exhausted → empty, got %q", got)
	}
}

func TestResolve_Nested(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("cheap", &protocol.SyncedRouting{
		ID: 1, Name: "cheap", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "deepseek", Priority: 0, Weight: 1},
			{Ref: "qwen", Priority: 0, Weight: 1},
		},
	})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"my": {
			ID: 2, Name: "my", Scope: "user", UserID: 42, Enabled: true,
			Members: []protocol.RoutingMember{
				{Ref: "cheap", Priority: 0, Weight: 1},
			},
		},
	})
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		ctx := NewResolveCtx()
		result := resolveToRealModel(s, "my", 42, ctx)
		seen[result] = true
	}
	if !seen["deepseek"] || !seen["qwen"] {
		t.Errorf("nested resolve should produce both deepseek and qwen, got %v", seen)
	}
	if seen[""] || seen["cheap"] || seen["my"] {
		t.Errorf("should never produce empty / routing names: %v", seen)
	}
}

func TestResolve_CycleDefense(t *testing.T) {
	s := newTestStore()
	// 直接绕过保存校验制造环：A → B → A
	s.SetGlobalRouting("A", &protocol.SyncedRouting{
		ID: 1, Name: "A", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "B", Priority: 0, Weight: 1}},
	})
	s.SetGlobalRouting("B", &protocol.SyncedRouting{
		ID: 2, Name: "B", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "A", Priority: 0, Weight: 1}},
	})
	ctx := NewResolveCtx()
	result := resolveToRealModel(s, "A", 0, ctx)
	if result != "" {
		t.Errorf("cycle should return empty (no real model), got %q", result)
	}
}

func TestResolve_DepthLimit(t *testing.T) {
	s := newTestStore()
	// R1 → R2 → R3 → R4 → R5 → R6 → realModel
	chain := []string{"R1", "R2", "R3", "R4", "R5", "R6"}
	for i, name := range chain {
		nextRef := "realModel"
		if i+1 < len(chain) {
			nextRef = chain[i+1]
		}
		s.SetGlobalRouting(name, &protocol.SyncedRouting{
			ID: uint(i + 1), Name: name, Scope: "global", Enabled: true,
			Members: []protocol.RoutingMember{{Ref: nextRef, Priority: 0, Weight: 1}},
		})
	}
	ctx := NewResolveCtx()
	result := resolveToRealModel(s, "R1", 0, ctx)
	// 深度 5 限制：第 6 层（R6）会被拦截，整链失败
	if result != "" {
		t.Errorf("depth > 5 should fail resolution, got %q", result)
	}
}

func TestResolve_AfterMarkExhausted(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "a", Priority: 0, Weight: 1},
			{Ref: "b", Priority: 0, Weight: 1},
		},
	})
	ctx := NewResolveCtx()
	first := resolveToRealModel(s, "smart", 0, ctx)
	if first != "a" && first != "b" {
		t.Fatalf("first resolve unexpected: %q", first)
	}
	ctx.MarkMemberExhausted(first)
	second := resolveToRealModel(s, "smart", 0, ctx)
	if second == first || (second != "a" && second != "b") {
		t.Errorf("second resolve should pick the other; first=%q second=%q", first, second)
	}
}

func TestResolve_AfterMarkExhausted_Nested(t *testing.T) {
	s := newTestStore()
	s.SetGlobalRouting("cheap", &protocol.SyncedRouting{
		ID: 1, Name: "cheap", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "deepseek", Priority: 0, Weight: 1},
			{Ref: "qwen", Priority: 0, Weight: 1},
		},
	})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"my": {
			ID: 2, Name: "my", Scope: "user", UserID: 42, Enabled: true,
			Members: []protocol.RoutingMember{
				{Ref: "cheap", Priority: 0, Weight: 1},
			},
		},
	})
	ctx := NewResolveCtx()
	first := resolveToRealModel(s, "my", 42, ctx)
	if first != "deepseek" && first != "qwen" {
		t.Fatalf("first resolve unexpected: %q", first)
	}
	ctx.MarkMemberExhausted(first)
	second := resolveToRealModel(s, "my", 42, ctx)
	if second == first || (second != "deepseek" && second != "qwen") {
		t.Errorf("after MarkMemberExhausted nested: first=%q second=%q (should differ, both real models)", first, second)
	}
}

// 路由 gpt-5.5 的最高优先级成员就是同名真实模型 gpt-5.5：应解析为该真实模型，而非误判成环。
func TestResolve_SelfNameShadowsRealModel(t *testing.T) {
	s := newTestStore()
	ch1 := &models.Channel{Models: "gpt-5.5"}
	ch1.ID = 1
	ch1.Status = consts.StatusEnabled
	s.SetChannel(ch1)
	s.RebuildModelIndex()
	s.SetGlobalRouting("gpt-5.5", &protocol.SyncedRouting{
		ID: 1, Name: "gpt-5.5", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "gpt-5.5", Priority: 10, Weight: 1}, // 主：同名真实模型
			{Ref: "gpt-4o", Priority: 0, Weight: 1},   // 降级
		},
	})
	ctx := NewResolveCtx()
	if got := resolveToRealModel(s, "gpt-5.5", 0, ctx); got != "gpt-5.5" {
		t.Errorf("self-name member should resolve to real model gpt-5.5, got %q", got)
	}
}

// off-path 同名冲突：路由 outer 的成员 N 既是全局路由又有同名真实模型。
// N 不在当前路径上 → 应展开为路由（routing wins），最终解析到 N 的成员 realM，
// 而非短路成同名真实模型 N。这道防线防止有人把 HasRealModel 判断挪到 visited 检查之前。
func TestResolve_OffPathRoutingWinsOverSameNameModel(t *testing.T) {
	s := newTestStore()
	// channel 同时提供 "N" 和 "realM" 两个真实模型名
	ch1 := &models.Channel{Models: "N,realM"}
	ch1.ID = 1
	ch1.Status = consts.StatusEnabled
	s.SetChannel(ch1)
	s.RebuildModelIndex()
	s.SetGlobalRouting("N", &protocol.SyncedRouting{
		ID: 1, Name: "N", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "realM", Priority: 0, Weight: 1}},
	})
	s.SetGlobalRouting("outer", &protocol.SyncedRouting{
		ID: 2, Name: "outer", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "N", Priority: 0, Weight: 1}},
	})
	ctx := NewResolveCtx()
	if got := resolveToRealModel(s, "outer", 0, ctx); got != "realM" {
		t.Errorf("off-path routing N should expand to its member realM (routing wins), got %q", got)
	}
}

// 主成员（同名真实模型）耗尽后，应降级到次成员。
func TestResolve_SelfNameFallbackAfterExhaust(t *testing.T) {
	s := newTestStore()
	ch1 := &models.Channel{Models: "gpt-5.5"}
	ch1.ID = 1
	ch1.Status = consts.StatusEnabled
	s.SetChannel(ch1)
	s.RebuildModelIndex()
	s.SetGlobalRouting("gpt-5.5", &protocol.SyncedRouting{
		ID: 1, Name: "gpt-5.5", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{
			{Ref: "gpt-5.5", Priority: 10, Weight: 1},
			{Ref: "gpt-4o", Priority: 0, Weight: 1},
		},
	})
	ctx := NewResolveCtx()
	first := resolveToRealModel(s, "gpt-5.5", 0, ctx)
	if first != "gpt-5.5" {
		t.Fatalf("first resolve should be gpt-5.5, got %q", first)
	}
	ctx.MarkMemberExhausted(first)
	if second := resolveToRealModel(s, "gpt-5.5", 0, ctx); second != "gpt-4o" {
		t.Errorf("after exhaust primary, should fall back to gpt-4o, got %q", second)
	}
}
