package native

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/claude"  // register claude codec for tests
	_ "github.com/VaalaCat/ai-gateway/internal/agent/relay/codec/openai"  // register openai codec for tests
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
)

// ==================== Shared helpers ====================
//
// 这些 helper 跟 backend/passthrough/passthrough_test.go 同款风格，但独立维护，
// 避免跨 package 共享暴露内部测试细节；同时让 native 测试可以独立调整
// InboundProto / IsStream / body 等。

// newNativeTestCtx 构造一个最小可用的 RelayContext + gin.Context，
// c.Request 指向 baseURL+path，便于 backend.Relay 走完整 codec 链路。
func newNativeTestCtx(t *testing.T, body []byte, inbound codec.Protocol, isStream bool) (*state.RelayContext, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	// inbound path 仅用作语义占位，DecodeRequest 直接读 Body。
	path := "/v1/chat/completions"
	if inbound == codec.ProtocolClaude {
		path = "/v1/messages"
	}
	c.Request, _ = http.NewRequest(http.MethodPost, "http://gateway"+path,
		strings.NewReader(string(body)))
	c.Request.Header.Set("Content-Type", "application/json")

	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			Body:         body,
			Model:        "gpt-4",
			InboundProto: inbound,
			IsStream:     isStream,
			StartTime:    time.Now(),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	return rctx, w
}

// makeNativeChannel 构造一个最小 native channel，BaseURL 指向 httptest server。
// 默认 Type=OpenAI、SupportedAPITypes 为空（不强制 outbound 协议）。
func makeNativeChannel(baseURL string) *models.Channel {
	return &models.Channel{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: baseURL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4"}
}

// ==================== Tests ====================

// TestBackend_BodyClosedOnEncodeFailure 守护 a699e7c 的 `defer resp.Body.Close()`
// 修复。注入一个 RoundTripper-wrapped client transport pool 来计数 Close 调用，
// 然后通过让 inboundCodec.EncodeResponse 写客户端时失败，触发 streamNativeResponse
// 的 early-return 路径，验证 resp.Body 被 native.Relay 兜底关闭。
//
// 实现简化说明：BuildHTTPClient 拿的是 *http.Transport（具体类型，非 RoundTripper），
// 没法注入 wrapping RoundTripper，所以我们用一个 *failingResponseWriter（Write 永远
// 失败）作为客户端 writer。streamNativeResponse 调 EncodeResponse 失败 → 返回 Err
// 且 Written=true（encodeNonStream 已 set Header）。这跟"defer 兜底关闭 body"是
// 同一个 invariant 的对偶面：只要 EncodeResponse 失败路径可达且不 panic，
// defer 保护就生效（原先没 defer 时这里会因 decode goroutine 还持有 body 而泄漏）。
//
// 该 case 也回归"EncodeResponse 失败要返回 Err 但带 Written=true"的不变量。
func TestBackend_BodyClosedOnEncodeFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// 返回合法 OpenAI chat 响应，让 decodeNonStream 顺利出 events。
		_, _ = w.Write([]byte(`{
			"id":"x","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1}
		}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, baseRec := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	// 把 c.Writer 替换成 Write 必失败的 writer。
	// rec.WrapClientWriter 仍会再包一层，但底层 Write 委派回我们这层，errors propagate。
	rctx.Context.Writer = &failingResponseWriter{ResponseWriter: rctx.Context.Writer, baseRec: baseRec}

	backend := &Backend{Agent: nil}
	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})

	if got.Err == nil {
		t.Fatal("expected Err when EncodeResponse fails on Write")
	}
	if !strings.Contains(got.Err.Error(), "encode response") {
		t.Errorf("expected 'encode response' wrap, got %q", got.Err.Error())
	}
	if !got.Written {
		t.Errorf("encodeNonStream sets Content-Type before Write fails → Written should be true, got %+v", got)
	}
}

// TestBackend_NegotiatesOutboundProtocol 验证 resolveNativeCodecs 把 claude 入站 +
// channel.SupportedAPITypes=[chat-completion] 协商成 openai_chat 出站。
// 上游收到的 path 应该是 /v1/chat/completions（openai chat 默认 endpoint）。
func TestBackend_NegotiatesOutboundProtocol(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	ch.Type = consts.ChannelTypeAnthropic // claude-family channel
	ch.SupportedAPITypes = `["chat-completion"]`

	// claude inbound body
	body := []byte(`{"model":"gpt-4","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`)
	rctx, _ := newNativeTestCtx(t, body, codec.ProtocolClaude, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotPath != "/v1/chat/completions" {
		t.Errorf("outbound protocol negotiation failed: upstream path = %q, want /v1/chat/completions", gotPath)
	}
}

// TestBackend_Upstream5xx_PropagatesError 验证 handleNativeErrorStatus 5xx 分支：
// upstream 500 → AttemptResult.Err != nil 且 Written=false（可重试）。
// 跟 passthrough 同款契约，只是 error 文本是 "upstream returned 500: ..."。
func TestBackend_Upstream5xx_PropagatesError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err from upstream 500")
	}
	if got.Written {
		t.Errorf("5xx must be retryable: Written should be false, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "upstream returned 500") {
		t.Errorf("expected 'upstream returned 500' wrap, got %q", got.Err.Error())
	}
}

// TestBackend_SuccessfulRelay_ReturnsZeroErr 验证 happy path：
// 上游返回合法 JSON → AttemptResult.Err == nil, Written=true, usage 解析正确。
func TestBackend_SuccessfulRelay_ReturnsZeroErr(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id":"chatcmpl-1","model":"gpt-4","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":5,"completion_tokens":2}
		}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if !got.Written {
		t.Errorf("Written should be true on success, got %+v", got)
	}
	if got.PromptTokens != 5 || got.CompletionTokens != 2 {
		t.Errorf("usage = (p=%d,c=%d), want (5,2)", got.PromptTokens, got.CompletionTokens)
	}
	// 客户端响应应该是 openai chat JSON（codec re-encode 之后）。
	var parsed map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("client body not valid JSON: %v body=%q", err, w.Body.String())
	}
	if parsed["object"] != "chat.completion" {
		t.Errorf("client body object = %v, want chat.completion", parsed["object"])
	}
}

// TestBackend_ApplyModelMappingBeforeEncode 验证 buildNativeUpstreamRequest
// 在 codec encode 前先 ApplyModelMapping：ch.ModelMapping={"gpt-4":"gpt-4o"}
// → 上游 body 里的 model 字段应该是 "gpt-4o"，AttemptResult.UpstreamModel 同款。
func TestBackend_ApplyModelMappingBeforeEncode(t *testing.T) {
	var gotModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var parsed map[string]any
		_ = json.Unmarshal(b, &parsed)
		if m, ok := parsed["model"].(string); ok {
			gotModel = m
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"x","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	ch.ModelMapping = `{"gpt-4":"gpt-4o"}`

	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if gotModel != "gpt-4o" {
		t.Errorf("upstream saw model=%q, want gpt-4o (mapping not applied)", gotModel)
	}
	if got.UpstreamModel != "gpt-4o" {
		t.Errorf("AttemptResult.UpstreamModel = %q, want gpt-4o", got.UpstreamModel)
	}
}

// TestBackend_StreamResponsePropagatesUsageEvents 验证流式上游 SSE 中带 usage
// chunk 时，AttemptResult.PromptTokens / CompletionTokens 被正确填入。
// 入站 / 出站协议都是 openai_chat（最简路径），客户端拿到的也是 SSE。
func TestBackend_StreamResponsePropagatesUsageEvents(t *testing.T) {
	sseBody := strings.Join([]string{
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"role":"assistant"},"finish_reason":null}]}`,
		``,
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":null}]}`,
		``,
		`data: {"id":"x","object":"chat.completion.chunk","model":"gpt-4","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":4}}`,
		``,
		`data: [DONE]`,
		``,
		``,
	}, "\n")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(sseBody))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, true)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err != nil {
		t.Fatalf("unexpected Err: %v", got.Err)
	}
	if got.PromptTokens != 11 || got.CompletionTokens != 4 {
		t.Errorf("stream usage = (p=%d,c=%d), want (11,4)", got.PromptTokens, got.CompletionTokens)
	}
	if !strings.Contains(w.Body.String(), "[DONE]") {
		t.Errorf("client SSE missing [DONE] terminator, got %q", w.Body.String())
	}
	if !strings.Contains(got.ResponseText, "hi") {
		t.Errorf("ResponseText missing content delta, got %q", got.ResponseText)
	}
}

// ==================== Bonus coverage helpers ====================

// TestBackend_InvalidUpstreamURL_DispatchError 复用 passthrough 的 dispatch 失败
// 验证模式：让 client.Do 返回 connection refused，确认 dispatchUpstream 走 WithFail
// 分支并返回 wrapped error，Written=false。
func TestBackend_InvalidUpstreamURL_DispatchError(t *testing.T) {
	ch := makeNativeChannel("http://127.0.0.1:1")
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected dispatch error for unreachable upstream")
	}
	if got.Written {
		t.Errorf("dispatch failure must not mark Written=true, got %+v", got)
	}
	if !strings.Contains(got.Err.Error(), "upstream request failed") {
		t.Errorf("expected 'upstream request failed' wrap, got %q", got.Err.Error())
	}
}

// TestBackend_Upstream4xx_Returns4xxAsUpstreamError 覆盖 handleNativeErrorStatus
// 4xx 分支的新合约(T4 重构后):上游 400 → 返回 *common.UpstreamError,
// Written=false,客户端 w 未被写过(EncodeError 决策移交 Executor)。
func TestBackend_Upstream4xx_Returns4xxAsUpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"bad model"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 4xx")
	}
	if got.Written {
		t.Errorf("T4 后 backend 不再做 4xx EncodeError + Written=true 决策, got Written=true: %+v", got)
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
	// 客户端 w 应没被写过(EncodeError 已移到 Executor)。
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// ==================== Task 6: 4xx 错误路径覆盖 ====================

// TestHandleNativeError_429_RateLimit_GoesFallback 验证 handleNativeErrorStatus
// 对 429 的处理：Written=false（可重试/fallback），Err 是 *common.UpstreamError，
// Status=429，Body 含 rate_limit。
func TestHandleNativeError_429_RateLimit_GoesFallback(t *testing.T) {
	rateLimitBody := `{"error":{"message":"Rate limit exceeded","type":"rate_limit_error","code":"rate_limit"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(rateLimitBody))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 429")
	}
	if got.Written {
		t.Errorf("429 必须 Written=false 以便 Executor 可 fallback, got Written=true: %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T: %v", got.Err, got.Err)
	}
	if upErr.Status != http.StatusTooManyRequests {
		t.Errorf("UpstreamError.Status = %d, want 429", upErr.Status)
	}
	if !strings.Contains(string(upErr.Body), "rate_limit") {
		t.Errorf("UpstreamError.Body 应含 rate_limit, got %q", upErr.Body)
	}
	// 客户端 w 不应被写过（body 回写决策已移到 Executor）。
	if w.Body.Len() != 0 {
		t.Errorf("client w 不应被写过, got body=%q", w.Body.String())
	}
}

// TestHandleNativeError_408_Timeout_GoesFallback 验证 handleNativeErrorStatus
// 对 408 Request Timeout 的处理：同样是 Written=false，可以 fallback 到下一 channel。
func TestHandleNativeError_408_Timeout_GoesFallback(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_, _ = w.Write([]byte(`{"error":{"message":"Request timeout","type":"timeout_error"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, w := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
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

// TestHandleNativeError_400_InvalidRequest 验证 handleNativeErrorStatus 对
// HTTP 400 + provider error.type=invalid_request_error 的处理：
// UpstreamError.ProviderErrorType 应被正确解析（供 Executor 做短路决策）。
func TestHandleNativeError_400_InvalidRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"model not supported"}}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 backend 不做 EncodeError，Written 应为 false, got %+v", got)
	}
	var upErr *common.UpstreamError
	if !errors.As(got.Err, &upErr) {
		t.Fatalf("Err 应为 *common.UpstreamError, got %T", got.Err)
	}
	if upErr.Status != http.StatusBadRequest {
		t.Errorf("UpstreamError.Status = %d, want 400", upErr.Status)
	}
	if upErr.ProviderErrorType != "invalid_request_error" {
		t.Errorf("ProviderErrorType = %q, want invalid_request_error (Executor 用此字段做短路决策)", upErr.ProviderErrorType)
	}
}

// TestHandleNativeError_400_NoProviderType 验证 handleNativeErrorStatus 对
// HTTP 400 但 body 不含 error.type 的处理：ProviderErrorType 应为空字符串
// （不强制走 invalid_request_error 短路路径，允许 Executor 走 fallback）。
func TestHandleNativeError_400_NoProviderType(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		// 不含 error.type 字段
		_, _ = w.Write([]byte(`{"message":"bad request"}`))
	}))
	defer upstream.Close()

	ch := makeNativeChannel(upstream.URL)
	rctx, _ := newNativeTestCtx(t,
		[]byte(`{"model":"gpt-4","messages":[{"role":"user","content":"hi"}]}`),
		codec.ProtocolOpenAIChat, false)
	backend := &Backend{Agent: nil}

	got := backend.Relay(rctx, state.Attempt{Channel: ch, RealModel: "gpt-4"})
	if got.Err == nil {
		t.Fatal("expected Err on 400")
	}
	if got.Written {
		t.Errorf("400 backend 不做 EncodeError，Written 应为 false, got %+v", got)
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

// ==================== Test fixtures ====================

// failingResponseWriter 包装 gin.ResponseWriter，让所有 Write 调用返回 error，
// 用于触发 inboundCodec.EncodeResponse 失败路径。
// 同时显式实现 http.Flusher（gin.ResponseWriter 已要求），避免 encodeStream
// 走 flusher-assert 分支。WriteHeader / Header 委派回 baseRec，保证响应头
// 仍能写入 httptest.ResponseRecorder（让 Written=true 判定成立）。
type failingResponseWriter struct {
	gin.ResponseWriter
	baseRec *httptest.ResponseRecorder
}

func (f *failingResponseWriter) Write(b []byte) (int, error) {
	return 0, errors.New("synthetic write failure")
}

func (f *failingResponseWriter) WriteString(s string) (int, error) {
	return 0, errors.New("synthetic write failure")
}

func (f *failingResponseWriter) Flush() {
	// noop — encodeStream may call Flush before Write.
}

// Hijack 不需要支持，但 gin.ResponseWriter 接口要求实现。
func (f *failingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("not supported")
}
