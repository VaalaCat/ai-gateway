package oauth_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func postRegister(t *testing.T, srv *master.Server, ticket string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ticket": ticket})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/oauth/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestRegister_Success(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-new", Email: "u@e", DisplayName: "U",
		SuggestedUsername: "u", ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})

	w := postRegister(t, srv, tk)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp["token"] == "" {
		t.Fatal("no token")
	}

	var ident models.OAuthIdentity
	srv.DB.Where("subject = ?", "sub-new").First(&ident)
	if ident.UserID == 0 {
		t.Fatal("identity not created")
	}
	var u models.User
	srv.DB.First(&u, ident.UserID)
	if u.Email != "u@e" {
		t.Fatalf("expected email written, got %q", u.Email)
	}
	if u.PasswordSet {
		t.Fatal("auto-created user should have PasswordSet=false")
	}
}

func TestRegister_TicketInvalid(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})

	w := postRegister(t, srv, "garbage")
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRegister_AutoCreateDisabled(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "false"})
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-x",
		ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})
	w := postRegister(t, srv, tk)
	if w.Code != 403 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRegister_AlreadyBound(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	srv.DB.Create(&models.User{Username: "existing", Password: "h", PasswordSet: true, Status: 1})
	var u models.User
	srv.DB.Where("username = ?", "existing").First(&u)
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: 1, Subject: "sub-x"})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-x",
		ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})
	w := postRegister(t, srv, tk)
	if w.Code != 409 {
		t.Fatalf("status %d", w.Code)
	}
}

func TestRegister_WritesDisplayNameAndAvatar(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID:        1,
		Subject:           "sub-dn",
		Email:             "zs@e",
		DisplayName:       "张三",
		Picture:           "https://example.com/avatar/zs.png",
		SuggestedUsername: "zs",
		ExpiresAt:         time.Now().Add(2 * time.Minute).Unix(),
	})

	w := postRegister(t, srv, tk)
	if w.Code != 200 {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}

	var ident models.OAuthIdentity
	if err := srv.DB.Where("subject = ?", "sub-dn").First(&ident).Error; err != nil {
		t.Fatalf("identity not created: %v", err)
	}
	var u models.User
	if err := srv.DB.First(&u, ident.UserID).Error; err != nil {
		t.Fatalf("user not found: %v", err)
	}
	if u.DisplayName != "张三" {
		t.Errorf("DisplayName mismatch: %q", u.DisplayName)
	}
	if u.AvatarURL != "https://example.com/avatar/zs.png" {
		t.Errorf("AvatarURL mismatch: %q", u.AvatarURL)
	}
}

func TestRegister_EmailConflict(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GH", Enabled: true})
	// 已存在持有 email 的本地账号
	srv.DB.Create(&models.User{Username: "existing", Email: "u@e", Password: "x", Status: 1, GroupID: 1})

	tk, _ := oauth.SignBindTicket(srv.Cfg.Master.JWTSecret, oauth.BindTicketClaims{
		ProviderID: 1, Subject: "sub-new", Email: "U@E", DisplayName: "U",
		SuggestedUsername: "u", ExpiresAt: time.Now().Add(2 * time.Minute).Unix(),
	})

	w := postRegister(t, srv, tk)
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var ident models.OAuthIdentity
	srv.DB.Where("subject = ?", "sub-new").First(&ident)
	if ident.UserID == 0 {
		t.Fatal("identity not created")
	}
	var newUser models.User
	srv.DB.First(&newUser, ident.UserID)
	if newUser.Email != "" {
		t.Fatalf("expected empty email on conflict, got %q", newUser.Email)
	}
}
