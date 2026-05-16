package oauth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

func setupMaster(t *testing.T) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	srv, err := master.New(&config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32), PublicBaseURLs: []string{"http://localhost:8140"},
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}, logger)
	if err != nil {
		t.Fatal(err)
	}
	return srv
}

func TestListPublicProviders(t *testing.T) {
	srv := setupMaster(t)
	srv.DB.Create(&models.OAuthProvider{Name: "github", DisplayName: "GitHub", Enabled: true, IconURL: "x.png"})
	offP := &models.OAuthProvider{Name: "off", DisplayName: "Off"}
	srv.DB.Create(offP)
	srv.DB.Model(offP).UpdateColumn("enabled", false)

	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/oauth/providers", nil)
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	json.Unmarshal(w.Body.Bytes(), &got)
	if len(got) != 1 || got[0]["name"] != "github" {
		t.Fatalf("got %+v", got)
	}
	if _, has := got[0]["client_secret"]; has {
		t.Fatal("public list must not expose client_secret")
	}
}
