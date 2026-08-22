package master

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masterapiusage "github.com/VaalaCat/ai-gateway/internal/master/apiusage"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

const (
	genericAPIE2ERequestBody     = "generic-api-e2e-body"
	genericAPIE2ERequestBodyHash = "634be56fe2e6af23bef003523f10014cb0aa449ce9bb2ea34f8bc4114c67e5ed"
)

type genericAPIE2EHarness struct{}

func newGenericAPIE2EHarness(t *testing.T) *genericAPIE2EHarness {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return &genericAPIE2EHarness{}
}

// Production break caught: selecting local, Direct, or Relay twice must never
// replay a body or create a second provider dispatch.
func TestGenericAPIE2ELocalDirectRelayMatrixSingleDispatch(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	harness.assertLocalDirectRelayMatrix(t)
}

// Production breaks caught: request/response trailers must survive both
// tunnel paths, and a provider response must cancel an unfinished upload.
func TestGenericAPIE2ERequestResponseTrailerAndEarlyResponse(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	for _, path := range []app.RoutePath{app.RoutePathLocal, app.RoutePathDirect, app.RoutePathRelay} {
		path := path
		t.Run(string(path)+"_trailers", func(t *testing.T) {
			harness.assertHTTPBidirectionalTrailers(t, path)
		})
		t.Run(string(path)+"_early_response", func(t *testing.T) {
			harness.assertHTTPEarlyResponse(t, path)
		})
	}
}

// Production breaks caught: every pre-dispatch gate must retain its public
// status, while successful usage is charged at the service price read later by
// the Master settler rather than a stale source-side snapshot.
func TestGenericAPIE2ERBACQuotaLimiterAndSettlement(t *testing.T) {
	harness := newGenericAPIE2EHarness(t)
	tests := []struct {
		name          string
		gates         genericAPIE2EGates
		wantStatus    int
		wantCode      string
		wantDispatch  bool
		settleCurrent bool
	}{
		{name: "rbac forbidden", gates: genericAPIE2EGates{permissionErr: genericapi.ErrAPIForbidden}, wantStatus: http.StatusForbidden, wantCode: genericapi.CodeAPIForbidden},
		{name: "quota exhausted", gates: genericAPIE2EGates{quotaErr: genericapi.ErrInsufficientQuota}, wantStatus: http.StatusPaymentRequired, wantCode: genericapi.CodeInsufficientQuota},
		{name: "limiter rejected", gates: genericAPIE2EGates{limiterErr: genericapi.ErrAPIRateLimited}, wantStatus: http.StatusTooManyRequests, wantCode: genericapi.CodeRateLimited},
		{name: "settled success", wantStatus: http.StatusOK, wantDispatch: true, settleCurrent: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			var providerCalls atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				providerCalls.Add(1)
				_, err := io.Copy(io.Discard, request.Body)
				require.NoError(t, err)
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(provider.Close)
			router, reporter, _ := harness.httpRouterWithGates(t, app.RoutePathLocal, provider.URL, test.gates)
			request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString("billing-body"))
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			require.Equal(t, test.wantStatus, response.Code)
			require.Equal(t, test.wantDispatch, providerCalls.Load() == 1)
			if test.wantCode != "" {
				require.Contains(t, response.Body.String(), `"code":"`+test.wantCode+`"`)
			}
			entries := reporter.Entries()
			require.Len(t, entries, 1)
			require.Equal(t, test.wantDispatch, entries[0].ProviderDispatched)
			if test.settleCurrent {
				finder := &genericAPIE2ECurrentServiceFinder{service: models.APIService{ID: 7, PricePerCall: 100}}
				finder.service.PricePerCall = 12345
				settlement, err := masterapiusage.NewAPIUsageSettler(finder).Settle(t.Context(), entries[0])
				require.NoError(t, err)
				require.Equal(t, int64(12345), settlement.Quota)
			}
		})
	}
}

func TestGenericAPIRootRouteE2EForwardsDynamicFirstSegment(t *testing.T) {
	providerPath := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		providerPath <- request.URL.RequestURI()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(provider.Close)

	fixture := genericAPIE2ERoute(provider.URL)
	fixture.route.Slug = ""
	fixture.route.ForwardSubpath = true
	index := agentcache.NewAPIIndex()
	require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{fixture.service}))
	require.NoError(t, index.ReplaceRoutes([]protocol.SyncedAPIRoute{fixture.route}))
	require.NoError(t, index.ReplaceUpstreams([]protocol.SyncedAPIUpstream{fixture.upstream}))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))

	upstreams := genericapi.NewAPIUpstreamPicker(index, genericAPIE2EBreakerFinder{})
	handler := genericapi.NewHandler(genericapi.HandlerOptions{
		Finder:     genericapi.NewServiceFinder(index),
		Permission: genericAPIE2EPermissionGate{}, Quota: genericAPIE2EQuotaGate{},
		Handlers: map[string]genericapi.ProtocolHandler{
			genericapi.ProtocolHTTP: genericapi.NewHTTPHandler(upstreams, genericapi.NewHTTPTransport("")),
		},
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/acme/users?active=true", nil)

	genericAPIE2ERouter(handler).ServeHTTP(response, request)

	require.Equal(t, http.StatusNoContent, response.Code)
	require.Equal(t, "/provider/acme/users?active=true", <-providerPath)
}

func (h *genericAPIE2EHarness) assertLocalDirectRelayMatrix(t *testing.T) {
	t.Helper()
	for _, path := range []app.RoutePath{app.RoutePathLocal, app.RoutePathDirect, app.RoutePathRelay} {
		path := path
		t.Run(string(path), func(t *testing.T) {
			var providerCalls atomic.Int32
			providerHashes := make(chan string, 1)
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				providerCalls.Add(1)
				body, err := io.ReadAll(request.Body)
				require.NoError(t, err)
				sum := sha256.Sum256(body)
				providerHashes <- hex.EncodeToString(sum[:])
				w.WriteHeader(http.StatusCreated)
				_, err = io.WriteString(w, "provider-response")
				require.NoError(t, err)
			}))
			t.Cleanup(provider.Close)
			router, reporter, tunnelOpens := h.httpRouterForPath(t, path, provider.URL)
			request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString(genericAPIE2ERequestBody))
			request.Header.Set(consts.HeaderXRequestID, "generic-api-e2e-"+string(path))
			response := httptest.NewRecorder()

			router.ServeHTTP(response, request)

			entries := reporter.Entries()
			if response.Code != http.StatusCreated {
				t.Logf("path=%s tunnel_opens=%v provider_calls=%d usage=%+v", path, tunnelOpens, providerCalls.Load(), entries)
			}
			require.Equalf(t, http.StatusCreated, response.Code, "gateway response: %s", response.Body.String())
			require.Equal(t, "provider-response", response.Body.String())
			require.Equal(t, int32(1), providerCalls.Load())
			require.Equal(t, genericAPIE2ERequestBodyHash, <-providerHashes)
			if tunnelOpens != nil {
				require.Equal(t, int32(1), tunnelOpens.Load())
			}
			require.Len(t, entries, 1)
			require.True(t, entries[0].ProviderDispatchKnown)
			require.True(t, entries[0].ProviderDispatched)
			require.Equal(t, string(path), entries[0].AgentRoutePath)
		})
	}
}

func (h *genericAPIE2EHarness) assertHTTPBidirectionalTrailers(t *testing.T, path app.RoutePath) {
	t.Helper()
	var providerCalls atomic.Int32
	requestTrailers := make(chan string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		providerCalls.Add(1)
		_, err := io.Copy(io.Discard, request.Body)
		require.NoError(t, err)
		requestTrailers <- request.Trailer.Get("X-Request-Final")
		w.Header().Add("Trailer", "X-Response-Final")
		w.WriteHeader(http.StatusOK)
		_, err = io.WriteString(w, "trailer-response")
		require.NoError(t, err)
		w.Header().Set("X-Response-Final", "response-final")
	}))
	t.Cleanup(provider.Close)
	router, _, tunnelOpens := h.httpRouterForPath(t, path, provider.URL)
	request := httptest.NewRequest(http.MethodPost, "/v1/api/weather/current", bytes.NewBufferString("trailer-body"))
	request.Trailer = http.Header{"X-Request-Final": {"request-final"}}
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equalf(t, http.StatusOK, response.Code, "gateway response: %s", response.Body.String())
	require.Equal(t, "trailer-response", response.Body.String())
	require.Equal(t, "request-final", <-requestTrailers)
	require.Equal(t, "response-final", response.Result().Trailer.Get("X-Response-Final"))
	require.Equal(t, int32(1), providerCalls.Load())
	if tunnelOpens != nil {
		require.Equal(t, int32(1), tunnelOpens.Load())
	}
}

func (h *genericAPIE2EHarness) assertHTTPEarlyResponse(t *testing.T, path app.RoutePath) {
	t.Helper()
	firstUpload := bytes.Repeat([]byte("a"), 64<<10)
	body := newGenericAPIE2EBlockedBody(firstUpload)
	var providerCalls atomic.Int32
	providerRead := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		providerCalls.Add(1)
		require.NoError(t, http.NewResponseController(w).EnableFullDuplex())
		buffer := make([]byte, len(firstUpload))
		_, err := io.ReadFull(request.Body, buffer)
		require.NoError(t, err)
		close(providerRead)
		w.WriteHeader(http.StatusAccepted)
		_, err = io.WriteString(w, "early")
		require.NoError(t, err)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		_, err = io.Copy(io.Discard, request.Body)
		require.NoError(t, err)
		_, err = io.WriteString(w, "-late")
		require.NoError(t, err)
	}))
	t.Cleanup(func() {
		body.Release()
		provider.Close()
	})
	router, _, tunnelOpens := h.httpRouterForPath(t, path, provider.URL)
	gateway := httptest.NewServer(router)
	t.Cleanup(gateway.Close)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/v1/api/weather/current", body)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	requireReceive(t, providerRead, "provider did not receive the first upload chunk")
	early := make([]byte, len("early"))
	_, err = io.ReadFull(response.Body, early)
	require.NoError(t, err)
	require.Equal(t, "early", string(early), "client must receive response bytes while upload remains blocked")
	body.Release()
	late, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "-late", string(late))
	requireReceive(t, body.closed, "gateway did not close the unfinished request body")
	require.Equal(t, http.StatusAccepted, response.StatusCode)
	require.Equal(t, int32(1), providerCalls.Load())
	if tunnelOpens != nil {
		require.Equal(t, int32(1), tunnelOpens.Load())
	}
}

type genericAPIE2EBlockedBody struct {
	mu        sync.Mutex
	first     []byte
	release   chan struct{}
	closed    chan struct{}
	closeOnce sync.Once
	relOnce   sync.Once
}

func newGenericAPIE2EBlockedBody(first []byte) *genericAPIE2EBlockedBody {
	return &genericAPIE2EBlockedBody{
		first: append([]byte(nil), first...), release: make(chan struct{}), closed: make(chan struct{}),
	}
}

func (b *genericAPIE2EBlockedBody) Read(target []byte) (int, error) {
	b.mu.Lock()
	if len(b.first) > 0 {
		count := copy(target, b.first)
		b.first = b.first[count:]
		b.mu.Unlock()
		return count, nil
	}
	b.mu.Unlock()
	select {
	case <-b.release:
		return 0, io.EOF
	case <-b.closed:
		return 0, io.ErrClosedPipe
	}
}

func (b *genericAPIE2EBlockedBody) Close() error {
	b.closeOnce.Do(func() { close(b.closed) })
	return nil
}

func (b *genericAPIE2EBlockedBody) Release() { b.relOnce.Do(func() { close(b.release) }) }

func requireReceive(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}

func (h *genericAPIE2EHarness) httpRouterForPath(
	t *testing.T,
	path app.RoutePath,
	providerURL string,
) (*gin.Engine, *genericAPIE2EUsageReporter, *atomic.Int32) {
	t.Helper()
	return h.httpRouterWithGates(t, path, providerURL, genericAPIE2EGates{})
}

type genericAPIE2EGates struct {
	permissionErr error
	quotaErr      error
	limiterErr    error
}

func (h *genericAPIE2EHarness) httpRouterWithGates(
	t *testing.T,
	path app.RoutePath,
	providerURL string,
	gates genericAPIE2EGates,
) (*gin.Engine, *genericAPIE2EUsageReporter, *atomic.Int32) {
	t.Helper()
	route := genericAPIE2ERoute(providerURL)
	finder := genericAPIE2ERouteFinder{route: route}
	upstreams := genericapi.NewAPIUpstreamPicker(
		genericAPIE2EUpstreamIndex{upstream: route.upstream},
		genericAPIE2EBreakerFinder{},
	)
	local := genericapi.NewHTTPHandler(upstreams, genericapi.NewHTTPTransport(""))
	pick, remote, tunnelOpens := h.httpRoutePath(t, path, finder, local)
	reporter := &genericAPIE2EUsageReporter{}
	handler := genericapi.NewHandler(genericapi.HandlerOptions{
		Finder: finder, Permission: genericAPIE2EPermissionGate{err: gates.permissionErr}, Quota: genericAPIE2EQuotaGate{err: gates.quotaErr},
		Limiter:     genericAPIE2ELimiterGate{err: gates.limiterErr},
		AgentPicker: genericAPIE2EAgentPicker{pick: pick}, ExecutionCapabilities: genericAPIE2ECapabilityFinder{},
		Usage: genericapi.NewUsageBuilder(func() time.Time { return time.Unix(10, 0) }), Reporter: reporter,
		MasterUsageSupport: genericAPIE2EMasterUsageSupport{}, SourceAgentID: "source-a",
		Handlers: map[string]genericapi.ProtocolHandler{
			genericapi.ProtocolHTTP: genericapi.NewExecutionRouter("source-a", local, remote),
		},
	})
	return genericAPIE2ERouter(handler), reporter, tunnelOpens
}

func (h *genericAPIE2EHarness) httpRoutePath(
	t *testing.T,
	path app.RoutePath,
	finder genericapi.APIServiceRouteByIDFinder,
	local *genericapi.HTTPHandler,
) (genericapi.AgentPick, *genericapi.RemoteHTTPHandler, *atomic.Int32) {
	t.Helper()
	if path == app.RoutePathLocal {
		return genericapi.AgentPick{ExecutionAgentID: "source-a"}, nil, nil
	}
	target := genericapi.NewAPITargetHandler(finder, local)
	source := startGenericAPIE2ESessionPair(t, path, target, nil)
	opens := &atomic.Int32{}
	pick := genericapi.AgentPick{
		ExecutionAgentID: "execution-a", AgentRouteID: 42,
		Target: models.Agent{
			AgentID: "execution-a", Status: consts.StatusEnabled,
			HTTPAddresses:        `[{"url":"http://execution-a.invalid","tag":"direct"}]`,
			DirectInboundEnabled: path == app.RoutePathDirect, RelayInboundEnabled: path == app.RoutePathRelay,
		},
	}
	if path == app.RoutePathDirect {
		return pick, genericapi.NewRemoteHTTPHandler(genericapi.RemoteHTTPHandlerOptions{
			Direct: &genericAPIE2EDirectHTTPBuilder{session: source, opens: opens}, AddressTag: "direct",
		}), opens
	}
	return pick, genericapi.NewRemoteHTTPHandler(genericapi.RemoteHTTPHandlerOptions{
		Relay: &genericAPIE2ERelayHTTPOpener{session: source, opens: opens},
	}), opens
}

type genericAPIE2ERouteFixture struct {
	service  protocol.SyncedAPIService
	route    protocol.SyncedAPIRoute
	upstream protocol.SyncedAPIUpstream
}

func genericAPIE2ERoute(providerURL string) genericAPIE2ERouteFixture {
	return genericAPIE2ERouteFixture{
		service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Name: "Weather", Status: consts.StatusEnabled},
		route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, BackendID: 10, Slug: "current", Protocols: []string{genericapi.ProtocolHTTP},
			AllowedMethods: []string{http.MethodPost}, UpstreamPath: "/provider", Status: consts.StatusEnabled,
		},
		upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 10, Name: "Primary", BaseURL: providerURL,
			AuthType: "none", Priority: 1, Weight: 1, Status: consts.StatusEnabled,
		},
	}
}

type genericAPIE2ERouteFinder struct{ route genericAPIE2ERouteFixture }

func (f genericAPIE2ERouteFinder) Find(service, requestPath, method, requestedProtocol string) (genericapi.ServiceRoute, string, error) {
	if service != f.route.service.Slug || requestPath != "/"+f.route.route.Slug || method != http.MethodPost || requestedProtocol != genericapi.ProtocolHTTP {
		return genericapi.ServiceRoute{}, "", genericapi.ErrExecutionUnavailable
	}
	return genericapi.ServiceRoute{Service: f.route.service, Route: f.route.route, Protocol: requestedProtocol}, "", nil
}

func (f genericAPIE2ERouteFinder) FindServiceRouteByID(serviceID, routeID uint) (genericapi.ServiceRoute, error) {
	if serviceID != f.route.service.ID || routeID != f.route.route.ID {
		return genericapi.ServiceRoute{}, genericapi.ErrExecutionUnavailable
	}
	return genericapi.ServiceRoute{Service: f.route.service, Route: f.route.route, Protocol: genericapi.ProtocolHTTP}, nil
}

type genericAPIE2EUpstreamIndex struct{ upstream protocol.SyncedAPIUpstream }

func (i genericAPIE2EUpstreamIndex) UpstreamsForBackend(backendID uint) []protocol.SyncedAPIUpstream {
	if backendID != i.upstream.BackendID {
		return nil
	}
	return []protocol.SyncedAPIUpstream{i.upstream}
}

type genericAPIE2EBreakerFinder struct{}

func (genericAPIE2EBreakerFinder) Healthy(uint) bool { return true }
func (genericAPIE2EBreakerFinder) TryAcquire(uint) (genericapi.APIBreakerPermit, bool) {
	return genericAPIE2EBreakerPermit{}, true
}

type genericAPIE2EBreakerPermit struct{}

func (genericAPIE2EBreakerPermit) Finish(genericapi.APIBreakerCompletion) {}

type genericAPIE2EPermissionGate struct{ err error }

func (g genericAPIE2EPermissionGate) AllowInvoke(context.Context, uint, uint, uint, uint) error {
	return g.err
}

type genericAPIE2EQuotaGate struct{ err error }

func (g genericAPIE2EQuotaGate) Allow(context.Context, uint, protocol.SyncedAPIService) error {
	return g.err
}

type genericAPIE2ELimiterGate struct{ err error }

func (g genericAPIE2ELimiterGate) Acquire(context.Context, genericapi.APIRequestFacts) (genericapi.APIPermit, error) {
	return nil, g.err
}

type genericAPIE2ECurrentServiceFinder struct{ service models.APIService }

func (f *genericAPIE2ECurrentServiceFinder) FindByID(context.Context, uint) (*models.APIService, error) {
	value := f.service
	return &value, nil
}

type genericAPIE2EAgentPicker struct{ pick genericapi.AgentPick }

func (p genericAPIE2EAgentPicker) Pick(uint, uint, uint, string) (genericapi.AgentPick, error) {
	return p.pick, nil
}

type genericAPIE2ECapabilityFinder struct{}

func (genericAPIE2ECapabilityFinder) SupportsGenericAPIExecution(string) bool { return true }

type genericAPIE2EMasterUsageSupport struct{}

func (genericAPIE2EMasterUsageSupport) SupportsGenericAPIUsage() bool { return true }

type genericAPIE2EUsageReporter struct {
	mu      sync.Mutex
	entries []protocol.APIUsageEntry
}

func (r *genericAPIE2EUsageReporter) EnqueueAPI(entry protocol.APIUsageEntry) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, entry)
	return nil
}

func (r *genericAPIE2EUsageReporter) Entries() []protocol.APIUsageEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]protocol.APIUsageEntry(nil), r.entries...)
}

func genericAPIE2ERouter(handler *genericapi.Handler) *gin.Engine {
	router := gin.New()
	v1 := router.Group("/v1")
	v1.Use(func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 5, GroupID: 3, TokenID: 2})
		c.Next()
	})
	genericapi.RegisterRoutes(v1, handler)
	return router
}

type genericAPIE2ERelayHTTPOpener struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (o *genericAPIE2ERelayHTTPOpener) OpenHTTPAPIStream(ctx context.Context, open app.APIOpen) (app.HTTPAPIStream, error) {
	o.opens.Add(1)
	return o.session.OpenHTTPAPIStream(ctx, open)
}

type genericAPIE2EDirectHTTPBuilder struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (b *genericAPIE2EDirectHTTPBuilder) BuildDirectHTTPAPITransport(context.Context, agentproxy.DirectSessionTarget) (agentproxy.DirectHTTPAPITransport, error) {
	return genericAPIE2EDirectHTTPTransport{session: b.session, opens: b.opens}, nil
}

type genericAPIE2EDirectHTTPTransport struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (genericAPIE2EDirectHTTPTransport) TransportIdentity() agentproxy.DirectTransportIdentity {
	return agentproxy.DirectTransportIdentity{}
}

func (t genericAPIE2EDirectHTTPTransport) AcquireHTTPAPIStream(context.Context) (agentproxy.DirectHTTPAPIStreamReservation, error) {
	return genericAPIE2EDirectHTTPReservation{session: t.session, opens: t.opens}, nil
}

type genericAPIE2EDirectHTTPReservation struct {
	session *agenttunnel.Session
	opens   *atomic.Int32
}

func (genericAPIE2EDirectHTTPReservation) TransportIdentity() agentproxy.DirectTransportIdentity {
	return agentproxy.DirectTransportIdentity{}
}
func (genericAPIE2EDirectHTTPReservation) AddressFingerprint() string { return "e2e-direct" }
func (r genericAPIE2EDirectHTTPReservation) OpenHTTPAPIStream(ctx context.Context, open app.APIOpen) (app.HTTPAPIStream, error) {
	r.opens.Add(1)
	return r.session.OpenHTTPAPIStream(ctx, open)
}
func (genericAPIE2EDirectHTTPReservation) Release() {}

func startGenericAPIE2ESessionPair(
	t *testing.T,
	path app.RoutePath,
	httpTarget agenttunnel.APITargetHandler,
	webSocketTarget agenttunnel.WebSocketTargetHandler,
) *agenttunnel.Session {
	t.Helper()
	client, peer := genericAPIE2EWebSocketPair(t)
	limits := wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 4 << 10, InitialStreamWindow: 64 << 10,
		MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 8,
	}
	direction := agenttunnel.SessionDirectionRelay
	sourceOptions := agenttunnel.SessionOptions{Direction: direction, PingInterval: time.Hour, PongTimeout: time.Hour}
	targetOptions := agenttunnel.SessionOptions{
		Direction: direction, PingInterval: time.Hour, PongTimeout: time.Hour,
		TargetHandler: agenttunnel.NewTargetHandler(agenttunnel.TargetHandlerOptions{
			TargetAgentID: "execution-a", Router: http.NotFoundHandler(),
		}),
		APITargetHandler: httpTarget, WebSocketTargetHandler: webSocketTarget,
	}
	if path == app.RoutePathDirect {
		sourceOptions.Direction = agenttunnel.SessionDirectionDirectOutgoing
		targetOptions.Direction = agenttunnel.SessionDirectionDirectIncoming
		targetOptions.BoundSourceAgentID = "source-a"
		targetOptions.AdmissionDeadline = time.Now().Add(time.Hour)
		targetOptions.SourceEnabled = func(source string) bool { return source == "source-a" }
		targetOptions.TargetStatusEnabled = func() bool { return true }
	}
	source := agenttunnel.NewSession(client, 21, limits, sourceOptions)
	target := agenttunnel.NewSession(peer, 22, limits, targetOptions)
	var runs sync.WaitGroup
	runs.Add(2)
	go func() { defer runs.Done(); _ = source.Run(t.Context()) }()
	go func() { defer runs.Done(); _ = target.Run(t.Context()) }()
	t.Cleanup(func() {
		source.Cancel(context.Canceled)
		target.Cancel(context.Canceled)
		runs.Wait()
	})
	return source
}

func genericAPIE2EWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		connection, err := upgrader.Upgrade(w, request, nil)
		if err == nil {
			accepted <- connection
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	peer := <-accepted
	t.Cleanup(func() {
		_ = client.Close()
		_ = peer.Close()
		server.Close()
	})
	return client, peer
}
