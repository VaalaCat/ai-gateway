package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	pkgauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

func TestRelayRecentErrorsForHeartbeatAreBoundedAndCopied(t *testing.T) {
	events := make([]diagnostics.Event, 0, 25)
	for index := range 25 {
		events = append(events, diagnostics.Event{Code: fmt.Sprintf("relay-%02d", index), Stage: "read", Message: "failure", At: time.Unix(int64(index), 0)})
	}

	recent := relayRecentErrorsForHeartbeat(events)
	require.Len(t, recent, 20)
	require.Equal(t, "relay-05", recent[0].Code)
	recent[0].Code = "mutated"
	require.Equal(t, "relay-05", relayRecentErrorsForHeartbeat(events)[0].Code)
}

type tunnelBootstrapControl struct {
	capabilities []string
}

func (c tunnelBootstrapControl) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	if method != consts.RPCAgentAuthBootstrap {
		return nil, errors.New("unexpected control method")
	}
	return json.Marshal(protocol.AuthBootstrapResponse{
		MasterInstanceID: "master-a",
		Capabilities:     c.capabilities,
		SigningKeys: []pkgauth.PublicKey{{
			KeyID: "key-a", Algorithm: "EdDSA", Key: make([]byte, 32),
		}},
	})
}

type countingTunnelTickets struct{ calls atomic.Int32 }

func (p *countingTunnelTickets) RelayTicket(context.Context, uint64) (pkgauth.RelayTicket, error) {
	p.calls.Add(1)
	return pkgauth.RelayTicket("ticket"), nil
}

type countingTunnelDialer struct{ calls atomic.Int32 }

func (d *countingTunnelDialer) Dial(context.Context, string, pkgauth.RelayTicket, uint64) (*agenttunnel.Session, error) {
	d.calls.Add(1)
	return nil, errors.New("dial stopped after boundary observation")
}

func newTunnelBootstrapCache(t *testing.T, capabilities []string) *agentauthcache.Cache {
	t.Helper()
	cache := agentauthcache.NewCache(tunnelBootstrapControl{capabilities: capabilities}, agentauthcache.CacheOptions{})
	require.NoError(t, cache.Run(context.Background()))
	t.Cleanup(func() {
		cache.Close()
		<-cache.Done()
	})
	return cache
}

func TestTunnelServerDerivesEffectiveDesiredURI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		agent      *models.Agent
		defaultURI string
		masterURL  string
		want       agenttunnel.Desired
		wantConfig string
	}{
		{name: "inherit global", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, defaultURI: "wss://relay.example/ws/agent-relay", masterURL: "https://ignored.example", want: agenttunnel.Desired{Mode: consts.RelayModeInherit, EffectiveURI: "wss://relay.example/ws/agent-relay"}, wantConfig: "configured"},
		{name: "inherit trims ASCII VT", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, defaultURI: "\vwss://relay.example/ws/agent-relay\v", masterURL: "https://ignored.example", want: agenttunnel.Desired{Mode: consts.RelayModeInherit, EffectiveURI: "wss://relay.example/ws/agent-relay"}, wantConfig: "configured"},
		{name: "inherit keeps Unicode whitespace invalid", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, defaultURI: "\u00a0wss://relay.example/ws/agent-relay\u00a0", masterURL: "https://ignored.example", want: agenttunnel.Desired{Mode: consts.RelayModeInherit, EffectiveURI: "\u00a0wss://relay.example/ws/agent-relay\u00a0"}, wantConfig: "configured"},
		{name: "inherit derives root master", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, masterURL: "https://master.example", want: agenttunnel.Desired{Mode: consts.RelayModeInherit, EffectiveURI: "wss://master.example/ws/agent-relay"}, wantConfig: "configured"},
		{name: "inherit derives prefixed control endpoint", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, masterURL: "wss://cdn.example/gateway/ws/agent?region=jp", want: agenttunnel.Desired{Mode: consts.RelayModeInherit, EffectiveURI: "wss://cdn.example/gateway/ws/agent-relay?region=jp"}, wantConfig: "configured"},
		{name: "inherit unix master", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, masterURL: "unix:/run/gateway.sock", want: agenttunnel.Desired{Mode: consts.RelayModeInherit}, wantConfig: "not_configured"},
		{name: "inherit invalid master", agent: &models.Agent{RelayMode: consts.RelayModeInherit}, masterURL: "://bad", want: agenttunnel.Desired{Mode: consts.RelayModeInherit}, wantConfig: "not_configured"},
		{name: "custom", agent: &models.Agent{RelayMode: consts.RelayModeCustom, RelayURI: "wss://relay.example/custom?region=us"}, defaultURI: "wss://ignored.example/ws", masterURL: "https://also-ignored.example", want: agenttunnel.Desired{Mode: consts.RelayModeCustom, ConfiguredURI: "wss://relay.example/custom?region=us", EffectiveURI: "wss://relay.example/custom?region=us"}, wantConfig: "configured"},
		{name: "disabled", agent: &models.Agent{RelayMode: consts.RelayModeDisabled, RelayURI: "wss://ignored.example/ws"}, defaultURI: "wss://ignored.example/ws", masterURL: "https://also-ignored.example", want: agenttunnel.Desired{Mode: consts.RelayModeDisabled, ConfiguredURI: "wss://ignored.example/ws"}, wantConfig: "disabled"},
		{name: "missing self record", agent: nil, defaultURI: "wss://relay.example/ws", masterURL: "https://master.example", want: agenttunnel.Desired{Mode: consts.RelayModeInherit}, wantConfig: "not_configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, config := deriveTunnelDesired(tt.agent, tt.defaultURI, tt.masterURL)
			require.Equal(t, tt.want, got)
			require.Equal(t, tt.wantConfig, config)
		})
	}
}

func TestTunnelServerClassifiesOldMasterUnsupported(t *testing.T) {
	t.Parallel()
	unsupported := agentauthcache.BootstrapSnapshot{MasterInstanceID: "master-old", SigningKeys: []pkgauth.PublicKey{{KeyID: "k"}}}
	require.False(t, tunnelBootstrapSupported(unsupported))
	supported := unsupported
	supported.Capabilities = []string{"future.capability", protocol.AgentCapabilityTunnelV1}
	require.True(t, tunnelBootstrapSupported(supported))
}

func TestTunnelServerOldMasterDoesNotDialUntilBootstrapGenerationChanges(t *testing.T) {
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "agent-a", RelayMode: consts.RelayModeInherit})
	store.LoadSettings([]models.Setting{{
		Key: consts.SettingAgentRelayDefaultURI, Value: "wss://master.example/ws/agent-relay",
	}})
	tickets := &countingTunnelTickets{}
	dialer := &countingTunnelDialer{}
	manager := agenttunnel.NewManager(agenttunnel.ManagerOptions{
		Dialer: dialer, Tickets: tickets, Limits: wire.Limits{
			MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 1 << 20,
			MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 1,
		}, BackoffMin: time.Hour, BackoffMax: time.Hour,
	})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	server := &Server{
		Creds: &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"}, Store: store,
		Logger: zap.NewNop(), Router: gin.New(), TunnelManager: manager,
	}
	unsupported := newTunnelBootstrapCache(t, nil)
	server.replaceAgentAuthCache(unsupported)

	server.reconcileTunnelDesired()
	unsupportedGeneration := manager.Snapshot().DesiredGeneration
	require.Equal(t, consts.RelayModeDisabled, manager.Snapshot().Desired.Mode)
	require.Zero(t, tickets.calls.Load())
	require.Zero(t, dialer.calls.Load())
	require.Zero(t, manager.Snapshot().RetryAt)
	for range 3 {
		server.reconcileTunnelDesired()
	}
	require.Equal(t, unsupportedGeneration, manager.Snapshot().DesiredGeneration)
	require.Zero(t, tickets.calls.Load())
	require.Zero(t, dialer.calls.Load())

	supported := newTunnelBootstrapCache(t, []string{protocol.AgentCapabilityTunnelV1})
	server.replaceAgentAuthCache(supported)
	server.reconcileTunnelDesired()
	require.Eventually(t, func() bool {
		return tickets.calls.Load() == 1 && dialer.calls.Load() == 1
	}, time.Second, time.Millisecond)
	require.Greater(t, manager.Snapshot().DesiredGeneration, unsupportedGeneration)
	require.Equal(t, consts.RelayModeInherit, manager.Snapshot().Desired.Mode)

	cancelRun()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestTunnelServerReportsRuntimeCapabilities(t *testing.T) {
	t.Parallel()
	require.ElementsMatch(t, []string{
		protocol.AgentCapabilityTunnelV1,
		protocol.AgentCapabilityForwardV1,
		protocol.AgentCapabilityDirectIngressV1,
		protocol.AgentCapabilityRelayHTTPPingV1,
		protocol.AgentCapabilityTokenRoutingV1,
	}, agentRuntimeCapabilities())
}

func TestTunnelServerOwnsManagerWithBorrowedRelayLink(t *testing.T) {
	t.Parallel()
	server := &Server{
		Creds:  &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"},
		Logger: zap.NewNop(),
		Router: gin.New(),
	}
	manager := server.newTunnelManager()
	require.NotNil(t, manager)
	t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })
	server.TunnelManager = manager
	var borrowed agentproxy.RelayLink = manager
	require.Same(t, manager, borrowed)
	require.Same(t, borrowed, server.GetRelayLink())
}

func TestTunnelServerHeartbeatProjectsManagerRelaySnapshot(t *testing.T) {
	t.Parallel()
	manager := agenttunnel.NewManager(agenttunnel.ManagerOptions{})
	t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })
	generation := manager.Apply(agenttunnel.Desired{
		Mode: consts.RelayModeCustom, ConfiguredURI: "wss://relay.example/custom?token=secret",
		EffectiveURI: "wss://relay.example/custom?token=secret",
	})
	server := &Server{TunnelManager: manager, tunnelState: tunnelRuntimeState{
		support: "supported", config: "configured",
		desired: agenttunnel.Desired{Mode: consts.RelayModeCustom, ConfiguredURI: "wss://relay.example/custom?token=secret", EffectiveURI: "wss://relay.example/custom?token=secret"},
	}}
	relay := server.tunnelHeartbeatRuntime()
	require.NotNil(t, relay)
	require.Equal(t, "supported", relay.Support)
	require.Equal(t, "configured", relay.Config)
	require.Equal(t, generation, relay.Desired.DesiredGeneration)
	require.Equal(t, consts.RelayModeCustom, relay.Desired.Mode)
	require.NotContains(t, relay.Desired.ConfiguredURI, "secret")
	require.NotContains(t, relay.Desired.EffectiveURI, "secret")
	require.Equal(t, "unavailable", relay.Availability)
}

func TestTunnelEmbeddedServerCreatesManagerAfterRouterMount(t *testing.T) {
	t.Parallel()
	cfg := &config.AgentRuntimeConfig{
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "credentials.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30}, Relay: config.RelayConfig{Timeout: 30},
	}
	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded-a", Secret: "secret"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	require.Nil(t, server.GetRelayLink())
	server.MountRoutes(gin.New())
	require.NotNil(t, server.TunnelManager)
	require.NotNil(t, server.GetRelayLink())
}

func TestTunnelServerReconcilesSelfRecordAndRuntimeSettingsChanges(t *testing.T) {
	t.Parallel()
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	server := &Server{
		Creds: &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"},
		Store: store, Logger: zap.NewNop(), Router: gin.New(),
		Cfg: &config.AgentRuntimeConfig{Agent: config.AgentConfig{MasterURL: "https://master.example/gateway"}},
	}
	server.TunnelManager = server.newTunnelManager()
	t.Cleanup(func() { require.NoError(t, server.TunnelManager.Close(context.Background())) })

	server.reconcileTunnelDesired()
	missingGeneration := server.TunnelManager.Snapshot().DesiredGeneration
	require.Equal(t, "", server.tunnelState.desired.EffectiveURI)

	store.SetAgent(&models.Agent{AgentID: "agent-a", RelayMode: consts.RelayModeInherit})
	server.reconcileTunnelDesired()
	selfGeneration := server.TunnelManager.Snapshot().DesiredGeneration
	require.Greater(t, selfGeneration, missingGeneration)
	require.Equal(t, "wss://master.example/gateway/ws/agent-relay", server.tunnelState.desired.EffectiveURI)

	store.LoadSettings([]models.Setting{{Key: consts.SettingAgentRelayDefaultURI, Value: "wss://master.example/ws/agent-relay"}})
	server.reconcileTunnelDesired()
	defaultGeneration := server.TunnelManager.Snapshot().DesiredGeneration
	require.Greater(t, defaultGeneration, selfGeneration)

	store.LoadSettings([]models.Setting{{Key: "agent.tunnel_max_streams", Value: "128"}})
	server.reconcileTunnelDesired()
	require.Greater(t, server.TunnelManager.Snapshot().DesiredGeneration, defaultGeneration)
}

func TestTunnelServerReconcileWithoutRuntimeConfigLeavesInheritNotConfigured(t *testing.T) {
	t.Parallel()
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "agent-a", RelayMode: consts.RelayModeInherit})
	server := &Server{
		Creds: &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"},
		Store: store, Logger: zap.NewNop(), Router: gin.New(),
	}
	server.TunnelManager = server.newTunnelManager()
	t.Cleanup(func() { require.NoError(t, server.TunnelManager.Close(context.Background())) })

	require.NotPanics(t, server.reconcileTunnelDesired)
	require.Equal(t, "not_configured", server.tunnelState.config)
	require.Empty(t, server.tunnelState.desired.EffectiveURI)
}

func TestTunnelStandaloneAndEmbeddedRoutersHandleBoundAttemptsEquivalently(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, mode := range []string{"standalone", "embedded"} {
		t.Run(mode, func(t *testing.T) {
			store := agentcache.NewStore(nil, config.AgentCacheConfig{})
			store.LoadSettings([]models.Setting{{Key: "agent.relay_fallback_enabled", Value: "1"}})
			calls := atomic.Int32{}
			router := gin.New()
			router.POST(attemptwire.EndpointPath, func(c *gin.Context) {
				calls.Add(1)
				c.JSON(http.StatusOK, gin.H{"pipeline": "committed"})
			})
			server := &Server{
				Creds: &enrollment.Credentials{AgentID: "target-a", Secret: "secret"}, Store: store, Logger: zap.NewNop(),
			}
			var targetHandler *agenttunnel.TargetHandler
			if mode == "standalone" {
				server.Router = router
				targetHandler = server.NewTunnelTargetHandler(nil)
			} else {
				targetHandler = server.NewTunnelTargetHandler(router)
			}

			conn, peer := tunnelServerWebSocketPair(t)
			limits := wire.Limits{
				MaxMetadataBytes: 64 * 1024, MaxDataBytes: 64 * 1024, InitialStreamWindow: 64 * 1024,
				MaxQueuedSessionBytes: 1024 * 1024, MaxConcurrentStreams: 1,
			}
			session := agenttunnel.NewSession(conn, 1, limits, agenttunnel.SessionOptions{TargetHandler: targetHandler})
			runDone := make(chan error, 1)
			go func() { runDone <- session.Run(t.Context()) }()
			t.Cleanup(func() {
				session.Cancel(context.Canceled)
				<-runDone
			})

			id := wire.StreamID{92}
			meta := attemptwire.AttemptProxyMeta{
				Attempt: attemptwire.BoundAttempt{
					Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
					RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
				},
				RequestPath: "/v1/chat/completions",
			}
			writeTunnelServerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1,
				Payload: encodeTunnelServerMetadata(t, limits, wire.Open{
					Method: http.MethodPost, Path: attemptwire.EndpointPath, BodyLength: 0,
					SourceAgentID: "source-a", TargetAgentID: "target-a", Hop: 1,
					ResponseWindow: limits.InitialStreamWindow, Attempt: &meta,
				}),
			})
			require.Equal(t, wire.FrameReady, readTunnelServerFrame(t, peer, limits).Type)
			require.Zero(t, calls.Load())
			writeTunnelServerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id, Sequence: 2,
			})
			require.Equal(t, wire.FrameCommitted, readTunnelServerFrame(t, peer, limits).Type)
			writeTunnelServerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id, Sequence: 3,
			})

			status := 0
			var response strings.Builder
			for {
				frame := readTunnelServerFrame(t, peer, limits)
				switch frame.Type {
				case wire.FrameHeaders:
					var headers wire.Headers
					require.NoError(t, wire.DecodeMetadata(frame.Payload, &headers, limits.MaxMetadataBytes))
					status = headers.StatusCode
				case wire.FrameResponseData:
					response.Write(frame.Payload)
				case wire.FrameEnd:
					require.Equal(t, http.StatusOK, status)
					require.JSONEq(t, `{"pipeline":"committed"}`, response.String())
					require.EqualValues(t, 1, calls.Load())
					return
				default:
					t.Fatalf("unexpected frame type %d", frame.Type)
				}
			}
		})
	}
}

func TestTunnelUnboundBusinessOpenNeverReachesAgentRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-tunnel", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		}))
	}))
	defer provider.Close()

	cfg := &config.AgentRuntimeConfig{
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "credentials.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30}, Relay: config.RelayConfig{Timeout: 30},
	}
	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "target-a", Secret: "secret"})
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	server.Store.SetToken(&models.Token{ID: 1, UserID: 0, Key: "tunnel-token", Name: "tunnel", Status: 1, ExpiredAt: -1})
	server.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: provider.URL, Status: 1, Weight: 1},
		Key:         "provider-key", Models: "gpt-4o",
	})
	server.Store.RebuildModelIndex()
	server.Store.LoadSettings([]models.Setting{
		{Key: "agent.relay_fallback_enabled", Value: "1"},
		{Key: "rate_limiter_enabled", Value: "1"},
	})
	server.Store.LimiterIndex.LoadLimiters([]models.RequestLimiter{{
		ID: 11, Name: "tunnel-request", Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
	}})
	server.Store.LimiterIndex.LoadBindings([]models.LimiterBinding{{
		ID: 12, LimiterID: 11, TargetType: models.LimiterTargetGlobal, Enabled: true,
	}})

	usage := make(chan protocol.UsageLogEntry, 2)
	_, err = events.SubscribeUsageCompleted(server.Bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		usage <- entry
		return nil
	})
	require.NoError(t, err)
	router := gin.New()
	server.MountRoutes(router)
	targetHandler := server.NewTunnelTargetHandler(router)

	conn, peer := tunnelServerWebSocketPair(t)
	limits := wire.Limits{
		MaxMetadataBytes: 64 * 1024, MaxDataBytes: 64 * 1024, InitialStreamWindow: 256 * 1024,
		MaxQueuedSessionBytes: 1024 * 1024, MaxConcurrentStreams: 4,
	}
	session := agenttunnel.NewSession(conn, 1, limits, agenttunnel.SessionOptions{TargetHandler: targetHandler})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	t.Cleanup(func() {
		session.Cancel(context.Canceled)
		select {
		case <-runDone:
		case <-time.After(time.Second):
			t.Error("tunnel pipeline session did not stop")
		}
	})

	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	id := wire.StreamID{91}
	tokenHitsBefore := server.Store.CacheSnapshot()["token"].Hits
	writeTunnelServerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1,
		Payload: encodeTunnelServerMetadata(t, limits, wire.Open{
			Method: http.MethodPost, Path: "/v1/chat/completions", BodyLength: int64(len(body)),
			SourceAgentID: "source-a", TargetAgentID: "target-a", ResponseWindow: limits.InitialStreamWindow,
			Header: map[string][]string{"Authorization": {"Bearer tunnel-token"}, "Content-Type": {"application/json"},
				consts.HeaderXAgentID: {"forged-target"}},
		}),
	})
	require.Equal(t, wire.FrameReset, readTunnelServerFrame(t, peer, limits).Type)
	require.Zero(t, providerCalls.Load(), "rejected OPEN must not dispatch the request body to a provider")
	require.EqualValues(t, 0, server.Store.CacheSnapshot()["token"].Hits-tokenHitsBefore, "TokenAuth count")
	select {
	case <-usage:
		t.Fatal("rejected OPEN reached Planner or published usage")
	default:
	}
	require.Empty(t, server.LimiterStore.SnapshotBuckets(), "Limiter bucket count")
}

func TestMountRoutesAttemptProxyAuthenticatesOriginalAuthorizationAndExecutesOnlyBoundAttempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		providerCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-attempt", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "bound-ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		}))
	}))
	defer provider.Close()

	server, err := NewEmbedded(
		&config.AgentRuntimeConfig{
			Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "credentials.json")},
			Runtime: config.RuntimeConfig{RelayTimeout: 30}, Relay: config.RelayConfig{Timeout: 30},
		},
		zap.NewNop(),
		&enrollment.Credentials{AgentID: "target-a", Secret: "secret"},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	server.Store.SetToken(&models.Token{ID: 1, UserID: 0, Key: "attempt-token", Name: "attempt", Status: 1, ExpiredAt: -1})
	server.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: provider.URL, Status: 1, Weight: 1},
		Key:         "provider-key", Models: "gpt-4o",
	})
	server.Store.RebuildModelIndex()

	usage := make(chan protocol.UsageLogEntry, 1)
	_, err = events.SubscribeUsageCompleted(server.Bus, func(_ context.Context, entry protocol.UsageLogEntry) error {
		usage <- entry
		return nil
	})
	require.NoError(t, err)
	router := gin.New()
	server.MountRoutes(router)
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/chat/completions",
	}
	identity := agentproxy.IngressMeta{
		Kind: agentproxy.IngressKindTunnel, SourceAgentID: "source-a", RouteID: 0,
		StreamID: wire.StreamID{92}, Hop: 1, Attempt: &meta,
	}
	body := `{"model":"public-model","messages":[{"role":"user","content":"hi"}]}`
	request := httptest.NewRequest(http.MethodPost, attemptwire.EndpointPath, strings.NewReader(body))
	request.Header.Set(consts.HeaderAuthorization, "Bearer attempt-token")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(attemptwire.HeaderMeta, `{"attempt":"forged"}`)
	request.Header.Set(consts.HeaderXAgentForwardTicket, "forged-ticket")
	request = request.WithContext(agentproxy.WithIngressMeta(request.Context(), identity))
	tokenHitsBefore := server.Store.CacheSnapshot()["token"].Hits
	response := httptest.NewRecorder()

	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), `"content":"bound-ok"`)
	require.EqualValues(t, 1, providerCalls.Load())
	require.EqualValues(t, 1, server.Store.CacheSnapshot()["token"].Hits-tokenHitsBefore)
	require.Empty(t, request.Header.Get(attemptwire.HeaderMeta))
	require.Empty(t, request.Header.Get(consts.HeaderXAgentForwardTicket))
	select {
	case entry := <-usage:
		t.Fatalf("target attempt proxy published entry-agent usage: %#v", entry)
	default:
	}
}

func tunnelServerWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 1)
	webServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(webServer.URL, "http"), nil)
	require.NoError(t, err)
	peer := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = peer.Close(); webServer.Close() })
	return client, peer
}

func readTunnelServerFrame(t *testing.T, peer *websocket.Conn, limits wire.Limits) wire.Frame {
	t.Helper()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, message, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(message, limits)
	require.NoError(t, err)
	return frame
}

func writeTunnelServerFrame(t *testing.T, peer *websocket.Conn, limits wire.Limits, frame wire.Frame) {
	t.Helper()
	message, err := wire.Encode(frame, limits)
	require.NoError(t, err)
	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, message))
}

func encodeTunnelServerMetadata[T any](t *testing.T, limits wire.Limits, value T) []byte {
	t.Helper()
	payload, err := wire.EncodeMetadata(value, limits.MaxMetadataBytes)
	require.NoError(t, err)
	return payload
}
