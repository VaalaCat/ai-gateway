package test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestAdmin_StatsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()
	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()

	srv.InitAdminUser("admin", "admin123")
	jwt := login(t, masterTS.URL, "admin", "admin123")

	req, _ := http.NewRequest("GET", masterTS.URL+"/api/admin/stats", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/admin/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}

	var stats map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}

	for _, field := range []string{"users", "channels", "connected_agents"} {
		if _, ok := stats[field]; !ok {
			t.Errorf("stats response missing field %q", field)
		}
	}
}

func TestAdmin_LogsEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()
	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()

	srv.InitAdminUser("admin", "admin123")
	jwt := login(t, masterTS.URL, "admin", "admin123")

	req, _ := http.NewRequest("GET", masterTS.URL+"/api/admin/logs", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /api/admin/logs: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, body)
	}
}

func TestAdmin_UserProfile(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()
	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()

	srv.InitAdminUser("admin", "admin123")
	adminJWT := login(t, masterTS.URL, "admin", "admin123")

	// Create a normal user via admin API
	userBody, _ := json.Marshal(map[string]any{"username": "alice", "password": "alicepass", "role": 1})
	createReq, _ := http.NewRequest("POST", masterTS.URL+"/api/admin/users", bytes.NewReader(userBody))
	createReq.Header.Set("Content-Type", "application/json")
	createReq.Header.Set("Authorization", "Bearer "+adminJWT)
	createResp, err := http.DefaultClient.Do(createReq)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	createResp.Body.Close()
	if createResp.StatusCode != 201 {
		t.Fatalf("create user: expected 201, got %d", createResp.StatusCode)
	}

	// Login as normal user
	userJWT := login(t, masterTS.URL, "alice", "alicepass")

	// GET /api/profile → verify username
	profileReq, _ := http.NewRequest("GET", masterTS.URL+"/api/profile", nil)
	profileReq.Header.Set("Authorization", "Bearer "+userJWT)
	profileResp, err := http.DefaultClient.Do(profileReq)
	if err != nil {
		t.Fatalf("GET /api/profile: %v", err)
	}
	defer profileResp.Body.Close()

	if profileResp.StatusCode != 200 {
		body, _ := io.ReadAll(profileResp.Body)
		t.Fatalf("profile: expected 200, got %d: %s", profileResp.StatusCode, body)
	}

	var profile map[string]any
	json.NewDecoder(profileResp.Body).Decode(&profile)
	if profile["username"] != "alice" {
		t.Errorf("expected username 'alice', got %v", profile["username"])
	}

	// PUT /api/profile/password with correct old password → 200
	pwBody, _ := json.Marshal(map[string]string{"old_password": "alicepass", "new_password": "newpass123"})
	pwReq, _ := http.NewRequest("PUT", masterTS.URL+"/api/profile/password", bytes.NewReader(pwBody))
	pwReq.Header.Set("Content-Type", "application/json")
	pwReq.Header.Set("Authorization", "Bearer "+userJWT)
	pwResp, err := http.DefaultClient.Do(pwReq)
	if err != nil {
		t.Fatalf("PUT /api/profile/password: %v", err)
	}
	pwResp.Body.Close()
	if pwResp.StatusCode != 200 {
		t.Fatalf("change password (correct old): expected 200, got %d", pwResp.StatusCode)
	}

	// PUT /api/profile/password with wrong old password → 401
	wrongPwBody, _ := json.Marshal(map[string]string{"old_password": "wrongpass", "new_password": "anotherpass"})
	wrongPwReq, _ := http.NewRequest("PUT", masterTS.URL+"/api/profile/password", bytes.NewReader(wrongPwBody))
	wrongPwReq.Header.Set("Content-Type", "application/json")
	wrongPwReq.Header.Set("Authorization", "Bearer "+userJWT)
	wrongPwResp, err := http.DefaultClient.Do(wrongPwReq)
	if err != nil {
		t.Fatalf("PUT /api/profile/password (wrong): %v", err)
	}
	wrongPwResp.Body.Close()
	if wrongPwResp.StatusCode != 401 {
		t.Fatalf("change password (wrong old): expected 401, got %d", wrongPwResp.StatusCode)
	}

	// Login with new password → success
	newJWT := login(t, masterTS.URL, "alice", "newpass123")
	if newJWT == "" {
		t.Fatal("login with new password failed: empty token")
	}
}
