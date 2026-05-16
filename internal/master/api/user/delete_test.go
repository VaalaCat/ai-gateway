package user_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

func setupMasterForUserAdmin(t *testing.T) *master.Server {
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

func adminToken(t *testing.T, srv *master.Server, userID uint) string {
	t.Helper()
	tok, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, userID, consts.RoleAdmin, "admin", "", "")
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestUserDelete_AlsoRemovesOAuthIdentities(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	admin := &models.User{Username: "admin", Password: "x", Role: consts.RoleAdmin, Status: consts.StatusEnabled, GroupID: 1}
	srv.DB.Create(admin)
	victim := &models.User{Username: "victim", Password: "x", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	srv.DB.Create(victim)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	srv.DB.Create(&models.OAuthProvider{Name: "feishu", DisplayName: "FS", Enabled: true})
	srv.DB.Create(&models.OAuthIdentity{UserID: victim.ID, ProviderID: 1, Subject: "sub-gh"})
	srv.DB.Create(&models.OAuthIdentity{UserID: victim.ID, ProviderID: 2, Subject: "sub-fs"})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/admin/users/"+itoa(victim.ID), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, srv, admin.ID))
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var userCount, identCount int64
	srv.DB.Model(&models.User{}).Where("id = ?", victim.ID).Count(&userCount)
	srv.DB.Model(&models.OAuthIdentity{}).Where("user_id = ?", victim.ID).Count(&identCount)
	if userCount != 0 {
		t.Fatalf("expected user removed, got %d row", userCount)
	}
	if identCount != 0 {
		t.Fatalf("expected oauth identities removed, got %d rows", identCount)
	}
}

func itoa(u uint) string {
	const digits = "0123456789"
	if u == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = digits[u%10]
		u /= 10
	}
	return string(buf[i:])
}
