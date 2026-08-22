package master

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/stretchr/testify/require"
)

// Breaks caught: root paths that are not imported as the empty-slug Route,
// an explicit Route permission failure that retries through root authority, or
// a disabled explicit Route that disappears and exposes the root fallback.
func TestOpenAPIRootRouteE2E(t *testing.T) {
	var providerMu sync.Mutex
	providerRequests := make([]string, 0, 2)
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		providerMu.Lock()
		providerRequests = append(providerRequests, request.Method+" "+request.URL.RequestURI())
		providerMu.Unlock()
		response.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)
	fixture := newOpenAPIE2EFixture(t)

	imported := fixture.importDocument(t, "root-e2e", openAPIE2ERootDocument(provider.URL))
	routes := fixture.routesBySlug(t, imported.ServiceID)
	require.ElementsMatch(t, []string{"", "orders"}, sortedOpenAPIE2EMapKeys(routes))
	rootRoute := routes[""]
	ordersRoute := routes["orders"]
	importedDocument := fixture.openAPIDocument(t, imported.ServiceID)
	require.Equal(t, "3.0.3", openAPIE2EDocumentVersion(t, importedDocument.Export))
	require.ElementsMatch(t, []string{"/", "/orders", "/{tenant}/users"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, importedDocument.Export)))
	reimported := fixture.importDocument(t, "root-reimported-e2e", importedDocument.Export)
	reimportedDocument := fixture.openAPIDocument(t, reimported.ServiceID)
	require.Equal(t, "3.0.3", openAPIE2EDocumentVersion(t, reimportedDocument.Export))
	require.ElementsMatch(t, []string{"/", "/orders", "/{tenant}/users"}, sortedOpenAPIE2EMapKeys(openAPIE2EDocumentPaths(t, reimportedDocument.Export)))
	fixture.grantRoutes(t, imported.ServiceID, rootRoute.ID)
	fixture.requireAgentRoute(t, "root-e2e", "", consts.StatusEnabled)
	fixture.requireAgentRoute(t, "root-e2e", "orders", consts.StatusEnabled)

	t.Run("empty slug forwards service root and dynamic first segment", func(t *testing.T) {
		for _, path := range []string{"/v1/api/root-e2e", "/v1/api/root-e2e/acme/users?active=true"} {
			response := fixture.request(t, http.MethodGet, path, openAPIE2ETokenKey, nil)
			require.Equal(t, http.StatusNoContent, response.status, string(response.body))
		}
		providerMu.Lock()
		defer providerMu.Unlock()
		require.Equal(t, []string{"GET /", "GET /acme/users?active=true"}, providerRequests)
	})

	t.Run("explicit route rejection never retries through root authority", func(t *testing.T) {
		before := openAPIE2EProviderRequestCount(&providerMu, &providerRequests)
		response := fixture.request(t, http.MethodPost, "/v1/api/root-e2e/orders", openAPIE2ETokenKey, nil)
		require.Equal(t, http.StatusForbidden, response.status, string(response.body))
		require.Equal(t, before, openAPIE2EProviderRequestCount(&providerMu, &providerRequests))
	})

	t.Run("disabled explicit route never becomes a root fallback", func(t *testing.T) {
		response := fixture.request(t, http.MethodPut,
			fmt.Sprintf("/api/admin/api-routes/%d", ordersRoute.ID), fixture.adminJWT, map[string]any{"status": consts.StatusDisabled})
		require.Equal(t, http.StatusOK, response.status, string(response.body))
		fixture.requireAgentRoute(t, "root-e2e", "orders", consts.StatusDisabled)

		before := openAPIE2EProviderRequestCount(&providerMu, &providerRequests)
		blocked := fixture.request(t, http.MethodPost, "/v1/api/root-e2e/orders", openAPIE2ETokenKey, nil)
		require.Equal(t, http.StatusNotFound, blocked.status, string(blocked.body))
		require.Equal(t, before, openAPIE2EProviderRequestCount(&providerMu, &providerRequests))
	})
}

func openAPIE2EProviderRequestCount(mu *sync.Mutex, requests *[]string) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*requests)
}

func openAPIE2ERootDocument(providerURL string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
	  "openapi":"3.0.3",
	  "info":{"title":"Root API","version":"1.0.0"},
	  "servers":[{"url":%q}],
	  "paths":{
	    "/":{"get":{"operationId":"root","responses":{"204":{"description":"ok"}}}},
	    "/{tenant}/users":{"get":{"operationId":"tenantUsers","parameters":[{"name":"tenant","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"204":{"description":"ok"}}}},
	    "/orders":{"post":{"operationId":"createOrder","responses":{"204":{"description":"ok"}}}}
	  }
	}`, providerURL))
}
