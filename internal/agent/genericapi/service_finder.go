package genericapi

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	ProtocolHTTP      = "http"
	ProtocolWebSocket = "websocket"
)

var (
	apiSlugPattern        = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)
	standardHTTPMethods   = []string{http.MethodDelete, http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPatch, http.MethodPost, http.MethodPut, http.MethodTrace}
	standardHTTPMethodSet = map[string]struct{}{
		http.MethodDelete: {}, http.MethodGet: {}, http.MethodHead: {}, http.MethodOptions: {},
		http.MethodPatch: {}, http.MethodPost: {}, http.MethodPut: {}, http.MethodTrace: {},
	}
)

// ServiceRoute is the validated API route selected for one request.
type ServiceRoute struct {
	Service  protocol.SyncedAPIService
	Route    protocol.SyncedAPIRoute
	Protocol string
}

type serviceRouteFinder interface {
	FindServiceRoute(serviceSlug, routeSlug string) (cache.ServiceRoute, error)
}

// ServiceFinder selects an explicit first-segment route or falls back to the
// service's empty-slug root route when that explicit route does not exist.
type ServiceFinder struct{ routeFinder serviceRouteFinder }

func NewServiceFinder(index *cache.APIIndex) *ServiceFinder {
	if index == nil {
		return &ServiceFinder{}
	}
	return &ServiceFinder{routeFinder: index}
}

func (f *ServiceFinder) Find(serviceSlug, requestPath, method, requestedProtocol string) (ServiceRoute, string, error) {
	if err := validRouteRequest(serviceSlug, requestedProtocol); err != nil {
		return ServiceRoute{}, "", err
	}
	if hasExtraLeadingSlash(requestPath) {
		return ServiceRoute{}, "", gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	if f == nil || f.routeFinder == nil {
		return ServiceRoute{}, "", gatewayError(CodeCacheNotReady, http.StatusServiceUnavailable, "", ErrAPICacheNotReady)
	}
	routeSlug, subpath := splitAPIRequestPath(requestPath)
	found, err := f.routeFinder.FindServiceRoute(serviceSlug, routeSlug)
	if routeSlug != "" && errors.Is(err, cache.ErrAPIRouteNotFound) {
		found, err = f.routeFinder.FindServiceRoute(serviceSlug, "")
		subpath = requestPath
	}
	if err != nil {
		return ServiceRoute{}, "", serviceRouteFindError(err)
	}
	if found.Service.Status != consts.StatusEnabled || found.Route.Status != consts.StatusEnabled {
		return ServiceRoute{}, "", gatewayError(CodeAPINotFound, http.StatusNotFound, "", nil)
	}
	if !routeSupportsProtocol(found.Route.Protocols, requestedProtocol) {
		return ServiceRoute{}, "", gatewayError(CodeAPINotFound, http.StatusNotFound, "", nil)
	}
	if err := validateMethod(found.Route.AllowedMethods, method, requestedProtocol); err != nil {
		return ServiceRoute{}, "", err
	}
	return ServiceRoute{Service: found.Service, Route: found.Route, Protocol: requestedProtocol}, subpath, nil
}

func serviceRouteFindError(err error) error {
	switch {
	case errors.Is(err, cache.ErrAPICacheNotReady):
		return gatewayError(CodeCacheNotReady, http.StatusServiceUnavailable, "", err)
	case errors.Is(err, cache.ErrAPIServiceNotFound), errors.Is(err, cache.ErrAPIRouteNotFound):
		return gatewayError(CodeAPINotFound, http.StatusNotFound, "", err)
	default:
		return gatewayError(CodeUnavailable, http.StatusServiceUnavailable, "", err)
	}
}

func hasExtraLeadingSlash(requestPath string) bool {
	if strings.HasPrefix(requestPath, "//") {
		return true
	}
	return len(requestPath) >= 4 && requestPath[0] == '/' && strings.EqualFold(requestPath[1:4], "%2f")
}

func validRouteRequest(serviceSlug, requestedProtocol string) error {
	if !apiSlugPattern.MatchString(serviceSlug) {
		return gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	if requestedProtocol != ProtocolHTTP && requestedProtocol != ProtocolWebSocket {
		return gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	return nil
}

func splitAPIRequestPath(requestPath string) (routeSlug, subpath string) {
	withoutLeadingSlash := strings.TrimPrefix(requestPath, "/")
	if withoutLeadingSlash == "" {
		return "", requestPath
	}
	separator := strings.IndexByte(withoutLeadingSlash, '/')
	if separator < 0 {
		return withoutLeadingSlash, ""
	}
	return withoutLeadingSlash[:separator], withoutLeadingSlash[separator:]
}

func routeSupportsProtocol(protocols []string, requested string) bool {
	for _, protocol := range protocols {
		if protocol == requested {
			return true
		}
	}
	return false
}

func validateMethod(allowedMethods []string, method, requestedProtocol string) error {
	if requestedProtocol == ProtocolWebSocket && method != http.MethodGet {
		return gatewayError(CodeMethodNotAllowed, http.StatusMethodNotAllowed, http.MethodGet, nil)
	}
	allowed := allowedMethods
	if len(allowed) == 0 {
		allowed = standardHTTPMethods
	}
	if _, standard := standardHTTPMethodSet[method]; !standard || !containsMethod(allowed, method) {
		return gatewayError(CodeMethodNotAllowed, http.StatusMethodNotAllowed, strings.Join(allowed, ", "), nil)
	}
	return nil
}

func containsMethod(allowed []string, method string) bool {
	for _, candidate := range allowed {
		if candidate == method {
			return true
		}
	}
	return false
}
