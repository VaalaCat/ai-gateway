package exec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
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

type injectedProvider struct {
	calls  int
	result attemptexec.ProviderResult
}

func newLocalTestExecutor(
	dispatcher state.Dispatcher,
	runner ResilientRunner,
	gate state.RateGate,
) *Executor {
	provider := attemptexec.NewProviderExecutor(dispatcher, runner, gate)
	return &Executor{
		SourceAgentID: "source",
		Routes:        NewAttemptRouteBuilder(nil),
		Local:         NewLocalAttemptExecutor("source", provider),
		RequestGate:   gate,
	}
}

func (p *injectedProvider) Execute(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
	p.calls++
	return p.result
}

type bodyRecordingDispatcher struct {
	bodies  [][]byte
	results []state.AttemptResult
}

func (d *bodyRecordingDispatcher) Dispatch(rctx *state.RelayContext, _ state.Attempt) state.AttemptResult {
	body, err := io.ReadAll(rctx.Context.Request.Body)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	d.bodies = append(d.bodies, body)
	return d.results[len(d.bodies)-1]
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
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return &state.RelayContext{
		Context: c,
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
	logger *zap.Logger
}

func (s *stubExecAgent) GetCache() app.AgentCache    { return nil }
func (s *stubExecAgent) GetBodyStore() app.BodyStore { return nil }
func (s *stubExecAgent) GetLogger() *zap.Logger {
	if s.logger != nil {
		return s.logger
	}
	return zap.NewNop()
}
func (s *stubExecAgent) GetConfig() *config.AgentRuntimeConfig { return nil }
func (s *stubExecAgent) GetTransportPool() app.TransportPool   { return stubExecTransportPool{} }
func (s *stubExecAgent) RelayTimeout() time.Duration           { return 0 }

type stubExecTransportPool struct{}

func (stubExecTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubExecTransportPool) Invalidate(uint, string)             {}
func (stubExecTransportPool) CloseIdleConnections()               {}

type execBodyStore struct {
	body app.ReplayBody
}

func (s execBodyStore) Capture(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
	return s.body, nil
}

type execReplayBody struct {
	data         []byte
	openErr      error
	opens        atomic.Int32
	readerCloses atomic.Int32
	closeOnce    sync.Once
}

func (b *execReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *execReplayBody) Open() (io.ReadCloser, error) {
	if b.openErr != nil {
		return nil, b.openErr
	}
	b.opens.Add(1)
	return &execTrackedReader{Reader: bytes.NewReader(b.data), closes: &b.readerCloses}, nil
}
func (b *execReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.data...), nil
}
func (b *execReplayBody) Close() error {
	b.closeOnce.Do(func() {})
	return nil
}

type execTrackedReader struct {
	io.Reader
	closes *atomic.Int32
	once   sync.Once
}

func (r *execTrackedReader) Close() error {
	r.once.Do(func() {
		if r.closes != nil {
			r.closes.Add(1)
		}
	})
	return nil
}

// TestExecutorSuccessFirstAttempt 成功路径：第一次 attempt 成功 → 不再 retry。
func TestExecutorSuccessFirstAttempt(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	d := backend
	e := newLocalTestExecutor(d, nil, nil)

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
	if !rctx.State.Execution.ProviderDispatched {
		t.Error("ProviderDispatched should be true after Dispatcher.Dispatch")
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
	e := newLocalTestExecutor(d, nil, nil)

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

func TestExecutorReopensReplayBodyForEveryDispatch(t *testing.T) {
	want := []byte("multipart-or-json-payload")
	replay := &execReplayBody{data: want}
	resources := &state.RequestResources{}
	if err := resources.Replace(context.Background(), execBodyStore{body: replay}, strings.NewReader("ignored"), app.BodyLimits{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })

	dispatcher := &bodyRecordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("retry")},
		{},
	}}
	e := newLocalTestExecutor(dispatcher, nil, nil)
	rctx := newTestExecutorRctx(state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}},
	}}, &stubExecAgent{})
	var originalCloses atomic.Int32
	original := &execTrackedReader{Reader: strings.NewReader("stale"), closes: &originalCloses}
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/audio/transcriptions", nil)
	rctx.Context.Request.Body = original
	rctx.Resources = resources
	rctx.Input.Body = []byte("bounded-json-compatibility-view")

	// behavior change: attempts reopen the owned replay body instead of rebuilding from RelayInput.Body.
	e.Run(rctx)
	if len(dispatcher.bodies) != 2 {
		t.Fatalf("dispatch bodies = %d, want 2", len(dispatcher.bodies))
	}
	for i, got := range dispatcher.bodies {
		if !bytes.Equal(got, want) {
			t.Fatalf("dispatch #%d body = %q, want %q", i+1, got, want)
		}
	}
	if originalCloses.Load() != 1 {
		t.Fatalf("original request reader closes = %d, want 1", originalCloses.Load())
	}
	if replay.opens.Load() != 2 {
		t.Fatalf("ReplayBody.Open calls = %d, want 2", replay.opens.Load())
	}
	if replay.readerCloses.Load() != 1 {
		t.Fatalf("replay reader closes before request cleanup = %d, want 1", replay.readerCloses.Load())
	}
	if err := rctx.Context.Request.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if replay.readerCloses.Load() != 2 {
		t.Fatalf("replay reader closes after request cleanup = %d, want 2", replay.readerCloses.Load())
	}
}

func TestExecutorReplayOpenFailureSkipsDispatch(t *testing.T) {
	wantErr := errors.New("replay open failed")
	resources := &state.RequestResources{}
	if err := resources.Replace(context.Background(), execBodyStore{
		body: &execReplayBody{openErr: wantErr},
	}, strings.NewReader("ignored"), app.BodyLimits{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resources.Close() })
	dispatcher := &recordingDispatcher{}
	rctx := newTestExecutorRctx(state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}},
	}}, &stubExecAgent{})
	rctx.Context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	rctx.Resources = resources

	(newLocalTestExecutor(dispatcher, nil, nil)).Run(rctx)
	if !errors.Is(rctx.State.Execution.Err, wantErr) {
		t.Fatalf("Execution.Err = %v, want replay open failure", rctx.State.Execution.Err)
	}
	if dispatcher.callCount != 0 {
		t.Fatalf("dispatcher calls = %d, want 0", dispatcher.callCount)
	}
	if rctx.State.Execution.ProviderDispatched {
		t.Fatal("ProviderDispatched should remain false when replay body open fails")
	}
}

// TestExecutorStopsOnWritten 失败 + Written=true（流已写出客户端）→ 不可 retry，立即 return。
func TestExecutorStopsOnWritten(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("stream broke"), Written: true},
		{PromptTokens: 99}, // should NOT be called
	}}
	d := backend
	e := newLocalTestExecutor(d, nil, nil)

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
	e := newLocalTestExecutor(d, nil, nil)
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

func TestExecutor_RelayAttemptFailedLogEmitted(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch1 boom"), Written: false},
		{PromptTokens: 7},
	}}
	d := backend
	e := newLocalTestExecutor(d, nil, nil)

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
	e := newLocalTestExecutor(d, nil, nil)

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
func TestLogAttemptFailed_AttemptsLeftReflectsPlanRemaining(t *testing.T) {
	core, recorded := observer.New(zap.WarnLevel)
	logger := zap.New(core)

	backend := &recordingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("ch1 fail"), Written: false},
		{Err: errors.New("ch2 fail"), Written: false},
		{Err: errors.New("ch3 fail"), Written: false},
	}}
	d := backend
	e := newLocalTestExecutor(d, nil, nil)

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
	e := newLocalTestExecutor(backend, nil, nil)
	e.Sleep = stubSleep{ms: 1000}

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
	e := newLocalTestExecutor(backend, nil, nil)
	e.Sleep = stubSleep{ms: 1000}

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
	e := newLocalTestExecutor(backend, nil, nil)
	e.Sleep = stubSleep{ms: 50}

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

// ==================== Task 6 Resilience Runner 覆盖 ====================

// stubRunner 把 dispatch 调用计数，并可选地重试 N 次再返回最终结果。
type stubRunner struct{ retries int }

func (s stubRunner) Run(_ *state.RelayContext, _ state.Attempt, dispatch func() state.AttemptResult) state.AttemptResult {
	var res state.AttemptResult
	for i := 0; i <= s.retries; i++ {
		res = dispatch()
		if res.Err == nil {
			break
		}
	}
	return res
}

// TestRun_ResilienceRunnerRetriesSameChannel 验证：注入 stubRunner{retries:2} 后，
// 单候选 plan 在第 1 次 dispatch 失败时会再试第 2 次（同 channel），第 2 次成功后终态 Err=nil。
func TestRun_ResilienceRunnerRetriesSameChannel(t *testing.T) {
	calls := 0
	d := &recordingDispatcher{
		results: []state.AttemptResult{
			{Err: &common.UpstreamError{Status: 503}},
			{}, // 第 2 次成功
		},
	}
	// 用自定义 dispatcher 计数，但让 stubRunner 驱动重试。
	var countingD state.Dispatcher = dispatchCounterDispatcher{inner: d, counter: &calls}
	e := newLocalTestExecutor(countingD, stubRunner{retries: 2}, nil)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)
	if calls != 2 {
		t.Fatalf("runner should retry same channel until success, calls=%d want 2", calls)
	}
	if rctx.State.Execution.Err != nil {
		t.Fatalf("expected success after retry, got %v", rctx.State.Execution.Err)
	}
}

// dispatchCounterDispatcher 包装任意 Dispatcher，每次 Dispatch 前递增 *counter。
type dispatchCounterDispatcher struct {
	inner   state.Dispatcher
	counter *int
}

func (d dispatchCounterDispatcher) Dispatch(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	*d.counter++
	return d.inner.Dispatch(rctx, a)
}

// TestRun_NilResilienceFallsBackToPlainDispatch 验证：Resilience==nil 时退化为裸 dispatch，
// 单候选 plan 恰好 dispatch 一次（向后兼容老 test stub 行为不变）。
func TestRun_NilResilienceFallsBackToPlainDispatch(t *testing.T) {
	calls := 0
	var countingD state.Dispatcher = dispatchCounterDispatcher{
		inner:   &recordingDispatcher{results: []state.AttemptResult{{}}},
		counter: &calls,
	}
	e := newLocalTestExecutor(countingD, nil, nil) // Resilience nil
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)
	if calls != 1 {
		t.Fatalf("nil runner should dispatch exactly once, calls=%d want 1", calls)
	}
}

func TestExecutorUsesInjectedLocalProviderExecutor(t *testing.T) {
	want := state.AttemptResult{UpstreamModel: "shared-provider"}
	provider := &injectedProvider{result: attemptexec.ProviderResult{
		Outcome: want, Dispatches: 2, ProviderDispatched: true,
	}}
	executor := &Executor{
		SourceAgentID: "source", Routes: NewAttemptRouteBuilder(nil),
		Local: NewLocalAttemptExecutor("source", provider),
	}
	attempt := state.Attempt{Channel: &models.Channel{}, Source: state.SourceAdmin, SourceID: 1, RealModel: "model"}
	rctx := newTestExecutorRctx(state.AttemptPlan{Attempts: []state.Attempt{attempt}}, &stubExecAgent{})

	executor.Run(rctx)

	if rctx.State.Execution.Outcome != want {
		t.Fatalf("execution result = %#v, want %#v", rctx.State.Execution.Outcome, want)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if !rctx.State.Execution.ProviderDispatched {
		t.Fatal("injected provider dispatch must update relay execution state")
	}
}

// ==================== Task 2: History 记录 ====================

// TestRun_HistoryRecordsEachCandidate 验证:两候选时,History 里有两条记录。
// 候选 1 返回 503 失败 → 候选 2 成功;History[0] seq=1/status=fail/HTTP=503/errorType,
// History[1] seq=2/status=ok。
func TestRun_HistoryRecordsEachCandidate(t *testing.T) {
	d := &recordingDispatcher{results: []state.AttemptResult{
		{Err: &common.UpstreamError{Status: 503, ProviderErrorType: "server_error"}, Written: false},
		{}, // 第 2 个候选成功
	}}
	e := newLocalTestExecutor(d, nil, nil)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	h := rctx.State.Execution.History
	if len(h) != 2 {
		t.Fatalf("want 2 history entries, got %d", len(h))
	}
	if h[0].Seq != 1 || h[0].Status != "fail" || h[0].HTTPStatus != 503 || h[0].ErrorType != "server_error" {
		t.Fatalf("entry[0] wrong: %+v", h[0])
	}
	if h[1].Seq != 2 || h[1].Status != "ok" {
		t.Fatalf("entry[1] wrong: %+v", h[1])
	}
}

// TestRun_HistoryCountsInnerRetries 验证:单候选 + stubRunner{retries:2} 时,
// stubRunner 在 runAttempt 内调 dispatch 3 次(2 失败 + 1 成功),
// History 应有 1 条记录且 Retries==2(= dispatch 次数 - 1)。
func TestRun_HistoryCountsInnerRetries(t *testing.T) {
	d := &recordingDispatcher{results: []state.AttemptResult{
		{Err: &common.UpstreamError{Status: 503}, Written: false},
		{Err: &common.UpstreamError{Status: 503}, Written: false},
		{}, // 第 3 次成功
	}}
	e := newLocalTestExecutor(d, stubRunner{retries: 2}, nil)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	h := rctx.State.Execution.History
	if len(h) != 1 {
		t.Fatalf("want 1 history entry, got %d", len(h))
	}
	if h[0].Retries != 2 {
		t.Fatalf("want Retries=2 (3 dispatches - 1), got %d; entry=%+v", h[0].Retries, h[0])
	}
	if h[0].Status != "ok" {
		t.Fatalf("want Status=ok after eventual success, got %q", h[0].Status)
	}
}

// TestRun_AttemptRecordHasTrace 验证 History 条目的 HasTrace 精确反映该候选是否写 trace 行。
func TestRun_AttemptRecordHasTrace(t *testing.T) {
	// case 1: 关 trace + 单次成功 → 非 verbose → HasTrace=false
	{
		d := &recorderMutatingDispatcher{results: []state.AttemptResult{{}}}
		e := newLocalTestExecutor(d, nil, nil)
		plan := state.AttemptPlan{Attempts: []state.Attempt{
			{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
		}}
		rctx := &state.RelayContext{
			Context: nil, Agent: &stubExecAgent{},
			Input: state.RelayInput{Body: []byte(`{"model":"x"}`), UserInfo: &app.UserInfo{TokenID: 1}},
			State: &state.RelayState{Recorder: trace.NewRecorder(false, 0), Plan: plan},
		}
		e.Run(rctx)
		if got := rctx.State.Execution.History[0].HasTrace; got {
			t.Errorf("关 trace 单成功候选 HasTrace=%v,应为 false", got)
		}
	}
	// case 2: 关 trace + 候选失败(WithFail → verbose) → HasTrace=true; 第2候选成功(关trace) → false
	{
		d := &recorderMutatingDispatcher{results: []state.AttemptResult{
			{Err: &common.UpstreamError{Status: 500}},
			{}, // 第 2 候选成功
		}}
		e := newLocalTestExecutor(d, nil, nil)
		plan := state.AttemptPlan{Attempts: []state.Attempt{
			{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
			{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
		}}
		rctx := &state.RelayContext{
			Context: nil, Agent: &stubExecAgent{},
			Input: state.RelayInput{Body: []byte(`{"model":"x"}`), UserInfo: &app.UserInfo{TokenID: 1}},
			State: &state.RelayState{Recorder: trace.NewRecorder(false, 0), Plan: plan},
		}
		e.Run(rctx)
		if got := rctx.State.Execution.History[0].HasTrace; !got {
			t.Errorf("失败候选 HasTrace=%v,应为 true", got)
		}
		if got := rctx.State.Execution.History[1].HasTrace; got {
			t.Errorf("第 2 候选成功(关 trace) HasTrace=%v,应为 false", got)
		}
	}
	// case 3: 开 trace + 成功 → verbose → HasTrace=true
	{
		d := &recorderMutatingDispatcher{results: []state.AttemptResult{{}}}
		e := newLocalTestExecutor(d, nil, nil)
		plan := state.AttemptPlan{Attempts: []state.Attempt{
			{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
		}}
		rctx := &state.RelayContext{
			Context: nil, Agent: &stubExecAgent{},
			Input: state.RelayInput{Body: []byte(`{"model":"x"}`), UserInfo: &app.UserInfo{TokenID: 1}},
			State: &state.RelayState{Recorder: trace.NewRecorder(true, 0), Plan: plan}, // 开 trace
		}
		e.Run(rctx)
		if got := rctx.State.Execution.History[0].HasTrace; !got {
			t.Errorf("开 trace 成功候选 HasTrace=%v,应为 true", got)
		}
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
	e := newLocalTestExecutor(backend, nil, nil)

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

type recordingRouteBuilder struct {
	routes []AttemptRoute
	errs   []error
	inputs []AttemptRouteInput
}

func (b *recordingRouteBuilder) Build(input AttemptRouteInput) (AttemptRoute, error) {
	b.inputs = append(b.inputs, input)
	idx := len(b.inputs) - 1
	if idx < len(b.errs) && b.errs[idx] != nil {
		return AttemptRoute{}, b.errs[idx]
	}
	if idx >= len(b.routes) {
		return AttemptRoute{}, errors.New("unexpected route build")
	}
	return b.routes[idx], nil
}

type recordingLocalExecutor struct {
	outcomes []AttemptOutcome
	attempts []state.Attempt
}

func (e *recordingLocalExecutor) Execute(_ *state.RelayContext, attempt state.Attempt) AttemptOutcome {
	e.attempts = append(e.attempts, attempt)
	return e.outcomes[len(e.attempts)-1]
}

type recordingRemoteExecutor struct {
	outcomes []AttemptOutcome
	targets  []AttemptTarget
	bound    []attemptwire.BoundAttempt
	routeIDs []uint
}

func (e *recordingRemoteExecutor) Execute(_ *state.RelayContext, target AttemptTarget, routeID uint, bound attemptwire.BoundAttempt) AttemptOutcome {
	e.targets = append(e.targets, target)
	e.bound = append(e.bound, bound)
	e.routeIDs = append(e.routeIDs, routeID)
	return e.outcomes[len(e.targets)-1]
}

func portAttempt(id uint) state.Attempt {
	return portAttemptWithIdentity(state.SourceAdmin, id, "gpt-4o")
}

func portAttemptWithIdentity(source state.ChannelSource, id uint, realModel string) state.Attempt {
	return state.Attempt{
		Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: id, Name: "channel"}},
		Source:  source, SourceID: id, RealModel: realModel, Mode: state.ModeNative,
	}
}

func portExecutorContext(attempts ...state.Attempt) *state.RelayContext {
	rctx := newTestExecutorRctx(state.AttemptPlan{Attempts: attempts}, &stubExecAgent{})
	rctx.Input.RequestID = "request-1"
	rctx.Input.HardSelector = app.AgentSelector{}
	return rctx
}

func localPortRoute() AttemptRoute {
	return AttemptRoute{Kind: AgentRouteNone, Targets: []AttemptTarget{{AgentID: "source", Kind: AttemptTargetLocal}}}
}

func remotePortRoute(hard bool, agentIDs ...string) AttemptRoute {
	targets := make([]AttemptTarget, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		targets = append(targets, AttemptTarget{AgentID: agentID, Kind: AttemptTargetRemote})
	}
	return AttemptRoute{Kind: AgentRouteToken, Hard: hard, Targets: targets}
}

func successfulPortOutcome(agentID string) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptSucceeded, ExecutionAgentID: agentID, Path: app.RoutePathDirect,
		Commit: tunnel.Committed, ProviderResultKnown: true, ProviderDispatched: true,
		Result: state.AttemptResult{PromptTokens: 7, CompletionTokens: 3},
	}
}

func retryablePortOutcome(agentID string) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptProviderFailed, ExecutionAgentID: agentID, Path: app.RoutePathDirect,
		Commit: tunnel.Committed, ProviderResultKnown: true, ProviderDispatched: true,
		PlanAdvanceAllowed: true, Result: state.AttemptResult{Err: errors.New("provider 500")},
	}
}

func retryableLocalPortOutcome(agentID string) AttemptOutcome {
	outcome := retryablePortOutcome(agentID)
	outcome.Path = app.RoutePathLocal
	return outcome
}

func unavailablePortOutcome(agentID string) AttemptOutcome {
	return AttemptOutcome{
		Kind: AttemptTransportUnavailable, ExecutionAgentID: agentID, Path: app.RoutePathRelay,
		Commit: tunnel.PreCommit, Result: state.AttemptResult{Err: errors.New("transport unavailable")},
	}
}

func unavailableTargetPaths(agentID string) AttemptOutcome {
	outcome := unavailablePortOutcome(agentID)
	outcome.AgentPaths = []models.AgentPathRecord{
		{AgentID: agentID, Path: models.AgentPathDirect, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: agentID, Path: models.AgentPathRelay, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
	}
	return outcome
}

func selectedRelayTargetPaths(agentID string) AttemptOutcome {
	outcome := successfulPortOutcome(agentID)
	outcome.Path = app.RoutePathRelay
	outcome.AgentPaths = []models.AgentPathRecord{
		{AgentID: agentID, Path: models.AgentPathDirect, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: agentID, Path: models.AgentPathRelay, Result: models.AgentPathSelected, Stage: models.AgentPathResponse, CommitState: models.AgentPathCommitted},
	}
	return outcome
}

func selectedLocalTargetPath(agentID string) AttemptOutcome {
	outcome := successfulPortOutcome(agentID)
	outcome.Path = app.RoutePathLocal
	outcome.AgentPaths = []models.AgentPathRecord{{
		AgentID: agentID, Path: models.AgentPathLocal, Result: models.AgentPathSelected,
		Stage: models.AgentPathDispatch, CommitState: models.AgentPathCommitted,
	}}
	return outcome
}

// behavior change: target switching stays inside one business/channel attempt
// and preserves each target's direct/relay path sequence.
func TestExecutorAccumulatesPathsAcrossRemoteTargets(t *testing.T) {
	route := remotePortRoute(false, "member-a", "member-b")
	routes := &recordingRouteBuilder{routes: []AttemptRoute{route}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{
		unavailableTargetPaths("member-a"), selectedRelayTargetPaths("member-b"),
	}}
	rctx := portExecutorContext(portAttempt(1))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote}).Run(rctx)

	require.Len(t, rctx.State.Execution.History, 1)
	require.Equal(t, []models.AgentPathRecord{
		{AgentID: "member-a", Path: models.AgentPathDirect, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: "member-a", Path: models.AgentPathRelay, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: "member-b", Path: models.AgentPathDirect, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: "member-b", Path: models.AgentPathRelay, Result: models.AgentPathSelected, Stage: models.AgentPathResponse, CommitState: models.AgentPathCommitted},
	}, rctx.State.Execution.History[0].AgentPaths)
}

// behavior change: exhausting remote targets before local fallback still emits
// one fallback-chain record with every path in actual execution order.
func TestExecutorAccumulatesRemotePathsBeforeLocalFallback(t *testing.T) {
	route := remotePortRoute(false, "member-a", "member-b")
	route.Targets = append(route.Targets, AttemptTarget{AgentID: "source", Kind: AttemptTargetLocal})
	routes := &recordingRouteBuilder{routes: []AttemptRoute{route}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{
		unavailableTargetPaths("member-a"), unavailableTargetPaths("member-b"),
	}}
	local := &recordingLocalExecutor{outcomes: []AttemptOutcome{selectedLocalTargetPath("source")}}
	rctx := portExecutorContext(portAttempt(1))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote, Local: local}).Run(rctx)

	require.Len(t, rctx.State.Execution.History, 1)
	require.Equal(t, []models.AgentPathKind{
		models.AgentPathDirect, models.AgentPathRelay,
		models.AgentPathDirect, models.AgentPathRelay,
		models.AgentPathLocal,
	}, agentPathKinds(rctx.State.Execution.History[0].AgentPaths))
	require.Equal(t, []string{"member-a", "member-a", "member-b", "member-b", "source"},
		agentPathIDs(rctx.State.Execution.History[0].AgentPaths))
}

func TestExecutorPathAccumulatorResetsForNextBusinessAttempt(t *testing.T) {
	first := remotePortRoute(false, "member-a")
	second := remotePortRoute(false, "member-b")
	routes := &recordingRouteBuilder{routes: []AttemptRoute{first, second}}
	failed := retryablePortOutcome("member-a")
	failed.AgentPaths = []models.AgentPathRecord{{
		AgentID: "member-a", Path: models.AgentPathDirect, Result: models.AgentPathSelected,
		Stage: models.AgentPathResponse, CommitState: models.AgentPathCommitted,
	}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{failed, selectedRelayTargetPaths("member-b")}}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote}).Run(rctx)

	require.Len(t, rctx.State.Execution.History, 2)
	require.Equal(t, []string{"member-a"}, agentPathIDs(rctx.State.Execution.History[0].AgentPaths))
	require.Equal(t, []string{"member-b", "member-b"}, agentPathIDs(rctx.State.Execution.History[1].AgentPaths))
}

func agentPathIDs(paths []models.AgentPathRecord) []string {
	ids := make([]string, 0, len(paths))
	for _, path := range paths {
		ids = append(ids, path.AgentID)
	}
	return ids
}

func TestExecutorRemoteWireDispatchesFoldIntoSingleAttemptRetries(t *testing.T) {
	raw, err := attemptwire.EncodeResult(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true,
		Dispatches: 3,
	})
	require.NoError(t, err)
	wireResult, err := attemptwire.DecodeResult(raw)
	require.NoError(t, err)
	outcome := outcomeFromAttemptResult("target-a", app.RoutePathDirect, tunnel.Committed, wireResult)
	outcome.AgentPaths = []models.AgentPathRecord{{
		AgentID: "target-a", Path: models.AgentPathDirect, Result: models.AgentPathSelected,
		Stage: models.AgentPathResponse, CommitState: models.AgentPathCommitted,
	}}
	routes := &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(false, "target-a")}}
	rctx := portExecutorContext(portAttempt(1))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}}}).Run(rctx)

	require.Len(t, rctx.State.Execution.History, 1)
	require.Equal(t, 2, rctx.State.Execution.History[0].Retries)
}

// behavior change: direct-to-relay path fallback remains one channel attempt
// and the final execution identity/route is copied to ExecutionResult.
func TestExecutorRecordsAgentPathsInSingleChannelAttempt(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{{
		Kind: AgentRouteToken, AgentRouteID: 77,
		Targets: []AttemptTarget{{AgentID: "target-a", Kind: AttemptTargetRemote}},
	}}}
	outcome := successfulPortOutcome("target-a")
	outcome.Path = app.RoutePathRelay
	outcome.AgentPaths = []models.AgentPathRecord{
		{AgentID: "target-a", Path: models.AgentPathDirect, Result: models.AgentPathUnavailable, Stage: models.AgentPathConnect, CommitState: models.AgentPathNotCommitted},
		{AgentID: "target-a", Path: models.AgentPathRelay, Result: models.AgentPathSelected, Stage: models.AgentPathResponse, CommitState: models.AgentPathCommitted},
	}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{outcome}}
	rctx := portExecutorContext(portAttempt(1))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote}).Run(rctx)

	require.Len(t, rctx.State.Execution.History, 1)
	record := rctx.State.Execution.History[0]
	require.Equal(t, uint(77), record.AgentRouteID)
	require.Equal(t, string(AgentRouteToken), record.AgentRouteKind)
	require.Equal(t, outcome.AgentPaths, record.AgentPaths)
	require.Equal(t, "target-a", rctx.State.Execution.ExecutionAgentID)
	require.Equal(t, "source", rctx.State.Execution.RouteSourceAgentID)
	require.Equal(t, uint(77), rctx.State.Execution.AgentRouteID)
	require.Equal(t, string(AgentRouteToken), rctx.State.Execution.AgentRouteKind)
	require.Equal(t, app.RoutePathRelay, rctx.State.Execution.AgentRoutePath)
}

func TestExecutorRemoteAttemptTracesFollowFallbackSequence(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{
		{Kind: AgentRouteToken, AgentRouteID: 1, Targets: []AttemptTarget{{AgentID: "target-a", Kind: AttemptTargetRemote}}},
		{Kind: AgentRouteChannel, AgentRouteID: 2, Targets: []AttemptTarget{{AgentID: "target-b", Kind: AttemptTargetRemote}}},
	}}
	failed := retryablePortOutcome("target-a")
	failed.Trace = &trace.TraceRecord{InboundPath: "/first", FailStage: trace.StageUpstreamStatus, Verbose: true}
	succeeded := successfulPortOutcome("target-b")
	succeeded.Trace = &trace.TraceRecord{InboundPath: "/second", Verbose: true}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{failed, succeeded}}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))
	rctx.State.Recorder = trace.NewRecorder(false, 0)

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote}).Run(rctx)

	require.Equal(t, []int{1, 2}, []int{rctx.State.Execution.History[0].Seq, rctx.State.Execution.History[1].Seq})
	attempts := rctx.State.Recorder.Attempts()
	require.Len(t, attempts, 2)
	require.Equal(t, "/first", attempts[0].InboundPath)
	require.Equal(t, "/second", attempts[1].InboundPath)
}

// behavior change: cumulative ProviderDispatched cannot prove that the final
// adopted attempt reached a provider.
func TestExecutorExecutionAgentIDClearsWhenFinalAttemptFailsBeforeDispatch(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{
		{Kind: AgentRouteToken, AgentRouteID: 1, Targets: []AttemptTarget{{AgentID: "target-a", Kind: AttemptTargetRemote}}},
		{Kind: AgentRouteChannel, AgentRouteID: 2, Targets: []AttemptTarget{{AgentID: "source", Kind: AttemptTargetLocal}}},
	}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{retryablePortOutcome("target-a")}}
	local := &recordingLocalExecutor{outcomes: []AttemptOutcome{{
		Kind: AttemptExecutionRejected, ExecutionAgentID: "source", Path: app.RoutePathLocal,
		ProviderResultKnown: true, Result: state.AttemptResult{Err: errors.New("attempt gate rejected")},
	}}}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))

	(&Executor{SourceAgentID: "source", Routes: routes, Remote: remote, Local: local}).Run(rctx)

	require.True(t, rctx.State.Execution.ProviderDispatched, "historical dispatch remains observable")
	require.Empty(t, rctx.State.Execution.ExecutionAgentID, "final pre-dispatch failure cannot claim source")
}

// behavior change: route lookup is scoped to the current business attempt.
func TestExecutorCurrentAttemptRouteRunsLocalBeforeLaterRemote(t *testing.T) {
	attempts := []state.Attempt{
		portAttemptWithIdentity(state.SourceAdmin, 1, "model-a"),
		portAttemptWithIdentity(state.SourcePrivate, 2, "model-b"),
		portAttemptWithIdentity(state.SourceAdmin, 3, "model-c"),
	}
	firstRoute := remotePortRoute(false, "target-a")
	firstRoute.Targets = append(firstRoute.Targets, AttemptTarget{AgentID: "source", Kind: AttemptTargetLocal})
	routes := &recordingRouteBuilder{routes: []AttemptRoute{firstRoute, remotePortRoute(false, "target-b")}}
	local := &recordingLocalExecutor{outcomes: []AttemptOutcome{retryableLocalPortOutcome("source")}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{
		unavailablePortOutcome("target-a"), successfulPortOutcome("target-b"),
	}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: local, Remote: remote}
	rctx := portExecutorContext(attempts...)

	executor.Run(rctx)

	require.Equal(t, attempts[:2], []state.Attempt{routes.inputs[0].Attempt, routes.inputs[1].Attempt})
	require.Equal(t, []state.Attempt{attempts[0]}, local.attempts)
	require.Equal(t, []AttemptTarget{
		{AgentID: "target-a", Kind: AttemptTargetRemote},
		{AgentID: "target-b", Kind: AttemptTargetRemote},
	}, remote.targets)
	require.Equal(t, []attemptwire.BoundAttempt{
		{Channel: attemptwire.ChannelRef{Source: state.SourceAdmin, ID: 1}, RealModel: "model-a", Mode: state.ModeNative},
		{Channel: attemptwire.ChannelRef{Source: state.SourcePrivate, ID: 2}, RealModel: "model-b", Mode: state.ModeNative},
	}, remote.bound)
	require.Len(t, routes.inputs, 2, "successful B must stop before route C is built")
}

func TestExecutorBindsCurrentRouteIDToEachRemoteAttempt(t *testing.T) {
	first := remotePortRoute(false, "target-a")
	first.AgentRouteID = 41
	second := remotePortRoute(false, "target-b")
	second.AgentRouteID = 42
	routes := &recordingRouteBuilder{routes: []AttemptRoute{first, second}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{
		retryablePortOutcome("target-a"), successfulPortOutcome("target-b"),
	}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: &recordingLocalExecutor{}, Remote: remote}

	executor.Run(portExecutorContext(portAttempt(1), portAttempt(2)))

	require.Equal(t, []uint{41, 42}, remote.routeIDs)
}

func TestExecutorHardAgentIDFreezesAcrossBusinessAttempts(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(true, "hard-a"), remotePortRoute(true, "hard-a")}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{retryablePortOutcome("hard-a"), successfulPortOutcome("hard-a")}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: &recordingLocalExecutor{}, Remote: remote}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))
	rctx.Input.HardSelector = app.AgentSelector{AgentID: "hard-a"}

	executor.Run(rctx)

	require.Equal(t, []string{"hard-a", "hard-a"}, []string{remote.targets[0].AgentID, remote.targets[1].AgentID})
	require.Empty(t, routes.inputs[0].FrozenHardAgentID)
	require.Equal(t, "hard-a", routes.inputs[1].FrozenHardAgentID)
}

func TestExecutorHardTagFreezesFirstNonTransportUnavailableTarget(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{
		remotePortRoute(true, "member-a", "member-b"),
		remotePortRoute(true, "member-b"),
	}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{
		unavailablePortOutcome("member-a"), retryablePortOutcome("member-b"), successfulPortOutcome("member-b"),
	}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: &recordingLocalExecutor{}, Remote: remote}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))
	rctx.Input.HardSelector = app.AgentSelector{AgentTag: "gpu"}

	executor.Run(rctx)

	require.Equal(t, []string{"member-a", "member-b", "member-b"}, []string{
		remote.targets[0].AgentID, remote.targets[1].AgentID, remote.targets[2].AgentID,
	})
	require.Equal(t, "member-b", routes.inputs[1].FrozenHardAgentID)
}

func TestExecutorSoftRemoteTransportExhaustionFallsBackToSourceLocal(t *testing.T) {
	route := remotePortRoute(false, "member-a", "member-b")
	route.Targets = append(route.Targets, AttemptTarget{AgentID: "source", Kind: AttemptTargetLocal})
	routes := &recordingRouteBuilder{routes: []AttemptRoute{route}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{unavailablePortOutcome("member-a"), unavailablePortOutcome("member-b")}}
	local := &recordingLocalExecutor{outcomes: []AttemptOutcome{successfulPortOutcome("source")}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: local, Remote: remote}

	executor.Run(portExecutorContext(portAttempt(1)))

	require.Equal(t, []string{"member-a", "member-b"}, []string{remote.targets[0].AgentID, remote.targets[1].AgentID})
	require.Len(t, local.attempts, 1)
}

func TestExecutorRemoteProviderFailureAdvancesPlanWithoutChangingAgent(t *testing.T) {
	first := remotePortRoute(false, "member-a", "member-b")
	first.Targets = append(first.Targets, AttemptTarget{AgentID: "source", Kind: AttemptTargetLocal})
	routes := &recordingRouteBuilder{routes: []AttemptRoute{first, localPortRoute()}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{retryablePortOutcome("member-a")}}
	local := &recordingLocalExecutor{outcomes: []AttemptOutcome{successfulPortOutcome("source")}}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: local, Remote: remote}

	executor.Run(portExecutorContext(portAttempt(1), portAttempt(2)))

	require.Equal(t, []AttemptTarget{{AgentID: "member-a", Kind: AttemptTargetRemote}}, remote.targets)
	require.Len(t, local.attempts, 1)
	require.Equal(t, uint(2), local.attempts[0].SourceID)
}

func TestExecutorHardTransportExhaustionStopsWithoutLocalFallback(t *testing.T) {
	routes := &recordingRouteBuilder{routes: []AttemptRoute{remotePortRoute(true, "member-a", "member-b"), localPortRoute()}}
	remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{unavailablePortOutcome("member-a"), unavailablePortOutcome("member-b")}}
	local := &recordingLocalExecutor{}
	executor := &Executor{SourceAgentID: "source", Routes: routes, Local: local, Remote: remote}
	rctx := portExecutorContext(portAttempt(1), portAttempt(2))
	rctx.Input.HardSelector = app.AgentSelector{AgentTag: "gpu"}

	executor.Run(rctx)

	require.Len(t, routes.inputs, 1)
	require.Empty(t, local.attempts)
	require.Error(t, rctx.State.Execution.Err)
}

// behavior change: terminal no-replay outcomes stop before every same-attempt
// target/local fallback and before the next business attempt is built.
func TestExecutorNoReplayOutcomesStopImmediately(t *testing.T) {
	tests := []struct {
		name    string
		outcome AttemptOutcome
	}{
		{name: "response started", outcome: AttemptOutcome{
			Kind: AttemptProviderFailed, ExecutionAgentID: "member-a", Path: app.RoutePathDirect, Commit: tunnel.Committed,
			ProviderResultKnown: true, ProviderDispatched: true, PlanAdvanceAllowed: true, ResponseStarted: true,
			Result: state.AttemptResult{Err: errors.New("stream broke"), Written: true},
		}},
		{name: "direct uncertain", outcome: AttemptOutcome{
			Kind: AttemptCommitUncertain, ExecutionAgentID: "member-a", Path: app.RoutePathDirect, Commit: tunnel.CommitUncertain,
			ProviderResultKnown: true, Result: state.AttemptResult{Err: errors.New("direct ack lost")},
		}},
		{name: "relay uncertain", outcome: AttemptOutcome{
			Kind: AttemptCommitUncertain, ExecutionAgentID: "member-a", Path: app.RoutePathRelay, Commit: tunnel.CommitUncertain,
			ProviderResultKnown: true, Result: state.AttemptResult{Err: errors.New("relay ack lost")},
		}},
		{name: "cancel", outcome: AttemptOutcome{
			Kind: AttemptCanceled, ExecutionAgentID: "member-a", Path: app.RoutePathDirect,
			ProviderResultKnown: true, Result: state.AttemptResult{Err: context.Canceled},
		}},
		{name: "deadline", outcome: AttemptOutcome{
			Kind: AttemptCanceled, ExecutionAgentID: "member-a", Path: app.RoutePathRelay,
			ProviderResultKnown: true, Result: state.AttemptResult{Err: context.DeadlineExceeded},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			firstRoute := remotePortRoute(false, "member-a", "member-b")
			firstRoute.Targets = append(firstRoute.Targets, AttemptTarget{AgentID: "source", Kind: AttemptTargetLocal})
			routes := &recordingRouteBuilder{routes: []AttemptRoute{firstRoute, localPortRoute()}}
			remote := &recordingRemoteExecutor{outcomes: []AttemptOutcome{tt.outcome}}
			local := &recordingLocalExecutor{}
			executor := &Executor{SourceAgentID: "source", Routes: routes, Local: local, Remote: remote}
			rctx := portExecutorContext(portAttempt(1), portAttempt(2))

			executor.Run(rctx)

			require.Equal(t, []AttemptTarget{{AgentID: "member-a", Kind: AttemptTargetRemote}}, remote.targets)
			require.Empty(t, local.attempts)
			require.Len(t, routes.inputs, 1)
			require.Equal(t, uint(1), routes.inputs[0].Attempt.SourceID)
			require.Error(t, rctx.State.Execution.Err)
		})
	}
}

func TestExecutorNilEmptyPlanAndRouteBuildErrorAreTerminalSafe(t *testing.T) {
	require.NotPanics(t, func() { (&Executor{}).Run(nil) })

	routes := &recordingRouteBuilder{}
	empty := portExecutorContext()
	require.NotPanics(t, func() { (&Executor{Routes: routes}).Run(empty) })
	require.Empty(t, routes.inputs)
	require.NoError(t, empty.State.Execution.Err)

	buildErr := errors.New("route build unavailable")
	routes = &recordingRouteBuilder{routes: []AttemptRoute{{}}, errs: []error{buildErr}}
	rctx := portExecutorContext(portAttempt(1))
	require.NotPanics(t, func() { (&Executor{SourceAgentID: "source", Routes: routes}).Run(rctx) })
	require.ErrorIs(t, rctx.State.Execution.Err, buildErr)
	require.Len(t, rctx.State.Execution.History, 1)
}
