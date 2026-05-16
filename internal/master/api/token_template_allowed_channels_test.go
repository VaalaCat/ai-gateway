package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestTokenTemplate_AllowedChannelIDs_CreateAndRoundtrip(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// login
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	loginW := httptest.NewRecorder()
	loginReq, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(loginW, loginReq)
	if loginW.Code != 200 {
		t.Fatalf("login: %d %s", loginW.Code, loginW.Body.String())
	}
	var loginResp map[string]string
	_ = json.Unmarshal(loginW.Body.Bytes(), &loginResp)
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

	// Create with allowed_channel_ids
	createW := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name":                "tpl-with-whitelist",
		"allowed_channel_ids": []int{3, 7, 9},
	})
	if createW.Code != 201 {
		t.Fatalf("create: %d %s", createW.Code, createW.Body.String())
	}
	var createResp map[string]any
	if err := json.Unmarshal(createW.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("parse create resp: %v body=%s", err, createW.Body.String())
	}
	gotIDs, _ := createResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 3 {
		t.Fatalf("allowed_channel_ids len = %d, want 3; body = %s", len(gotIDs), createW.Body.String())
	}
	want := []float64{3, 7, 9} // JSON unmarshals numbers as float64
	got := make([]float64, len(gotIDs))
	for i, v := range gotIDs {
		got[i] = v.(float64)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("allowed_channel_ids = %v, want %v", got, want)
	}
}

func TestTokenTemplate_AllowedChannelIDs_TooMany(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	loginW := httptest.NewRecorder()
	loginReq, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(loginW, loginReq)
	var loginResp map[string]string
	_ = json.Unmarshal(loginW.Body.Bytes(), &loginResp)
	jwt := loginResp["token"]

	ids := make([]int, 101)
	for i := range ids {
		ids[i] = i + 1
	}
	body, _ := json.Marshal(map[string]any{
		"name":                "tpl-too-many",
		"allowed_channel_ids": ids,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/token-templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for >100 IDs; got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenTemplate_AllowedChannelIDs_ZeroID(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	loginW := httptest.NewRecorder()
	loginReq, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(loginW, loginReq)
	var loginResp map[string]string
	_ = json.Unmarshal(loginW.Body.Bytes(), &loginResp)
	jwt := loginResp["token"]

	body, _ := json.Marshal(map[string]any{
		"name":                "tpl-with-zero",
		"allowed_channel_ids": []int{1, 0, 3},
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/token-templates", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)

	if w.Code != 400 {
		t.Fatalf("expected 400 for ID=0; got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenTemplate_AllowedChannelIDs_Update(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	loginW := httptest.NewRecorder()
	loginReq, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	loginReq.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(loginW, loginReq)
	var loginResp map[string]string
	_ = json.Unmarshal(loginW.Body.Bytes(), &loginResp)
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

	// Create without whitelist
	createW := doReq("POST", "/api/admin/token-templates", map[string]any{"name": "tpl-update"})
	if createW.Code != 201 {
		t.Fatalf("create: %d %s", createW.Code, createW.Body.String())
	}
	var createResp map[string]any
	_ = json.Unmarshal(createW.Body.Bytes(), &createResp)
	id := int(createResp["id"].(float64))

	// Update to add whitelist
	updateW := doReq("PUT", "/api/admin/token-templates/"+itoa(id), map[string]any{
		"allowed_channel_ids": []int{5, 11},
	})
	if updateW.Code != 200 {
		t.Fatalf("update: %d %s", updateW.Code, updateW.Body.String())
	}
	var updateResp map[string]any
	_ = json.Unmarshal(updateW.Body.Bytes(), &updateResp)
	gotIDs, _ := updateResp["allowed_channel_ids"].([]any)
	if len(gotIDs) != 2 {
		t.Fatalf("after update allowed_channel_ids len = %d, want 2; resp = %s", len(gotIDs), updateW.Body.String())
	}
}
