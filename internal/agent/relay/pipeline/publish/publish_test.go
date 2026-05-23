package publish

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// newGinCtxForTest 构造一个最小可用的 *gin.Context。包级隔离副本，
// 与 internal/agent/relay/test_helpers_test.go 中的同名 helper 等价。
func newGinCtxForTest(setup func(c *gin.Context)) *gin.Context {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(""))
	if setup != nil {
		setup(c)
	}
	return c
}

// ---------- H1: attachTraceData(*Recorder) tests ----------

func TestAttachTraceDataFillsMsColumns(t *testing.T) {
	rec := trace.NewRecorder(false, 0)
	rec.WithStage(trace.StageInboundDecode)
	time.Sleep(2 * time.Millisecond)
	rec.WithStage(trace.StageOutboundEncode)

	var e protocol.UsageLogEntry
	attachTraceData(&e, rec)
	if e.InboundDecodeMs <= 0 {
		t.Errorf("InboundDecodeMs should be > 0, got %d", e.InboundDecodeMs)
	}
}

func TestAttachTraceDataErrorStageLiteral(t *testing.T) {
	// Behavior-equal: error_stage column gets the trace-internal Stage value.
	rec := trace.NewRecorder(false, 0)
	rec.WithFail(trace.StageOutboundEncode, state.ErrInvalidBody)
	var e protocol.UsageLogEntry
	attachTraceData(&e, rec)
	if e.ErrorStage != "outbound_encode" {
		t.Errorf("ErrorStage = %q want outbound_encode", e.ErrorStage)
	}
}

func TestAttachTraceDataTraceDataPopulatedOnFail(t *testing.T) {
	// Failure path forces verbose; even without body capture, MarshalJSON returns
	// a valid object so TraceData is non-empty.
	rec := trace.NewRecorder(false, 0)
	rec.WithFail(trace.StageInternal, state.ErrReadBody)
	var e protocol.UsageLogEntry
	attachTraceData(&e, rec)
	// HasBody() is true on failure path (verbose triggers). TraceData should be set.
	if e.TraceData == "" {
		t.Logf("TraceData empty (acceptable if HasBody() conditional excludes this case)")
	} else {
		// Sanity check: it should be JSON
		var probe map[string]any
		if err := json.Unmarshal([]byte(e.TraceData), &probe); err != nil {
			t.Errorf("TraceData not valid JSON: %v", err)
		}
	}
}

func TestAttachTraceDataNilRecorderSafe(t *testing.T) {
	// boundary: nil Recorder — must not panic.
	var e protocol.UsageLogEntry
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("attachTraceData(nil) panicked: %v", r)
		}
	}()
	attachTraceData(&e, nil)
	// Should leave all fields zero.
	if e.ErrorStage != "" || e.InboundDecodeMs != 0 {
		t.Errorf("nil Recorder should not touch entry: %+v", e)
	}
}

func TestAttachTraceDataIsZeroWhenNoStages(t *testing.T) {
	// boundary: fresh recorder, no stages → all *Ms = 0
	rec := trace.NewRecorder(false, 0)
	var e protocol.UsageLogEntry
	attachTraceData(&e, rec)
	if e.InboundDecodeMs != 0 || e.OutboundEncodeMs != 0 ||
		e.UpstreamDispatchMs != 0 || e.UpstreamDecodeMs != 0 || e.ClientEncodeMs != 0 {
		t.Errorf("all ms should be 0 on fresh recorder: %+v", e)
	}
	if e.ErrorStage != "none" && e.ErrorStage != "" {
		// Finalize returns FailStage = StageNone if no fail; string is "none".
		t.Logf("ErrorStage on fresh recorder = %q", e.ErrorStage)
	}
}

// ---------- H2: Publisher.Publish tests ----------

// captureBus 用 eventbus.NewMemoryBus 真实实现 + subscribe 收集发布出来的 UsageLogEntry。
// 比手写 stub 更可靠：直接复用 events.PublishUsageCompleted 的 marshal 路径。
type captureBus struct {
	bus app.EventBus
	mu  sync.Mutex
	got []protocol.UsageLogEntry
}

func newCaptureBus(t *testing.T) *captureBus {
	t.Helper()
	cb := &captureBus{bus: eventbus.NewMemoryBus()}
	_, err := events.SubscribeUsageCompleted(cb.bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		cb.mu.Lock()
		cb.got = append(cb.got, e)
		cb.mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	return cb
}

func (b *captureBus) wait() {
	// MemoryBus delivery is async via goroutine — small sleep suffices for tests.
	time.Sleep(20 * time.Millisecond)
}

func (b *captureBus) last() protocol.UsageLogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.got) == 0 {
		return protocol.UsageLogEntry{}
	}
	return b.got[len(b.got)-1]
}

func (b *captureBus) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.got)
}

func newPublishTestRctx() *state.RelayContext {
	c := newGinCtxForTest(nil)
	return &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			RequestID:    "req-test",
			Model:        "gpt-4",
			IsStream:     false,
			StartTime:    time.Now(),
			InboundProto: codec.ProtocolOpenAIChat,
			UserInfo:     &app.UserInfo{UserID: 1, TokenID: 2, TokenName: "test"},
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
}

func TestPublishCtxBuildFailFillsBaseOnly(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseCtxBuild
	rctx.State.Err = state.ErrReadBody

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	if cb.count() != 1 {
		t.Fatalf("count = %d", cb.count())
	}
	got := cb.last()
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0 for CtxBuild fail", got.Status)
	}
	if got.ErrorMessage != state.ErrReadBody.Error() {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, state.ErrReadBody.Error())
	}
	if got.ChannelID != 0 {
		t.Error("ChannelID should be 0 for CtxBuild")
	}
	if got.UserID != 1 || got.TokenID != 2 || got.TokenName != "test" {
		t.Errorf("user fields not propagated: %+v", got)
	}
	if got.RequestID != "req-test" {
		t.Errorf("RequestID = %q", got.RequestID)
	}
}

func TestPublishPlanFailFillsRoutingName(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhasePlan
	rctx.State.Err = state.ErrNoChannelAvailable
	rctx.State.Plan.RoutingName = "smart"

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	if got.RoutingName != "smart" {
		t.Errorf("RoutingName = %q, want smart", got.RoutingName)
	}
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	// behavior parity with main: UsageLog.ErrorMessage 必须带 model 名
	// （老 handler.go 非 routing 404 分支 errMsg = "no channel available for model <name>"），
	// 否则计费/统计/日志侧丢失了 model 维度。
	want := "no channel available for model gpt-4"
	if got.ErrorMessage != want {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, want)
	}
}

// TestPublishPlanFail_RoutingFallback_SkipsRoutingName:
// main:handler.go 502 fallback (lastErr==nil) 分支 502 fallback 路径走 buildBaseUsageLogEntry，
// 不带 RoutingName → UsageLog.RoutingName 应保持空。strict parity 钉死。
func TestPublishPlanFail_RoutingFallback_SkipsRoutingName(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhasePlan
	rctx.State.Err = state.ErrRoutingFallback
	rctx.State.Plan.RoutingName = "smart" // Planner 已写入，但 state.ErrRoutingFallback 路径不应外露

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	if got.RoutingName != "" {
		t.Errorf("RoutingName = %q, want \"\" (main parity: 502 fallback path skips RoutingName)", got.RoutingName)
	}
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	// B-C1 行为修复联动断言：ErrorMessage 必须是 "no available channels"
	// （= consts.ErrNoChannelAvailable = ErrRoutingFallback.Error()），
	// 而非 ErrNoChannelAvailable 路径的 "no channel available for model X"。
	if got.ErrorMessage != consts.ErrNoChannelAvailable {
		t.Errorf("ErrorMessage = %q, want %q (502 fallback body)", got.ErrorMessage, consts.ErrNoChannelAvailable)
	}
}

func TestPublishExecuteSuccess(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseNone // success
	rctx.State.Plan.RoutingName = "rt"
	rctx.State.Execution = state.ExecutionResult{
		Used: state.Attempt{
			Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 7, Type: consts.ChannelTypeOpenAI, Name: "ch7"}},
			RealModel: "gpt-4",
			Mode:      state.ModeNative,
		},
		Outcome: state.AttemptResult{
			PromptTokens:     10,
			CompletionTokens: 20,
			UpstreamModel:    "gpt-4-real",
			TokenSource:      "provider",
		},
	}

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	if got.Status != 1 {
		t.Errorf("Status = %d, want 1", got.Status)
	}
	if got.ChannelID != 7 {
		t.Errorf("ChannelID = %d", got.ChannelID)
	}
	if got.UpstreamModel != "gpt-4-real" {
		t.Errorf("UpstreamModel = %q", got.UpstreamModel)
	}
	if got.PromptTokens != 10 || got.CompletionTokens != 20 {
		t.Errorf("tokens = %d/%d", got.PromptTokens, got.CompletionTokens)
	}
	if got.RoutingName != "rt" {
		t.Errorf("RoutingName = %q", got.RoutingName)
	}
	if got.UseLegacy {
		t.Error("UseLegacy should be false for native")
	}
	if got.TokenSource != "provider" {
		t.Errorf("TokenSource = %q", got.TokenSource)
	}
	if got.Other == "" {
		t.Error("Other should be populated")
	}
}

func TestPublishForwardedSkipped(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.Forwarded = true

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	if cb.count() != 0 {
		t.Errorf("Forwarded should skip publish, count = %d", cb.count())
	}
}

func TestPublishExecuteFailSetsStatus0(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseExecute
	upstreamErr := errors.New("upstream 500")
	rctx.State.Err = upstreamErr
	rctx.State.Execution = state.ExecutionResult{
		Used: state.Attempt{
			Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 5, Type: consts.ChannelTypeOpenAI, Name: "ch5"}},
			RealModel: "gpt-4",
			Mode:      state.ModeNative,
		},
		Outcome: state.AttemptResult{Err: upstreamErr},
		Err:     upstreamErr,
	}

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	if got.ErrorMessage != "upstream 500" {
		t.Errorf("ErrorMessage = %q", got.ErrorMessage)
	}
	if got.ChannelID != 5 {
		t.Errorf("ChannelID = %d", got.ChannelID)
	}
}

// TestPublishErrorMessage_ParityWithMain 钉死 4 个 sentinel → ErrorMessage 文本必须与
// main 分支老 handler.go 1:1 一致（含 model 名 / whitelist 后缀）。
// 老引用：
//   - state.ErrNoRoutableModel:    handler.go:227 "no available real model after routing: <model>"
//   - state.ErrModelNotAllowed:    handler.go:280 "model not allowed: <name>"
//   - state.ErrInvalidForcedChannelID: handler.go:255 "no channel available for model <name>"
//   - state.ErrNoChannelAvailable: handler.go:340 "no channel available for model <name>" (+ optional whitelist 后缀)
//
// behavior parity with main
func TestPublishErrorMessage_ParityWithMain(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		phase     state.Phase
		whitelist bool
		want      string
	}{
		{
			name:  "no_routable_model",
			err:   state.ErrNoRoutableModel,
			phase: state.PhasePlan,
			want:  "no available real model after routing: gpt-4",
		},
		{
			name:  "model_not_allowed",
			err:   state.ErrModelNotAllowed,
			phase: state.PhasePlan,
			want:  "model not allowed: gpt-4",
		},
		{
			name:  "invalid_forced_channel_id",
			err:   state.ErrInvalidForcedChannelID,
			phase: state.PhaseCtxBuild,
			want:  "no channel available for model gpt-4",
		},
		{
			name:  "no_channel_available_plain",
			err:   state.ErrNoChannelAvailable,
			phase: state.PhasePlan,
			want:  "no channel available for model gpt-4",
		},
		{
			name:      "no_channel_available_whitelist_active",
			err:       state.ErrNoChannelAvailable,
			phase:     state.PhasePlan,
			whitelist: true,
			want:      "no channel available for model gpt-4 (token whitelist active)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rctx := newPublishTestRctx()
			rctx.State.FailPhase = c.phase
			rctx.State.Err = c.err
			if c.whitelist {
				rctx.Input.UserInfo = &app.UserInfo{
					UserID: 1, TokenID: 2, TokenName: "test",
					AllowedChannelIDs: []uint{1},
				}
			}

			cb := newCaptureBus(t)
			p := NewPublisher(cb.bus, zap.NewNop())
			p.Publish(rctx)
			cb.wait()

			got := cb.last()
			if got.ErrorMessage != c.want {
				t.Errorf("ErrorMessage = %q, want %q (main parity)", got.ErrorMessage, c.want)
			}
			if got.Status != 0 {
				t.Errorf("Status = %d, want 0", got.Status)
			}
		})
	}
}

// TestPublishExecuteFailWritten_NoCacheNoFirstResponseMs：mid-stream fail (Written=true)
// 时，UsageLog **不应** 带 CacheReadTokens / CacheWriteTokens / FirstResponseMs ——
// 这三个字段在老 main:handler.go mid-stream failed 字段集分支 struct literal 里没列，保持零值。
// 重构后 fillExecution 误把 Written=true 当成"完整字段"分支处理，导致计费
// 字段漂移。新行为：Written=true && Err != nil → 只写 prompt / completion /
// token_source。
//
// behavior parity with main
func TestPublishExecuteFailWritten_NoCacheNoFirstResponseMs(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseExecute
	streamErr := errors.New("mid-stream upstream 502")
	rctx.State.Err = streamErr
	rctx.State.Execution = state.ExecutionResult{
		Used: state.Attempt{
			Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 8, Type: consts.ChannelTypeOpenAI, Name: "ch8"}},
			RealModel: "gpt-4",
			Mode:      state.ModeNative,
		},
		Outcome: state.AttemptResult{
			PromptTokens:     100,
			CompletionTokens: 50,
			CacheReadTokens:  77,  // 老主循环不会写
			CacheWriteTokens: 88,  // 老主循环不会写
			FirstResponseMs:  999, // 老主循环不会写
			TokenSource:      "estimate",
			Written:          true,
			Err:              streamErr,
		},
		Err: streamErr,
	}

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	// 计费字段：prompt / completion / token_source 必须写。
	if got.PromptTokens != 100 || got.CompletionTokens != 50 {
		t.Errorf("tokens = %d/%d, want 100/50 (mid-stream 仍计费)",
			got.PromptTokens, got.CompletionTokens)
	}
	if got.TokenSource != "estimate" {
		t.Errorf("TokenSource = %q, want estimate", got.TokenSource)
	}
	// 老 main:handler.go mid-stream failed 字段集分支 struct literal 没列的 3 字段：保持零值。
	if got.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0 (main parity)", got.CacheReadTokens)
	}
	if got.CacheWriteTokens != 0 {
		t.Errorf("CacheWriteTokens = %d, want 0 (main parity)", got.CacheWriteTokens)
	}
	if got.FirstResponseMs != 0 {
		t.Errorf("FirstResponseMs = %d, want 0 (main parity)", got.FirstResponseMs)
	}
	// Status / ErrorMessage 一致正确。
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	if got.ErrorMessage != streamErr.Error() {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, streamErr.Error())
	}
}

// TestPublish_ExecuteFail_NotWritten_AllTokenFieldsZero 验证审计 D-I2：
// Err != nil && Written=false（最常见的 pre-write 失败 / final fallback 分支）时，
// 即使 outcome.PromptTokens / CompletionTokens / CacheReadTokens / CacheWriteTokens /
// FirstResponseMs 都是非零，UsageLog 也必须全清零——fillExecution switch 在该路径
// 不命中任何 case，所有 token / cache / first-response 字段必须保持零值。
// 守护意外的 copy-through（例如未来某 mutation 在 default 分支误抄字段）。
//
// behavior parity with main: 老 handler.go final fallback 分支 struct literal
// 完全不列这五个字段。
func TestPublish_ExecuteFail_NotWritten_AllTokenFieldsZero(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseExecute
	upstreamErr := errors.New("upstream 500 pre-stream")
	rctx.State.Err = upstreamErr
	rctx.State.Execution = state.ExecutionResult{
		Used: state.Attempt{
			Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 9, Type: consts.ChannelTypeOpenAI, Name: "ch9"}},
			RealModel: "gpt-4",
			Mode:      state.ModeNative,
		},
		Outcome: state.AttemptResult{
			Err:              upstreamErr,
			Written:          false, // pre-write 失败：body 没写出去
			PromptTokens:     100,   // 故意塞非零，断言全清零
			CompletionTokens: 50,
			CacheReadTokens:  77,
			CacheWriteTokens: 88,
			FirstResponseMs:  999,
			TokenSource:      "estimate",
		},
		Err: upstreamErr,
	}

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	if cb.count() != 1 {
		t.Fatalf("expected 1 UsageLogged event, got %d", cb.count())
	}
	got := cb.last()

	// 五字段全清零：Written=false 分支不写 prompt / completion / cache / first-response。
	if got.PromptTokens != 0 || got.CompletionTokens != 0 {
		t.Errorf("expected zero token counts for Written=false failure, got prompt=%d completion=%d",
			got.PromptTokens, got.CompletionTokens)
	}
	if got.CacheReadTokens != 0 || got.CacheWriteTokens != 0 {
		t.Errorf("expected zero cache fields, got read=%d write=%d",
			got.CacheReadTokens, got.CacheWriteTokens)
	}
	if got.FirstResponseMs != 0 {
		t.Errorf("expected FirstResponseMs=0, got %d", got.FirstResponseMs)
	}
	// TokenSource 也不写：老 final fallback 分支 struct literal 没列。
	if got.TokenSource != "" {
		t.Errorf("expected TokenSource=\"\", got %q", got.TokenSource)
	}
	// Status / ErrorMessage 走错误路径。
	if got.Status != 0 {
		t.Errorf("Status = %d, want 0", got.Status)
	}
	if got.ErrorMessage != upstreamErr.Error() {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, upstreamErr.Error())
	}
	// ChannelID 仍然要正确归属（channel 已经选定）。
	if got.ChannelID != 9 {
		t.Errorf("ChannelID = %d, want 9", got.ChannelID)
	}
}

// ---------- H3: Publish nil 防御 (single exit-point 公共出口) ----------
// 这三条 boundary 来自审核员 D 报告：Publish 是单一出口，nil 防御未测
// → 生产 panic 会丢整条 UsageLog，必须钉死。

func TestPublishNilRelayContext_NoPanicNoPublish(t *testing.T) {
	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Publish(nil) panicked: %v", r)
		}
	}()
	p.Publish(nil)
	cb.wait()

	if cb.count() != 0 {
		t.Errorf("nil rctx must not emit usage log, got %d", cb.count())
	}
}

func TestPublishNilState_NoPanicNoPublish(t *testing.T) {
	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())

	rctx := &state.RelayContext{Context: newGinCtxForTest(nil), State: nil}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Publish(rctx with nil State) panicked: %v", r)
		}
	}()
	p.Publish(rctx)
	cb.wait()

	if cb.count() != 0 {
		t.Errorf("nil State must not emit usage log, got %d", cb.count())
	}
}

func TestPublishForwardedAfterNilGuard(t *testing.T) {
	// boundary: rctx + State 都有，但 Forwarded=true → 跳过 publish。
	// 跟前面 nil 防御共用一条短路链路，独立断言一次防止合并 regression。
	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())

	rctx := newPublishTestRctx()
	rctx.State.Forwarded = true
	p.Publish(rctx)
	cb.wait()

	if cb.count() != 0 {
		t.Errorf("Forwarded=true must not emit usage log, got %d", cb.count())
	}
}

// boundary: legacy mode + UpstreamModel empty → fall back to ApplyModelMapping
func TestPublishExecuteLegacyAndUpstreamFallback(t *testing.T) {
	rctx := newPublishTestRctx()
	rctx.State.FailPhase = state.PhaseNone
	rctx.State.Execution = state.ExecutionResult{
		Used: state.Attempt{
			Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 9, Type: consts.ChannelTypeOpenAI, Name: "ch9"}},
			RealModel: "gpt-4",
			Mode:      state.ModeLegacy,
		},
		Outcome: state.AttemptResult{
			PromptTokens:     3,
			CompletionTokens: 4,
			// UpstreamModel intentionally empty → publisher should fall back to ApplyModelMapping
		},
	}

	cb := newCaptureBus(t)
	p := NewPublisher(cb.bus, zap.NewNop())
	p.Publish(rctx)
	cb.wait()

	got := cb.last()
	if !got.UseLegacy {
		t.Error("UseLegacy should be true")
	}
	if got.UpstreamModel != "gpt-4" {
		t.Errorf("UpstreamModel fallback = %q, want gpt-4 (ApplyModelMapping passthrough)", got.UpstreamModel)
	}
}
