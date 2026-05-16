package oauth_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestLink_HappyPath(t *testing.T) {
	srv := setupMaster(t)
	idp := newIdPStub(t)
	defer idp.server.Close()
	p := seedProvider(t, srv, idp)
	srv.DB.Create(&models.User{Username: "alice", Password: "h", PasswordSet: true, Status: 1})
	var u models.User
	srv.DB.Where("username = ?", "alice").First(&u)

	tk, _ := oauth.SignLinkTicket(srv.Cfg.Master.JWTSecret, oauth.LinkTicketClaims{
		UserID:    u.ID,
		ExpiresAt: 9999999999,
	})

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/github/link?ticket="+url.QueryEscape(tk), nil)
	req.Host = "localhost:8140"
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusFound {
		t.Fatalf("status %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, idp.server.URL+"/authorize?") {
		t.Fatalf("loc=%s", loc)
	}
	_ = p
}

func TestLink_BadTicket(t *testing.T) {
	srv := setupMaster(t)
	idp := newIdPStub(t)
	defer idp.server.Close()
	seedProvider(t, srv, idp)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/github/link?ticket=bad", nil)
	srv.Router.ServeHTTP(w, req)
	loc := w.Header().Get("Location")
	if loc != "/profile?oauth_error=ticket_invalid" {
		t.Fatalf("loc=%s", loc)
	}
}
