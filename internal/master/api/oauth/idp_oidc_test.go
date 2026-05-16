package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestOIDCAdapter_ExchangeCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code") != "abc" {
			t.Fatalf("code=%q", r.FormValue("code"))
		}
		if r.FormValue("client_id") != "cid" {
			t.Fatalf("cid=%q", r.FormValue("client_id"))
		}
		if r.FormValue("client_secret") != "csec" {
			t.Fatalf("csec=%q", r.FormValue("client_secret"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-1",
			"id_token":     "id-1",
			"token_type":   "Bearer",
		})
	}))
	defer srv.Close()

	cl := NewOIDCAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "cid", ClientSecret: "csec"}
	tok, err := cl.ExchangeCode(context.Background(), p, "abc", "http://example/cb")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "at-1" || tok.IDToken != "id-1" {
		t.Fatalf("got: %+v", tok)
	}
}

func TestOIDCAdapter_ExchangeCode_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_grant"}`, 400)
	}))
	defer srv.Close()
	cl := NewOIDCAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "cid", ClientSecret: "csec"}
	if _, err := cl.ExchangeCode(context.Background(), p, "abc", "http://example/cb"); err == nil {
		t.Fatal("expected error")
	}
}

func TestOIDCAdapter_FetchUserinfo(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") || auth[7:] != "at-1" {
			t.Fatalf("authz=%q", auth)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"sub": "u-1", "email": "u@e", "preferred_username": "u",
		})
	}))
	defer srv.Close()

	cl := NewOIDCAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	info, err := cl.FetchUserinfo(context.Background(), p, "at-1")
	if err != nil {
		t.Fatal(err)
	}
	if info.Sub != "u-1" {
		t.Fatalf("got %+v", info)
	}
}

func TestOIDCAdapter_FetchUserinfo_Non2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_token"}`, 401)
	}))
	defer srv.Close()
	cl := NewOIDCAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	if _, err := cl.FetchUserinfo(context.Background(), p, "at-x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestOIDCFetchUserinfo_MapsPicture(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{
			"sub": "abc",
			"email": "u@x.com",
			"preferred_username": "u",
			"name": "User One",
			"picture": "https://example.com/p.png"
		}`))
	}))
	defer srv.Close()

	a := NewOIDCAdapter(srv.Client())
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	info, err := a.FetchUserinfo(context.Background(), p, "token")
	if err != nil {
		t.Fatalf("FetchUserinfo: %v", err)
	}
	if info.Picture != "https://example.com/p.png" {
		t.Errorf("Picture mismatch: %q", info.Picture)
	}
	if info.Name != "User One" {
		t.Errorf("Name mismatch: %q", info.Name)
	}
}
