package oauth_provider_admin_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"go.uber.org/zap"
)

func setupMaster(t *testing.T) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), PublicBaseURLs: []string{"http://localhost:8140"},
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := master.New(cfg, logger)
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	srv.InitAdminUser("admin", "admin123")
	return srv
}

func adminToken(t *testing.T, srv *master.Server) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["token"]
}

func doJSON(t *testing.T, srv *master.Server, jwt, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf []byte
	if body != nil {
		buf, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(method, path, bytes.NewReader(buf))
	req.Header.Set("Content-Type", "application/json")
	if jwt != "" {
		req.Header.Set("Authorization", "Bearer "+jwt)
	}
	srv.Router.ServeHTTP(w, req)
	return w
}

func TestOAuthProviderAdminCRUD(t *testing.T) {
	srv := setupMaster(t)
	jwt := adminToken(t, srv)

	w := doJSON(t, srv, jwt, "POST", "/api/admin/oauth-providers", map[string]any{
		"name": "github", "display_name": "GitHub",
		"authorization_endpoint": "https://example/authorize",
		"token_endpoint":         "https://example/token",
		"userinfo_endpoint":      "https://example/userinfo",
		"client_id":              "cid", "client_secret": "csec", "scopes": "read:user",
		"enabled": true,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	if got := created["client_secret"]; got != "***" {
		t.Fatalf("expected masked, got %v", got)
	}
	id := uintIDFrom(created)

	// list
	w = doJSON(t, srv, jwt, "GET", "/api/admin/oauth-providers", nil)
	if w.Code != 200 {
		t.Fatal(w.Code)
	}

	// update without client_secret should keep old
	w = doJSON(t, srv, jwt, "PUT", "/api/admin/oauth-providers/"+uintToStr(id), map[string]any{
		"display_name": "GitHub.com",
	})
	if w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}

	// delete
	w = doJSON(t, srv, jwt, "DELETE", "/api/admin/oauth-providers/"+uintToStr(id), nil)
	if w.Code != 200 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
}

func uintIDFrom(m map[string]any) uint {
	if v, ok := m["id"].(float64); ok {
		return uint(v)
	}
	return 0
}
func uintToStr(u uint) string {
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

func TestOAuthProviderAdmin_Protocol_Defaults(t *testing.T) {
	srv := setupMaster(t)
	jwt := adminToken(t, srv)

	w := doJSON(t, srv, jwt, "POST", "/api/admin/oauth-providers", map[string]any{
		"name": "github-default", "display_name": "GitHub",
		"authorization_endpoint": "https://example/authorize",
		"token_endpoint":         "https://example/token",
		"userinfo_endpoint":      "https://example/userinfo",
		"client_id":              "cid", "client_secret": "csec",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["protocol"] != "oidc" {
		t.Fatalf("default protocol = %v, want oidc", got["protocol"])
	}
}

func TestOAuthProviderAdmin_Protocol_Feishu(t *testing.T) {
	srv := setupMaster(t)
	jwt := adminToken(t, srv)

	w := doJSON(t, srv, jwt, "POST", "/api/admin/oauth-providers", map[string]any{
		"name": "bytedance-lark", "display_name": "飞书", "protocol": "feishu",
		"authorization_endpoint": "https://accounts.feishu.cn/open-apis/authen/v1/authorize",
		"token_endpoint":         "https://open.feishu.cn/open-apis/authen/v2/oauth/token",
		"userinfo_endpoint":      "https://open.feishu.cn/open-apis/authen/v1/user_info",
		"client_id":              "cli_x", "client_secret": "sec",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create feishu: %d %s", w.Code, w.Body.String())
	}
	var got map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if got["protocol"] != "feishu" {
		t.Fatalf("protocol = %v, want feishu", got["protocol"])
	}
}

func TestOAuthProviderAdmin_Protocol_Invalid(t *testing.T) {
	srv := setupMaster(t)
	jwt := adminToken(t, srv)

	w := doJSON(t, srv, jwt, "POST", "/api/admin/oauth-providers", map[string]any{
		"name": "bad", "display_name": "Bad", "protocol": "dingding",
		"authorization_endpoint": "https://x/a", "token_endpoint": "https://x/t",
		"userinfo_endpoint":      "https://x/u", "client_id": "c", "client_secret": "s",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid protocol: want 400, got %d %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "invalid protocol") {
		t.Fatalf("err body = %s", w.Body.String())
	}
}

func TestOAuthProviderAdmin_Update_RejectsReadonlyFields(t *testing.T) {
	srv := setupMaster(t)
	jwt := adminToken(t, srv)

	w := doJSON(t, srv, jwt, "POST", "/api/admin/oauth-providers", map[string]any{
		"name": "github", "display_name": "GitHub",
		"authorization_endpoint": "https://example/authorize",
		"token_endpoint":         "https://example/token",
		"userinfo_endpoint":      "https://example/userinfo",
		"client_id":              "cid", "client_secret": "csec",
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", w.Code, w.Body.String())
	}
	var created map[string]any
	json.Unmarshal(w.Body.Bytes(), &created)
	id := uintIDFrom(created)

	cases := []struct {
		field string
		value any
	}{
		{"created_at", 1778836221},
		{"updated_at", 1778836221},
		{"name", "renamed"},
		{"protocol", "feishu"},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			w := doJSON(t, srv, jwt, "PUT", "/api/admin/oauth-providers/"+uintToStr(id), map[string]any{
				"display_name": "GitHub2",
				c.field:        c.value,
			})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("want 400 for %s, got %d %s", c.field, w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "unknown field: "+c.field) {
				t.Fatalf("err body for %s = %s", c.field, w.Body.String())
			}
		})
	}
}
