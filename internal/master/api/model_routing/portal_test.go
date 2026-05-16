package model_routing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
)

// enableRegistration 通过 admin API 开启用户注册（默认关闭）。
func enableRegistration(t *testing.T, srv *master.Server, adminJWT string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"settings": map[string]any{"registration_enabled": "true"},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", "/api/admin/system/settings", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	srv.Router.ServeHTTP(w, req)
	if w.Code >= 300 {
		t.Fatalf("enable registration: %d %s", w.Code, w.Body.String())
	}
}

// registerAndLoginUser 注册普通用户并登录，返回 (userID, jwt)。
func registerAndLoginUser(t *testing.T, srv *master.Server, username, password string) (uint, string) {
	t.Helper()
	// 注册
	body, _ := json.Marshal(map[string]any{"username": username, "email": username + "@test.example.com", "password": password})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("register %s: %d %s", username, w.Code, w.Body.String())
	}

	// 登录
	body, _ = json.Marshal(map[string]any{"username": username, "password": password})
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login %s: %d %s", username, w.Code, w.Body.String())
	}
	var loginResp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwt, _ := loginResp["token"].(string)

	// 获取 profile 拿 user_id
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/api/profile", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var pr map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &pr)
	var uid uint
	switch v := pr["id"].(type) {
	case float64:
		uid = uint(v)
	case int:
		uid = uint(v)
	}
	return uid, jwt
}

// createPortalRouting 通过 portal API 创建 routing，返回 (id, recorder)。
func createPortalRouting(t *testing.T, srv *master.Server, jwt string, body map[string]any) (int, *httptest.ResponseRecorder) {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/model-routings", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	var id int
	if v, ok := resp["id"].(float64); ok {
		id = int(v)
	}
	return id, w
}

// TestPortal_CreateForcesUserScope 验证即使请求 scope=global，portal 也强制为 user。
func TestPortal_CreateForcesUserScope(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	_, userJWT := registerAndLoginUser(t, srv, "alice", "password123")

	id, w := createPortalRouting(t, srv, userJWT, map[string]any{
		"name":    "my",
		"scope":   "global", // 故意写 global，应被强制改为 user
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
		"enabled": true,
	})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if id == 0 {
		t.Fatalf("expected non-zero id, body: %s", w.Body.String())
	}

	// 用 admin 直查验证 scope
	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	srv.Router.ServeHTTP(w2, req)
	var got map[string]any
	_ = json.Unmarshal(w2.Body.Bytes(), &got)
	if got["scope"] != "user" {
		t.Errorf("scope should be forced to 'user', got %v", got["scope"])
	}
}

// TestPortal_CrossUserGetBlocked 验证 bob 无法访问 alice 的 routing（返回 404）。
func TestPortal_CrossUserGetBlocked(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	_, aliceJWT := registerAndLoginUser(t, srv, "alice2", "password123")
	_, bobJWT := registerAndLoginUser(t, srv, "bob2", "password123")

	// alice 创建一条 routing
	id, w := createPortalRouting(t, srv, aliceJWT, map[string]any{
		"name":    "alices",
		"scope":   "user",
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
		"enabled": true,
	})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("alice create: %d %s", w.Code, w.Body.String())
	}
	if id == 0 {
		t.Fatalf("expected id > 0")
	}

	// bob 试访问 alice 的 routing，应返回 404
	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+bobJWT)
	srv.Router.ServeHTTP(w2, req)
	if w2.Code != 404 {
		t.Errorf("cross-user get should be 404, got %d: %s", w2.Code, w2.Body.String())
	}
}

// TestPortal_CannotChangeScope 验证通过 PUT 无法把 scope 改为 global。
func TestPortal_CannotChangeScope(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o")
	_, userJWT := registerAndLoginUser(t, srv, "charlie", "password123")

	id, w := createPortalRouting(t, srv, userJWT, map[string]any{
		"name":    "x",
		"scope":   "user",
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
		"enabled": true,
	})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	if id == 0 {
		t.Fatal("expected id")
	}

	// 试图通过 PUT 把 scope 改成 global，同时改 remark
	body, _ := json.Marshal(map[string]any{"scope": "global", "remark": "hacked"})
	w2 := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/model-routings/%d", id), bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+userJWT)
	srv.Router.ServeHTTP(w2, req)
	if w2.Code != 200 {
		t.Fatalf("update: %d %s", w2.Code, w2.Body.String())
	}

	// admin 验证 scope 仍是 user，remark 已更新
	w3 := httptest.NewRecorder()
	req, _ = http.NewRequest("GET", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	srv.Router.ServeHTTP(w3, req)
	var got map[string]any
	_ = json.Unmarshal(w3.Body.Bytes(), &got)
	if got["scope"] != "user" {
		t.Errorf("scope should still be 'user' after attempted change, got %v", got["scope"])
	}
	if got["remark"] != "hacked" {
		t.Errorf("remark should be updated, got %v", got["remark"])
	}
}

// createGlobalRouting 通过 admin API 创建 global scope routing。
func createGlobalRouting(t *testing.T, srv *master.Server, adminJWT, name string, members []map[string]any, enabled bool) {
	t.Helper()
	body := map[string]any{
		"name":    name,
		"scope":   "global",
		"members": members,
		"enabled": enabled,
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "/api/admin/model-routings", bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer "+adminJWT)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create global routing %q: %d %s", name, w.Code, w.Body.String())
	}
}

// TestPortalGlobalRoutingNames_OnlyEnabledGlobals 验证新端点只返回 enabled global routing 名。
func TestPortalGlobalRoutingNames_OnlyEnabledGlobals(t *testing.T) {
	srv := setupTestMaster(t)
	adminJWT := loginAdmin(t, srv)
	enableRegistration(t, srv, adminJWT)
	seedChannel(t, srv, adminJWT, "gpt-4o,deepseek-v3")
	_, userJWT := registerAndLoginUser(t, srv, "ned", "password123")

	// 通过 admin API 造数据：两个 enabled global、一个 disabled global、一个 enabled user-scope
	createGlobalRouting(t, srv, adminJWT, "fast", []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}}, true)
	createGlobalRouting(t, srv, adminJWT, "premium", []map[string]any{{"ref": "deepseek-v3", "priority": 0, "weight": 1}}, true)
	createGlobalRouting(t, srv, adminJWT, "legacy", []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}}, false)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/model-routings/global-routing-names", nil)
	req.Header.Set("Authorization", "Bearer "+userJWT)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("global-routing-names: %d %s", w.Code, w.Body.String())
	}

	var resp struct {
		Names []string `json:"names"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []string{"fast", "premium"} // legacy 被 disabled 排除；按字母升序
	if len(resp.Names) != len(want) {
		t.Fatalf("expected %d names, got %v", len(want), resp.Names)
	}
	for i, n := range want {
		if resp.Names[i] != n {
			t.Errorf("idx %d: want %q got %q (full=%v)", i, n, resp.Names[i], resp.Names)
		}
	}
}
