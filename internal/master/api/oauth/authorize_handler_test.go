package oauth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

func newServerWithProviders(t *testing.T, urls []string) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	srv, err := master.New(&config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
			PublicBaseURLs: urls,
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	srv.DB.Create(&models.OAuthProvider{
		Name:                  "demo",
		DisplayName:           "Demo",
		AuthorizationEndpoint: "https://idp.example.com/authorize",
		TokenEndpoint:         "https://idp.example.com/token",
		UserinfoEndpoint:      "https://idp.example.com/userinfo",
		ClientID:              "client123",
		Scopes:                "openid profile",
		Enabled:               true,
	})
	return srv
}

func parseLocation(t *testing.T, w *httptest.ResponseRecorder) *url.URL {
	t.Helper()
	loc := w.Header().Get("Location")
	if loc == "" {
		t.Fatalf("missing Location: %d %s", w.Code, w.Body.String())
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestHandleAuthorize_MultiHostHits(t *testing.T) {
	srv := newServerWithProviders(t, []string{
		"https://a.example.com",
		"https://b.example.com",
	})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/api/oauth/demo/authorize", nil)
	r.Host = "a.example.com"
	r.Header.Set("X-Forwarded-Host", "b.example.com")
	r.Header.Set("X-Forwarded-Proto", "https")
	srv.Router.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	u := parseLocation(t, w)
	got := u.Query().Get("redirect_uri")
	if got != "https://b.example.com/api/oauth/demo/callback" {
		t.Fatalf("redirect_uri = %q", got)
	}
}

func TestHandleAuthorize_UntrustedOrigin(t *testing.T) {
	srv := newServerWithProviders(t, []string{"https://a.example.com"})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/api/oauth/demo/authorize", nil)
	r.Host = "evil.example.com"
	srv.Router.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "/login?oauth_error=untrusted_origin") {
		t.Fatalf("loc=%q", loc)
	}
	for _, h := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(h, "oauth_state=") {
			t.Fatalf("untrusted origin must not set oauth_state cookie, got %q", h)
		}
	}
}

func TestHandleAuthorize_EmptyAllowlistFallback(t *testing.T) {
	srv := newServerWithProviders(t, nil)
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodGet, "/api/oauth/demo/authorize", nil)
	r.Host = "anything.example.com"
	srv.Router.ServeHTTP(w, r)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	u := parseLocation(t, w)
	got := u.Query().Get("redirect_uri")
	if got != "http://anything.example.com/api/oauth/demo/callback" {
		t.Fatalf("redirect_uri = %q (expected fallback)", got)
	}
}

func TestHandleAuthorize_CookieSecureMatchesOrigin(t *testing.T) {
	srv := newServerWithProviders(t, []string{
		"https://secure.example.com",
		"http://plain.example.com",
	})

	t.Run("https origin sets Secure", func(t *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(http.MethodGet, "/api/oauth/demo/authorize", nil)
		r.Host = "secure.example.com"
		r.Header.Set("X-Forwarded-Proto", "https")
		srv.Router.ServeHTTP(w, r)
		if !cookieHas(w, "oauth_state", "Secure") {
			t.Fatalf("expected Secure on https origin, headers=%v", w.Header())
		}
	})

	t.Run("http origin omits Secure", func(t *testing.T) {
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(http.MethodGet, "/api/oauth/demo/authorize", nil)
		r.Host = "plain.example.com"
		srv.Router.ServeHTTP(w, r)
		if cookieHas(w, "oauth_state", "Secure") {
			t.Fatalf("Secure must not be set on http origin")
		}
	})
}

func cookieHas(w *httptest.ResponseRecorder, name, attr string) bool {
	for _, h := range w.Header().Values("Set-Cookie") {
		if !strings.HasPrefix(h, name+"=") {
			continue
		}
		if strings.Contains(h, "; "+attr) {
			return true
		}
	}
	return false
}
