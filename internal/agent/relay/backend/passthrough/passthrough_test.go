package passthrough

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
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

	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			Body:         body,
			Model:        "gpt-4o",
			InboundProto: codec.ProtocolOpenAIChat,
			IsStream:     isStream,
			StartTime:    time.Now(),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	return rctx, w
}

// makeChannel 构造一个最小 passthrough channel，BaseURL 指向 httptest server。
func makeChannel(baseURL string) *models.Channel {
	return &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: baseURL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"}
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

// TestBackend_ResponseDropsContentEncoding ensures copyRespHeaders strips
// Content-Encoding / Content-Length from the upstream response. Go's Transport
// 透明解压后这两个 header 已不再代表实际响应体形态，转发给 client 会让解码失败。
func TestBackend_ResponseDropsContentEncoding(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Manually claim gzip even though body is raw — emulates a Transport-decompressed
		// passthrough where upstream Content-Encoding header survives if not filtered.
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", "999")
		w.Header().Set("X-Trace-Id", "abc")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"hello":"world"}`))
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
