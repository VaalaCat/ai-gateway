package master

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// Production breaks caught: each transport path must preserve data-message
// types and control frames without opening a second provider connection.
func TestGenericAPIWebSocketE2ELocalDirectRelay(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	harness.assertWebSocketLocalDirectRelay(t)
}

func (h *genericAPIE2EHarness) assertWebSocketLocalDirectRelay(t *testing.T) {
	t.Helper()
	for _, path := range []app.RoutePath{app.RoutePathLocal, app.RoutePathDirect, app.RoutePathRelay} {
		path := path
		t.Run(string(path), func(t *testing.T) {
			var providerDials atomic.Int32
			providerPing := make(chan string, 1)
			providerDone := make(chan struct{})
			providerErrors := make(chan error, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				providerDials.Add(1)
				upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
				connection, err := upgrader.Upgrade(w, request, nil)
				if err != nil {
					recordGenericAPIE2EWebSocketError(providerErrors, err)
					return
				}
				defer close(providerDone)
				defer connection.Close()
				connection.SetPingHandler(func(payload string) error {
					providerPing <- payload
					return connection.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(time.Second))
				})
				for {
					messageType, payload, readErr := connection.ReadMessage()
					if readErr != nil {
						var closeErr *websocket.CloseError
						if !errors.As(readErr, &closeErr) || closeErr.Code != websocket.CloseNormalClosure {
							recordGenericAPIE2EWebSocketError(providerErrors, readErr)
						}
						return
					}
					if writeErr := connection.WriteMessage(messageType, payload); writeErr != nil {
						recordGenericAPIE2EWebSocketError(providerErrors, writeErr)
						return
					}
				}
			}))
			t.Cleanup(provider.Close)

			gateway, reporter, tunnelOpens := h.webSocketGatewayForPath(t, path, provider.URL)
			client, response, err := websocket.DefaultDialer.DialContext(
				t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
			)
			require.NoError(t, err)
			if response != nil {
				require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
			}

			assertGenericAPIE2EWebSocketEcho(t, client, websocket.TextMessage, []byte("text-message"))
			assertGenericAPIE2EWebSocketEcho(t, client, websocket.BinaryMessage, []byte{0, 1, 2, 255})
			pong := make(chan string, 1)
			client.SetPongHandler(func(payload string) error { pong <- payload; return nil })
			require.NoError(t, client.WriteControl(websocket.PingMessage, []byte("control-ping"), time.Now().Add(time.Second)))
			assertGenericAPIE2EWebSocketEcho(t, client, websocket.TextMessage, []byte("after-ping"))
			require.Equal(t, "control-ping", receiveGenericAPIE2EString(t, providerPing, "provider did not receive ping"))
			require.Equal(t, "control-ping", receiveGenericAPIE2EString(t, pong, "client did not receive pong"))
			require.NoError(t, client.WriteControl(
				websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
				time.Now().Add(time.Second),
			))
			_ = client.Close()

			requireReceive(t, providerDone, "provider WebSocket did not close")
			require.Eventually(t, func() bool { return len(reporter.Entries()) == 1 }, 3*time.Second, time.Millisecond)
			select {
			case providerErr := <-providerErrors:
				require.NoError(t, providerErr)
			default:
			}
			require.Equal(t, int32(1), providerDials.Load())
			if tunnelOpens != nil {
				require.Equal(t, int32(1), tunnelOpens.Load())
			}
			entries := reporter.Entries()
			require.True(t, entries[0].ProviderDispatchKnown)
			require.True(t, entries[0].ProviderDispatched)
			require.Equal(t, string(path), entries[0].AgentRoutePath)
		})
	}
}

func (h *genericAPIE2EHarness) webSocketGatewayForPath(
	t *testing.T,
	path app.RoutePath,
	providerURL string,
) (*httptest.Server, *genericAPIE2EUsageReporter, *atomic.Int32) {
	t.Helper()
	route := genericAPIE2EWebSocketRoute(providerURL)
	finder := genericAPIE2EWebSocketRouteFinder{route: route}
	upstreams := genericapi.NewAPIUpstreamPicker(
		genericAPIE2EUpstreamIndex{upstream: route.upstream}, genericAPIE2EBreakerFinder{},
	)
	local := genericapi.NewWebSocketHandler(genericapi.WebSocketHandlerOptions{Picker: upstreams})
	pick, remote, tunnelOpens := h.webSocketRoutePath(t, path, finder, local, upstreams)
	reporter := &genericAPIE2EUsageReporter{}
	handler := genericapi.NewHandler(genericapi.HandlerOptions{
		Finder: finder, Permission: genericAPIE2EPermissionGate{}, Quota: genericAPIE2EQuotaGate{},
		AgentPicker: genericAPIE2EAgentPicker{pick: pick}, ExecutionCapabilities: genericAPIE2ECapabilityFinder{},
		Usage: genericapi.NewUsageBuilder(func() time.Time { return time.Unix(10, 0) }), Reporter: reporter,
		MasterUsageSupport: genericAPIE2EMasterUsageSupport{}, SourceAgentID: "source-a",
		Handlers: map[string]genericapi.ProtocolHandler{
			genericapi.ProtocolWebSocket: genericapi.NewExecutionRouter("source-a", local, remote),
		},
	})
	gateway := httptest.NewServer(genericAPIE2ERouter(handler))
	t.Cleanup(gateway.Close)
	return gateway, reporter, tunnelOpens
}

func (h *genericAPIE2EHarness) webSocketRoutePath(
	t *testing.T,
	path app.RoutePath,
	finder genericapi.APIServiceRouteByIDFinder,
	local *genericapi.WebSocketHandler,
	upstreams genericapi.WebSocketUpstreamPicker,
) (genericapi.AgentPick, *genericapi.RemoteWebSocketHandler, *atomic.Int32) {
	t.Helper()
	if path == app.RoutePathLocal {
		return genericapi.AgentPick{ExecutionAgentID: "source-a"}, nil, nil
	}
	target := genericapi.NewWebSocketTargetHandler(genericapi.WebSocketTargetHandlerOptions{
		Finder: finder, Picker: upstreams,
	})
	source := startGenericAPIE2ESessionPair(t, path, nil, target)
	opens := &atomic.Int32{}
	pick := genericapi.AgentPick{
		ExecutionAgentID: "execution-a", AgentRouteID: 42,
		Target: models.Agent{
			AgentID: "execution-a", Status: consts.StatusEnabled,
			HTTPAddresses:        `[{"url":"http://execution-a.invalid","tag":"direct"}]`,
			DirectInboundEnabled: path == app.RoutePathDirect, RelayInboundEnabled: path == app.RoutePathRelay,
		},
	}
	options := genericapi.RemoteWebSocketHandlerOptions{
		TargetSupports: func(string) bool { return true }, AddressTag: "direct",
	}
	if path == app.RoutePathDirect {
		options.Direct = &genericAPIE2EDirectWebSocketOpener{session: source, opens: opens}
	} else {
		options.Relay = &genericAPIE2ERelayWebSocketOpener{session: source, opens: opens}
	}
	return pick, genericapi.NewRemoteWebSocketHandler(options), opens
}

type genericAPIE2EWebSocketRouteFixture struct {
	service  protocol.SyncedAPIService
	route    protocol.SyncedAPIRoute
	upstream protocol.SyncedAPIUpstream
}

func genericAPIE2EWebSocketRoute(providerURL string) genericAPIE2EWebSocketRouteFixture {
	return genericAPIE2EWebSocketRouteFixture{
		service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Name: "Weather", Status: consts.StatusEnabled},
		route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, BackendID: 10, Slug: "live", Protocols: []string{genericapi.ProtocolWebSocket},
			AllowedMethods: []string{http.MethodGet}, UpstreamPath: "/socket", Status: consts.StatusEnabled,
		},
		upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 10, Name: "WebSocket Primary", BaseURL: providerURL,
			AuthType: "none", Priority: 1, Weight: 1, Status: consts.StatusEnabled,
		},
	}
}

type genericAPIE2EWebSocketRouteFinder struct {
	route genericAPIE2EWebSocketRouteFixture
}

func (f genericAPIE2EWebSocketRouteFinder) Find(service, route, method, requestedProtocol string) (genericapi.ServiceRoute, error) {
	if service != f.route.service.Slug || route != f.route.route.Slug || method != http.MethodGet || requestedProtocol != genericapi.ProtocolWebSocket {
		return genericapi.ServiceRoute{}, genericapi.ErrExecutionUnavailable
	}
	return genericapi.ServiceRoute{Service: f.route.service, Route: f.route.route, Protocol: requestedProtocol}, nil
}

func (f genericAPIE2EWebSocketRouteFinder) FindServiceRouteByID(serviceID, routeID uint) (genericapi.ServiceRoute, error) {
	if serviceID != f.route.service.ID || routeID != f.route.route.ID {
		return genericapi.ServiceRoute{}, genericapi.ErrExecutionUnavailable
	}
	return genericapi.ServiceRoute{Service: f.route.service, Route: f.route.route, Protocol: genericapi.ProtocolWebSocket}, nil
}

type genericAPIE2EDirectWebSocketOpener struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (o *genericAPIE2EDirectWebSocketOpener) OpenWebSocketAPIStream(
	ctx context.Context,
	_ agentproxy.DirectSessionTarget,
	open app.WebSocketOpen,
) (app.WebSocketAPIStream, error) {
	o.opens.Add(1)
	return o.session.OpenWebSocketAPIStream(ctx, open)
}

type genericAPIE2ERelayWebSocketOpener struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (o *genericAPIE2ERelayWebSocketOpener) OpenWebSocketAPIStream(
	ctx context.Context,
	open app.WebSocketOpen,
) (app.WebSocketAPIStream, error) {
	o.opens.Add(1)
	return o.session.OpenWebSocketAPIStream(ctx, open)
}

func assertGenericAPIE2EWebSocketEcho(t *testing.T, client *websocket.Conn, messageType int, payload []byte) {
	t.Helper()
	require.NoError(t, client.WriteMessage(messageType, payload))
	gotType, gotPayload, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, messageType, gotType)
	require.Equal(t, payload, gotPayload)
}

func receiveGenericAPIE2EString(t *testing.T, values <-chan string, message string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal(message)
		return ""
	}
}

func recordGenericAPIE2EWebSocketError(target chan<- error, err error) {
	select {
	case target <- err:
	default:
	}
}
