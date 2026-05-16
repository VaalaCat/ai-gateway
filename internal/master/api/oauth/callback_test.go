package oauth_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type idpStub struct {
	server   *httptest.Server
	authCode string
	userinfo map[string]any
}

func newIdPStub(t *testing.T) *idpStub {
	t.Helper()
	stub := &idpStub{
		authCode: "code-1",
		userinfo: map[string]any{"sub": "u-1", "email": "u@e", "preferred_username": "u"},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") != stub.authCode {
			http.Error(w, "bad code", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at-1", "token_type": "Bearer"})
	})
	mux.HandleFunc("/userinfo", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-1" {
			http.Error(w, "no auth", 401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(stub.userinfo)
	})
	stub.server = httptest.NewServer(mux)
	return stub
}

func seedProvider(t *testing.T, srv *master.Server, idp *idpStub) *models.OAuthProvider {
	t.Helper()
	p := &models.OAuthProvider{
		Name: "github", DisplayName: "GitHub",
		AuthorizationEndpoint: idp.server.URL + "/authorize",
		TokenEndpoint:         idp.server.URL + "/token",
		UserinfoEndpoint:      idp.server.URL + "/userinfo",
		ClientID:              "cid", ClientSecret: "csec", Scopes: "read",
		Enabled: true,
	}
	if err := srv.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func runAuthorizeAndExtractState(t *testing.T, srv *master.Server) (state, cookie string) {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/github/authorize", nil)
	req.Host = "localhost:8140"
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("authorize status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	u, _ := url.Parse(loc)
	state = u.Query().Get("state")
	for _, ck := range w.Result().Cookies() {
		if ck.Name == "oauth_state" {
			cookie = ck.Value
		}
	}
	return
}

func callCallback(t *testing.T, srv *master.Server, code, state, cookie string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("/api/oauth/github/callback?code=%s&state=%s", code, state), nil)
	req.Host = "localhost:8140"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "oauth_state", Value: cookie})
	}
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestCallback_AutoCreateChoosePage(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	idp := newIdPStub(t)
	defer idp.server.Close()
	seedProvider(t, srv, idp)

	state, cookie := runAuthorizeAndExtractState(t, srv)
	w := callCallback(t, srv, idp.authCode, state, cookie)

	if w.Code != http.StatusFound {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/choose?ticket=") {
		t.Fatalf("expected /oauth/choose redirect, got %s", loc)
	}
	var n int64
	srv.DB.Model(&models.User{}).Count(&n)
	if n != 0 {
		t.Fatalf("expected no user created at callback stage, got %d", n)
	}
}

func TestCallback_BindTicket(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "false"})
	idp := newIdPStub(t)
	defer idp.server.Close()
	seedProvider(t, srv, idp)

	state, cookie := runAuthorizeAndExtractState(t, srv)
	w := callCallback(t, srv, idp.authCode, state, cookie)

	if w.Code != http.StatusFound {
		t.Fatalf("status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/bind?ticket=") {
		t.Fatalf("loc=%s", loc)
	}
}

func TestCallback_AlreadyBound(t *testing.T) {
	srv := setupMaster(t)
	idp := newIdPStub(t)
	defer idp.server.Close()
	p := seedProvider(t, srv, idp)
	srv.DB.Create(&models.User{Username: "alice", Password: "h", PasswordSet: true, Status: 1})
	var u models.User
	srv.DB.Where("username = ?", "alice").First(&u)
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "u-1"})

	state, cookie := runAuthorizeAndExtractState(t, srv)
	w := callCallback(t, srv, idp.authCode, state, cookie)
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/success?token=") {
		t.Fatalf("loc=%s", loc)
	}
}

func TestCallback_InvalidState(t *testing.T) {
	srv := setupMaster(t)
	idp := newIdPStub(t)
	defer idp.server.Close()
	seedProvider(t, srv, idp)

	w := callCallback(t, srv, idp.authCode, "wrong", "wrong")
	loc := w.Header().Get("Location")
	if !strings.HasSuffix(loc, "/login?oauth_error=invalid_state") {
		t.Fatalf("loc=%s", loc)
	}
}

func TestCallback_ExistingUser_DoesNotOverwriteProfile(t *testing.T) {
	srv := setupMaster(t)
	idp := newIdPStub(t)
	defer idp.server.Close()
	// IdP returns different name/picture than what's stored locally
	idp.userinfo = map[string]any{
		"sub":     "u-existing",
		"email":   "z@e",
		"name":    "IdP 返回的新名字",
		"picture": "https://idp/new.png",
	}
	p := seedProvider(t, srv, idp)

	// Pre-create user with custom values
	srv.DB.Create(&models.User{
		Username:    "alice_keep",
		Password:    "h",
		PasswordSet: true,
		Status:      1,
		DisplayName: "我的自定义昵称",
		AvatarURL:   "https://my-custom/avatar.png",
	})
	var u models.User
	srv.DB.Where("username = ?", "alice_keep").First(&u)
	srv.DB.Create(&models.OAuthIdentity{UserID: u.ID, ProviderID: p.ID, Subject: "u-existing"})

	state, cookie := runAuthorizeAndExtractState(t, srv)
	w := callCallback(t, srv, idp.authCode, state, cookie)

	if w.Code != http.StatusFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/success?token=") {
		t.Fatalf("expected /oauth/success redirect, got %s", loc)
	}

	var got models.User
	srv.DB.First(&got, u.ID)
	if got.DisplayName != "我的自定义昵称" {
		t.Errorf("existing user DisplayName 被覆盖: %q", got.DisplayName)
	}
	if got.AvatarURL != "https://my-custom/avatar.png" {
		t.Errorf("existing user AvatarURL 被覆盖: %q", got.AvatarURL)
	}
}

func TestCallback_OrphanIdentityRecycles(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.Setting{Key: "oauth_auto_create", Value: "true"})
	idp := newIdPStub(t)
	defer idp.server.Close()
	p := seedProvider(t, srv, idp)
	// 直接插入 orphan identity: user_id=9999 不存在
	srv.DB.Create(&models.OAuthIdentity{UserID: 9999, ProviderID: p.ID, Subject: "u-1"})

	state, cookie := runAuthorizeAndExtractState(t, srv)
	w := callCallback(t, srv, idp.authCode, state, cookie)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/oauth/choose?ticket=") {
		t.Fatalf("expected /oauth/choose redirect, got %s", loc)
	}
	var n int64
	srv.DB.Model(&models.OAuthIdentity{}).Where("user_id = ?", 9999).Count(&n)
	if n != 0 {
		t.Fatalf("expected orphan identity removed, got %d rows", n)
	}
}
