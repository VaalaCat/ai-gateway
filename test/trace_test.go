package test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestTrace_500Error(t *testing.T) {
	env := setupFullEnv(t, "trace500", 1)
	defer env.Close()

	// Setup: upstream that returns 500
	mockUpstream := mockErrorUpstream(500, `{"error":{"message":"internal server error","type":"server_error"}}`)
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("trace500user", 100000)
	env.CreateChannel("err500-ch", 1, "sk-secret-key-for-500-test", mockUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	apiKey := createTraceToken(t, env, userID, "trace500-token", true, models.TokenTraceModeHeaders, false)
	env.SyncFromMaster()

	requestID := fmt.Sprintf("trace500-%s", t.Name())
	w := env.SendChatWithHeaders(apiKey, "gpt-4o", "hello", map[string]string{
		consts.HeaderXRequestID: requestID,
	})

	if w.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}

	env.WaitForLogs()

	// Verify usage log created
	var usageLog models.RequestLog
	result := env.RequestLogDB(t).Where("request_id = ?", requestID).First(&usageLog)
	if result.Error != nil {
		t.Fatalf("no usage log found: %v", result.Error)
	}
	if usageLog.TotalCost != 0 {
		t.Errorf("expected total_cost=0 for failed request, got %d", usageLog.TotalCost)
	}

	// behavior change: none; failed attempts intentionally override headers mode to full.
	// Verify trace record created
	var trace models.RequestTrace
	result = env.RequestTraceDB(t).Where("request_id = ?", requestID).First(&trace)
	if result.Error != nil {
		t.Fatalf("no trace record found: %v", result.Error)
	}

	if trace.InboundPath != "/v1/chat/completions" {
		t.Errorf("inbound_path = %q, want /v1/chat/completions", trace.InboundPath)
	}
	if trace.UpstreamStatus != 500 {
		t.Errorf("upstream_status = %d, want 500", trace.UpstreamStatus)
	}
	if trace.InboundBody == "" {
		t.Error("inbound_body should not be empty")
	}
	if trace.OutboundBody == "" {
		t.Error("outbound_body should not be empty")
	}
	if trace.ResponseBody == "" {
		t.Error("response_body should not be empty")
	}
	if trace.ClientResponseBody == "" {
		t.Error("client_response_body should not be empty")
	}
	if trace.ClientResponseBody != w.Body.String() {
		t.Errorf("client_response_body = %q, want actual response %q", trace.ClientResponseBody, w.Body.String())
	}

	// Verify API key is masked in trace
	if strings.Contains(trace.InboundBody, "sk-secret-key-for-500-test") {
		t.Error("inbound_body contains unmasked API key")
	}
	if strings.Contains(trace.OutboundBody, "sk-secret-key-for-500-test") {
		t.Error("outbound_body contains unmasked API key")
	}

	// Verify upstream host is masked
	upstreamHost := strings.TrimPrefix(mockUpstream.URL, "http://")
	if strings.Contains(trace.OutboundHeaders, upstreamHost) {
		t.Error("outbound_headers contains unmasked upstream host")
	}

	// Verify trace API endpoint returns data
	resp := env.DoAdmin("GET", fmt.Sprintf("/api/admin/logs/%s/trace", requestID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("GET trace API: expected 200, got %d", resp.StatusCode)
	}
	// GetTrace returns an array of per-attempt trace rows (channel resilience records
	// one usage_log_trace per attempt), so decode into a slice and assert on a row.
	var apiTraces []map[string]any
	json.NewDecoder(resp.Body).Decode(&apiTraces)
	resp.Body.Close()

	if len(apiTraces) == 0 {
		t.Fatalf("API trace returned empty array")
	}
	first := apiTraces[0]
	if first["request_id"] != requestID {
		t.Errorf("API trace request_id = %v, want %s", first["request_id"], requestID)
	}
	if status, ok := first["upstream_status"].(float64); !ok || status != 500 {
		t.Errorf("API trace upstream_status = %v, want 500", first["upstream_status"])
	}

	// Verify 404 for nonexistent trace
	resp = env.DoAdmin("GET", "/api/admin/logs/nonexistent/trace", nil)
	if resp.StatusCode != 404 {
		t.Errorf("expected 404 for nonexistent trace, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestTrace_4xxError(t *testing.T) {
	env := setupFullEnv(t, "trace429", 1)
	defer env.Close()

	rateLimitBody := `{"error":{"message":"Rate limit exceeded. Please retry after 20s","type":"rate_limit_error","code":"rate_limit_exceeded"}}`
	mockUpstream := mockErrorUpstream(429, rateLimitBody)
	defer mockUpstream.Close()

	userID := env.CreateUserWithQuota("trace429user", 100000)
	env.CreateChannel("err429-ch", 1, "sk-test-key", mockUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	apiKey := env.CreateTokenWithTrace(userID, "trace429-token")
	env.SyncFromMaster()

	requestID := fmt.Sprintf("trace429-%s", t.Name())
	w := env.SendChatWithHeaders(apiKey, "gpt-4o", "hello", map[string]string{
		consts.HeaderXRequestID: requestID,
	})

	// 4xx errors are forwarded directly to the client
	if w.Code != 429 {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}

	env.WaitForLogs()

	var trace models.RequestTrace
	result := env.RequestTraceDB(t).Where("request_id = ?", requestID).First(&trace)
	if result.Error != nil {
		t.Fatalf("no trace record found: %v", result.Error)
	}

	if trace.UpstreamStatus != 429 {
		t.Errorf("upstream_status = %d, want 429", trace.UpstreamStatus)
	}

	if !strings.Contains(trace.ResponseBody, "rate_limit") {
		t.Errorf("response_body should contain rate limit error, got: %s", trace.ResponseBody)
	}
}

func TestTrace_ConnectionError(t *testing.T) {
	env := setupFullEnv(t, "traceconn", 1)
	defer env.Close()

	// Use an address that will refuse connections
	unreachableURL := "http://127.0.0.1:1"

	userID := env.CreateUserWithQuota("traceconnuser", 100000)
	env.CreateChannel("conn-err-ch", 1, "sk-test-key", unreachableURL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	apiKey := env.CreateTokenWithTrace(userID, "traceconn-token")
	env.SyncFromMaster()

	requestID := fmt.Sprintf("traceconn-%s", t.Name())
	w := env.SendChatWithHeaders(apiKey, "gpt-4o", "hello", map[string]string{
		consts.HeaderXRequestID: requestID,
	})

	if w.Code != 502 {
		t.Fatalf("expected 502, got %d: %s", w.Code, w.Body.String())
	}

	env.WaitForLogs()

	var trace models.RequestTrace
	result := env.RequestTraceDB(t).Where("request_id = ?", requestID).First(&trace)
	if result.Error != nil {
		t.Fatalf("no trace record found: %v", result.Error)
	}

	// Inbound data should still be captured even if connection failed
	if trace.InboundPath != "/v1/chat/completions" {
		t.Errorf("inbound_path = %q, want /v1/chat/completions", trace.InboundPath)
	}
	if trace.InboundBody == "" {
		t.Error("inbound_body should not be empty even for connection errors")
	}

	// Response headers/body may be empty since connection was refused - that's OK
	// But the trace itself should exist
	t.Logf("trace captured for connection error: upstream_status=%d, response_body_len=%d",
		trace.UpstreamStatus, len(trace.ResponseBody))
}

func TestRelay_TraceEnabled(t *testing.T) {
	upstream := mockOpenAIUpstream("Hello with trace!")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-trace-enabled", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("traceenuser", 100000)
	env.CreateChannel("trace-en-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")

	for _, tc := range []struct {
		name      string
		enabled   bool
		mode      models.TokenTraceMode
		legacy    bool
		wantTrace bool
		wantBody  bool
	}{
		{name: "off success", mode: models.TokenTraceModeHeaders},
		{name: "legacy full", enabled: true, legacy: true, wantTrace: true, wantBody: true},
		{name: "headers success", enabled: true, mode: models.TokenTraceModeHeaders, wantTrace: true},
		{name: "full success", enabled: true, mode: models.TokenTraceModeFull, wantTrace: true, wantBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			apiKey := createTraceToken(t, env, userID, "trace-"+strings.ReplaceAll(tc.name, " ", "-"), tc.enabled, tc.mode, tc.legacy)
			env.SyncFromMaster()
			requestID := fmt.Sprintf("trace-%s-%d", strings.ReplaceAll(tc.name, " ", "-"), time.Now().UnixNano())

			w := env.SendChatWithHeaders(apiKey, "gpt-4o", "hello", map[string]string{
				consts.HeaderXRequestID: requestID,
			})
			if w.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
			}
			env.WaitForLogs()

			var usage models.RequestLog
			if err := env.RequestLogDB(t).Where("request_id = ?", requestID).First(&usage).Error; err != nil {
				t.Fatalf("no usage log found: %v", err)
			}
			if usage.HasTrace != tc.wantTrace {
				t.Fatalf("HasTrace=%v want=%v", usage.HasTrace, tc.wantTrace)
			}

			var got models.RequestTrace
			err := env.RequestTraceDB(t).Where("request_id = ?", requestID).First(&got).Error
			if !tc.wantTrace {
				if err == nil {
					t.Fatal("trace row exists for off success")
				}
				return
			}
			if err != nil {
				t.Fatalf("no trace record found: %v", err)
			}
			if got.InboundPath != "/v1/chat/completions" || got.InboundHeaders == "" || got.OutboundHeaders == "" || got.ResponseHeaders == "" {
				t.Fatalf("trace metadata incomplete: %+v", got)
			}
			if tc.wantBody {
				if got.InboundBody == "" || got.OutboundBody == "" || got.ResponseBody == "" || got.ClientResponseBody == "" {
					t.Fatalf("full trace has empty body field: %+v", got)
				}
				return
			}
			if got.InboundBody != "" || got.OutboundBody != "" || got.ResponseBody != "" || got.ClientResponseBody != "" {
				t.Fatalf("header-only trace persisted body: %+v", got)
			}
		})
	}
}

func createTraceToken(
	t *testing.T,
	env *testEnv,
	userID uint,
	name string,
	enabled bool,
	mode models.TokenTraceMode,
	legacy bool,
) string {
	t.Helper()
	body := map[string]any{"user_id": userID, "name": name, "trace_enabled": enabled}
	if mode != "" {
		body["trace_mode"] = mode
	}
	resp := env.DoAdmin("POST", "/api/admin/tokens", body)
	if resp.StatusCode != http.StatusCreated {
		raw, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token: %d %s", resp.StatusCode, raw)
	}
	var tokenResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		t.Fatalf("decode token: %v", err)
	}
	resp.Body.Close()
	key := tokenResp["key"].(string)
	if legacy {
		if err := env.Srv.DB.Model(&models.Token{}).Where("key = ?", key).Update("trace_mode", "").Error; err != nil {
			t.Fatalf("clear legacy trace mode: %v", err)
		}
	}
	return key
}

// Ensure imports are referenced.
var _ = httptest.NewRecorder
