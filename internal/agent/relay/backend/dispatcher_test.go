package backend

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/legacy"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/native"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/passthrough"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// TestAttemptPlanZero：零值 plan 必须可读：空 slice / 空字符串 / 空 trace。
func TestAttemptPlanZero(t *testing.T) {
	var p state.AttemptPlan
	if len(p.Attempts) != 0 {
		t.Fatalf("zero Attempts should be empty, got %d", len(p.Attempts))
	}
	if p.RoutingName != "" {
		t.Fatalf("zero RoutingName should be empty, got %q", p.RoutingName)
	}
	if len(p.Trace) != 0 {
		t.Fatalf("zero Trace should be empty, got %d", len(p.Trace))
	}
}

// TestExecutionResultZero：零值 result 同样不能 panic。
// Used.Channel == nil 是 Executor "未曾 dispatch" 的信号（取代被删的 History 长度判断）。
func TestExecutionResultZero(t *testing.T) {
	var e state.ExecutionResult
	if e.Err != nil {
		t.Fatalf("zero Err should be nil, got %v", e.Err)
	}
	if e.Used.Channel != nil {
		t.Fatalf("zero Used.Channel should be nil, got %#v", e.Used.Channel)
	}
}

// TestAttemptResultErrChain：边界 — Err 非 nil 且 Written=true（不可重试场景）。
func TestAttemptResultErrChain(t *testing.T) {
	inner := errors.New("upstream 500")
	r := state.AttemptResult{Err: inner, Written: true}
	if r.Err == nil || !r.Written {
		t.Fatalf("field set broken: %#v", r)
	}
	if !errors.Is(r.Err, inner) {
		t.Fatal("error identity broken")
	}
}

// TestAttemptFieldsRoundTrip：Attempt / AttemptResult 是 plan→executor→reporter
// 的载具，所有字段必须可读写不丢失（落到 ExecutionResult.Used / Outcome 后仍可还原）。
func TestAttemptFieldsRoundTrip(t *testing.T) {
	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 7}}
	a := state.Attempt{Channel: ch, RealModel: "claude-3-5-sonnet", Mode: state.ModeNative}
	r := state.AttemptResult{
		PromptTokens:     100,
		CompletionTokens: 50,
		FirstResponseMs:  123,
		UpstreamModel:    "claude-3-5-sonnet-20241022",
		Written:          true,
		TokenSource:      "upstream",
		ResponseText:     "ok",
	}
	e := state.ExecutionResult{Used: a, Outcome: r}
	if e.Used.Channel == nil || e.Used.Channel.ID != 7 {
		t.Fatal("Used.Channel lost")
	}
	if e.Used.Mode != state.ModeNative {
		t.Fatalf("Mode lost, got %q", e.Used.Mode)
	}
	if e.Outcome.PromptTokens != 100 || e.Outcome.UpstreamModel != "claude-3-5-sonnet-20241022" {
		t.Fatalf("Outcome fields lost: %#v", e.Outcome)
	}
	if !e.Outcome.Written || e.Outcome.TokenSource != "upstream" {
		t.Fatalf("Outcome flags lost: %#v", e.Outcome)
	}
}

// TestBackendInterfacesConform 编译期断言三个 backend 都满足 Backend 接口。
// 这是 G1 唯一的硬约束 — 接口骨架到位即视为通过。
func TestBackendInterfacesConform(t *testing.T) {
	var _ Backend = (*native.Backend)(nil)
	var _ Backend = (*legacy.Backend)(nil)
	var _ Backend = (*passthrough.Backend)(nil)
}

// TestBackendsHoldDependencies 边界：迁移后 3 个 backend 都持有 app.AgentApplication，
// 不再依赖 *Handler。保证字段名稳定，否则 NewDispatcher 装配点会静默错位。
func TestBackendsHoldDependencies(t *testing.T) {
	if (&native.Backend{Agent: nil}).Agent != nil {
		t.Error("native.Backend.Agent not assignable")
	}
	if (&legacy.Backend{Agent: nil}).Agent != nil {
		t.Error("legacy.Backend.Agent not assignable")
	}
	if (&passthrough.Backend{Agent: nil}).Agent != nil {
		t.Error("passthrough.Backend.Agent not assignable")
	}
}

// fakeBackend 测试用 stub：返回构造时设定的 AttemptResult，记录调用次数。
type fakeBackend struct {
	result    state.AttemptResult
	callCount int
}

func (f *fakeBackend) Relay(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	f.callCount++
	return f.result
}

// TestDispatcherUnknownMode 失败路径：未注册的 mode 必须返回带 error 的 AttemptResult，
// 不能 panic / 不能静默返回零值。
func TestDispatcherUnknownMode(t *testing.T) {
	d := NewDispatcher(nil)
	rctx := &state.RelayContext{
		Input: state.RelayInput{Body: []byte(`{"model":"x"}`)},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	res := d.Dispatch(rctx, state.Attempt{Mode: state.RelayMode("bogus"), RealModel: "gpt-4"})
	if res.Err == nil {
		t.Fatal("expected error on unknown mode, got nil")
	}
}

// TestDispatcherFinalizesTokenCounts 成功路径：Dispatch 应把 backend 返回的原始 token 计数
// 交给 FinalizeTokenCounts，并把 Source 写到 AttemptResult.TokenSource。
func TestDispatcherFinalizesTokenCounts(t *testing.T) {
	// backend 返回非零 prompt/completion；body 是合法 JSON 方便 EstimatePromptTokens 不爆。
	fake := &fakeBackend{result: state.AttemptResult{PromptTokens: 10, CompletionTokens: 20, ResponseText: ""}}
	d := &Dispatcher{Backends: map[state.RelayMode]Backend{state.ModeNative: fake}}
	rctx := &state.RelayContext{
		Input: state.RelayInput{Body: []byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`)},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	res := d.Dispatch(rctx, state.Attempt{Mode: state.ModeNative, RealModel: "gpt-4"})
	if res.TokenSource == "" {
		t.Error("TokenSource should be filled after Dispatch")
	}
	if fake.callCount != 1 {
		t.Errorf("backend should be called exactly once, got %d", fake.callCount)
	}
}

// TestDispatcherRegistersAllThreeModes 边界：NewDispatcher 必须把 3 种 mode 都注册上。
func TestDispatcherRegistersAllThreeModes(t *testing.T) {
	d := NewDispatcher(nil)
	if d.Backends[state.ModeNative] == nil {
		t.Error("native not registered")
	}
	if d.Backends[state.ModeLegacy] == nil {
		t.Error("legacy not registered")
	}
	if d.Backends[state.ModePassthrough] == nil {
		t.Error("passthrough not registered")
	}
}
