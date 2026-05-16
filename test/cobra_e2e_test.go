package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// startRealMaster starts a real master server on a random port, returns srv, baseURL, cleanup.
func startRealMaster(t *testing.T) (*master.Server, string, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := newTestMasterRuntimeConfig("127.0.0.1:0")
	srv, err := master.New(cfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	if err := srv.InitAdminUser("admin", "admin123"); err != nil {
		t.Fatalf("init admin: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	// Wait for listener to be ready
	deadline := time.Now().Add(5 * time.Second)
	for srv.Listener == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Listener == nil {
		t.Fatal("master did not start in time")
	}

	baseURL := fmt.Sprintf("http://%s", srv.Listener.Addr().String())
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}
	return srv, baseURL, cleanup
}

func TestCobra_MasterStartAndServe(t *testing.T) {
	_, baseURL, cleanup := startRealMaster(t)
	defer cleanup()

	// Test ping
	resp, err := http.Get(baseURL + "/ping")
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("ping status: %d", resp.StatusCode)
	}
	var pingResult map[string]any
	json.NewDecoder(resp.Body).Decode(&pingResult)
	if pingResult["role"] != "master" {
		t.Errorf("expected role=master, got %v", pingResult["role"])
	}

	// Test login
	jwt := login(t, baseURL, "admin", "admin123")
	if jwt == "" {
		t.Fatal("empty JWT")
	}

	// Test authenticated stats endpoint
	req, _ := http.NewRequest("GET", baseURL+"/api/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	statsResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	defer statsResp.Body.Close()
	if statsResp.StatusCode != 200 {
		body, _ := io.ReadAll(statsResp.Body)
		t.Fatalf("stats status: %d, body: %s", statsResp.StatusCode, body)
	}

	t.Log("Cobra master start and serve test passed!")
}

func TestCobra_MasterEmbeddedRelay(t *testing.T) {
	srv, baseURL, cleanup := startRealMaster(t)
	defer cleanup()

	jwt := login(t, baseURL, "admin", "admin123")

	doReq := func(method, path string, body any) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, baseURL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// Create user + quota
	resp := doReq("POST", "/api/admin/users", map[string]any{"username": "relayuser", "password": "pass", "role": 1})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create user: %d %s", resp.StatusCode, body)
	}
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	userID := uint(user["id"].(float64))

	resp = doReq("PUT", fmt.Sprintf("/api/admin/users/%d/quota", userID), map[string]any{"delta": 100000})
	resp.Body.Close()

	// Mock upstream
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "test", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "relay works!"}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer mockUpstream.Close()

	// Create channel + model + token via API (events sync to embedded relay cache)
	resp = doReq("POST", "/api/admin/channels", map[string]any{
		"name": "relay-ch", "type": 1, "key": "k", "base_url": mockUpstream.URL, "models": "gpt-4o",
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create channel: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	resp = doReq("POST", "/api/admin/models", map[string]any{
		"model_name": "gpt-4o", "input_price": 2.5, "output_price": 10.0,
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create model: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	resp = doReq("POST", "/api/admin/tokens", map[string]any{"user_id": userID, "name": "relay-token"})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token: %d %s", resp.StatusCode, body)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	// Wait for embedded agent WebSocket sync
	time.Sleep(2 * time.Second)

	// Send request through master's /v1/chat/completions
	chatBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	chatReq, _ := http.NewRequest("POST", baseURL+"/v1/chat/completions", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Authorization", "Bearer "+apiKey)
	chatResp, err := http.DefaultClient.Do(chatReq)
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	defer chatResp.Body.Close()

	if chatResp.StatusCode != 200 {
		body, _ := io.ReadAll(chatResp.Body)
		t.Fatalf("relay status %d: %s", chatResp.StatusCode, body)
	}

	var result map[string]any
	json.NewDecoder(chatResp.Body).Decode(&result)
	choices, _ := result["choices"].([]any)
	if len(choices) == 0 {
		t.Fatal("no choices in relay response")
	}

	// Verify usage log was created
	time.Sleep(2 * time.Second)
	logResp := doReq("GET", "/api/admin/logs", nil)
	var logResult map[string]any
	json.NewDecoder(logResp.Body).Decode(&logResult)
	logResp.Body.Close()
	logTotal, _ := logResult["total"].(float64)
	if logTotal < 1 {
		t.Fatal("expected at least 1 usage log from embedded agent")
	}

	_ = srv // keep reference
	t.Log("Cobra master embedded relay test passed!")
}
