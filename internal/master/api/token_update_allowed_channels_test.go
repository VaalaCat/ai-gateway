package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestTokenUpdate_NormalUser_IgnoresAllowedChannelIDs verifies normal users
// CANNOT modify allowed_channel_ids; the field is silently dropped.
func TestTokenUpdate_NormalUser_IgnoresAllowedChannelIDs(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

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

	// admin creates user1
	wU := doReq(adminJWT, "POST", "/api/admin/users", map[string]any{"username": "user1", "password": "pass", "role": 1})
	if wU.Code != 201 {
		t.Fatalf("create user: %d %s", wU.Code, wU.Body.String())
	}

	// admin creates a template with whitelist
	wT := doReq(adminJWT, "POST", "/api/admin/token-templates", map[string]any{
		"name":                "tpl",
		"allowed_channel_ids": []int{3, 7},
	})
	if wT.Code != 201 {
		t.Fatalf("create template: %d %s", wT.Code, wT.Body.String())
	}
	var tplResp map[string]any
	_ = json.Unmarshal(wT.Body.Bytes(), &tplResp)
	tplID := uint(tplResp["id"].(float64))

	// user1 creates a token via template (snapshots [3,7])
	userJWT := loginAs("user1", "pass")
	wTk := doReq(userJWT, "POST", "/api/tokens", map[string]any{
		"name": "user-tok", "template_id": tplID,
	})
	if wTk.Code != 201 {
		t.Fatalf("user create token: %d %s", wTk.Code, wTk.Body.String())
	}
	var tokenResp map[string]any
	_ = json.Unmarshal(wTk.Body.Bytes(), &tokenResp)
	tokID := uint(tokenResp["id"].(float64))

	// user1 tries to update allowed_channel_ids → should be silently ignored
	wU2 := doReq(userJWT, "PUT", "/api/tokens/"+itoa(int(tokID)), map[string]any{
		"name":                "renamed",
		"allowed_channel_ids": []int{99},
	})
	if wU2.Code != 200 {
		t.Fatalf("user update: %d %s", wU2.Code, wU2.Body.String())
	}
	var updatedResp map[string]any
	_ = json.Unmarshal(wU2.Body.Bytes(), &updatedResp)
	if updatedResp["name"] != "renamed" {
		t.Fatalf("name not updated: %v", updatedResp["name"])
	}
	gotIDs, _ := updatedResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 2 {
		t.Fatalf("allowed_channel_ids was modified by normal user: got %v, want unchanged [3,7]; resp=%s", gotIDs, wU2.Body.String())
	}
	want := map[float64]bool{3: false, 7: false}
	for _, v := range gotIDs {
		f := v.(float64)
		if _, ok := want[f]; !ok {
			t.Fatalf("normal user modified whitelist: got ID %v, want unchanged from {3,7}", f)
		}
		want[f] = true
	}
}

// TestTokenUpdate_Admin_WritesAllowedChannelIDs verifies admin can modify
// the field via PUT /api/tokens/:id.
func TestTokenUpdate_Admin_WritesAllowedChannelIDs(t *testing.T) {
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

	wU := doReq("POST", "/api/admin/users", map[string]any{"username": "user2", "password": "pass", "role": 1})
	if wU.Code != 201 {
		t.Fatalf("create user: %d %s", wU.Code, wU.Body.String())
	}
	var userResp map[string]any
	_ = json.Unmarshal(wU.Body.Bytes(), &userResp)
	userID := uint(userResp["id"].(float64))

	wTk := doReq("POST", "/api/tokens", map[string]any{
		"user_id": userID, "name": "admin-tok",
	})
	if wTk.Code != 201 {
		t.Fatalf("create token: %d %s", wTk.Code, wTk.Body.String())
	}
	var tokResp map[string]any
	_ = json.Unmarshal(wTk.Body.Bytes(), &tokResp)
	tokID := uint(tokResp["id"].(float64))

	// admin updates allowed_channel_ids
	wU2 := doReq("PUT", "/api/tokens/"+itoa(int(tokID)), map[string]any{
		"allowed_channel_ids": []int{3, 7, 9},
	})
	if wU2.Code != 200 {
		t.Fatalf("admin update: %d %s", wU2.Code, wU2.Body.String())
	}
	var updatedResp map[string]any
	_ = json.Unmarshal(wU2.Body.Bytes(), &updatedResp)
	gotIDs, _ := updatedResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 3 {
		t.Fatalf("admin update did not persist whitelist; resp=%s", wU2.Body.String())
	}
}

