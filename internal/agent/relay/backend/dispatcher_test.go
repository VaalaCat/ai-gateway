package backend

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/legacy"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/native"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/passthrough"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDispatcherTransferProgressWithoutTrace(t *testing.T) {
	for _, mode := range []state.RelayMode{state.ModeNative, state.ModePassthrough, state.ModeLegacy} {
		t.Run(string(mode), func(t *testing.T) {
			payload := fmt.Sprintf(`{"id":"reply","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":%q},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`, strings.Repeat("answer", 1000))
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(payload))
			}))
			defer server.Close()
			registry := inflight.NewRegistry(nil, 0)
			entry := registry.Track(inflight.Meta{})
			entry.SetCurrentAttempt(&inflight.AttemptInProgress{Seq: 1})
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			requestBody := []byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`)
			context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(requestBody)))
			context.Request.Header.Set("Content-Type", "application/json")
			context.Writer = entry.WrapClientWriter(context.Writer)
			relay := &state.RelayContext{
				Context: context, Inflight: entry,
				Input: state.RelayInput{Body: requestBody, Model: "gpt-4o", InboundProto: llmkit.ProtocolOpenAIChat, StartTime: time.Now()},
				State: &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 32)},
			}
			channel := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: server.URL}, Key: "test-key"}
			result := NewDispatcher(nil).Dispatch(relay, state.Attempt{Channel: channel, RealModel: "gpt-4o", Mode: mode})
			require.NoError(t, result.Err)
			snapshot := registry.Snapshot()[0]
			require.NotNil(t, snapshot.Progress.UpstreamBodyBytes)
			require.EqualValues(t, len(payload), *snapshot.Progress.UpstreamBodyBytes)
			require.EqualValues(t, recorder.Body.Len(), *snapshot.Progress.ClientBodyBytes)
			require.Equal(t, snapshot.Progress, *snapshot.AttemptProgress)
			require.NotNil(t, snapshot.Progress.UpstreamIdleMs)
			require.NotNil(t, snapshot.Progress.ClientIdleMs)
			require.Less(t, len(relay.State.Recorder.UpstreamBodyBytes()), len(payload))
		})
	}
}

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
		State: &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 0)},
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
		State: &state.RelayState{Recorder: trace.NewRecorder(trace.CaptureOff, 0)},
	}
	res := d.Dispatch(rctx, state.Attempt{Mode: state.ModeNative, RealModel: "gpt-4"})
	if res.TokenSource == "" {
		t.Error("TokenSource should be filled after Dispatch")
	}
	if fake.callCount != 1 {
		t.Errorf("backend should be called exactly once, got %d", fake.callCount)
	}
}

// TestDispatcherPreservesFullyCachedInput 验证缓存输入不被本地文本估算重复记入普通输入。
func TestDispatcherPreservesFullyCachedInput(t *testing.T) {
	// 1. 上游报告全部输入来自缓存，完成文本仍有独立输出计数。
	for _, counts := range []state.AttemptResult{
		{CacheReadTokens: 100, CompletionTokens: 5, ResponseText: "answer"},
		{CacheReadTokens: 100},
		{CacheWriteTokens: 100, CompletionTokens: 5},
	} {
		d := &Dispatcher{Backends: map[state.RelayMode]Backend{state.ModeNative: &fakeBackend{result: counts}}}
		rctx := &state.RelayContext{Input: state.RelayInput{
			Body: []byte(`{"model":"gemini","messages":[{"role":"user","content":"a cached prompt with nonempty text"}]}`),
		}}
		result := d.Dispatch(rctx, state.Attempt{Mode: state.ModeNative, RealModel: "gemini"})
		// 2. 只保留上游缓存桶，普通输入不补估算值。
		if result.PromptTokens != 0 || result.CacheReadTokens != counts.CacheReadTokens || result.CacheWriteTokens != counts.CacheWriteTokens || result.CompletionTokens != counts.CompletionTokens || result.TokenSource == "none" {
			t.Fatalf("fully cached result changed: %+v", result)
		}
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

func TestDispatcherInjectsSharedLLMKitDependenciesIntoNative(t *testing.T) {
	dispatcher := NewDispatcher(nil)
	backend, ok := dispatcher.Backends[state.ModeNative].(*native.Backend)
	if !ok {
		t.Fatalf("native backend = %T, want *native.Backend", dispatcher.Backends[state.ModeNative])
	}
	if backend.Codec == nil || backend.Client == nil {
		t.Fatalf("native dependencies not injected: Codec=%v Client=%v", backend.Codec, backend.Client)
	}
}
