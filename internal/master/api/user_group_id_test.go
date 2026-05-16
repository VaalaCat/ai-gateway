package api_test

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserCreate_DefaultsToDefaultGroup(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doReq("POST", "/api/admin/users", map[string]any{"username": "alice", "password": "pwd1234"})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"group_id":1`) {
		t.Fatalf("expected group_id 1 default: %s", w.Body.String())
	}
}

func TestUserCreate_RejectsUnknownGroup(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doReq("POST", "/api/admin/users", map[string]any{
		"username": "bob", "password": "pwd1234", "group_id": 9999,
	})
	if w.Code == 200 || w.Code == 201 {
		t.Fatalf("expected 4xx for unknown group, got %d", w.Code)
	}
}

func TestUserUpdate_GroupID_Reassign(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	gW := doReq("POST", "/api/admin/user-groups", map[string]any{"name": "g-update"})
	g := jsonBody(t, gW)
	gid := uint(g["id"].(float64))

	uW := doReq("POST", "/api/admin/users", map[string]any{"username": "carol", "password": "pwd1234"})
	u := jsonBody(t, uW)
	uid := uint(u["id"].(float64))

	upd := doReq("PUT", "/api/admin/users/"+itoa(int(uid)), map[string]any{"group_id": gid})
	if upd.Code != 200 {
		t.Fatalf("status %d body %s", upd.Code, upd.Body.String())
	}

	got := doReq("GET", "/api/admin/users/"+itoa(int(uid)), nil)
	if !strings.Contains(got.Body.String(), `"group_id":`+itoa(int(gid))) {
		t.Fatalf("group_id not updated: %s", got.Body.String())
	}
}

func TestUserUpdate_GroupID_Unknown(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	uW := doReq("POST", "/api/admin/users", map[string]any{"username": "dave", "password": "pwd1234"})
	u := jsonBody(t, uW)
	uid := uint(u["id"].(float64))

	upd := doReq("PUT", "/api/admin/users/"+itoa(int(uid)), map[string]any{"group_id": 99999})
	if upd.Code == 200 {
		t.Fatalf("expected 4xx, got 200")
	}
}

func TestProfile_ReturnsGroupName(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	gW := doReq("POST", "/api/admin/user-groups", map[string]any{"name": "p-team"})
	g := jsonBody(t, gW)
	gid := uint(g["id"].(float64))

	uW := doReq("POST", "/api/admin/users", map[string]any{
		"username": "eve", "password": "pwd1234", "group_id": gid,
	})
	if uW.Code != 200 && uW.Code != 201 {
		t.Fatalf("create user: %d %s", uW.Code, uW.Body.String())
	}

	eveToken := loginHelper(t, srv, "eve", "pwd1234")
	pResp := reqHelper(srv, eveToken, "GET", "/api/profile", nil)
	if pResp.Code != 200 {
		t.Fatalf("profile: %d %s", pResp.Code, pResp.Body.String())
	}
	if !strings.Contains(pResp.Body.String(), `"group_name":"p-team"`) {
		t.Fatalf("profile missing group_name: %s", pResp.Body.String())
	}
	if !strings.Contains(pResp.Body.String(), `"group_id":`+itoa(int(gid))) {
		t.Fatalf("profile missing group_id: %s", pResp.Body.String())
	}
}
