package genericapi

import (
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestServiceFinderMethodAndProtocolMatrix(t *testing.T) {
	finder := NewServiceFinder(serviceFinderIndex(t, protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusEnabled}, protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusEnabled,
		Protocols: []string{"http", "websocket"}, AllowedMethods: []string{http.MethodGet, http.MethodPost},
	}))

	for _, test := range []struct {
		name      string
		method    string
		protocol  string
		wantCode  string
		wantAllow string
	}{
		{name: "http explicit post", method: http.MethodPost, protocol: ProtocolHTTP},
		{name: "websocket get", method: http.MethodGet, protocol: ProtocolWebSocket},
		{name: "http method denied", method: http.MethodDelete, protocol: ProtocolHTTP, wantCode: CodeMethodNotAllowed, wantAllow: "GET, POST"},
		{name: "connect rejected", method: http.MethodConnect, protocol: ProtocolHTTP, wantCode: CodeMethodNotAllowed, wantAllow: "GET, POST"},
		{name: "websocket requires get", method: http.MethodPost, protocol: ProtocolWebSocket, wantCode: CodeMethodNotAllowed, wantAllow: http.MethodGet},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := finder.Find("weather", "current", test.method, test.protocol)
			if test.wantCode == "" {
				require.NoError(t, err)
				require.Equal(t, test.protocol, got.Protocol)
				return
			}
			require.Equal(t, test.wantCode, ErrorCode(err))
			require.Equal(t, test.wantAllow, ErrorAllow(err))
		})
	}
}

func TestServiceFinderAcceptsAllStandardMethodsForEmptyConfiguration(t *testing.T) {
	finder := NewServiceFinder(serviceFinderIndex(t, protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusEnabled}, protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusEnabled, Protocols: []string{"http"},
	}))

	for _, method := range []string{http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace} {
		_, err := finder.Find("weather", "current", method, ProtocolHTTP)
		require.NoErrorf(t, err, "method %s", method)
	}

	_, err := finder.Find("weather", "current", http.MethodConnect, ProtocolHTTP)
	require.Equal(t, CodeMethodNotAllowed, ErrorCode(err))
	require.Equal(t, "DELETE, GET, HEAD, OPTIONS, PATCH, POST, PUT, TRACE", ErrorAllow(err))
}

func TestServiceFinderRejectsMissingDisabledAndMalformedSlugs(t *testing.T) {
	for _, test := range []struct {
		name    string
		service protocol.SyncedAPIService
		route   protocol.SyncedAPIRoute
		slug    string
		want    string
	}{
		{name: "missing service", service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusEnabled}, route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusEnabled, Protocols: []string{"http"}}, slug: "missing", want: CodeAPINotFound},
		{name: "disabled service", service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusDisabled}, route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusEnabled, Protocols: []string{"http"}}, slug: "weather", want: CodeAPINotFound},
		{name: "disabled route", service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusEnabled}, route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusDisabled, Protocols: []string{"http"}}, slug: "weather", want: CodeAPINotFound},
		{name: "malformed slug", service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: consts.StatusEnabled}, route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "current", Status: consts.StatusEnabled, Protocols: []string{"http"}}, slug: "Weather", want: CodeInvalidRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			finder := NewServiceFinder(serviceFinderIndex(t, test.service, test.route))
			_, err := finder.Find(test.slug, "current", http.MethodGet, ProtocolHTTP)
			require.Equal(t, test.want, ErrorCode(err))
		})
	}
}

func TestServiceFinderReturnsCacheNotReadyBeforeEntityLookup(t *testing.T) {
	finder := NewServiceFinder(cache.NewAPIIndex())
	_, err := finder.Find("missing", "current", http.MethodGet, ProtocolHTTP)
	require.Equal(t, CodeCacheNotReady, ErrorCode(err))
}

func serviceFinderIndex(t *testing.T, service protocol.SyncedAPIService, route protocol.SyncedAPIRoute) *cache.APIIndex {
	t.Helper()
	index := cache.NewAPIIndex()
	require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{service}))
	require.NoError(t, index.ReplaceRoutes([]protocol.SyncedAPIRoute{route}))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
	return index
}
