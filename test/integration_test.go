package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	agentrelay "github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestEndToEndFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	// ======= 1. Start Master =======
	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()

	// ======= 2. Setup data via API =======
	// Create admin
	srv.InitAdminUser("admin", "admin123")

	// Login
	jwt := login(t, masterTS.URL, "admin", "admin123")

	// Helper for authed requests
	doReq := func(method, path string, body any) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, masterTS.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		return resp
	}

	// Create normal user with quota
	resp := doReq("POST", "/api/admin/users", map[string]any{"username": "user1", "password": "pass", "role": 1})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create user: %d %s", resp.StatusCode, body)
	}
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	userID := uint(user["id"].(float64))

	// Top up user quota
	resp = doReq("PUT", fmt.Sprintf("/api/admin/users/%d/quota", userID), map[string]any{"delta": 100000})
	resp.Body.Close()

	// Create mock upstream server
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "Hello from test!"}}},
			"usage":   map[string]int{"prompt_tokens": 100, "completion_tokens": 50},
		})
	}))
	defer mockUpstream.Close()

	// Create channel pointing to mock upstream
	resp = doReq("POST", "/api/admin/channels", map[string]any{
		"name":     "test-channel",
		"type":     1,
		"key":      "test-upstream-key",
		"base_url": mockUpstream.URL,
		"models":   "gpt-4o",
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create channel: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Create model config
	resp = doReq("POST", "/api/admin/models", map[string]any{
		"model_name":   "gpt-4o",
		"input_price":  2.5,
		"output_price": 10.0,
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create model: %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Create token for user
	resp = doReq("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID,
		"name":    "test-token",
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token: %d %s", resp.StatusCode, body)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	// Create agent credentials in master DB
	srv.DB.Create(&models.Agent{AgentID: "test-agent", Secret: "test-secret", Name: "test", Status: 1})

	// ======= 3. Setup Agent-side components =======
	wsURL := "ws" + strings.TrimPrefix(masterTS.URL, "http") + "/ws/agent"
	wsHeaders := http.Header{}
	wsHeaders.Set(consts.HeaderXAgentID, "test-agent")
	wsHeaders.Set(consts.HeaderXAgentSecret, "test-secret")
	client, err := ws.Dial(context.Background(), wsURL, logger, wsHeaders)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()

	agentBus := eventbus.NewMemoryBus()
	store := cache.NewStore(client, config.AgentCacheConfig{})

	// Bridge WS to local EventBus
	bridge := cache.NewWSBridge(client, store, agentBus, logger)
	bridge.Start()

	// Syncer
	syncer := cache.NewSyncer(store, client, agentBus, logger, 5*time.Minute)
	syncer.SubscribeEvents()

	// Full sync
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := syncer.FullSync(ctx); err != nil {
		t.Fatalf("full sync: %v", err)
	}

	// LRU 实体（token / user）不参与 FullSync——只走 push apply-if-present 或 miss → RPC fetch
	if store.ChannelCount() == 0 {
		t.Error("no channels synced")
	}
	if store.ModelConfigCount() == 0 {
		t.Error("no model configs synced")
	}
	t.Logf("synced: channels=%d models=%d version=%d (token LRU populated lazily)",
		store.ChannelCount(), store.ModelConfigCount(), store.Version())

	// Token 走 LRU miss → RPC fetch
	token := store.GetToken(ctx, apiKey)
	if token == nil {
		t.Fatal("API key not found via LRU miss + RPC fetch")
	}

	// Verify channel index
	channels := store.GetChannelsForModel("gpt-4o")
	if len(channels) == 0 {
		t.Fatal("no channels for gpt-4o in cache")
	}

	// ======= 4. Setup Agent relay + reporter =======
	rep := reporter.New(agentBus, client, logger, 100, 1*time.Second, "test-agent")
	rep.Start(ctx)

	pool := upstream.NewTransportPool(100, 10, 30*time.Second, upstream.KeepaliveConfig{Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3})
	relayCfg := &config.AgentRuntimeConfig{
		Relay: config.RelayConfig{Timeout: 30},
	}
	agentApp := agentappkg.NewDefaultAgentApplication(store, nil, logger, relayCfg, pool)
	relayHandler := agentrelay.NewHandler(agentBus, agentApp, backend.NewDispatcher(agentApp), nil, nil)

	// Create agent's HTTP router
	agentRouter := gin.New()
	v1 := agentRouter.Group("/v1")
	v1.Use(auth.TokenAuth(store))
	v1.POST("/chat/completions", relayHandler.Relay)

	// ======= 5. Send request through agent =======
	w := httptest.NewRecorder()
	chatReq, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`))
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Authorization", "Bearer "+apiKey)
	agentRouter.ServeHTTP(w, chatReq)

	if w.Code != 200 {
		t.Fatalf("relay status = %d, body = %s", w.Code, w.Body.String())
	}

	// Verify response from mock upstream (native codec re-encodes, so id may differ)
	var chatResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &chatResp)
	choices, _ := chatResp["choices"].([]any)
	if len(choices) == 0 {
		t.Fatalf("unexpected response (no choices): %v", chatResp)
	}
	choice0, _ := choices[0].(map[string]any)
	msg, _ := choice0["message"].(map[string]any)
	if msg["content"] != "Hello from test!" {
		t.Errorf("unexpected response content: %v", chatResp)
	}

	// ======= 6. Wait for usage report and billing =======
	// Reporter flushes every 1 second
	time.Sleep(3 * time.Second)

	// Check usage log on master
	var logCount int64
	srv.DB.Model(&models.UsageLog{}).Count(&logCount)
	if logCount == 0 {
		t.Error("no usage logs created on master")
	} else {
		t.Logf("usage logs: %d", logCount)
	}

	// Check user quota decreased
	var updatedUser models.User
	srv.DB.First(&updatedUser, userID)
	if updatedUser.Quota >= 100000 {
		t.Errorf("user quota should have decreased, still %d", updatedUser.Quota)
	} else {
		t.Logf("user quota: %d (was 100000)", updatedUser.Quota)
	}

	t.Log("End-to-end test passed!")
}

func TestEndToEnd_ChannelTest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	// Mock upstream that responds to both /v1/models and /v1/chat/completions
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "chat/completions") {
			json.NewEncoder(w).Encode(map[string]any{
				"id": "chatcmpl-test", "object": "chat.completion",
				"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
				"usage":   map[string]int{"prompt_tokens": 5, "completion_tokens": 1, "total_tokens": 6},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-4o"}, {"id": "gpt-3.5-turbo"}},
		})
	}))
	defer mockUpstream.Close()

	masterCfg := newTestMasterRuntimeConfig("127.0.0.1:0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	srv.InitAdminUser("admin", "admin123")

	// Use Run() so embedded agent starts and relay routes are available
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	deadline := time.Now().Add(5 * time.Second)
	for srv.Listener == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.Listener == nil {
		t.Fatal("master did not start in time")
	}
	masterURL := fmt.Sprintf("http://%s", srv.Listener.Addr().String())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	jwt := login(t, masterURL, "admin", "admin123")

	doReq := func(method, path string, body any) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, masterURL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, _ := http.DefaultClient.Do(req)
		return resp
	}

	// Create user + quota for the test token
	resp := doReq("POST", "/api/admin/users", map[string]any{"username": "chtestuser", "password": "pass", "role": 1})
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	userID := uint(user["id"].(float64))
	doReq("PUT", fmt.Sprintf("/api/admin/users/%d/quota", userID), map[string]any{"delta": 100000}).Body.Close()

	// Pre-create the __system_test__ token via API so it syncs to embedded agent.
	// The channel test handler uses this token internally to call the relay endpoint.
	resp = doReq("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID, "name": "__system_test__",
	})
	resp.Body.Close()

	// Create channel pointing to mock
	resp = doReq("POST", "/api/admin/channels", map[string]any{
		"name": "test-ch", "type": 1, "key": "test-key", "base_url": mockUpstream.URL, "models": "gpt-4o",
	})
	var ch map[string]any
	json.NewDecoder(resp.Body).Decode(&ch)
	resp.Body.Close()
	chID := int(ch["id"].(float64))

	// Create model mapping so relay can find the channel
	doReq("POST", "/api/admin/models", map[string]any{"name": "gpt-4o", "enabled_channel_ids": []int{chID}}).Body.Close()

	// Wait for token, channel, and model to sync to embedded agent
	time.Sleep(500 * time.Millisecond)

	// Test channel connectivity
	resp = doReq("POST", fmt.Sprintf("/api/admin/channels/%d/test", chID), nil)
	if resp.StatusCode != 200 {
		t.Fatalf("channel test status: %d", resp.StatusCode)
	}
	var testResult map[string]any
	json.NewDecoder(resp.Body).Decode(&testResult)
	resp.Body.Close()
	if testResult["success"] != true {
		t.Errorf("channel test failed: %v", testResult)
	}

	// Fetch models from upstream
	resp = doReq("POST", "/api/admin/channels/fetch-models", map[string]any{
		"base_url": mockUpstream.URL, "key": "test-key", "type": 1,
	})
	if resp.StatusCode != 200 {
		t.Fatalf("fetch models status: %d", resp.StatusCode)
	}
	var fetchResult map[string]any
	json.NewDecoder(resp.Body).Decode(&fetchResult)
	resp.Body.Close()
	fetchedModels, _ := fetchResult["models"].([]any)
	if len(fetchedModels) != 2 {
		t.Errorf("expected 2 fetched models, got %d", len(fetchedModels))
	}

	t.Log("Channel test and fetch models passed!")
}

func TestEndToEnd_ModelSync(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()
	srv.InitAdminUser("admin", "admin123")
	jwt := login(t, masterTS.URL, "admin", "admin123")

	doReq := func(method, path string, body any) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, masterTS.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, _ := http.DefaultClient.Do(req)
		return resp
	}

	// Create channels with overlapping models
	doReq("POST", "/api/admin/channels", map[string]any{
		"name": "ch1", "type": 1, "key": "k1", "base_url": "http://example.com", "models": "gpt-4o,gpt-3.5-turbo",
	}).Body.Close()
	doReq("POST", "/api/admin/channels", map[string]any{
		"name": "ch2", "type": 1, "key": "k2", "base_url": "http://example.com", "models": "gpt-4o,claude-3",
	}).Body.Close()

	// Sync
	resp := doReq("POST", "/api/admin/models/sync", nil)
	if resp.StatusCode != 200 {
		t.Fatalf("sync status: %d", resp.StatusCode)
	}
	var result map[string]any
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	created := int(result["created"].(float64))
	if created != 3 { // gpt-4o, gpt-3.5-turbo, claude-3
		t.Errorf("expected 3 models created, got %d", created)
	}

	// Second sync should create 0
	resp = doReq("POST", "/api/admin/models/sync", nil)
	json.NewDecoder(resp.Body).Decode(&result)
	resp.Body.Close()
	if int(result["created"].(float64)) != 0 {
		t.Error("second sync should create 0")
	}
	t.Log("Model sync test passed!")
}

func TestEndToEnd_MasterEmbeddedRelay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	// Mock upstream
	mockUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id": "emb-test", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "embedded relay!"}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	defer mockUpstream.Close()

	masterCfg := newTestMasterRuntimeConfig("127.0.0.1:0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	srv.InitAdminUser("admin", "admin123")

	// Use Run() so embedded agent is started
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
	masterURL := fmt.Sprintf("http://%s", srv.Listener.Addr().String())
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	jwt := login(t, masterURL, "admin", "admin123")

	doReq := func(method, path string, body any) *http.Response {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		req, _ := http.NewRequest(method, masterURL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		resp, _ := http.DefaultClient.Do(req)
		return resp
	}

	// Create user + quota
	resp := doReq("POST", "/api/admin/users", map[string]any{"username": "embuser", "password": "pass", "role": 1})
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	userID := uint(user["id"].(float64))
	doReq("PUT", fmt.Sprintf("/api/admin/users/%d/quota", userID), map[string]any{"delta": 100000}).Body.Close()

	// Create token via API
	resp = doReq("POST", "/api/admin/tokens", map[string]any{"user_id": userID, "name": "emb-token"})
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	apiKey := tokenResp["key"].(string)

	// Create channel via API
	doReq("POST", "/api/admin/channels", map[string]any{
		"name": "emb-ch", "type": 1, "key": "k", "base_url": mockUpstream.URL, "models": "gpt-4o",
	}).Body.Close()

	// Create model config via API
	doReq("POST", "/api/admin/models", map[string]any{
		"model_name": "gpt-4o", "input_price": 2.5, "output_price": 10.0,
	}).Body.Close()

	// Wait for channel/model push events to propagate via FullCache Apply.
	// Token does not need waiting: LRU apply-if-present never warms from push;
	// auth cache-miss triggers RPC (sync.fetchEntity) to load the token on demand.
	time.Sleep(200 * time.Millisecond)

	// Call master's /v1/chat/completions
	chatBody, _ := json.Marshal(map[string]any{
		"model":    "gpt-4o",
		"messages": []map[string]string{{"role": "user", "content": "test embedded"}},
	})
	chatReq, _ := http.NewRequest("POST", masterURL+"/v1/chat/completions", bytes.NewReader(chatBody))
	chatReq.Header.Set("Content-Type", "application/json")
	chatReq.Header.Set("Authorization", "Bearer "+apiKey)
	chatResp, err := http.DefaultClient.Do(chatReq)
	if err != nil {
		t.Fatalf("embedded relay: %v", err)
	}
	defer chatResp.Body.Close()

	if chatResp.StatusCode != 200 {
		body, _ := io.ReadAll(chatResp.Body)
		t.Fatalf("embedded relay status %d: %s", chatResp.StatusCode, body)
	}

	var chatResult map[string]any
	json.NewDecoder(chatResp.Body).Decode(&chatResult)
	choices, _ := chatResult["choices"].([]any)
	if len(choices) == 0 {
		t.Fatal("no choices in embedded relay response")
	}

	// Verify usage log was created (this was the bug — old relay lost usage logs)
	time.Sleep(2 * time.Second) // wait for Reporter flush → Settler settle
	logResp := doReq("GET", "/api/admin/logs", nil)
	var logResult map[string]any
	json.NewDecoder(logResp.Body).Decode(&logResult)
	logResp.Body.Close()

	total, _ := logResult["total"].(float64)
	if total < 1 {
		t.Fatal("expected at least 1 usage log, got 0 — usage logging is broken")
	}
	t.Log("Master embedded agent E2E test passed (with usage logging!)")
}
