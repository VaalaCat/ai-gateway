package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	relayupstream "github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// TestShouldPassthrough 已迁移到 pipeline/plan/mode_test.go——shouldPassthrough
// 随 mode_picker.go 拆到 plan 子包后属于该包包级私有函数，单测只能放在同包。
func TestRelayPassthrough_Success(t *testing.T) {
	var receivedModel string
	var receivedAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Capture what was sent
		receivedAuth = r.Header.Get("Authorization")
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		receivedModel, _ = body["model"].(string)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-pt",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "passthrough!"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "upstream-key", Models: "gpt-4o", ModelMapping: `{"gpt-4o":"gpt-4o-mapped"}`},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	// Verify model was mapped
	if receivedModel != "gpt-4o-mapped" {
		t.Errorf("expected upstream model 'gpt-4o-mapped', got %q", receivedModel)
	}

	// Verify auth was replaced
	if receivedAuth != "Bearer upstream-key" {
		t.Errorf("expected auth 'Bearer upstream-key', got %q", receivedAuth)
	}

	// Verify response was passed through
	body := w.Body.String()
	if !strings.Contains(body, "passthrough!") {
		t.Errorf("expected response to contain 'passthrough!', got: %s", body)
	}
}

func TestRelayPassthrough_4xxForwarded(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Custom-Header", "preserved")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "test-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}

	// Verify upstream headers are forwarded
	if w.Header().Get("X-Custom-Header") != "preserved" {
		t.Errorf("expected X-Custom-Header to be preserved")
	}
}

func TestRelayPassthrough_HeaderFilter(t *testing.T) {
	received := make(http.Header)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-pt-hf",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "filtered"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "upstream-key", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("CF-Connecting-IP", "142.91.103.35")
	req.Header.Set("CF-IPCountry", "SG")
	req.Header.Set("X-Forwarded-For", "142.91.103.35")
	req.Header.Set("Forwarded", "for=142.91.103.35;proto=https")
	req.Header.Set("X-Real-IP", "142.91.103.35")
	req.Header.Set("Cdn-Loop", "cloudflare; loops=1")
	req.Header.Set("X-Custom-Keep", "keep-me")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if got := received.Get("X-Custom-Keep"); got != "keep-me" {
		t.Errorf("X-Custom-Keep = %q, want %q", got, "keep-me")
	}

	for _, header := range []string{
		"Cf-Connecting-Ip",
		"Cf-Ipcountry",
		"X-Forwarded-For",
		"Forwarded",
		"X-Real-Ip",
		"Cdn-Loop",
	} {
		if got := received.Get(header); got != "" {
			t.Errorf("%s should be filtered, got %q", header, got)
		}
	}
}

func TestRelayPassthrough_HeaderOverrideCanRestoreFilteredHeader(t *testing.T) {
	var receivedForwardedFor string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedForwardedFor = r.Header.Get("X-Forwarded-For")
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-pt-override",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "override"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "upstream-key", Models: "gpt-4o", HeaderOverride: `{"X-Forwarded-For":"override-value"}`},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Forwarded-For", "original-value")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	if receivedForwardedFor != "override-value" {
		t.Errorf("X-Forwarded-For = %q, want %q", receivedForwardedFor, "override-value")
	}
}

func TestExtractUsageFromPassthroughBody_NonStream(t *testing.T) {
	body := []byte(`{"id":"resp_1","usage":{"input_tokens":100,"output_tokens":50,"total_tokens":150,"input_tokens_details":{"cached_tokens":30}}}`)
	prompt, completion, cacheRead, _ := relayupstream.ExtractUsageFromPassthroughBody(body, false)
	if prompt != 100 {
		t.Errorf("prompt = %d, want 100", prompt)
	}
	if completion != 50 {
		t.Errorf("completion = %d, want 50", completion)
	}
	if cacheRead != 30 {
		t.Errorf("cacheRead = %d, want 30", cacheRead)
	}
}

func TestExtractUsageFromPassthroughBody_ChatFormat(t *testing.T) {
	body := []byte(`{"id":"chatcmpl-1","usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	prompt, completion, _, _ := relayupstream.ExtractUsageFromPassthroughBody(body, false)
	if prompt != 10 {
		t.Errorf("prompt = %d, want 10", prompt)
	}
	if completion != 5 {
		t.Errorf("completion = %d, want 5", completion)
	}
}

func TestExtractUsageFromPassthroughBody_SSEStream(t *testing.T) {
	sseBody := []byte("event: response.created\ndata: {\"type\":\"response.created\"}\n\n" +
		"event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"Hi\"}\n\n" +
		"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"usage\":{\"input_tokens\":200,\"output_tokens\":80,\"total_tokens\":280,\"input_tokens_details\":{\"cached_tokens\":50}}}}\n\n")
	prompt, completion, cacheRead, _ := relayupstream.ExtractUsageFromPassthroughBody(sseBody, true)
	if prompt != 200 {
		t.Errorf("prompt = %d, want 200", prompt)
	}
	if completion != 80 {
		t.Errorf("completion = %d, want 80", completion)
	}
	if cacheRead != 50 {
		t.Errorf("cacheRead = %d, want 50", cacheRead)
	}
}

func TestExtractUsageFromPassthroughBody_EmptyBody(t *testing.T) {
	prompt, completion, _, _ := relayupstream.ExtractUsageFromPassthroughBody(nil, false)
	if prompt != 0 || completion != 0 {
		t.Errorf("expected 0/0, got %d/%d", prompt, completion)
	}
}

func TestNormalizeUsage_OpenAICachedTokens(t *testing.T) {
	// OpenAI: PromptTokens includes cached, CachedTokens is set, CacheReadTokens is 0
	u := relayupstream.NormalizeUsage(codec.Usage{
		PromptTokens: 1000,
		CachedTokens: 800,
	})
	if u.PromptTokens != 200 {
		t.Errorf("PromptTokens = %d, want 200", u.PromptTokens)
	}
	if u.CacheReadTokens != 800 {
		t.Errorf("CacheReadTokens = %d, want 800", u.CacheReadTokens)
	}
}

func TestNormalizeUsage_ClaudeUnchanged(t *testing.T) {
	// Claude: PromptTokens already excludes cached, CacheReadTokens is set directly
	u := relayupstream.NormalizeUsage(codec.Usage{
		PromptTokens:     200,
		CacheReadTokens:  800,
		CacheWriteTokens: 50,
	})
	if u.PromptTokens != 200 {
		t.Errorf("PromptTokens = %d, want 200", u.PromptTokens)
	}
	if u.CacheReadTokens != 800 {
		t.Errorf("CacheReadTokens = %d, want 800", u.CacheReadTokens)
	}
}

func TestNormalizeUsage_NoCaching(t *testing.T) {
	u := relayupstream.NormalizeUsage(codec.Usage{
		PromptTokens:     500,
		CompletionTokens: 100,
	})
	if u.PromptTokens != 500 {
		t.Errorf("PromptTokens = %d, want 500", u.PromptTokens)
	}
	if u.CacheReadTokens != 0 {
		t.Errorf("CacheReadTokens = %d, want 0", u.CacheReadTokens)
	}
}

func TestParseUsageJSON_ClaudeFormat(t *testing.T) {
	data := []byte(`{"input_tokens":200,"output_tokens":50,"cache_read_input_tokens":800,"cache_creation_input_tokens":100}`)
	prompt, completion, cacheRead, cacheWrite := relayupstream.ParseUsageJSON(data)
	if prompt != 200 {
		t.Errorf("prompt = %d, want 200", prompt)
	}
	if completion != 50 {
		t.Errorf("completion = %d, want 50", completion)
	}
	if cacheRead != 800 {
		t.Errorf("cacheRead = %d, want 800", cacheRead)
	}
	if cacheWrite != 100 {
		t.Errorf("cacheWrite = %d, want 100", cacheWrite)
	}
}

func TestParseUsageJSON_OpenAICachedTokens(t *testing.T) {
	data := []byte(`{"prompt_tokens":1000,"completion_tokens":50,"prompt_tokens_details":{"cached_tokens":800}}`)
	prompt, completion, cacheRead, _ := relayupstream.ParseUsageJSON(data)
	if prompt != 1000 {
		t.Errorf("prompt = %d, want 1000", prompt)
	}
	if completion != 50 {
		t.Errorf("completion = %d, want 50", completion)
	}
	if cacheRead != 800 {
		t.Errorf("cacheRead = %d, want 800", cacheRead)
	}
}

func TestExtractUsageFromPassthroughBody_ClaudeSSE(t *testing.T) {
	// Claude SSE: input/cache tokens in message_start (message.usage),
	// output tokens in message_delta (top-level usage)
	sseBody := []byte("event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","model":"claude-sonnet-4-20250514","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":800,"cache_creation_input_tokens":50}}}` + "\n\n" +
		"event: content_block_delta\n" +
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":30}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	prompt, completion, cacheRead, cacheWrite := relayupstream.ExtractUsageFromPassthroughBody(sseBody, true)
	if prompt != 100 {
		t.Errorf("prompt = %d, want 100", prompt)
	}
	if completion != 30 {
		t.Errorf("completion = %d, want 30", completion)
	}
	if cacheRead != 800 {
		t.Errorf("cacheRead = %d, want 800", cacheRead)
	}
	if cacheWrite != 50 {
		t.Errorf("cacheWrite = %d, want 50", cacheWrite)
	}
}

func TestExtractUsageFromPassthroughBody_ClaudeSSE_CumulativeDelta(t *testing.T) {
	// Claude SSE where message_delta contains cumulative values (aligned with Anthropic SDK)
	sseBody := []byte("event: message_start\n" +
		`data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":100,"output_tokens":0,"cache_read_input_tokens":800,"cache_creation_input_tokens":50}}}` + "\n\n" +
		"event: message_delta\n" +
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"input_tokens":100,"output_tokens":30,"cache_read_input_tokens":800,"cache_creation_input_tokens":50}}` + "\n\n" +
		"event: message_stop\n" +
		`data: {"type":"message_stop"}` + "\n\n")
	prompt, completion, cacheRead, cacheWrite := relayupstream.ExtractUsageFromPassthroughBody(sseBody, true)
	if prompt != 100 {
		t.Errorf("prompt = %d, want 100", prompt)
	}
	if completion != 30 {
		t.Errorf("completion = %d, want 30", completion)
	}
	if cacheRead != 800 {
		t.Errorf("cacheRead = %d, want 800", cacheRead)
	}
	if cacheWrite != 50 {
		t.Errorf("cacheWrite = %d, want 50", cacheWrite)
	}
}

func TestExtractUsageFromPassthroughBody_ClaudeNonStream(t *testing.T) {
	body := []byte(`{"id":"msg_1","type":"message","usage":{"input_tokens":200,"output_tokens":50,"cache_read_input_tokens":300,"cache_creation_input_tokens":40}}`)
	prompt, completion, cacheRead, cacheWrite := relayupstream.ExtractUsageFromPassthroughBody(body, false)
	if prompt != 200 {
		t.Errorf("prompt = %d, want 200", prompt)
	}
	if completion != 50 {
		t.Errorf("completion = %d, want 50", completion)
	}
	if cacheRead != 300 {
		t.Errorf("cacheRead = %d, want 300", cacheRead)
	}
	if cacheWrite != 40 {
		t.Errorf("cacheWrite = %d, want 40", cacheWrite)
	}
}

func TestRelayPassthrough_5xxRetries(t *testing.T) {
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
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, Priority: 1, PassthroughEnabled: true}, Key: "k1", Models: "gpt-4o"},
		{ChannelCore: models.ChannelCore{ID: 2, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, Priority: 1, PassthroughEnabled: true}, Key: "k2", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"test"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected 200 after retry, got %d: %s", w.Code, w.Body.String())
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 upstream calls (retry), got %d", callCount)
	}
}

// TestRelayPassthrough_NonStreamUsageCaptured 复现 channel_id=21 yimo-xiaomi 上
// mimo-v2.5-pro 非流式请求 usage 漏抓的故障：
//   - is_stream=false
//   - trace 关
//   - 上游回标准 OpenAI Chat usage（含 prompt_tokens_details.cached_tokens）
//
// 修复前 captureBody=false 导致 respBodyBuf 永远空，落库 token_source=partial_estimated、
// completion_tokens=0；修复后应解析到 provider 报的 prompt=253-192=61、completion=36、
// cache_read=192。
func TestRelayPassthrough_NonStreamUsageCaptured(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-nonstream-usage",
			"object": "chat.completion",
			"model":  "mimo-v2.5-pro",
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "Hi there!"},
				"index":         0,
				"finish_reason": "stop",
			}},
			"usage": map[string]any{
				"prompt_tokens":     253,
				"completion_tokens": 36,
				"total_tokens":      289,
				"prompt_tokens_details": map[string]any{
					"cached_tokens": 192,
				},
				"completion_tokens_details": map[string]any{
					"reasoning_tokens": 13,
				},
			},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "upstream-key", Models: "mimo-v2.5-pro"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST",
		"/v1/chat/completions",
		strings.NewReader(`{"model":"mimo-v2.5-pro","messages":[{"role":"user","content":"Say hi"}],"stream":false}`),
	)
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	var entry protocol.UsageLogEntry
	select {
	case entry = <-got:
	case <-time.After(5 * time.Second):
		t.Fatal("usage.completed event not received within 5s")
	}

	if entry.PromptTokens != 61 {
		t.Errorf("PromptTokens = %d, want 61 (253 - 192 cached)", entry.PromptTokens)
	}
	if entry.CompletionTokens != 36 {
		t.Errorf("CompletionTokens = %d, want 36", entry.CompletionTokens)
	}
	if entry.CacheReadTokens != 192 {
		t.Errorf("CacheReadTokens = %d, want 192", entry.CacheReadTokens)
	}
	if entry.IsStream {
		t.Errorf("IsStream = true, want false")
	}
	if entry.InboundProtocol != "openai_chat" || entry.OutboundProtocol != "openai_chat" {
		t.Errorf("protocols = (%s, %s), want (openai_chat, openai_chat)",
			entry.InboundProtocol, entry.OutboundProtocol)
	}
	// Reconcile 的 divergence 检查在 prompt 估算与 provider 报告差距 >50% 时会把 source
	// 标为 provider_divergent。本 case 即如此，两种都可接受；不可接受的是 partial_estimated /
	// estimated / none，那意味着 provider usage 根本没被读到。
	switch entry.TokenSource {
	case string(relayupstream.TokenSourceProvider), string(relayupstream.TokenSourceProviderDivergent):
	default:
		t.Errorf("TokenSource = %q, want provider or provider_divergent", entry.TokenSource)
	}
	if entry.Status != 1 {
		t.Errorf("Status = %d, want 1", entry.Status)
	}
}

// TestRelayPassthrough_InvalidParamOverrideJSON_Graceful 复刻 backend_passthrough.go:120-128
// 的 graceful warn skip 分支：ch.ParamOverride 是非法 JSON 时不能让整次请求失败，
// 必须 warn + skip + 继续走完上游调用。
//
// 边界点：
//   - 老 main 行为是"配置坏 → 当层跳过 → 上游成功 = 整次成功"，不能演化成 panic 或 500。
func TestRelayPassthrough_InvalidParamOverrideJSON_Graceful(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-bad-param",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true, ParamOverride: `{not-json`}, Key: "k", Models: "gpt-4o"},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid ParamOverride 不该 panic，got: %v", r)
		}
	}()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("invalid ParamOverride 应 graceful 降级到 200，got %d: %s", w.Code, w.Body.String())
	}
}

// TestRelayPassthrough_InvalidHeaderOverrideJSON_Graceful 复刻 backend_passthrough.go:130-138
// 的 graceful warn skip 分支——HeaderOverride 是非法 JSON 时跳过 header 应用，
// 整次请求仍然走通。
func TestRelayPassthrough_InvalidHeaderOverrideJSON_Graceful(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-bad-header",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 1, "completion_tokens": 1, "total_tokens": 2},
		})
	}))
	defer upstream.Close()

	handler, _, _ := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o", HeaderOverride: `[not an object`},
	})

	r := setupRouter(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("invalid HeaderOverride 不该 panic，got: %v", r)
		}
	}()
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("invalid HeaderOverride 应 graceful 降级到 200，got %d: %s", w.Code, w.Body.String())
	}
}

// TestRelayPassthrough_InvalidRequestBodyJSON_ErrNotPanic 已迁移到
// passthrough_external_test.go（package relay_test），因为 Task 4 把
// passthroughBackend 拆到 backend/passthrough 子包后，本文件所属的
// package relay 不能反向 import 子包，否则形成循环依赖。新位置直接构造
// passthrough.Backend 验证同样的 nil-body 防御行为。
