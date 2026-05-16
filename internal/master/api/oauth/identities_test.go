package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func loginAsUser(t *testing.T, srv *master.Server, u *models.User) string {
	t.Helper()
	tok, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, u.ID, u.Role, u.Username, u.DisplayName, u.AvatarURL)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestIdentities_ListAndDelete(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	u := &models.User{Username: "alice", Password: "h", PasswordSet: true, Role: 1, Status: 1}
	srv.DB.Create(u)
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: 1, Subject: "s1"})
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: 1, Subject: "s2"})

	jwt := loginAsUser(t, srv, u)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/identities", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("list: %d %s", w.Code, w.Body.String())
	}
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 2 {
		t.Fatalf("len=%d", len(list))
	}

	id := uint((list[0]["id"]).(float64))
	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/oauth/identities/"+toStr(id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

func TestIdentities_LastLoginMethod(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	u := &models.User{Username: "alice", Password: "h", PasswordSet: false, Role: 1, Status: 1}
	srv.DB.Create(u)
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: 1, Subject: "s1"})

	jwt := loginAsUser(t, srv, u)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/identities", nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	var list []map[string]any
	json.Unmarshal(w.Body.Bytes(), &list)
	id := uint((list[0]["id"]).(float64))

	w = httptest.NewRecorder()
	req, _ = http.NewRequest(http.MethodDelete, "/api/oauth/identities/"+toStr(id), nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", w.Code)
	}
}

func toStr(u uint) string {
	if u == 0 {
		return "0"
	}
	s := ""
	for u > 0 {
		s = string(rune('0'+u%10)) + s
		u /= 10
	}
	return s
}
