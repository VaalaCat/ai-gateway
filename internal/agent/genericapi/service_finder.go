package genericapi

import (
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

// ServiceFinder validates the public route shape against the current API index.
type ServiceFinder struct{ index *cache.APIIndex }

func NewServiceFinder(index *cache.APIIndex) *ServiceFinder { return &ServiceFinder{index: index} }

func (f *ServiceFinder) Find(serviceSlug, routeSlug, method, requestedProtocol string) (ServiceRoute, error) {
	if err := validRouteRequest(serviceSlug, routeSlug, requestedProtocol); err != nil {
		return ServiceRoute{}, err
	}
	if f == nil || f.index == nil {
		return ServiceRoute{}, gatewayError(CodeCacheNotReady, http.StatusServiceUnavailable, "", ErrAPICacheNotReady)
	}
	if err := f.index.RequireReady(); err != nil {
		return ServiceRoute{}, gatewayError(CodeCacheNotReady, http.StatusServiceUnavailable, "", err)
	}
	found, err := f.index.FindServiceRoute(serviceSlug, routeSlug)
	if err != nil {
		return ServiceRoute{}, gatewayError(CodeAPINotFound, http.StatusNotFound, "", err)
	}
	if found.Service.Status != consts.StatusEnabled || found.Route.Status != consts.StatusEnabled {
		return ServiceRoute{}, gatewayError(CodeAPINotFound, http.StatusNotFound, "", nil)
	}
	if !routeSupportsProtocol(found.Route.Protocols, requestedProtocol) {
		return ServiceRoute{}, gatewayError(CodeAPINotFound, http.StatusNotFound, "", nil)
	}
	if err := validateMethod(found.Route.AllowedMethods, method, requestedProtocol); err != nil {
		return ServiceRoute{}, err
	}
	return ServiceRoute{Service: found.Service, Route: found.Route, Protocol: requestedProtocol}, nil
}

func validRouteRequest(serviceSlug, routeSlug, requestedProtocol string) error {
	if !apiSlugPattern.MatchString(serviceSlug) || !apiSlugPattern.MatchString(routeSlug) {
		return gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	if requestedProtocol != ProtocolHTTP && requestedProtocol != ProtocolWebSocket {
		return gatewayError(CodeInvalidRequest, http.StatusBadRequest, "", nil)
	}
	return nil
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
