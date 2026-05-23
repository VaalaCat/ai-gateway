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

// Deleting a user must also cascade-clear their BYOK private_channels and
// any share rows whose target is that user. Leaving them behind would orphan
// encrypted key ciphertext — a GDPR/SOC2 violation.
func TestUserDelete_CascadesPrivateChannelsAndShares(t *testing.T) {
	srv := setupMasterForUserAdmin(t)
	admin := &models.User{Username: "admin", Password: "x", Role: consts.RoleAdmin, Status: consts.StatusEnabled, GroupID: 1}
	srv.DB.Create(admin)
	victim := &models.User{Username: "victim", Password: "x", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	srv.DB.Create(victim)
	survivor := &models.User{Username: "survivor", Password: "x", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	srv.DB.Create(survivor)

	// victim owns two BYOK channels with encrypted key material
	victimCh1 := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: victim.ID, Name: "v-1", Status: 1, KeyCipher: []byte("cipher1"), KeyLast4: "aaaa"}
	srv.DB.Create(victimCh1)
	victimCh2 := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: victim.ID, Name: "v-2", Status: 1, KeyCipher: []byte("cipher2"), KeyLast4: "bbbb"}
	srv.DB.Create(victimCh2)
	// survivor owns an unrelated channel — must remain
	survivorCh := &models.PrivateChannel{ChannelCore: models.ChannelCore{Type: 1}, OwnerID: survivor.ID, Name: "s-1", Status: 1, KeyCipher: []byte("cipher3"), KeyLast4: "cccc"}
	srv.DB.Create(survivorCh)

	// victim is a share *target* of survivor's channel — that row must be cleared too
	srv.DB.Create(&models.PrivateChannelShare{ChannelID: survivorCh.ID, TargetType: models.PrivateShareTargetUser, TargetID: victim.ID})
	// unrelated share row to a different user — must remain
	srv.DB.Create(&models.PrivateChannelShare{ChannelID: survivorCh.ID, TargetType: models.PrivateShareTargetUser, TargetID: admin.ID})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("DELETE", "/api/admin/users/"+itoa(victim.ID), nil)
	req.Header.Set("Authorization", "Bearer "+adminToken(t, srv, admin.ID))
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	// victim's user row gone
	var userCount int64
	srv.DB.Model(&models.User{}).Where("id = ?", victim.ID).Count(&userCount)
	if userCount != 0 {
		t.Fatalf("victim user not deleted; rows=%d", userCount)
	}
	// victim's private channels gone (encrypted ciphertext purged)
	var victimChCount int64
	srv.DB.Model(&models.PrivateChannel{}).Where("owner_id = ?", victim.ID).Count(&victimChCount)
	if victimChCount != 0 {
		t.Fatalf("victim private channels not cascaded; rows=%d", victimChCount)
	}
	// share row where victim was the target gone
	var victimShareCount int64
	srv.DB.Model(&models.PrivateChannelShare{}).
		Where("target_type = ? AND target_id = ?", models.PrivateShareTargetUser, victim.ID).
		Count(&victimShareCount)
	if victimShareCount != 0 {
		t.Fatalf("victim share-target rows not cascaded; rows=%d", victimShareCount)
	}
	// survivor's own channel untouched
	var survivorChCount int64
	srv.DB.Model(&models.PrivateChannel{}).Where("id = ?", survivorCh.ID).Count(&survivorChCount)
	if survivorChCount != 1 {
		t.Fatalf("survivor channel wrongly affected; rows=%d", survivorChCount)
	}
	// unrelated share row to admin untouched
	var adminShareCount int64
	srv.DB.Model(&models.PrivateChannelShare{}).
		Where("target_type = ? AND target_id = ?", models.PrivateShareTargetUser, admin.ID).
		Count(&adminShareCount)
	if adminShareCount != 1 {
		t.Fatalf("unrelated admin share row wrongly affected; rows=%d", adminShareCount)
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
