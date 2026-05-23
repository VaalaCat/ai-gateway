package plan

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// newPlannerTestRctx 构造 Planner 测试用的最小 state.RelayContext。
// 复用 test_helpers_chain_test.go 的 stubAgentApp / stubAgentCache，仅注入 RetryMax。
func newPlannerTestRctx(channels []*models.Channel, ui *app.UserInfo, model string, retryMax int) *state.RelayContext {
	cache := &stubAgentCache{channels: channels}
	rctx := newTestRelayContext(cache, model, ui, 0)
	// 注入 RetryMax 配置——stubAgentApp 默认 cfg=nil 不满足 Planner 需求。
	rctx.Agent.(*stubAgentApp).cfg = &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: retryMax},
	}
	return rctx
}

// TestPlanner_Success: 链长 1 个 model + 2 个 enabled channel → Attempts 长度 2。
func TestPlanner_Success(t *testing.T) {
	chs := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled, Weight: 1}},
	}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 5)

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v, want nil", err)
	}
	if len(rctx.State.Plan.Attempts) != 2 {
		t.Errorf("Attempts len = %d, want 2", len(rctx.State.Plan.Attempts))
	}
	if got := rctx.State.Plan.Attempts[0].RealModel; got != "gpt-4" {
		t.Errorf("Attempts[0].RealModel = %q, want gpt-4", got)
	}
}

// TestPlanner_NoRoutableModel: Input.Model = "" → ChainBuilder.Build 返回空 Models → state.ErrNoRoutableModel。
func TestPlanner_NoRoutableModel(t *testing.T) {
	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "", 5)

	err := NewSolver().Solve(rctx)
	if err != state.ErrNoRoutableModel {
		t.Errorf("err = %v, want state.ErrNoRoutableModel", err)
	}
}

// TestPlanner_NoChannelAvailable: 有 model 但 store 没 channel → state.ErrNoChannelAvailable。
func TestPlanner_NoChannelAvailable(t *testing.T) {
	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "gpt-4", 5)

	err := NewSolver().Solve(rctx)
	if err != state.ErrNoChannelAvailable {
		t.Errorf("err = %v, want state.ErrNoChannelAvailable", err)
	}
}

// TestPlanner_RetryMaxTruncates: 3 个候选 channel，RetryMax=2 → 截断到 2。
func TestPlanner_RetryMaxTruncates(t *testing.T) {
	chs := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled, Weight: 1}},
	}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 2)

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v, want nil", err)
	}
	if got := len(rctx.State.Plan.Attempts); got != 2 {
		t.Errorf("Attempts len = %d, want 2 (RetryMax 截断)", got)
	}
}

// TestSolve_RetryMaxOne_SingleAttempt: RetryMax=1 边界（修审计 D-I4b / #28）。
// 之前覆盖了 RetryMax=0/2/4/5/7/10，唯独缺 1；off-by-one 容易漏。
// 3 channels（不同 Priority）+ RetryMax=1 →
//   - Plan.Attempts 长度必须 == 1
//   - 选中的必须是最高 Priority 的 channel（Sorter 按 priority 降序，截断后取首）。
func TestSolve_RetryMaxOne_SingleAttempt(t *testing.T) {
	chLow := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Priority: 5, Status: consts.StatusEnabled, Weight: 1}}
	chHigh := &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Priority: 10, Status: consts.StatusEnabled, Weight: 1}}
	chMid := &models.Channel{ChannelCore: models.ChannelCore{ID: 3, Priority: 1, Status: consts.StatusEnabled, Weight: 1}}

	rctx := newPlannerTestRctx(
		[]*models.Channel{chLow, chHigh, chMid},
		&app.UserInfo{},
		"gpt-4",
		1,
	)

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v, want nil", err)
	}
	if got := len(rctx.State.Plan.Attempts); got != 1 {
		t.Fatalf("Attempts len = %d, want 1 (RetryMax=1 截断)", got)
	}
	if got := rctx.State.Plan.Attempts[0].Channel.ID; got != chHigh.ID {
		t.Errorf("Attempts[0].Channel.ID = %d, want chHigh.ID=%d (最高 priority)",
			got, chHigh.ID)
	}
}

// TestSolve_RetryMaxZero_ReturnsRoutingFallback：B-C1 行为修复——
// Runtime.RetryMax <= 0 时 Plan.Solve 必须返回 state.ErrRoutingFallback
// （而非 state.ErrNoChannelAvailable）。
//
// 旧 HEAD（Task 9 done）：404 + "no channel available for model X (whitelist 后缀)"
// 新 HEAD：               502 + "no available channels"（state.StatusFromState 映射）
// main 对照：handler.go:271-273 主循环 attemptsLeft<=0 跳过 →
//   :553 else 分支 → 502 + consts.ErrNoChannelAvailable。
//
// 配套断言：Attempts 必须为空（budget=0 决不能产 attempt）。
func TestSolve_RetryMaxZero_ReturnsRoutingFallback(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 0)

	err := NewSolver().Solve(rctx)
	if err != state.ErrRoutingFallback {
		t.Errorf("err = %v, want state.ErrRoutingFallback (RetryMax=0 → 502 fallback)", err)
	}
	if len(rctx.State.Plan.Attempts) != 0 {
		t.Errorf("RetryMax=0 不该产生 attempt，got %d", len(rctx.State.Plan.Attempts))
	}
}

// TestPlanner_WhitelistSkipsAllChannels: channels 存在但 token 白名单全部排除 → state.ErrNoChannelAvailable。
func TestPlanner_WhitelistSkipsAllChannels(t *testing.T) {
	chs := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled, Weight: 1}},
	}
	ui := &app.UserInfo{AllowedChannelIDs: []uint{999}}
	rctx := newPlannerTestRctx(chs, ui, "gpt-4", 5)

	err := NewSolver().Solve(rctx)
	if err != state.ErrNoChannelAvailable {
		t.Errorf("err = %v, want state.ErrNoChannelAvailable (whitelist 全部排除)", err)
	}
}

// TestPlanner_TokenModelsBlocks: UserInfo.TokenModels 不包含 realModel → 二次白名单跳过；
// 整条链都被白名单拦 + 没走 routing → state.ErrModelNotAllowed（复刻老主循环 line 359）。
// 边界：验证 modelAllowedByWhitelist(ui 是值传递) 走到了路径。
func TestPlanner_TokenModelsBlocks(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	ui := &app.UserInfo{TokenModels: []string{"only-this-one"}}
	rctx := newPlannerTestRctx(chs, ui, "gpt-4", 5)

	err := NewSolver().Solve(rctx)
	if err != state.ErrModelNotAllowed {
		t.Errorf("err = %v, want state.ErrModelNotAllowed (TokenModels 排除 gpt-4，整条链被拦)", err)
	}
}

// TestPlanner_NilUserInfo: UserInfo = nil 必须不 panic；nil 视为"无白名单"放行。
// 边界：modelAllowedByWhitelist 接受值参数，Planner 必须 nil 防御后再解引用。
func TestPlanner_NilUserInfo(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	rctx := newPlannerTestRctx(chs, nil, "gpt-4", 5)

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v, want nil (nil ui 应放行)", err)
	}
	if len(rctx.State.Plan.Attempts) != 1 {
		t.Errorf("nil UserInfo 应放行：Attempts len = %d, want 1", len(rctx.State.Plan.Attempts))
	}
}

// TestPlanner_RoutingFallback_AllMembersEmpty_Returns502Sentinel:
// behavior parity with main:handler.go 502 fallback (lastErr==nil) 分支
//
// routing 路径下，整条 chain (Models 非空) 走完后每个 member 都没 channel → 老主循环
// 走 else 分支 → 502 + "no available channels"（consts.ErrNoChannelAvailable）。
// 新实现：Planner 在 RoutingName != "" + Attempts==0 时返回 state.ErrRoutingFallback，
// state.StatusFromState 映射到 502。
//
// 非 routing 路径在相同场景下走 state.ErrNoChannelAvailable → 404（见 TestPlanner_NoChannelAvailable）。
// 两路状态码不同是 main 的现状行为，必须保留。
func TestPlanner_RoutingFallback_AllMembersEmpty_Returns502Sentinel(t *testing.T) {
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
	cache := &stubAgentCache{rs: rs, channels: nil} // 每个 member 都 0 channel
	rctx := newTestRelayContext(cache, "smart", &app.UserInfo{UserID: 1}, 0)
	rctx.Agent.(*stubAgentApp).cfg = &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: 5},
	}

	err := NewSolver().Solve(rctx)
	if err != state.ErrRoutingFallback {
		t.Fatalf("err = %v, want state.ErrRoutingFallback (routing 链耗尽走 502 分支)", err)
	}
	// Plan.RoutingName 必须被填，确保 state.StatusFromState / publish.Publisher 拿得到 routing 名。
	if rctx.State.Plan.RoutingName != "smart" {
		t.Errorf("Plan.RoutingName = %q, want smart", rctx.State.Plan.RoutingName)
	}
}

// TestPlanner_NonRoutingNoChannel_StaysAt404Sentinel: 反例——非 routing 路径下 Attempts 空仍走
// state.ErrNoChannelAvailable，对应 main:handler.go inner-loop len(channels)==0 → 404 分支 的 404 路径。两路必须分开。
//
// behavior parity with main:handler.go inner-loop len(channels)==0 → 404 分支 (inner-loop len(channels)==0 → 404)
func TestPlanner_NonRoutingNoChannel_StaysAt404Sentinel(t *testing.T) {
	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "gpt-4", 5)

	err := NewSolver().Solve(rctx)
	if err != state.ErrNoChannelAvailable {
		t.Errorf("err = %v, want state.ErrNoChannelAvailable (非 routing 路径)", err)
	}
}

// ---- Plan.Trace 断言：UsageLog.Other.routing_trace 字段的源头 ----
// Plan.Trace = chain.Trace 是 publish.Publisher 的 buildOtherJSON 用来生成
// UsageLog.Other "routing_trace" 字段的来源。必须钉死以防止漂移。

// TestPlanner_TracePropagatedToPlan: routing 命中时 Plan.Trace 必须非空。
func TestPlanner_TracePropagatedToPlan(t *testing.T) {
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
	cache := &stubAgentCache{rs: rs, channels: []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
	}}
	rctx := newTestRelayContext(cache, "smart", &app.UserInfo{UserID: 1}, 0)
	rctx.Agent.(*stubAgentApp).cfg = &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: 5},
	}

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if len(rctx.State.Plan.Trace) == 0 {
		t.Errorf("routing hit should produce non-empty Plan.Trace, got %v",
			rctx.State.Plan.Trace)
	}
}

// TestPlanner_RoutingResolvedLogEmitted: routing 解析后 trace 非空时必须 emit 一条
// Info "routing resolved" 日志（main:handler.go routing resolved 诊断日志分支 老行为；refactor 之前漏写，本测试钉死）。
// 字段必须含 request_model / real_model / user_id / trace 全套，对齐 main parity。
func TestPlanner_RoutingResolvedLogEmitted(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

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
	cache := &stubAgentCache{rs: rs, channels: []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}},
	}}
	rctx := newTestRelayContext(cache, "smart", &app.UserInfo{UserID: 42}, 0)
	rctx.Agent.(*stubAgentApp).cfg = &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: 5},
	}
	rctx.Agent.(*stubAgentApp).logger = logger

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	entries := recorded.FilterMessage("routing resolved").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 'routing resolved' log, got %d (all=%v)", len(entries), recorded.All())
	}
	fields := entries[0].ContextMap()
	if fields["request_model"] != "smart" {
		t.Errorf("request_model = %v, want smart", fields["request_model"])
	}
	if fields["real_model"] != "real-A" {
		t.Errorf("real_model = %v, want real-A", fields["real_model"])
	}
	if fields["user_id"] != uint64(42) {
		t.Errorf("user_id = %v, want 42", fields["user_id"])
	}
	if _, ok := fields["trace"]; !ok {
		t.Errorf("trace field missing, got fields=%v", fields)
	}
}

// TestPlanner_RoutingResolvedLogSkippedWhenNoTrace: 非 routing 路径下 chain.Trace 为空，
// 不应 emit "routing resolved" 日志（对齐 main if 守护）。
func TestPlanner_RoutingResolvedLogSkippedWhenNoTrace(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 5)
	rctx.Agent.(*stubAgentApp).logger = logger

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	if got := recorded.FilterMessage("routing resolved").Len(); got != 0 {
		t.Errorf("non-routing → no 'routing resolved' log, got %d", got)
	}
}

// TestPlanner_TraceEmptyOnPassthrough: 非 routing → Plan.Trace 应为空。
func TestPlanner_TraceEmptyOnPassthrough(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 5)

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if len(rctx.State.Plan.Trace) != 0 {
		t.Errorf("non-routing → Plan.Trace empty, got %v", rctx.State.Plan.Trace)
	}
}

// TestPlanner_ModePickerInvoked: 验证 Picker 装配生效——单个 enabled channel 的 Mode 字段被填。
// 默认 ProtocolUnknown 走 legacy 分支（见 shouldUseLegacy）。
func TestPlanner_ModePickerInvoked(t *testing.T) {
	chs := []*models.Channel{{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled, Weight: 1}}}
	rctx := newPlannerTestRctx(chs, &app.UserInfo{}, "gpt-4", 5)
	// 不设置 InboundProto → ProtocolUnknown → 走 legacy 分支。

	if err := NewSolver().Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}
	if len(rctx.State.Plan.Attempts) != 1 {
		t.Fatalf("Attempts len = %d, want 1", len(rctx.State.Plan.Attempts))
	}
	if rctx.State.Plan.Attempts[0].Mode != state.ModeLegacy {
		t.Errorf("Mode = %q, want %q (ProtocolUnknown 应走 legacy)",
			rctx.State.Plan.Attempts[0].Mode, state.ModeLegacy)
	}
}

// ---- 多 realModel × 多 channel 组合断言 ----
//
// 现有 TestPlanner_RetryMaxTruncates 只测了 "链长 1 + 3 channel + RetryMax=2 截断"。
// 下面两个用例补充 "链长 ≥ 2 + 每个 realModel 都有 channel" 的组合，
// 钉死 RetryMax 跨 model 跨 channel 截断的顺序契约（行为 1:1 复刻 handler.go 主循环）。

// staticChainBuilder 把 Build 返回的链固定下来，便于测试不依赖真实 routing 解析。
type staticChainBuilder struct {
	models      []string
	routingName string
	trace       []string
}

func (s staticChainBuilder) Build(*state.RelayContext) ModelChain {
	return ModelChain{Models: s.models, RoutingName: s.routingName, Trace: s.trace}
}

// staticPerModelPool 给每个 realModel 返回独立的 channel 切片。
// listKeys 用 RealModel 名 → channel 切片映射；未配置的 model 返回 nil。
type staticPerModelPool struct {
	byModel map[string][]*models.Channel
}

func (p staticPerModelPool) Available(_ *state.RelayContext, realModel string) []ScoredCandidate {
	return toScoredAdmin(p.byModel[realModel])
}

// identitySorter 不动顺序，保证测试可预期断言 Attempts 排序。
type identitySorter struct{}

func (identitySorter) Sort(cands []ScoredCandidate) []ScoredCandidate { return cands }

// newMultiModelPlanner 装出 staticChainBuilder + staticPerModelPool + identitySorter 的 Planner，
// 让 Plan() 行为完全可预测：按链顺序遍历 model，model 内按输入 channel 顺序展开。
func newMultiModelPlanner(chain []string, pool map[string][]*models.Channel) *defaultSolver {
	return &defaultSolver{
		ChainBuilder: staticChainBuilder{models: chain},
		Pool:         staticPerModelPool{byModel: pool},
		Sorter:       identitySorter{},
		Picker:       defaultModePicker{},
	}
}

// TestPlanner_MultiModel_RetryMaxTruncatesAcrossChain:
// 链长 2 (A→B)、每个 model 3 channel、RetryMax=4 →
// Attempts=[A.ch1, A.ch2, A.ch3, B.ch1]（先把 A 的 3 个填满，再填 B 第一个）。
// RealModel 字段必须按 chain 顺序：前 3 个是 A，第 4 个是 B。
func TestPlanner_MultiModel_RetryMaxTruncatesAcrossChain(t *testing.T) {
	chsA := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 11, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 12, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 13, Status: consts.StatusEnabled, Weight: 1}},
	}
	chsB := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 21, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 22, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 23, Status: consts.StatusEnabled, Weight: 1}},
	}
	pool := map[string][]*models.Channel{
		"A": chsA,
		"B": chsB,
	}
	planner := newMultiModelPlanner([]string{"A", "B"}, pool)

	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "user-facing", 4)

	if err := planner.Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	atts := rctx.State.Plan.Attempts
	if len(atts) != 4 {
		t.Fatalf("Attempts len = %d, want 4 (RetryMax=4)", len(atts))
	}

	// 前 3 个必须 RealModel=A，channel ID 按 chsA 顺序 11,12,13
	wantA := []uint{11, 12, 13}
	for i, want := range wantA {
		if atts[i].RealModel != "A" {
			t.Errorf("atts[%d].RealModel = %q, want A", i, atts[i].RealModel)
		}
		if atts[i].Channel.ID != want {
			t.Errorf("atts[%d].Channel.ID = %d, want %d", i, atts[i].Channel.ID, want)
		}
	}
	// 第 4 个必须 RealModel=B + channel ID=21（B 的第一个）
	if atts[3].RealModel != "B" {
		t.Errorf("atts[3].RealModel = %q, want B (跨 model)", atts[3].RealModel)
	}
	if atts[3].Channel.ID != 21 {
		t.Errorf("atts[3].Channel.ID = %d, want 21 (B 的第一个 channel)", atts[3].Channel.ID)
	}
}

// TestPlanner_MultiModel_RetryMaxNotTruncated:
// 链长 2 + 每 model 3 channel + RetryMax=10 → 全 6 个 attempt，不截断。
// 顺序：A.11, A.12, A.13, B.21, B.22, B.23。
func TestPlanner_MultiModel_RetryMaxNotTruncated(t *testing.T) {
	chsA := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 11, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 12, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 13, Status: consts.StatusEnabled, Weight: 1}},
	}
	chsB := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 21, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 22, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 23, Status: consts.StatusEnabled, Weight: 1}},
	}
	pool := map[string][]*models.Channel{
		"A": chsA,
		"B": chsB,
	}
	planner := newMultiModelPlanner([]string{"A", "B"}, pool)

	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "user-facing", 10)

	if err := planner.Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	atts := rctx.State.Plan.Attempts
	if len(atts) != 6 {
		t.Fatalf("Attempts len = %d, want 6 (RetryMax=10 不截断)", len(atts))
	}

	type expect struct {
		model string
		id    uint
	}
	want := []expect{
		{"A", 11}, {"A", 12}, {"A", 13},
		{"B", 21}, {"B", 22}, {"B", 23},
	}
	for i, w := range want {
		if atts[i].RealModel != w.model {
			t.Errorf("atts[%d].RealModel = %q, want %q", i, atts[i].RealModel, w.model)
		}
		if atts[i].Channel.ID != w.id {
			t.Errorf("atts[%d].Channel.ID = %d, want %d", i, atts[i].Channel.ID, w.id)
		}
	}
}

// TestPlanner_MultiModel_RetryMaxExactBoundary:
// 边界 case：RetryMax 正好 = 链 ×  channel 总数（4）→ 全填，不截断也不溢出。
// 链 [A,B] 每 model 2 channel + RetryMax=4 → 期望 4 个 attempt。
func TestPlanner_MultiModel_RetryMaxExactBoundary(t *testing.T) {
	chsA := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 11, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 12, Status: consts.StatusEnabled, Weight: 1}},
	}
	chsB := []*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 21, Status: consts.StatusEnabled, Weight: 1}},
		{ChannelCore: models.ChannelCore{ID: 22, Status: consts.StatusEnabled, Weight: 1}},
	}
	pool := map[string][]*models.Channel{
		"A": chsA,
		"B": chsB,
	}
	planner := newMultiModelPlanner([]string{"A", "B"}, pool)

	rctx := newPlannerTestRctx(nil, &app.UserInfo{}, "user-facing", 4)

	if err := planner.Solve(rctx); err != nil {
		t.Fatalf("Plan() err = %v", err)
	}

	atts := rctx.State.Plan.Attempts
	if len(atts) != 4 {
		t.Fatalf("Attempts len = %d, want 4 (RetryMax 正好打满)", len(atts))
	}
	if atts[1].RealModel != "A" || atts[2].RealModel != "B" {
		t.Errorf("RealModel 切换边界错误：atts[1]=%q atts[2]=%q, want A→B",
			atts[1].RealModel, atts[2].RealModel)
	}
}
