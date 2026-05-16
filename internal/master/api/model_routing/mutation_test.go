package model_routing_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// --- helpers ---

func updateRouting(t *testing.T, srv *master.Server, jwt string, id int, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/admin/model-routings/%d", id), bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

func deleteRouting(t *testing.T, srv *master.Server, jwt string, id int) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// --- TestCreate ---

func TestCreate_Success(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	id := createRouting(t, srv, jwt, map[string]any{
		"name": "test-create", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	if id == 0 {
		t.Fatal("expected non-zero id")
	}
}

func TestCreate_InvalidRef_StructuredError(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	// 不 seedChannel，ref 不存在
	raw, _ := json.Marshal(map[string]any{
		"name": "bad-ref", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "nonexistent-model", "priority": 0, "weight": 1}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/model-routings", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["code"] != "invalid_ref" {
		t.Errorf("expected code=invalid_ref, got %v", resp["code"])
	}
	if resp["message"] == "" {
		t.Error("message should not be empty")
	}
}

func TestCreate_CycleDetected_409Or400(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	// 创建 A 引用 gpt-4o
	idA := createRouting(t, srv, jwt, map[string]any{
		"name": "route-A", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	// 创建 B 引用 A（正常）
	_ = createRouting(t, srv, jwt, map[string]any{
		"name": "route-B", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "route-A", "priority": 0, "weight": 1}},
	})

	// 更新 A 引用 B → cycle
	code, resp := updateRouting(t, srv, jwt, idA, map[string]any{
		"members": []map[string]any{{"ref": "route-B", "priority": 0, "weight": 1}},
	})
	if code != http.StatusBadRequest && code != http.StatusConflict {
		t.Fatalf("expected 400 or 409 for cycle, got %d: %v", code, resp)
	}
	if resp["code"] != "cycle_detected" {
		t.Errorf("expected code=cycle_detected, got %v", resp["code"])
	}
}

// --- TestUpdate ---

func TestUpdate_Success(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	id := createRouting(t, srv, jwt, map[string]any{
		"name": "upd-test", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	code, resp := updateRouting(t, srv, jwt, id, map[string]any{
		"remark": "updated-remark",
	})
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}
	if resp["remark"] != "updated-remark" {
		t.Errorf("remark not updated: %v", resp["remark"])
	}
}

func TestUpdate_NotFound(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)

	code, _ := updateRouting(t, srv, jwt, 9999, map[string]any{"remark": "x"})
	if code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", code)
	}
}

func TestUpdate_CannotChangeScope(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	id := createRouting(t, srv, jwt, map[string]any{
		"name": "scope-test", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	// scope 字段在 DAO Update 的 allowed map 中被忽略，应该静默成功
	code, _ := updateRouting(t, srv, jwt, id, map[string]any{"scope": "user"})
	if code != http.StatusOK {
		t.Fatalf("expected 200 (scope silently ignored), got %d", code)
	}

	// 验证 scope 没有变化
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var got models.ModelRouting
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if got.Scope != "global" {
		t.Errorf("scope should still be global, got %s", got.Scope)
	}
}

// --- TestDelete ---

func TestDelete_Success(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	id := createRouting(t, srv, jwt, map[string]any{
		"name": "del-test", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	code, resp := deleteRouting(t, srv, jwt, id)
	if code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %v", code, resp)
	}

	// 确认已删除
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/admin/model-routings/%d", id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("after delete, expected 404, got %d", w.Code)
	}
}

func TestDelete_Referenced_409(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o")

	// 创建 B（被引用）
	idB := createRouting(t, srv, jwt, map[string]any{
		"name": "route-B2", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})

	// 创建 A 引用 B
	_ = createRouting(t, srv, jwt, map[string]any{
		"name": "route-A2", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "route-B2", "priority": 0, "weight": 1}},
	})

	// 删 B → 409 + code=referenced_by
	code, resp := deleteRouting(t, srv, jwt, idB)
	if code != http.StatusConflict {
		t.Fatalf("expected 409 conflict, got %d: %v", code, resp)
	}
	if resp["code"] != "referenced_by" {
		t.Errorf("expected code=referenced_by, got %v", resp["code"])
	}
}
