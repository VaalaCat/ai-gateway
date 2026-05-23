package exec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// stubSleep 实现 SleepReader，让测试控制 FallbackSleepMs 返回值，
// 供 TestExecutor_DefaultFallback_SleepsBetween 等用例注入。
type stubSleep struct {
	ms int
}

func (s stubSleep) FallbackSleepMs() int { return s.ms }

// recordingDispatcher 一次性返回预设的 state.AttemptResult 队列，记录调用计数。
// 队列中 callCount 超出长度时返回零值，方便检测"按预期不被多调"的场景。
//
// 直接实现 state.Dispatcher 接口（Dispatch 方法），代替 Task 4 之前的
// recordingDispatcher + map[state.RelayMode]RelayBackend{state.ModeNative: backend} 双层包装；
// 因为 exec_test.go 在 package exec 内，无法 import backend 子包，
// 直接注入 stub 进 Executor.Dispatcher 即可。
type recordingDispatcher struct {
	callCount int
	results   []state.AttemptResult
}

func (r *recordingDispatcher) Dispatch(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	if r.callCount >= len(r.results) {
		r.callCount++
		return state.AttemptResult{}
	}
	res := r.results[r.callCount]
	r.callCount++
	return res
}

// newTestExecutorRctx 构造一个最小可用的 state.RelayContext：Plan 已落到 State，
// Recorder 是 disabled（避免 trace 落盘）；Agent 由调用方按需注入。
func newTestExecutorRctx(plan state.AttemptPlan, agent app.AgentApplication) *state.RelayContext {
	return &state.RelayContext{
		Context: &gin.Context{},
		Agent:   agent,
		Input: state.RelayInput{
			Body:     []byte(`{"model":"x"}`),
			UserInfo: &app.UserInfo{TokenID: 1},
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0), Plan: plan},
	}
}

// stubExecAgent 是 Executor 测试用 agent stub：完全可控 forwarder / cache / logger。
// 嵌入 app.AgentApplication 为零接口，只覆盖测试用到的方法。
// logger 非 nil 时 GetLogger 返回它（log emit 用例用 observer.New 注入）。
type stubExecAgent struct {
	app.AgentApplication
	forwarder app.RouteForwarder
	cache     app.AgentCache
	logger    *zap.Logger
}

func (s *stubExecAgent) GetRouteForwarder() app.RouteForwarder { return s.forwarder }
func (s *stubExecAgent) GetCache() app.AgentCache              { return s.cache }
func (s *stubExecAgent) GetLogger() *zap.Logger {
	if s.logger != nil {
		return s.logger
	}
	return zap.NewNop()
}
func (s *stubExecAgent) GetConfig() *config.AgentRuntimeConfig { return nil }
func (s *stubExecAgent) GetTransportPool() app.TransportPool   { return stubExecTransportPool{} }

type stubExecTransportPool struct{}

func (stubExecTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubExecTransportPool) Invalidate(uint, string)             {}

// stubExecCache 嵌套 app.AgentCache 仅覆盖 MatchRoute（forward 决策用），
// 其它方法（Store 的 GetXxx）测试不触达。
type stubExecCache struct {
	app.AgentCache
	route *models.AgentRoute
}

func (c *stubExecCache) MatchRoute(uint, string, []uint) *models.AgentRoute { return c.route }

// stubForwarder 可控 ForwardByRoute 的返回值，方便覆盖 forward 命中 / 失败两条路径。
type stubForwarder struct {
	forwarded bool
	err       error
	calls     int
}

func (f *stubForwarder) ForwardByRoute(*gin.Context, *models.AgentRoute) (bool, error) {
	f.calls++
	return f.forwarded, f.err
}

// TestExecutorSuccessFirstAttempt 成功路径：第一次 attempt 成功 → 不再 retry。
func TestExecutorSuccessFirstAttempt(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	if rctx.State.Execution.Err != nil {
		t.Fatalf("unexpected err: %v", rctx.State.Execution.Err)
	}
	// dispatch 计数 + 终态 attempt 双断言：取代被删的 History len 检查
	if backend.callCount != 1 {
		t.Errorf("backend called %d times, want 1", backend.callCount)
	}
	if rctx.State.Execution.Used.Channel == nil || rctx.State.Execution.Used.Channel.ID != 1 {
		t.Errorf("Used.Channel should be ch=1, got %#v", rctx.State.Execution.Used.Channel)
	}
}

// TestExecutorRetryOnFail 第一次失败但 Written=false → 必须 retry 下一个 attempt。
func TestExecutorRetryOnFail(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("first failed"), Written: false},
		{PromptTokens: 7},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	// dispatch 2 次 + 终态 attempt = 第 2 个 channel：双断言验证 retry 链路完整推进
	if backend.callCount != 2 {
		t.Errorf("backend should be called 2 times, got %d", backend.callCount)
	}
	if rctx.State.Execution.Used.Channel == nil || rctx.State.Execution.Used.Channel.ID != 2 {
		t.Errorf("Used should land on retry channel (id=2), got %#v", rctx.State.Execution.Used.Channel)
	}
	if rctx.State.Execution.Err != nil {
		t.Errorf("final should succeed: %v", rctx.State.Execution.Err)
	}
}

// TestExecutorStopsOnWritten 失败 + Written=true（流已写出客户端）→ 不可 retry，立即 return。
func TestExecutorStopsOnWritten(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("stream broke"), Written: true},
		{PromptTokens: 99}, // should NOT be called
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	if backend.callCount != 1 {
		t.Errorf("Written should stop retry: called %d", backend.callCount)
	}
	if rctx.State.Execution.Err == nil {
		t.Error("Err should be set on terminal Written failure")
	}
}

// TestExecutorEmptyPlan 边界：空 Plan.Attempts 不应迭代，不应 panic，不应 err。
func TestExecutorEmptyPlan(t *testing.T) {
	d := &recordingDispatcher{}
	e := &Executor{Dispatcher: d}
	rctx := newTestExecutorRctx(state.AttemptPlan{}, &stubExecAgent{})
	e.Run(rctx)
	if rctx.State.Execution.Err != nil {
		t.Errorf("empty plan should not err: %v", rctx.State.Execution.Err)
	}
	// Used.Channel == nil 是"未曾 dispatch"的等价信号（取代被删的 History len 检查）
	if rctx.State.Execution.Used.Channel != nil {
		t.Errorf("Used.Channel should remain nil on empty plan, got %#v", rctx.State.Execution.Used.Channel)
	}
}

// TestExecutorMaybeForwardCommits 转发命中：forwarder.ForwardByRoute 返 true → backend 不应被调用。
func TestExecutorMaybeForwardCommits(t *testing.T) {
	backend := &recordingDispatcher{}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	cache := &stubExecCache{route: &models.AgentRoute{ID: 1, AgentID: "other-agent"}}
	fwd := &stubForwarder{forwarded: true}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})
	e.Run(rctx)

	if !rctx.State.Forwarded {
		t.Error("State.Forwarded should be true")
	}
	if backend.callCount != 0 {
		t.Errorf("backend should NOT be called when forwarded, called %d", backend.callCount)
	}
	if fwd.calls != 1 {
		t.Errorf("forwarder should be called once, got %d", fwd.calls)
	}
}

// TestExecutorMaybeForwardFailsToBackend 转发失败：forwarder 返 (false, err) → 降级到 backend。
func TestExecutorMaybeForwardFailsToBackend(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	cache := &stubExecCache{route: &models.AgentRoute{ID: 1}}
	fwd := &stubForwarder{forwarded: false, err: errors.New("network")}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})
	e.Run(rctx)

	if rctx.State.Forwarded {
		t.Error("Forwarded=false when ForwardByRoute returns false")
	}
	if backend.callCount != 1 {
		t.Errorf("backend should run when forward fails: called %d", backend.callCount)
	}
}

// TestExecutorNoForwarderSkipsForwardCheck 边界：GetRouteForwarder=nil 时 maybeForward 直接 false，
// 不应 panic 在 GetCache() 取 nil 上。
func TestExecutorNoForwarderSkipsForwardCheck(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 1}}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: nil, cache: nil})
	e.Run(rctx)

	if rctx.State.Forwarded {
		t.Error("Forwarded should be false when no forwarder")
	}
	if backend.callCount != 1 {
		t.Errorf("backend should run, got %d", backend.callCount)
	}
}

// TestMaybeForward_ForwarderPresentButCacheNil_SkipsForward 锁定 forward.go 的
// cache==nil 早返分支（审计 D-I4 / spec §5.2 #27）。
//
// 既有 TestExecutorNoForwarderSkipsForwardCheck 把 forwarder=nil 与 cache=nil
// 同时设了 nil，未独立覆盖"forwarder!=nil 但 cache==nil"的边界：
// 若 forward.go 的 `if cache == nil { return false }` 被误删，
// 后续 cache.MatchRoute 会 nil deref panic。
//
// 直接 call package-private maybeForward 而非走 Executor.Run，
// 验证 forward 决策本身的早返语义，避开 dispatch 路径噪声。
func TestMaybeForward_ForwarderPresentButCacheNil_SkipsForward(t *testing.T) {
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	// forwarder 非 nil（任何 stub 即可），cache 显式 nil。
	fwd := &stubForwarder{forwarded: true}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: nil})

	got := maybeForward(rctx, 0, &rctx.State.Plan)

	if got {
		t.Fatalf("maybeForward should return false when cache is nil, got %v", got)
	}
	if fwd.calls != 0 {
		t.Errorf("forwarder should NOT be called when cache is nil, calls=%d", fwd.calls)
	}
	// 抵达此处即说明没 panic — 早返保护生效。
}

// perChannelCache 让 MatchRoute 按调用顺序返回不同 route，模拟"只有部分 attempt 命中"。
// MatchRoute 入参 channelIDs 复制后落进 captured，供测试断言"传入的是当前 attempt 的 channel"。
type perChannelCache struct {
	app.AgentCache
	routes   []*models.AgentRoute // 按调用顺序消费
	captured [][]uint             // 每次 MatchRoute 传入的 channelIDs 副本
	calls    int
}

func (c *perChannelCache) MatchRoute(_ uint, _ string, channelIDs []uint) *models.AgentRoute {
	idsCopy := append([]uint(nil), channelIDs...)
	c.captured = append(c.captured, idsCopy)
	idx := c.calls
	c.calls++
	if idx >= len(c.routes) {
		return nil
	}
	return c.routes[idx]
}

// perCallForwarder 按调用顺序返回 (forwarded, err) 队列，并捕获每次传入的 route。
// 队列耗尽后均返回零值——便于测试"应仅被调 N 次"的契约。
type perCallForwarder struct {
	results []forwardResult
	routes  []*models.AgentRoute
	calls   int
}

type forwardResult struct {
	forwarded bool
	err       error
}

func (f *perCallForwarder) ForwardByRoute(_ *gin.Context, route *models.AgentRoute) (bool, error) {
	f.routes = append(f.routes, route)
	idx := f.calls
	f.calls++
	if idx >= len(f.results) {
		return false, nil
	}
	return f.results[idx].forwarded, f.results[idx].err
}

// TestExecutorPerAttemptForwardLateHit
// behavior change vs main: per-attempt forward decision (spec §1 exception)
//
// 三 attempt：前 2 attempt 没匹配 route（cache.MatchRoute=nil）→ 直接走 backend，
// 第 3 attempt 才匹配到 route 并 ForwardByRoute=(true, nil) → forwarded=true 终止。
// 验证 forward 决策按 attempt 评估，**不是** Plan 开局一次性决定。
func TestExecutorPerAttemptForwardLateHit(t *testing.T) {
	// 前 2 attempts 没有匹配 route → 直接 backend；让 backend 返回 retry-able 错误推进循环。
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch1 fail"), Written: false},
		{Err: errors.New("ch2 fail"), Written: false},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 3}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	cache := &perChannelCache{routes: []*models.AgentRoute{nil, nil, {ID: 42}}}
	// ForwardByRoute 仅在 MatchRoute 命中（第 3 attempt）时被调用 → 队列首项即对应第 3 attempt。
	fwd := &perCallForwarder{results: []forwardResult{{forwarded: true}}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})
	e.Run(rctx)

	if !rctx.State.Forwarded {
		t.Fatal("Forwarded should be true on 3rd attempt forward hit")
	}
	// backend 仅前 2 attempts 走（第 3 attempt 被 forward 接管，不走 backend）。
	if backend.callCount != 2 {
		t.Errorf("backend should be called 2 times (only attempts 1 & 2), got %d", backend.callCount)
	}
	// MatchRoute 必须每个 attempt 都问一次——证明决策是 per-attempt。
	if cache.calls != 3 {
		t.Errorf("MatchRoute should be evaluated per attempt (3 times), got %d", cache.calls)
	}
	// 只有命中 attempt 才会触发 ForwardByRoute。
	if fwd.calls != 1 {
		t.Errorf("ForwardByRoute should fire only on matched attempt, got %d calls", fwd.calls)
	}
}

// TestExecutorPerAttemptForwardAfterBackendFailures
// behavior change vs main: per-attempt forward decision (spec §1 exception)
//
// 三 attempt：前 2 attempt backend 失败（retry-able），第 3 attempt 才匹配 route + forward 命中。
// 验证 backend 失败链路下 forward 仍可在中途接管，最终 Forwarded=true 且 backend 调 2 次。
func TestExecutorPerAttemptForwardAfterBackendFailures(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("upstream 500"), Written: false},
		{Err: errors.New("upstream 502"), Written: false},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 11}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 22}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 33}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	// 前 2 不匹配 route → 走 backend；第 3 匹配 → 走 forward。
	cache := &perChannelCache{routes: []*models.AgentRoute{nil, nil, {ID: 7}}}
	fwd := &perCallForwarder{results: []forwardResult{{forwarded: true}}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})
	e.Run(rctx)

	if !rctx.State.Forwarded {
		t.Fatal("Forwarded should be true after backend failures, when 3rd attempt forwards")
	}
	if backend.callCount != 2 {
		t.Errorf("backend should run 2 times (attempts 1 & 2 failed), got %d", backend.callCount)
	}
	if fwd.calls != 1 {
		t.Errorf("ForwardByRoute should fire once (3rd attempt), got %d", fwd.calls)
	}
	// 最终终态是 forwarded，Err 不应当作"backend 最后一次错"暴露 —— Forwarded 后 Run 直接 return。
	if rctx.State.Execution.Err != nil {
		// History 中确有失败项但终态由 forward 接管，Err 字段不会设置
		// （Run 在 Forwarded 分支直接 return，不进入末尾 out.Err 赋值）。
		t.Errorf("Execution.Err should remain nil when forwarded, got %v", rctx.State.Execution.Err)
	}
}

// TestExecutorForwardDecisionUsesRemainingChannelBatch
// behavior change vs main: per-attempt forward decision (spec §1) +
// batch channelIDs aligned with main:handler.go:357-360 (审计 #12 修复)
//
// 验证 maybeForward 传给 cache.MatchRoute 的 channelIDs 是 **当前 attempt 起的
// 剩余 batch**（plan.Attempts[idx:] 的 channel ID，去重保序），而不是单个 channel.ID。
// per-attempt 时机评估 + batch 集合查询，是修复后的契约。
func TestExecutorForwardDecisionUsesRemainingChannelBatch(t *testing.T) {
	// 让 backend 也返回 retry-able 错误，确保 3 个 attempts 都被迭代 → 3 次 MatchRoute 调用。
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch101 fail"), Written: false},
		{Err: errors.New("ch202 fail"), Written: false},
		{Err: errors.New("ch303 fail"), Written: false},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 101}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 202}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 303}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	// MatchRoute 全部返 nil → 不进入 ForwardByRoute，但 captured 仍记录每次传入的 channelIDs。
	cache := &perChannelCache{routes: []*models.AgentRoute{nil, nil, nil}}
	fwd := &perCallForwarder{}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})
	e.Run(rctx)

	if cache.calls != 3 {
		t.Fatalf("expected MatchRoute called 3 times (per attempt), got %d", cache.calls)
	}
	// 每个 attempt 应传入"当前及后续"全部 channelID（去重保序）。
	wantBatches := [][]uint{
		{101, 202, 303}, // attempt 0: remaining = [101,202,303]
		{202, 303},      // attempt 1: remaining = [202,303]
		{303},           // attempt 2: remaining = [303]
	}
	for i, ids := range cache.captured {
		want := wantBatches[i]
		if len(ids) != len(want) {
			t.Errorf("attempt %d: MatchRoute batch len = %d (%v), want %d (%v)", i, len(ids), ids, len(want), want)
			continue
		}
		for j := range want {
			if ids[j] != want[j] {
				t.Errorf("attempt %d: MatchRoute batch[%d] = %d, want %d (full batch=%v)", i, j, ids[j], want[j], ids)
			}
		}
	}
	if fwd.calls != 0 {
		t.Errorf("ForwardByRoute should not fire when MatchRoute returns nil, got %d", fwd.calls)
	}
}

// TestExecutor_RelayAttemptFailedLogEmitted: 每次 backend.Relay 返 Err != nil 时
// 必须 emit 一条 Warn "relay attempt failed"（main:handler.go 老主循环 attempt 失败分支 老行为；refactor 之前
// 漏写，本测试钉死）。字段对齐 main parity：channel_id / attempts_left / path / error。
//
// 用例配置：2 个 attempt，第 1 个失败（retry-able）+ 第 2 个成功 → 仅 1 条失败日志。
func TestExecutor_RelayAttemptFailedLogEmitted(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch1 boom"), Written: false},
		{PromptTokens: 7},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 101}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 202}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{logger: logger})
	// 注入带 URL 的 Request，让日志能填 path 字段。
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	e.Run(rctx)

	entries := recorded.FilterMessage("relay attempt failed").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 'relay attempt failed' log, got %d (all=%v)", len(entries), recorded.All())
	}
	fields := entries[0].ContextMap()
	if fields["channel_id"] != uint64(101) {
		t.Errorf("channel_id = %v, want 101", fields["channel_id"])
	}
	// 2 个 attempt，失败发生在 idx=0 → attempts_left = 2-0-1 = 1
	if fields["attempts_left"] != int64(1) {
		t.Errorf("attempts_left = %v, want 1", fields["attempts_left"])
	}
	// path 字段对齐 main:handler.go 老主循环 attempt 失败分支 的 nativeOrLegacy(useLegacy) 语义：
	// legacy → "legacy"，其它（含 native / passthrough）→ "native"。
	if fields["path"] != "native" {
		t.Errorf("path = %v, want \"native\" (Mode=state.ModeNative)", fields["path"])
	}
	if fields["error"] != "ch1 boom" {
		t.Errorf("error = %v, want 'ch1 boom'", fields["error"])
	}
}

// TestExecutor_RelayAttemptFailedLogEmittedOnWritten: 失败 + Written=true（mid-stream fail）
// 也必须 emit 日志（在 return 之前）—— main 老行为在所有 result.Err != nil 路径都 log。
func TestExecutor_RelayAttemptFailedLogEmittedOnWritten(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("stream broke"), Written: true},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 7}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{logger: logger})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	e.Run(rctx)

	if got := recorded.FilterMessage("relay attempt failed").Len(); got != 1 {
		t.Fatalf("expected 1 'relay attempt failed' log on Written failure, got %d", got)
	}
}

// TestExecutor_RouteForwardingFailedLogMessage:
// "route forwarding failed, processing locally" 是 main:handler.go 老主循环的完整 warn 文案，
// ops 可能 grep 完整 message 触发告警，必须 byte-equal 不能漂移。
// 构造 forwarder 返回 (false, err) → 不命中 + 带 err 触发 warn 分支。
func TestExecutor_RouteForwardingFailedLogMessage(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	cache := &stubExecCache{route: &models.AgentRoute{ID: 42}}
	fwd := &stubForwarder{forwarded: false, err: errors.New("upstream agent unreachable")}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache, logger: logger})

	e.Run(rctx)

	entries := recorded.FilterMessage("route forwarding failed, processing locally").All()
	if len(entries) != 1 {
		t.Fatalf("want exactly 1 warn %q, got %d entries; all=%v",
			"route forwarding failed, processing locally", len(entries), recorded.All())
	}
	fields := entries[0].ContextMap()
	if fields["route_id"] != uint64(42) {
		t.Errorf("route_id = %v, want 42", fields["route_id"])
	}
	if fields["error"] != "upstream agent unreachable" {
		t.Errorf("error = %v, want 'upstream agent unreachable'", fields["error"])
	}
}

// TestRun_ForwardBatch_MatchesLaterChannel 钉死审计 #12 修复：
// 当 AgentRoute 配在 Plan 后面的 channel（非首个）时，maybeForward 必须用
// 剩余 attempts 的全部 channelIDs batch 查 route，第一轮就命中并 forward，
// 绝不会先 dispatch 头部 channel。
//
// 对照 main:handler.go:357-360：
//
//	channelIDs := make([]uint, len(channels))
//	for i, ch := range channels { channelIDs[i] = ch.ID }
//	if route := h.Store.RouteIndex.Match(userInfo.TokenID, realModel, channelIDs); route != nil { ... }
//
// 旧 HEAD 行为：attempt 0 查 [A] 不命中 → dispatch A → 失败 → attempt 1 查 [B] 命中
// 新 HEAD 行为：attempt 0 查 [A,B,C] 命中 B → forward（dispatcher 永远不被调用）
func TestRun_ForwardBatch_MatchesLaterChannel(t *testing.T) {
	chA := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Name: "A"}}
	chB := &models.Channel{ChannelCore: models.ChannelCore{ID: 2, Name: "B"}}
	chC := &models.Channel{ChannelCore: models.ChannelCore{ID: 3, Name: "C"}}

	route := &models.AgentRoute{ID: 42}
	// idChannelCache：当传入的 channelIDs 包含 chB.ID 时返回 route，否则 nil。
	// 用真实"按 ID 命中"的语义模拟 RouteIndex.Match。
	cache := &idChannelCache{routeByID: map[uint]*models.AgentRoute{chB.ID: route}}
	fwd := &perCallForwarder{results: []forwardResult{{forwarded: true}}}

	backend := &recordingDispatcher{}
	e := &Executor{Dispatcher: backend}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: chA, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: chB, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: chC, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{forwarder: fwd, cache: cache})

	e.Run(rctx)

	if !rctx.State.Forwarded {
		t.Fatal("expected Forwarded=true (batch query should hit ChB at attempt 0)")
	}
	if fwd.calls != 1 {
		t.Fatalf("expected 1 ForwardByRoute call, got %d", fwd.calls)
	}
	if len(fwd.routes) != 1 || fwd.routes[0].ID != route.ID {
		t.Fatalf("expected ForwardByRoute called with route ID %d, got routes=%v", route.ID, fwd.routes)
	}
	// 关键断言：batch 命中后绝不 dispatch 头部 channel —— 这是审计 #12 修复的本质。
	if backend.callCount != 0 {
		t.Fatalf("expected 0 dispatcher calls (forward should fire before any dispatch), got %d", backend.callCount)
	}
	// 还要验证第一次 MatchRoute 传入的是完整 batch（[A,B,C]）。
	if len(cache.captured) < 1 {
		t.Fatal("MatchRoute should have been called at least once")
	}
	firstCall := cache.captured[0]
	if len(firstCall) != 3 || firstCall[0] != chA.ID || firstCall[1] != chB.ID || firstCall[2] != chC.ID {
		t.Errorf("first MatchRoute should receive batch [A=1,B=2,C=3], got %v", firstCall)
	}
}

// idChannelCache 按 channelIDs 中是否含特定 ID 决定返回 route，更贴近 main 的
// Store.RouteIndex.Match 行为（id-in-set 判定）。每次调用捕获入参 channelIDs。
type idChannelCache struct {
	app.AgentCache
	routeByID map[uint]*models.AgentRoute
	captured  [][]uint
}

func (c *idChannelCache) MatchRoute(_ uint, _ string, channelIDs []uint) *models.AgentRoute {
	idsCopy := append([]uint(nil), channelIDs...)
	c.captured = append(c.captured, idsCopy)
	for _, id := range channelIDs {
		if r, ok := c.routeByID[id]; ok {
			return r
		}
	}
	return nil
}

// TestLogAttemptFailed_AttemptsLeftReflectsPlanRemaining
// 钉死新语义：attempts_left 是 Plan 内剩余 attempt 数（Planner truncate 后真实剩余），
// 不是 main 老主循环的 RetryMax 全局累减。
//
// 场景：Plan.Attempts 有 3 个 attempt，全部失败（retry-able）。
//   - 第 1 次失败（idx=0）→ attempts_left = 3-0-1 = 2
//   - 第 2 次失败（idx=1）→ attempts_left = 3-1-1 = 1
//   - 第 3 次失败（idx=2）→ attempts_left = 3-2-1 = 0
//
// 与 main 对比：若全局 RetryMax=5 而 Planner truncate 后 Plan 只有 3 个 attempt，main
// 第 1 次会打 attempts_left=4，新流程打 2 —— 新值更准确反映真实剩余尝试机会。
func TestLogAttemptFailed_AttemptsLeftReflectsPlanRemaining(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch1 fail"), Written: false},
		{Err: errors.New("ch2 fail"), Written: false},
		{Err: errors.New("ch3 fail"), Written: false},
	}}
	d := backend
	e := &Executor{Dispatcher: d}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 3}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{logger: logger})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	e.Run(rctx)

	entries := recorded.FilterMessage("relay attempt failed").All()
	if len(entries) != 3 {
		t.Fatalf("want 3 'relay attempt failed' warn entries, got %d (all=%v)", len(entries), recorded.All())
	}
	wantLeft := []int64{2, 1, 0}
	for i, entry := range entries {
		got, ok := entry.ContextMap()["attempts_left"].(int64)
		if !ok {
			t.Errorf("attempt %d: attempts_left field missing or wrong type, got %T", i, entry.ContextMap()["attempts_left"])
			continue
		}
		if got != wantLeft[i] {
			t.Errorf("attempt %d: attempts_left=%d, want %d (Plan-remaining semantics: len(attempts)-idx-1)", i, got, wantLeft[i])
		}
	}
}

// ==================== Task 6: Sleep 行为覆盖 ====================

// TestExecutor_ContextCanceled_NoSleep 验证例外 2：attempt 返回 context.Canceled
// 时 Executor 立即返回（不进入 sleep 分支）。
// 注入 stubSleep{ms:1000}（正常会 sleep 1 秒），但 context.Canceled 例外在 sleep
// 之前触发，整体耗时应远小于 sleep 时长（100ms 以内）。
func TestExecutor_ContextCanceled_NoSleep(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: context.Canceled, Written: false},
		{PromptTokens: 99}, // 不应被调
	}}
	e := &Executor{
		Dispatcher: backend,
		Sleep:      stubSleep{ms: 1000},
	}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	start := time.Now()
	e.Run(rctx)
	elapsed := time.Since(start)

	// context.Canceled 触发例外 2，立即返回，不进入 sleep 分支。
	if elapsed >= 100*time.Millisecond {
		t.Errorf("context.Canceled 例外应立即返回，耗时 %v >= 100ms（怀疑进入了 sleep）", elapsed)
	}
	if backend.callCount != 1 {
		t.Errorf("context.Canceled 后不应 retry，backend 调用次数 = %d, want 1", backend.callCount)
	}
	if !errors.Is(rctx.State.Execution.Err, context.Canceled) {
		t.Errorf("Execution.Err 应为 context.Canceled, got %v", rctx.State.Execution.Err)
	}
}

// TestExecutor_InvalidRequest_NoSleep 验证例外 3：attempt 返回
// *UpstreamError{Status:400, ProviderErrorType:"invalid_request_error"} 时
// Executor 立即短路返回，不进入 sleep，不 retry 下一 attempt。
func TestExecutor_InvalidRequest_NoSleep(t *testing.T) {
	invReqErr := &common.UpstreamError{
		Status:            400,
		Body:              []byte(`{"error":{"type":"invalid_request_error","message":"bad prompt"}}`),
		ProviderErrorType: "invalid_request_error",
	}
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: invReqErr, Written: false},
		{PromptTokens: 99}, // 不应被调
	}}
	e := &Executor{
		Dispatcher: backend,
		Sleep:      stubSleep{ms: 1000},
	}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	start := time.Now()
	e.Run(rctx)
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Errorf("invalid_request_error 例外应立即返回，耗时 %v >= 100ms", elapsed)
	}
	if backend.callCount != 1 {
		t.Errorf("invalid_request_error 短路后不应 retry，backend 调用次数 = %d, want 1", backend.callCount)
	}
	var gotErr *common.UpstreamError
	if !errors.As(rctx.State.Execution.Err, &gotErr) {
		t.Fatalf("Execution.Err 应为 *common.UpstreamError, got %T: %v", rctx.State.Execution.Err, rctx.State.Execution.Err)
	}
	if gotErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error", gotErr.ProviderErrorType)
	}
}

// TestExecutor_DefaultFallback_SleepsBetween 验证默认路径：attempt 1 失败（503，可
// fallback），attempt 2 成功；stubSleep{ms:50} → 两次 attempt 之间至少 sleep 50ms。
func TestExecutor_DefaultFallback_SleepsBetween(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: &common.UpstreamError{Status: 503, Body: []byte("overloaded")}, Written: false},
		{PromptTokens: 7}, // 成功
	}}
	e := &Executor{
		Dispatcher: backend,
		Sleep:      stubSleep{ms: 50},
	}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	// Sleep 分支需要 rctx.Context.Request != nil
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	start := time.Now()
	e.Run(rctx)
	elapsed := time.Since(start)

	if backend.callCount != 2 {
		t.Errorf("应 dispatch 2 次（fail + success），got %d", backend.callCount)
	}
	if rctx.State.Execution.Err != nil {
		t.Errorf("终态应为成功（第 2 次 attempt），got Err=%v", rctx.State.Execution.Err)
	}
	if elapsed < 50*time.Millisecond {
		t.Errorf("两次 attempt 之间应 sleep ≥ 50ms，实际耗时 %v < 50ms", elapsed)
	}
}

// TestRun_AttemptFailLog_LegacyModePath 钉死审计 D-C2 / 审计 #3：
// 当 Attempt.Mode == state.ModeLegacy 失败时，"relay attempt failed" Warn 日志
// 的 `path` 字段必须是 "legacy"（对齐 main:handler.go 老主循环的 nativeOrLegacy(useLegacy)
// 语义）。Mutation guard：把 logAttemptFailed 中的 `path = "legacy"` 改回 "native"
// 或删掉 `if a.Mode == state.ModeLegacy` 分支，本测试必挂。
func TestRun_AttemptFailLog_LegacyModePath(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("legacy upstream 500"), Written: false},
	}}
	e := &Executor{Dispatcher: backend}

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 7}}, RealModel: "claude", Mode: state.ModeLegacy},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{logger: logger})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	e.Run(rctx)

	entries := recorded.FilterMessage("relay attempt failed").All()
	if len(entries) != 1 {
		t.Fatalf("expected 1 'relay attempt failed' log, got %d (all=%v)", len(entries), recorded.All())
	}
	fields := entries[0].ContextMap()
	if fields["path"] != "legacy" {
		t.Fatalf("path = %v, want \"legacy\" (Mode=state.ModeLegacy)", fields["path"])
	}
	// 顺手再钉一下 channel_id，避免日志构造时 ModeLegacy 误改其它字段。
	if fields["channel_id"] != uint64(7) {
		t.Errorf("channel_id = %v, want 7", fields["channel_id"])
	}
}
