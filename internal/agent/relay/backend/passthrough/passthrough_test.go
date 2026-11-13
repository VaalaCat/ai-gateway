package passthrough

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/firstresponse"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/datatypes"
)

// ==================== Task 12 baseline case ====================

// TestApplyPassthroughOverrides_UnmarshalError_NoWarnLog verifies audit fix #13:
// 当 ch.ParamOverride / ch.HeaderOverride 为非法 JSON 时，passthrough 必须静默
// fallback（不产生 Warn 日志），与 main:native.go:552-558 的行为对齐。
//
// 旧实现：每个 override 解析失败各打 1 条 Warn ("passthrough: unmarshal ..."），
// 导致生产环境告警噪音。新实现：降到 Debug 级。
func TestApplyPassthroughOverrides_UnmarshalError_NoWarnLog(t *testing.T) {
	core, recorded := observer.New(zap.DebugLevel)
	logger := zap.New(core)

	ch := &models.Channel{ChannelCore: models.ChannelCore{ID: 1, ParamOverride: "{bad json"}, HeaderOverride: "{also bad"}

	req, err := http.NewRequest(http.MethodPost, "http://example.com/v1/chat", strings.NewReader(""))
	if err != nil {
		t.Fatalf("build req: %v", err)
	}

	got := applyPassthroughOverrides(req, []byte(`{"model":"x"}`), ch, logger)

	// 两段 override 都解析失败 + ApplyOverrides 对 nil/nil 是 no-op，因此 body 应原样返回。
	if string(got) != `{"model":"x"}` {
		t.Fatalf("expected body unchanged on unmarshal failure, got %q", string(got))
	}

	warnLogs := recorded.FilterLevelExact(zap.WarnLevel).
		FilterMessageSnippet("unmarshal").All()
	if len(warnLogs) != 0 {
		t.Fatalf("expected 0 Warn logs about override unmarshal, got %d: %v",
			len(warnLogs), warnLogs)
	}

	debugLogs := recorded.FilterLevelExact(zap.DebugLevel).
		FilterMessageSnippet("unmarshal").All()
	if len(debugLogs) != 2 {
		t.Fatalf("expected 2 Debug logs (param + header unmarshal), got %d: %v",
			len(debugLogs), debugLogs)
	}
}

// ==================== Task 15: 7 new helpers ====================

// newPassthroughTestCtx 构造一个最小可用的 RelayContext + gin.Context，
// 把 c.Request 指向 baseURL+path，便于 backend.Relay 走完整链路。
//
// 调用方负责后续设置 ch.BaseURL 指向 httptest.NewServer.URL。
func newPassthroughTestCtx(t *testing.T, body []byte, isStream bool) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest(http.MethodPost, "http://gateway/v1/chat/completions",
		strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	startedAt := time.Now().Add(-100 * time.Millisecond)
	tracker := firstresponse.NewTracker(startedAt)
	c.Writer = firstresponse.Wrap(c.Writer, tracker)
	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			Body:         body,
			Model:        "gpt-4o",
			InboundProto: llmkit.ProtocolOpenAIChat,
			IsStream:     isStream,
			StartTime:    startedAt,
		},
		State: &state.RelayState{
			Recorder:      trace.NewRecorderAt(trace.CaptureOff, 0, startedAt),
			FirstResponse: tracker,
		},
	}
	return rctx, w
}

// makeChannel 构造一个最小 passthrough channel，BaseURL 指向 httptest server。
func makeChannel(baseURL string) *models.Channel {
	return &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: baseURL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"}
}

type passthroughScriptProvider struct {
	scripts []*script.Compiled
}

func (p passthroughScriptProvider) MatchScripts(_ script.MatchInput) []*script.Compiled {
	return p.scripts
}

type passthroughScriptCache struct {
	app.AgentCache
	eng *script.Engine
}

func (c passthroughScriptCache) ScriptEngine() *script.Engine {
	return c.eng
}

type passthroughScriptAgent struct {
	app.AgentApplication
	cache app.AgentCache
}

func (a passthroughScriptAgent) GetCache() app.AgentCache              { return a.cache }
func (a passthroughScriptAgent) GetBodyStore() app.BodyStore           { return nil }
func (a passthroughScriptAgent) GetLogger() *zap.Logger                { return zap.NewNop() }
func (a passthroughScriptAgent) GetConfig() *config.AgentRuntimeConfig { return nil }
func (a passthroughScriptAgent) GetTransportPool() app.TransportPool   { return nil }
func (a passthroughScriptAgent) RelayTimeout() time.Duration           { return 0 }

func newScriptTestAgent(t *testing.T, code string) app.AgentApplication {
	t.Helper()
	compiled, err := script.Compile(models.AdminScript{Name: "test-script", Code: code})
	if err != nil {
		t.Fatalf("compile script: %v", err)
	}
	eng := script.NewEngine(passthroughScriptProvider{scripts: []*script.Compiled{compiled}}, zap.NewNop(), time.Second)
	return passthroughScriptAgent{cache: passthroughScriptCache{eng: eng}}
}

// TestBackend_ParamOverrideApplied verifies that ch.ParamOverride is merged into the
// outbound JSON body before reaching the upstream server (top-level shallow merge).
func TestBackend_ParamOverrideApplied(t *testing.T) {
	var gotTopP any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(b, &parsed)
		gotTopP = parsed["top_p"]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	ch.ParamOverride = `{"top_p":0.9}`

	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotTopP == nil {
		t.Fatal("upstream did not receive top_p; param override not applied")
	}
	if v, ok := gotTopP.(float64); !ok || v != 0.9 {
		t.Errorf("expected top_p=0.9 reached upstream, got %v (%T)", gotTopP, gotTopP)
	}
}

// TestBackend_HeaderOverrideApplied verifies that ch.HeaderOverride sets headers
// on the outbound upstream request.
func TestBackend_HeaderOverrideApplied(t *testing.T) {
	var gotVersion string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotVersion = r.Header.Get("X-Anthropic-Version")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	ch.HeaderOverride = `{"X-Anthropic-Version":"2023-06-01"}`

	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotVersion != "2023-06-01" {
		t.Errorf("expected X-Anthropic-Version=2023-06-01, got %q", gotVersion)
	}
}

func TestBackend_UpstreamScriptMutatesBody(t *testing.T) {
	var gotAdded any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(b, &parsed)
		gotAdded = parsed["script_added"]
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), false)
	rctx.Agent = newScriptTestAgent(t, `function onUpstreamRequest(ctx){ ctx.body.script_added = "yes" }`)
	backend := &Backend{Agent: rctx.Agent}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotAdded != "yes" {
		t.Fatalf("script mutation did not reach upstream, got %v (%T)", gotAdded, gotAdded)
	}
}

func TestBackend_UpstreamScriptRunsAfterHeaderOverride(t *testing.T) {
	var gotOverride string
	var gotRemove string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotOverride = r.Header.Get("X-Override")
		gotRemove = r.Header.Get("X-Remove")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	ch.HeaderOverride = `{"X-Override":"channel","X-Remove":"channel"}`
	rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"gpt-4o","messages":[]}`), false)
	rctx.Agent = newScriptTestAgent(t, `function onUpstreamRequest(ctx){ ctx.setHeader("X-Override", "script"); ctx.removeHeader("X-Remove") }`)
	backend := &Backend{Agent: rctx.Agent}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotOverride != "script" {
		t.Fatalf("script header should override channel header, got %q", gotOverride)
	}
	if gotRemove != "" {
		t.Fatalf("script should remove channel header, got %q", gotRemove)
	}
}

func TestBackend_UpstreamScriptRejectStopsPassthrough(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t, []byte(`{"model":"gpt-4o","messages":[]}`), false)
	rctx.Agent = newScriptTestAgent(t, `function onUpstreamRequest(ctx){ ctx.reject(451, "blocked by script") }`)
	backend := &Backend{Agent: rctx.Agent}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected script reject error")
	}
	if !got.Written {
		t.Fatalf("reject should mark Written=true to stop fallback, got %+v", got)
	}
	if called {
		t.Fatal("upstream should not be called after script reject")
	}
	if w.Code != http.StatusUnavailableForLegalReasons {
		t.Fatalf("response status = %d, want 451", w.Code)
	}
	if !strings.Contains(w.Body.String(), "blocked by script") || !strings.Contains(w.Body.String(), "script_rejected") {
		t.Fatalf("reject response body missing script error shape: %s", w.Body.String())
	}
}

// TestBackend_Upstream5xx_PropagatesError covers handlePassthroughErrorStatus 5xx
// branch: upstream 500 → AttemptResult.Err != nil, Written=false (retryable).
func TestBackend_Upstream5xx_PropagatesError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err from upstream 500")
	}
	if got.Written {
		t.Errorf("5xx must be retryable: Written should be false, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "upstream returned 500") {
		t.Errorf("expected upstream-500 wrap, got %q", got.Err.Error())
	}
}

// TestBackend_Upstream4xx_Returns4xxAsUpstreamError covers handlePassthroughErrorStatus
// 4xx 分支的新合约(T4 重构后):上游 400 → 返回 *common.UpstreamError,
// Written=false,客户端 w 未被写过(body 原样回写决策移交 Executor)。
func TestBackend_Upstream4xx_Returns4xxAsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err on 4xx")
	}
	if got.Written {
		t.Errorf("T4 后 backend 不再做 4xx body 回写 + Written=true 决策, got Written=true: %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T: %v", got.Err, got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error", upErr.ProviderErrorType)
	}
	// 客户端 w 应没被写过(body 回写已移到 Executor)。
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestBackend_StreamResponse_PropagatesSSE verifies streamPassthroughResponse writes
// chunks back to the client + extractPassthroughUsage parses SSE usage frames.
func TestBackend_StreamResponse_PropagatesSSE(t *testing.T) {
	sseBody := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" there\"}}],\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n" +
		"data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","stream":true,"messages":[]}`), true)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if !got.Written {
		t.Errorf("Written should be true for 2xx, got %+v", got)
	}
	if !strings.Contains(w.Body.String(), "hi") || !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("client did not receive SSE body, got %q", w.Body.String())
	}
	// SSE usage extraction
	if got.PromptTokens != 7 || got.CompletionTokens != 3 {
		t.Errorf("SSE usage = (%d,%d), want (7,3)", got.PromptTokens, got.CompletionTokens)
	}
	// ResponseText 应包含 SSE 拼接出来的 content
	if !strings.Contains(got.ResponseText, "hi") || !strings.Contains(got.ResponseText, " there") {
		t.Errorf("ResponseText missing content delta, got %q", got.ResponseText)
	}
}

func TestBackendStreamFirstResponseOwnedByClientWriter(t *testing.T) {
	const sseBody = ": keepalive\n\nevent: response.created\ndata: {}\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		flusher := writer.(http.Flusher)
		_, _ = io.WriteString(writer, ": keepalive\n\n")
		flusher.Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(writer, "event: response.created\ndata: {}\n\n")
	}))
	defer upstream.Close()

	rctx, response := newPassthroughTestCtx(
		t,
		[]byte(`{"model":"gpt-4o","stream":true,"messages":[]}`),
		true,
	)
	result := (&Backend{}).Relay(rctx, state.Attempt{
		Channel: makeChannel(upstream.URL), RealModel: "gpt-4o",
	})

	if result.Err != nil {
		t.Fatalf("Relay() error = %v", result.Err)
	}
	if result.FirstResponseMs != 0 {
		t.Fatalf("backend first response = %dms, want writer-owned zero outcome", result.FirstResponseMs)
	}
	if rctx.State.FirstResponse.Milliseconds() <= 0 {
		t.Fatal("client writer did not observe SSE response")
	}
	if response.Body.String() != sseBody {
		t.Fatalf("client body = %q, want %q", response.Body.String(), sseBody)
	}
}

func TestBackendNonStreamFirstResponseFallsBackToClientBody(t *testing.T) {
	const responseBody = `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, responseBody)
	}))
	defer upstream.Close()

	rctx, response := newPassthroughTestCtx(
		t,
		[]byte(`{"model":"gpt-4o","messages":[]}`),
		false,
	)
	result := (&Backend{}).Relay(rctx, state.Attempt{
		Channel: makeChannel(upstream.URL), RealModel: "gpt-4o",
	})

	if result.Err != nil {
		t.Fatalf("Relay() error = %v", result.Err)
	}
	if result.FirstResponseMs != 0 {
		t.Fatalf("backend first response = %dms, want writer-owned zero outcome", result.FirstResponseMs)
	}
	if rctx.State.FirstResponse.Milliseconds() <= 0 {
		t.Fatal("client writer did not observe JSON response")
	}
	if response.Body.String() != responseBody {
		t.Fatalf("client body = %q, want %q", response.Body.String(), responseBody)
	}
	if result.PromptTokens != 3 || result.CompletionTokens != 1 || result.ResponseText != "ok" {
		t.Fatalf("usage/text = (%d,%d,%q), want (3,1,ok)", result.PromptTokens, result.CompletionTokens, result.ResponseText)
	}
}

// TestBackend_NonStreamResponse_ExtractsUsage verifies extractPassthroughUsage parses
// non-stream JSON usage + ResponseText is filled for fallback cl100k estimation.
func TestBackend_NonStreamResponse_ExtractsUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"choices":[{"message":{"content":"answer here"}}],
			"usage":{"prompt_tokens":12,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":4}}
		}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	// OpenAI Chat prompt_tokens=12 包含 cached=4 → 归一化后 prompt=8 / cacheRead=4
	if got.PromptTokens != 8 || got.CompletionTokens != 5 || got.CacheReadTokens != 4 {
		t.Errorf("usage = (p=%d,c=%d,cr=%d), want (8,5,4)",
			got.PromptTokens, got.CompletionTokens, got.CacheReadTokens)
	}
	if got.ResponseText != "answer here" {
		t.Errorf("ResponseText = %q, want \"answer here\" (used by FinalizeTokenCounts fallback)", got.ResponseText)
	}
}

// Claude 的 cache_read_input_tokens 是和 input_tokens 分离的独立桶(input 本就不含
// 缓存),即使 input_tokens > cache_read 也绝不能从 prompt 里减掉。
// behavior change: 旧 passthrough.go:141 盲减在 input>cache 时会误减(500→200)。
func TestBackend_NonStreamResponse_ClaudeSeparateCacheNotSubtracted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","content":[{"type":"text","text":"hi"}],
			"usage":{"input_tokens":500,"output_tokens":50,"cache_read_input_tokens":300}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"claude","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "claude"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if got.PromptTokens != 500 || got.CompletionTokens != 50 || got.CacheReadTokens != 300 {
		t.Errorf("usage = (p=%d,c=%d,cr=%d), want (500,50,300) — separate cache must not be subtracted",
			got.PromptTokens, got.CompletionTokens, got.CacheReadTokens)
	}
}

// llama.cpp 的 prompt_n(非缓存)和 cache_n(命中缓存)互斥,cache_n 是独立桶,
// prompt_n>=cache_n 时也绝不能减。behavior change: 旧盲减在 1478>=500 时误减到 978。
func TestBackend_StreamResponse_LlamaTimingsCacheNotSubtracted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"finish_reason\":\"stop\",\"index\":0,\"delta\":{}}],\"object\":\"chat.completion.chunk\",\"timings\":{\"cache_n\":500,\"prompt_n\":1478,\"predicted_n\":32}}\n\n" +
				"data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"qwen","messages":[],"stream":true}`), true)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "qwen"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if got.PromptTokens != 1478 || got.CompletionTokens != 32 || got.CacheReadTokens != 500 {
		t.Errorf("usage = (p=%d,c=%d,cr=%d), want (1478,32,500) — mutually-exclusive cache must not be subtracted",
			got.PromptTokens, got.CompletionTokens, got.CacheReadTokens)
	}
}

// TestBackend_ResponseDropsContentEncoding ensures copyRespHeaders strips
// Content-Encoding / Content-Length from the upstream response. Go's Transport
// 透明解压后这两个 header 已不再代表实际响应体形态，转发给 client 会让解码失败。
func TestBackend_ResponseDropsContentEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. 发送真实 gzip 数据，Transport 解压后转发正文。
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("X-Trace-Id", "abc")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		compressed := gzip.NewWriter(w)
		_, _ = compressed.Write([]byte(`{"hello":"world"}`))
		_ = compressed.Close()
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	require.Equal(t, `{"hello":"world"}`, w.Body.String())
	// Content-Encoding 必须被剥除 — 否则客户端会按 gzip 解压一段裸 JSON，崩。
	if enc := w.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("Content-Encoding should be stripped, got %q", enc)
	}
	if cl := w.Header().Get("Content-Length"); cl == "999" {
		t.Errorf("upstream Content-Length must not leak, got %q", cl)
	}
	// 但非编码 header 应原样转发
	if x := w.Header().Get("X-Trace-Id"); x != "abc" {
		t.Errorf("non-encoding headers should propagate, X-Trace-Id = %q want \"abc\"", x)
	}
}

// TestBackend_InvalidUpstreamURL_DispatchError 让 dispatchUpstream 走失败分支：
// 把 BaseURL 改成一个明显无法连接的端口，client.Do 会返回连接错误，
// AttemptResult.Err 应包含 "passthrough upstream failed" 前缀，Written=false。
func TestBackend_InvalidUpstreamURL_DispatchError(t *testing.T) {
	// 127.0.0.1:1 几乎可以保证 connection refused
	ch := makeChannel("http://127.0.0.1:1")
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected dispatch error for unreachable upstream")
	}
	if got.Written {
		t.Errorf("dispatch failure must not mark Written=true, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "passthrough upstream failed") {
		t.Errorf("expected 'passthrough upstream failed' wrap, got %q", got.Err.Error())
	}
}

// ==================== Task 6: 4xx 错误路径覆盖（passthrough） ====================

// TestRelayPassthrough_4xxForwarded 验证 handlePassthroughErrorStatus 对 429 的处理：
// Written=false（可重试/fallback），Err 是 *common.UpstreamError，Status=429。
func TestRelayPassthrough_4xxForwarded(t *testing.T) {
	rateLimitBody := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(rateLimitBody))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err on 429")
	}
	if got.Written {
		t.Errorf("429 必须 Written=false 以便 Executor 可 fallback, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusTooManyRequests {
		t.Errorf("UpstreamError.Status = %d, want 429", upErr.Status)
	}
	if !strings.Contains(string(upErr.Body), "rate_limit") {
		t.Errorf("UpstreamError.Body 应含 rate_limit, got %q", upErr.Body)
	}
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestHandlePassthroughError_408_Timeout_GoesFallback 验证 handlePassthroughErrorStatus
// 对 408 Request Timeout 的处理：同样 Written=false，可 fallback。
func TestHandlePassthroughError_408_Timeout_GoesFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = w.Write([]byte(`{"error":{"message":"Request timeout","type":"timeout_error"}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, w := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err on 408")
	}
	if got.Written {
		t.Errorf("408 必须 Written=false 以便 Executor 可 fallback, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusRequestTimeout {
		t.Errorf("UpstreamError.Status = %d, want 408", upErr.Status)
	}
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestHandlePassthroughError_400_InvalidRequest 验证 handlePassthroughErrorStatus 对
// HTTP 400 + provider error.type=invalid_request_error 的处理：
// ProviderErrorType 应被正确解析（供 Executor 做短路决策）。
func TestHandlePassthroughError_400_InvalidRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad prompt"}}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 passthrough backend 不做写回，Written 应为 false, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error", upErr.ProviderErrorType)
	}
}

// TestPassthrough_DispatchHonorsCanceledContext verifies that dispatchUpstream
// propagates a canceled context to the upstream HTTP call, causing fast failure
// rather than hanging until a network timeout.
func TestPassthrough_DispatchHonorsCanceledContext(t *testing.T) {
	b := &Backend{Agent: nil}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req, _ := http.NewRequest(http.MethodPost, "http://10.255.255.1:9/hang", nil)
	rec := trace.NewRecorder(trace.CaptureOff, 0)

	start := time.Now()
	_, err := b.dispatchUpstream(ctx, req, &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, rec)
	if err == nil {
		t.Fatal("expected error on canceled context")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("canceled context should fail fast, took %v", time.Since(start))
	}
}

func TestPassthroughErrorBodyIsBoundedAndTraceKeepsTail(t *testing.T) {
	const physicalTail = `","tail":"passthrough-physical-tail"}}`
	body := `{"error":{"type":"invalid_request_error","message":"` + strings.Repeat("x", 96*1024) + physicalTail
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"gpt-4o","messages":[]}`), false)
	rctx.State.Recorder = trace.NewRecorder(trace.CaptureFull, 64)
	got := (&Backend{}).Relay(rctx, state.Attempt{Channel: makeChannel(upstream.URL), RealModel: "gpt-4o"})
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("err=%T %v, want UpstreamError", got.Err, got.Err)
	}
	if len(upErr.Body) >= len(body) {
		t.Fatalf("bounded body len=%d, original=%d", len(upErr.Body), len(body))
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Fatalf("provider error type=%q", upErr.ProviderErrorType)
	}
	traceRecord := rctx.State.Recorder.Finalize()
	if !strings.Contains(traceRecord.UpstreamBody, physicalTail) {
		t.Fatalf("trace tail=%q", traceRecord.UpstreamBody)
	}
}

func TestPassthroughErrorBodyReadErrorPropagates(t *testing.T) {
	wantErr := errors.New("passthrough error body failed")
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: &passthroughErrorReadCloser{err: wantErr}}
	result, handled := handlePassthroughErrorStatus(trace.NewRecorder(trace.CaptureFull, 64), resp, nil, "gpt-4o")
	if !handled || !errors.Is(result.Err, wantErr) {
		t.Fatalf("handled=%v err=%v, want read error", handled, result.Err)
	}
}

func TestPassthroughErrorBodyCancellationUnblocksRead(t *testing.T) {
	body := newPassthroughBlockingReadCloser()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	resp := &http.Response{StatusCode: http.StatusBadGateway, Body: body, Request: req}
	done := make(chan error, 1)
	go func() {
		result, _ := handlePassthroughErrorStatus(trace.NewRecorder(trace.CaptureFull, 64), resp, nil, "gpt-4o")
		done <- result.Err
	}()
	<-body.entered
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		_ = body.Close()
		t.Fatal("cancellation did not unblock passthrough error body")
	}
}

type passthroughErrorReadCloser struct{ err error }

func (r *passthroughErrorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (r *passthroughErrorReadCloser) Close() error             { return nil }

type passthroughBlockingReadCloser struct {
	entered chan struct{}
	closed  chan struct{}
}

func newPassthroughBlockingReadCloser() *passthroughBlockingReadCloser {
	return &passthroughBlockingReadCloser{entered: make(chan struct{}), closed: make(chan struct{})}
}
func (r *passthroughBlockingReadCloser) Read([]byte) (int, error) {
	select {
	case <-r.entered:
	default:
		close(r.entered)
	}
	<-r.closed
	return 0, errors.New("closed")
}
func (r *passthroughBlockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

// TestBuildPassthroughRequest_RejectsHostRewrite verifies that a malicious endpoints
// row (path containing "@evil.example" userinfo) is rejected at URL-build time and
// never reaches the network.
func TestBuildPassthroughRequest_RejectsHostRewrite(t *testing.T) {
	ch := &models.Channel{}
	ch.BaseURL = "https://api.openai.com"
	ch.ChannelCore.Endpoints = `{"chat_completions":"@evil.example/v1/chat/completions"}`
	req, _ := http.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	_, err := buildPassthroughRequest(req, ch, llmkit.ProtocolOpenAIChat, []byte(`{}`))
	if err == nil {
		t.Fatal("want host-mismatch error, got nil")
	}
}

// TestBackend_ForwardsUAAndStripsClientCredentials 验证诚实透传:
// 客户端 UA 透传到上游;客户端 x-api-key 被剥离(凭证泄漏修复);
// 上游 Authorization 用渠道 key。
func TestBackend_ForwardsUAAndStripsClientCredentials(t *testing.T) {
	var gotUA, gotXAPIKey, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotXAPIKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"gpt-4o","messages":[]}`), false)
	rctx.Context.Request.Header.Set("User-Agent", "claude-cli/1.0")
	rctx.Context.Request.Header.Set("x-api-key", "client-leak")
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotUA != "claude-cli/1.0" {
		t.Errorf("upstream User-Agent = %q, want forwarded client UA", gotUA)
	}
	if gotXAPIKey != "" {
		t.Errorf("upstream x-api-key = %q, want stripped (credential leak)", gotXAPIKey)
	}
	if gotAuth != "Bearer k" {
		t.Errorf("upstream Authorization = %q, want channel key", gotAuth)
	}
}

// TestHandlePassthroughError_400_NoProviderType 验证 handlePassthroughErrorStatus 对
// HTTP 400 但 body 不含 error.type 的处理：ProviderErrorType 应为空字符串。
func TestHandlePassthroughError_400_NoProviderType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer upstream.Close()

	ch := makeChannel(upstream.URL)
	rctx, _ := newPassthroughTestCtx(t,
		[]byte(`{"model":"gpt-4o","messages":[]}`), false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4o"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 passthrough backend 不做写回，Written 应为 false, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "" {
		t.Errorf("ProviderErrorType 应为空(无 error.type 字段), got %q", upErr.ProviderErrorType)
	}
}

func TestBackendChannelAuth(t *testing.T) {
	tests := []struct {
		name      string
		auth      models.ChannelAuth
		key       string
		override  string
		script    string
		wantAuth  string
		wantToken string
		wantCode  string
	}{
		{name: "unconfigured keeps bearer", key: "selected-key", wantAuth: "Bearer selected-key"},
		{name: "custom header", key: "selected-key", auth: models.ChannelAuth{Default: &models.AuthType{Header: "X-Custom-Token", Value: "Token ${CHANNEL_KEY}"}}, wantToken: "Token selected-key"},
		{name: "protocol pair overrides default", key: "selected-key", auth: models.ChannelAuth{Default: &models.AuthType{Header: "Authorization", Value: "Bearer ${CHANNEL_KEY}"}, Protocols: map[string]models.AuthType{"chat_completions": {Header: "X-Custom-Token", Value: "literal-secret"}}}, wantToken: "literal-secret"},
		{name: "ordinary override wins", auth: models.ChannelAuth{Default: &models.AuthType{Header: "X-Custom-Token", Value: "literal-secret"}}, override: `{"X-Custom-Token":"override-secret"}`, wantToken: "override-secret"},
		{name: "script wins", auth: models.ChannelAuth{Default: &models.AuthType{Header: "X-Custom-Token", Value: "literal-secret"}}, override: `{"X-Custom-Token":"override-secret"}`, script: `function onUpstreamRequest(ctx) { ctx.setHeader("X-Custom-Token", "script-secret"); }`, wantToken: "script-secret"},
		{name: "script removes auth", auth: models.ChannelAuth{Default: &models.AuthType{Header: "X-Custom-Token", Value: "literal-secret"}}, script: `function onUpstreamRequest(ctx) { ctx.removeHeader("X-Custom-Token"); }`},
		{name: "missing key stops dispatch", auth: models.ChannelAuth{Default: &models.AuthType{Header: "X-Custom-Token", Value: "${CHANNEL_KEY}"}}, wantCode: "upstream_auth_key_missing"},
		{name: "invalid persistence stops dispatch", auth: models.ChannelAuth{Default: &models.AuthType{Header: "Content-Type", Value: "secret-value"}}, wantCode: "invalid_upstream_auth"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var requests atomic.Int32
			var gotHeader http.Header
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				gotHeader = request.Header.Clone()
				_, _ = io.WriteString(writer, `{}`)
			}))
			t.Cleanup(server.Close)
			channel := makeChannel(server.URL)
			channel.Key = test.key
			channel.Auth = datatypes.NewJSONType(test.auth)
			channel.HeaderOverride = test.override
			rctx, _ := newPassthroughTestCtx(t, []byte(`{"model":"gpt-4o","messages":[]}`), false)
			rctx.Context.Request.Header.Set("Authorization", "Bearer gateway-client-secret")
			rctx.Context.Request.Header.Set("X-Api-Key", "gateway-client-secret")
			if test.script != "" {
				rctx.Agent = newScriptTestAgent(t, test.script)
			}
			result := (&Backend{Agent: rctx.Agent}).Relay(rctx, state.Attempt{Channel: channel, RealModel: "gpt-4o"})
			if test.wantCode != "" {
				var authErr *upstream.ChannelAuthError
				require.ErrorAs(t, result.Err, &authErr)
				require.Equal(t, test.wantCode, authErr.Code)
				require.Zero(t, requests.Load())
				require.Equal(t, trace.StageUpstreamDispatch, rctx.State.Recorder.Finalize().FailStage)
				return
			}
			require.NoError(t, result.Err)
			require.EqualValues(t, 1, requests.Load())
			require.Equal(t, test.wantAuth, gotHeader.Get("Authorization"))
			require.Equal(t, test.wantToken, gotHeader.Get("X-Custom-Token"))
			require.Empty(t, gotHeader.Get("X-Api-Key"))
		})
	}
}

func TestGeminiNativePassthroughPreservesBodyQueryAndLargeUsage(t *testing.T) {
	for _, tc := range []struct {
		name, path, responseType string
		stream                   bool
	}{
		{"json", "/v1beta/models/client:generateContent?key=client-secret&custom=value", "application/json", false},
		{"sse", "/v1/models/client:streamGenerateContent?alt=sse&key=client-secret&custom=value", "text/event-stream", true},
		{"array", "/v1beta/models/client:streamGenerateContent?key=client-secret&custom=value", "application/json", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := []byte(" {\"contents\":[],\"unknown\":true} \n")
			result := `{"usageMetadata":{"promptTokenCount":100,"cachedContentTokenCount":40,"candidatesTokenCount":10,"thoughtsTokenCount":2},"candidates":[{"content":{"parts":[{"inlineData":{"mimeType":"image/png","data":"` + strings.Repeat("A", 2*1024*1024) + `"}}]},"finishReason":"STOP"}]}`
			switch tc.name {
			case "sse":
				result = "data: " + result + "\n\n"
			case "array":
				result = "[" + result + "]"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Equal(t, body, got)
				require.Contains(t, r.URL.Path, "/models/mapped:")
				require.Equal(t, "value", r.URL.Query().Get("custom"))
				require.Empty(t, r.URL.Query().Get("key"))
				if tc.name == "sse" {
					require.Equal(t, "sse", r.URL.Query().Get("alt"))
				} else {
					require.Empty(t, r.URL.Query().Get("alt"))
				}
				require.Equal(t, "channel-key", r.Header.Get("x-goog-api-key"))
				require.Empty(t, r.Header.Get("Authorization"))
				require.Empty(t, r.Header.Get("x-api-key"))
				w.Header().Set("Content-Type", tc.responseType)
				_, _ = io.WriteString(w, result)
			}))
			defer srv.Close()
			ch := makeChannel(srv.URL)
			ch.Type = consts.ChannelTypeGemini
			ch.Key = "channel-key"
			ch.ModelMapping = `{"client":"mapped"}`
			ch.Endpoints = `{"generate_content":"/v1beta/models/{model}:{action}"}`
			relay, out := newPassthroughTestCtx(t, body, tc.stream)
			relay.Input.InboundProto = llmkit.ProtocolGemini
			relay.Input.Model = "client"
			relay.Context.Request = httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(string(body)))
			relay.Context.Request.Header.Set("Authorization", "Bearer client-secret")
			relay.Context.Request.Header.Set("x-goog-api-key", "other-client-secret")
			got := (&Backend{}).Relay(relay, state.Attempt{Channel: ch, RealModel: "client"})
			require.NoError(t, got.Err)
			require.Equal(t, result, out.Body.String())
			require.Equal(t, 60, got.PromptTokens)
			require.Equal(t, 12, got.CompletionTokens)
			require.Equal(t, 40, got.CacheReadTokens)
			require.Less(t, len(relay.State.Recorder.UpstreamBodyBytes()), len(result))
		})
	}
}

func TestGeminiNativePassthroughDoesNotValidateJSON(t *testing.T) {
	body := []byte("not a JSON request")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := io.ReadAll(r.Body)
		require.Equal(t, body, got)
		w.WriteHeader(400)
		_, _ = io.WriteString(w, `{"error":{"message":"invalid JSON"}}`)
	}))
	defer srv.Close()
	ch := makeChannel(srv.URL)
	ch.Type = consts.ChannelTypeGemini
	ch.Endpoints = `{"generate_content":"/v1beta/models/{model}:{action}"}`
	relay, _ := newPassthroughTestCtx(t, body, false)
	relay.Input.InboundProto = llmkit.ProtocolGemini
	relay.Context.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/model:generateContent", strings.NewReader(string(body)))
	got := (&Backend{}).Relay(relay, state.Attempt{Channel: ch, RealModel: "model"})
	require.Error(t, got.Err)
	var upstreamErr *common.UpstreamError
	require.ErrorAs(t, got.Err, &upstreamErr)
	require.Equal(t, 400, upstreamErr.Status)
}

func TestGeminiNativePassthroughReportsInterruptedResponse(t *testing.T) {
	for _, response := range []string{
		`data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}` + "\n\n",
		`data: {"error":{"code":429,"message":"upstream refused"}}` + "\n\n",
		`{"candidates":[{"content":{"parts":[{"text":"unfinished`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = io.WriteString(w, response)
		}))
		ch := makeChannel(srv.URL)
		ch.Type = consts.ChannelTypeGemini
		relay, out := newPassthroughTestCtx(t, []byte(`{}`), true)
		relay.Input.InboundProto = llmkit.ProtocolGemini
		relay.Context.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent?alt=sse", strings.NewReader(`{}`))
		got := (&Backend{}).Relay(relay, state.Attempt{Channel: ch, RealModel: "gemini"})
		require.Error(t, got.Err)
		require.True(t, got.Written)
		require.Equal(t, response, out.Body.String())
		srv.Close()
	}
}

func TestGeminiNativePassthroughCancellation(t *testing.T) {
	started := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}]}`+"\n\n")
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer srv.Close()
	ch := makeChannel(srv.URL)
	ch.Type = consts.ChannelTypeGemini
	relay, _ := newPassthroughTestCtx(t, []byte(`{}`), true)
	relay.Input.InboundProto = llmkit.ProtocolGemini
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	relay.Context.Request = httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini:streamGenerateContent?alt=sse", strings.NewReader(`{}`)).WithContext(ctx)
	done := make(chan state.AttemptResult, 1)
	go func() { done <- (&Backend{}).Relay(relay, state.Attempt{Channel: ch, RealModel: "gemini"}) }()
	<-started
	cancel()
	select {
	case got := <-done:
		require.ErrorIs(t, got.Err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("request did not cancel")
	}
}
