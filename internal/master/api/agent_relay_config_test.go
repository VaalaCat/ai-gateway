package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	apiagent "github.com/VaalaCat/ai-gateway/internal/master/api/agent"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestSetupTestMasterClosesOwnedDatabaseAfterTestCleanup(t *testing.T) {
	var ping func() error
	t.Run("fixture", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.True(t, filepath.IsAbs(srv.Cfg.Agent.CredentialsFile),
			"test credentials file %q must use an isolated absolute path", srv.Cfg.Agent.CredentialsFile)
		sqlDB, err := srv.DB.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Ping())
		ping = sqlDB.Ping
	})

	require.NotNil(t, ping)
	require.Error(t, ping())
}

func TestAgentUpdateRouteUsesTypedPatchAndPreservesOmittedProxy(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("relay-admin", "admin123"))
	token := loginAsAdmin(t, srv, "relay-admin", "admin123")
	agent := models.Agent{
		AgentID:   "relay-route-agent",
		Name:      "before",
		Status:    1,
		ProxyURL:  "http://proxy.example",
		RelayMode: "custom",
		RelayURI:  "wss://relay.example/tunnel?token=secret",
	}
	require.NoError(t, srv.DB.Create(&agent).Error)

	request := func(body map[string]any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		require.NoError(t, err)
		w := httptest.NewRecorder()
		req := httptest.NewRequest(
			http.MethodPut,
			"/api/admin/agents/"+strconv.Itoa(int(agent.ID)),
			bytes.NewReader(raw),
		)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, req)
		return w
	}

	omitted := request(map[string]any{"name": "after"})
	require.Equal(t, http.StatusOK, omitted.Code, omitted.Body.String())
	var stored models.Agent
	require.NoError(t, srv.DB.First(&stored, agent.ID).Error)
	require.Equal(t, "after", stored.Name)
	require.Equal(t, "http://proxy.example", stored.ProxyURL)
	require.Equal(t, "custom", stored.RelayMode)
	require.Equal(t, "wss://relay.example/tunnel?token=secret", stored.RelayURI)

	cleared := request(map[string]any{"proxy_url": ""})
	require.Equal(t, http.StatusOK, cleared.Code, cleared.Body.String())
	require.NoError(t, srv.DB.First(&stored, agent.ID).Error)
	require.Empty(t, stored.ProxyURL)
}

func TestAgentListEditCompatibilityAndDetailRelayConfiguration(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("relay-list-admin", "admin123"))
	token := loginAsAdmin(t, srv, "relay-list-admin", "admin123")
	agent := models.Agent{
		AgentID:       "relay-list-agent",
		Secret:        "database-secret",
		Name:          "before",
		Status:        1,
		HTTPAddresses: `[{"url":"http://127.0.0.1:9000","tag":"manual"}]`,
		ProxyURL:      "http://proxy.example",
		RelayMode:     "custom",
		RelayURI:      "wss://relay.example/tunnel?token=query-secret",
	}
	require.NoError(t, srv.DB.Create(&agent).Error)

	request := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			require.NoError(t, err)
		}
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, req)
		return w
	}

	updated := request(http.MethodPut, "/api/admin/agents/"+strconv.Itoa(int(agent.ID)), map[string]any{"name": "after"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())

	listed := request(http.MethodGet, "/api/admin/agents", nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	require.NotContains(t, listed.Body.String(), "relay_uri")
	require.NotContains(t, listed.Body.String(), "query-secret")
	var listResponse struct {
		Data []apiagent.AgentResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &listResponse))
	require.Len(t, listResponse.Data, 1)
	require.Equal(t, "after", listResponse.Data[0].Name)
	require.Equal(t, "http://proxy.example", listResponse.Data[0].ProxyURL)
	require.Equal(t, "custom", listResponse.Data[0].RelayMode)

	detailed := request(http.MethodGet, "/api/admin/agents/"+strconv.Itoa(int(agent.ID))+"/detail", nil)
	require.Equal(t, http.StatusOK, detailed.Code, detailed.Body.String())
	var detail apiagent.AgentDetailResponse
	require.NoError(t, json.Unmarshal(detailed.Body.Bytes(), &detail))
	require.Empty(t, detail.Secret)
	require.Equal(t, "http://proxy.example", detail.ProxyURL)
	require.Equal(t, "custom", detail.RelayMode)
	require.Equal(t, "wss://relay.example/tunnel?token=query-secret", detail.RelayURI)
	require.NotEmpty(t, detail.Connection.SnapshotEpoch)
	require.NotNil(t, detail.RouteTargets.Data)
	require.Equal(t, agent.HTTPAddresses, detail.ConfiguredHTTPAddresses)
	require.Equal(t, agent.HTTPAddresses, detail.EffectiveHTTPAddresses)
}
