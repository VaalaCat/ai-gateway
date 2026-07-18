package master

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	mastertunnel "github.com/VaalaCat/ai-gateway/internal/master/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type rolloutTicketProvider struct{}

func (rolloutTicketProvider) RelayTicket(context.Context, uint64) (pkgauth.RelayTicket, error) {
	return pkgauth.RelayTicket("rollout-ticket"), nil
}

type rolloutDialer struct {
	calls   chan string
	session *agenttunnel.Session
	used    atomic.Bool
}

func newRolloutDialer(session *agenttunnel.Session) *rolloutDialer {
	return &rolloutDialer{calls: make(chan string, 4), session: session}
}

func (d *rolloutDialer) Dial(ctx context.Context, rawURI string, _ pkgauth.RelayTicket, _ uint64) (*agenttunnel.Session, error) {
	d.calls <- rawURI
	if d.session != nil && d.used.CompareAndSwap(false, true) {
		return d.session, nil
	}
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-time.After(time.Millisecond):
		return nil, errors.New("rollout dial unavailable")
	}
}

type rolloutControlSource struct {
	facts map[string]connectivity.ControlSessionFact
}

func (s rolloutControlSource) GetControlSession(agentID string) (connectivity.ControlSessionFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

func runRolloutManager(t *testing.T, dialer agenttunnel.Dialer, desired agenttunnel.Desired) *agenttunnel.Manager {
	t.Helper()
	manager := agenttunnel.NewManager(agenttunnel.ManagerOptions{
		SourceID: "rollout-agent", Dialer: dialer, Tickets: rolloutTicketProvider{},
		Limits:     wire.Limits{MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10, MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4},
		BackoffMin: time.Hour, BackoffMax: time.Hour,
	})
	ctx, cancel := context.WithCancel(context.WithoutCancel(t.Context()))
	done := make(chan error, 1)
	go func() { done <- manager.Run(ctx) }()
	manager.Apply(desired)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("rollout tunnel manager did not stop")
		}
	})
	return manager
}

func requireRolloutDial(t *testing.T, dialer *rolloutDialer, uri string) {
	t.Helper()
	select {
	case got := <-dialer.calls:
		require.Equal(t, uri, got)
	case <-time.After(time.Second):
		t.Fatal("rollout tunnel manager did not dial")
	}
}

func requireNoRolloutDial(t *testing.T, dialer *rolloutDialer) {
	t.Helper()
	select {
	case uri := <-dialer.calls:
		t.Fatalf("unexpected rollout dial to %q", uri)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestAgentRelayRollout(t *testing.T) {
	t.Run("fresh install has false switch and empty default URI", func(t *testing.T) {
		defaults := settings.Defaults()
		require.Equal(t, "", defaults[consts.SettingAgentRelayDefaultURI])
		require.Equal(t, "0", defaults[consts.SettingAgentRelayFallbackEnabled])
	})

	t.Run("custom canary remains isolated while inherit is not configured", func(t *testing.T) {
		customDialer := newRolloutDialer(nil)
		inheritDialer := newRolloutDialer(nil)
		runRolloutManager(t, customDialer, agenttunnel.Desired{
			Mode: consts.RelayModeCustom, ConfiguredURI: "wss://relay.example/custom", EffectiveURI: "wss://relay.example/custom",
		})
		runRolloutManager(t, inheritDialer, agenttunnel.Desired{Mode: consts.RelayModeInherit})

		requireRolloutDial(t, customDialer, "wss://relay.example/custom")
		requireNoRolloutDial(t, inheritDialer)
	})

	t.Run("global enabled plus empty default admits custom only", func(t *testing.T) {
		var gate mastertunnel.AdmissionGate
		gate.Set(true)
		require.True(t, gate.AllowNew())

		client, peer, server := agentRelayWebSocketPair(t)
		t.Cleanup(server.Close)
		t.Cleanup(func() { _ = peer.Close() })
		session := agenttunnel.NewSession(client, 1, wire.Limits{
			MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
			MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
		}, agenttunnel.SessionOptions{})
		customDialer := newRolloutDialer(session)
		custom := runRolloutManager(t, customDialer, agenttunnel.Desired{
			Mode: consts.RelayModeCustom, ConfiguredURI: "ws://relay.example/custom", EffectiveURI: "ws://relay.example/custom",
		})
		inheritDialer := newRolloutDialer(nil)
		runRolloutManager(t, inheritDialer, agenttunnel.Desired{Mode: consts.RelayModeInherit})

		requireRolloutDial(t, customDialer, "ws://relay.example/custom")
		require.Eventually(t, func() bool { return custom.Snapshot().AcceptingNewStreams }, time.Second, time.Millisecond)
		requireNoRolloutDial(t, inheritDialer)
	})

	t.Run("setting default URI converges inherit Agents", func(t *testing.T) {
		client, peer, server := agentRelayWebSocketPair(t)
		t.Cleanup(server.Close)
		t.Cleanup(func() { _ = peer.Close() })
		limits := wire.Limits{
			MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
			MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
		}
		dialer := newRolloutDialer(agenttunnel.NewSession(client, 2, limits, agenttunnel.SessionOptions{}))
		manager := runRolloutManager(t, dialer, agenttunnel.Desired{
			Mode: consts.RelayModeInherit, EffectiveURI: "ws://relay.example/default",
		})

		requireRolloutDial(t, dialer, "ws://relay.example/default")
		require.Eventually(t, func() bool {
			snapshot := manager.Snapshot()
			return snapshot.Desired.Mode == consts.RelayModeInherit &&
				snapshot.Desired.EffectiveURI == "ws://relay.example/default" &&
				snapshot.AcceptingNewStreams && snapshot.Convergence == "converged"
		}, time.Second, time.Millisecond)
	})

	t.Run("old Agent is unsupported rather than disconnected", func(t *testing.T) {
		service := connectivity.NewService("rollout-master", connectivity.Sources{
			Control: rolloutControlSource{facts: map[string]connectivity.ControlSessionFact{
				"old-agent": {Generation: 7, ConnectedAt: 100, HeartbeatAt: 120},
			}},
		}, connectivity.Options{Now: func() time.Time { return time.Unix(125, 0) }})

		snapshot := service.Build(models.Agent{AgentID: "old-agent", Status: consts.StatusEnabled})
		require.Equal(t, "connected", snapshot.Control.State)
		require.Equal(t, "unsupported", snapshot.Relay.Support)
		require.Equal(t, "unavailable", snapshot.Relay.Availability)
	})

	t.Run("mixed target capability blocks Relay until target advertises tunnel v1", func(t *testing.T) {
		blocked := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
		blocked.lookup.SetCapabilities("target", nil)
		status, body, err := blocked.request(t.Context(), false)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		blocked.requireProviderAndUsage(1, 0, 1, "source", "local")

		ready := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
		ready.lookup.SetCapabilities("target", []string{protocol.AgentCapabilityTunnelV1})
		status, body, err = ready.request(t.Context(), false)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		ready.requireProviderAndUsage(0, 1, 1, "source", "relay")
	})

	t.Run("wrong Master WELCOME proof leaves old active ready", func(t *testing.T) {
		fixture := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
		before := fixture.source.TunnelManager.Snapshot()
		require.True(t, before.AcceptingNewStreams)

		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		handlerDone := make(chan error, 1)
		wrongMaster := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			handlerErr := func() error {
				conn, err := upgrader.Upgrade(w, r, nil)
				if err != nil {
					return fmt.Errorf("upgrade wrong Master websocket: %w", err)
				}
				defer conn.Close()
				_, payload, err := conn.ReadMessage()
				if err != nil {
					return fmt.Errorf("read wrong Master HELLO: %w", err)
				}
				var hello wire.Hello
				if err := json.Unmarshal(payload, &hello); err != nil {
					return fmt.Errorf("decode wrong Master HELLO: %w", err)
				}
				proof, err := fixture.signer.SignWelcome(pkgauth.WelcomeProofClaims{
					AgentID: "source", Nonce: hello.Nonce, MasterInstanceID: "master-route-fixture",
					SessionGeneration: 99, DesiredGeneration: hello.DesiredGeneration,
				})
				if err != nil {
					return fmt.Errorf("sign wrong Master WELCOME: %w", err)
				}
				if err := conn.WriteJSON(wire.Welcome{
					NonceProof: string(proof), MasterInstanceID: "wrong-master", SessionGeneration: 99, Limits: fixture.limits,
				}); err != nil {
					return fmt.Errorf("write wrong Master WELCOME: %w", err)
				}
				return nil
			}()
			select {
			case handlerDone <- handlerErr:
			default:
			}
		}))
		t.Cleanup(wrongMaster.Close)
		badURI := "ws" + strings.TrimPrefix(wrongMaster.URL, "http")
		fixture.source.TunnelManager.Apply(agenttunnel.Desired{Mode: consts.RelayModeCustom, ConfiguredURI: badURI, EffectiveURI: badURI})

		require.Eventually(t, func() bool { return fixture.source.TunnelManager.Snapshot().LastError != "" }, time.Second, time.Millisecond)
		select {
		case err := <-handlerDone:
			require.NoError(t, err)
		case <-time.After(time.Second):
			t.Fatal("wrong Master handler did not finish")
		}
		after := fixture.source.TunnelManager.Snapshot()
		require.Equal(t, before.SessionGeneration, after.SessionGeneration)
		require.Equal(t, before.ActiveGeneration, after.ActiveGeneration)
		require.True(t, after.AcceptingNewStreams)
	})

	t.Run("kill switch rejects OPEN while Source Control is disconnected", func(t *testing.T) {
		fixture := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
		require.Equal(t, 1, fixture.source.Store.Settings().RelayFallbackEnabled)
		fixture.admission.Set(false)

		status, body, err := fixture.request(t.Context(), false)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		fixture.requireProviderAndUsage(1, 0, 1, "source", "local")
	})

	t.Run("kill switch drains existing committed Stream", func(t *testing.T) {
		barrier := newRelayTargetBarrier()
		fixture := newRoutedRelayFixture(t, relayProviderSuccess(nil), barrier, true)
		type requestResult struct {
			status int
			body   string
			err    error
		}
		requestDone := make(chan requestResult, 1)
		go func() {
			status, body, err := fixture.request(t.Context(), false)
			requestDone <- requestResult{status: status, body: body, err: err}
		}()
		barrier.wait(t)
		fixture.admission.Set(false)
		drainDone := make(chan error, 1)
		go func() { drainDone <- fixture.hub.DrainAll(t.Context()) }()
		select {
		case err := <-drainDone:
			t.Fatalf("drain returned before the committed Stream completed: %v", err)
		case <-time.After(50 * time.Millisecond):
		}

		barrier.release()
		result := <-requestDone
		require.NoError(t, result.err)
		require.Equal(t, http.StatusOK, result.status, result.body)
		require.NoError(t, <-drainDone)
		fixture.requireProviderAndUsage(0, 1, 1, "source", "relay")
	})

	t.Run("rollback leaves additive Agent columns and signing key intact", func(t *testing.T) {
		db := newAgentRouteUsageDB(t)
		agent := models.Agent{
			AgentID: "rollback-agent", Status: consts.StatusEnabled,
			RelayMode: consts.RelayModeCustom, RelayURI: "wss://relay.example/custom",
		}
		require.NoError(t, db.Create(&agent).Error)
		publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
		require.NoError(t, err)
		active := uint8(1)
		key := models.MasterSigningKey{KeyID: "rollout-key", PublicKey: publicKey, PrivateKey: privateKey, ActiveSlot: &active}
		require.NoError(t, db.Create(&key).Error)
		require.NoError(t, db.Create(&models.Setting{Key: consts.SettingAgentRelayDefaultURI, Value: "wss://relay.example/default"}).Error)
		require.NoError(t, db.Create(&models.Setting{Key: consts.SettingAgentRelayFallbackEnabled, Value: "1"}).Error)

		require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&models.Setting{}).Where("key = ?", consts.SettingAgentRelayDefaultURI).Update("value", "").Error; err != nil {
				return err
			}
			return tx.Model(&models.Setting{}).Where("key = ?", consts.SettingAgentRelayFallbackEnabled).Update("value", "0").Error
		}))

		var storedAgent models.Agent
		require.NoError(t, db.Where("agent_id = ?", agent.AgentID).First(&storedAgent).Error)
		require.Equal(t, consts.RelayModeCustom, storedAgent.RelayMode)
		require.Equal(t, "wss://relay.example/custom", storedAgent.RelayURI)
		var storedKey models.MasterSigningKey
		require.NoError(t, db.Where("key_id = ?", key.KeyID).First(&storedKey).Error)
		require.Equal(t, publicKey, ed25519.PublicKey(storedKey.PublicKey))
		require.Equal(t, privateKey, ed25519.PrivateKey(storedKey.PrivateKey))
	})
}
