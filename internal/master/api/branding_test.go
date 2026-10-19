package api_test

import (
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestSiteBrandingAdminWriteAndPublicProjection(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	user := models.User{Username: "branding-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	payload := map[string]any{"settings": map[string]string{
		"site_name":       " Custom Gateway ",
		"site_title":      "Custom Browser Title",
		"site_icon_url":   "https://cdn.example.com/logo.svg?v=3#logo",
		"welcome_title":   "Welcome <b>friend</b>",
		"welcome_message": "Welcome\nto the gateway",
	}}
	for _, caller := range []struct {
		name   string
		token  string
		status int
	}{
		{name: "anonymous", status: http.StatusUnauthorized},
		{name: "non-admin", token: userJWT, status: http.StatusForbidden},
	} {
		t.Run(caller.name, func(t *testing.T) {
			for _, method := range []string{http.MethodGet, http.MethodPut} {
				response := reqHelper(srv, caller.token, method, "/api/admin/system/settings", payload)
				require.Equal(t, caller.status, response.Code, response.Body.String())
			}
		})
	}
	public := reqHelper(srv, "", http.MethodGet, "/api/system/public-config", nil)
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	require.Equal(t, "AI Gateway", jsonBody(t, public)["site_name"], "unauthorized attempts must not change branding")

	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	invalid := reqHelper(srv, adminJWT, http.MethodPut, "/api/admin/system/settings", map[string]any{
		"settings": map[string]string{"site_icon_url": "javascript:alert(1)", "site_name": "Rejected"},
	})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	saved := reqHelper(srv, adminJWT, http.MethodPut, "/api/admin/system/settings", payload)
	require.Equal(t, http.StatusOK, saved.Code, saved.Body.String())
	require.NoError(t, srv.DB.Create(&models.Setting{Key: "custom_private_key", Value: "never-public-secret"}).Error)
	public = reqHelper(srv, "", http.MethodGet, "/api/system/public-config", nil)
	require.Equal(t, http.StatusOK, public.Code, public.Body.String())
	body := jsonBody(t, public)
	require.Equal(t, "Custom Gateway", body["site_name"])
	require.Equal(t, "Custom Browser Title", body["site_title"])
	require.Equal(t, "https://cdn.example.com/logo.svg?v=3#logo", body["site_icon_url"])
	require.Equal(t, "Welcome <b>friend</b>", body["welcome_title"])
	require.Equal(t, "Welcome\nto the gateway", body["welcome_message"])
	require.Len(t, body, 8)
	require.NotContains(t, public.Body.String(), "never-public-secret")
	require.NotContains(t, body, "custom_private_key")
	require.NotContains(t, body, "settings")
	var stored models.Setting
	require.NoError(t, srv.DB.Where("key = ?", "site_name").First(&stored).Error)
	require.Equal(t, "Custom Gateway", stored.Value)
}
