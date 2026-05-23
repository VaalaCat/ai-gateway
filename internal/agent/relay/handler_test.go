package relay

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	relayupstream "github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// setupTestHandler 是测试用的 Handler 工厂，对应生产链路里 server.go.buildRelayHandler。
// retryMax=3 / timeout=30s / pool=(100,10) 对齐 server.go.buildRelayHandler 的默认值。
func setupTestHandler(channels []*models.Channel) (*Handler, *cache.Store, app.EventBus) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	for _, ch := range channels {
		store.SetChannel(ch)
	}
	store.RebuildModelIndex()

	bus := eventbus.NewMemoryBus()
	logger, _ := zap.NewDevelopment()
	pool := relayupstream.NewTransportPool(100, 10, 30*time.Second)
	cfg := &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: 3},
		Relay:   config.RelayConfig{Timeout: 30},
	}
	agentApp := agentappkg.NewDefaultAgentApplication(store, nil, logger, cfg, pool)
	handler := NewHandler(bus, agentApp, TestDispatcherFactory(agentApp))
	return handler, store, bus
}

func setupRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})
		handler.Relay(c)
	})
	return r
}

func TestRelayHandler_Success(t *testing.T) {
	// Mock upstream that behaves like OpenAI API
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-123",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "Hello!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})

	usageReceived := make(chan bool, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, _ protocol.UsageLogEntry) error {
		usageReceived <- true
		return nil
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	select {
	case <-usageReceived:
	case <-time.After(2 * time.Second):
		t.Error("usage event not published")
	}
}

func TestRelayHandler_RetryOn5xx(t *testing.T) {
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"internal"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-retry",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, Priority: 1}, Key: "k1", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, Priority: 1}, Key: "k2", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 after retry, got %d: %s", w.Code, w.Body.String())
	}
}

func TestRelayHandler_NoModel(t *testing.T) {
	handler, _, _ := setupTestHandler(nil)
	r := setupRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Errorf("expected 400 for missing model, got %d", w.Code)
	}
}

func TestRelayHandler_NoChannels(t *testing.T) {
	handler, _, _ := setupTestHandler(nil)
	r := setupRouter(handler)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"nonexistent"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Errorf("expected 404 for no channels, got %d", w.Code)
	}
}

func TestApplyModelMapping(t *testing.T) {
	ch := &models.Channel{ModelMapping: `{"gpt-4":"gpt-4-turbo"}`}
	result := state.ApplyModelMapping(ch, "gpt-4")
	if result != "gpt-4-turbo" {
		t.Errorf("got %s, want gpt-4-turbo", result)
	}

	result = state.ApplyModelMapping(ch, "gpt-3.5")
	if result != "gpt-3.5" {
		t.Errorf("got %s, want gpt-3.5", result)
	}

	ch2 := &models.Channel{}
	result = state.ApplyModelMapping(ch2, "gpt-4")
	if result != "gpt-4" {
		t.Errorf("got %s, want gpt-4", result)
	}
}

func setupResponsesRouter(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/responses", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1})
		handler.Relay(c)
	})
	return r
}

func TestRelayHandler_ResponsesEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"resp_123","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hi!"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`))
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, SupportedAPITypes: `["responses"]`}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupResponsesRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hello","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "Hi!") {
		t.Errorf("expected response to contain 'Hi!', got: %s", body)
	}
}

func TestRelayHandler_CrossProtocol_ChatToResponses(t *testing.T) {
	// Mock upstream returning Responses API format.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"resp_cross","object":"response","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"converted!"}]}],"usage":{"input_tokens":5,"output_tokens":3,"total_tokens":8}}`))
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, SupportedAPITypes: `["responses"]`}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Response should be valid Chat Completions format.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := resp["choices"]; !ok {
		t.Errorf("expected Chat Completions format with 'choices' field, got: %s", w.Body.String())
	}
}

func TestRelayHandler_CrossProtocol_ResponsesToChat(t *testing.T) {
	// Mock upstream returning Chat Completions format.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-cross",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "converted!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 3, "total_tokens": 8},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, SupportedAPITypes: `["chat-completion"]`}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupResponsesRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/responses", strings.NewReader(`{"model":"gpt-4o","input":"hello","stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Response should be valid Responses API format.
	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v", err)
	}
	if _, ok := resp["output"]; !ok {
		t.Errorf("expected Responses API format with 'output' field, got: %s", w.Body.String())
	}
}

func TestRelayHandler_StreamingRelay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		lines := []string{
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n",
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n",
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n",
			"data: [DONE]\n\n",
		}
		for _, line := range lines {
			w.Write([]byte(line))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	body := w.Body.String()
	if !strings.Contains(body, "data:") {
		t.Errorf("expected SSE data: lines, got: %s", body)
	}
	if !strings.Contains(body, "[DONE]") {
		t.Errorf("expected [DONE] in streaming response, got: %s", body)
	}
}

func TestRelayHandler_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`))
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	// Path B 后:上游 429 应原样透传给客户端(status + body),不再被 StatusFromState
	// 泛化成 502。tighten 自原"!= 200"弱断言。
	if w.Code != 429 {
		t.Fatalf("expected 429 (上游原样透传), got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "rate_limit_error") {
		t.Errorf("expected client body 含上游 rate_limit_error 文案, got %q", w.Body.String())
	}
}

// TestRelay_4xxForwards_CustomHeader 验证上游 4xx 响应的自定义 header 通过
// UpstreamError.Header + handler.writeResponse 路径正确透传到客户端 Response。
// 同时锁定 Content-Encoding 必须被 strip(避免客户端按 gzip 解未压缩 body 崩)。
//
// 配合 T6 引入的 UpstreamError.Header 字段:backend 用 resp.Header.Clone() 填充,
// writeResponse 转发(跳过 Content-Encoding / Content-Length)。
// 走 native 路径(channel 不开 PassthroughEnabled),确保非 passthrough 模式也走通。
func TestRelay_4xxForwards_CustomHeader(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Limit", "100")
		w.Header().Set("X-Custom", "forwarded")
		w.Header().Set("Retry-After", "20")
		// 验证 Content-Encoding 被 strip——不 strip 的话客户端会按 gzip 解一段裸 JSON,崩。
		w.Header().Set("Content-Encoding", "should-be-stripped")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limit exceeded","type":"rate_limit_exceeded"}}`))
	}))
	defer upstream.Close()

	// 单 channel + 无 fallback,确保终态走到 writeResponse 的 UpstreamError 分支。
	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("X-RateLimit-Limit"); got != "100" {
		t.Errorf("X-RateLimit-Limit = %q, want %q (Header.Clone → writeResponse 透传)", got, "100")
	}
	if got := w.Header().Get("X-Custom"); got != "forwarded" {
		t.Errorf("X-Custom = %q, want %q", got, "forwarded")
	}
	if got := w.Header().Get("Retry-After"); got != "20" {
		t.Errorf("Retry-After = %q, want %q (rate limit 关键 hint header)", got, "20")
	}
	// Content-Encoding 必须被 strip — 否则客户端按 gzip 解一段裸 JSON 崩。
	if got := w.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding 应被 strip, got %q", got)
	}
}

// usageLogCollector 把 SubscribeUsageCompleted 收到的日志攒到 slice，
// 内部用 mu 保护 append 和读取。订阅回调在 eventbus.MemoryBus 的发布 goroutine
// 里执行（异步），测试主 goroutine 在 ServeHTTP 返回后读 — 没有 mu 时 -race 会爆。
type usageLogCollector struct {
	mu   sync.Mutex
	logs []protocol.UsageLogEntry
}

// Len 返回当前已收日志条数，加锁读快照长度。
func (c *usageLogCollector) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.logs)
}

// Get 返回 index 处的日志副本，越界返回零值。加锁读保证可见性。
func (c *usageLogCollector) Get(i int) protocol.UsageLogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if i < 0 || i >= len(c.logs) {
		return protocol.UsageLogEntry{}
	}
	return c.logs[i]
}

// Snapshot 返回 logs 的 shallow copy，供调用方做整体扫描（如 range / filter）。
// 加锁读保证可见性并防止 range 期间被并发 append 改了底层数组。
func (c *usageLogCollector) Snapshot() []protocol.UsageLogEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]protocol.UsageLogEntry, len(c.logs))
	copy(out, c.logs)
	return out
}

// collectUsageLogs subscribes to usage events and collects them into a thread-safe slice.
func collectUsageLogs(bus app.EventBus) *usageLogCollector {
	c := &usageLogCollector{}
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		c.mu.Lock()
		c.logs = append(c.logs, entry)
		c.mu.Unlock()
		return nil
	})
	return c
}

func TestRelayHandler_4xxErrorLogsFailure(t *testing.T) {
	errBody := `{"error":{"message":"rate limit exceeded","type":"rate_limit_error"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(429)
		w.Write([]byte(errBody))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})
	usageLogs := collectUsageLogs(bus)

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if usageLogs.Len() == 0 {
		t.Fatal("expected usage log for 4xx error, got none")
	}
	log := usageLogs.Get(0)
	if log.Status != 0 {
		t.Errorf("expected Status=0 for 4xx error, got %d", log.Status)
	}
	if log.ErrorMessage == "" {
		t.Error("expected ErrorMessage to be set for 4xx error")
	}
	if !strings.Contains(log.ErrorMessage, "rate limit") {
		t.Errorf("expected ErrorMessage to contain upstream error body, got: %s", log.ErrorMessage)
	}
}

func TestRelayHandler_5xxErrorLogsFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})
	usageLogs := collectUsageLogs(bus)

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if usageLogs.Len() == 0 {
		t.Fatal("expected usage log for 5xx error, got none")
	}
	log := usageLogs.Get(0)
	if log.Status != 0 {
		t.Errorf("expected Status=0 for 5xx error, got %d", log.Status)
	}
	if log.ErrorMessage == "" {
		t.Error("expected ErrorMessage to be set for 5xx error")
	}
}

func TestRelayHandler_SuccessLogsCorrectly(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-ok", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "Hello!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})
	usageLogs := collectUsageLogs(bus)

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if usageLogs.Len() == 0 {
		t.Fatal("expected usage log for success, got none")
	}
	log := usageLogs.Get(0)
	if log.Status != 1 {
		t.Errorf("expected Status=1 for success, got %d", log.Status)
	}
	if log.ErrorMessage != "" {
		t.Errorf("expected no ErrorMessage for success, got: %s", log.ErrorMessage)
	}
	if log.PromptTokens != 10 || log.CompletionTokens != 5 {
		t.Errorf("expected tokens 10/5, got %d/%d", log.PromptTokens, log.CompletionTokens)
	}
}

func TestRelayHandler_TokenSource_Provider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-123",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "Hello!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})
	usageLogs := collectUsageLogs(bus)

	r := setupRouter(handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if usageLogs.Len() == 0 {
		t.Fatal("expected usage log")
	}
	log := usageLogs.Get(0)
	if log.TokenSource != string(relayupstream.TokenSourceProvider) && log.TokenSource != string(relayupstream.TokenSourceProviderDivergent) {
		t.Errorf("expected provider or provider_divergent source, got %q", log.TokenSource)
	}
	if log.PromptTokens != 10 {
		t.Errorf("expected prompt_tokens=10, got %d", log.PromptTokens)
	}
}

// setupRouterWithUserInfo extends setupRouter to inject custom UserInfo
// for whitelist / X-Channel-ID behavior tests.
func setupRouterWithUserInfo(handler *Handler, ui *app.UserInfo) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, ui)
		handler.Relay(c)
	})
	return r
}

// upstreamReturning200 is a helper that returns 200 for any chat completion request.
func upstreamReturning200() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-x",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
}

func TestHandler_WhitelistFiltering(t *testing.T) {
	upA := upstreamReturning200()
	defer upA.Close()
	upB := upstreamReturning200()
	defer upB.Close()
	upC := upstreamReturning200()
	defer upC.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 3, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 5, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: upC.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:            1,
		TokenID:           1,
		AllowedChannelIDs: []uint{5},
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	// Best-effort: verify the request reached upstream B (channel 5) — not strictly
	// asserted here; WhitelistExhausted + XChannelID_OutsideWhitelist tests cover
	// the negative invariants.
}

func TestHandler_WhitelistExhausted_404(t *testing.T) {
	upA := upstreamReturning200()
	defer upA.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 3, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:            1,
		TokenID:           1,
		AllowedChannelIDs: []uint{99}, // none match
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("status = %d, want 404; body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "whitelist active") {
		t.Fatalf("error message must include 'whitelist active'; got %s", w.Body.String())
	}
}

func TestHandler_XChannelID_NotFound_404_BehaviorChange(t *testing.T) {
	// 行为变更回归：现有逻辑在 forcedID 找不到时静默退化；新行为是 404。
	upA := upstreamReturning200()
	defer upA.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 3, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouterWithUserInfo(handler, &app.UserInfo{UserID: 1, TokenID: 1})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(consts.HeaderXChannelID, "999") // not present
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("BREAK regression: forced X-Channel-ID not found must return 404 (was: silent fallback); status = %d body = %s", w.Code, w.Body.String())
	}
}

func TestHandler_XChannelID_OutsideWhitelist_404(t *testing.T) {
	upA := upstreamReturning200()
	defer upA.Close()
	upB := upstreamReturning200()
	defer upB.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 5, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:            1,
		TokenID:           1,
		AllowedChannelIDs: []uint{5},
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(consts.HeaderXChannelID, "7") // exists but not in whitelist
	r.ServeHTTP(w, req)

	if w.Code != 404 {
		t.Fatalf("X-Channel-ID outside whitelist must 404; status = %d body = %s", w.Code, w.Body.String())
	}
}

// TestHandler_GroupWhitelist_FiltersChannels — group-only whitelist (token whitelist empty).
// Channel 1 is excluded by GroupAllowedChannelIDs=[2]; only channel 2 is reachable.
func TestHandler_GroupWhitelist_FiltersChannels(t *testing.T) {
	upA := upstreamReturning200()
	defer upA.Close()
	upB := upstreamReturning200()
	defer upB.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upA.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upB.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:                 1,
		TokenID:                1,
		GroupAllowedChannelIDs: []uint{2}, // only channel 2 allowed
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("group whitelist should allow channel 2; status=%d body=%s", w.Code, w.Body.String())
	}
}

// TestHandler_BothWhitelists_Intersect — group:[1,2] AND token:[2,3] → only ch2 usable.
func TestHandler_BothWhitelists_Intersect(t *testing.T) {
	up1 := upstreamReturning200()
	defer up1.Close()
	up2 := upstreamReturning200()
	defer up2.Close()
	up3 := upstreamReturning200()
	defer up3.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: up1.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: up2.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 3, Type: consts.ChannelTypeOpenAI, BaseURL: up3.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	// group=[1,2] ∩ token=[2,3] → intersection={2}
	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:                 1,
		TokenID:                1,
		GroupAllowedChannelIDs: []uint{1, 2},
		AllowedChannelIDs:      []uint{2, 3},
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("intersection {2} is non-empty; expected 200, got %d body=%s", w.Code, w.Body.String())
	}
}

// TestHandler_BothWhitelists_Conflict_404 — group:[1] AND token:[2] → empty intersection → 404.
func TestHandler_BothWhitelists_Conflict_404(t *testing.T) {
	up1 := upstreamReturning200()
	defer up1.Close()
	up2 := upstreamReturning200()
	defer up2.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: up1.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: up2.URL, Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	// group=[1] ∩ token=[2] → empty
	r := setupRouterWithUserInfo(handler, &app.UserInfo{
		UserID:                 1,
		TokenID:                1,
		GroupAllowedChannelIDs: []uint{1},
		AllowedChannelIDs:      []uint{2},
	})

	w := httptest.NewRecorder()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("conflicting whitelists must yield 404; got %d body=%s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "whitelist active") {
		t.Fatalf("error message must include 'whitelist active'; got %s", w.Body.String())
	}
}

func TestRelayHandler_TokenSource_Estimated(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-123",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "Hello!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 0, "completion_tokens": 0, "total_tokens": 0},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1}, Key: "test-key", Models: "gpt-4o"},
	})
	usageLogs := collectUsageLogs(bus)

	r := setupRouter(handler)
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"Tell me a story about a dragon"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	time.Sleep(100 * time.Millisecond)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if usageLogs.Len() == 0 {
		t.Fatal("expected usage log")
	}
	log := usageLogs.Get(0)
	if log.PromptTokens <= 0 {
		t.Errorf("expected estimated prompt_tokens > 0, got %d", log.PromptTokens)
	}
	if log.TokenSource != string(relayupstream.TokenSourceEstimated) && log.TokenSource != string(relayupstream.TokenSourcePartialEstimated) {
		t.Errorf("expected estimated or partial_estimated source, got %q", log.TokenSource)
	}
}

// TestUserFacingErrorMessage_NilGuards 覆盖 3 个 early-return：
// rctx==nil / rctx.State==nil（State 是 *state.RelayState 指针）/ rctx.State.Err==nil。
// 任一分支都应返回空串而不是 panic 或走 state.StatusFromState。
func TestUserFacingErrorMessage_NilGuards(t *testing.T) {
	// 分支 1: rctx == nil
	if got := state.UserFacingErrorMessage(nil); got != "" {
		t.Errorf("nil rctx → %q, want \"\"", got)
	}
	// 分支 2: rctx.State == nil（state.RelayContext.State 字段是 *state.RelayState 指针，零值即 nil）
	if got := state.UserFacingErrorMessage(&state.RelayContext{}); got != "" {
		t.Errorf("nil State → %q, want \"\"", got)
	}
	// 分支 3: rctx.State.Err == nil（State 非 nil 但 Err 字段未设）
	rctx := &state.RelayContext{State: &state.RelayState{}}
	if got := state.UserFacingErrorMessage(rctx); got != "" {
		t.Errorf("nil Err → %q, want \"\"", got)
	}
}

// TestWhitelistActiveFor_NilUserInfo 覆盖 nil 入参分支：
// 入参签名是 *app.UserInfo，typed-nil 也应返 false。
func TestWhitelistActiveFor_NilUserInfo(t *testing.T) {
	if got := state.WhitelistActiveFor(nil); got {
		t.Errorf("nil *app.UserInfo → true, want false")
	}
	if got := state.WhitelistActiveFor((*app.UserInfo)(nil)); got {
		t.Errorf("typed-nil *app.UserInfo → true, want false")
	}
}

// TestStatusFromState_NilErr_Returns500 覆盖 defensive default：
// State.Err == nil 时不应该走到 switch，应直接回 (500, "")。
// 生产路径 writeResponse 在 Err==nil 时会先 early-return，但 helper 自身的防御分支必须保留。
func TestStatusFromState_NilErr_Returns500(t *testing.T) {
	rctx := &state.RelayContext{State: &state.RelayState{}}
	code, msg := state.StatusFromState(rctx)
	if code != http.StatusInternalServerError {
		t.Errorf("nil Err code = %d, want 500", code)
	}
	if msg != "" {
		t.Errorf("nil Err msg = %q, want \"\"", msg)
	}
}

// TestWriteResponse_WrittenSkipsJSON 覆盖 Outcome.Written=true 短路：
// backend 已写过部分流式 body 时，writeResponse 不能再调 c.JSON 否则会双写（破坏 SSE 协议）。
func TestWriteResponse_WrittenSkipsJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/test", nil)
	// 模拟 backend 已写过 SSE chunk
	_, _ = c.Writer.Write([]byte("data: x\n\n"))
	preLen := w.Body.Len()

	h := &Handler{}
	rctx := &state.RelayContext{
		Context: c,
		State: &state.RelayState{
			Execution: state.ExecutionResult{
				Outcome: state.AttemptResult{Err: errors.New("upstream 500"), Written: true},
				Err:     errors.New("upstream 500"),
			},
			Err:       errors.New("upstream 500"),
			FailPhase: state.PhaseExecute,
		},
	}
	h.writeResponse(rctx)
	if got := w.Body.Len() - preLen; got != 0 {
		t.Errorf("writeResponse appended %d bytes; expected 0 (Written=true must skip JSON)", got)
	}
}

// TestWriteResponse_ForwardedSkips 覆盖 forwarder 接管短路：State.Forwarded=true 直接返。
// 即使 Err 已经设置也不能再 c.JSON 覆盖被 forwarder 写过的响应。
func TestWriteResponse_ForwardedSkips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/test", nil)

	h := &Handler{}
	rctx := &state.RelayContext{
		Context: c,
		State: &state.RelayState{
			Forwarded: true,
			Err:       errors.New("would otherwise 502"),
		},
	}
	h.writeResponse(rctx)
	if w.Body.Len() != 0 {
		t.Errorf("Forwarded=true must skip; wrote %d bytes", w.Body.Len())
	}
}

// TestWriteResponse_NoErrSkips 覆盖 success-path 短路：
// Err==nil 表示 backend 已成功写完 200 body，writeResponse 不应再追加 JSON。
func TestWriteResponse_NoErrSkips(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request, _ = http.NewRequest("POST", "/v1/test", nil)

	h := &Handler{}
	rctx := &state.RelayContext{
		Context: c,
		State:   &state.RelayState{}, // Forwarded=false, Written=false, Err=nil
	}
	h.writeResponse(rctx)
	if w.Body.Len() != 0 {
		t.Errorf("Err=nil must skip JSON; wrote %d bytes", w.Body.Len())
	}
}
