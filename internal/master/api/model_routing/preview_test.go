package model_routing_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
)

func previewRouting(t *testing.T, srv *master.Server, jwt string, body map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/model-routings/preview", bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("preview: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp
}

func TestPreview_FlatSamePriority(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "a,b,c")
	resp := previewRouting(t, srv, jwt, map[string]any{
		"members": []map[string]any{
			{"ref": "a", "priority": 0, "weight": 3},
			{"ref": "b", "priority": 0, "weight": 1},
		},
	})
	weights := resp["effective_weights"].([]any)
	if len(weights) != 2 {
		t.Fatalf("expected 2 weights, got %d", len(weights))
	}
	// a should be 75%, b 25%
	a := weights[0].(map[string]any)
	b := weights[1].(map[string]any)
	if a["ref"] != "a" || a["percent"].(float64) != 75 {
		t.Errorf("a expected 75%%, got %v", a)
	}
	if b["ref"] != "b" || b["percent"].(float64) != 25 {
		t.Errorf("b expected 25%%, got %v", b)
	}
}

func TestPreview_PriorityOnlyTopGets100(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "a,b")
	resp := previewRouting(t, srv, jwt, map[string]any{
		"members": []map[string]any{
			{"ref": "a", "priority": 10, "weight": 1},
			{"ref": "b", "priority": 5, "weight": 1},
		},
	})
	weights := resp["effective_weights"].([]any)
	if len(weights) != 1 || weights[0].(map[string]any)["ref"] != "a" {
		t.Errorf("only top-priority a should appear, got %v", weights)
	}
	if weights[0].(map[string]any)["percent"].(float64) != 100 {
		t.Errorf("a expected 100%%, got %v", weights[0])
	}
}

func TestPreview_NestedRouting(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "x,y,z")
	// 创建 cheap-pool=[x P0 W1, y P0 W1] (50/50)
	createRouting(t, srv, jwt, map[string]any{
		"name": "cheap-pool", "scope": "global", "enabled": true,
		"members": []map[string]any{
			{"ref": "x", "priority": 0, "weight": 1},
			{"ref": "y", "priority": 0, "weight": 1},
		},
	})
	// preview smart=[cheap-pool P0 W3, z P0 W1] → 75% cheap-pool, 25% z
	// → 真实 model 维度: x=37.5%, y=37.5%, z=25%
	resp := previewRouting(t, srv, jwt, map[string]any{
		"members": []map[string]any{
			{"ref": "cheap-pool", "priority": 0, "weight": 3},
			{"ref": "z", "priority": 0, "weight": 1},
		},
	})
	weights := resp["effective_weights"].([]any)
	weightMap := map[string]float64{}
	for _, w := range weights {
		m := w.(map[string]any)
		weightMap[m["ref"].(string)] = m["percent"].(float64)
	}
	if weightMap["x"] != 37.5 || weightMap["y"] != 37.5 || weightMap["z"] != 25 {
		t.Errorf("nested routing weights: %v", weightMap)
	}
}

func TestPreview_InvalidRefMarked(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "a")
	resp := previewRouting(t, srv, jwt, map[string]any{
		"members": []map[string]any{
			{"ref": "nonexistent", "priority": 0, "weight": 1},
		},
	})
	root := resp["root"].(map[string]any)
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	child := children[0].(map[string]any)
	if child["kind"] != "invalid" || child["error"] != "not_found" {
		t.Errorf("invalid ref should be marked, got %v", child)
	}
}

func TestPreview_DisabledRoutingMarked(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "x")
	// 创建 disabled routing
	createRouting(t, srv, jwt, map[string]any{
		"name": "off-pool", "scope": "global", "enabled": false,
		"members": []map[string]any{{"ref": "x", "priority": 0, "weight": 1}},
	})
	resp := previewRouting(t, srv, jwt, map[string]any{
		"members": []map[string]any{
			{"ref": "off-pool", "priority": 0, "weight": 1},
		},
	})
	root := resp["root"].(map[string]any)
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	child := children[0].(map[string]any)
	if child["kind"] != "routing" || child["error"] != "disabled" {
		t.Errorf("disabled routing should be marked kind=routing error=disabled, got %v", child)
	}
}

// 编辑路由 gpt-5.5、成员引用同名真实模型 gpt-5.5：预览应为 model 叶子，不标 cycle。
func TestPreview_SelfNameModelNotCycle(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-5.5,gpt-4o")
	// 先建一个名为 gpt-5.5 的全局路由（成员随意填真实模型），使其进入 rIdx。
	createRouting(t, srv, jwt, map[string]any{
		"name": "gpt-5.5", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	resp := previewRouting(t, srv, jwt, map[string]any{
		"self_name": "gpt-5.5", "self_scope": "global",
		"members": []map[string]any{{"ref": "gpt-5.5", "priority": 0, "weight": 1}},
	})
	root := resp["root"].(map[string]any)
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	child := children[0].(map[string]any)
	if child["kind"] != "model" || child["error"] != nil {
		t.Errorf("same-name real model should be kind=model without error, got %v", child)
	}
	weights := resp["effective_weights"].([]any)
	if len(weights) != 1 {
		t.Fatalf("expected 1 effective weight, got %d", len(weights))
	}
	w := weights[0].(map[string]any)
	if w["ref"] != "gpt-5.5" || w["percent"].(float64) != 100 {
		t.Errorf("shadow model should be 100%% effective weight, got %v", w)
	}
}

// off-path：preview outer 引用 N，N 是已保存全局路由且存在同名真实模型 →
// 应展开为 routing 节点（有 children），而非短路成 model 叶子。
func TestPreview_OffPathRoutingNotShortCircuitToModel(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "N,realM")
	createRouting(t, srv, jwt, map[string]any{
		"name": "N", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "realM", "priority": 0, "weight": 1}},
	})
	resp := previewRouting(t, srv, jwt, map[string]any{
		"self_name": "outer", "self_scope": "global",
		"members": []map[string]any{{"ref": "N", "priority": 0, "weight": 1}},
	})
	root := resp["root"].(map[string]any)
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	child := children[0].(map[string]any)
	if child["kind"] != "routing" || child["error"] != nil {
		t.Errorf("off-path routing N should expand as routing node (not short-circuit to model), got %v", child)
	}
	sub := child["children"].([]any)
	if len(sub) != 1 || sub[0].(map[string]any)["ref"] != "realM" {
		t.Errorf("routing N should expand to realM leaf, got %v", sub)
	}
}

// 同上但不存在同名真实模型：仍应标 cycle。
func TestPreview_SelfNameNoModelCycle(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o") // 没有 gpt-5.5 真实模型
	createRouting(t, srv, jwt, map[string]any{
		"name": "gpt-5.5", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	resp := previewRouting(t, srv, jwt, map[string]any{
		"self_name": "gpt-5.5", "self_scope": "global",
		"members": []map[string]any{{"ref": "gpt-5.5", "priority": 0, "weight": 1}},
	})
	root := resp["root"].(map[string]any)
	children := root["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("expected 1 child, got %d", len(children))
	}
	child := children[0].(map[string]any)
	if child["kind"] != "routing" || child["error"] != "cycle" {
		t.Errorf("self-loop without real model should be kind=routing error=cycle, got %v", child)
	}
}

func TestCandidates_ListsModelsAndRoutings(t *testing.T) {
	srv := setupTestMaster(t)
	jwt := loginAdmin(t, srv)
	seedChannel(t, srv, jwt, "gpt-4o,claude-sonnet-4")
	createRouting(t, srv, jwt, map[string]any{
		"name": "smart", "scope": "global", "enabled": true,
		"members": []map[string]any{{"ref": "gpt-4o", "priority": 0, "weight": 1}},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/api/admin/model-routings/candidates", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("candidates: %d %s", w.Code, w.Body.String())
	}
	var resp struct {
		Models         []string `json:"models"`
		GlobalRoutings []string `json:"global_routings"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	if len(resp.Models) < 2 {
		t.Errorf("expected >= 2 models, got %v", resp.Models)
	}
	found := false
	for _, n := range resp.GlobalRoutings {
		if n == "smart" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'smart' in global_routings, got %v", resp.GlobalRoutings)
	}
}
