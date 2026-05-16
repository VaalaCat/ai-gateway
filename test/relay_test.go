package test

import (
	"bytes"
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

func TestRelay_BasicChatCompletion(t *testing.T) {
	upstream := mockOpenAIUpstream("Hello from basic test!")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-basic", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("basicuser", 100000)
	apiKey := env.CreateToken(userID, "basic-token")
	env.CreateChannel("basic-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "hi")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in response: %v", resp)
	}
	choice0 := choices[0].(map[string]any)
	msg := choice0["message"].(map[string]any)
	if msg["content"] != "Hello from basic test!" {
		t.Errorf("unexpected content: %v", msg["content"])
	}

	// Verify usage log is written to master DB
	env.WaitForLogs()
	var logCount int64
	env.Srv.DB.Model(&models.UsageLog{}).Count(&logCount)
	if logCount == 0 {
		t.Error("no usage logs created on master")
	}
}

func TestRelay_Streaming(t *testing.T) {
	upstream := mockStreamingUpstream()
	defer upstream.Close()

	env := setupFullEnv(t, "relay-stream", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("streamuser", 100000)
	apiKey := env.CreateToken(userID, "stream-token")
	env.CreateChannel("stream-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
		"stream":   true,
	})
	w := env.SendRaw(apiKey, "POST", "/v1/chat/completions", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	respBody := w.Body.String()
	if !strings.Contains(respBody, "data: ") {
		t.Errorf("streaming response should contain 'data: ' lines, got: %s", respBody)
	}
	if !strings.Contains(respBody, "Hello") {
		t.Errorf("streaming response should contain 'Hello', got: %s", respBody)
	}
	if !strings.Contains(respBody, "[DONE]") {
		t.Errorf("streaming response should contain '[DONE]', got: %s", respBody)
	}
}

func TestRelay_ResponsesAPIStreaming(t *testing.T) {
	// Mock upstream returns chat completions streaming format
	upstream := mockStreamingUpstream()
	defer upstream.Close()

	env := setupFullEnv(t, "relay-resp-stream", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("respstreamuser", 100000)
	apiKey := env.CreateToken(userID, "respstream-token")
	env.CreateChannel("respstream-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	// Send Response API streaming request
	body, _ := json.Marshal(map[string]any{
		"model":  "gpt-4o",
		"input":  "Say Hi",
		"stream": true,
	})
	w := env.SendRaw(apiKey, "POST", "/v1/responses", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	respBody := w.Body.String()
	t.Logf("Response API streaming output:\n%s", respBody)

	// Verify SSE format with event: prefix (Response API format)
	if !strings.Contains(respBody, "event: response.created") {
		t.Errorf("missing event: response.created")
	}
	if !strings.Contains(respBody, "event: response.output_text.delta") {
		t.Errorf("missing event: response.output_text.delta")
	}
	if !strings.Contains(respBody, "event: response.completed") {
		t.Errorf("missing event: response.completed")
	}
	// Verify content includes the streamed text
	if !strings.Contains(respBody, "Hello") {
		t.Errorf("streaming response should contain 'Hello', got: %s", respBody)
	}
}

func TestRelay_ResponsesAPINonStreaming(t *testing.T) {
	upstream := mockOpenAIUpstream("Hello from responses!")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-resp-nonstream", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("respuser", 100000)
	apiKey := env.CreateToken(userID, "resp-token")
	env.CreateChannel("resp-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":  "gpt-4o",
		"input":  "Say Hi",
		"stream": false,
	})
	w := env.SendRaw(apiKey, "POST", "/v1/responses", body, nil)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	t.Logf("Response API non-streaming output: %s", w.Body.String())

	if resp["object"] != "response" {
		t.Errorf("expected object=response, got %v", resp["object"])
	}
	output, _ := resp["output"].([]any)
	if len(output) == 0 {
		t.Fatalf("no output in response: %v", resp)
	}
}

func TestRelay_ResponsesAPIStreaming_DashScopeFormat(t *testing.T) {
	// Test Response API streaming with DashScope's non-standard SSE format:
	// - No space after "data:" and "event:"
	// - Event names with ":HTTP_STATUS/200" suffix
	// - "response.output_text.delta" instead of "response.content_part.delta"
	// - Delta as plain string instead of object
	upstream := mockDashScopeResponsesStreamingUpstream()
	defer upstream.Close()

	env := setupFullEnv(t, "relay-resp-ds", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("dsuser", 100000)
	apiKey := env.CreateToken(userID, "ds-token")
	// Channel with supported_api_types including "responses" so outbound
	// uses ProtocolOpenAIResponses (passthrough to upstream).
	env.CreateChannel("ds-ch", 1, "sk-test", upstream.URL, "qwen-flash", map[string]any{
		"supported_api_types": `["responses"]`,
	})
	env.CreateModelConfig("qwen-flash")
	env.SyncFromMaster()

	body, _ := json.Marshal(map[string]any{
		"model":  "qwen-flash",
		"input":  "Say Hi",
		"stream": true,
	})

	// Use real HTTP server to catch any flushing/buffering issues
	ts := httptest.NewServer(env.Router)
	defer ts.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	output := string(respBody)
	t.Logf("DashScope Response API streaming output:\n%s", output)

	if !strings.Contains(output, "event: response.created") {
		t.Errorf("missing event: response.created")
	}
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("missing event: response.output_text.delta")
	}
	if !strings.Contains(output, "Hello") {
		t.Errorf("streaming response should contain 'Hello', got: %s", output)
	}
	if !strings.Contains(output, "event: response.completed") {
		t.Errorf("missing event: response.completed")
	}
}

func TestRelay_ResponsesAPIStreaming_RealHTTP(t *testing.T) {
	// This test uses a real HTTP server (not httptest.ResponseRecorder)
	// to catch buffering/flushing issues that the recorder won't reveal.
	upstream := mockStreamingUpstream()
	defer upstream.Close()

	env := setupFullEnv(t, "relay-resp-stream-real", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("respstreamrealuser", 100000)
	apiKey := env.CreateToken(userID, "respstreamreal-token")
	env.CreateChannel("respstreamreal-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	// Start a real HTTP server using the agent router
	ts := httptest.NewServer(env.Router)
	defer ts.Close()

	// Send Response API streaming request via real HTTP
	body, _ := json.Marshal(map[string]any{
		"model":  "gpt-4o",
		"input":  "Say Hi",
		"stream": true,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	// Verify Content-Type header
	ct := resp.Header.Get("Content-Type")
	t.Logf("Content-Type: %s", ct)
	if !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q, want text/event-stream", ct)
	}

	// Read full body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	output := string(respBody)
	t.Logf("Response API streaming output (real HTTP):\n%s", output)

	// Verify SSE events
	if !strings.Contains(output, "event: response.created") {
		t.Errorf("missing event: response.created")
	}
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("missing event: response.output_text.delta")
	}
	if !strings.Contains(output, "event: response.completed") {
		t.Errorf("missing event: response.completed")
	}
	if !strings.Contains(output, "Hello") {
		t.Errorf("streaming response should contain 'Hello', got: %s", output)
	}
}

func TestRelay_ResponsesAPIStreaming_OpenRouterReasoningFormat(t *testing.T) {
	// Test full pipeline with OpenRouter-style SSE that includes
	// response.reasoning_text.delta events (thinking/reasoning content).
	upstream := mockOpenRouterResponsesStreamingUpstream()
	defer upstream.Close()

	env := setupFullEnv(t, "relay-resp-reasoning", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("reasoninguser", 100000)
	apiKey := env.CreateToken(userID, "reasoning-token")
	env.CreateChannel("reasoning-ch", 1, "sk-test", upstream.URL, "qwen3-8b", map[string]any{
		"supported_api_types": `["responses"]`,
	})
	env.CreateModelConfig("qwen3-8b")
	env.SyncFromMaster()

	// Start a real HTTP server
	ts := httptest.NewServer(env.Router)
	defer ts.Close()

	body, _ := json.Marshal(map[string]any{
		"model":  "qwen3-8b",
		"input":  "Say Hi",
		"stream": true,
	})
	req, _ := http.NewRequest("POST", ts.URL+"/v1/responses", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	output := string(respBody)
	t.Logf("Response API streaming output (reasoning):\n%s", output)

	// Verify reasoning events are present
	if !strings.Contains(output, "event: response.reasoning_text.delta") {
		t.Errorf("missing event: response.reasoning_text.delta")
	}
	if !strings.Contains(output, "Thinking") {
		t.Errorf("streaming response should contain reasoning text 'Thinking'")
	}

	// Verify content events are present
	if !strings.Contains(output, "event: response.output_text.delta") {
		t.Errorf("missing event: response.output_text.delta")
	}
	if !strings.Contains(output, "Hi there!") {
		t.Errorf("streaming response should contain content 'Hi there!'")
	}

	// Verify stream lifecycle events
	if !strings.Contains(output, "event: response.created") {
		t.Errorf("missing event: response.created")
	}
	if !strings.Contains(output, "event: response.completed") {
		t.Errorf("missing event: response.completed")
	}
}

func TestRelay_MultiChannelFailover(t *testing.T) {
	// First channel always returns 500
	failUpstream := mockErrorUpstream(500, `{"error":{"message":"server error","type":"server_error"}}`)
	defer failUpstream.Close()

	// Second channel works
	goodUpstream := mockOpenAIUpstream("failover success")
	defer goodUpstream.Close()

	env := setupFullEnv(t, "relay-failover", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("failoveruser", 100000)
	apiKey := env.CreateToken(userID, "failover-token")
	env.CreateChannel("fail-ch", 1, "sk-test", failUpstream.URL, "gpt-4o")
	env.CreateChannel("good-ch", 1, "sk-test", goodUpstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "hi")
	if w.Code != 200 {
		t.Fatalf("expected 200 after failover, got %d: %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	json.Unmarshal(w.Body.Bytes(), &resp)
	choices, _ := resp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("no choices in response: %v", resp)
	}
	choice0 := choices[0].(map[string]any)
	msg := choice0["message"].(map[string]any)
	if msg["content"] != "failover success" {
		t.Errorf("expected 'failover success', got %v", msg["content"])
	}
}

func TestRelay_DisabledChannel(t *testing.T) {
	upstream := mockOpenAIUpstream("should not reach")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-disabled", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("disableduser", 100000)
	apiKey := env.CreateToken(userID, "disabled-token")
	chID := env.CreateChannel("disabled-ch", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")

	// Disable the channel via admin API
	env.DoAdmin("PUT", fmt.Sprintf("/api/admin/channels/%d", chID), map[string]any{"status": 0}).Body.Close()

	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "hi")
	// No available channel, should fail
	if w.Code == 200 {
		t.Errorf("expected non-200 for disabled channel, got 200: %s", w.Body.String())
	}
	t.Logf("disabled channel response status: %d", w.Code)
}

func TestRelay_4xxForwarding(t *testing.T) {
	errorBody := `{"error":{"message":"rate limit exceeded","type":"rate_limit_error","code":"rate_limit"}}`
	upstream := mockErrorUpstream(429, errorBody)
	defer upstream.Close()

	env := setupFullEnv(t, "relay-4xx", 1)
	defer env.Close()

	userID := env.CreateUserWithQuota("user4xx", 100000)
	apiKey := env.CreateTokenWithTrace(userID, "token-4xx")
	env.CreateChannel("ch-4xx", 1, "sk-test", upstream.URL, "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	requestID := fmt.Sprintf("4xx-test-%d", time.Now().UnixNano())
	w := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set(consts.HeaderXRequestID, requestID)
	env.Router.ServeHTTP(w, req)

	if w.Code != 429 {
		t.Fatalf("expected 429, got %d: %s", w.Code, w.Body.String())
	}

	// Verify error trace is recorded
	env.WaitForLogs()
	var traceCount int64
	env.Srv.DB.Model(&models.UsageLogTrace{}).Where("request_id = ?", requestID).Count(&traceCount)
	if traceCount == 0 {
		t.Error("no trace record created for the 429 request")
	}
}

func TestRelay_ModelMapping(t *testing.T) {
	upstream, captured := mockInspectingUpstream("mapped response")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-modelmap", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("modelmapuser", 100000)
	apiKey := env.CreateToken(userID, "modelmap-token")
	env.CreateChannel("modelmap-ch", 1, "sk-test", upstream.URL, "gpt-4o", map[string]any{
		"model_mapping": `{"gpt-4o":"gpt-4o-real"}`,
	})
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "test mapping")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the upstream received the mapped model name
	var upstreamBody map[string]any
	if err := json.Unmarshal(captured.Body, &upstreamBody); err != nil {
		t.Fatalf("failed to parse captured upstream body: %v", err)
	}
	model, _ := upstreamBody["model"].(string)
	if model != "gpt-4o-real" {
		t.Errorf("upstream received model=%q, want 'gpt-4o-real'", model)
	}
}

func TestRelay_SystemPromptInjection(t *testing.T) {
	upstream, captured := mockInspectingUpstream("system prompt response")
	defer upstream.Close()

	env := setupFullEnv(t, "relay-sysprompt", 3)
	defer env.Close()

	userID := env.CreateUserWithQuota("syspromptuser", 100000)
	apiKey := env.CreateToken(userID, "sysprompt-token")
	env.CreateChannel("sysprompt-ch", 1, "sk-test", upstream.URL, "gpt-4o", map[string]any{
		"system_prompt": "You are a helpful assistant.",
	})
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	w := env.SendChat(apiKey, "gpt-4o", "hello")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	// Verify the upstream request body contains the system prompt
	var upstreamBody map[string]any
	if err := json.Unmarshal(captured.Body, &upstreamBody); err != nil {
		t.Fatalf("failed to parse captured upstream body: %v", err)
	}
	messages, _ := upstreamBody["messages"].([]any)
	if len(messages) == 0 {
		t.Fatal("no messages in upstream request body")
	}

	// Look for a system message with the injected prompt
	foundSystemPrompt := false
	for _, m := range messages {
		msg, _ := m.(map[string]any)
		if msg["role"] == "system" && msg["content"] == "You are a helpful assistant." {
			foundSystemPrompt = true
			break
		}
	}
	if !foundSystemPrompt {
		t.Errorf("system prompt not found in upstream messages: %s", string(captured.Body))
	}
}
