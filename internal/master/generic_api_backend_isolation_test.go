package master

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestGenericAPIHTTPBackendIsolationAcrossLocalDirectAndRelay(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	for _, path := range []app.RoutePath{app.RoutePathLocal, app.RoutePathDirect, app.RoutePathRelay} {
		path := path
		t.Run(string(path), func(t *testing.T) {
			var providerACalls, providerBCalls atomic.Int32
			providerA := backendIsolationHTTPProvider(t, "provider-a", &providerACalls)
			providerB := backendIsolationHTTPProvider(t, "provider-b", &providerBCalls)
			gateway, picker, reporter, tunnelOpens := harness.backendIsolationHTTPGateway(t, path, newBackendIsolationFixture(providerA.URL, providerB.URL, apiattempt.APIProtocolHTTP))

			for _, route := range []struct {
				slug string
				body string
			}{
				{slug: "forecast", body: "provider-a"},
				{slug: "radar", body: "provider-b"},
			} {
				response := httptest.NewRecorder()
				gateway.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/api/weather/"+route.slug, bytes.NewBufferString(route.slug)))
				require.Equalf(t, http.StatusOK, response.Code, "%s response: %s", route.slug, response.Body.String())
				require.Equal(t, route.body, response.Body.String())
			}

			require.Equal(t, int32(1), providerACalls.Load(), "forecast must only hit provider-a")
			require.Equal(t, int32(1), providerBCalls.Load(), "radar must only hit provider-b")
			require.Equal(t, map[uint]int{10: 1, 20: 1}, picker.CallsByBackend())
			if tunnelOpens != nil {
				require.Equal(t, int32(2), tunnelOpens.Load())
			}
			entries := reporter.Entries()
			require.Len(t, entries, 2)
			require.Equal(t, uint(101), entries[0].APIUpstreamID)
			require.Equal(t, uint(201), entries[1].APIUpstreamID)
		})
	}
}

func TestGenericAPIWebSocketBackendIsolationAcrossLocalDirectAndRelay(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	for _, path := range []app.RoutePath{app.RoutePathLocal, app.RoutePathDirect, app.RoutePathRelay} {
		path := path
		t.Run(string(path), func(t *testing.T) {
			var providerACalls, providerBCalls atomic.Int32
			providerA := backendIsolationWebSocketProvider(t, "provider-a", &providerACalls)
			providerB := backendIsolationWebSocketProvider(t, "provider-b", &providerBCalls)
			gateway, picker, reporter, tunnelOpens := harness.backendIsolationWebSocketGateway(t, path, newBackendIsolationFixture(providerA.URL, providerB.URL, apiattempt.APIProtocolWebSocket))

			for _, route := range []struct {
				slug  string
				label string
			}{
				{slug: "forecast", label: "provider-a"},
				{slug: "radar", label: "provider-b"},
			} {
				client, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/"+route.slug, nil)
				require.NoError(t, err)
				if response != nil {
					require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
				}
				assertBackendIsolationWebSocketEcho(t, client, route.label, []byte(route.slug+"-one"))
				assertBackendIsolationWebSocketEcho(t, client, route.label, []byte(route.slug+"-two"))
				require.NoError(t, client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second)))
				require.NoError(t, client.Close())
			}

			require.Eventually(t, func() bool { return len(reporter.Entries()) == 2 }, 3*time.Second, time.Millisecond)
			require.Equal(t, int32(1), providerACalls.Load(), "forecast must dial provider-a once")
			require.Equal(t, int32(1), providerBCalls.Load(), "radar must dial provider-b once")
			require.Equal(t, map[uint]int{10: 1, 20: 1}, picker.CallsByBackend(), "each connection must keep one frozen lease")
			if tunnelOpens != nil {
				require.Equal(t, int32(2), tunnelOpens.Load())
			}
		})
	}
}

func TestGenericAPIBackendIsolationReturnsUnavailableForZeroAndEmptyPools(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	for _, requestedProtocol := range []apiattempt.APIProtocol{apiattempt.APIProtocolHTTP, apiattempt.APIProtocolWebSocket} {
		requestedProtocol := requestedProtocol
		t.Run(string(requestedProtocol), func(t *testing.T) {
			fixture := newBackendIsolationFixture("http://provider-a.invalid", "http://provider-b.invalid", requestedProtocol)
			fixture.routes["zero"] = backendIsolationRoute(11, "zero", 0, requestedProtocol)
			fixture.routes["missing"] = backendIsolationRoute(12, "missing", 30, requestedProtocol)
			if requestedProtocol == apiattempt.APIProtocolHTTP {
				gateway, picker, _, _ := harness.backendIsolationHTTPGateway(t, app.RoutePathLocal, fixture)
				for _, slug := range []string{"zero", "missing"} {
					response := httptest.NewRecorder()
					gateway.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/v1/api/weather/"+slug, nil))
					require.Equal(t, http.StatusServiceUnavailable, response.Code)
					require.Contains(t, response.Body.String(), `"code":"`+genericapi.CodeUnavailable+`"`)
				}
				require.Equal(t, map[uint]int{0: 1, 30: 1}, picker.CallsByBackend())
				return
			}

			gateway, picker, _, _ := harness.backendIsolationWebSocketGateway(t, app.RoutePathLocal, fixture)
			for _, slug := range []string{"zero", "missing"} {
				client, response, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/"+slug, nil)
				require.Error(t, err)
				require.Nil(t, client)
				require.NotNil(t, response)
				body, readErr := io.ReadAll(response.Body)
				require.NoError(t, readErr)
				require.NoError(t, response.Body.Close())
				require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
				require.Contains(t, string(body), `"code":"`+genericapi.CodeUnavailable+`"`)
			}
			require.Equal(t, map[uint]int{0: 1, 30: 1}, picker.CallsByBackend())
		})
	}
}

type backendIsolationFixture struct {
	service   protocol.SyncedAPIService
	routes    map[string]protocol.SyncedAPIRoute
	upstreams map[uint][]protocol.SyncedAPIUpstream
	protocol  apiattempt.APIProtocol
}

func newBackendIsolationFixture(providerA, providerB string, requestedProtocol apiattempt.APIProtocol) backendIsolationFixture {
	return backendIsolationFixture{
		service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Name: "Weather", Status: consts.StatusEnabled},
		routes: map[string]protocol.SyncedAPIRoute{
			"forecast": backendIsolationRoute(9, "forecast", 10, requestedProtocol),
			"radar":    backendIsolationRoute(10, "radar", 20, requestedProtocol),
		},
		upstreams: map[uint][]protocol.SyncedAPIUpstream{
			10: {{ID: 101, BackendID: 10, Name: "provider-a", BaseURL: providerA, AuthType: "none", Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
			20: {{ID: 201, BackendID: 20, Name: "provider-b", BaseURL: providerB, AuthType: "none", Priority: 1, Weight: 1, Status: consts.StatusEnabled}},
		},
		protocol: requestedProtocol,
	}
}

func backendIsolationRoute(id uint, slug string, backendID uint, requestedProtocol apiattempt.APIProtocol) protocol.SyncedAPIRoute {
	method := http.MethodPost
	if requestedProtocol == apiattempt.APIProtocolWebSocket {
		method = http.MethodGet
	}
	return protocol.SyncedAPIRoute{
		ID: id, ServiceID: 7, BackendID: backendID, Slug: slug, Status: consts.StatusEnabled,
		Protocols: []string{string(requestedProtocol)}, AllowedMethods: []string{method}, UpstreamPath: "/provider",
	}
}

func (f backendIsolationFixture) Find(serviceSlug, requestPath, method, requestedProtocol string) (genericapi.ServiceRoute, string, error) {
	route, ok := f.routes[strings.TrimPrefix(requestPath, "/")]
	if !ok || serviceSlug != f.service.Slug || requestedProtocol != string(f.protocol) || len(route.AllowedMethods) != 1 || method != route.AllowedMethods[0] {
		return genericapi.ServiceRoute{}, "", genericapi.ErrExecutionUnavailable
	}
	return genericapi.ServiceRoute{Service: f.service, Route: route, Protocol: requestedProtocol}, "", nil
}

func (f backendIsolationFixture) FindServiceRouteByID(serviceID, routeID uint) (genericapi.ServiceRoute, error) {
	if serviceID != f.service.ID {
		return genericapi.ServiceRoute{}, genericapi.ErrExecutionUnavailable
	}
	for _, route := range f.routes {
		if route.ID == routeID {
			return genericapi.ServiceRoute{Service: f.service, Route: route, Protocol: string(f.protocol)}, nil
		}
	}
	return genericapi.ServiceRoute{}, genericapi.ErrExecutionUnavailable
}

func (f backendIsolationFixture) UpstreamsForBackend(backendID uint) []protocol.SyncedAPIUpstream {
	return append([]protocol.SyncedAPIUpstream(nil), f.upstreams[backendID]...)
}

type backendIsolationPicker struct {
	picker *genericapi.APIUpstreamPicker
	mu     sync.Mutex
	calls  map[uint]int
}

func newBackendIsolationPicker(fixture backendIsolationFixture) *backendIsolationPicker {
	return &backendIsolationPicker{
		picker: genericapi.NewAPIUpstreamPicker(fixture, genericAPIE2EBreakerFinder{}),
		calls:  make(map[uint]int),
	}
}

func (p *backendIsolationPicker) Pick(backendID uint, requestedProtocol apiattempt.APIProtocol, requestID string) (*genericapi.APIUpstreamLease, error) {
	p.mu.Lock()
	p.calls[backendID]++
	p.mu.Unlock()
	return p.picker.Pick(backendID, requestedProtocol, requestID)
}

func (p *backendIsolationPicker) CallsByBackend() map[uint]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make(map[uint]int, len(p.calls))
	for backendID, calls := range p.calls {
		result[backendID] = calls
	}
	return result
}

func (h *genericAPIE2EHarness) backendIsolationHTTPGateway(t *testing.T, path app.RoutePath, fixture backendIsolationFixture) (http.Handler, *backendIsolationPicker, *genericAPIE2EUsageReporter, *atomic.Int32) {
	t.Helper()
	picker := newBackendIsolationPicker(fixture)
	local := genericapi.NewHTTPHandler(picker, genericapi.NewHTTPTransport(""))
	pick, remote, tunnelOpens := h.httpRoutePath(t, path, fixture, local)
	reporter := &genericAPIE2EUsageReporter{}
	handler := genericapi.NewHandler(genericapi.HandlerOptions{
		Finder: fixture, Permission: genericAPIE2EPermissionGate{}, Quota: genericAPIE2EQuotaGate{},
		AgentPicker: genericAPIE2EAgentPicker{pick: pick}, ExecutionCapabilities: genericAPIE2ECapabilityFinder{},
		Usage: genericapi.NewUsageBuilder(nil), Reporter: reporter, MasterUsageSupport: genericAPIE2EMasterUsageSupport{}, SourceAgentID: "source-a",
		Handlers: map[string]genericapi.ProtocolHandler{genericapi.ProtocolHTTP: genericapi.NewExecutionRouter("source-a", local, remote)},
	})
	return genericAPIE2ERouter(handler), picker, reporter, tunnelOpens
}

func (h *genericAPIE2EHarness) backendIsolationWebSocketGateway(t *testing.T, path app.RoutePath, fixture backendIsolationFixture) (*httptest.Server, *backendIsolationPicker, *genericAPIE2EUsageReporter, *atomic.Int32) {
	t.Helper()
	picker := newBackendIsolationPicker(fixture)
	local := genericapi.NewWebSocketHandler(genericapi.WebSocketHandlerOptions{Picker: picker})
	pick, remote, tunnelOpens := h.webSocketRoutePath(t, path, fixture, local, picker)
	reporter := &genericAPIE2EUsageReporter{}
	handler := genericapi.NewHandler(genericapi.HandlerOptions{
		Finder: fixture, Permission: genericAPIE2EPermissionGate{}, Quota: genericAPIE2EQuotaGate{},
		AgentPicker: genericAPIE2EAgentPicker{pick: pick}, ExecutionCapabilities: genericAPIE2ECapabilityFinder{},
		Usage: genericapi.NewUsageBuilder(nil), Reporter: reporter, MasterUsageSupport: genericAPIE2EMasterUsageSupport{}, SourceAgentID: "source-a",
		Handlers: map[string]genericapi.ProtocolHandler{genericapi.ProtocolWebSocket: genericapi.NewExecutionRouter("source-a", local, remote)},
	})
	gateway := httptest.NewServer(genericAPIE2ERouter(handler))
	t.Cleanup(gateway.Close)
	return gateway, picker, reporter, tunnelOpens
}

func backendIsolationHTTPProvider(t *testing.T, body string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		_, err := io.Copy(io.Discard, request.Body)
		require.NoError(t, err)
		_, err = io.WriteString(writer, body)
		require.NoError(t, err)
	}))
	t.Cleanup(provider.Close)
	return provider
}

func backendIsolationWebSocketProvider(t *testing.T, label string, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			messageType, payload, readErr := connection.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := connection.WriteMessage(messageType, append([]byte(label+":"), payload...)); writeErr != nil {
				return
			}
		}
	}))
	t.Cleanup(provider.Close)
	return provider
}

func assertBackendIsolationWebSocketEcho(t *testing.T, client *websocket.Conn, label string, payload []byte) {
	t.Helper()
	require.NoError(t, client.WriteMessage(websocket.TextMessage, payload))
	messageType, received, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, append([]byte(label+":"), payload...), received)
}
