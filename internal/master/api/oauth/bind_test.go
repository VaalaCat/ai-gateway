package oauth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

func setupMasterForBind(t *testing.T) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	srv, err := master.New(&config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), PublicBaseURLs: []string{"http://localhost:8140"},
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestBind_Success(t *testing.T) {
	srv := setupMasterForBind(t)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1"), bcrypt.DefaultCost)
	srv.DB.Create(&models.User{Username: "alice", Password: string(hashed), PasswordSet: true, Status: 1})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-1",
		ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})

	body, _ := json.Marshal(map[string]string{"ticket": tk, "username": "alice", "password": "pw1"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/oauth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["token"] == "" {
		t.Fatal("no token")
	}
}

func TestBind_BadCredentials(t *testing.T) {
	srv := setupMasterForBind(t)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	hashed, _ := bcrypt.GenerateFromPassword([]byte("pw1"), bcrypt.DefaultCost)
	srv.DB.Create(&models.User{Username: "alice", Password: string(hashed), PasswordSet: true, Status: 1})
	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-1", ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})
	body, _ := json.Marshal(map[string]string{"ticket": tk, "username": "alice", "password": "WRONG"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/oauth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestBind_TicketInvalid(t *testing.T) {
	srv := setupMasterForBind(t)
	body, _ := json.Marshal(map[string]string{"ticket": "garbage", "username": "x", "password": "y"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/oauth/bind", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
}
