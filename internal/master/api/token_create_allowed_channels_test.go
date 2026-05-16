package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTokenCreate_NormalUser_SnapshotsAllowedChannelsFromTemplate verifies
// that when a normal user creates a token via /api/tokens with a template_id,
// the new token's allowed_channel_ids is snapshotted from the template.
func TestTokenCreate_NormalUser_SnapshotsAllowedChannelsFromTemplate(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// admin login
	loginAs := func(user, pass string) string {
		body, _ := json.Marshal(map[string]any{"username": user, "password": pass})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("login %s: %d %s", user, w.Code, w.Body.String())
		}
		var resp map[string]string
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp["token"]
	}

	doReq := func(jwt, method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	adminJWT := loginAs("admin", "admin123")

	// 1. admin creates a normal user
	wU := doReq(adminJWT, "POST", "/api/admin/users", map[string]any{
		"username": "user1", "password": "pass", "role": 1,
	})
	if wU.Code != 201 {
		t.Fatalf("create user: %d %s", wU.Code, wU.Body.String())
	}

	// 2. admin creates a template with whitelist
	wT := doReq(adminJWT, "POST", "/api/admin/token-templates", map[string]any{
		"name":                "tpl-with-whitelist",
		"allowed_channel_ids": []int{3, 7, 9},
	})
	if wT.Code != 201 {
		t.Fatalf("create template: %d %s", wT.Code, wT.Body.String())
	}
	var tplResp map[string]any
	_ = json.Unmarshal(wT.Body.Bytes(), &tplResp)
	tplID := tplResp["id"].(float64)

	// 3. user1 logs in and creates a token via /api/tokens with template_id
	userJWT := loginAs("user1", "pass")
	wTk := doReq(userJWT, "POST", "/api/tokens", map[string]any{
		"name":        "user-token",
		"template_id": uint(tplID),
	})
	if wTk.Code != 201 {
		t.Fatalf("user create token: %d %s", wTk.Code, wTk.Body.String())
	}
	var tokenResp map[string]any
	_ = json.Unmarshal(wTk.Body.Bytes(), &tokenResp)
	gotIDs, _ := tokenResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 3 {
		t.Fatalf("token allowed_channel_ids len = %d, want 3 (snapshotted from template); resp = %s", len(gotIDs), wTk.Body.String())
	}
	want := map[float64]bool{3: false, 7: false, 9: false}
	for _, v := range gotIDs {
		f := v.(float64)
		if _, ok := want[f]; !ok {
			t.Fatalf("unexpected channel ID %v in token; want subset of {3,7,9}", f)
		}
		want[f] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("channel ID %v not snapshotted into token", k)
		}
	}
}

// TestTokenCreate_Admin_WritesAllowedChannelsFromRequest verifies that
// admin path writes whitelist directly from request body, NOT from any
// template, mirroring how Models is treated.
func TestTokenCreate_Admin_WritesAllowedChannelsFromRequest(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	var loginResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwt := loginResp["token"]

	// admin creates a normal user as token's owner
	wU := httptest.NewRecorder()
	uBody, _ := json.Marshal(map[string]any{"username": "user1", "password": "pass", "role": 1})
	uReq, _ := http.NewRequest("POST", "/api/admin/users", bytes.NewReader(uBody))
	uReq.Header.Set("Content-Type", "application/json")
	uReq.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(wU, uReq)
	if wU.Code != 201 {
		t.Fatalf("create user: %d %s", wU.Code, wU.Body.String())
	}
	var userResp map[string]any
	_ = json.Unmarshal(wU.Body.Bytes(), &userResp)
	userID := uint(userResp["id"].(float64))

	// admin creates a token with explicit whitelist
	wTk := httptest.NewRecorder()
	tBody, _ := json.Marshal(map[string]any{
		"user_id":             userID,
		"name":                "admin-token-with-whitelist",
		"allowed_channel_ids": []int{11, 22},
	})
	tReq, _ := http.NewRequest("POST", "/api/tokens", bytes.NewReader(tBody))
	tReq.Header.Set("Content-Type", "application/json")
	tReq.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(wTk, tReq)
	if wTk.Code != 201 {
		t.Fatalf("admin create token: %d %s", wTk.Code, wTk.Body.String())
	}
	var tokenResp map[string]any
	_ = json.Unmarshal(wTk.Body.Bytes(), &tokenResp)
	gotIDs, _ := tokenResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 2 {
		t.Fatalf("admin token allowed_channel_ids len = %d, want 2; resp = %s", len(gotIDs), wTk.Body.String())
	}
	want := map[float64]bool{11: false, 22: false}
	for _, v := range gotIDs {
		f := v.(float64)
		if _, ok := want[f]; !ok {
			t.Fatalf("unexpected channel ID %v; want subset of {11,22}", f)
		}
		want[f] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("channel ID %v missing from token", k)
		}
	}
}

// TestTokenCreate_Admin_TemplateIDIgnoresWhitelist verifies that admin path
// with both template_id and allowed_channel_ids takes the request value,
// NOT the template's whitelist.
func TestTokenCreate_Admin_TemplateIDIgnoresWhitelist(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	body, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	var loginResp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwt := loginResp["token"]

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwt)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	// create template with whitelist [99]
	wT := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name":                "tpl-99",
		"allowed_channel_ids": []int{99},
	})
	if wT.Code != 201 {
		t.Fatalf("create template: %d %s", wT.Code, wT.Body.String())
	}
	var tplResp map[string]any
	_ = json.Unmarshal(wT.Body.Bytes(), &tplResp)
	tplID := uint(tplResp["id"].(float64))

	// create user
	wU := doReq("POST", "/api/admin/users", map[string]any{"username": "user1", "password": "pass", "role": 1})
	if wU.Code != 201 {
		t.Fatalf("create user: %d %s", wU.Code, wU.Body.String())
	}
	var userResp map[string]any
	_ = json.Unmarshal(wU.Body.Bytes(), &userResp)
	userID := uint(userResp["id"].(float64))

	// admin creates token with BOTH template_id AND explicit allowed_channel_ids;
	// expect req values win (NOT template's [99]).
	wTk := doReq("POST", "/api/tokens", map[string]any{
		"user_id":             userID,
		"name":                "admin-explicit-wins",
		"template_id":         tplID,
		"allowed_channel_ids": []int{1, 2},
	})
	if wTk.Code != 201 {
		t.Fatalf("admin create token: %d %s", wTk.Code, wTk.Body.String())
	}
	var tokenResp map[string]any
	_ = json.Unmarshal(wTk.Body.Bytes(), &tokenResp)
	gotIDs, _ := tokenResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 2 {
		t.Fatalf("len = %d, want 2; resp = %s", len(gotIDs), wTk.Body.String())
	}
	want := map[float64]bool{1: false, 2: false}
	for _, v := range gotIDs {
		f := v.(float64)
		if _, ok := want[f]; !ok {
			t.Fatalf("admin path leaked template's whitelist; got ID %v, want subset of {1,2}", f)
		}
		want[f] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("channel ID %v missing", k)
		}
	}
}
