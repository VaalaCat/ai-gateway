package api_test

import (
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestUserGroup_CreateListGetUpdateDelete(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create
	w := doReq("POST", "/api/admin/user-groups", map[string]any{
		"name":                "team-x",
		"description":         "X team",
		"allowed_channel_ids": []int{1, 2, 3},
		"models":              `["gpt-4o"]`,
	})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	created := jsonBody(t, w)
	id := int(created["id"].(float64))
	if id == 0 {
		t.Fatalf("create returned id=0: %v", created)
	}
	if created["name"] != "team-x" {
		t.Fatalf("name mismatch: %v", created)
	}

	// List
	w = doReq("GET", "/api/admin/user-groups?search=team-x", nil)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	listResp := jsonBody(t, w)
	data, _ := listResp["data"].([]any)
	if len(data) < 1 {
		t.Fatalf("list returned no items: %v", listResp)
	}

	// Get (with user_count)
	w = doReq("GET", "/api/admin/user-groups/"+itoa(id), nil)
	if w.Code != 200 {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	got := jsonBody(t, w)
	if _, ok := got["user_count"]; !ok {
		t.Fatalf("get response missing user_count: %v", got)
	}

	// Update
	w = doReq("PUT", "/api/admin/user-groups/"+itoa(id), map[string]any{"description": "updated"})
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}

	// Delete
	w = doReq("DELETE", "/api/admin/user-groups/"+itoa(id), nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

func TestUserGroup_CannotRenameOrDisableDefault(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doReq("PUT", "/api/admin/user-groups/1", map[string]any{"name": "new-default"})
	if w.Code == 200 {
		t.Fatalf("expected 4xx for renaming default, got 200; body: %s", w.Body.String())
	}

	w = doReq("PUT", "/api/admin/user-groups/1", map[string]any{"status": 0})
	if w.Code == 200 {
		t.Fatalf("expected 4xx for disabling default, got 200; body: %s", w.Body.String())
	}
}

func TestUserGroup_RejectsInvalidStatus(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	// Create a non-default group so we can exercise the validator
	wc := reqHelper(srv, adminToken, "POST", "/api/admin/user-groups", map[string]any{"name": "g-invalid-status"})
	if wc.Code != 200 && wc.Code != 201 {
		t.Fatalf("create user group: %d %s", wc.Code, wc.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(wc.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created group: %v", err)
	}
	gid := int(created["id"].(float64))

	w := reqHelper(srv, adminToken, "PUT", "/api/admin/user-groups/"+strconv.Itoa(gid), map[string]any{"status": 2})
	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid user_group status=2, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUserGroup_CannotDeleteDefault(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	w := reqHelper(srv, adminToken, "DELETE", "/api/admin/user-groups/1", nil)
	if w.Code == 200 {
		t.Fatalf("expected 4xx for deleting default, got 200; body: %s", w.Body.String())
	}
}

func TestUserGroup_DeleteReassignsMembers(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create a non-default group via API
	w := doReq("POST", "/api/admin/user-groups", map[string]any{"name": "tmp"})
	if w.Code != 200 && w.Code != 201 {
		t.Fatalf("create group: %d %s", w.Code, w.Body.String())
	}
	g := jsonBody(t, w)
	gid := uint(g["id"].(float64))

	// Add a user to that group via raw DB (the user API doesn't yet accept group_id — Task 22 will)
	u := models.User{Username: "carol", Password: "x", GroupID: gid}
	if err := srv.DB.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	// Delete the group
	w = doReq("DELETE", "/api/admin/user-groups/"+itoa(int(gid)), nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}

	// Verify user reassigned to default (1)
	var reloaded models.User
	if err := srv.DB.First(&reloaded, u.ID).Error; err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if reloaded.GroupID != 1 {
		t.Fatalf("user not reassigned to default, GroupID = %d", reloaded.GroupID)
	}
}
