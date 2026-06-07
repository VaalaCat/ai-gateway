package api_test

import (
	"testing"
)

func TestTokenCreate_GroupGate(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	ga := uint(jsonBody(t, reqHelper(srv, adminToken, "POST", "/api/admin/user-groups", map[string]any{"name": "gate-A"}))["id"].(float64))

	tpl := jsonBody(t, reqHelper(srv, adminToken, "POST", "/api/admin/token-templates", map[string]any{
		"name": "tpl-gate", "allowed_group_ids": []uint{ga},
	}))
	tplID := uint(tpl["id"].(float64))

	reqHelper(srv, adminToken, "POST", "/api/admin/users", map[string]any{"username": "ina", "password": "pwd1234", "role": 1, "group_id": ga})
	reqHelper(srv, adminToken, "POST", "/api/admin/users", map[string]any{"username": "outb", "password": "pwd1234", "role": 1})

	// in-group user can create a token from the template
	inToken := loginHelper(t, srv, "ina", "pwd1234")
	okW := reqHelper(srv, inToken, "POST", "/api/tokens", map[string]any{"name": "tok-ok", "template_id": tplID})
	if okW.Code != 200 && okW.Code != 201 {
		t.Fatalf("in-group create should succeed, got %d %s", okW.Code, okW.Body.String())
	}

	// out-group user is blocked with 403
	outToken := loginHelper(t, srv, "outb", "pwd1234")
	denyW := reqHelper(srv, outToken, "POST", "/api/tokens", map[string]any{"name": "tok-deny", "template_id": tplID})
	if denyW.Code != 403 {
		t.Fatalf("out-group create should be 403, got %d %s", denyW.Code, denyW.Body.String())
	}
}
