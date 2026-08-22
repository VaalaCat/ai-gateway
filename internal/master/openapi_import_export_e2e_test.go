package master

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	apiaccessgrant "github.com/VaalaCat/ai-gateway/internal/master/api/api_access_grant"
	apiopenapi "github.com/VaalaCat/ai-gateway/internal/master/api/api_openapi"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const openAPIE2ETokenKey = "sk-openapi-e2e-token"

type openAPIE2EFixture struct {
	server   *Server
	baseURL  string
	adminJWT string
	userJWT  string
	user     models.User
	token    models.Token
}

type openAPIE2EHTTPResponse struct {
	status int
	body   []byte
}

// Breaks caught: an imported route graph that is absent from the embedded
// Agent, a route-only grant that leaks sibling routes, a platform edit that is
// omitted from export, or an export that cannot be imported again.
func TestOpenAPIImportExportCatalogE2E(t *testing.T) {
	var providerMu sync.Mutex
	providerRequests := make([]string, 0, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerMu.Lock()
		providerRequests = append(providerRequests, request.Method+" "+request.URL.RequestURI())
		providerMu.Unlock()
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(response, `{"source":"users-provider"}`)
	}))
	t.Cleanup(provider.Close)
	fixture := newOpenAPIE2EFixture(t)

	imported := fixture.importDocument(t, "people-e2e", openAPIE2EUsersDocument(provider.URL))
	routes := fixture.routesBySlug(t, imported.ServiceID)
	require.ElementsMatch(t, []string{"orders", "users"}, sortedOpenAPIE2EMapKeys(routes))
	importedDocument := fixture.openAPIDocument(t, imported.ServiceID)
	require.Equal(t, "3.1.0", openAPIE2EDocumentVersion(t, importedDocument.Export))
	require.ElementsMatch(t, []string{"/orders", "/users", "/users/{id}"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, importedDocument.Export)))
	fixture.grantRoutes(t, imported.ServiceID, routes["users"].ID)
	fixture.requireAgentRoute(t, "people-e2e", "users", consts.StatusEnabled)

	t.Run("Agent invocation uses the imported users route", func(t *testing.T) {
		response := fixture.request(t, http.MethodGet, "/v1/api/people-e2e/users/42?expand=profile", openAPIE2ETokenKey, nil)
		require.Equal(t, http.StatusOK, response.status, string(response.body))
		require.JSONEq(t, `{"source":"users-provider"}`, string(response.body))
		providerMu.Lock()
		defer providerMu.Unlock()
		require.Equal(t, []string{"GET /users/42?expand=profile"}, providerRequests)
	})

	t.Run("route-only catalog and document omit sibling routes", func(t *testing.T) {
		routePageResponse := fixture.request(t, http.MethodGet,
			fmt.Sprintf("/api/api-catalog/routes?service_id=%d&token_id=%d", imported.ServiceID, fixture.token.ID), fixture.userJWT, nil)
		require.Equal(t, http.StatusOK, routePageResponse.status, string(routePageResponse.body))
		var routePage masterapi.PaginatedResponse[map[string]any]
		require.NoError(t, json.Unmarshal(routePageResponse.body, &routePage))
		require.EqualValues(t, 1, routePage.Total)
		require.Len(t, routePage.Data, 1)
		require.Equal(t, "users", routePage.Data[0]["slug"])

		catalogDocumentResponse := fixture.request(t, http.MethodGet,
			fmt.Sprintf("/api/api-catalog/openapi?service_id=%d&token_id=%d", imported.ServiceID, fixture.token.ID), fixture.userJWT, nil)
		require.Equal(t, http.StatusOK, catalogDocumentResponse.status, string(catalogDocumentResponse.body))
		var catalogDocument struct {
			Document json.RawMessage `json:"document"`
		}
		require.NoError(t, json.Unmarshal(catalogDocumentResponse.body, &catalogDocument))
		paths := openAPIE2EDocumentPaths(t, catalogDocument.Document)
		require.ElementsMatch(t, []string{"/users", "/users/{id}"}, sortedOpenAPIE2EMapKeys(paths))
		require.NotContains(t, string(catalogDocument.Document), "/orders")
		require.NotContains(t, string(catalogDocument.Document), provider.URL)
	})

	t.Run("platform edit is exported and the sanitized export reimports", func(t *testing.T) {
		documentPath := fmt.Sprintf("/api/admin/api-services/%d/openapi", imported.ServiceID)
		get := fixture.request(t, http.MethodGet, documentPath, fixture.adminJWT, nil)
		require.Equal(t, http.StatusOK, get.status, string(get.body))
		var snapshot apiopenapi.DocumentResponse
		require.NoError(t, json.Unmarshal(get.body, &snapshot))
		require.Equal(t, "3.1.0", openAPIE2EDocumentVersion(t, snapshot.Export))
		require.ElementsMatch(t, []string{"/orders", "/users", "/users/{id}"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, snapshot.Export)))
		for routeIndex := range snapshot.Routes {
			if snapshot.Routes[routeIndex].Slug != "users" {
				continue
			}
			pathItem := snapshot.Routes[routeIndex].Paths["/users/{id}"]
			operation := pathItem.Operations[http.MethodGet]
			operation.Summary = "Edited in the platform"
			pathItem.Operations[http.MethodGet] = operation
			snapshot.Routes[routeIndex].Paths["/users/{id}"] = pathItem
		}
		payload := openAPIE2EUpdatePayload(snapshot)
		put := fixture.request(t, http.MethodPut, documentPath, fixture.adminJWT, payload)
		require.Equal(t, http.StatusOK, put.status, string(put.body))
		var updated apiopenapi.DocumentResponse
		require.NoError(t, json.Unmarshal(put.body, &updated))
		require.Contains(t, string(updated.Export), "Edited in the platform")
		require.Equal(t, "3.1.0", openAPIE2EDocumentVersion(t, updated.Export))
		require.ElementsMatch(t, []string{"/orders", "/users", "/users/{id}"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, updated.Export)))
		require.NotContains(t, string(updated.Export), provider.URL)
		require.NotContains(t, string(updated.Export), "backend_id")
		require.NotContains(t, string(updated.Export), "upstream_path")

		reimported := fixture.importDocument(t, "people-reimported-e2e", updated.Export)
		reimportedRoutes := fixture.routesBySlug(t, reimported.ServiceID)
		require.ElementsMatch(t, []string{"orders", "users"}, sortedOpenAPIE2EMapKeys(reimportedRoutes))
		reimportedDocument := fixture.openAPIDocument(t, reimported.ServiceID)
		require.Equal(t, "3.1.0", openAPIE2EDocumentVersion(t, reimportedDocument.Export))
		require.ElementsMatch(t, []string{"/orders", "/users", "/users/{id}"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, reimportedDocument.Export)))
		reimportedUsers := reimportedRoutes["users"].OpenAPIPaths.Data()["/users/{id}"]
		require.Equal(t, "Edited in the platform", reimportedUsers.Operations[http.MethodGet].Summary)
	})
}

func newOpenAPIE2EFixture(t *testing.T) *openAPIE2EFixture {
	t.Helper()
	dir := t.TempDir()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: filepath.Join(dir, "core.db"), LogDBPath: filepath.Join(dir, "log.db"),
			JWTSecret: strings.Repeat("x", 32), PublicBaseURLs: []string{"https://gateway.example.test"},
		},
		Agent: config.AgentConfig{
			CredentialsFile: filepath.Join(dir, "embedded-agent.json"), PreferredAddrTag: "local",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 1,
		},
	}
	server, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.NoError(t, server.InitAdminUser("openapi-admin", "openapi-admin-password"))

	fixture := &openAPIE2EFixture{server: server}
	fixture.user = models.User{
		Username: "openapi-e2e-user", Role: consts.RoleUser, Status: consts.StatusEnabled,
		GroupID: models.DefaultUserGroupID, Quota: 1_000_000,
	}
	require.NoError(t, server.DB.Create(&fixture.user).Error)
	fixture.token = models.Token{
		UserID: fixture.user.ID, Key: openAPIE2ETokenKey, Name: "openapi-e2e",
		Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit,
	}
	require.NoError(t, server.DB.Create(&fixture.token).Error)
	fixture.userJWT, err = middleware.GenerateToken(
		server.Cfg.Master.JWTSecret, fixture.user.ID, fixture.user.Role, fixture.user.Username, "", "",
	)
	require.NoError(t, err)

	masterHTTP := httptest.NewServer(server.Router)
	fixture.baseURL = masterHTTP.URL
	parsed, err := url.Parse(masterHTTP.URL)
	require.NoError(t, err)
	require.NoError(t, server.SetupEmbeddedAgentForTest(parsed.Host))
	waitForConnectedAgents(t, server, 1)
	fixture.adminJWT = fixture.login(t, "openapi-admin", "openapi-admin-password")

	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown OpenAPI E2E server: %v", err)
		}
		masterHTTP.CloseClientConnections()
		masterHTTP.Close()
	})
	return fixture
}

func (f *openAPIE2EFixture) login(t *testing.T, username, password string) string {
	t.Helper()
	response := f.request(t, http.MethodPost, "/api/login", "", map[string]any{"username": username, "password": password})
	require.Equal(t, http.StatusOK, response.status, string(response.body))
	var payload struct {
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(response.body, &payload))
	require.NotEmpty(t, payload.Token)
	return payload.Token
}

func (f *openAPIE2EFixture) request(t *testing.T, method, path, bearer string, body any) openAPIE2EHTTPResponse {
	t.Helper()
	var reader io.Reader = http.NoBody
	if body != nil {
		encoded, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, f.baseURL+path, reader)
	require.NoError(t, err)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	return openAPIE2EHTTPResponse{status: response.StatusCode, body: responseBody}
}

func (f *openAPIE2EFixture) importDocument(t *testing.T, slug string, document json.RawMessage) apiopenapi.ImportResult {
	t.Helper()
	selectedServer := 0
	response := f.request(t, http.MethodPost, "/api/admin/api-services/openapi/import", f.adminJWT, map[string]any{
		"document": document, "slug": slug, "selected_server": selectedServer,
		"backend_name": "primary", "upstream": map[string]any{"name": "origin"},
	})
	require.Equal(t, http.StatusCreated, response.status, string(response.body))
	var result apiopenapi.ImportResult
	require.NoError(t, json.Unmarshal(response.body, &result))
	require.NotZero(t, result.ServiceID)
	require.NotEmpty(t, result.RouteIDs)
	return result
}

func (f *openAPIE2EFixture) openAPIDocument(t *testing.T, serviceID uint) apiopenapi.DocumentResponse {
	t.Helper()
	response := f.request(t, http.MethodGet, fmt.Sprintf("/api/admin/api-services/%d/openapi", serviceID), f.adminJWT, nil)
	require.Equal(t, http.StatusOK, response.status, string(response.body))
	var document apiopenapi.DocumentResponse
	require.NoError(t, json.Unmarshal(response.body, &document))
	return document
}

func (f *openAPIE2EFixture) routesBySlug(t *testing.T, serviceID uint) map[string]models.APIRoute {
	t.Helper()
	var routes []models.APIRoute
	require.NoError(t, f.server.DB.Where("api_service_id = ?", serviceID).Find(&routes).Error)
	result := make(map[string]models.APIRoute, len(routes))
	for _, route := range routes {
		result[route.Slug] = route
	}
	return result
}

func (f *openAPIE2EFixture) grantRoutes(t *testing.T, serviceID uint, routeIDs ...uint) {
	t.Helper()
	response := f.request(t, http.MethodPut, fmt.Sprintf(
		"/api/admin/api-access-grants/%s/%d/services/%d", models.APIPrincipalToken, f.token.ID, serviceID,
	), f.adminJWT, apiaccessgrant.ReplaceGrantRequest{Scope: apiaccessgrant.GrantScopeRoutes, RouteIDs: routeIDs})
	require.Equal(t, http.StatusOK, response.status, string(response.body))
}

func (f *openAPIE2EFixture) requireAgentRoute(t *testing.T, serviceSlug, routeSlug string, status int) {
	t.Helper()
	require.Eventually(t, func() bool {
		store := f.server.GetEmbeddedAgentStore()
		if store == nil || store.APIIndex.RequireReady() != nil {
			return false
		}
		found, err := store.APIIndex.FindServiceRoute(serviceSlug, routeSlug)
		return err == nil && found.Route.Status == status && len(store.APIIndex.UpstreamsForBackend(found.Route.BackendID)) > 0
	}, 5*time.Second, 10*time.Millisecond)
}

func openAPIE2EUpdatePayload(snapshot apiopenapi.DocumentResponse) map[string]any {
	routes := make([]map[string]any, 0, len(snapshot.Routes))
	for _, route := range snapshot.Routes {
		routes = append(routes, map[string]any{
			"id": route.ID, "updated_at": route.UpdatedAt, "paths": route.Paths,
		})
	}
	return map[string]any{
		"service": map[string]any{
			"id": snapshot.Service.ID, "updated_at": snapshot.Service.UpdatedAt, "document": snapshot.Service.Document,
		},
		"routes": routes,
	}
}

func openAPIE2EUsersDocument(providerURL string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
	  "openapi":"3.1.0",
	  "info":{"title":"People API","version":"1.0.0"},
	  "servers":[{"url":%q}],
	  "paths":{
	    "/users":{"get":{"operationId":"listUsers","responses":{"200":{"description":"ok"}}}},
	    "/users/{id}":{"get":{"operationId":"getUser","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}},
	    "/orders":{"post":{"operationId":"createOrder","responses":{"201":{"description":"created"}}}}
	  }
	}`, providerURL))
}

func openAPIE2EDocumentPaths(t *testing.T, document json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var payload struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(document, &payload))
	return payload.Paths
}

func openAPIE2EDocumentVersion(t *testing.T, document json.RawMessage) string {
	t.Helper()
	var payload struct {
		OpenAPI string `json:"openapi"`
	}
	require.NoError(t, json.Unmarshal(document, &payload))
	require.NotEmpty(t, payload.OpenAPI)
	return payload.OpenAPI
}

func sortedOpenAPIE2EMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
