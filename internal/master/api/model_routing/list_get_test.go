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

func seedListedRouting(t *testing.T, srv *master.Server, routing models.ModelRouting) models.ModelRouting {
	t.Helper()
	if err := srv.App.GetCoreDB().Create(&routing).Error; err != nil {
		t.Fatalf("seed listed routing: %v", err)
	}
	return routing
}

func TestModelRoutingList_DefaultFilterReturnsEveryScope(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedListedRouting(t, srv, models.ModelRouting{Name: "g1", Scope: models.RoutingScopeGlobal})
	seedListedRouting(t, srv, models.ModelRouting{Name: "u-other", Scope: models.RoutingScopeUser, UserID: 42})
	seedListedRouting(t, srv, models.ModelRouting{Name: "t-other", Scope: models.RoutingScopeToken, TokenID: 88})

	list := listRoutings(t, srv, jwt, "")
	names := make(map[string]bool)
	for _, r := range list {
		names[r.Name] = true
	}
	for _, name := range []string{"g1", "u-other", "t-other"} {
		if !names[name] {
			t.Errorf("default admin filter should include %q, got %v", name, names)
		}
	}
}

func TestModelRoutingList_ExplicitOwnerFilters(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedListedRouting(t, srv, models.ModelRouting{Name: "u42", Scope: models.RoutingScopeUser, UserID: 42})
	seedListedRouting(t, srv, models.ModelRouting{Name: "u43", Scope: models.RoutingScopeUser, UserID: 43})
	seedListedRouting(t, srv, models.ModelRouting{Name: "t7", Scope: models.RoutingScopeToken, TokenID: 7})
	seedListedRouting(t, srv, models.ModelRouting{Name: "t8", Scope: models.RoutingScopeToken, TokenID: 8})

	userList := listRoutings(t, srv, jwt, "?scope=user&user_id=42")
	if len(userList) != 1 || userList[0].Name != "u42" {
		t.Errorf("scope=user&user_id=42 should return only u42, got %v", userList)
	}
	tokenList := listRoutings(t, srv, jwt, "?scope=token&token_id=7")
	if len(tokenList) != 1 || tokenList[0].Name != "t7" {
		t.Errorf("scope=token&token_id=7 should return only t7, got %v", tokenList)
	}
}

func TestModelRoutingList_PaginatesAcrossEveryScope(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedListedRouting(t, srv, models.ModelRouting{Name: "g1", Scope: models.RoutingScopeGlobal})
	seedListedRouting(t, srv, models.ModelRouting{Name: "u1", Scope: models.RoutingScopeUser, UserID: 1})
	seedListedRouting(t, srv, models.ModelRouting{Name: "t1", Scope: models.RoutingScopeToken, TokenID: 1})

	w := routingRequest(t, srv, jwt, http.MethodGet, "/api/admin/model-routings?page=2&page_size=2", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var response struct {
		Data     []models.ModelRouting `json:"data"`
		Total    int64                 `json:"total"`
		Page     int                   `json:"page"`
		PageSize int                   `json:"page_size"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if response.Total != 3 || response.Page != 2 || response.PageSize != 2 || len(response.Data) != 1 {
		t.Fatalf("unexpected page: %+v", response)
	}
}

func TestModelRoutingList_RejectsInvalidScopeOwnerCombinations(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)

	for _, query := range []string{
		"?scope=invalid",
		"?user_id=1",
		"?token_id=1",
		"?scope=global&user_id=1",
		"?scope=global&token_id=1",
		"?scope=user&token_id=1",
		"?scope=token&user_id=1",
		"?scope=user&user_id=1&token_id=1",
	} {
		t.Run(query, func(t *testing.T) {
			w := routingRequest(t, srv, jwt, http.MethodGet, "/api/admin/model-routings"+query, nil)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestModelRoutingList_EmptyTokenFilter(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	if got := listRoutings(t, srv, jwt, "?scope=token&token_id=999"); len(got) != 0 {
		t.Fatalf("empty token filter = %v, want no rows", got)
	}
}

func TestModelRoutingList_ScopeGlobalOnly(t *testing.T) {
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

func TestModelRoutingList_QSearch(t *testing.T) {
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
