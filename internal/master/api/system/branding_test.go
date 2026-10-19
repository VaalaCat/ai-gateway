package system

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/stretchr/testify/require"
)

func TestBrandingSettingsDefaultsAndPublicProjection(t *testing.T) {
	c := newSettingsContext(t)
	h := &Handler{}
	defaults := map[string]string{
		"site_name":       "AI Gateway",
		"site_title":      "AI Gateway",
		"site_icon_url":   "",
		"welcome_title":   "",
		"welcome_message": "",
	}
	response, err := h.GetSettings(c, GetSettingsRequest{})
	require.NoError(t, err)
	for key, value := range defaults {
		require.Equal(t, value, settingDefs[key].Default, key)
		require.Equal(t, value, response.Settings[key], key)
		require.NotContains(t, settings.Defaults(), key)
	}
	// Private and unrelated settings must never be included in the public DTO.
	require.NoError(t, c.App.GetCoreDB().Create(&models.Setting{Key: "oauth_client_secret", Value: "private-client-secret"}).Error)
	require.NoError(t, c.App.GetCoreDB().Create(&models.Setting{Key: "proxy_url", Value: "https://user:private-password@proxy.example"}).Error)
	public, err := h.PublicConfig(c, api.EmptyRequest{})
	require.NoError(t, err)
	encoded, err := json.Marshal(public)
	require.NoError(t, err)
	var body map[string]any
	require.NoError(t, json.Unmarshal(encoded, &body))
	require.Len(t, body, 8)
	for key, value := range defaults {
		require.Equal(t, value, body[key], key)
	}
	require.NotContains(t, string(encoded), "private-")
	require.NotContains(t, body, "settings")
	require.NotContains(t, body, "oauth_client_secret")
	require.NotContains(t, body, "proxy_url")
}

func TestBrandingSettingsPersistAndImmediatelyUpdatePublicConfig(t *testing.T) {
	c, bus := newSettingsContextWithBus(t)
	h := &Handler{}
	values := map[string]string{
		"site_name":       "  网关 🚀  ",
		"site_title":      "\tCustom Gateway\n",
		"site_icon_url":   "/assets/logo.svg?version=2&theme=light#brand",
		"welcome_title":   "欢迎 <b>朋友</b>",
		"welcome_message": "第一行\n<script>alert('plain text')</script>\n最后一行\n",
	}
	response, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: values})
	require.NoError(t, err)
	want := map[string]string{
		"site_name":       "网关 🚀",
		"site_title":      "Custom Gateway",
		"site_icon_url":   values["site_icon_url"],
		"welcome_title":   values["welcome_title"],
		"welcome_message": values["welcome_message"],
	}
	for key, value := range want {
		require.Equal(t, value, response.Settings[key], key)
		var stored models.Setting
		require.NoError(t, c.App.GetCoreDB().Where("key = ?", key).First(&stored).Error)
		require.Equal(t, value, stored.Value, key)
	}
	require.Empty(t, bus.events, "master-only branding must not be published to agents")
	assertPublic := func() {
		t.Helper()
		public, err := h.PublicConfig(c, api.EmptyRequest{})
		require.NoError(t, err)
		require.Equal(t, want["site_name"], public.SiteName)
		require.Equal(t, want["site_title"], public.SiteTitle)
		require.Equal(t, want["site_icon_url"], public.SiteIconURL)
		require.Equal(t, want["welcome_title"], public.WelcomeTitle)
		require.Equal(t, want["welcome_message"], public.WelcomeMessage)
	}
	assertPublic()
	// Recreate the application using persisted records, not the write response.
	restarted := app.NewApplication()
	restarted.SetCoreDB(c.App.GetCoreDB())
	require.NoError(t, SeedMasterSettingsSnapshot(restarted))
	c.App = restarted
	assertPublic()
	_, err = h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		"site_name": "AI Gateway", "site_title": "AI Gateway", "site_icon_url": "", "welcome_title": "", "welcome_message": "",
	}})
	require.NoError(t, err)
	public, err := h.PublicConfig(c, api.EmptyRequest{})
	require.NoError(t, err)
	require.Equal(t, "AI Gateway", public.SiteName)
	require.Equal(t, "AI Gateway", public.SiteTitle)
	require.Empty(t, public.SiteIconURL)
	require.Empty(t, public.WelcomeTitle)
	require.Empty(t, public.WelcomeMessage)
}

func TestBrandingSettingsUnicodeCharacterLimits(t *testing.T) {
	for key, maximum := range map[string]int{"site_name": 100, "site_title": 200, "welcome_title": 200, "welcome_message": 2000} {
		t.Run(key, func(t *testing.T) {
			c := newSettingsContext(t)
			h := &Handler{}
			boundary := strings.Repeat("界", maximum)
			_, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{key: boundary}})
			require.NoError(t, err)
			for _, invalid := range []string{boundary + "🚀", "invalid\xff"} {
				_, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{key: invalid}})
				require.True(t, isBadRequest(err), "%q: %v", invalid, err)
			}
			var stored models.Setting
			require.NoError(t, c.App.GetCoreDB().Where("key = ?", key).First(&stored).Error)
			require.Equal(t, boundary, stored.Value)
			_, err = h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{key: " \n\t "}})
			if key == "site_name" || key == "site_title" {
				require.True(t, isBadRequest(err))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestBrandingSettingsIconURLValidation(t *testing.T) {
	valid := []string{
		"", "/", "/favicon.ico", "/assets/图标.svg?v=2&label=hello%20world#brand",
		"https://cdn.example.com/icon.svg?v=2#logo", "http://localhost:18140/icon.png",
		"https://[::1]:18140/icon.png", "https://cdn.example.com/logo%20small.png",
	}
	for _, icon := range valid {
		t.Run("accept "+icon, func(t *testing.T) {
			c := newSettingsContext(t)
			_, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{"site_icon_url": icon}})
			require.NoError(t, err)
			public, err := (&Handler{}).PublicConfig(c, api.EmptyRequest{})
			require.NoError(t, err)
			require.Equal(t, icon, public.SiteIconURL)
		})
	}
	invalid := []string{
		"//evil.example/icon.png", "///evil.example/icon.png", "/%2fevil.example/icon.png",
		"javascript:alert(1)", "data:image/svg+xml,<svg/>", "ftp://example.com/icon.png",
		"icon.png", "?icon=true", "#icon", "https:icon.png", "https:///icon.png", "https://",
		"https://user:secret@example.com/icon.png", "https://@example.com/icon.png",
		"https://example.com\\@evil.example/icon.png", "/\\evil.example/icon.png",
		"/icon%5c.png", "https://example.com/icon.svg?x=%5C", "/icon.svg?x=%0a", "/icon%00.svg",
		"https://example.com/icon.svg\n", "\thttps://example.com/icon.svg", "/icon.svg#\u0085",
		" https://example.com/icon.png", "https://example.com/icon small.png",
		"https://example.com/%zz", "https://example.com/icon?x=%zz", "https://example.com:bad/icon.png", "/invalid\xff",
	}
	for _, icon := range invalid {
		t.Run("reject "+icon, func(t *testing.T) {
			c, bus := newSettingsContextWithBus(t)
			_, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
				"site_icon_url": icon, "site_name": "Not committed",
			}})
			require.True(t, isBadRequest(err), "icon=%q: %v", icon, err)
			require.EqualError(t, err, "invalid value for site_icon_url")
			var count int64
			require.NoError(t, c.App.GetCoreDB().Model(&models.Setting{}).Count(&count).Error)
			require.Zero(t, count)
			require.Empty(t, bus.events)
		})
	}
}
