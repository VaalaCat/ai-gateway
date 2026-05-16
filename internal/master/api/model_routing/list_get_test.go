package model_routing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
			JWTSecret: "test-secret",
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := master.New(cfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	if err := srv.InitAdminUser("admin", "admin123"); err != nil {
		t.Fatalf("init admin: %v", err)
	}
	return srv
}

func loginAdmin(t *testing.T, srv *master.Server) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
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

// seedChannel 创建一个 channel，使后续创建 routing 时 HasModel 校验能通过。
func seedChannel(t *testing.T, srv *master.Server, jwt, modelCSV string) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":     "ch-" + modelCSV,
		"type":     1,
		"key":      "sk-x",
		"base_url": "http://x",
		"models":   modelCSV,
		"status":   1,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code >= 300 {
		t.Fatalf("seed channel: %d %s", w.Code, w.Body.String())
	}
}

func createRouting(t *testing.T, srv *master.Server, jwt string, body map[string]any) int {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/model-routings", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code >= 300 {
		t.Fatalf("create routing failed: %d %s", w.Code, w.Body.String())
	}
	var resp models.ModelRouting
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return int(resp.ID)
}

func listRoutings(t *testing.T, srv *master.Server, jwt, query string) []models.ModelRouting {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/admin/model-routings"+query, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Data []models.ModelRouting `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp.Data
}

func TestList_DefaultFilter(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	// 创建：global + admin 自己的 user-scope(user_id=1) + 别人 user_id=42 的 user-scope
	createRouting(t, srv, jwt, map[string]any{
		"name": "g1", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	createRouting(t, srv, jwt, map[string]any{
		"name": "u-self", "scope": "user", "user_id": 1, "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	createRouting(t, srv, jwt, map[string]any{
		"name": "u-other", "scope": "user", "user_id": 42, "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	// 默认筛选：应该看到 g1 + u-self，不看到 u-other
	list := listRoutings(t, srv, jwt, "")
	names := make(map[string]bool)
	for _, r := range list {
		names[r.Name] = true
	}
	if !names["g1"] || !names["u-self"] {
		t.Errorf("default filter should include g1 + u-self, got %v", names)
	}
	if names["u-other"] {
		t.Errorf("default filter should NOT include other user's routings")
	}
}

func TestList_ExplicitUserID(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")
	createRouting(t, srv, jwt, map[string]any{
		"name": "u42", "scope": "user", "user_id": 42, "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	list := listRoutings(t, srv, jwt, "?user_id=42")
	if len(list) != 1 || list[0].Name != "u42" {
		t.Errorf("explicit user_id=42 should return only u42, got %d items", len(list))
	}
}

func TestList_ScopeGlobalOnly(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")
	createRouting(t, srv, jwt, map[string]any{
		"name": "g1", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	createRouting(t, srv, jwt, map[string]any{
		"name": "us1", "scope": "user", "user_id": 1, "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	list := listRoutings(t, srv, jwt, "?scope=global")
	if len(list) != 1 || list[0].Name != "g1" {
		t.Errorf("scope=global should return only g1, got %d items: %v", len(list), list)
	}
}

func TestList_QSearch(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")
	createRouting(t, srv, jwt, map[string]any{
		"name": "smart-large", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	createRouting(t, srv, jwt, map[string]any{
		"name": "cheap-pool", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	list := listRoutings(t, srv, jwt, "?q=smart")
	if len(list) != 1 || list[0].Name != "smart-large" {
		t.Errorf("q=smart should return only smart-large, got %v", list)
	}
}

func TestGet_Found(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")
	id := createRouting(t, srv, jwt, map[string]any{
		"name": "x", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Name           string   `json:"name"`
		ExpandedModels []string `json:"expanded_models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.Name != "x" {
		t.Errorf("name mismatch: %s", resp.Name)
	}
	if len(resp.ExpandedModels) != 1 || resp.ExpandedModels[0] != "gpt-4o" {
		t.Errorf("expanded_models = %v, want [gpt-4o]", resp.ExpandedModels)
	}
}

func TestGet_404(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/admin/model-routings/9999", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
