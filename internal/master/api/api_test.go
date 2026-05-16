package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

func setupTestMaster(t *testing.T) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
			DBPath:    ":memory:",
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := master.New(cfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	return srv
}

func loginAsAdmin(t *testing.T, srv *master.Server, user, pwd string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": user, "password": pwd})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["token"]
}

func createChannelE2E(t *testing.T, srv *master.Server, jwt, name, baseURL, modelsCSV string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":     name,
		"type":     1,
		"key":      "sk-fake",
		"base_url": baseURL,
		"models":   modelsCSV,
		"status":   1,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return fmt.Sprintf("%v", resp["id"])
}

func TestFullAPIFlow(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Login
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwtToken := loginResp["token"]
	if jwtToken == "" {
		t.Fatal("no token in login response")
	}

	// Helper to make authenticated requests
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	// Create user
	w2 := doReq("POST", "/api/admin/users", map[string]any{"username": "user1", "password": "pass", "role": 1})
	if w2.Code != 201 {
		t.Fatalf("create user: %d %s", w2.Code, w2.Body.String())
	}

	// List users
	w3 := doReq("GET", "/api/admin/users", nil)
	if w3.Code != 200 {
		t.Fatalf("list users: %d", w3.Code)
	}

	// Create token (via new /api/tokens path)
	w4 := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "test-token"})
	if w4.Code != 201 {
		t.Fatalf("create token: %d %s", w4.Code, w4.Body.String())
	}
	var createdToken map[string]any
	json.Unmarshal(w4.Body.Bytes(), &createdToken)
	generatedKey, _ := createdToken["key"].(string)
	if generatedKey == "" {
		t.Fatal("expected generated token key")
	}

	// Create token with custom key
	const customTokenKey = "sk-custom-fixed-key"
	w4c := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "custom-token", "key": customTokenKey})
	if w4c.Code != 201 {
		t.Fatalf("create token with custom key: %d %s", w4c.Code, w4c.Body.String())
	}
	var createdCustomToken map[string]any
	json.Unmarshal(w4c.Body.Bytes(), &createdCustomToken)
	if got, _ := createdCustomToken["key"].(string); got != customTokenKey {
		t.Fatalf("expected custom key %q, got %q", customTokenKey, got)
	}

	// Duplicate custom key should conflict
	w4d := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "dup-token", "key": customTokenKey})
	if w4d.Code != 409 {
		t.Fatalf("duplicate custom key: expected 409, got %d %s", w4d.Code, w4d.Body.String())
	}

	// Create channel
	w5 := doReq("POST", "/api/admin/channels", map[string]any{"name": "openai-1", "type": 1, "key": "sk-xxx", "base_url": "https://api.openai.com", "models": "gpt-4o"})
	if w5.Code != 201 {
		t.Fatalf("create channel: %d %s", w5.Code, w5.Body.String())
	}

	// Create model config
	w6 := doReq("POST", "/api/admin/models", map[string]any{"model_name": "gpt-4o", "input_price": 2.5, "output_price": 10.0})
	if w6.Code != 201 {
		t.Fatalf("create model: %d %s", w6.Code, w6.Body.String())
	}

	// === Full CRUD: Users ===
	// Get user
	w_ug := doReq("GET", "/api/admin/users/2", nil) // user1 is ID 2 (admin is 1)
	if w_ug.Code != 200 {
		t.Fatalf("get user: %d %s", w_ug.Code, w_ug.Body.String())
	}

	// Update user
	w_uu := doReq("PUT", "/api/admin/users/2", map[string]any{"status": 0})
	if w_uu.Code != 200 {
		t.Fatalf("update user: %d %s", w_uu.Code, w_uu.Body.String())
	}

	// User update with illegal status=2 must be rejected
	w_usi := doReq("PUT", "/api/admin/users/2", map[string]any{"status": 2})
	if w_usi.Code != 400 {
		t.Fatalf("expected 400 for invalid user status=2, got %d: %s", w_usi.Code, w_usi.Body.String())
	}

	// Update quota
	w_uq := doReq("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 5000})
	if w_uq.Code != 200 {
		t.Fatalf("update quota: %d %s", w_uq.Code, w_uq.Body.String())
	}

	// === Full CRUD: Tokens ===
	// Get token (via new path)
	w_tg := doReq("GET", "/api/tokens/1", nil)
	if w_tg.Code != 200 {
		t.Fatalf("get token: %d %s", w_tg.Code, w_tg.Body.String())
	}

	// List tokens (via new path)
	w_tl := doReq("GET", "/api/tokens", nil)
	if w_tl.Code != 200 {
		t.Fatalf("list tokens: %d", w_tl.Code)
	}

	// Update token (via new path)
	w_tu := doReq("PUT", "/api/tokens/1", map[string]any{"name": "updated-token"})
	if w_tu.Code != 200 {
		t.Fatalf("update token: %d %s", w_tu.Code, w_tu.Body.String())
	}

	// Update token should ignore key changes (immutable)
	w_tuk := doReq("PUT", "/api/tokens/1", map[string]any{"key": "sk-should-not-change"})
	if w_tuk.Code != 200 {
		t.Fatalf("update token with key: %d %s", w_tuk.Code, w_tuk.Body.String())
	}
	w_tg2 := doReq("GET", "/api/tokens/1", nil)
	if w_tg2.Code != 200 {
		t.Fatalf("get token after key update attempt: %d %s", w_tg2.Code, w_tg2.Body.String())
	}
	var updatedToken map[string]any
	json.Unmarshal(w_tg2.Body.Bytes(), &updatedToken)
	if got, _ := updatedToken["key"].(string); got != generatedKey {
		t.Fatalf("token key should remain immutable, want %q, got %q", generatedKey, got)
	}

	// === Full CRUD: Channels ===
	// Get channel
	w_cg := doReq("GET", "/api/admin/channels/1", nil)
	if w_cg.Code != 200 {
		t.Fatalf("get channel: %d %s", w_cg.Code, w_cg.Body.String())
	}

	// List channels
	w_cl := doReq("GET", "/api/admin/channels", nil)
	if w_cl.Code != 200 {
		t.Fatalf("list channels: %d", w_cl.Code)
	}

	// Update channel
	w_cu := doReq("PUT", "/api/admin/channels/1", map[string]any{"name": "updated-channel"})
	if w_cu.Code != 200 {
		t.Fatalf("update channel: %d %s", w_cu.Code, w_cu.Body.String())
	}

	// Channel update with illegal status=2 must be rejected
	w_csi := doReq("PUT", "/api/admin/channels/1", map[string]any{"status": 2})
	if w_csi.Code != 400 {
		t.Fatalf("expected 400 for invalid channel status=2, got %d: %s", w_csi.Code, w_csi.Body.String())
	}

	// === Full CRUD: Models ===
	// Get model
	w_mg := doReq("GET", "/api/admin/models/1", nil)
	if w_mg.Code != 200 {
		t.Fatalf("get model: %d %s", w_mg.Code, w_mg.Body.String())
	}

	// List models
	w_ml := doReq("GET", "/api/admin/models", nil)
	if w_ml.Code != 200 {
		t.Fatalf("list models: %d", w_ml.Code)
	}

	// Update model
	w_mu := doReq("PUT", "/api/admin/models/1", map[string]any{"input_price": 3.0})
	if w_mu.Code != 200 {
		t.Fatalf("update model: %d %s", w_mu.Code, w_mu.Body.String())
	}

	// === Agents CRUD ===
	// Create agent
	w_ac := doReq("POST", "/api/admin/agents", map[string]any{"name": "manual-agent"})
	if w_ac.Code != 201 {
		t.Fatalf("create agent: %d %s", w_ac.Code, w_ac.Body.String())
	}

	// List agents
	w_al := doReq("GET", "/api/admin/agents", nil)
	if w_al.Code != 200 {
		t.Fatalf("list agents: %d", w_al.Code)
	}

	// Get agent
	w_ag := doReq("GET", "/api/admin/agents/1", nil)
	if w_ag.Code != 200 {
		t.Fatalf("get agent: %d %s", w_ag.Code, w_ag.Body.String())
	}

	// Update agent
	w_au := doReq("PUT", "/api/admin/agents/1", map[string]any{"name": "renamed-agent"})
	if w_au.Code != 200 {
		t.Fatalf("update agent: %d %s", w_au.Code, w_au.Body.String())
	}

	// Agent update with illegal status=2 must be rejected
	w_asi := doReq("PUT", "/api/admin/agents/1", map[string]any{"status": 2})
	if w_asi.Code != 400 {
		t.Fatalf("expected 400 for invalid agent status=2, got %d: %s", w_asi.Code, w_asi.Body.String())
	}

	// === Stats & Logs (new paths) ===
	w_stats := doReq("GET", "/api/stats/overview", nil)
	if w_stats.Code != 200 {
		t.Fatalf("stats: %d", w_stats.Code)
	}
	var statsResp map[string]any
	json.Unmarshal(w_stats.Body.Bytes(), &statsResp)
	if _, ok := statsResp["users"]; !ok {
		t.Error("stats missing users field")
	}

	w_logs := doReq("GET", "/api/logs", nil)
	if w_logs.Code != 200 {
		t.Fatalf("logs: %d", w_logs.Code)
	}

	// Logs with filters
	w_logs2 := doReq("GET", "/api/logs?user_id=1&model_name=gpt-4o", nil)
	if w_logs2.Code != 200 {
		t.Fatalf("logs with filters: %d", w_logs2.Code)
	}

	// Trend endpoint
	w_trend := doReq("GET", "/api/stats/trend", nil)
	if w_trend.Code != 200 {
		t.Fatalf("trend: %d %s", w_trend.Code, w_trend.Body.String())
	}

	// === Backward compatible aliases (deprecated admin paths) ===
	w_compat_tokens := doReq("GET", "/api/admin/tokens", nil)
	if w_compat_tokens.Code != 200 {
		t.Fatalf("backward compat tokens: %d", w_compat_tokens.Code)
	}

	w_compat_logs := doReq("GET", "/api/admin/logs", nil)
	if w_compat_logs.Code != 200 {
		t.Fatalf("backward compat logs: %d", w_compat_logs.Code)
	}

	w_compat_stats := doReq("GET", "/api/admin/stats", nil)
	if w_compat_stats.Code != 200 {
		t.Fatalf("backward compat stats: %d", w_compat_stats.Code)
	}

	// === Delete operations (do these last) ===
	// Delete model
	w_md := doReq("DELETE", "/api/admin/models/1", nil)
	if w_md.Code != 200 {
		t.Fatalf("delete model: %d %s", w_md.Code, w_md.Body.String())
	}

	// Delete channel
	w_cd := doReq("DELETE", "/api/admin/channels/1", nil)
	if w_cd.Code != 200 {
		t.Fatalf("delete channel: %d %s", w_cd.Code, w_cd.Body.String())
	}

	// Delete token (via new path)
	w_td := doReq("DELETE", "/api/tokens/1", nil)
	if w_td.Code != 200 {
		t.Fatalf("delete token: %d %s", w_td.Code, w_td.Body.String())
	}

	// Delete custom token (via backward-compat path)
	w_tdc := doReq("DELETE", "/api/admin/tokens/2", nil)
	if w_tdc.Code != 200 {
		t.Fatalf("delete custom token: %d %s", w_tdc.Code, w_tdc.Body.String())
	}

	// Delete user
	w_ud := doReq("DELETE", "/api/admin/users/2", nil)
	if w_ud.Code != 200 {
		t.Fatalf("delete user: %d %s", w_ud.Code, w_ud.Body.String())
	}

	// Delete agent
	w_ad := doReq("DELETE", "/api/admin/agents/1", nil)
	if w_ad.Code != 200 {
		t.Fatalf("delete agent: %d %s", w_ad.Code, w_ad.Body.String())
	}

	// === Get non-existent resources (404) ===
	if w := doReq("GET", "/api/admin/users/999", nil); w.Code != 404 {
		t.Errorf("get missing user: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/tokens/999", nil); w.Code != 404 {
		t.Errorf("get missing token: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/channels/999", nil); w.Code != 404 {
		t.Errorf("get missing channel: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/models/999", nil); w.Code != 404 {
		t.Errorf("get missing model: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/agents/999", nil); w.Code != 404 {
		t.Errorf("get missing agent: expected 404, got %d", w.Code)
	}

	// Test unauthorized access (no token)
	w7 := httptest.NewRecorder()
	req7, _ := http.NewRequest("GET", "/api/admin/users", nil)
	srv.Router.ServeHTTP(w7, req7)
	if w7.Code != 401 {
		t.Fatalf("expected 401 for no token, got %d", w7.Code)
	}

	// Test wrong login password
	wrongLogin, _ := json.Marshal(map[string]any{"username": "admin", "password": "wrong"})
	w_wl := httptest.NewRecorder()
	req_wl, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(wrongLogin))
	req_wl.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w_wl, req_wl)
	if w_wl.Code != 401 {
		t.Errorf("wrong password: expected 401, got %d", w_wl.Code)
	}

	// Generate enrollment token (admin endpoint)
	w8a := doReq("POST", "/api/admin/agents/enrollment-token", map[string]any{"ttl": 300})
	if w8a.Code != 200 {
		t.Fatalf("generate enrollment token: %d %s", w8a.Code, w8a.Body.String())
	}
	var enrollTokenResp map[string]any
	json.Unmarshal(w8a.Body.Bytes(), &enrollTokenResp)
	enrollToken := enrollTokenResp["enrollment_token"].(string)

	// Test agent enrollment (public endpoint)
	enrollBody, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "test-agent"})
	w8 := httptest.NewRecorder()
	req8, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody))
	req8.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8, req8)
	if w8.Code != 201 {
		t.Fatalf("enroll agent: %d %s", w8.Code, w8.Body.String())
	}

	// Test enrollment with invalid token (should fail)
	enrollBody2, _ := json.Marshal(map[string]any{"enrollment_token": "invalid-token"})
	w8b := httptest.NewRecorder()
	req8b, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody2))
	req8b.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8b, req8b)
	if w8b.Code != 401 {
		t.Fatalf("expected 401 for invalid token, got %d %s", w8b.Code, w8b.Body.String())
	}

	// Test enrollment with same unexpired token (should succeed)
	enrollBody3, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "test-agent-2"})
	w8c := httptest.NewRecorder()
	req8c, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody3))
	req8c.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8c, req8c)
	if w8c.Code != 201 {
		t.Fatalf("expected 201 for reused unexpired token, got %d %s", w8c.Code, w8c.Body.String())
	}
}

func TestChannelTypesCatalog(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Login
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwtToken := loginResp["token"]
	if jwtToken == "" {
		t.Fatal("no token in login response")
	}

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/admin/channels/types", nil)
	req2.Header.Set("Authorization", "Bearer "+jwtToken)
	srv.Router.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("list channel types: %d %s", w2.Code, w2.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &items); err != nil {
		t.Fatalf("parse channel types response: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("channel types should not be empty")
	}

	lastID := -1
	for _, item := range items {
		id, ok := item["id"].(float64)
		if !ok {
			t.Fatalf("channel type id is missing or invalid: %#v", item)
		}
		if int(id) < lastID {
			t.Fatalf("channel types should be sorted by id ascending, got %d after %d", int(id), lastID)
		}
		lastID = int(id)
		if name, _ := item["name"].(string); name == "" {
			t.Fatalf("channel type name is required: %#v", item)
		}
		if key, _ := item["i18n_key"].(string); key == "" {
			t.Fatalf("channel type i18n_key is required: %#v", item)
		}
	}
}

func TestUserPermissions(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Helper for login
	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]any{"username": username, "password": password})
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
		}
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp["token"]
	}

	// Helper for authenticated requests with a specific token
	doReqWith := func(jwtToken, method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+jwtToken)
		srv.Router.ServeHTTP(w, r)
		return w
	}

	adminToken := login("admin", "admin123")

	// Create a normal user
	w := doReqWith(adminToken, "POST", "/api/admin/users", map[string]any{
		"username": "normaluser", "password": "pass123", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create normal user: %d %s", w.Code, w.Body.String())
	}
	var normalUserResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &normalUserResp)
	normalUserID := uint(normalUserResp["id"].(float64))

	// Set quota for normal user
	w = doReqWith(adminToken, "PUT", "/api/admin/users/2/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	// Re-enable user (was set to status 2 in other test? no, fresh DB)
	userToken := login("normaluser", "pass123")

	// Create a token template (admin only)
	w = doReqWith(adminToken, "POST", "/api/admin/token-templates", map[string]any{
		"name": "default-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	var tplResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &tplResp)
	templateID := uint(tplResp["id"].(float64))

	// Admin creates token freely (no template needed)
	w = doReqWith(adminToken, "POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-token",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token: %d %s", w.Code, w.Body.String())
	}
	var adminTokenResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &adminTokenResp)
	adminCreatedTokenID := int(adminTokenResp["id"].(float64))

	// Normal user creates token without template (should fail)
	w = doReqWith(userToken, "POST", "/api/tokens", map[string]any{"name": "my-token"})
	if w.Code != 400 {
		t.Fatalf("user create token without template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with template (should succeed)
	w = doReqWith(userToken, "POST", "/api/tokens", map[string]any{
		"name": "my-token", "template_id": templateID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with template: %d %s", w.Code, w.Body.String())
	}
	var userTokenResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &userTokenResp)
	userCreatedTokenID := int(userTokenResp["id"].(float64))
	// Verify template-inherited fields
	if uid := uint(userTokenResp["user_id"].(float64)); uid != normalUserID {
		t.Fatalf("expected user_id %d, got %d", normalUserID, uid)
	}
	if models, _ := userTokenResp["models"].(string); models != `["gpt-4o"]` {
		t.Fatalf("expected models from template, got %q", models)
	}

	// Normal user listing only shows own tokens
	w = doReqWith(userToken, "GET", "/api/tokens", nil)
	if w.Code != 200 {
		t.Fatalf("user list tokens: %d", w.Code)
	}
	var listResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &listResp)
	tokenList, _ := listResp["data"].([]any)
	for _, tok := range tokenList {
		tokMap := tok.(map[string]any)
		if uid := uint(tokMap["user_id"].(float64)); uid != normalUserID {
			t.Fatalf("user listing shows token with user_id=%d, expected %d", uid, normalUserID)
		}
	}

	// Normal user PUT on admin's token returns 404
	w = doReqWith(userToken, "PUT", "/api/tokens/"+itoa(adminCreatedTokenID), map[string]any{"name": "hacked"})
	if w.Code != 404 {
		t.Fatalf("user update other's token: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Normal user DELETE on admin's token returns 404
	w = doReqWith(userToken, "DELETE", "/api/tokens/"+itoa(adminCreatedTokenID), nil)
	if w.Code != 404 {
		t.Fatalf("user delete other's token: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Normal user can update own token (name only)
	w = doReqWith(userToken, "PUT", "/api/tokens/"+itoa(userCreatedTokenID), map[string]any{"name": "renamed"})
	if w.Code != 200 {
		t.Fatalf("user update own token: %d %s", w.Code, w.Body.String())
	}

	// Normal user gets 403 on admin-only routes
	w = doReqWith(userToken, "GET", "/api/admin/users", nil)
	if w.Code != 403 {
		t.Fatalf("user access admin route: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Stats/overview for normal user returns quota fields
	w = doReqWith(userToken, "GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("user stats overview: %d %s", w.Code, w.Body.String())
	}
	var userStats map[string]any
	json.Unmarshal(w.Body.Bytes(), &userStats)
	if _, ok := userStats["quota"]; !ok {
		t.Error("user stats missing quota field")
	}
	if _, ok := userStats["used_quota"]; !ok {
		t.Error("user stats missing used_quota field")
	}
	// Admin-only fields should be absent
	if _, ok := userStats["users"]; ok {
		t.Error("user stats should not have users field")
	}

	// Normal user can delete own token
	w = doReqWith(userToken, "DELETE", "/api/tokens/"+itoa(userCreatedTokenID), nil)
	if w.Code != 200 {
		t.Fatalf("user delete own token: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistration(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Helper for unauthenticated requests
	doPublic := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, r)
		return w
	}

	// 1. Registration status returns false by default
	w := doPublic("GET", "/api/system/registration-status", nil)
	if w.Code != 200 {
		t.Fatalf("registration status: %d %s", w.Code, w.Body.String())
	}
	var statusResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &statusResp)
	if statusResp["registration_enabled"] != false {
		t.Fatalf("expected registration_enabled=false, got %v", statusResp["registration_enabled"])
	}

	// 2. Register returns 403 when disabled
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 403 {
		t.Fatalf("register when disabled: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// 3. Admin enables registration
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	wl := httptest.NewRecorder()
	rl, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	rl.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(wl, rl)
	if wl.Code != 200 {
		t.Fatalf("admin login: %d %s", wl.Code, wl.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(wl.Body.Bytes(), &loginResp)
	adminToken := loginResp["token"]

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+adminToken)
		srv.Router.ServeHTTP(w, r)
		return w
	}

	w = doAdmin("PUT", "/api/admin/system/settings", map[string]any{"settings": map[string]any{"registration_enabled": "true"}})
	if w.Code != 200 {
		t.Fatalf("enable registration: %d %s", w.Code, w.Body.String())
	}

	// 4. Register succeeds with 201
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 201 {
		t.Fatalf("register: expected 201, got %d %s", w.Code, w.Body.String())
	}

	// 5. Duplicate username returns 409
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 409 {
		t.Fatalf("duplicate register: expected 409, got %d %s", w.Code, w.Body.String())
	}

	// 6. Invalid username returns 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "bad user!", "email": "baduser@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("invalid username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// 7. New user can login
	w = doPublic("POST", "/api/login", map[string]any{"username": "newuser", "password": "password123"})
	if w.Code != 200 {
		t.Fatalf("new user login: %d %s", w.Code, w.Body.String())
	}
	var newLoginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &newLoginResp)
	if newLoginResp["token"] == "" {
		t.Fatal("new user login: no token returned")
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// loginHelper logs in and returns the JWT token.
func loginHelper(t *testing.T, srv *master.Server, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": username, "password": password})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	tok := resp["token"]
	if tok == "" {
		t.Fatalf("login %s: no token in response", username)
	}
	return tok
}

// reqHelper makes an authenticated HTTP request and returns the recorder.
func reqHelper(srv *master.Server, jwtToken, method, path string, body any) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if jwtToken != "" {
		r.Header.Set("Authorization", "Bearer "+jwtToken)
	}
	srv.Router.ServeHTTP(w, r)
	return w
}

// jsonBody parses response body into map.
func jsonBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse response body: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

func TestTokenTemplateCRUD(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create template with valid models JSON
	w := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "test-tpl", "models": `["gpt-4o","claude-.*"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: expected 201, got %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	if tpl["id"] == nil || tpl["name"] != "test-tpl" || tpl["models"] != `["gpt-4o","claude-.*"]` {
		t.Fatalf("create template response unexpected: %v", tpl)
	}
	if int(tpl["expiry_days"].(float64)) != 30 {
		t.Fatalf("expected expiry_days=30, got %v", tpl["expiry_days"])
	}
	if int(tpl["status"].(float64)) != 1 {
		t.Fatalf("expected status=1, got %v", tpl["status"])
	}
	tplID := int(tpl["id"].(float64))

	// List templates (admin sees all)
	w = doReq("GET", "/api/admin/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("list templates: %d %s", w.Code, w.Body.String())
	}
	listResp := jsonBody(t, w)
	if total := int(listResp["total"].(float64)); total < 1 {
		t.Fatalf("expected total >= 1, got %d", total)
	}

	// Update template
	w = doReq("PUT", "/api/admin/token-templates/"+itoa(tplID), map[string]any{
		"name": "updated-tpl", "expiry_days": 60,
	})
	if w.Code != 200 {
		t.Fatalf("update template: %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if updated["name"] != "updated-tpl" {
		t.Fatalf("expected name=updated-tpl, got %v", updated["name"])
	}

	// Create template with invalid regex
	w = doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "bad", "models": `["gpt-[invalid"]`,
	})
	if w.Code != 400 {
		t.Fatalf("invalid regex: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Create a second template, then disable it via update (status=0 in create defaults to 1)
	w = doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "disabled-tpl", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create disabled template: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))

	// Disable the template via update
	w = doReq("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Token template update with illegal status=2 must be rejected
	wTplInvalid := doReq("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 2})
	if wTplInvalid.Code != 400 {
		t.Fatalf("expected 400 for invalid template status=2, got %d: %s", wTplInvalid.Code, wTplInvalid.Body.String())
	}

	// Delete the disabled template
	w = doReq("DELETE", "/api/admin/token-templates/"+itoa(disabledTplID), nil)
	if w.Code != 200 {
		t.Fatalf("delete template: %d %s", w.Code, w.Body.String())
	}

	// Delete non-existent template
	w = doReq("DELETE", "/api/admin/token-templates/999", nil)
	if w.Code != 404 {
		t.Fatalf("delete non-existent: expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenTemplateAccess(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "normie", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userToken := loginHelper(t, srv, "normie", "pass1234")

	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Admin creates 2 templates: one enabled, one to be disabled
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "enabled-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create enabled template: %d %s", w.Code, w.Body.String())
	}
	enabledTpl := jsonBody(t, w)
	enabledTplID := int(enabledTpl["id"].(float64))

	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "disabled-tpl", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create second template: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))

	// Disable the second template
	w = doAdmin("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/token-templates (only enabled)
	w = doUser("GET", "/api/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("user list templates: %d %s", w.Code, w.Body.String())
	}
	userList := jsonBody(t, w)
	if total := int(userList["total"].(float64)); total != 1 {
		t.Fatalf("user should see 1 enabled template, got %d", total)
	}

	// Normal user: POST /api/admin/token-templates (create) -> 403
	w = doUser("POST", "/api/admin/token-templates", map[string]any{
		"name": "hacker-tpl", "models": `["test"]`,
	})
	if w.Code != 403 {
		t.Fatalf("user create template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Normal user: PUT /api/admin/token-templates/:id -> 403
	w = doUser("PUT", "/api/admin/token-templates/"+itoa(enabledTplID), map[string]any{"name": "hacked"})
	if w.Code != 403 {
		t.Fatalf("user update template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Normal user: DELETE /api/admin/token-templates/:id -> 403
	w = doUser("DELETE", "/api/admin/token-templates/"+itoa(enabledTplID), nil)
	if w.Code != 403 {
		t.Fatalf("user delete template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Admin: GET /api/admin/token-templates sees both
	w = doAdmin("GET", "/api/admin/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("admin list templates: %d %s", w.Code, w.Body.String())
	}
	adminList := jsonBody(t, w)
	if total := int(adminList["total"].(float64)); total != 2 {
		t.Fatalf("admin should see 2 templates, got %d", total)
	}
}

func TestTokenCreationWithTemplate(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user with quota
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "creator", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := int(userResp["id"].(float64))

	w = doAdmin("PUT", "/api/admin/users/"+itoa(normalUserID)+"/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "creator", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Create enabled template (expiry_days=30, models=["gpt-4o"])
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "30day-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	tplID := int(tpl["id"].(float64))

	// Normal user creates token with template
	beforeCreate := time.Now().Unix()
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "my-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with template: %d %s", w.Code, w.Body.String())
	}
	tok := jsonBody(t, w)

	// Verify models == template.models
	if tok["models"] != `["gpt-4o"]` {
		t.Fatalf("expected models=[\"gpt-4o\"], got %v", tok["models"])
	}

	// Verify expired_at is approximately now + 30*86400 (within 10 seconds)
	expectedExpiry := beforeCreate + 30*86400
	actualExpiry := int64(tok["expired_at"].(float64))
	if math.Abs(float64(actualExpiry-expectedExpiry)) > 10 {
		t.Fatalf("expired_at %d not within 10s of expected %d", actualExpiry, expectedExpiry)
	}

	// Verify template_id is set
	if tok["template_id"] == nil || int(tok["template_id"].(float64)) != tplID {
		t.Fatalf("expected template_id=%d, got %v", tplID, tok["template_id"])
	}

	// Verify user_id == normal user's ID
	if int(tok["user_id"].(float64)) != normalUserID {
		t.Fatalf("expected user_id=%d, got %v", normalUserID, tok["user_id"])
	}

	// Create a disabled template
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "will-disable", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create template to disable: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))
	w = doAdmin("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with disabled template -> 400
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "bad-token", "template_id": disabledTplID,
	})
	if w.Code != 400 {
		t.Fatalf("disabled template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with non-existent template_id=999 -> 400
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "bad-token2", "template_id": 999,
	})
	if w.Code != 400 {
		t.Fatalf("non-existent template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Admin creates token with template (should inherit template fields)
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-tpl-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("admin create token with template: %d %s", w.Code, w.Body.String())
	}

	// Admin creates token without template for another user
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": normalUserID, "name": "admin-token-for-user",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token without template: %d %s", w.Code, w.Body.String())
	}

	// Create template with expiry_days=-1 (never expires)
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "never-expire-tpl", "models": `["gpt-4o"]`, "expiry_days": -1,
	})
	if w.Code != 201 {
		t.Fatalf("create never-expire template: %d %s", w.Code, w.Body.String())
	}
	neverExpireTpl := jsonBody(t, w)
	neverExpireTplID := int(neverExpireTpl["id"].(float64))

	// Normal user creates token with never-expire template
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "forever-token", "template_id": neverExpireTplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with never-expire template: %d %s", w.Code, w.Body.String())
	}
	foreverTok := jsonBody(t, w)
	if int64(foreverTok["expired_at"].(float64)) != -1 {
		t.Fatalf("expected expired_at=-1, got %v", foreverTok["expired_at"])
	}
}

func TestTokenEditRestrictions(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "editor", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	w = doAdmin("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "editor", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Create template
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "edit-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	tplID := int(tpl["id"].(float64))

	// User creates token via template
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "edit-test-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token: %d %s", w.Code, w.Body.String())
	}
	tok := jsonBody(t, w)
	tokID := int(tok["id"].(float64))
	origModels := tok["models"].(string)
	origExpiredAt := int64(tok["expired_at"].(float64))
	origStatus := int(tok["status"].(float64))
	tokPath := "/api/tokens/" + itoa(tokID)

	// Normal user updates name (allowed)
	w = doUser("PUT", tokPath, map[string]any{"name": "new-name"})
	if w.Code != 200 {
		t.Fatalf("user update name: %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if updated["name"] != "new-name" {
		t.Fatalf("expected name=new-name, got %v", updated["name"])
	}

	// Normal user updates trace_enabled (allowed)
	w = doUser("PUT", tokPath, map[string]any{"trace_enabled": true})
	if w.Code != 200 {
		t.Fatalf("user update trace_enabled: %d %s", w.Code, w.Body.String())
	}
	updated = jsonBody(t, w)
	if updated["trace_enabled"] != true {
		t.Fatalf("expected trace_enabled=true, got %v", updated["trace_enabled"])
	}

	// Normal user attempts to update models (should be ignored)
	w = doUser("PUT", tokPath, map[string]any{"models": `["hacked"]`})
	if w.Code != 200 {
		t.Fatalf("user update models: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	if w.Code != 200 {
		t.Fatalf("get token: %d %s", w.Code, w.Body.String())
	}
	got := jsonBody(t, w)
	if got["models"].(string) != origModels {
		t.Fatalf("models should be unchanged: expected %q, got %q", origModels, got["models"])
	}

	// Normal user attempts to update status (should be ignored)
	w = doUser("PUT", tokPath, map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("user update status: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	got = jsonBody(t, w)
	if int(got["status"].(float64)) != origStatus {
		t.Fatalf("status should be unchanged: expected %d, got %v", origStatus, got["status"])
	}

	// Normal user attempts to update expired_at (should be ignored)
	w = doUser("PUT", tokPath, map[string]any{"expired_at": 9999999999})
	if w.Code != 200 {
		t.Fatalf("user update expired_at: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	got = jsonBody(t, w)
	if int64(got["expired_at"].(float64)) != origExpiredAt {
		t.Fatalf("expired_at should be unchanged: expected %d, got %v", origExpiredAt, got["expired_at"])
	}

	// Admin can update all fields on their own token
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-edit-token",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token: %d %s", w.Code, w.Body.String())
	}
	adminTok := jsonBody(t, w)
	adminTokID := int(adminTok["id"].(float64))
	adminTokPath := "/api/tokens/" + itoa(adminTokID)

	w = doAdmin("PUT", adminTokPath, map[string]any{
		"models": `["new-model"]`, "status": 0, "expired_at": 1234567890,
	})
	if w.Code != 200 {
		t.Fatalf("admin update all fields: %d %s", w.Code, w.Body.String())
	}
	adminUpdated := jsonBody(t, w)
	if adminUpdated["models"] != `["new-model"]` {
		t.Fatalf("admin models not updated: got %v", adminUpdated["models"])
	}
	if int(adminUpdated["status"].(float64)) != 0 {
		t.Fatalf("admin status not updated: got %v", adminUpdated["status"])
	}
	if int64(adminUpdated["expired_at"].(float64)) != 1234567890 {
		t.Fatalf("admin expired_at not updated: got %v", adminUpdated["expired_at"])
	}

	// Admin updating with illegal status value should be rejected
	w = doAdmin("PUT", adminTokPath, map[string]any{"status": 2})
	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid status=2, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAdminCanReassignRegularTokenToAnotherUser(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username, "password": "pass1234", "role": 1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	sourceUserID := createUser("token-owner-src")
	targetUserID := createUser("token-owner-dst")

	w := doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": sourceUserID, "name": "reassign-regular-token",
	})
	if w.Code != 201 {
		t.Fatalf("create regular token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": targetUserID})
	if w.Code != 200 {
		t.Fatalf("admin reassign regular token: expected 200, got %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if int(updated["user_id"].(float64)) != targetUserID {
		t.Fatalf("expected reassigned user_id=%d, got %v", targetUserID, updated["user_id"])
	}
}

func TestAdminCannotSetRegularTokenOwnerToZero(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "regular-owner", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create regular owner user: %d %s", w.Code, w.Body.String())
	}
	ownerUserID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": ownerUserID, "name": "regular-zero-owner-token",
	})
	if w.Code != 201 {
		t.Fatalf("create regular token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": 0})
	if w.Code != 400 {
		t.Fatalf("admin set regular token owner to zero: expected 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminCanSetSystemTestTokenOwnerToZero(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "system-test-owner", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create system test owner user: %d %s", w.Code, w.Body.String())
	}
	ownerUserID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": ownerUserID, "name": "__system_test__",
	})
	if w.Code != 201 {
		t.Fatalf("create __system_test__ token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": 0})
	if w.Code != 200 {
		t.Fatalf("admin set __system_test__ owner to zero: expected 200, got %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if int(updated["user_id"].(float64)) != 0 {
		t.Fatalf("expected __system_test__ user_id=0, got %v", updated["user_id"])
	}
}

func TestLogAccessControl(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "loguser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := uint(userResp["id"].(float64))

	userToken := loginHelper(t, srv, "loguser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Insert logs directly into DB
	srv.DB.Create(&models.UsageLog{
		UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4o",
		RequestID: "admin-req-1", Status: 1, PromptTokens: 100,
		CompletionTokens: 50, InputCost: 10, OutputCost: 5, TotalCost: 15,
	})
	srv.DB.Create(&models.UsageLog{
		UserID: normalUserID, TokenID: 2, ChannelID: 1, ModelName: "gpt-4o",
		RequestID: "user-req-1", Status: 1, PromptTokens: 200,
		CompletionTokens: 100, InputCost: 20, OutputCost: 10, TotalCost: 30,
	})

	// Admin: GET /api/logs - sees all logs
	w = doAdmin("GET", "/api/logs", nil)
	if w.Code != 200 {
		t.Fatalf("admin list logs: %d %s", w.Code, w.Body.String())
	}
	adminLogs := jsonBody(t, w)
	if total := int(adminLogs["total"].(float64)); total < 2 {
		t.Fatalf("admin should see >= 2 logs, got %d", total)
	}

	// Normal user: GET /api/logs - sees only own logs
	w = doUser("GET", "/api/logs", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs: %d %s", w.Code, w.Body.String())
	}
	userLogs := jsonBody(t, w)
	data, _ := userLogs["data"].([]any)
	for _, item := range data {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("user sees log with user_id=%v, expected %d", logItem["user_id"], normalUserID)
		}
		// channel_id should be hidden (0)
		if int(logItem["channel_id"].(float64)) != 0 {
			t.Fatalf("channel_id should be 0 for normal user, got %v", logItem["channel_id"])
		}
	}

	// Normal user: GET /api/logs?user_id=1 - user_id param ignored
	w = doUser("GET", "/api/logs?user_id=1", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs with user_id filter: %d %s", w.Code, w.Body.String())
	}
	filteredLogs := jsonBody(t, w)
	filteredData, _ := filteredLogs["data"].([]any)
	for _, item := range filteredData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("user_id filter should be ignored for normal user, got user_id=%v", logItem["user_id"])
		}
	}

	// Admin: GET /api/logs?user_id=2 - can filter by user
	w = doAdmin("GET", "/api/logs?user_id="+itoa(int(normalUserID)), nil)
	if w.Code != 200 {
		t.Fatalf("admin filter by user: %d %s", w.Code, w.Body.String())
	}
	adminFiltered := jsonBody(t, w)
	adminFilteredData, _ := adminFiltered["data"].([]any)
	for _, item := range adminFilteredData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("admin filter: expected user_id=%d, got %v", normalUserID, logItem["user_id"])
		}
	}

	// Normal user: GET /api/logs?channel_id=1 - channel_id ignored
	w = doUser("GET", "/api/logs?channel_id=1", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs with channel_id filter: %d %s", w.Code, w.Body.String())
	}
	// Should still return the user's own logs without error
	channelFilterLogs := jsonBody(t, w)
	channelFilterData, _ := channelFilterLogs["data"].([]any)
	for _, item := range channelFilterData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("channel_id filter should not leak other user logs, got user_id=%v", logItem["user_id"])
		}
	}
}

func TestTraceAccessControl(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "traceuser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := uint(userResp["id"].(float64))

	userToken := loginHelper(t, srv, "traceuser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Insert usage logs and traces
	srv.DB.Create(&models.UsageLog{
		UserID: 1, TokenID: 1, ChannelID: 1,
		RequestID: "admin-trace-req", Status: 1, HasTrace: true,
	})
	srv.DB.Create(&models.UsageLogTrace{
		RequestID: "admin-trace-req", InboundPath: "/v1/chat/completions",
	})

	srv.DB.Create(&models.UsageLog{
		UserID: normalUserID, TokenID: 2, ChannelID: 1,
		RequestID: "user-trace-req", Status: 1, HasTrace: true,
	})
	srv.DB.Create(&models.UsageLogTrace{
		RequestID: "user-trace-req", InboundPath: "/v1/chat/completions",
	})

	// Admin: GET /api/logs/admin-trace-req/trace - succeeds
	w = doAdmin("GET", "/api/logs/admin-trace-req/trace", nil)
	if w.Code != 200 {
		t.Fatalf("admin get own trace: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/logs/user-trace-req/trace - own trace, succeeds
	w = doUser("GET", "/api/logs/user-trace-req/trace", nil)
	if w.Code != 200 {
		t.Fatalf("user get own trace: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/logs/admin-trace-req/trace - other's trace, denied
	w = doUser("GET", "/api/logs/admin-trace-req/trace", nil)
	if w.Code != 404 {
		t.Fatalf("user get other's trace: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Non-existent trace
	w = doAdmin("GET", "/api/logs/nonexistent/trace", nil)
	if w.Code != 404 {
		t.Fatalf("non-existent trace: expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestStatsScope(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user with quota
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "statsuser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	w = doAdmin("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 50000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "statsuser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Admin: GET /api/stats/overview
	w = doAdmin("GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("admin stats overview: %d %s", w.Code, w.Body.String())
	}
	adminStats := jsonBody(t, w)
	// Should have admin-only fields
	for _, field := range []string{"users", "channels", "tokens", "usage_logs", "total_cost"} {
		if _, ok := adminStats[field]; !ok {
			t.Errorf("admin stats missing field: %s", field)
		}
	}
	// Should NOT have user-only fields
	if _, ok := adminStats["quota"]; ok {
		t.Error("admin stats should not have quota field")
	}
	if _, ok := adminStats["used_quota"]; ok {
		t.Error("admin stats should not have used_quota field")
	}

	// Normal user: GET /api/stats/overview
	w = doUser("GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("user stats overview: %d %s", w.Code, w.Body.String())
	}
	userStats := jsonBody(t, w)
	// Should have user fields
	for _, field := range []string{"tokens", "usage_logs", "total_cost", "quota", "used_quota"} {
		if _, ok := userStats[field]; !ok {
			t.Errorf("user stats missing field: %s", field)
		}
	}
	// Should NOT have admin-only fields
	for _, field := range []string{"users", "channels", "agents", "connected_agents"} {
		if _, ok := userStats[field]; ok {
			t.Errorf("user stats should not have field: %s", field)
		}
	}

	// Admin: GET /api/stats/trend
	w = doAdmin("GET", "/api/stats/trend", nil)
	if w.Code != 200 {
		t.Fatalf("admin trend: %d %s", w.Code, w.Body.String())
	}
	trendResp := jsonBody(t, w)
	if _, ok := trendResp["items"]; !ok {
		t.Error("trend response missing items")
	}

	// Normal user: GET /api/stats/trend
	w = doUser("GET", "/api/stats/trend", nil)
	if w.Code != 200 {
		t.Fatalf("user trend: %d %s", w.Code, w.Body.String())
	}
	userTrend := jsonBody(t, w)
	if _, ok := userTrend["items"]; !ok {
		t.Error("user trend response missing items")
	}

	// Trend with custom days
	w = doAdmin("GET", "/api/stats/trend?days=7", nil)
	if w.Code != 200 {
		t.Fatalf("trend with days=7: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistrationValidation(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	// Enable registration
	w := reqHelper(srv, adminToken, "PUT", "/api/admin/system/settings", map[string]any{
		"settings": map[string]any{"registration_enabled": "true"},
	})
	if w.Code != 200 {
		t.Fatalf("enable registration: %d %s", w.Code, w.Body.String())
	}

	doPublic := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, "", method, path, body)
	}

	// Short username (< 3 chars) -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "ab", "email": "ab@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("short username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Short password (< 8 chars) -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "validuser", "email": "validuser@test.example.com", "password": "short"})
	if w.Code != 400 {
		t.Fatalf("short password: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Username with special chars -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "user@name", "email": "username@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("special chars username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Empty username -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "", "email": "empty@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("empty username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Valid registration -> 201
	w = doPublic("POST", "/api/register", map[string]any{"username": "valid_user_123", "email": "valid_user_123@test.example.com", "password": "longpassword"})
	if w.Code != 201 {
		t.Fatalf("valid registration: expected 201, got %d %s", w.Code, w.Body.String())
	}

	// New user role should be 1 (normal user) - verify via admin API
	w = reqHelper(srv, adminToken, "GET", "/api/admin/users", nil)
	if w.Code != 200 {
		t.Fatalf("admin list users: %d %s", w.Code, w.Body.String())
	}
	usersResp := jsonBody(t, w)
	usersData, _ := usersResp["data"].([]any)
	found := false
	for _, u := range usersData {
		user := u.(map[string]any)
		if user["username"] == "valid_user_123" {
			found = true
			if int(user["role"].(float64)) != 1 {
				t.Fatalf("new user role should be 1, got %v", user["role"])
			}
			break
		}
	}
	if !found {
		t.Fatal("registered user not found in admin user list")
	}
}

func TestChannelTest_E2EHitsRelayNotSPA(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-e2e","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Start the master router on a real loopback listener and update the channel
	// handler's MasterListen to match. Without this the handler would have
	// MasterListen=":0" from setupTestMaster which is meaningless after the OS
	// picks a real port.
	ts := httptest.NewServer(srv.Router)
	defer ts.Close()
	tsURL, _ := url.Parse(ts.URL)
	srv.SetChannelMasterListen(":" + tsURL.Port())

	// Mount relay routes so the channel test API can reach /v1/chat/completions.
	// In production this happens inside Run() via setupEmbeddedAgent; in tests we
	// call the exported shim to avoid spinning up a real net.Listener.
	if err := srv.SetupEmbeddedAgentForTest(tsURL.Host); err != nil {
		t.Fatalf("setup embedded agent: %v", err)
	}

	jwt := loginAsAdmin(t, srv, "admin", "admin123")

	chID := createChannelE2E(t, srv, jwt, "e2e-stub", upstream.URL, "gpt-4")

	testBody, _ := json.Marshal(map[string]any{
		"model":         "gpt-4",
		"endpoint_type": "chat-completion",
		"stream":        false,
	})

	// The relay's auth middleware reads the __system_test__ token from the
	// embedded agent's cache, which is populated asynchronously via WS sync.
	// On a fast machine the first request can race ahead of cache propagation
	// and receive a 401 "invalid api key". Retry for up to 5 s, but only on
	// that specific symptom — any other failure mode aborts immediately.
	var testResp struct {
		Success    bool   `json:"success"`
		StatusCode int    `json:"status_code"`
		Response   string `json:"response"`
		Error      string `json:"error"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		httpReq, _ := http.NewRequest("POST", ts.URL+"/api/admin/channels/"+chID+"/test", bytes.NewReader(testBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+jwt)
		httpResp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("API call failed: %d %s", httpResp.StatusCode, string(body))
		}
		if err := json.Unmarshal(body, &testResp); err != nil {
			t.Fatalf("decode response: %v body=%s", err, string(body))
		}

		// Retry only while the embedded agent cache is still syncing the
		// __system_test__ token (relay returns 401 "invalid api key").
		if !testResp.Success && strings.Contains(testResp.Response, "invalid api key") && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			// Re-encode body for next iteration
			testBody, _ = json.Marshal(map[string]any{
				"model":         "gpt-4",
				"endpoint_type": "chat-completion",
				"stream":        false,
			})
			continue
		}
		break
	}

	if !testResp.Success {
		t.Fatalf("expected Success=true, got %+v", testResp)
	}
	// The relay rewrites the upstream response ID, so we check for structural
	// markers that confirm we got a real chat completion JSON payload, not SPA HTML.
	if !strings.Contains(testResp.Response, `"object":"chat.completion"`) &&
		!strings.Contains(testResp.Response, `"choices"`) {
		t.Errorf("expected chat completion JSON in Response, got %q", testResp.Response)
	}
	if strings.Contains(testResp.Response, "<!DOCTYPE html>") || strings.Contains(testResp.Response, "__next_f") {
		t.Errorf("Response looks like SPA fallback HTML: %q", testResp.Response)
	}
}
