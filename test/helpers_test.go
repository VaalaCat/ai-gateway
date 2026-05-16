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
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// testEnv holds all components for an end-to-end test: master server,
// agent cache/relay, WebSocket bridge, and reporter. It simulates the
// full production architecture (master ↔ agent via WebSocket) with a
// real in-memory SQLite database.
type testEnv struct {
	t         *testing.T
	Srv       *master.Server
	MasterTS  *httptest.Server
	JWT       string
	Store     *cache.Store
	AgentBus  app.EventBus
	Router    *gin.Engine
	wsClient  *ws.Client
	cancelCtx context.CancelFunc
}

func newTestMasterRuntimeConfig(listen string) *config.MasterRuntimeConfig {
	return &config.MasterRuntimeConfig{
		LogLevel: "info",
		Master: config.MasterConfig{
			Listen:    listen,
			DBPath:    ":memory:",
			JWTSecret: "test-secret",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			ReportFlushInterval: 1,
		},
	}
}

// setupFullEnv creates a complete master + agent environment connected via WebSocket.
// The agent syncs all data (tokens, channels, models) from master.
// Call env.Close() when done.
func setupFullEnv(t *testing.T, agentID string, retryMax int) *testEnv {
	t.Helper()
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)

	srv.InitAdminUser("admin", "admin123")
	jwt := login(t, masterTS.URL, "admin", "admin123")

	// Register agent and connect via WebSocket
	srv.DB.Create(&models.Agent{AgentID: agentID, Secret: agentID + "-secret", Name: agentID, Status: 1})
	wsURL := "ws" + strings.TrimPrefix(masterTS.URL, "http") + "/ws/agent"
	wsHeaders := http.Header{}
	wsHeaders.Set(consts.HeaderXAgentID, agentID)
	wsHeaders.Set(consts.HeaderXAgentSecret, agentID+"-secret")
	client, err := ws.Dial(context.Background(), wsURL, logger, wsHeaders)
	if err != nil {
		masterTS.Close()
		t.Fatalf("dial: %v", err)
	}

	agentBus := eventbus.NewMemoryBus()
	store := cache.NewStore(client, config.AgentCacheConfig{})
	bridge := cache.NewWSBridge(client, store, agentBus, logger)
	syncer := cache.NewSyncer(store, client, agentBus, logger, 5*time.Minute)
	bridge.Syncer = syncer
	bridge.Start()
	syncer.SubscribeEvents()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	// Start reporter so usage events flow back to master
	rep := reporter.New(agentBus, client, logger, 100, 1*time.Second, agentID)
	rep.Start(ctx)

	pool := upstream.NewTransportPool(100, 10, 30*time.Second)
	relayCfg := &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RetryMax: retryMax},
		Relay:   config.RelayConfig{Timeout: 30},
	}
	agentApp := agentappkg.NewDefaultAgentApplication(store, nil, logger, relayCfg, pool)
	relayHandler := agentrelay.NewHandler(agentBus, agentApp, backend.NewDispatcher(agentApp))

	agentRouter := gin.New()
	v1 := agentRouter.Group("/v1")
	v1.Use(auth.TokenAuth(store))
	v1.GET("/models", agentrelay.ListModels(store))
	v1.POST("/chat/completions", relayHandler.Relay)
	v1.POST("/responses", relayHandler.Relay)
	v1.POST("/messages", relayHandler.Relay)

	return &testEnv{
		t:         t,
		Srv:       srv,
		MasterTS:  masterTS,
		JWT:       jwt,
		Store:     store,
		AgentBus:  agentBus,
		Router:    agentRouter,
		wsClient:  client,
		cancelCtx: cancel,
	}
}

// Close tears down the test environment.
func (e *testEnv) Close() {
	e.cancelCtx()
	e.wsClient.Close()
	e.MasterTS.Close()
}

// SyncFromMaster runs a full sync from master to agent cache.
func (e *testEnv) SyncFromMaster() {
	e.t.Helper()
	syncer := cache.NewSyncer(e.Store, e.wsClient, e.AgentBus, zap.NewNop(), 5*time.Minute)
	syncer.SubscribeEvents()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := syncer.FullSync(ctx); err != nil {
		e.t.Fatalf("full sync: %v", err)
	}
}

// DoAdmin sends an authenticated admin API request to the master.
func (e *testEnv) DoAdmin(method, path string, body any) *http.Response {
	e.t.Helper()
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	req, _ := http.NewRequest(method, e.MasterTS.URL+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+e.JWT)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

// CreateUserWithQuota creates a user and tops up quota.
func (e *testEnv) CreateUserWithQuota(username string, quota int) uint {
	e.t.Helper()
	resp := e.DoAdmin("POST", "/api/admin/users", map[string]any{"username": username, "password": "pass", "role": 1})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create user %s: %d %s", username, resp.StatusCode, body)
	}
	var user map[string]any
	json.NewDecoder(resp.Body).Decode(&user)
	resp.Body.Close()
	userID := uint(user["id"].(float64))
	e.DoAdmin("PUT", fmt.Sprintf("/api/admin/users/%d/quota", userID), map[string]any{"delta": quota}).Body.Close()
	return userID
}

// CreateToken creates an API token for a user and returns the key.
func (e *testEnv) CreateToken(userID uint, name string) string {
	e.t.Helper()
	resp := e.DoAdmin("POST", "/api/admin/tokens", map[string]any{"user_id": userID, "name": name})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create token %s: %d %s", name, resp.StatusCode, body)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	return tokenResp["key"].(string)
}

// CreateTokenWithTrace creates an API token with trace_enabled=true and returns the key.
func (e *testEnv) CreateTokenWithTrace(userID uint, name string) string {
	e.t.Helper()
	resp := e.DoAdmin("POST", "/api/admin/tokens", map[string]any{
		"user_id": userID, "name": name, "trace_enabled": true,
	})
	if resp.StatusCode != 201 {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create token %s: %d %s", name, resp.StatusCode, body)
	}
	var tokenResp map[string]any
	json.NewDecoder(resp.Body).Decode(&tokenResp)
	resp.Body.Close()
	return tokenResp["key"].(string)
}

// CreateChannel creates a channel and returns its ID.
func (e *testEnv) CreateChannel(name string, channelType int, key, baseURL, models string, extra ...map[string]any) uint {
	e.t.Helper()
	body := map[string]any{
		"name": name, "type": channelType, "key": key,
		"base_url": baseURL, "models": models,
	}
	for _, ex := range extra {
		for k, v := range ex {
			body[k] = v
		}
	}
	resp := e.DoAdmin("POST", "/api/admin/channels", body)
	if resp.StatusCode != 201 {
		b, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("create channel %s: %d %s", name, resp.StatusCode, b)
	}
	var ch map[string]any
	json.NewDecoder(resp.Body).Decode(&ch)
	resp.Body.Close()
	return uint(ch["id"].(float64))
}

// CreateModelConfig creates a model pricing config.
func (e *testEnv) CreateModelConfig(modelName string) {
	e.t.Helper()
	e.DoAdmin("POST", "/api/admin/models", map[string]any{
		"model_name": modelName, "input_price": 2.5, "output_price": 10.0,
	}).Body.Close()
}

// SendChat sends a chat completion request through the agent router.
func (e *testEnv) SendChat(apiKey, model, content string, extra ...map[string]any) *httptest.ResponseRecorder {
	e.t.Helper()
	body := map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": content}},
	}
	for _, ex := range extra {
		for k, v := range ex {
			body[k] = v
		}
	}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	e.Router.ServeHTTP(w, req)
	return w
}

// SendChatWithHeaders sends a chat completion request with additional headers.
func (e *testEnv) SendChatWithHeaders(apiKey, model, content string, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": content}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	e.Router.ServeHTTP(w, req)
	return w
}

// SendRaw sends a raw request to any path on the agent router.
func (e *testEnv) SendRaw(apiKey, method, path string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	e.Router.ServeHTTP(w, req)
	return w
}

// ListModels sends a GET /v1/models request through the agent router.
func (e *testEnv) ListModels(apiKey string) *httptest.ResponseRecorder {
	e.t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	e.Router.ServeHTTP(w, req)
	return w
}

// WaitForLogs waits for the reporter to flush usage logs to master.
func (e *testEnv) WaitForLogs() {
	time.Sleep(3 * time.Second)
}

func login(t *testing.T, baseURL, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password})
	resp, err := http.Post(baseURL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("login failed: %d %s", resp.StatusCode, b)
	}
	var result map[string]string
	json.NewDecoder(resp.Body).Decode(&result)
	return result["token"]
}

// mockOpenAIUpstream creates a mock OpenAI-compatible upstream server.
func mockOpenAIUpstream(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": content}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
}

// mockStreamingUpstream creates a mock streaming OpenAI-compatible upstream.
func mockStreamingUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"role":"assistant"},"index":0}]}`,
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":"Hello"},"index":0}]}`,
			`{"id":"c1","object":"chat.completion.chunk","choices":[{"delta":{"content":" world"},"index":0}],"usage":{"prompt_tokens":5,"completion_tokens":2}}`,
			"[DONE]",
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
	}))
}

// mockErrorUpstream creates a mock upstream that returns a specific HTTP status code.
func mockErrorUpstream(statusCode int, errorBody string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		fmt.Fprint(w, errorBody)
	}))
}

// mockClaudeUpstream creates a mock Anthropic Claude API upstream.
func mockClaudeUpstream(content string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":   "msg_test",
			"type": "message",
			"role": "assistant",
			"content": []map[string]any{{
				"type": "text",
				"text": content,
			}},
			"model":         "claude-sonnet-4-20250514",
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]int{"input_tokens": 10, "output_tokens": 5},
		})
	}))
}

// mockDashScopeResponsesStreamingUpstream creates a mock upstream that returns
// DashScope-style Response API SSE streaming (non-standard format).
func mockDashScopeResponsesStreamingUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		events := []string{
			"id:1\nevent:response.created:HTTP_STATUS/200\ndata:{\"type\":\"response.created\",\"response\":{\"id\":\"resp_ds\",\"object\":\"response\",\"output\":[],\"status\":\"queued\"}}\n",
			"id:2\nevent:response.output_item.added:HTTP_STATUS/200\ndata:{\"type\":\"response.output_item.added\",\"item\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"type\":\"message\",\"content\":[]}}\n",
			"id:3\nevent:response.content_part.added:HTTP_STATUS/200\ndata:{\"type\":\"response.content_part.added\",\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n",
			"id:4\nevent:response.output_text.delta:HTTP_STATUS/200\ndata:{\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n",
			"id:5\nevent:response.output_text.delta:HTTP_STATUS/200\ndata:{\"type\":\"response.output_text.delta\",\"delta\":\" world\"}\n",
			"id:6\nevent:response.completed:HTTP_STATUS/200\ndata:{\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ds\",\"object\":\"response\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hello world\"}]}],\"usage\":{\"input_tokens\":5,\"output_tokens\":2,\"total_tokens\":7}}}\n",
		}
		for _, e := range events {
			fmt.Fprint(w, e+"\n")
			flusher.Flush()
		}
	}))
}

// mockOpenRouterResponsesStreamingUpstream creates a mock upstream that returns
// OpenRouter-style Response API SSE streaming with reasoning/thinking events.
func mockOpenRouterResponsesStreamingUpstream() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)

		lines := []string{
			"event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_or\",\"object\":\"response\",\"output\":[],\"status\":\"in_progress\"}}\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[]}}\n",
			"event: response.reasoning_text.delta\ndata: {\"type\":\"response.reasoning_text.delta\",\"item_id\":\"rs_1\",\"output_index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Thinking\"}}\n",
			"event: response.reasoning_text.delta\ndata: {\"type\":\"response.reasoning_text.delta\",\"item_id\":\"rs_1\",\"output_index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" about it\"}}\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":0,\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"summary\":[]}}\n",
			"event: response.output_item.added\ndata: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n",
			"event: response.content_part.added\ndata: {\"type\":\"response.content_part.added\",\"output_index\":1,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"\"}}\n",
			"event: response.content_part.delta\ndata: {\"type\":\"response.content_part.delta\",\"output_index\":1,\"content_index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi there!\"}}\n",
			"event: response.content_part.done\ndata: {\"type\":\"response.content_part.done\",\"output_index\":1,\"content_index\":0,\"part\":{\"type\":\"output_text\",\"text\":\"Hi there!\"}}\n",
			"event: response.output_item.done\ndata: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hi there!\"}]}}\n",
			"event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_or\",\"object\":\"response\",\"output\":[{\"id\":\"rs_1\",\"type\":\"reasoning\"},{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"Hi there!\"}]}],\"usage\":{\"input_tokens\":10,\"output_tokens\":8,\"total_tokens\":18}}}\n",
		}
		for _, l := range lines {
			fmt.Fprint(w, l+"\n")
			flusher.Flush()
		}
	}))
}

// mockInspectingUpstream creates a mock upstream that captures the request for inspection.
type capturedRequest struct {
	Method  string
	Path    string
	Headers http.Header
	Body    []byte
}

func mockInspectingUpstream(content string) (*httptest.Server, *capturedRequest) {
	captured := &capturedRequest{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.Method = r.Method
		captured.Path = r.URL.Path
		captured.Headers = r.Header.Clone()
		captured.Body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"id":      "chatcmpl-test",
			"object":  "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": content}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 5},
		})
	}))
	return srv, captured
}
