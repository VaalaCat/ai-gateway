package test

import (
	"encoding/json"
	"fmt"
	"io"
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
	apiKey := env.CreateTokenWithTrace(userID, "trace500-token")
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
	var usageLog models.UsageLog
	result := env.Srv.DB.Where("request_id = ?", requestID).First(&usageLog)
	if result.Error != nil {
		t.Fatalf("no usage log found: %v", result.Error)
	}
	if usageLog.TotalCost != 0 {
		t.Errorf("expected total_cost=0 for failed request, got %d", usageLog.TotalCost)
	}

	// Verify trace record created
	var trace models.UsageLogTrace
	result = env.Srv.DB.Where("request_id = ?", requestID).First(&trace)
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

	var trace models.UsageLogTrace
	result := env.Srv.DB.Where("request_id = ?", requestID).First(&trace)
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

	var trace models.UsageLogTrace
	result := env.Srv.DB.Where("request_id = ?", requestID).First(&trace)
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

	// Create token with trace_enabled=true via admin API
	resp := env.DoAdmin("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID, "name": "trace-on-token", "trace_enabled": true,
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token: %d %s", resp.StatusCode, body)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKeyOn := tokenResp["key"].(string)

	env.SyncFromMaster()

	// Send a successful request with trace_enabled=true
	requestIDOn := fmt.Sprintf("trace-on-%d", time.Now().UnixNano())
	w := env.SendChatWithHeaders(apiKeyOn, "gpt-4o", "hello", map[string]string{
		consts.HeaderXRequestID: requestIDOn,
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	env.WaitForLogs()

	// Verify UsageLog has has_trace=true
	var usageLogOn models.UsageLog
	result := env.Srv.DB.Where("request_id = ?", requestIDOn).First(&usageLogOn)
	if result.Error != nil {
		t.Fatalf("no usage log found for trace-on request: %v", result.Error)
	}
	if !usageLogOn.HasTrace {
		t.Error("expected has_trace=true for token with trace_enabled=true")
	}

	// Verify UsageLogTrace record exists
	var traceOn models.UsageLogTrace
	result = env.Srv.DB.Where("request_id = ?", requestIDOn).First(&traceOn)
	if result.Error != nil {
		t.Fatalf("no trace record found for trace-on request: %v", result.Error)
	}
	if traceOn.InboundPath != "/v1/chat/completions" {
		t.Errorf("inbound_path = %q, want /v1/chat/completions", traceOn.InboundPath)
	}

	// ---- Now test with trace_enabled=false (default) ----

	// CreateToken helper creates with trace_enabled=false by default
	apiKeyOff := env.CreateToken(userID, "trace-off-token")
	env.SyncFromMaster()

	requestIDOff := fmt.Sprintf("trace-off-%d", time.Now().UnixNano())
	w = env.SendChatWithHeaders(apiKeyOff, "gpt-4o", "hello again", map[string]string{
		consts.HeaderXRequestID: requestIDOff,
	})
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	env.WaitForLogs()

	// Verify UsageLog has has_trace=false
	var usageLogOff models.UsageLog
	result = env.Srv.DB.Where("request_id = ?", requestIDOff).First(&usageLogOff)
	if result.Error != nil {
		t.Fatalf("no usage log found for trace-off request: %v", result.Error)
	}
	if usageLogOff.HasTrace {
		t.Error("expected has_trace=false for token with trace_enabled=false")
	}

	// Verify NO UsageLogTrace record exists
	var traceOff models.UsageLogTrace
	result = env.Srv.DB.Where("request_id = ?", requestIDOff).First(&traceOff)
	if result.Error == nil {
		t.Error("expected no trace record for token with trace_enabled=false, but found one")
	}
}

// Ensure imports are referenced.
var _ = httptest.NewRecorder
