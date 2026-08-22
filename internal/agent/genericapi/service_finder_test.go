package genericapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type serviceRouteFinderFunc func(serviceSlug, routeSlug string) (cache.ServiceRoute, error)

func (f serviceRouteFinderFunc) FindServiceRoute(serviceSlug, routeSlug string) (cache.ServiceRoute, error) {
	return f(serviceSlug, routeSlug)
}

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
			got, subpath, err := finder.Find("weather", "/current", test.method, test.protocol)
			if test.wantCode == "" {
				require.NoError(t, err)
				require.Equal(t, test.protocol, got.Protocol)
				require.Empty(t, subpath)
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
		_, _, err := finder.Find("weather", "/current", method, ProtocolHTTP)
		require.NoErrorf(t, err, "method %s", method)
	}

	_, _, err := finder.Find("weather", "/current", http.MethodConnect, ProtocolHTTP)
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
			_, _, err := finder.Find(test.slug, "/current", http.MethodGet, ProtocolHTTP)
			require.Equal(t, test.want, ErrorCode(err))
		})
	}
}

func TestServiceFinderReturnsCacheNotReadyBeforeEntityLookup(t *testing.T) {
	finder := NewServiceFinder(cache.NewAPIIndex())
	_, _, err := finder.Find("missing", "/current", http.MethodGet, ProtocolHTTP)
	require.Equal(t, CodeCacheNotReady, ErrorCode(err))
}

// Break caught: readiness can change between the explicit-route lookup and
// root fallback; the second lookup's cache error must remain a 503 instead of
// being flattened into a route-not-found 404.
func TestServiceFinderPreservesCacheNotReadyFromRootFallbackLookup(t *testing.T) {
	var routeSlugs []string
	finder := &ServiceFinder{routeFinder: serviceRouteFinderFunc(func(_, routeSlug string) (cache.ServiceRoute, error) {
		routeSlugs = append(routeSlugs, routeSlug)
		if routeSlug == "accounts" {
			return cache.ServiceRoute{}, cache.ErrAPIRouteNotFound
		}
		return cache.ServiceRoute{}, cache.ErrAPICacheNotReady
	})}

	_, _, err := finder.Find("users", "/accounts", http.MethodGet, ProtocolHTTP)
	require.Equal(t, []string{"accounts", ""}, routeSlugs)
	require.Equal(t, CodeCacheNotReady, ErrorCode(err))
	var gatewayErr *GatewayError
	require.True(t, errors.As(err, &gatewayErr))
	require.Equal(t, http.StatusServiceUnavailable, gatewayErr.status)
}

func TestServiceFinderDoesNotReportUnexpectedIndexFailureAsNotFound(t *testing.T) {
	finder := &ServiceFinder{routeFinder: serviceRouteFinderFunc(func(_, _ string) (cache.ServiceRoute, error) {
		return cache.ServiceRoute{}, errors.New("unexpected index failure")
	})}

	_, _, err := finder.Find("users", "/accounts", http.MethodGet, ProtocolHTTP)
	require.Equal(t, CodeUnavailable, ErrorCode(err))
	var gatewayErr *GatewayError
	require.True(t, errors.As(err, &gatewayErr))
	require.Equal(t, http.StatusServiceUnavailable, gatewayErr.status)
}

func TestServiceFinderRootRouteFallbackAndExplicitRoutePriority(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}
	root := protocol.SyncedAPIRoute{
		ID: 8, ServiceID: service.ID, Slug: "", Status: consts.StatusEnabled,
		Protocols: []string{ProtocolHTTP}, ForwardSubpath: true,
	}
	explicit := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: service.ID, Slug: "accounts", Status: consts.StatusEnabled,
		Protocols: []string{ProtocolHTTP}, ForwardSubpath: true,
	}
	finder := NewServiceFinder(serviceFinderIndex(t, service, root, explicit))

	for _, test := range []struct {
		name        string
		requestPath string
		wantRouteID uint
		wantSubpath string
	}{
		{name: "service root", requestPath: "", wantRouteID: root.ID},
		{name: "trailing root slash", requestPath: "/", wantRouteID: root.ID, wantSubpath: "/"},
		{name: "dynamic first segment falls back to root", requestPath: "/acme/users", wantRouteID: root.ID, wantSubpath: "/acme/users"},
		{name: "explicit route wins", requestPath: "/accounts", wantRouteID: explicit.ID},
		{name: "explicit route keeps remaining subpath", requestPath: "/accounts/42/profile", wantRouteID: explicit.ID, wantSubpath: "/42/profile"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, subpath, err := finder.Find("users", test.requestPath, http.MethodGet, ProtocolHTTP)
			require.NoError(t, err)
			require.Equal(t, test.wantRouteID, got.Route.ID)
			require.Equal(t, test.wantSubpath, subpath)
		})
	}
}

// Break caught: trimming only one leading slash turns //accounts into an
// empty route slug, allowing a request for an explicit route to use the root
// route's status, method, permission, and limiter identity instead.
func TestServiceFinderRejectsExtraLeadingSlashBeforeRouteSelection(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}
	root := protocol.SyncedAPIRoute{
		ID: 8, ServiceID: service.ID, Slug: "", Status: consts.StatusEnabled,
		Protocols: []string{ProtocolHTTP}, ForwardSubpath: true,
	}

	for _, requestPath := range []string{"//accounts", "///accounts", "/%2Faccounts"} {
		t.Run(requestPath, func(t *testing.T) {
			calls := 0
			finder := &ServiceFinder{routeFinder: serviceRouteFinderFunc(func(_, _ string) (cache.ServiceRoute, error) {
				calls++
				return cache.ServiceRoute{Service: service, Route: root}, nil
			})}
			_, _, err := finder.Find("users", requestPath, http.MethodGet, ProtocolHTTP)
			require.Equal(t, CodeInvalidRequest, ErrorCode(err))
			require.Zero(t, calls)
		})
	}
}

func TestServiceFinderExplicitRouteFailuresNeverFallBackToRootRoute(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}

	for _, test := range []struct {
		name     string
		explicit protocol.SyncedAPIRoute
		method   string
		protocol string
		wantCode string
	}{
		{
			name: "disabled explicit route", method: http.MethodGet, protocol: ProtocolHTTP, wantCode: CodeAPINotFound,
			explicit: protocol.SyncedAPIRoute{ID: 9, ServiceID: service.ID, Slug: "accounts", Status: consts.StatusDisabled, Protocols: []string{ProtocolHTTP}},
		},
		{
			name: "explicit route rejects method", method: http.MethodPost, protocol: ProtocolHTTP, wantCode: CodeMethodNotAllowed,
			explicit: protocol.SyncedAPIRoute{ID: 9, ServiceID: service.ID, Slug: "accounts", Status: consts.StatusEnabled, Protocols: []string{ProtocolHTTP}, AllowedMethods: []string{http.MethodGet}},
		},
		{
			name: "explicit route rejects protocol", method: http.MethodGet, protocol: ProtocolHTTP, wantCode: CodeAPINotFound,
			explicit: protocol.SyncedAPIRoute{ID: 9, ServiceID: service.ID, Slug: "accounts", Status: consts.StatusEnabled, Protocols: []string{ProtocolWebSocket}},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var routeSlugs []string
			finder := &ServiceFinder{routeFinder: serviceRouteFinderFunc(func(_, routeSlug string) (cache.ServiceRoute, error) {
				routeSlugs = append(routeSlugs, routeSlug)
				return cache.ServiceRoute{Service: service, Route: test.explicit}, nil
			})}
			_, _, err := finder.Find("users", "/accounts", test.method, test.protocol)
			require.Equal(t, test.wantCode, ErrorCode(err))
			require.Equal(t, []string{"accounts"}, routeSlugs)
		})
	}
}

func TestServiceFinderMissingRootRouteReturnsNotFound(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "users", Status: consts.StatusEnabled}
	explicit := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: service.ID, Slug: "accounts", Status: consts.StatusEnabled,
		Protocols: []string{ProtocolHTTP},
	}
	finder := NewServiceFinder(serviceFinderIndex(t, service, explicit))

	_, _, err := finder.Find("users", "/missing", http.MethodGet, ProtocolHTTP)
	require.Equal(t, CodeAPINotFound, ErrorCode(err))
}

func serviceFinderIndex(t *testing.T, service protocol.SyncedAPIService, routes ...protocol.SyncedAPIRoute) *cache.APIIndex {
	t.Helper()
	index := cache.NewAPIIndex()
	require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{service}))
	require.NoError(t, index.ReplaceRoutes(routes))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
	return index
}
