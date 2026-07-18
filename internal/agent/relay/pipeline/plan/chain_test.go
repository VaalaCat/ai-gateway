package plan

import (
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// TestBuildChainFromStore_NoRouting: boundary — store 里没 routing → chain 只有 1 个真实 model。
func TestBuildChainFromStore_NoRouting(t *testing.T) {
	rs := &stubRoutingStore{}
	rctx := newTestRelayContext(nil, "gpt-4", &app.UserInfo{UserID: 1}, 0)

	chain := buildChainFromStore(rs, rctx)
	if !reflect.DeepEqual(chain.Models, []string{"gpt-4"}) {
		t.Errorf("Models = %v, want [gpt-4]", chain.Models)
	}
	if chain.RoutingName != "" {
		t.Errorf("RoutingName = %q, want empty", chain.RoutingName)
	}
}

// TestBuildChainFromStore_WithMembers: success — routing 有 2 个 member → chain 长度 2。
func TestBuildChainFromStore_WithMembers(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "A", Priority: 5, Weight: 1},
					{Ref: "B", Priority: 1, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 1}, 0)

	chain := buildChainFromStore(rs, rctx)
	if len(chain.Models) != 2 {
		t.Fatalf("Models length = %d, want 2 (%v)", len(chain.Models), chain.Models)
	}
	// A priority 5 > B priority 1 → A 先
	if chain.Models[0] != "A" || chain.Models[1] != "B" {
		t.Errorf("Models = %v, want [A B]", chain.Models)
	}
	if chain.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want smart", chain.RoutingName)
	}
}

// TestBuildChainFromStore_EmptyInput: boundary — Input.Model = "" → chain 退化为 [""] 或空。
// 当前 ResolveToRealModel("") 返回 ""（因为 routing 查不到 + 当真实 model 名直接返回 ref）。
// 实际：空串走 store.ResolveRouting("", 0) → nil → 返回 ref 即 ""。
// ResolveToRealModel 返回 ""，buildChainFromStore 当作整链失败处理 → Models 长度 0。
func TestBuildChainFromStore_EmptyInput(t *testing.T) {
	rs := &stubRoutingStore{}
	rctx := newTestRelayContext(nil, "", &app.UserInfo{UserID: 1}, 0)

	chain := buildChainFromStore(rs, rctx)
	if len(chain.Models) != 0 {
		t.Errorf("empty input → Models should be empty, got %v", chain.Models)
	}
}

// TestBuildChainFromStore_UserScopeWinsOverGlobal: success — user routing 优先于 global routing。
func TestBuildChainFromStore_UserScopeWinsOverGlobal(t *testing.T) {
	rs := &stubRoutingStore{
		user: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 2, Name: "smart", Scope: "user", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "user-only", Priority: 1, Weight: 1},
				},
			},
		},
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "global-only", Priority: 1, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 42}, 0)

	chain := buildChainFromStore(rs, rctx)
	if len(chain.Models) == 0 || chain.Models[0] != "user-only" {
		t.Errorf("user routing should win, got Models = %v", chain.Models)
	}
}

func TestBuildChainFromStore_TokenScopeWinsOverUserAndGlobal(t *testing.T) {
	rs := &stubRoutingStore{
		token: map[string]*protocol.SyncedRouting{
			"smart": {ID: 30, Name: "smart", Scope: "token", TokenID: 7, Enabled: true, Members: []protocol.RoutingMember{{Ref: "token-model", Weight: 1}}},
		},
		user: map[string]*protocol.SyncedRouting{
			"smart": {ID: 20, Name: "smart", Scope: "user", UserID: 42, Enabled: true, Members: []protocol.RoutingMember{{Ref: "user-model", Weight: 1}}},
		},
		global: map[string]*protocol.SyncedRouting{
			"smart": {ID: 10, Name: "smart", Scope: "global", Enabled: true, Members: []protocol.RoutingMember{{Ref: "global-model", Weight: 1}}},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 42, TokenID: 7}, 0)
	chain := buildChainFromStore(rs, rctx)
	if len(chain.Models) != 1 || chain.Models[0] != "token-model" {
		t.Fatalf("chain = %+v", chain)
	}
	if len(chain.Trace) != 1 || chain.Trace[0] != "token:smart" {
		t.Fatalf("trace = %v", chain.Trace)
	}
}

// TestRoutingChainBuilder_Build_ThroughStubAgentCache: wire-up smoke。
// routingChainBuilder.Build → routingStoreFromContext → stubAgentCache（满足 RoutingStore）。
func TestRoutingChainBuilder_Build_ThroughStubAgentCache(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "real-A", Priority: 1, Weight: 1},
				},
			},
		},
	}
	cache := &stubAgentCache{rs: rs}
	rctx := newTestRelayContext(cache, "smart", &app.UserInfo{UserID: 1}, 0)

	chain := routingChainBuilder{}.Build(rctx)
	if len(chain.Models) != 1 || chain.Models[0] != "real-A" {
		t.Errorf("Models = %v, want [real-A]", chain.Models)
	}
	if chain.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want smart", chain.RoutingName)
	}
}

// TestRoutingChainBuilder_Build_NilContext: 边界 — Build(nil) 不 panic，返回空 chain。
func TestRoutingChainBuilder_Build_NilContext(t *testing.T) {
	// 这里测 buildChainFromStore 对 nil rctx 防御
	chain := buildChainFromStore(&stubRoutingStore{}, nil)
	if len(chain.Models) != 0 || chain.RoutingName != "" {
		t.Errorf("nil rctx → empty chain, got %+v", chain)
	}
}

// ---- Trace 字段断言：UsageLog.RoutingTrace 的源头，必须钉死 ----
// resolveCtx.Trace() 一次拍照写到 UsageLog.Other.routing_trace；
// Plan.Trace = chain.Trace 由 buildChainFromStore 产生。

func TestBuildChainFromStore_Trace_NonEmptyOnRoutingHit(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "real-A", Priority: 1, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Trace) == 0 {
		t.Errorf("routing hit should produce non-empty Trace, got %v", chain.Trace)
	}
}

func TestBuildChainFromStore_Trace_EmptyOnPassthrough(t *testing.T) {
	// 没 routing 命中 → Trace 应该空（直接 passthrough）。
	rs := &stubRoutingStore{}
	rctx := newTestRelayContext(nil, "gpt-4", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Trace) != 0 {
		t.Errorf("passthrough should have empty Trace, got %v", chain.Trace)
	}
}

func TestBuildChainFromStore_Trace_PreservedOnFailure(t *testing.T) {
	// 边界：链整体失败时（empty input）Trace 仍然能被 caller 读到（即便为空）。
	// 防止"失败路径返回 nil chain 让 caller 丢 Trace"的回归。
	rs := &stubRoutingStore{}
	rctx := newTestRelayContext(nil, "", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Models) != 0 {
		t.Errorf("empty input → Models empty, got %v", chain.Models)
	}
	// chain.Trace 字段必须存在（可为 empty slice），不应 panic 读取
	_ = chain.Trace
}

// TestBuildChainFromStore_TraceMatchesMainSnapshot: success path —
// 验证 ModelChain.Trace 仅包含首次 Resolve 后的 trace 快照，
// 不被后续 MarkMemberExhausted + 重 Resolve 累加放大。
// 这是 routing_trace 几何级数 bug 的核心断言。
func TestBuildChainFromStore_TraceMatchesMainSnapshot(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "a", Priority: 10, Weight: 1},
					{Ref: "b", Priority: 5, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Models) != 2 || chain.Models[0] != "a" || chain.Models[1] != "b" {
		t.Errorf("Models = %v, want [a, b]", chain.Models)
	}
	if len(chain.Trace) != 1 || chain.Trace[0] != "global:smart" {
		t.Errorf("Trace = %v, want [\"global:smart\"] (main parity: single snapshot, not amplified by fallback Resolves)", chain.Trace)
	}
}

// TestBuildChainFromStore_TraceOnFirstResolveFailure: failure path —
// 首次 Resolve 失败（cycle/depth_exceeded）时 Trace 仍是该次 push 的快照。
func TestBuildChainFromStore_TraceOnFirstResolveFailure(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"cyclic": {
				ID: 1, Name: "cyclic", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{{Ref: "cyclic", Priority: 1, Weight: 1}},
			},
		},
	}
	rctx := newTestRelayContext(nil, "cyclic", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Models) != 0 {
		t.Errorf("Models = %v, want empty on cycle", chain.Models)
	}
	if len(chain.Trace) < 1 {
		t.Errorf("Trace should be non-empty even on cycle, got %v", chain.Trace)
	}
}

// TestBuildChainFromStore_TraceNoRouting: boundary —
// 非 routing 入参（直接是 model 名）→ Trace 空，Models 单条。
func TestBuildChainFromStore_TraceNoRouting(t *testing.T) {
	rs := &stubRoutingStore{global: map[string]*protocol.SyncedRouting{}}
	rctx := newTestRelayContext(nil, "gpt-4o", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)
	if len(chain.Models) != 1 || chain.Models[0] != "gpt-4o" {
		t.Errorf("Models = %v, want [gpt-4o]", chain.Models)
	}
	if len(chain.Trace) != 0 {
		t.Errorf("Trace = %v, want empty (non-routing path doesn't push)", chain.Trace)
	}
}

// TestBuildChainFromStore_MultiMemberChain_SingleTraceEntry verifies
// 7d776ca routing_trace single-snapshot fix in the multi-member case:
// routing smart with 5 members; ModelChain.Trace must be a single entry,
// not 5 (the pre-fix geometric blow-up累积同名 trace 写到 UsageLog).
// Mutation guard：把 chain.go 的 Trace=firstTrace 改回 Trace=ctx.Trace()
// 后这个测试会失败（实际值 = 5 条 "global:smart"）。
func TestBuildChainFromStore_MultiMemberChain_SingleTraceEntry(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "a", Priority: 50, Weight: 1},
					{Ref: "b", Priority: 40, Weight: 1},
					{Ref: "c", Priority: 30, Weight: 1},
					{Ref: "d", Priority: 20, Weight: 1},
					{Ref: "e", Priority: 10, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Models) != 5 {
		t.Fatalf("expected 5 resolved models, got %d: %v", len(chain.Models), chain.Models)
	}
	// priority 大的先：a > b > c > d > e
	wantModels := []string{"a", "b", "c", "d", "e"}
	for i, m := range wantModels {
		if chain.Models[i] != m {
			t.Errorf("Models[%d] = %q, want %q (full %v)", i, chain.Models[i], m, chain.Models)
		}
	}
	if len(chain.Trace) != 1 {
		t.Fatalf("expected single Trace entry for multi-member chain, got %d: %v "+
			"(若是 5 条同名 = chain.go 把 Trace=firstTrace 改回 ctx.Trace() 的回归)",
			len(chain.Trace), chain.Trace)
	}
	if chain.Trace[0] != "global:smart" {
		t.Errorf("Trace[0] = %q, want \"global:smart\"", chain.Trace[0])
	}
	if chain.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want smart", chain.RoutingName)
	}
}

// TestBuildChainFromStore_NestedRouting_SingleTraceEntry verifies that
// routing → routing 嵌套（global:smart → tier1 → real model）的 trace
// 仍只取第一次 Resolve 的快照，不被后续 fallback Resolve 累加。
//
// 首次 Resolve 走 smart → tier1 → "gpt-4"，trace 会推两条
// "global:smart" + "global:tier1"；fallback 没有更多 member 时
// 第二次 Resolve 返回 "" 立刻 break。关键断言：trace 长度稳定为 2，
// 不会被回归 ctx.Trace() 后倍增为 4。
func TestBuildChainFromStore_NestedRouting_SingleTraceEntry(t *testing.T) {
	rs := &stubRoutingStore{
		global: map[string]*protocol.SyncedRouting{
			"smart": {
				ID: 1, Name: "smart", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "tier1", Priority: 1, Weight: 1},
				},
			},
			"tier1": {
				ID: 2, Name: "tier1", Scope: "global", Enabled: true,
				Members: []protocol.RoutingMember{
					{Ref: "gpt-4", Priority: 1, Weight: 1},
				},
			},
		},
	}
	rctx := newTestRelayContext(nil, "smart", &app.UserInfo{UserID: 1}, 0)
	chain := buildChainFromStore(rs, rctx)

	if len(chain.Models) != 1 || chain.Models[0] != "gpt-4" {
		t.Fatalf("Models = %v, want [gpt-4]", chain.Models)
	}
	// 第一次 Resolve push 了 "global:smart" + "global:tier1"，
	// fallback 没有其它 member 立刻 break——trace 长度应稳定 2。
	if len(chain.Trace) != 2 {
		t.Fatalf("expected 2 Trace entries for one-pass nested routing, got %d: %v "+
			"(>2 = chain.go 把 Trace=firstTrace 改回 ctx.Trace() 后被 fallback 累加)",
			len(chain.Trace), chain.Trace)
	}
	if chain.Trace[0] != "global:smart" || chain.Trace[1] != "global:tier1" {
		t.Errorf("Trace = %v, want [global:smart global:tier1]", chain.Trace)
	}
}
