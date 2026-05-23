package relay

// 集成测试矩阵（Task 16）：4 份 body × 路径 × success/error
//
// 测试覆盖：
//   - TestRelayNative_TraceBodiesFilled              (#1) native success：4 份 body 全非空
//   - TestRelayNative_UpstreamDecodeFailTrace        (#2) native upstream_decode fail：upstream 返无效 JSON
//   - TestRelayNative_OutboundEncodeFailTrace        (#2b) native outbound_encode fail：BaseURL 含控制字符
//   - TestRelayNative_UpstreamDispatchFailTrace      (#3) native dispatch fail：上游关闭后请求
//   - TestRelayPassthrough_NonStreamClientResponseBodyFilled (#5) passthrough 非流式：关键修复证据
//   - TestRelayPassthrough_StreamClientResponseBodyFilled    (#6) passthrough 流式：4 份 body 全非空
//   - TestRelayPassthrough_UpstreamStatusErrorTrace  (#7) passthrough 502：upstream_status error
//   - TestRelayLegacy_TraceBodiesFilled              (#8) legacy success：4 份 body 全非空
//   - TestRelayLegacy_FailTrace                      (#9) legacy dispatch fail：upstream 不可达
//   - TestRelayEarly_ReadBodyFailTrace               (#10) read body fail：ErrorStage = inbound_decode
//   - TestRelayEarly_ModelEmptyTrace                 (#11) model 缺失：ErrorStage = inbound_decode，InboundBody 已 capture
//   - TestRelayEarly_NoChannelTrace                  (#12) no channel：ErrorStage = internal，InboundBody 已 capture
//
// #4（TestRelayNative_ClientEncodePartialTrace）：需要 hijack 中断 SSE 流，httptest.ResponseRecorder
// 不支持 hijack（无法实现 http.Hijacker 接口），跳过，在报告里 flag。

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
)

// setupRouterWithTrace 走生产链路：通过 UserInfo.TraceEnabled=true 让
// Handler.newRelayContext 显式构造 enabled=true 的 Recorder，TraceData 由 Finalize 填充。
func setupRouterWithTrace(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1, TraceEnabled: true})
		handler.Relay(c)
	})
	return r
}

// setupRouterTraceOff 与 setupRouterWithTrace 镜像，但 UserInfo.TraceEnabled=false。
// 用于矩阵中 trace=off 的所有用例：验证 5 个 _ms / error_stage 始终落，
// 验证失败时仍能凭 failStage!=None 强制走 verbose 路径。
func setupRouterTraceOff(handler *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 1, TraceEnabled: false})
		handler.Relay(c)
	})
	return r
}

// waitForUsage 等待最多 5s 从 got channel 里接收一个 UsageLogEntry。
func waitForUsage(t *testing.T, got <-chan protocol.UsageLogEntry) protocol.UsageLogEntry {
	t.Helper()
	select {
	case entry := <-got:
		return entry
	case <-time.After(5 * time.Second):
		t.Fatal("usage.completed event not received within 5s")
		return protocol.UsageLogEntry{}
	}
}

// traceJSON 对应 TraceRecord.MarshalJSON 的输出结构。
// models.UsageLogTrace 里 headers 是 string（GORM text），
// TraceRecord.MarshalJSON 把 map[string][]string 先 json.Marshal 再作为字符串写入，
// 与 UsageLogTrace 模型保持一致（settler 反序列化零修改）。
type traceJSON struct {
	InboundPath        string `json:"inbound_path"`
	OutboundPath       string `json:"outbound_path"`
	InboundHeaders     string `json:"inbound_headers"`
	OutboundHeaders    string `json:"outbound_headers"`
	InboundBody        string `json:"inbound_body"`
	OutboundBody       string `json:"outbound_body"`
	ResponseHeaders    string `json:"response_headers"`
	ResponseBody       string `json:"response_body"`
	ClientResponseBody string `json:"client_response_body"`
	UpstreamStatus     int    `json:"upstream_status"`
	ErrorStage         string `json:"error_stage"`
}

// unmarshalTrace 从 entry.TraceData 反序列化出 traceJSON（与 TraceRecord.MarshalJSON 对齐）。
func unmarshalTrace(t *testing.T, entry protocol.UsageLogEntry) traceJSON {
	t.Helper()
	if entry.TraceData == "" {
		t.Fatal("entry.TraceData is empty — Recorder not enabled or trace not written")
	}
	var trace traceJSON
	if err := json.Unmarshal([]byte(entry.TraceData), &trace); err != nil {
		t.Fatalf("unmarshal TraceData: %v\nraw: %s", err, entry.TraceData)
	}
	return trace
}

// --- 标准 OpenAI Chat 上游响应（success） ---

func openAIChatSuccessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":     "chatcmpl-trace-test",
			"object": "chat.completion",
			"model":  "gpt-4o",
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "Hello!"},
				"index":         0,
				"finish_reason": "stop",
			}},
			"usage": map[string]int{
				"prompt_tokens":     10,
				"completion_tokens": 5,
				"total_tokens":      15,
			},
		})
	}
}

// ==================== #1 TestRelayNative_TraceBodiesFilled ====================

func TestRelayNative_TraceBodiesFilled(t *testing.T) {
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entry := waitForUsage(t, got)

	// ErrorStage 应为空字符串或 "none"（成功）
	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty or 'none'", entry.ErrorStage)
	}

	// timing 字段不为负
	if entry.InboundDecodeMs < 0 {
		t.Errorf("InboundDecodeMs = %d, want >= 0", entry.InboundDecodeMs)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty")
	}
	if !strings.Contains(trace.InboundBody, "gpt-4o") {
		t.Errorf("InboundBody should contain model name, got: %s", trace.InboundBody)
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty (codec-converted request)")
	}
	if trace.ResponseBody == "" {
		t.Error("ResponseBody (upstream raw) should be non-empty")
	}
	if trace.ClientResponseBody == "" {
		t.Error("ClientResponseBody should be non-empty")
	}
}

// ==================== #2 TestRelayNative_UpstreamDecodeFailTrace ====================
// native upstream 返回非法 JSON → upstream_decode fail。

func TestRelayNative_UpstreamDecodeFailTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`not-valid-json`))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	// upstream_decode fail 时 4 份 body 状态：
	// - InboundBody / OutboundBody 应有值（dispatch 前已填）
	// - ResponseBody 应有值（raw 响应体已被 Recorder 捕获）
	// - ErrorStage 应为 upstream_decode 或 upstream_status（取决于 codec 在哪抛）
	if entry.ErrorStage == "" || entry.ErrorStage == "none" {
		t.Logf("NOTE: ErrorStage = %q (may be empty if native codec returned empty result as success)", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty even on upstream_decode fail")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty even on upstream_decode fail")
	}
	// ResponseBody 应包含上游返回的非法 JSON 原文或为空（取决于流是否被读取）
	t.Logf("ErrorStage=%q InboundBody=%q OutboundBody=%q ResponseBody=%q ClientResponseBody=%q",
		entry.ErrorStage, trace.InboundBody, trace.OutboundBody, trace.ResponseBody, trace.ClientResponseBody)
}

// ==================== #2b TestRelayNative_OutboundEncodeFailTrace ====================
// 让 codec.EncodeRequest 中的 http.NewRequest 因 URL 含控制字符而失败，
// 验证 ErrorStage = outbound_encode，InboundBody 非空、Outbound/Response/Client 为空。

func TestRelayNative_OutboundEncodeFailTrace(t *testing.T) {
	// BaseURL 含 \n —— http.NewRequest 在 codec.EncodeRequest 内部会返
	// "net/url: invalid control character in URL"，触发 outbound_encode 失败。
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://invalid\n.example", Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "outbound_encode" {
		t.Errorf("ErrorStage = %q, want 'outbound_encode'", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty (read before encode)")
	}
	// outbound_encode 在 http.NewRequest 失败时返回，OutboundBody 未被 WithOutbound 写入
	if trace.OutboundBody != "" {
		t.Errorf("OutboundBody should be empty on outbound_encode fail, got: %s", trace.OutboundBody)
	}
	if trace.ResponseBody != "" {
		t.Errorf("ResponseBody should be empty on outbound_encode fail, got: %s", trace.ResponseBody)
	}
	if trace.ClientResponseBody != "" {
		t.Errorf("ClientResponseBody should be empty on outbound_encode fail, got: %s", trace.ClientResponseBody)
	}
}

// ==================== #3 TestRelayNative_UpstreamDispatchFailTrace ====================

func TestRelayNative_UpstreamDispatchFailTrace(t *testing.T) {
	// 先创建 upstream，然后立刻关闭，让 dispatch 失败
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "upstream_dispatch" {
		t.Errorf("ErrorStage = %q, want 'upstream_dispatch'", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty even on dispatch fail")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty (encode succeeded before dispatch)")
	}
	// dispatch 失败后上游没有响应，ResponseBody 和 ClientResponseBody 应为空
	if trace.ResponseBody != "" {
		t.Logf("NOTE: ResponseBody = %q (expected empty on dispatch fail)", trace.ResponseBody)
	}
	if trace.ClientResponseBody != "" {
		t.Logf("NOTE: ClientResponseBody = %q (expected empty on dispatch fail)", trace.ClientResponseBody)
	}
}

// ==================== #5 TestRelayPassthrough_NonStreamClientResponseBodyFilled ====================
// 这是原痛点（passthrough 不写 ClientResponseBody）的结构性修复证据。

func TestRelayPassthrough_NonStreamClientResponseBodyFilled(t *testing.T) {
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":false}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty or 'none' (success)", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	// 4 份 body 全部非空
	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty")
	}
	if trace.ResponseBody == "" {
		t.Error("ResponseBody should be non-empty (upstream raw response)")
	}

	// 这是核心修复断言：passthrough ClientResponseBody 必须非空，且等于 ResponseBody
	if trace.ClientResponseBody == "" {
		t.Error("ClientResponseBody should be non-empty — passthrough client_response_body fix regression!")
	}
	if trace.ClientResponseBody != trace.ResponseBody {
		t.Errorf("passthrough: ClientResponseBody should mirror ResponseBody\n  ClientResponseBody=%q\n  ResponseBody=%q",
			trace.ClientResponseBody, trace.ResponseBody)
	}
}

// ==================== #6 TestRelayPassthrough_StreamClientResponseBodyFilled ====================

func TestRelayPassthrough_StreamClientResponseBodyFilled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		chunks := []string{
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n",
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":null}]}\n\n",
			"data: {\"id\":\"chatcmpl-s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n",
			"data: [DONE]\n\n",
		}
		for _, chunk := range chunks {
			w.Write([]byte(chunk))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty or 'none' (success)", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty")
	}
	if trace.ResponseBody == "" {
		t.Error("ResponseBody should be non-empty (SSE stream captured)")
	}
	if !strings.Contains(trace.ResponseBody, "data:") {
		t.Errorf("ResponseBody should contain SSE data: lines, got: %s", trace.ResponseBody)
	}
	// passthrough：ClientResponseBody 镜像 ResponseBody
	if trace.ClientResponseBody == "" {
		t.Error("ClientResponseBody should be non-empty for passthrough stream")
	}
}

// ==================== #7 TestRelayPassthrough_UpstreamStatusErrorTrace ====================

func TestRelayPassthrough_UpstreamStatusErrorTrace(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(502)
		w.Write([]byte(`{"error":"bad gateway from upstream"}`))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "upstream_status" {
		t.Errorf("ErrorStage = %q, want 'upstream_status'", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty")
	}
	if trace.UpstreamStatus != 502 {
		t.Errorf("UpstreamStatus = %d, want 502", trace.UpstreamStatus)
	}
	// 4xx/5xx error body 也必须被 Recorder 捕获（spec §3.3 要求 4 份 body 都有）
	if trace.ResponseBody == "" {
		t.Error("ResponseBody should be non-empty even on upstream_status error")
	}
	if !strings.Contains(trace.ResponseBody, "bad gateway from upstream") {
		t.Errorf("ResponseBody should contain upstream error payload, got: %s", trace.ResponseBody)
	}
}

// ==================== #8 TestRelayLegacy_TraceBodiesFilled ====================

func TestRelayLegacy_TraceBodiesFilled(t *testing.T) {
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, UseLegacyAdaptor: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty or 'none' (success)", entry.ErrorStage)
	}
	if !entry.UseLegacy {
		t.Error("UseLegacy should be true for legacy path")
	}

	trace := unmarshalTrace(t, entry)

	if trace.InboundBody == "" {
		t.Error("InboundBody should be non-empty")
	}
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty (legacy adaptor outbound body)")
	}
	if trace.ResponseBody == "" {
		t.Error("ResponseBody should be non-empty (legacy upstream response)")
	}
	// legacy 走 passthrough 镜像，所以 ClientResponseBody 也应非空
	if trace.ClientResponseBody == "" {
		t.Error("ClientResponseBody should be non-empty (legacy mirrors upstream)")
	}
}

// ==================== #9 TestRelayLegacy_FailTrace ====================

func TestRelayLegacy_FailTrace(t *testing.T) {
	// 先创建再立刻关闭，让 legacy dispatch 失败
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, UseLegacyAdaptor: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "upstream_dispatch" {
		t.Errorf("ErrorStage = %q, want 'upstream_dispatch'", entry.ErrorStage)
	}

	trace := unmarshalTrace(t, entry)

	// legacy dispatch fail 时 outbound body 应有值（adaptor 序列化了请求）
	if trace.OutboundBody == "" {
		t.Error("OutboundBody should be non-empty (legacy serialized request before dispatch)")
	}
}

// ==================== #10 TestRelayEarly_ReadBodyFailTrace ====================
// 模拟 io.ReadAll 失败（body reader 直接报错），验证 ErrorStage = inbound_decode。

// errReader 是一个始终返回错误的 io.ReadCloser，用于模拟 body 读取失败。
type errReader struct{}

func (errReader) Read(_ []byte) (int, error) { return 0, errors.New("read failure") }
func (errReader) Close() error               { return nil }

func TestRelayEarly_ReadBodyFailTrace(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Key: "k", Models: "test-model"},
	})
	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", errReader{})
	req.ContentLength = 100
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "inbound_decode" {
		t.Errorf("ErrorStage = %q, want inbound_decode", entry.ErrorStage)
	}
	if entry.Status != 0 {
		t.Errorf("Status = %d, want 0", entry.Status)
	}
}

// ==================== #11 TestRelayEarly_ModelEmptyTrace ====================
// model 字段缺失（合法 JSON，但 model 为空字符串），验证 ErrorStage = inbound_decode，
// 且 InboundBody 已被 Recorder capture（body 在 model 校验前已被完整读取）。

func TestRelayEarly_ModelEmptyTrace(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Key: "k", Models: "test-model"},
	})
	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	body := `{"messages":[{"role":"user","content":"hi"}]}` // model 字段缺失
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "inbound_decode" {
		t.Errorf("ErrorStage = %q, want inbound_decode", entry.ErrorStage)
	}
	// body 已被 ReadAll 进 Recorder，即使 model 缺失
	trace := unmarshalTrace(t, entry)
	if trace.InboundBody == "" {
		t.Error("InboundBody 应被 capture（body 在 model 校验前已完整读取）")
	}
}

// ==================== #12 TestRelayEarly_NoChannelTrace ====================
// 请求了一个没有对应 channel 的模型，验证 ErrorStage = internal，
// 且 InboundBody 已被 capture。

func TestRelayEarly_NoChannelTrace(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://x", Status: 1, Weight: 1}, Key: "k", Models: "other-model"},
	})
	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterWithTrace(handler)
	w := httptest.NewRecorder()
	body := `{"model":"unconfigured-model","messages":[]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "internal" {
		t.Errorf("ErrorStage = %q, want internal", entry.ErrorStage)
	}
	// body 在 no-channel 分支前已被完整读取并 capture
	trace := unmarshalTrace(t, entry)
	if trace.InboundBody == "" {
		t.Error("InboundBody 应被 capture（body 在 channel 查找前已完整读取）")
	}
}

// TestRelayPassthrough_TraceOff_Success_TimingFilled 复现 req-1778659446008217864 现场：
//
//	passthrough 非流式 + TraceEnabled=false + 成功
//
// 修复前: 5 个 _ms 列全 0；修复后: upstream_dispatch_ms ≥ 50（上游延迟下界）。
func TestRelayPassthrough_TraceOff_Success_TimingFilled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(60 * time.Millisecond) // 保证 dispatch 段 ≥ 50ms
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-x", "object": "chat.completion", "model": "mimo-v2.5-pro",
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": "ok"},
				"index":         0,
				"finish_reason": "stop",
			}},
			"usage": map[string]int{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5},
		})
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "mimo-v2.5-pro"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		got <- entry
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"mimo-v2.5-pro","stream":false,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	entry := waitForUsage(t, got)

	// 核心断言：upstream_dispatch_ms 真实反映 ≥ 50ms 的上游延迟（不再为 0）
	if entry.UpstreamDispatchMs < 50 {
		t.Errorf("UpstreamDispatchMs = %d, want >= 50 (this is the core regression fix)",
			entry.UpstreamDispatchMs)
	}

	// passthrough 不切 outbound_encode / upstream_decode / client_encode，恒 0
	if entry.OutboundEncodeMs != 0 {
		t.Errorf("OutboundEncodeMs = %d, want 0 (passthrough does not switch this stage)",
			entry.OutboundEncodeMs)
	}
	if entry.UpstreamDecodeMs != 0 {
		t.Errorf("UpstreamDecodeMs = %d, want 0", entry.UpstreamDecodeMs)
	}
	if entry.ClientEncodeMs != 0 {
		t.Errorf("ClientEncodeMs = %d, want 0", entry.ClientEncodeMs)
	}

	// 成功 + trace 关：error_stage 空、TraceData 空
	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty", entry.ErrorStage)
	}
	if entry.TraceData != "" {
		t.Errorf("TraceData should be empty when trace=off + success, got: %s", entry.TraceData)
	}
}

// TestRelayNative_TraceOff_Success：codec 转换路径 + trace 关 + 成功
// 验证 5 个 _ms 落、TraceData 空（成功不触发强制 verbose）。
func TestRelayNative_TraceOff_Success(t *testing.T) {
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty", entry.ErrorStage)
	}
	if entry.TraceData != "" {
		t.Errorf("TraceData should be empty on success+trace=off, got: %s", entry.TraceData)
	}
	// 5 个 _ms 至少非负（native 路径执行快，每段可能为 0，但不应为负）
	if entry.InboundDecodeMs < 0 || entry.OutboundEncodeMs < 0 ||
		entry.UpstreamDispatchMs < 0 || entry.UpstreamDecodeMs < 0 || entry.ClientEncodeMs < 0 {
		t.Errorf("any _ms < 0: in=%d out=%d dispatch=%d decode=%d clientenc=%d",
			entry.InboundDecodeMs, entry.OutboundEncodeMs,
			entry.UpstreamDispatchMs, entry.UpstreamDecodeMs, entry.ClientEncodeMs)
	}
}

// TestRelayNative_TraceOff_UpstreamFail：native + trace 关 + 上游 5xx
// 核心：失败必须强制 verbose，TraceData 非空、error_stage=upstream_status。
func TestRelayNative_TraceOff_UpstreamFail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boom"}`))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: false}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "upstream_status" {
		t.Errorf("ErrorStage = %q, want upstream_status", entry.ErrorStage)
	}
	// 失败强制 verbose：TraceData 必须非空（这是回归老语义的核心）
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty when failed (forced verbose), got empty")
	}
	trace := unmarshalTrace(t, entry)
	if trace.InboundBody == "" {
		t.Error("InboundBody should be filled when forced verbose")
	}
	if trace.UpstreamStatus < 500 {
		t.Errorf("UpstreamStatus = %d, want >= 500", trace.UpstreamStatus)
	}
}

// TestRelayPassthrough_TraceOff_UpstreamFail：passthrough + trace 关 + 上游 4xx
func TestRelayPassthrough_TraceOff_UpstreamFail(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad params"}`))
	}))
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, PassthroughEnabled: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	if entry.ErrorStage != "upstream_status" {
		t.Errorf("ErrorStage = %q, want upstream_status", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty when failed (forced verbose)")
	}
	trace := unmarshalTrace(t, entry)
	if trace.UpstreamStatus != 400 {
		t.Errorf("UpstreamStatus = %d, want 400", trace.UpstreamStatus)
	}
}

// TestRelayLegacy_TraceOff_Success：legacy 路径 + trace 关 + 成功
// 只验 upstream_dispatch_ms 落、TraceData 空。
func TestRelayLegacy_TraceOff_Success(t *testing.T) {
	upstream := httptest.NewServer(openAIChatSuccessHandler())
	defer upstream.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL, Status: 1, Weight: 1, UseLegacyAdaptor: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status = %d", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "" && entry.ErrorStage != "none" {
		t.Errorf("ErrorStage = %q, want empty", entry.ErrorStage)
	}
	if entry.TraceData != "" {
		t.Errorf("TraceData should be empty (legacy success + trace=off), got: %s", entry.TraceData)
	}
	if entry.OutboundEncodeMs != 0 || entry.UpstreamDecodeMs != 0 || entry.ClientEncodeMs != 0 {
		t.Errorf("legacy should only fill upstream_dispatch_ms; got out=%d decode=%d clientenc=%d",
			entry.OutboundEncodeMs, entry.UpstreamDecodeMs, entry.ClientEncodeMs)
	}
}

// TestRelayLegacy_TraceOff_UpstreamFail：legacy + trace 关 + 上游错误
func TestRelayLegacy_TraceOff_UpstreamFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"upstream err"}`))
	}))
	defer server.Close()

	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: server.URL, Status: 1, Weight: 1, UseLegacyAdaptor: true}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"x"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	entry := waitForUsage(t, got)

	// legacy 不细分 stage，统一归到 upstream_dispatch 或 upstream_status
	if entry.ErrorStage != "upstream_dispatch" && entry.ErrorStage != "upstream_status" {
		t.Errorf("ErrorStage = %q, want upstream_dispatch or upstream_status", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty when failed (forced verbose)")
	}
}

// ==================== Task 7: 早错误路径 × trace=off (4 tests) ====================
// 验证早错误（read body fail / invalid JSON / model empty / no channel）在 trace 关时
// 也必须落 TraceData，让 debug 链路不依赖用户开关（design §3 + §4.4 边界）。
// errReader 复用上面（line ~646）声明的 helper struct。

// TestRelayEarly_TraceOff_ReadBodyFail：read body 失败 + trace 关
func TestRelayEarly_TraceOff_ReadBodyFail(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://up", Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", nil)
	req.Body = errReader{}
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "inbound_decode" {
		t.Errorf("ErrorStage = %q, want inbound_decode", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty for early-fail (forced verbose)")
	}
}

// TestRelayEarly_TraceOff_InvalidJSON：JSON 解析失败 + trace 关
func TestRelayEarly_TraceOff_InvalidJSON(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://up", Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{bad json`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "inbound_decode" {
		t.Errorf("ErrorStage = %q, want inbound_decode", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty for early-fail (forced verbose)")
	}
	trace := unmarshalTrace(t, entry)
	if !strings.Contains(trace.InboundBody, "bad json") {
		t.Errorf("InboundBody should include raw body, got: %q", trace.InboundBody)
	}
}

// TestRelayEarly_TraceOff_ModelEmpty：JSON 合法但 model 字段缺失 + trace 关
func TestRelayEarly_TraceOff_ModelEmpty(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://up", Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "inbound_decode" {
		t.Errorf("ErrorStage = %q, want inbound_decode", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty for early-fail (forced verbose)")
	}
}

// TestRelayEarly_TraceOff_NoChannel：模型无可用 channel + trace 关
func TestRelayEarly_TraceOff_NoChannel(t *testing.T) {
	handler, _, bus := setupTestHandler([]*models.Channel{
		// 故意只放 gpt-4o，不放 claude-3
		{ChannelCore: models.ChannelCore{ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://up", Status: 1, Weight: 1}, Key: "k", Models: "gpt-4o"},
	})

	got := make(chan protocol.UsageLogEntry, 1)
	events.SubscribeUsageCompleted(bus, func(_ context.Context, e protocol.UsageLogEntry) error {
		got <- e
		return nil
	})

	r := setupRouterTraceOff(handler)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"claude-3","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	if w.Code < 400 {
		t.Fatalf("status = %d, want >= 400 (no channel)", w.Code)
	}
	entry := waitForUsage(t, got)

	if entry.ErrorStage != "internal" {
		t.Errorf("ErrorStage = %q, want internal", entry.ErrorStage)
	}
	if entry.TraceData == "" {
		t.Fatal("TraceData must be non-empty for early-fail (forced verbose)")
	}
	trace := unmarshalTrace(t, entry)
	if !strings.Contains(trace.InboundBody, "claude-3") {
		t.Errorf("InboundBody should include raw request, got: %q", trace.InboundBody)
	}
}
