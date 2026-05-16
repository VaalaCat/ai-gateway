package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestFeishuAdapter_ExchangeCode_Success(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("content-type=%q want application/json", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code":         0,
			"access_token": "u-at-1",
			"token_type":   "Bearer",
			"expires_in":   7200,
		})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "cli_x", ClientSecret: "sec"}
	tok, err := cl.ExchangeCode(context.Background(), p, "code-abc", "http://example/cb")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "u-at-1" {
		t.Fatalf("got %+v", tok)
	}
	if gotBody["grant_type"] != "authorization_code" || gotBody["client_id"] != "cli_x" || gotBody["code"] != "code-abc" {
		t.Fatalf("request body = %+v", gotBody)
	}
}

func TestFeishuAdapter_ExchangeCode_BizError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code":              1234,
			"error":             "invalid_code",
			"error_description": "code expired",
		})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "cli_x", ClientSecret: "sec"}
	_, err := cl.ExchangeCode(context.Background(), p, "x", "http://example/cb")
	if err == nil {
		t.Fatal("want err")
	}
	if !strings.Contains(err.Error(), "1234") || !strings.Contains(err.Error(), "invalid_code") {
		t.Fatalf("err = %v", err)
	}
}

func TestFeishuAdapter_ExchangeCode_MissingAccessToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "access_token": ""})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "x", ClientSecret: "x"}
	if _, err := cl.ExchangeCode(context.Background(), p, "x", "http://example/cb"); err == nil {
		t.Fatal("want missing access_token error")
	}
}

func TestFeishuAdapter_ExchangeCode_HTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"code":1234,"error":"unauthorized"}`, 401)
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{TokenEndpoint: srv.URL, ClientID: "x", ClientSecret: "x"}
	_, err := cl.ExchangeCode(context.Background(), p, "x", "http://example/cb")
	if err == nil {
		t.Fatal("want err")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("err should mention 401 status, got %v", err)
	}
}

func TestFeishuAdapter_FetchUserinfo_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer at-x" {
			t.Fatalf("authz=%q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0, "msg": "success",
			"data": map[string]any{
				"name":             "张三",
				"en_name":          "zhangsan",
				"open_id":          "ou-abc",
				"union_id":         "on-xyz",
				"email":            "zhangsan@feishu.cn",
				"enterprise_email": "demo@mail.com",
				"user_id":          "5d9b",
			},
		})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	info, err := cl.FetchUserinfo(context.Background(), p, "at-x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Sub != "on-xyz" {
		t.Fatalf("Sub=%q want on-xyz", info.Sub)
	}
	if info.PreferredUsername != "zhangsan" {
		t.Fatalf("PreferredUsername=%q", info.PreferredUsername)
	}
	if info.Name != "张三" {
		t.Fatalf("Name=%q", info.Name)
	}
	if info.Email != "zhangsan@feishu.cn" {
		t.Fatalf("Email=%q", info.Email)
	}
}

func TestFeishuAdapter_FetchUserinfo_BizError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"code": 99991663, "msg": "app token invalid"})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	if _, err := cl.FetchUserinfo(context.Background(), p, "at-x"); err == nil {
		t.Fatal("want err")
	} else if !strings.Contains(err.Error(), "99991663") {
		t.Fatalf("err=%v", err)
	}
}

func TestFeishuAdapter_FetchUserinfo_EmptyUnionAndEnterpriseEmailFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"code": 0,
			"data": map[string]any{
				"open_id":          "ou-x",
				"union_id":         "",
				"email":            "",
				"enterprise_email": "fallback@corp",
				"en_name":          "fb",
				"name":             "FB",
			},
		})
	}))
	defer srv.Close()

	cl := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	info, err := cl.FetchUserinfo(context.Background(), p, "at-x")
	if err != nil {
		t.Fatal(err)
	}
	if info.Sub != "" {
		t.Fatalf("Sub=%q want empty (caller handles ErrMissingSub)", info.Sub)
	}
	if info.Email != "fallback@corp" {
		t.Fatalf("Email=%q want fallback@corp", info.Email)
	}
}

func TestFeishuFetchUserinfo_MapsAvatar(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{
			"code": 0,
			"msg": "",
			"data": {
				"union_id": "u-123",
				"open_id": "o-1",
				"name": "张三",
				"en_name": "zhangsan",
				"email": "zs@example.com",
				"avatar_url": "https://feishu.example.com/avatar/u-123.png"
			}
		}`))
	}))
	defer srv.Close()

	a := NewFeishuAdapter(http.DefaultClient)
	p := &models.OAuthProvider{UserinfoEndpoint: srv.URL}
	info, err := a.FetchUserinfo(context.Background(), p, "token")
	if err != nil {
		t.Fatalf("FetchUserinfo: %v", err)
	}
	if info.Picture != "https://feishu.example.com/avatar/u-123.png" {
		t.Errorf("Picture mismatch: %q", info.Picture)
	}
	if info.Name != "张三" {
		t.Errorf("Name mismatch: %q", info.Name)
	}
}
