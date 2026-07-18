package agent

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	bodypkg "github.com/VaalaCat/ai-gateway/internal/agent/body"
	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	pkgws "github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentMetricEndpointUsesInjectedRegistry(t *testing.T) {
	registry := prometheus.NewRegistry()
	relayMetrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
	server := &Server{Router: gin.New(), RelayMetrics: relayMetrics}
	server.setupMetricsRoute()
	relayMetrics.IncDirectProbe(pkgmetrics.ProbeVerified)

	response := httptest.NewRecorder()
	server.Router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "agent_direct_probe_total")
	require.Contains(t, response.Body.String(), `result="verified"`)
}

func TestAgentAuthCacheSlotUsesPointerMatchedReplacementAndCleanup(t *testing.T) {
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	s := &Server{Store: store}
	first := agentauthcache.NewCache(nil, agentauthcache.CacheOptions{})
	second := agentauthcache.NewCache(nil, agentauthcache.CacheOptions{})
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	store.SetAgentCapabilities("stale-agent", []string{"stale-capability"})

	if previous := s.replaceAgentAuthCache(first); previous != nil {
		t.Fatalf("first install replaced %p", previous)
	}
	if got := store.GetAgentCapabilities("stale-agent"); got != nil {
		t.Fatalf("first session install retained stale capabilities: %#v", got)
	}
	store.SetAgentCapabilities("first-session-agent", []string{"first-capability"})
	if got := s.borrowAgentAuthCache(); got != first {
		t.Fatalf("borrowed cache = %p, want first %p", got, first)
	}
	if previous := s.replaceAgentAuthCache(second); previous != first {
		t.Fatalf("replacement returned %p, want first %p", previous, first)
	}
	if got := store.GetAgentCapabilities("first-session-agent"); got != nil {
		t.Fatalf("replacement retained first-session capabilities: %#v", got)
	}
	store.SetAgentCapabilities("replacement-agent", []string{"replacement-capability"})
	if s.clearAgentAuthCache(first) {
		t.Fatal("old-session cleanup cleared the replacement cache")
	}
	if got := store.GetAgentCapabilities("replacement-agent"); len(got) != 1 || got[0] != "replacement-capability" {
		t.Fatalf("old-session cleanup cleared replacement capabilities: %#v", got)
	}
	if got := s.borrowAgentAuthCache(); got != second {
		t.Fatalf("old cleanup left cache = %p, want second %p", got, second)
	}
	if !s.clearAgentAuthCache(second) {
		t.Fatal("current cache cleanup did not clear its own slot")
	}
	if got := store.GetAgentCapabilities("replacement-agent"); got != nil {
		t.Fatalf("current cleanup retained replacement capabilities: %#v", got)
	}
	if got := s.borrowAgentAuthCache(); got != nil {
		t.Fatalf("cache slot retained %p after matched cleanup", got)
	}
}

type agentAuthControlFunc func(context.Context, string, any) (json.RawMessage, error)

func (f agentAuthControlFunc) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return f(ctx, method, params)
}

func TestCachedForwardTicketServerAdapterIgnoresRequestContextAndNeverIssues(t *testing.T) {
	initialNow := time.Unix(2_000_000_000, 0).UTC()
	var clockMu sync.Mutex
	now := initialNow
	nowFunc := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	setNow := func(next time.Time) {
		clockMu.Lock()
		now = next
		clockMu.Unlock()
	}
	refreshStarted := make(chan struct{})
	releaseRefresh := make(chan struct{})
	var releaseOnce sync.Once
	var issues atomic.Int32
	control := agentAuthControlFunc(func(ctx context.Context, method string, _ any) (json.RawMessage, error) {
		switch method {
		case consts.RPCAgentAuthBootstrap:
			return marshalServerTest(protocol.AuthBootstrapResponse{
				MasterInstanceID: "master-a",
				Capabilities:     []string{protocol.AgentCapabilityForwardV1},
				SigningKeys: []pkgagentauth.PublicKey{{
					KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, 32),
				}},
			})
		case consts.RPCAgentIssueForwardTicket:
			switch issues.Add(1) {
			case 1:
				return marshalServerTest(protocol.TicketResponse{
					Token: "forward-1", ExpiresAt: initialNow.Add(7 * 24 * time.Hour).Unix(),
				})
			case 2:
				close(refreshStarted)
				select {
				case <-releaseRefresh:
					return marshalServerTest(protocol.TicketResponse{
						Token: "forward-2", ExpiresAt: nowFunc().Add(7 * 24 * time.Hour).Unix(),
					})
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			default:
				return nil, errors.New("unexpected extra forward issue")
			}
		default:
			return nil, errors.New("unexpected control method")
		}
	})
	cache := agentauthcache.NewCache(control, agentauthcache.CacheOptions{Now: nowFunc})
	require.NoError(t, cache.Run(context.Background()))
	server := &Server{}
	server.replaceAgentAuthCache(cache)
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(releaseRefresh) })
		server.clearAgentAuthCache(cache)
		cache.Close()
		<-cache.Done()
	})
	require.Eventually(t, func() bool {
		ticket, err := cache.CachedForwardTicket()
		return err == nil && ticket == pkgagentauth.ForwardTicket("forward-1")
	}, time.Second, time.Millisecond)

	setNow(initialNow.Add(25 * time.Hour))
	select {
	case <-refreshStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("background forward refresh did not start")
	}
	requestCtx, cancel := context.WithCancel(context.Background())
	cancel()
	result := make(chan struct {
		ticket pkgagentauth.ForwardTicket
		err    error
	}, 1)
	go func() {
		ticket, err := server.cachedForwardTicket(requestCtx)
		result <- struct {
			ticket pkgagentauth.ForwardTicket
			err    error
		}{ticket: ticket, err: err}
	}()
	select {
	case got := <-result:
		require.NoError(t, got.err)
		require.Equal(t, pkgagentauth.ForwardTicket("forward-1"), got.ticket)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("server cached forward adapter blocked on control RPC")
	}
	require.Equal(t, int32(2), issues.Load())
}

func TestEndClientDoesNotClearReplacementControlSession(t *testing.T) {
	syncer := &agentcache.Syncer{}
	server := &Server{Syncer: syncer}
	staleClient := &pkgws.Client{}
	staleSession := syncer.BeginControlSession(staleClient)
	server.client = staleClient

	replacementClient := &pkgws.Client{}
	replacementSession := syncer.BeginControlSession(replacementClient)
	server.client = replacementClient
	server.endClient(staleSession, staleClient)

	require.Same(t, replacementSession, syncer.CurrentControlSession())
	require.Same(t, replacementClient, server.getClient())

	server.endClient(replacementSession, replacementClient)
	require.Nil(t, syncer.CurrentControlSession())
	require.Nil(t, server.getClient())
}

type controlledControlSession struct {
	done        chan struct{}
	closeCalled chan struct{}
	closeOnce   sync.Once
}

func (c *controlledControlSession) Done() <-chan struct{} { return c.done }

func (c *controlledControlSession) Close() error {
	c.closeOnce.Do(func() { close(c.closeCalled) })
	return nil
}

type orderedAgentControlClient struct {
	mu                   sync.Mutex
	methods              []string
	capabilitiesUpdate   protocol.AgentCapabilitiesUpdate
	handlers             map[string]appkg.NotificationHandler
	inlineHandlers       map[string]appkg.NotificationHandler
	capabilityOnRegister json.RawMessage
	fullSyncAgentItems   json.RawMessage
	notifyErr            error
	onNotify             func()
}

func newOrderedAgentControlClient() *orderedAgentControlClient {
	return &orderedAgentControlClient{
		handlers:       make(map[string]appkg.NotificationHandler),
		inlineHandlers: make(map[string]appkg.NotificationHandler),
	}
}

func (c *orderedAgentControlClient) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.recordMethod(method)
	switch method {
	case consts.RPCAgentAuthBootstrap:
		return marshalServerTest(protocol.AuthBootstrapResponse{
			MasterInstanceID: "master-a",
			SigningKeys: []pkgagentauth.PublicKey{{
				KeyID:     "key-a",
				Algorithm: "EdDSA",
				Key:       make([]byte, 32),
			}},
		})
	case consts.RPCSyncFullSync:
		response := protocol.FullSyncResponse{Items: []byte("[]"), Version: 1, Keyset: true}
		if request, ok := params.(protocol.FullSyncRequest); ok && request.Entity == "agent" {
			if len(c.fullSyncAgentItems) > 0 {
				response.Items = append(json.RawMessage(nil), c.fullSyncAgentItems...)
			}
			response.SnapshotContract = protocol.AgentFullSyncSnapshotContractV1
		}
		return marshalServerTest(response)
	default:
		return nil, errors.New("unexpected control call")
	}
}

func (c *orderedAgentControlClient) Notify(method string, params any) error {
	c.recordMethod(method)
	if method != consts.RPCSyncAgentCapabilities {
		return errors.New("unexpected control notification")
	}
	update, ok := params.(protocol.AgentCapabilitiesUpdate)
	if !ok {
		return errors.New("invalid capability update type")
	}
	c.mu.Lock()
	c.capabilitiesUpdate = update
	c.mu.Unlock()
	if c.onNotify != nil {
		c.onNotify()
	}
	return c.notifyErr
}

func (c *orderedAgentControlClient) OnNotification(method string, handler appkg.NotificationHandler) {
	c.mu.Lock()
	c.handlers[method] = handler
	c.mu.Unlock()
}

func (c *orderedAgentControlClient) OnNotificationInline(method string, handler appkg.NotificationHandler) {
	c.mu.Lock()
	c.inlineHandlers[method] = handler
	capabilityOnRegister := append(json.RawMessage(nil), c.capabilityOnRegister...)
	c.mu.Unlock()
	if method == consts.RPCSyncAgentCapabilities && capabilityOnRegister != nil {
		_, _ = handler(context.Background(), capabilityOnRegister)
	}
}

func (c *orderedAgentControlClient) Close() error { return nil }
func (c *orderedAgentControlClient) ReadLoop()    {}

func (c *orderedAgentControlClient) recordMethod(method string) {
	c.mu.Lock()
	c.methods = append(c.methods, method)
	c.mu.Unlock()
}

func (c *orderedAgentControlClient) snapshot() ([]string, protocol.AgentCapabilitiesUpdate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.methods...), c.capabilitiesUpdate
}

func (c *orderedAgentControlClient) inlineHandler(method string) appkg.NotificationHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inlineHandlers[method]
}

func (c *orderedAgentControlClient) handler(method string) appkg.NotificationHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.handlers[method]
}

func TestRegisterControlHandlersRegistersAgentOperation(t *testing.T) {
	client := newOrderedAgentControlClient()
	server := &Server{Cfg: &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: ":0"}}, Logger: zap.NewNop()}
	server.registerControlHandlers(client)

	handler := client.handler(consts.RPCAgentOperation)
	if handler == nil {
		t.Fatal("agent.operation handler was not registered")
	}
	_, err := handler(t.Context(), json.RawMessage(`{"operation":"unknown"}`))
	if err == nil || !strings.Contains(err.Error(), "unsupported agent operation") {
		t.Fatalf("handler error = %v, want unsupported operation", err)
	}
}

func TestAgentAuthCacheSessionStartsBeforeCapabilitiesAndFullSync(t *testing.T) {
	client := newOrderedAgentControlClient()
	client.capabilityOnRegister, _ = marshalServerTest(protocol.AgentCapabilitiesUpdate{
		AgentID:      "snapshot-peer",
		Capabilities: []string{"snapshot-capability"},
	})
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	store.SetAgentCapabilities("offline-stale-peer", []string{"stale-capability"})
	server := &Server{
		Cfg:    &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: ":0"}},
		Logger: zap.NewNop(),
		Creds:  &enrollment.Credentials{AgentID: "agent-a", Secret: "secret-a"},
		Store:  store,
		Syncer: agentcache.NewSyncer(store, client, nil, zap.NewNop(), time.Hour),
	}

	owned, err := server.startAgentAuthSession(context.Background(), client)
	if err != nil {
		t.Fatalf("start agent auth session: %v", err)
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			server.stopAgentAuthSession(owned)
		}
	})
	if got := store.GetAgentCapabilities("offline-stale-peer"); got != nil {
		t.Fatalf("new control session retained offline peer capabilities: %#v", got)
	}
	if got := store.GetAgentCapabilities("snapshot-peer"); len(got) != 1 || got[0] != "snapshot-capability" {
		t.Fatalf("session clear raced with the registered capability handler: %#v", got)
	}
	methods, update := client.snapshot()
	if len(methods) < 3 {
		t.Fatalf("control methods = %#v", methods)
	}
	wantPrefix := []string{
		consts.RPCAgentAuthBootstrap,
		consts.RPCSyncAgentCapabilities,
		consts.RPCSyncFullSync,
	}
	for i, want := range wantPrefix {
		if methods[i] != want {
			t.Fatalf("control method[%d] = %q, want %q; all=%#v", i, methods[i], want, methods)
		}
	}
	// behavior change: the authenticated runtime advertises relay, forward-auth, Direct ingress, Relay ping, and Token routing contracts.
	if update.AgentID != "agent-a" || len(update.Capabilities) != 5 ||
		update.Capabilities[0] != protocol.AgentCapabilityTunnelV1 ||
		update.Capabilities[1] != protocol.AgentCapabilityForwardV1 ||
		update.Capabilities[2] != protocol.AgentCapabilityDirectIngressV1 ||
		update.Capabilities[3] != protocol.AgentCapabilityRelayHTTPPingV1 ||
		update.Capabilities[4] != protocol.AgentCapabilityTokenRoutingV1 {
		t.Fatalf("runtime capability update = %#v, want authenticated ID and supported contracts", update)
	}
	if got := server.borrowAgentAuthCache(); got != owned {
		t.Fatalf("Server current auth cache = %p, want %p", got, owned)
	}
	if snapshot := owned.Bootstrap(); snapshot.MasterInstanceID != "master-a" || len(snapshot.SigningKeys) != 1 {
		t.Fatalf("owned bootstrap snapshot = %#v", snapshot)
	}
	store.SetAgentCapabilities("current-session-peer", []string{"current-capability"})
	server.stopAgentAuthSession(owned)
	stopped = true
	if got := store.GetAgentCapabilities("current-session-peer"); got != nil {
		t.Fatalf("current session stop retained capabilities: %#v", got)
	}
}

func TestAgentAuthSessionCapabilityPublicationFailureStopsBeforeFullSync(t *testing.T) {
	notifyErr := errors.New("capability notification queue full")
	client := newOrderedAgentControlClient()
	client.notifyErr = notifyErr
	client.capabilityOnRegister, _ = marshalServerTest(protocol.AgentCapabilitiesUpdate{
		AgentID:      "snapshot-peer",
		Capabilities: []string{"snapshot-capability"},
	})
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	server := &Server{
		Cfg:    &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: ":0"}},
		Logger: zap.NewNop(),
		Creds:  &enrollment.Credentials{AgentID: "agent-a", Secret: "secret-a"},
		Store:  store,
		Syncer: agentcache.NewSyncer(store, client, nil, zap.NewNop(), time.Hour),
	}
	var owned *agentauthcache.Cache
	client.onNotify = func() {
		owned = server.borrowAgentAuthCache()
	}

	got, err := server.startAgentAuthSession(context.Background(), client)
	if !errors.Is(err, notifyErr) {
		t.Fatalf("start error = %v, want wrapped %v", err, notifyErr)
	}
	if got != nil {
		t.Fatalf("failed start returned cache %p", got)
	}
	if owned == nil {
		t.Fatal("capability publication ran without an installed auth cache")
	}
	if current := server.borrowAgentAuthCache(); current != nil {
		t.Fatalf("failed start retained auth cache %p", current)
	}
	select {
	case <-owned.Done():
	default:
		t.Fatal("failed start returned before Cache.Done closed")
	}
	if capabilities := store.GetAgentCapabilities("snapshot-peer"); capabilities != nil {
		t.Fatalf("failed start retained session capabilities: %#v", capabilities)
	}
	methods, _ := client.snapshot()
	for _, method := range methods {
		if method == consts.RPCSyncFullSync {
			t.Fatalf("failed capability publication continued to FullSync: %#v", methods)
		}
	}
}

func TestAgentAuthSessionReplacementRejectsPreviousCapabilityHandler(t *testing.T) {
	firstClient := newOrderedAgentControlClient()
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	server := &Server{
		Cfg:    &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: ":0"}},
		Logger: zap.NewNop(),
		Creds:  &enrollment.Credentials{AgentID: "agent-a", Secret: "secret-a"},
		Store:  store,
		Syncer: agentcache.NewSyncer(store, firstClient, nil, zap.NewNop(), time.Hour),
	}

	first, err := server.startAgentAuthSession(context.Background(), firstClient)
	if err != nil {
		t.Fatalf("start first agent auth session: %v", err)
	}
	previousHandler := firstClient.inlineHandler(consts.RPCSyncAgentCapabilities)
	if previousHandler == nil {
		t.Fatal("first control session did not register capability handler")
	}

	secondClient := newOrderedAgentControlClient()
	server.Syncer.SetClient(secondClient)
	second, err := server.startAgentAuthSession(context.Background(), secondClient)
	if err != nil {
		server.stopAgentAuthSession(first)
		t.Fatalf("start replacement agent auth session: %v", err)
	}
	t.Cleanup(func() { server.stopAgentAuthSession(second) })

	staleUpdate, _ := marshalServerTest(protocol.AgentCapabilitiesUpdate{
		AgentID:      "stale-peer",
		Capabilities: []string{"stale-capability"},
	})
	if _, err := previousHandler(context.Background(), staleUpdate); err != nil {
		t.Fatalf("invoke previous capability handler: %v", err)
	}
	if got := store.GetAgentCapabilities("stale-peer"); got != nil {
		t.Fatalf("previous control session repopulated replacement Store: %#v", got)
	}

	currentHandler := secondClient.inlineHandler(consts.RPCSyncAgentCapabilities)
	currentUpdate, _ := marshalServerTest(protocol.AgentCapabilitiesUpdate{
		AgentID:      "current-peer",
		Capabilities: []string{"current-capability"},
	})
	if _, err := currentHandler(context.Background(), currentUpdate); err != nil {
		t.Fatalf("invoke current capability handler: %v", err)
	}
	if got := store.GetAgentCapabilities("current-peer"); len(got) != 1 || got[0] != "current-capability" {
		t.Fatalf("current control session capability update = %#v", got)
	}
}

func TestAgentAuthCacheOwnerJoinsControlDisconnectAndServerCancellation(t *testing.T) {
	tests := []struct {
		name          string
		endSession    func(context.CancelFunc, *controlledControlSession)
		wantReconnect bool
	}{
		{
			name: "remote disconnect",
			endSession: func(_ context.CancelFunc, conn *controlledControlSession) {
				close(conn.done)
			},
			wantReconnect: true,
		},
		{
			name: "server cancellation waits for connection done",
			endSession: func(cancel context.CancelFunc, conn *controlledControlSession) {
				cancel()
				select {
				case <-conn.closeCalled:
				case <-time.After(time.Second):
					t.Fatal("server cancellation did not close control connection")
				}
				close(conn.done)
			},
			wantReconnect: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cache := agentauthcache.NewCache(newOrderedAgentControlClient(), agentauthcache.CacheOptions{})
			requireNoServerTestError(t, cache.Run(ctx))
			server := &Server{}
			if previous := server.replaceAgentAuthCache(cache); previous != nil {
				t.Fatalf("unexpected previous cache %p", previous)
			}
			conn := &controlledControlSession{
				done:        make(chan struct{}),
				closeCalled: make(chan struct{}),
			}
			result := make(chan bool, 1)
			go func() { result <- server.waitForAgentAuthSession(ctx, conn, cache) }()

			tc.endSession(cancel, conn)
			select {
			case reconnect := <-result:
				if reconnect != tc.wantReconnect {
					t.Fatalf("reconnect = %t, want %t", reconnect, tc.wantReconnect)
				}
			case <-time.After(time.Second):
				t.Fatal("agent auth session owner did not return")
			}
			if got := server.borrowAgentAuthCache(); got != nil {
				t.Fatalf("session returned before clearing auth cache slot: %p", got)
			}
			select {
			case <-cache.Done():
			default:
				t.Fatal("session returned before Cache.Done closed")
			}
		})
	}
}

func marshalServerTest(value any) (json.RawMessage, error) {
	data, err := json.Marshal(value)
	return json.RawMessage(data), err
}

func requireNoServerTestError(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunBackground_CancellationWhileControlConnected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	conn := &controlledControlSession{done: make(chan struct{}), closeCalled: make(chan struct{})}
	result := make(chan bool, 1)
	go func() { result <- waitForControlSession(ctx, conn) }()

	cancel()
	select {
	case <-conn.closeCalled:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not close the current control connection")
	}
	select {
	case <-result:
		t.Fatal("control wait returned before the connection write loop was done")
	default:
	}
	close(conn.done)
	select {
	case reconnect := <-result:
		if reconnect {
			t.Fatal("cancellation must stop reconnecting")
		}
	case <-time.After(time.Second):
		t.Fatal("control wait did not converge after connection shutdown")
	}
}

func TestNewEmbedded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:          ":8139",
			MasterURL:       "http://localhost:9999",
			CredentialsFile: filepath.Join(t.TempDir(), "agent_credentials.json"),
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	if srv.Creds.AgentID != "test-embedded" {
		t.Errorf("expected agent_id test-embedded, got %s", srv.Creds.AgentID)
	}
	if srv.Store == nil {
		t.Fatal("store is nil")
	}
	if srv.Bus == nil {
		t.Fatal("bus is nil")
	}
	if srv.Router != nil {
		t.Fatal("embedded agent should not have its own router")
	}
}

func TestNewEmbeddedReusesOwnerMetricsInsteadOfCreatingUnservedRegistry(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
	cfg := &config.AgentRuntimeConfig{Agent: config.AgentConfig{
		MasterURL: "http://localhost:9999", CredentialsFile: filepath.Join(t.TempDir(), "credentials.json"),
	}}

	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded", Secret: "secret"}, EmbeddedOptions{
		MetricsRegistry: registry, RelayMetrics: metrics,
	})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	require.Same(t, registry, server.MetricsRegistry)
	require.Same(t, metrics, server.RelayMetrics)

	unowned, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "unowned", Secret: "secret"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, unowned.Shutdown(context.Background())) })
	require.Nil(t, unowned.MetricsRegistry)
	require.Nil(t, unowned.RelayMetrics)
}

func TestMountRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:          ":8139",
			MasterURL:       "http://localhost:9999",
			CredentialsFile: filepath.Join(t.TempDir(), "agent_credentials.json"),
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	router := gin.New()
	srv.MountRoutes(router)

	routes := router.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes mounted")
	}

	found := false
	for _, r := range routes {
		if r.Path == "/v1/chat/completions" && r.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("/v1/chat/completions route not found")
	}
}

func TestStandaloneAndEmbeddedRegisterOnlyPostAttemptEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	constructors := []struct {
		name string
		new  func(*config.AgentRuntimeConfig) (*Server, *gin.Engine, error)
	}{
		{
			name: "standalone",
			new: func(cfg *config.AgentRuntimeConfig) (*Server, *gin.Engine, error) {
				if err := os.WriteFile(cfg.Agent.CredentialsFile, []byte(`{"agent_id":"standalone-attempt","secret":"secret"}`), 0o600); err != nil {
					return nil, nil, err
				}
				server, err := New(cfg, zap.NewNop())
				if err != nil {
					return nil, nil, err
				}
				return server, server.Router, nil
			},
		},
		{
			name: "embedded",
			new: func(cfg *config.AgentRuntimeConfig) (*Server, *gin.Engine, error) {
				server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded-attempt", Secret: "secret"})
				if err != nil {
					return nil, nil, err
				}
				router := gin.New()
				server.MountRoutes(router)
				return server, router, nil
			},
		},
	}
	for _, tt := range constructors {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.AgentRuntimeConfig{Agent: config.AgentConfig{
				CredentialsFile: filepath.Join(t.TempDir(), "credentials.json"),
			}}
			server, router, err := tt.new(cfg)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })

			var postCount, otherCount int
			for _, route := range router.Routes() {
				if route.Path != attemptwire.EndpointPath {
					continue
				}
				if route.Method == http.MethodPost {
					postCount++
				} else {
					otherCount++
				}
			}
			require.Equal(t, 1, postCount)
			require.Zero(t, otherCount)

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, attemptwire.EndpointPath, nil))
			require.Equal(t, http.StatusNotFound, response.Code)

			response = httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath+"/other", nil))
			require.Equal(t, http.StatusNotFound, response.Code)

			response = httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, nil)
			request.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"ordinary-client-token")
			router.ServeHTTP(response, request)
			require.Equal(t, http.StatusUnauthorized, response.Code)
		})
	}
}

func TestRunBackground_Cancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:          ":8139",
			MasterURL:       "http://localhost:9999",
			CredentialsFile: filepath.Join(t.TempDir(), "agent_credentials.json"),
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		srv.RunBackground(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("RunBackground did not stop after cancel")
	}
}

func TestStandaloneAndEmbeddedOwnRequestBodyStores(t *testing.T) {
	constructors := []struct {
		name string
		new  func(*config.AgentRuntimeConfig, *zap.Logger) (*Server, error)
	}{
		{
			name: "standalone",
			new: func(cfg *config.AgentRuntimeConfig, logger *zap.Logger) (*Server, error) {
				if err := os.WriteFile(
					cfg.Agent.CredentialsFile,
					[]byte(`{"agent_id":"standalone-test","secret":"secret"}`),
					0o600,
				); err != nil {
					return nil, err
				}
				return New(cfg, logger)
			},
		},
		{
			name: "embedded",
			new: func(cfg *config.AgentRuntimeConfig, logger *zap.Logger) (*Server, error) {
				return NewEmbedded(cfg, logger, &enrollment.Credentials{AgentID: "embedded-test", Secret: "secret"})
			},
		},
	}

	for _, tt := range constructors {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.AgentRuntimeConfig{
				Agent: config.AgentConfig{
					Listen:          ":0",
					MasterURL:       "http://localhost:9999",
					CredentialsFile: filepath.Join(dir, "agent_credentials.json"),
				},
			}
			srv, err := tt.new(cfg, zap.NewNop())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
			if srv.BodyStore == nil {
				t.Fatal("Server BodyStore is nil")
			}
			runtime := srv.buildRelayHandler(time.Second)
			if runtime.relayHandler.Agent.GetBodyStore() != srv.BodyStore {
				t.Fatal("relay handler did not receive the Server-owned BodyStore")
			}
			require.NotNil(t, runtime.attemptHandler)
			require.Equal(t, runtime.provider, runtime.attemptHandler.Provider)
			require.Equal(t, runtime.provider, runtime.relayHandler.ProviderAttemptExecutor())

			body, err := srv.BodyStore.Capture(context.Background(), strings.NewReader("spill"), appkg.BodyLimits{
				MemoryThreshold: 1,
				HardLimit:       16,
			})
			if err != nil {
				t.Fatal(err)
			}
			requestBodyDir := filepath.Join(filepath.Dir(cfg.Agent.CredentialsFile), "request-bodies")
			entries, err := os.ReadDir(requestBodyDir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("spill entries = %d, want 1", len(entries))
			}
			if err := body.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestShutdownClosesBodyStoreEvenWhenHTTPServerExists(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.AgentRuntimeConfig{Agent: config.AgentConfig{
		CredentialsFile: filepath.Join(dir, "agent_credentials.json"),
	}}
	srv, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded-test", Secret: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	srv.httpSrv = &http.Server{}
	body, err := srv.BodyStore.Capture(context.Background(), strings.NewReader("file-backed"), appkg.BodyLimits{
		MemoryThreshold: 1,
		HardLimit:       64,
	})
	if err != nil {
		t.Fatal(err)
	}
	reader, err := body.Open()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown with active reader = %v, want deadline exceeded", err)
	}
	if _, err := srv.BodyStore.Capture(context.Background(), strings.NewReader("late"), appkg.BodyLimits{}); !errors.Is(err, bodypkg.ErrStoreClosed) {
		t.Fatalf("Capture after Shutdown = %v, want ErrStoreClosed", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := srv.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-srv.BodyStore.Done():
	default:
		t.Fatal("BodyStore.Done remains open after shutdown retry")
	}
	entries, err := os.ReadDir(filepath.Join(dir, "request-bodies"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("request body directory contains %d residual files", len(entries))
	}
}

type fakeCloser struct{ closed int }

func (f *fakeCloser) Close() error { f.closed++; return nil }

func TestMaybeForceReconnect(t *testing.T) {
	s := &Server{Logger: zap.NewNop()}
	fc := &fakeCloser{}
	// 未达阈值:不关连接,计数原样
	if got := s.maybeForceReconnect(fc, 2, 3); got != 2 || fc.closed != 0 {
		t.Fatalf("below threshold: got=%d closed=%d", got, fc.closed)
	}
	// 达到阈值:关连接 + 计数归零
	if got := s.maybeForceReconnect(fc, 3, 3); got != 0 || fc.closed != 1 {
		t.Fatalf("at threshold: got=%d closed=%d", got, fc.closed)
	}
	// 禁用(0):永不关
	if got := s.maybeForceReconnect(fc, 99, 0); got != 99 || fc.closed != 1 {
		t.Fatalf("disabled: got=%d closed=%d", got, fc.closed)
	}
	// nil client 容错
	if got := s.maybeForceReconnect(nil, 5, 3); got != 5 {
		t.Fatalf("nil client must be noop, got=%d", got)
	}
}
