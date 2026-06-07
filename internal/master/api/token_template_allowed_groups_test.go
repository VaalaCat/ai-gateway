package api_test

import (
	"net/http/httptest"
	"testing"
)

func TestTokenTemplate_AllowedGroupIDs_CRUD(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	g1 := uint(jsonBody(t, doReq("POST", "/api/admin/user-groups", map[string]any{"name": "grp-1"}))["id"].(float64))
	g2 := uint(jsonBody(t, doReq("POST", "/api/admin/user-groups", map[string]any{"name": "grp-2"}))["id"].(float64))

	// create with allowed_group_ids
	createW := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name":              "tpl-groups",
		"allowed_group_ids": []uint{g1, g2},
	})
	if createW.Code != 201 {
		t.Fatalf("create: %d %s", createW.Code, createW.Body.String())
	}
	created := jsonBody(t, createW)
	if ids, _ := created["allowed_group_ids"].([]any); len(ids) != 2 {
		t.Fatalf("allowed_group_ids len = %d, want 2; body=%s", len(ids), createW.Body.String())
	}
	tplID := uint(created["id"].(float64))

	// update: shrink to one group
	updW := doReq("PUT", "/api/admin/token-templates/"+itoa(int(tplID)), map[string]any{
		"allowed_group_ids": []uint{g1},
	})
	if updW.Code != 200 {
		t.Fatalf("update: %d %s", updW.Code, updW.Body.String())
	}
	if ids, _ := jsonBody(t, updW)["allowed_group_ids"].([]any); len(ids) != 1 {
		t.Fatalf("after update len = %d, want 1; body=%s", len(ids), updW.Body.String())
	}

	// invalid: zero id rejected
	badW := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name":              "tpl-bad",
		"allowed_group_ids": []uint{0},
	})
	if badW.Code == 200 || badW.Code == 201 {
		t.Fatalf("expected 4xx for zero group id, got %d %s", badW.Code, badW.Body.String())
	}
}

func TestTokenTemplate_ListEnabled_GroupFilter(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	adminReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	ga := uint(jsonBody(t, adminReq("POST", "/api/admin/user-groups", map[string]any{"name": "gA"}))["id"].(float64))

	adminReq("POST", "/api/admin/token-templates", map[string]any{"name": "tpl-A", "allowed_group_ids": []uint{ga}})
	adminReq("POST", "/api/admin/token-templates", map[string]any{"name": "tpl-open"})

	adminReq("POST", "/api/admin/users", map[string]any{"username": "ua", "password": "pwd1234", "role": 1, "group_id": ga})
	adminReq("POST", "/api/admin/users", map[string]any{"username": "ub", "password": "pwd1234", "role": 1})

	// user A (in group A) sees both
	uaToken := loginHelper(t, srv, "ua", "pwd1234")
	uaList := jsonBody(t, reqHelper(srv, uaToken, "GET", "/api/token-templates?page=1&page_size=100", nil))
	if n := len(uaList["data"].([]any)); n != 2 {
		t.Fatalf("user A should see 2 templates, got %d: %v", n, uaList["data"])
	}

	// user B (default group) sees only the open one
	ubToken := loginHelper(t, srv, "ub", "pwd1234")
	ubW := reqHelper(srv, ubToken, "GET", "/api/token-templates?page=1&page_size=100", nil)
	data := jsonBody(t, ubW)["data"].([]any)
	if len(data) != 1 {
		t.Fatalf("user B should see 1 template, got %d: %s", len(data), ubW.Body.String())
	}
	if name := data[0].(map[string]any)["name"]; name != "tpl-open" {
		t.Fatalf("user B should see tpl-open, got %v", name)
	}

	// admin hitting the same enabled endpoint is NOT filtered
	adminList := jsonBody(t, adminReq("GET", "/api/token-templates?page=1&page_size=100", nil))
	if n := len(adminList["data"].([]any)); n != 2 {
		t.Fatalf("admin should see 2 templates, got %d", n)
	}
}
