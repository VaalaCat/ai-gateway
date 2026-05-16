package oauth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestAuthorize_Redirects(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.OAuthProvider{
		Name:                  "github",
		DisplayName:           "GitHub",
		AuthorizationEndpoint: "https://idp.example/authorize",
		TokenEndpoint:         "https://idp.example/token",
		UserinfoEndpoint:      "https://idp.example/userinfo",
		ClientID:              "cid",
		ClientSecret:          "csec",
		Scopes:                "read:user",
		Enabled:               true,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/github/authorize", nil)
	req.Host = "localhost:8140"
	srv.Router.ServeHTTP(w, req)

	if w.Code != http.StatusFound {
		t.Fatalf("status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example/authorize?") {
		t.Fatalf("location: %s", loc)
	}
	if !strings.Contains(loc, "client_id=cid") || !strings.Contains(loc, "redirect_uri=") || !strings.Contains(loc, "state=") {
		t.Fatalf("missing query: %s", loc)
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 || cookies[0].Name != "oauth_state" {
		t.Fatalf("missing oauth_state cookie: %+v", cookies)
	}
}

func TestAuthorize_UnknownProvider(t *testing.T) {
	srv := setupMaster(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/nope/authorize", nil)
	req.Host = "localhost:8140"
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.HasSuffix(loc, "/login?oauth_error=unknown_provider") {
		t.Fatalf("loc: %s", loc)
	}
}

func TestAuthorize_DisabledProvider(t *testing.T) {
	// Regression: provider exists in DB but Enabled=false should redirect
	// to /login?oauth_error=unknown_provider, NOT panic on nil-deref.
	srv := setupMaster(t)
	p := &models.OAuthProvider{
		Name:                  "github",
		DisplayName:           "GitHub",
		AuthorizationEndpoint: "https://idp.example/authorize",
		TokenEndpoint:         "https://idp.example/token",
		UserinfoEndpoint:      "https://idp.example/userinfo",
		ClientID:              "cid",
		ClientSecret:          "csec",
	}
	srv.DB.Create(p)
	srv.DB.Model(p).UpdateColumn("enabled", false)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/github/authorize", nil)
	req.Host = "localhost:8140"
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.HasSuffix(loc, "/login?oauth_error=unknown_provider") {
		t.Fatalf("loc: %s", loc)
	}
}
