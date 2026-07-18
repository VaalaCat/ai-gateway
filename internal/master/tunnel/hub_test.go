package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masteragentauth "github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	pkgauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type tunnelMetricRecorder struct {
	mu            sync.Mutex
	sessions      map[[2]string]float64
	sessionWrites []tunnelSessionMetricWrite
	streamValues  []float64
	bytes         map[pkgmetrics.Direction]float64
	resets        []struct {
		stage     pkgmetrics.Stage
		committed bool
	}
}

type tunnelSessionMetricWrite struct {
	availability string
	convergence  string
	value        float64
}

func newTunnelMetricRecorder() *tunnelMetricRecorder {
	return &tunnelMetricRecorder{sessions: make(map[[2]string]float64), bytes: make(map[pkgmetrics.Direction]float64)}
}

func (m *tunnelMetricRecorder) SetTunnelSession(availability pkgmetrics.SessionAvailability, convergence pkgmetrics.SessionConvergence, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[[2]string{string(availability), string(convergence)}] = value
	m.sessionWrites = append(m.sessionWrites, tunnelSessionMetricWrite{
		availability: string(availability), convergence: string(convergence), value: value,
	})
}

func (m *tunnelMetricRecorder) allSessionWritesNonNegative() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, write := range m.sessionWrites {
		if write.value < 0 {
			return false
		}
	}
	return true
}

func (m *tunnelMetricRecorder) hasSessionWrite(availability, convergence string, value float64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, write := range m.sessionWrites {
		if write.availability == availability && write.convergence == convergence && write.value == value {
			return true
		}
	}
	return false
}

func (m *tunnelMetricRecorder) SetTunnelStreams(value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamValues = append(m.streamValues, value)
}
func (m *tunnelMetricRecorder) AddTunnelBytes(direction pkgmetrics.Direction, value float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.bytes[direction] += value
}
func (m *tunnelMetricRecorder) IncTunnelReset(stage pkgmetrics.Stage, committed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.resets = append(m.resets, struct {
		stage     pkgmetrics.Stage
		committed bool
	}{stage: stage, committed: committed})
}

func (m *tunnelMetricRecorder) session(availability, convergence string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[[2]string{availability, convergence}]
}

func (m *tunnelMetricRecorder) streams() []float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]float64(nil), m.streamValues...)
}

func (m *tunnelMetricRecorder) byteCount(direction pkgmetrics.Direction) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bytes[direction]
}

func (m *tunnelMetricRecorder) resetEvents() []struct {
	stage     pkgmetrics.Stage
	committed bool
} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]struct {
		stage     pkgmetrics.Stage
		committed bool
	}(nil), m.resets...)
}

func TestHubMetricsAggregateActiveCandidateAndDrainingSessions(t *testing.T) {
	metrics := newTunnelMetricRecorder()
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
	t.Cleanup(func() { require.NoError(t, hub.Close(context.Background())) })
	first := newTestSession(hub, "agent-a", 1)
	second := newTestSession(hub, "agent-a", 2)

	require.NoError(t, hub.register(first))
	require.Equal(t, float64(1), metrics.session("available", "converged"))
	require.NoError(t, hub.register(second))
	require.Equal(t, float64(1), metrics.session("unavailable", "converging"))
	require.NoError(t, hub.promote(second))
	require.Equal(t, float64(1), metrics.session("available", "converged"))
	require.Equal(t, float64(1), metrics.session("draining", "converging"))
	require.Equal(t, float64(0), metrics.session("unavailable", "converging"))

	hub.unregister(first)
	require.Equal(t, float64(0), metrics.session("draining", "converging"))
}

func TestHubMetricsTrackExplicitDrainIdempotently(t *testing.T) {
	metrics := newTunnelMetricRecorder()
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics, DrainTimeout: time.Hour})
	session := newTestSession(hub, "agent-a", 1)
	require.NoError(t, hub.register(session))
	require.Equal(t, float64(1), metrics.session("available", "converged"))

	require.NoError(t, hub.Drain("agent-a", 1))
	require.Equal(t, "draining", session.snapshot().Availability)
	require.Equal(t, float64(0), metrics.session("available", "converged"))
	require.Equal(t, float64(1), metrics.session("draining", "converging"))
	require.NoError(t, hub.Drain("agent-a", 1))
	require.Equal(t, float64(0), metrics.session("available", "converged"))
	require.Equal(t, float64(1), metrics.session("draining", "converging"))

	hub.unregister(session)
	require.Equal(t, float64(0), metrics.session("draining", "converging"))
	require.True(t, metrics.allSessionWritesNonNegative())
	session.Cancel(errors.New("cleanup"))
	require.NoError(t, hub.Close(context.Background()))
}

func TestHubMetricsTrackDrainAllAndFinalUnregister(t *testing.T) {
	metrics := newTunnelMetricRecorder()
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
	activeA := newTestSession(hub, "agent-a", 1)
	candidateA := newTestSession(hub, "agent-a", 2)
	activeB := newTestSession(hub, "agent-b", 3)
	require.NoError(t, hub.register(activeA))
	require.NoError(t, hub.register(candidateA))
	require.NoError(t, hub.register(activeB))
	require.Equal(t, float64(2), metrics.session("available", "converged"))
	require.Equal(t, float64(1), metrics.session("unavailable", "converging"))

	require.NoError(t, hub.DrainAll(t.Context()))
	for _, session := range []*Session{activeA, candidateA, activeB} {
		require.Equal(t, "draining", session.snapshot().Availability)
	}
	require.True(t, metrics.hasSessionWrite("draining", "converging", 3))
	<-candidateA.Done()
	require.Equal(t, float64(0), metrics.session("available", "converged"))
	require.Equal(t, float64(0), metrics.session("unavailable", "converging"))
	require.Equal(t, float64(2), metrics.session("draining", "converging"))

	for _, session := range []*Session{candidateA, activeA, activeB} {
		hub.unregister(session)
	}
	require.Equal(t, float64(0), metrics.session("draining", "converging"))
	require.True(t, metrics.allSessionWritesNonNegative())
	for _, session := range []*Session{activeA, candidateA, activeB} {
		session.Cancel(errors.New("cleanup"))
	}
	require.NoError(t, hub.Close(context.Background()))
}

func TestMergeRecentErrorsSortsStablyAndKeepsLatestTwenty(t *testing.T) {
	t.Run("interleaved groups sort chronologically", func(t *testing.T) {
		first := []connectivity.RecentError{{Code: "three", OccurredAt: 3}, {Code: "five", OccurredAt: 5}}
		second := []connectivity.RecentError{{Code: "one", OccurredAt: 1}, {Code: "four", OccurredAt: 4}}
		got := mergeRecentErrors(first, second)
		require.Equal(t, []string{"one", "three", "four", "five"}, recentErrorCodes(got))
	})

	t.Run("evicts oldest after global ordering", func(t *testing.T) {
		odd := make([]connectivity.RecentError, 0, 13)
		even := make([]connectivity.RecentError, 0, 12)
		for index := int64(1); index <= 25; index++ {
			event := connectivity.RecentError{Code: fmt.Sprintf("event-%02d", index), OccurredAt: index}
			if index%2 == 0 {
				even = append(even, event)
			} else {
				odd = append(odd, event)
			}
		}
		got := mergeRecentErrors(odd, even)
		require.Len(t, got, diagnostics.DefaultRingCapacity)
		require.Equal(t, "event-06", got[0].Code)
		require.Equal(t, "event-25", got[len(got)-1].Code)
	})

	t.Run("ties preserve input order and output is isolated", func(t *testing.T) {
		first := []connectivity.RecentError{{Code: "first", Message: "first-message", OccurredAt: 7}, {Code: "second", OccurredAt: 9}}
		second := []connectivity.RecentError{{Code: "third", OccurredAt: 9}}
		got := mergeRecentErrors(first, second)
		require.Equal(t, []string{"first", "second", "third"}, recentErrorCodes(got))
		got[0].Message = "mutated"
		require.Equal(t, "first-message", first[0].Message)
	})
}

func recentErrorCodes(events []connectivity.RecentError) []string {
	codes := make([]string, 0, len(events))
	for _, event := range events {
		codes = append(codes, event.Code)
	}
	return codes
}

func TestRelaySnapshotSourceProjectsDesiredConfigurationWithoutSession(t *testing.T) {
	t.Parallel()
	agents := fakeAgents{
		agents: map[string]*models.Agent{
			"inherit":  {AgentID: "inherit", Status: consts.StatusEnabled, RelayMode: consts.RelayModeInherit},
			"custom":   {AgentID: "custom", Status: consts.StatusEnabled, RelayMode: consts.RelayModeCustom, RelayURI: "wss://relay.example/custom?token=secret"},
			"disabled": {AgentID: "disabled", Status: consts.StatusEnabled, RelayMode: consts.RelayModeDisabled},
		},
		caps: map[string][]string{
			"inherit": {protocol.AgentCapabilityTunnelV1}, "custom": {protocol.AgentCapabilityTunnelV1}, "disabled": {protocol.AgentCapabilityTunnelV1},
		},
		relay: map[string]connectivity.RelayRuntimeFact{
			"inherit":  {Support: "supported", Config: "configured", Availability: "unavailable", Convergence: "converging", Desired: connectivity.RelayDesiredSnapshot{Mode: consts.RelayModeInherit, EffectiveURI: "wss://reported.example/ws?token=REDACTED"}},
			"custom":   {Support: "supported", Config: "configured", Availability: "available", AcceptingNewStreams: true, Convergence: "converged", Desired: connectivity.RelayDesiredSnapshot{Mode: consts.RelayModeCustom, ConfiguredURI: "wss://relay.example/custom?token=REDACTED", EffectiveURI: "wss://relay.example/custom?token=REDACTED"}, Active: connectivity.RelayActiveSnapshot{URI: "wss://active.example/ws?token=REDACTED", ActiveGeneration: 8, SessionGeneration: 9}},
			"disabled": {Support: "supported", Config: "disabled", Availability: "unavailable", Convergence: "converged", Desired: connectivity.RelayDesiredSnapshot{Mode: consts.RelayModeDisabled}},
		},
	}
	hub := NewHub(HubOptions{Agents: agents, Limits: testLimits()})
	var source connectivity.RelaySource = hub

	inherit, ok := source.GetRelayRuntime("inherit")
	require.True(t, ok)
	require.Equal(t, "supported", inherit.Support)
	require.Equal(t, "configured", inherit.Config)
	require.Equal(t, consts.RelayModeInherit, inherit.Desired.Mode)
	require.Equal(t, "wss://reported.example/ws?token=REDACTED", inherit.Desired.EffectiveURI)
	require.Equal(t, "unavailable", inherit.Availability)
	require.Equal(t, "converging", inherit.Convergence)

	custom, ok := source.GetRelayRuntime("custom")
	require.True(t, ok)
	require.Equal(t, "configured", custom.Config)
	require.Equal(t, consts.RelayModeCustom, custom.Desired.Mode)
	require.NotContains(t, custom.Desired.ConfiguredURI, "secret")
	require.Equal(t, "unavailable", custom.Availability)
	require.False(t, custom.AcceptingNewStreams)
	require.Zero(t, custom.Active)
	require.Equal(t, "converging", custom.Convergence)

	disabled, ok := source.GetRelayRuntime("disabled")
	require.True(t, ok)
	require.Equal(t, "disabled", disabled.Config)
	require.Equal(t, "converged", disabled.Convergence)
	require.False(t, disabled.AcceptingNewStreams)
}

func TestRelaySnapshotSourceDoesNotMixMismatchedHeartbeatAndHubGenerations(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"source": {protocol.AgentCapabilityTunnelV1}},
		relay: map[string]connectivity.RelayRuntimeFact{"source": {
			Support: "supported", Config: "configured", Availability: "available", AcceptingNewStreams: true,
			Convergence: "converged",
			Desired: connectivity.RelayDesiredSnapshot{
				EffectiveURI: "wss://stale.example/ws", DesiredGeneration: 7,
			},
			Active: connectivity.RelayActiveSnapshot{
				URI: "wss://stale.example/ws", ActiveGeneration: 7, SessionGeneration: 999,
			},
		}},
	}
	fixture := newRelayE2EFixture(t, agents)
	conn, welcome := fixture.connectDesired("source", 8)
	t.Cleanup(func() { _ = conn.Close() })

	fact, ok := fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, "available", fact.Availability)
	require.True(t, fact.AcceptingNewStreams)
	require.Equal(t, welcome.SessionGeneration, fact.Active.SessionGeneration)
	require.Equal(t, uint64(8), fact.Active.ActiveGeneration)
	require.Empty(t, fact.Active.URI)
	require.Equal(t, "converging", fact.Convergence)

	matching := agents.relay["source"]
	matching.Desired = connectivity.RelayDesiredSnapshot{
		EffectiveURI: "wss://current.example/ws", DesiredGeneration: 8,
	}
	matching.Active = connectivity.RelayActiveSnapshot{
		URI: "wss://wrong.example/ws", ActiveGeneration: 8, SessionGeneration: welcome.SessionGeneration,
	}
	agents.relay["source"] = matching
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, "converging", fact.Convergence, "URI mismatch must not converge")

	matching.Active.URI = matching.Desired.EffectiveURI
	matching.AcceptingNewStreams = false
	agents.relay["source"] = matching
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, "converging", fact.Convergence, "accepting mismatch must not converge")

	matching.AcceptingNewStreams = true
	agents.relay["source"] = matching
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, "converged", fact.Convergence)
	require.Equal(t, "wss://current.example/ws", fact.Active.URI)
	require.Equal(t, uint64(8), fact.Active.ActiveGeneration)
	require.Equal(t, welcome.SessionGeneration, fact.Active.SessionGeneration)

	fixture.hub.mu.RLock()
	active := fixture.hub.sessions["source"].Active
	fixture.hub.mu.RUnlock()
	require.NotNil(t, active)
	active.accepting.Store(false)
	matching.AcceptingNewStreams = false
	agents.relay["source"] = matching
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.False(t, fact.AcceptingNewStreams)
	require.Equal(t, "converging", fact.Convergence, "both sides not accepting must not converge")
	active.accepting.Store(true)
	matching.AcceptingNewStreams = true
	agents.relay["source"] = matching

	nextConn, nextWelcome := fixture.connectDesired("source", 9)
	t.Cleanup(func() { _ = nextConn.Close() })
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, uint64(9), fact.Active.ActiveGeneration)
	require.Equal(t, nextWelcome.SessionGeneration, fact.Active.SessionGeneration)
	require.Empty(t, fact.Active.URI, "old heartbeat URI must not join the replacement tuple")
	require.Equal(t, "converging", fact.Convergence)

	matching.Desired = connectivity.RelayDesiredSnapshot{
		EffectiveURI: "wss://next.example/ws", DesiredGeneration: 9,
	}
	matching.Active = connectivity.RelayActiveSnapshot{
		URI: "wss://next.example/ws", ActiveGeneration: 9, SessionGeneration: nextWelcome.SessionGeneration,
	}
	agents.relay["source"] = matching
	fact, ok = fixture.hub.GetRelayRuntime("source")
	require.True(t, ok)
	require.Equal(t, "converged", fact.Convergence)
	require.Equal(t, "wss://next.example/ws", fact.Active.URI)
	require.Equal(t, uint64(9), fact.Active.ActiveGeneration)
}

type signingStore struct{ key *models.MasterSigningKey }

func (s signingStore) LoadOrCreateActive(context.Context) (*models.MasterSigningKey, error) {
	return s.key, nil
}

func testSigner(t *testing.T, masterID string) *masteragentauth.Signer {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest := sha256.Sum256(public)
	active := uint8(1)
	signer, err := masteragentauth.NewSigner(context.Background(), signingStore{key: &models.MasterSigningKey{
		ActiveSlot: &active, KeyID: hex.EncodeToString(digest[:]), PublicKey: public, PrivateKey: private,
	}}, masterID, masteragentauth.SignerOptions{})
	require.NoError(t, err)
	return signer
}

type fakeAgents struct {
	agents map[string]*models.Agent
	caps   map[string][]string
	relay  map[string]connectivity.RelayRuntimeFact
}

type blockingAgentLookup struct {
	entered chan struct{}
	release chan struct{}
}

func (l *blockingAgentLookup) GetByAgentID(ctx context.Context, _ string) (*models.Agent, error) {
	close(l.entered)
	select {
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	case <-l.release:
		return &models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}, nil
	}
}

func (*blockingAgentLookup) Capabilities(string) []string { return nil }

func TestRelayHubCloseCancelsBlockedAgentLookup(t *testing.T) {
	signer := testSigner(t, "master-a")
	ticket, _, err := signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	lookup := &blockingAgentLookup{entered: make(chan struct{}), release: make(chan struct{})}
	defer close(lookup.release)
	hub := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: lookup, Limits: testLimits()})
	router := gin.New()
	router.GET("/ws/agent-relay", hub.HandleWS)
	server := httptest.NewServer(router)
	defer server.Close()
	request, err := http.NewRequest(http.MethodGet, server.URL+"/ws/agent-relay", nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+string(ticket))
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(request)
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	<-lookup.entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	require.NoError(t, hub.Close(ctx))
	select {
	case <-hub.Done():
	case <-time.After(time.Second):
		t.Fatal("Relay Hub.Done remained open after lookup cancellation")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("relay HTTP handler remained blocked after Hub close")
	}
}

func (f fakeAgents) GetByAgentID(_ context.Context, id string) (*models.Agent, error) {
	a := f.agents[id]
	if a == nil {
		return nil, errors.New("missing")
	}
	copy := *a
	return &copy, nil
}

func (f fakeAgents) Capabilities(id string) []string { return append([]string(nil), f.caps[id]...) }

func (f fakeAgents) GetRelayRuntime(id string) (connectivity.RelayRuntimeFact, bool) {
	fact, ok := f.relay[id]
	return fact, ok
}

func testLimits() wire.Limits {
	return wire.Limits{MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 1 << 20, MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 32}
}

func TestAdmissionGateDefaultsClosedAndCanBeEnabled(t *testing.T) {
	var gate AdmissionGate
	require.False(t, gate.AllowNew())
	gate.Set(true)
	require.True(t, gate.AllowNew())
	gate.Set(false)
	require.False(t, gate.AllowNew())
}

func TestHubRuntimeSettingsAreReadDynamically(t *testing.T) {
	t.Parallel()
	current := RuntimeSettings{Limits: testLimits(), DrainTimeout: 3 * time.Second}
	hub := NewHub(HubOptions{
		Limits: testLimits(), DrainTimeout: time.Second,
		RuntimeSettings: func() RuntimeSettings { return current },
	})
	first := hub.runtimeSettings()
	require.Equal(t, 3*time.Second, first.DrainTimeout)
	require.Equal(t, 32, first.Limits.MaxConcurrentStreams)

	current = RuntimeSettings{Limits: testLimits(), DrainTimeout: 7 * time.Second}
	current.Limits.MaxConcurrentStreams = 7
	second := hub.runtimeSettings()
	require.Equal(t, 7*time.Second, second.DrainTimeout)
	require.Equal(t, 7, second.Limits.MaxConcurrentStreams)
}

func TestHubValidatesRelayBearerTicketAndRejectsWrongMaster(t *testing.T) {
	signer := testSigner(t, "master-a")
	h := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: fakeAgents{agents: map[string]*models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}, Limits: testLimits()})
	ticket, _, err := signer.SignRelay("agent-a", 7)
	require.NoError(t, err)
	claims, err := h.authenticate("Bearer " + string(ticket))
	require.NoError(t, err)
	require.Equal(t, "agent-a", claims.AgentID)
	require.Equal(t, uint64(7), claims.DesiredGeneration)
	_, err = h.authenticate("Bearer forged")
	require.ErrorIs(t, err, errInvalidTicket)

	wrongSigner := testSigner(t, "master-b")
	wrongTicket, _, err := wrongSigner.SignRelay("agent-a", 7)
	require.NoError(t, err)
	_, err = h.authenticate("Bearer " + string(wrongTicket))
	require.ErrorIs(t, err, errWrongMaster)
}

func TestWelcomeBindsHelloNonceAndGeneration(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := testSigner(t, "master-a")
	agents := fakeAgents{agents: map[string]*models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}
	h := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: agents, Limits: testLimits()})
	router := gin.New()
	router.GET("/ws/agent-relay", h.HandleWS)
	server := httptest.NewServer(router)
	t.Cleanup(func() {
		server.Close()
		require.NoError(t, h.Close(context.Background()))
	})
	ticket, _, err := signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	header := http.Header{"Authorization": {"Bearer " + string(ticket)}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/ws/agent-relay", header)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	hello := wire.Hello{Nonce: "nonce-a", DesiredGeneration: 0}
	require.NoError(t, conn.WriteJSON(hello))
	messageType, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	require.Equal(t, "master-a", welcome.MasterInstanceID)
	require.NotZero(t, welcome.SessionGeneration)
	verifier := pkgauth.NewVerifier(singleKey{signer.PublicKey()})
	require.NoError(t, verifier.VerifyWelcome(welcome.NonceProof, pkgauth.WelcomeProofClaims{
		AgentID: "agent-a", Nonce: hello.Nonce, MasterInstanceID: "master-a",
		SessionGeneration: welcome.SessionGeneration, DesiredGeneration: hello.DesiredGeneration,
	}))
}

func TestHubShutdownClosesPendingHandshakeAndRejectsNewUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	signer := testSigner(t, "master-a")
	agents := fakeAgents{agents: map[string]*models.Agent{"agent-a": {AgentID: "agent-a", Status: consts.StatusEnabled}}}
	h := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: agents, Limits: testLimits()})
	router := gin.New()
	router.GET("/ws/agent-relay", h.HandleWS)
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	ticket, _, err := signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	header := http.Header{"Authorization": {"Bearer " + string(ticket)}}
	url := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent-relay"
	conn, _, err := websocket.DefaultDialer.Dial(url, header)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, h.Close(ctx))
	requireClosed(t, h.Done(), "Hub.Done")
	readResult := make(chan error, 1)
	go func() { _, _, readErr := conn.ReadMessage(); readResult <- readErr }()
	select {
	case readErr := <-readResult:
		require.Error(t, readErr)
	case <-ctx.Done():
		t.Fatal("pending handshake socket remained open after Hub.Close")
	}

	lateConn, response, err := websocket.DefaultDialer.Dial(url, header)
	if lateConn != nil {
		_ = lateConn.Close()
	}
	require.Error(t, err)
	if response != nil {
		require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	}
}

func TestHubCloseDeadlineDoesNotWaitForBlockedSocketClose(t *testing.T) {
	h := NewHub(HubOptions{Limits: testLimits()})
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	h.closeConnection = func(*websocket.Conn) error {
		startOnce.Do(func() { close(started) })
		<-release
		return nil
	}
	conn := &websocket.Conn{}
	h.mu.Lock()
	h.connections[conn] = wire.NewConnectionCloseOwner(func() error { return h.closeConnection(conn) })
	h.mu.Unlock()

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, h.Close(ctx), context.DeadlineExceeded)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Hub finalizer did not start socket close")
	}
	second := make(chan error, 1)
	go func() { second <- h.Close(context.Background()) }()
	select {
	case <-h.Done():
		t.Fatal("Hub.Done closed while socket close was blocked")
	default:
	}
	close(release)
	require.NoError(t, <-second)
	requireClosed(t, h.Done(), "Hub.Done after blocked socket close joined")
}

func TestHubCloseDoesNotWaitWithWorkerAdmissionLock(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Logger: zap.NewNop()})
	old := newTestSession(h, "agent-a", 1)
	candidate := newTestSession(h, "agent-a", 2)
	require.NoError(t, h.register(old))
	require.NoError(t, h.register(candidate))

	committed := make(chan struct{})
	releaseDrainSchedule := make(chan struct{})
	admissionClosed := make(chan struct{})
	h.beforeScheduleDrain = func() {
		close(committed)
		<-releaseDrainSchedule
	}
	h.afterWorkerAdmissionClosed = func() { close(admissionClosed) }

	require.True(t, h.beginHandler())
	handlerDone := make(chan error, 1)
	go func() {
		defer h.finishHandler()
		handlerDone <- h.commitConfirmedCandidate(candidate)
	}()
	requireClosed(t, committed, "candidate commit before drain scheduling")

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	closeDone := make(chan error, 1)
	go func() { closeDone <- h.Close(ctx) }()
	requireClosed(t, admissionClosed, "relay worker admission close")
	close(releaseDrainSchedule)
	select {
	case err := <-handlerDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("replacement handler deadlocked scheduling drain during Hub.Close")
	}
	require.NoError(t, <-closeDone)
	requireClosed(t, h.Done(), "Relay Hub.Done after committed replacement handler")
}

type nilAgentLookup struct{}

func (*nilAgentLookup) GetByAgentID(context.Context, string) (*models.Agent, error) {
	panic("typed nil lookup called")
}
func (*nilAgentLookup) Capabilities(string) []string { panic("typed nil lookup called") }

func TestHubTypedNilAgentLookupFailsClosed(t *testing.T) {
	signer := testSigner(t, "master-a")
	var agents *nilAgentLookup
	h := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: agents, Limits: testLimits()})
	ticket, _, err := signer.SignRelay("agent-a", 0)
	require.NoError(t, err)
	_, err = h.authenticate("Bearer " + string(ticket))
	require.ErrorIs(t, err, errInvalidTicket)
	require.NoError(t, h.Close(context.Background()))
}

type relayE2EFixture struct {
	t      *testing.T
	hub    *Hub
	server *httptest.Server
	signer *masteragentauth.Signer
	limits wire.Limits
	wsURL  string
}

func newRelayE2EFixture(t *testing.T, agents fakeAgents) *relayE2EFixture {
	return newRelayE2EFixtureWithLimits(t, agents, testLimits())
}

func newRelayE2EFixtureWithLimits(t *testing.T, agents fakeAgents, limits wire.Limits) *relayE2EFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	signer := testSigner(t, "master-a")
	gate := &AdmissionGate{}
	gate.Set(true)
	h := NewHub(HubOptions{InstanceID: "master-a", Signer: signer, Agents: agents, Admission: gate, Limits: limits})
	router := gin.New()
	router.GET("/ws/agent-relay", h.HandleWS)
	server := httptest.NewServer(router)
	f := &relayE2EFixture{t: t, hub: h, server: server, signer: signer, limits: limits, wsURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws/agent-relay"}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, h.Close(ctx))
		server.Close()
	})
	return f
}

func (f *relayE2EFixture) dial(agentID string, desiredGeneration uint64) *websocket.Conn {
	f.t.Helper()
	ticket, _, err := f.signer.SignRelay(agentID, desiredGeneration)
	require.NoError(f.t, err)
	conn, _, err := websocket.DefaultDialer.Dial(f.wsURL, http.Header{"Authorization": {"Bearer " + string(ticket)}})
	require.NoError(f.t, err)
	return conn
}

func (f *relayE2EFixture) connect(agentID string) (*websocket.Conn, wire.Welcome) {
	return f.connectDesired(agentID, 0)
}

func (f *relayE2EFixture) connectDesired(agentID string, desiredGeneration uint64) (*websocket.Conn, wire.Welcome) {
	f.t.Helper()
	conn := f.dial(agentID, desiredGeneration)
	require.NoError(f.t, conn.WriteJSON(wire.Hello{Nonce: "nonce-" + agentID, DesiredGeneration: desiredGeneration}))
	messageType, raw, err := conn.ReadMessage()
	require.NoError(f.t, err)
	require.Equal(f.t, websocket.TextMessage, messageType)
	var welcome wire.Welcome
	require.NoError(f.t, json.Unmarshal(raw, &welcome))
	require.NoError(f.t, conn.WriteJSON(wire.Authenticated{
		DesiredGeneration: desiredGeneration, SessionGeneration: welcome.SessionGeneration,
	}))
	var confirmed wire.Confirmed
	require.NoError(f.t, conn.ReadJSON(&confirmed))
	require.Equal(f.t, desiredGeneration, confirmed.DesiredGeneration)
	require.Equal(f.t, welcome.SessionGeneration, confirmed.SessionGeneration)
	require.Eventually(f.t, func() bool {
		snapshot, ok := f.hub.Snapshot(agentID)
		return ok && snapshot.Generation == welcome.SessionGeneration
	}, time.Second, time.Millisecond)
	return conn, welcome
}

func TestHubDoesNotPromoteCandidateBeforeAuthenticatedACK(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"source": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	oldConn, oldWelcome := f.connectDesired("source", 1)
	t.Cleanup(func() { _ = oldConn.Close() })
	require.Eventually(t, func() bool {
		snapshot, ok := f.hub.Snapshot("source")
		return ok && snapshot.Generation == oldWelcome.SessionGeneration
	}, time.Second, time.Millisecond)

	candidate := f.dial("source", 2)
	t.Cleanup(func() { _ = candidate.Close() })
	require.NoError(t, candidate.WriteJSON(wire.Hello{Nonce: "candidate", DesiredGeneration: 2}))
	_, raw, err := candidate.ReadMessage()
	require.NoError(t, err)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	snapshot, ok := f.hub.Snapshot("source")
	require.True(t, ok)
	require.Equal(t, oldWelcome.SessionGeneration, snapshot.Generation)
	require.NotEqual(t, welcome.SessionGeneration, snapshot.Generation)

	require.NoError(t, candidate.WriteJSON(wire.Authenticated{DesiredGeneration: 999, SessionGeneration: welcome.SessionGeneration}))
	require.NoError(t, candidate.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, readErr := candidate.ReadMessage()
	require.Error(t, readErr)
	stillOld, found := f.hub.Snapshot("source")
	require.True(t, found)
	require.Equal(t, oldWelcome.SessionGeneration, stillOld.Generation)
}

func TestHubConfirmedWriteFailureRollsBackToOldActive(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"source": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	oldConn, oldWelcome := f.connectDesired("source", 1)
	t.Cleanup(func() { _ = oldConn.Close() })
	f.hub.mu.RLock()
	oldSession := f.hub.sessions["source"].Active
	f.hub.mu.RUnlock()
	require.NotNil(t, oldSession)
	writes := 0
	confirmedStarted := make(chan struct{})
	releaseConfirmed := make(chan struct{})
	var releaseOnce sync.Once
	releaseWrite := func() { releaseOnce.Do(func() { close(releaseConfirmed) }) }
	t.Cleanup(releaseWrite)
	f.hub.writeHandshake = func(conn *websocket.Conn, messageType int, payload []byte) error {
		writes++
		if writes == 2 {
			close(confirmedStarted)
			<-releaseConfirmed
			return errors.New("injected CONFIRMED write failure")
		}
		return conn.WriteMessage(messageType, payload)
	}

	candidate := f.dial("source", 2)
	t.Cleanup(func() { _ = candidate.Close() })
	require.NoError(t, candidate.WriteJSON(wire.Hello{Nonce: "candidate-confirm", DesiredGeneration: 2}))
	_, raw, err := candidate.ReadMessage()
	require.NoError(t, err)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	require.NoError(t, candidate.WriteJSON(wire.Authenticated{
		DesiredGeneration: 2, SessionGeneration: welcome.SessionGeneration,
	}))
	requireClosed(t, confirmedStarted, "failed CONFIRMED write")
	snapshot, ok := f.hub.Snapshot("source")
	require.True(t, ok)
	require.Equal(t, oldWelcome.SessionGeneration, snapshot.Generation, "old must remain active until CONFIRMED succeeds")
	f.hub.mu.RLock()
	set := f.hub.sessions["source"]
	activeDuringConfirmed := set.Active
	candidateDuringConfirmed := set.Candidate
	candidateRunningDuringConfirmed := candidateDuringConfirmed != nil && candidateDuringConfirmed.running.Load()
	f.hub.mu.RUnlock()
	require.Same(t, oldSession, activeDuringConfirmed)
	require.NotNil(t, candidateDuringConfirmed)
	require.False(t, candidateRunningDuringConfirmed)
	require.NoError(t, oldConn.Close())
	requireClosed(t, oldSession.Done(), "old active ending during CONFIRMED write")
	releaseWrite()
	require.NoError(t, candidate.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = candidate.ReadMessage()
	require.Error(t, err)
	_, ok = f.hub.Snapshot("source")
	require.False(t, ok, "failed candidate must not resurrect ended old active")
	f.hub.mu.RLock()
	set = f.hub.sessions["source"]
	oldDraining := false
	if set != nil {
		_, oldDraining = set.Draining[oldWelcome.SessionGeneration]
	}
	f.hub.mu.RUnlock()
	require.False(t, oldDraining)
}

func TestHubHandshakeDeadlineFailuresKeepOldActive(t *testing.T) {
	for _, tc := range []struct {
		name                                                     string
		failReadSet, failWriteSet, failReadClear, failWriteClear bool
	}{
		{name: "set read", failReadSet: true},
		{name: "set write", failWriteSet: true},
		{name: "clear read", failReadClear: true},
		{name: "clear write", failWriteClear: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
			f := newRelayE2EFixture(t, agents)
			oldConn, oldWelcome := f.connectDesired("source", 1)
			t.Cleanup(func() { _ = oldConn.Close() })
			f.hub.setReadDeadline = func(conn *websocket.Conn, deadline time.Time) error {
				if (deadline.IsZero() && tc.failReadClear) || (!deadline.IsZero() && tc.failReadSet) {
					return errors.New("injected read deadline failure")
				}
				return conn.SetReadDeadline(deadline)
			}
			f.hub.setWriteDeadline = func(conn *websocket.Conn, deadline time.Time) error {
				if (deadline.IsZero() && tc.failWriteClear) || (!deadline.IsZero() && tc.failWriteSet) {
					return errors.New("injected write deadline failure")
				}
				return conn.SetWriteDeadline(deadline)
			}

			candidate := f.dial("source", 2)
			t.Cleanup(func() { _ = candidate.Close() })
			_ = candidate.WriteJSON(wire.Hello{Nonce: "deadline", DesiredGeneration: 2})
			require.NoError(t, candidate.SetReadDeadline(time.Now().Add(time.Second)))
			_, raw, readErr := candidate.ReadMessage()
			if readErr == nil {
				var welcome wire.Welcome
				require.NoError(t, json.Unmarshal(raw, &welcome))
				_ = candidate.WriteJSON(wire.Authenticated{DesiredGeneration: 2, SessionGeneration: welcome.SessionGeneration})
				_, _, readErr = candidate.ReadMessage()
			}
			require.Error(t, readErr)
			snapshot, ok := f.hub.Snapshot("source")
			require.True(t, ok)
			require.Equal(t, oldWelcome.SessionGeneration, snapshot.Generation)
		})
	}
}

func TestHubSuccessfulHandshakeClearsDeadlinesBeforeConfirmed(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	f := newRelayE2EFixture(t, agents)
	var deadlineMu sync.Mutex
	var readDeadlines, writeDeadlines []time.Time
	f.hub.setReadDeadline = func(conn *websocket.Conn, deadline time.Time) error {
		deadlineMu.Lock()
		readDeadlines = append(readDeadlines, deadline)
		deadlineMu.Unlock()
		return conn.SetReadDeadline(deadline)
	}
	f.hub.setWriteDeadline = func(conn *websocket.Conn, deadline time.Time) error {
		deadlineMu.Lock()
		writeDeadlines = append(writeDeadlines, deadline)
		deadlineMu.Unlock()
		return conn.SetWriteDeadline(deadline)
	}

	conn, welcome := f.connectDesired("source", 1)
	t.Cleanup(func() { _ = conn.Close() })
	deadlineMu.Lock()
	require.Len(t, readDeadlines, 2)
	require.False(t, readDeadlines[0].IsZero())
	require.True(t, readDeadlines[1].IsZero())
	require.Len(t, writeDeadlines, 2)
	require.False(t, writeDeadlines[0].IsZero())
	require.True(t, writeDeadlines[1].IsZero())
	deadlineMu.Unlock()
	snapshot, ok := f.hub.Snapshot("source")
	require.True(t, ok)
	require.Equal(t, welcome.SessionGeneration, snapshot.Generation)
}

func TestHubConfirmedWriteTimeoutKeepsOldActive(t *testing.T) {
	for _, tc := range []struct {
		name        string
		writeResult error
	}{
		{name: "write returns error", writeResult: errors.New("injected blocked CONFIRMED write")},
		{name: "write returns nil after timeout close"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testHubConfirmedWriteTimeoutKeepsOldActive(t, tc.writeResult)
		})
	}
}

func testHubConfirmedWriteTimeoutKeepsOldActive(t *testing.T, writeResult error) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	f := newRelayE2EFixture(t, agents)
	oldConn, oldWelcome := f.connectDesired("source", 1)
	t.Cleanup(func() { _ = oldConn.Close() })
	f.hub.handshakeTimeout = 100 * time.Millisecond
	confirmedStarted := make(chan struct{})
	connectionClosed := make(chan struct{})
	candidateSessions := make(chan *Session, 1)
	var closeOnce sync.Once
	var closeCalls atomic.Int32
	f.hub.closeConnection = func(conn *websocket.Conn) error {
		closeCalls.Add(1)
		closeOnce.Do(func() { close(connectionClosed) })
		return conn.Close()
	}
	writes := 0
	f.hub.writeHandshake = func(conn *websocket.Conn, messageType int, payload []byte) error {
		writes++
		if writes == 2 {
			f.hub.mu.RLock()
			session := f.hub.sessions["source"].Candidate
			f.hub.mu.RUnlock()
			candidateSessions <- session
			close(confirmedStarted)
			<-connectionClosed
			return writeResult
		}
		return conn.WriteMessage(messageType, payload)
	}

	candidate := f.dial("source", 2)
	t.Cleanup(func() { _ = candidate.Close() })
	require.NoError(t, candidate.WriteJSON(wire.Hello{Nonce: "candidate-timeout", DesiredGeneration: 2}))
	_, raw, err := candidate.ReadMessage()
	require.NoError(t, err)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	require.NoError(t, candidate.WriteJSON(wire.Authenticated{
		DesiredGeneration: 2, SessionGeneration: welcome.SessionGeneration,
	}))
	requireClosed(t, confirmedStarted, "blocked CONFIRMED write")
	candidateSession := <-candidateSessions
	requireClosed(t, connectionClosed, "CONFIRMED write timeout")
	require.NoError(t, candidate.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = candidate.ReadMessage()
	require.Error(t, err)
	requireClosed(t, candidateSession.Done(), "timed out candidate session")
	snapshot, ok := f.hub.Snapshot("source")
	require.True(t, ok)
	require.Equal(t, oldWelcome.SessionGeneration, snapshot.Generation)
	require.Eventually(t, func() bool {
		f.hub.mu.RLock()
		defer f.hub.mu.RUnlock()
		return f.hub.sessions["source"].Candidate == nil
	}, time.Second, time.Millisecond)
	f.hub.mu.RLock()
	set := f.hub.sessions["source"]
	remainingCandidate := set.Candidate
	_, draining := set.Draining[welcome.SessionGeneration]
	f.hub.mu.RUnlock()
	require.Nil(t, remainingCandidate)
	require.False(t, draining)
	require.EqualValues(t, 1, closeCalls.Load())
}

func TestHubConfirmedWriteCancellationClosesCandidateOnce(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	f := newRelayE2EFixture(t, agents)
	f.hub.handshakeTimeout = time.Hour
	confirmedStarted := make(chan struct{})
	connectionClosed := make(chan struct{})
	candidateSessions := make(chan *Session, 1)
	var closeOnce sync.Once
	var closeCalls atomic.Int32
	f.hub.closeConnection = func(conn *websocket.Conn) error {
		closeCalls.Add(1)
		closeOnce.Do(func() { close(connectionClosed) })
		return conn.Close()
	}
	writes := 0
	f.hub.writeHandshake = func(conn *websocket.Conn, messageType int, payload []byte) error {
		writes++
		if writes == 2 {
			f.hub.mu.RLock()
			session := f.hub.sessions["source"].Candidate
			f.hub.mu.RUnlock()
			candidateSessions <- session
			close(confirmedStarted)
			<-connectionClosed
			return nil
		}
		return conn.WriteMessage(messageType, payload)
	}

	candidate := f.dial("source", 1)
	t.Cleanup(func() { _ = candidate.Close() })
	require.NoError(t, candidate.WriteJSON(wire.Hello{Nonce: "candidate-cancel", DesiredGeneration: 1}))
	_, raw, err := candidate.ReadMessage()
	require.NoError(t, err)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	require.NoError(t, candidate.WriteJSON(wire.Authenticated{
		DesiredGeneration: 1, SessionGeneration: welcome.SessionGeneration,
	}))
	requireClosed(t, confirmedStarted, "blocked CONFIRMED write before cancellation")
	candidateSession := <-candidateSessions
	f.hub.cancel(errHubClosed)
	requireClosed(t, connectionClosed, "CONFIRMED write cancellation")
	require.NoError(t, candidate.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = candidate.ReadMessage()
	require.Error(t, err)
	requireClosed(t, candidateSession.Done(), "canceled candidate session")
	require.EqualValues(t, 1, closeCalls.Load())
}

func TestHubConfirmedWriteSuccessPromotesCandidateAfterOldEnds(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	f := newRelayE2EFixture(t, agents)
	oldConn, oldWelcome := f.connectDesired("source", 1)
	t.Cleanup(func() { _ = oldConn.Close() })
	f.hub.mu.RLock()
	oldSession := f.hub.sessions["source"].Active
	f.hub.mu.RUnlock()
	require.NotNil(t, oldSession)
	confirmedStarted := make(chan struct{})
	releaseConfirmed := make(chan struct{})
	var releaseOnce sync.Once
	var candidateConn atomic.Pointer[websocket.Conn]
	var candidateCloseCalls atomic.Int32
	f.hub.closeConnection = func(conn *websocket.Conn) error {
		if candidateConn.Load() == conn {
			candidateCloseCalls.Add(1)
		}
		return conn.Close()
	}
	releaseWrite := func() { releaseOnce.Do(func() { close(releaseConfirmed) }) }
	t.Cleanup(releaseWrite)
	writes := 0
	f.hub.writeHandshake = func(conn *websocket.Conn, messageType int, payload []byte) error {
		writes++
		if writes == 2 {
			candidateConn.Store(conn)
			close(confirmedStarted)
			<-releaseConfirmed
		}
		return conn.WriteMessage(messageType, payload)
	}

	candidate := f.dial("source", 2)
	t.Cleanup(func() { _ = candidate.Close() })
	require.NoError(t, candidate.WriteJSON(wire.Hello{Nonce: "candidate-success", DesiredGeneration: 2}))
	_, raw, err := candidate.ReadMessage()
	require.NoError(t, err)
	var welcome wire.Welcome
	require.NoError(t, json.Unmarshal(raw, &welcome))
	require.NoError(t, candidate.WriteJSON(wire.Authenticated{
		DesiredGeneration: 2, SessionGeneration: welcome.SessionGeneration,
	}))
	requireClosed(t, confirmedStarted, "successful CONFIRMED write")
	f.hub.mu.RLock()
	set := f.hub.sessions["source"]
	activeBeforeConfirmed := set.Active
	candidateBeforeConfirmed := set.Candidate
	candidateRunningBeforeConfirmed := candidateBeforeConfirmed != nil && candidateBeforeConfirmed.running.Load()
	f.hub.mu.RUnlock()
	require.Same(t, oldSession, activeBeforeConfirmed)
	require.NotNil(t, candidateBeforeConfirmed)
	require.False(t, candidateRunningBeforeConfirmed)
	require.NoError(t, oldConn.Close())
	requireClosed(t, oldSession.Done(), "old active ending during successful CONFIRMED write")
	releaseWrite()
	var confirmed wire.Confirmed
	require.NoError(t, candidate.ReadJSON(&confirmed))
	require.Equal(t, welcome.SessionGeneration, confirmed.SessionGeneration)
	require.Eventually(t, func() bool {
		snapshot, ok := f.hub.Snapshot("source")
		return ok && snapshot.Generation == welcome.SessionGeneration
	}, time.Second, time.Millisecond)
	f.hub.mu.RLock()
	set = f.hub.sessions["source"]
	candidateAfterConfirmed := set.Candidate
	_, oldDraining := set.Draining[oldWelcome.SessionGeneration]
	activeRunning := set.Active != nil && set.Active.running.Load()
	f.hub.mu.RUnlock()
	require.Nil(t, candidateAfterConfirmed)
	require.False(t, oldDraining)
	require.True(t, activeRunning)
	require.NoError(t, candidate.Close())
	requireClosed(t, candidateBeforeConfirmed.Done(), "successful candidate session close")
	require.EqualValues(t, 1, candidateCloseCalls.Load())
}

func readPeerErrorAndCloseHub(t *testing.T, f *relayE2EFixture, conn *websocket.Conn) error {
	t.Helper()
	_, _, err := conn.ReadMessage()
	require.Error(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, f.hub.Close(ctx))
	requireClosed(t, f.hub.Done(), "Hub.Done after transport rejection")
	return err
}

func TestHubWebSocketReadLimitBoundsHello(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	helloAtTransportLimit := mustJSON(t, wire.Hello{Nonce: "n"})
	helloAtTransportLimit = append(helloAtTransportLimit, bytes.Repeat([]byte{' '}, int(maxHelloMessageBytes)-len(helloAtTransportLimit))...)
	require.Len(t, helloAtTransportLimit, int(maxHelloMessageBytes))
	for _, tc := range []struct {
		name    string
		payload []byte
		allowed bool
	}{
		{name: "nonce at limit", payload: mustJSON(t, wire.Hello{Nonce: string(bytes.Repeat([]byte{'n'}, 256))}), allowed: true},
		{name: "message at transport limit", payload: helloAtTransportLimit, allowed: true},
		{name: "nonce over limit", payload: mustJSON(t, wire.Hello{Nonce: string(bytes.Repeat([]byte{'n'}, 257))})},
		{name: "message over transport limit", payload: []byte(`{"nonce":"n","padding":"` + string(bytes.Repeat([]byte{'p'}, 1024)) + `"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelayE2EFixture(t, agents)
			conn := f.dial("source", 0)
			t.Cleanup(func() { _ = conn.Close() })
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, tc.payload))
			if tc.allowed {
				messageType, _, err := conn.ReadMessage()
				require.NoError(t, err)
				require.Equal(t, websocket.TextMessage, messageType)
				require.NoError(t, conn.Close())
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				require.NoError(t, f.hub.Close(ctx))
				return
			}
			readErr := readPeerErrorAndCloseHub(t, f, conn)
			if tc.name == "message over transport limit" {
				require.True(t, websocket.IsCloseError(readErr, websocket.CloseMessageTooBig), "unexpected close error: %v", readErr)
			}
		})
	}
}

func TestHubWebSocketReadLimitBoundsBinaryMessages(t *testing.T) {
	limits := testLimits()
	limits.MaxMetadataBytes = 64
	limits.MaxDataBytes = 128
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	for _, overLimit := range []bool{false, true} {
		name := "at limit"
		if overLimit {
			name = "over limit"
		}
		t.Run(name, func(t *testing.T) {
			f := newRelayE2EFixtureWithLimits(t, agents, limits)
			conn, _ := f.connect("source")
			t.Cleanup(func() { _ = conn.Close() })
			raw, err := wire.Encode(wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: wire.StreamID{1}, Sequence: 1,
				Payload: bytes.Repeat([]byte{'d'}, int(limits.MaxDataBytes)),
			}, limits)
			require.NoError(t, err)
			if overLimit {
				raw = append(raw, 'd')
				binary.BigEndian.PutUint32(raw[24:28], uint32(limits.MaxDataBytes+1))
			}
			require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, raw))
			if overLimit {
				readErr := readPeerErrorAndCloseHub(t, f, conn)
				require.True(t, websocket.IsCloseError(readErr, websocket.CloseMessageTooBig), "unexpected close error: %v", readErr)
				return
			}
			reset := readRelayFrame(t, conn, limits)
			require.Equal(t, wire.FrameReset, reset.Type)
			require.NoError(t, conn.Close())
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			require.NoError(t, f.hub.Close(ctx))
		})
	}
}

func TestHubGenerationExhaustionFailsHandshakeClosed(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}}
	f := newRelayE2EFixture(t, agents)
	f.hub.mu.Lock()
	f.hub.nextGen = math.MaxUint64
	f.hub.mu.Unlock()
	conn := f.dial("source", math.MaxUint64)
	t.Cleanup(func() { _ = conn.Close() })
	require.NoError(t, conn.WriteJSON(wire.Hello{Nonce: "nonce-source", DesiredGeneration: math.MaxUint64}))
	readPeerErrorAndCloseHub(t, f, conn)
	f.hub.mu.RLock()
	require.Empty(t, f.hub.sessions)
	f.hub.mu.RUnlock()
}

func TestDesiredGenerationCannotAdvanceMasterGeneration(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{
		"source": {AgentID: "source", Status: consts.StatusEnabled},
		"target": {AgentID: "target", Status: consts.StatusEnabled},
	}}
	f := newRelayE2EFixture(t, agents)
	first, firstWelcome := f.connectDesired("source", math.MaxUint64)
	t.Cleanup(func() { _ = first.Close() })
	require.Equal(t, uint64(1), firstWelcome.SessionGeneration)
	second, secondWelcome := f.connect("target")
	t.Cleanup(func() { _ = second.Close() })
	require.Equal(t, uint64(2), secondWelcome.SessionGeneration)
	f.hub.mu.RLock()
	require.Equal(t, uint64(2), f.hub.nextGen)
	f.hub.mu.RUnlock()
}

func TestAllocateGenerationBoundaries(t *testing.T) {
	t.Run("zero starts at one", func(t *testing.T) {
		h := NewHub(HubOptions{Limits: testLimits()})
		generation, err := h.allocateGeneration()
		require.NoError(t, err)
		require.Equal(t, uint64(1), generation)
	})
	t.Run("max minus one advances once", func(t *testing.T) {
		h := NewHub(HubOptions{Limits: testLimits()})
		h.nextGen = math.MaxUint64 - 1
		generation, err := h.allocateGeneration()
		require.NoError(t, err)
		require.Equal(t, uint64(math.MaxUint64), generation)
		_, err = h.allocateGeneration()
		require.ErrorIs(t, err, errGenerationExhausted)
	})
}

func TestGenerationExhaustionNeverReusesDrainingGeneration(t *testing.T) {
	h := NewHub(HubOptions{Limits: testLimits()})
	firstGeneration, err := h.allocateGeneration()
	require.NoError(t, err)
	first := newTestSession(h, "agent-a", firstGeneration)
	require.NoError(t, h.register(first))

	h.nextGen = math.MaxUint64 - 1
	lastGeneration, err := h.allocateGeneration()
	require.NoError(t, err)
	last := newTestSession(h, "agent-a", lastGeneration)
	require.NoError(t, h.register(last))
	require.NoError(t, h.promote(last))

	_, err = h.allocateGeneration()
	require.ErrorIs(t, err, errGenerationExhausted)
	h.mu.RLock()
	require.Same(t, last, h.sessions["agent-a"].Active)
	require.Same(t, first, h.sessions["agent-a"].Draining[firstGeneration])
	h.mu.RUnlock()
	require.NoError(t, h.Close(context.Background()))
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	require.NoError(t, err)
	return payload
}

func writeRelayFrame(t *testing.T, conn *websocket.Conn, limits wire.Limits, frame wire.Frame) {
	t.Helper()
	raw, err := wire.Encode(frame, limits)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, raw))
}

func readRelayFrame(t *testing.T, conn *websocket.Conn, limits wire.Limits) wire.Frame {
	t.Helper()
	messageType, raw, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(raw, limits)
	require.NoError(t, err)
	return frame
}

func openRelaySwitch(t *testing.T, f *relayE2EFixture, source, target *websocket.Conn, id wire.StreamID) *Switch {
	return openRelaySwitchWithRemaining(t, f, source, target, id, time.Second)
}

func openRelaySwitchWithRemaining(t *testing.T, f *relayE2EFixture, source, target *websocket.Conn, id wire.StreamID, remaining time.Duration) *Switch {
	t.Helper()
	payload, err := wire.EncodeMetadata(wire.Open{Method: http.MethodPost, Path: "/v1/responses", SourceAgentID: "forged", TargetAgentID: "target", RemainingNanos: int64(remaining)}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	writeRelayFrame(t, source, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload})
	forwarded := readRelayFrame(t, target, f.limits)
	require.Equal(t, wire.FrameOpen, forwarded.Type)
	var open wire.Open
	require.NoError(t, wire.DecodeMetadata(forwarded.Payload, &open, f.limits.MaxMetadataBytes))
	require.Equal(t, "source", open.SourceAgentID)
	if remaining > 0 {
		require.Less(t, open.RemainingNanos, int64(remaining))
	} else {
		require.Zero(t, open.RemainingNanos)
	}
	f.hub.mu.RLock()
	defer f.hub.mu.RUnlock()
	for _, sw := range f.hub.switches {
		if sw.id == id {
			return sw
		}
	}
	t.Fatal("Hub did not register Switch")
	return nil
}

func readRelayReset(t *testing.T, conn *websocket.Conn, limits wire.Limits) (wire.Frame, wire.Reset) {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	frame := readRelayFrame(t, conn, limits)
	require.NoError(t, conn.SetReadDeadline(time.Time{}))
	require.Equal(t, wire.FrameReset, frame.Type)
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &reset, limits.MaxMetadataBytes))
	return frame, reset
}

func TestHubRelayE2EForwardsOpenReadyDataFlowControlAndTerminal(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{
			"source": {AgentID: "source", Status: consts.StatusEnabled},
			"target": {AgentID: "target", Status: consts.StatusEnabled},
		},
		caps: map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	target, _ := f.connect("target")
	source, _ := f.connect("source")
	t.Cleanup(func() { _ = source.Close(); _ = target.Close() })
	id := wire.StreamID{70}
	sw := openRelaySwitch(t, f, source, target, id)

	readyPayload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: 1024}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: readyPayload})
	require.Equal(t, wire.FrameReady, readRelayFrame(t, source, f.limits).Type)
	writeRelayFrame(t, source, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 2, Payload: []byte("request")})
	require.Equal(t, []byte("request"), readRelayFrame(t, target, f.limits).Payload)
	windowPayload, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: 7}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	writeRelayFrame(t, source, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate, StreamID: id, Sequence: 3, Payload: windowPayload})
	require.Equal(t, wire.FrameWindowUpdate, readRelayFrame(t, target, f.limits).Type)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate, StreamID: id, Sequence: 2, Payload: windowPayload})
	require.Equal(t, wire.FrameWindowUpdate, readRelayFrame(t, source, f.limits).Type)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: id, Sequence: 3, Payload: []byte("response")})
	require.Equal(t, []byte("response"), readRelayFrame(t, source, f.limits).Payload)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: id, Sequence: 4})
	require.Equal(t, wire.FrameEnd, readRelayFrame(t, source, f.limits).Type)
	requireClosed(t, sw.Done(), "Switch.Done after END")
	require.NoError(t, source.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
	_, _, err = source.ReadMessage()
	require.ErrorContains(t, err, "i/o timeout", "normal END must not synthesize a second terminal")
}

func TestHubRelayE2ENormalTerminalsDoNotSynthesizeReset(t *testing.T) {
	for _, terminalType := range []wire.Type{wire.FrameEnd, wire.FrameCancel, wire.FrameReset} {
		t.Run(fmt.Sprintf("type-%d", terminalType), func(t *testing.T) {
			agents := fakeAgents{
				agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
				caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
			}
			f := newRelayE2EFixture(t, agents)
			target, _ := f.connect("target")
			source, _ := f.connect("source")
			t.Cleanup(func() { _ = source.Close(); _ = target.Close() })
			id := wire.StreamID{76, byte(terminalType)}
			sw := openRelaySwitch(t, f, source, target, id)
			var payload []byte
			if terminalType != wire.FrameEnd {
				var err error
				payload, err = wire.EncodeMetadata(wire.Reset{Code: wire.ErrorCodeRequestCancelled, Stage: "target"}, f.limits.MaxMetadataBytes)
				require.NoError(t, err)
			}
			writeRelayFrame(t, target, f.limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: terminalType, StreamID: id, Sequence: 1, Payload: payload,
			})
			require.Equal(t, terminalType, readRelayFrame(t, source, f.limits).Type)
			requireClosed(t, sw.Done(), "Switch.Done after normal terminal")
			require.NoError(t, source.SetReadDeadline(time.Now().Add(50*time.Millisecond)))
			_, _, err := source.ReadMessage()
			require.ErrorContains(t, err, "i/o timeout", "normal terminal must not synthesize RESET")
		})
	}
}

func TestHubSyntheticResetTerminatesRealAgentStream(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	target, _ := f.connect("target")
	sourceConn, welcome := f.connect("source")
	agentSession := agenttunnel.NewSession(sourceConn, welcome.SessionGeneration, welcome.Limits, agenttunnel.SessionOptions{
		PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- agentSession.Run(runCtx) }()
	t.Cleanup(func() {
		agentSession.Cancel(context.Canceled)
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(time.Second):
			t.Error("Agent Session.Run did not exit")
		}
		_ = target.Close()
	})
	stream, err := agentSession.OpenStream(t.Context(), agentproxy.RelayRequest{
		Purpose: wire.StreamPurposeConnectivityProbe, TargetAgentID: "target",
		Method: http.MethodGet, Path: "/ping", Header: make(http.Header), Remaining: time.Second,
	})
	require.NoError(t, err)
	open := readRelayFrame(t, target, f.limits)
	require.Equal(t, wire.FrameOpen, open.Type)
	require.NoError(t, target.Close())
	requireClosed(t, stream.Done(), "real Agent Stream after synthetic RESET")
	commitCtx, cancelCommit := context.WithTimeout(context.Background(), time.Second)
	defer cancelCommit()
	require.EqualError(t, stream.Commit(commitCtx), "agent tunnel: stream closed")
	select {
	case <-agentSession.Done():
		t.Fatal("synthetic stream RESET closed the Agent Session")
	default:
	}
}

func TestRouteFallbackDisabledResetReachesSourceCommitWithTypedCode(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"source": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	sourceConn, welcome := f.connect("source")
	agentSession := agenttunnel.NewSession(sourceConn, welcome.SessionGeneration, welcome.Limits, agenttunnel.SessionOptions{
		PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	runCtx, cancelRun := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- agentSession.Run(runCtx) }()
	t.Cleanup(func() {
		agentSession.Cancel(context.Canceled)
		cancelRun()
		select {
		case <-runDone:
		case <-time.After(time.Second):
			t.Error("Agent Session.Run did not exit")
		}
	})

	f.hub.opts.Admission.Set(false)
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	stream, err := agentSession.OpenStream(t.Context(), agentproxy.RelayRequest{
		TargetAgentID: "target", Method: http.MethodPost, Path: attemptwire.EndpointPath,
		Remaining: time.Second, Hop: 1, Attempt: &meta,
	})
	require.NoError(t, err)
	commitCtx, cancelCommit := context.WithTimeout(t.Context(), time.Second)
	defer cancelCommit()
	err = stream.Commit(commitCtx)
	require.Error(t, err)
	require.Equal(t, wire.PreCommit, stream.CommitState())
	var coded interface{ ResetCode() string }
	require.ErrorAs(t, err, &coded)
	require.Equal(t, "relay_fallback_disabled", coded.ResetCode())
}

func TestRouteFallbackDisabledAdmissionAllowsOnlyBoundedConnectivityProbe(t *testing.T) {
	tests := []struct {
		name    string
		purpose wire.StreamPurpose
		method  string
		path    string
		allowed bool
	}{
		{name: "connectivity probe", purpose: wire.StreamPurposeConnectivityProbe, method: http.MethodGet, path: "/ping", allowed: true},
		{name: "probe purpose on another path", purpose: wire.StreamPurposeConnectivityProbe, method: http.MethodGet, path: "/v1/models"},
		{name: "unmarked ping", method: http.MethodGet, path: "/ping"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			agents := fakeAgents{
				agents: map[string]*models.Agent{
					"source": {AgentID: "source", Status: consts.StatusEnabled},
					"target": {AgentID: "target", Status: consts.StatusEnabled},
				},
				caps: map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
			}
			f := newRelayE2EFixture(t, agents)
			target, _ := f.connect("target")
			source, _ := f.connect("source")
			t.Cleanup(func() { _ = source.Close(); _ = target.Close() })
			f.hub.opts.Admission.Set(false)

			payload, err := wire.EncodeMetadata(wire.Open{
				Purpose: test.purpose, Method: test.method, Path: test.path, Header: map[string][]string{},
				TargetAgentID: "target", RequestID: "relay-connectivity-probe",
				RemainingNanos: int64(time.Second), ResponseWindow: f.limits.InitialStreamWindow,
			}, f.limits.MaxMetadataBytes)
			require.NoError(t, err)
			id := wire.StreamID{91, byte(len(test.name))}
			writeRelayFrame(t, source, f.limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload,
			})

			if test.allowed {
				require.NoError(t, target.SetReadDeadline(time.Now().Add(time.Second)))
				forwarded := readRelayFrame(t, target, f.limits)
				require.Equal(t, wire.FrameOpen, forwarded.Type)
				var open wire.Open
				require.NoError(t, wire.DecodeMetadata(forwarded.Payload, &open, f.limits.MaxMetadataBytes))
				require.Equal(t, wire.StreamPurposeConnectivityProbe, open.Purpose)
				require.Equal(t, "source", open.SourceAgentID)
				return
			}

			_, reset := readRelayReset(t, source, f.limits)
			require.Equal(t, wire.ErrorCodeRelayFallbackDisabled, reset.Code)
			require.Equal(t, "admission", reset.Stage)
		})
	}
}

func TestSwitchDoesNotResetRealTargetAgentBeforeOpenDelivery(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger func(*testing.T, *relayE2EFixture, *Session, *Session, wire.StreamID) *Switch
	}{
		{
			name: "prepare OPEN deadline",
			trigger: func(t *testing.T, f *relayE2EFixture, source, _ *Session, id wire.StreamID) *Switch {
				payload, err := wire.EncodeMetadata(wire.Open{
					Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target", RemainingNanos: 1,
				}, f.limits.MaxMetadataBytes)
				require.NoError(t, err)
				require.NoError(t, source.handleOpen(wire.Frame{
					Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload,
				}))
				return nil
			},
		},
		{
			name: "queued OPEN dropped",
			trigger: func(t *testing.T, f *relayE2EFixture, source, target *Session, id wire.StreamID) *Switch {
				sw := newSwitch(f.hub, source, target, id, time.Now(), f.limits)
				require.NoError(t, f.hub.attachSwitch(sw))
				sw.started.Store(true)
				payload, err := wire.EncodeMetadata(wire.Open{
					Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target", RemainingNanos: int64(time.Second),
				}, f.limits.MaxMetadataBytes)
				require.NoError(t, err)
				require.NoError(t, sw.accept(source, source.generation, wire.Frame{
					Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload,
				}))
				sw.TerminateProtocol(source, errProtocol)
				return sw
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agents := fakeAgents{
				agents: map[string]*models.Agent{"target": {AgentID: "target", Status: consts.StatusEnabled}},
				caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
			}
			f := newRelayE2EFixture(t, agents)
			targetConn, welcome := f.connect("target")
			targetAgent := agenttunnel.NewSession(targetConn, welcome.SessionGeneration, welcome.Limits, agenttunnel.SessionOptions{
				PingInterval: time.Hour, PongTimeout: time.Hour,
			})
			runCtx, cancelRun := context.WithCancel(context.Background())
			runDone := make(chan error, 1)
			go func() { runDone <- targetAgent.Run(runCtx) }()
			t.Cleanup(func() {
				targetAgent.Cancel(context.Canceled)
				cancelRun()
				select {
				case <-runDone:
				case <-time.After(time.Second):
					t.Error("target Agent Session.Run did not exit")
				}
			})
			target, err := f.hub.activeTarget(context.Background(), "target")
			require.NoError(t, err)
			source, sourceConn := liveTestSession(f.hub, "source", 100)
			t.Cleanup(func() {
				source.Cancel(errors.New("cleanup"))
				requireClosed(t, source.Done(), "source cleanup")
			})
			id := wire.StreamID{120, byte(len(tc.name))}
			sw := tc.trigger(t, f, source, target, id)
			offenderFrame := decodeCapturedFrame(t, <-sourceConn.writes)
			require.Equal(t, wire.FrameReset, offenderFrame.Type)
			require.Equal(t, uint32(1), offenderFrame.Sequence)
			var offenderReset wire.Reset
			require.NoError(t, wire.DecodeMetadata(offenderFrame.Payload, &offenderReset, f.limits.MaxMetadataBytes))
			require.Equal(t, wire.ErrorCodeRelayProtocol, offenderReset.Code)
			require.Equal(t, "protocol", offenderReset.Stage)
			if sw != nil {
				requireClosed(t, sw.Done(), "Switch.Done after undelivered OPEN")
			}
			select {
			case <-targetAgent.Done():
				t.Fatal("undelivered OPEN caused an unknown RESET to close the target Agent Session")
			case <-time.After(100 * time.Millisecond):
			}
		})
	}
}

func TestHubRelayE2ETargetEligibilityFailuresResetSource(t *testing.T) {
	cases := []struct {
		name   string
		target *models.Agent
		caps   []string
		code   string
	}{
		{name: "disabled", target: &models.Agent{AgentID: "target", Status: consts.StatusDisabled}, caps: []string{protocol.AgentCapabilityTunnelV1}, code: consts.RouteErrorTargetDisabled},
		{name: "missing capability", target: &models.Agent{AgentID: "target", Status: consts.StatusEnabled}, code: consts.RouteErrorRelayUnsupported},
		{name: "unknown", target: nil, caps: []string{protocol.AgentCapabilityTunnelV1}, code: consts.RouteErrorTargetNotFound},
		{name: "not ready", target: &models.Agent{AgentID: "target", Status: consts.StatusEnabled}, caps: []string{protocol.AgentCapabilityTunnelV1}, code: consts.RouteErrorRelayNotReady},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			agents := fakeAgents{agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}}, caps: map[string][]string{"target": tc.caps}}
			if tc.target != nil {
				agents.agents["target"] = tc.target
			}
			f := newRelayE2EFixture(t, agents)
			source, _ := f.connect("source")
			defer source.Close()
			payload, err := wire.EncodeMetadata(wire.Open{TargetAgentID: "target"}, f.limits.MaxMetadataBytes)
			require.NoError(t, err)
			writeRelayFrame(t, source, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: wire.StreamID{71}, Sequence: 1, Payload: payload})
			_, reset := readRelayReset(t, source, f.limits)
			require.Equal(t, tc.code, reset.Code)
			require.Equal(t, "target", reset.Stage)
		})
	}
}

func TestHubRelayE2ESocketDisconnectsConvergeSwitch(t *testing.T) {
	for _, disconnect := range []string{"source", "target", "simultaneous"} {
		t.Run(disconnect, func(t *testing.T) {
			agents := fakeAgents{
				agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
				caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
			}
			f := newRelayE2EFixture(t, agents)
			target, _ := f.connect("target")
			source, _ := f.connect("source")
			id := wire.StreamID{72}
			sw := openRelaySwitch(t, f, source, target, id)
			if disconnect != "target" {
				require.NoError(t, source.Close())
			}
			if disconnect != "source" {
				require.NoError(t, target.Close())
			}
			requireClosed(t, sw.Done(), "Switch.Done after socket disconnect")
			_ = source.Close()
			_ = target.Close()
		})
	}
}

func TestHubRelayE2EPeerDisconnectSendsSingleReset(t *testing.T) {
	for _, tc := range []struct {
		name      string
		failed    string
		remaining time.Duration
		sequence  uint32
	}{
		{name: "source", failed: "source", remaining: time.Second, sequence: 2},
		{name: "target", failed: "target", remaining: time.Second, sequence: 1},
		{name: "source with no deadline", failed: "source", remaining: 0, sequence: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agents := fakeAgents{
				agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
				caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
			}
			f := newRelayE2EFixture(t, agents)
			target, _ := f.connect("target")
			source, _ := f.connect("source")
			id := wire.StreamID{73}
			sw := openRelaySwitchWithRemaining(t, f, source, target, id, tc.remaining)
			failed, peer := source, target
			if tc.failed == "target" {
				failed, peer = target, source
			}
			require.NoError(t, failed.Close())
			frame, reset := readRelayReset(t, peer, f.limits)
			require.Equal(t, tc.sequence, frame.Sequence)
			require.Equal(t, wire.ErrorCodeSessionClosed, reset.Code)
			require.Equal(t, "peer", reset.Stage)
			require.False(t, reset.Committed)
			requireClosed(t, sw.Done(), "Switch.Done after peer RESET")
			require.Equal(t, int32(1), sw.finalizations.Load())
			_ = source.Close()
			_ = target.Close()
		})
	}
}

func TestHubRelayE2EBoundProtocolErrorTerminatesOffenderAndPeer(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	target, _ := f.connect("target")
	source, _ := f.connect("source")
	id := wire.StreamID{74}
	sw := openRelaySwitch(t, f, source, target, id)
	readyPayload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: 1024}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: readyPayload})
	require.Equal(t, wire.FrameReady, readRelayFrame(t, source, f.limits).Type)
	writeRelayFrame(t, source, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2})
	offenderFrame, offenderReset := readRelayReset(t, source, f.limits)
	require.Equal(t, uint32(2), offenderFrame.Sequence)
	require.Equal(t, wire.ErrorCodeRelayProtocol, offenderReset.Code)
	require.Equal(t, "protocol", offenderReset.Stage)
	require.False(t, offenderReset.Committed)
	peerFrame, peerReset := readRelayReset(t, target, f.limits)
	require.Equal(t, uint32(2), peerFrame.Sequence)
	require.Equal(t, wire.ErrorCodeSessionClosed, peerReset.Code)
	require.Equal(t, "peer", peerReset.Stage)
	require.False(t, peerReset.Committed)
	requireClosed(t, sw.Done(), "Switch.Done after protocol RESETs")
	_ = source.Close()
	_ = target.Close()
}

func TestHubRelayE2EPeerResetCarriesCommittedStateAndSequence(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{"source": {AgentID: "source", Status: consts.StatusEnabled}, "target": {AgentID: "target", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
	}
	f := newRelayE2EFixture(t, agents)
	target, _ := f.connect("target")
	source, _ := f.connect("source")
	id := wire.StreamID{75}
	sw := openRelaySwitch(t, f, source, target, id)
	readyPayload, err := wire.EncodeMetadata(wire.Ready{RequestWindow: 1024}, f.limits.MaxMetadataBytes)
	require.NoError(t, err)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1, Payload: readyPayload})
	require.Equal(t, wire.FrameReady, readRelayFrame(t, source, f.limits).Type)
	writeRelayFrame(t, target, f.limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: id, Sequence: 2})
	require.Equal(t, wire.FrameCommitted, readRelayFrame(t, source, f.limits).Type)
	require.NoError(t, target.Close())
	frame, reset := readRelayReset(t, source, f.limits)
	require.Equal(t, uint32(3), frame.Sequence)
	require.Equal(t, wire.ErrorCodeSessionClosed, reset.Code)
	require.Equal(t, "peer", reset.Stage)
	require.True(t, reset.Committed)
	requireClosed(t, sw.Done(), "Switch.Done after committed peer RESET")
	_ = source.Close()
}

func TestHubRejectsDisabledAndUnsupportedTarget(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{
			"disabled": {AgentID: "disabled", Status: consts.StatusDisabled},
			"legacy":   {AgentID: "legacy", Status: consts.StatusEnabled},
		},
		caps: map[string][]string{"legacy": {"other"}},
	}
	h := NewHub(HubOptions{InstanceID: "master-a", Agents: agents, Admission: &AdmissionGate{}, Limits: testLimits(), Logger: zap.NewNop()})
	require.ErrorIs(t, h.validateTarget(context.Background(), "disabled"), errTargetDisabled)
	require.ErrorIs(t, h.validateTarget(context.Background(), "legacy"), errTargetCapability)
	require.Error(t, h.validateTarget(context.Background(), ""))
}

func TestHubRegistersActiveCandidateAndDrainingGenerationSafely(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), DrainTimeout: time.Second, Logger: zap.NewNop()})
	t.Cleanup(func() { require.NoError(t, h.Close(context.Background())) })
	first := newTestSession(h, "agent-a", 1)
	second := newTestSession(h, "agent-a", 2)
	require.NoError(t, h.register(first))
	require.NoError(t, h.register(second))

	h.mu.RLock()
	sessions := h.sessions["agent-a"]
	require.Same(t, first, sessions.Active)
	require.Same(t, second, sessions.Candidate)
	h.mu.RUnlock()

	require.NoError(t, h.promote(second))
	h.mu.RLock()
	require.Same(t, second, sessions.Active)
	require.Same(t, first, sessions.Draining[1])
	h.mu.RUnlock()

	h.unregister(first)
	h.mu.RLock()
	require.Same(t, second, sessions.Active, "old cleanup must not remove the replacement")
	h.mu.RUnlock()
}

func TestHubSnapshotDrainDisconnectAndShutdownAreIdempotent(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Logger: zap.NewNop()})
	s := newTestSession(h, "agent-a", 7)
	require.NoError(t, h.register(s))
	snapshot, ok := h.Snapshot("agent-a")
	require.True(t, ok)
	require.Equal(t, uint64(7), snapshot.Generation)
	require.Equal(t, "active", snapshot.Availability)
	require.NoError(t, h.Drain("agent-a", 7))
	require.NoError(t, h.Disconnect("agent-a", 7, errors.New("test")))
	require.NoError(t, h.Close(context.Background()))
	require.NoError(t, h.Close(context.Background()))
	select {
	case <-h.Done():
	default:
		t.Fatal("hub Done was not closed")
	}
}

func TestCancelImmediatelyRemovesSessionFromActiveTargetAdmission(t *testing.T) {
	agents := fakeAgents{agents: map[string]*models.Agent{"target": {AgentID: "target", Status: consts.StatusEnabled}}, caps: map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}}}
	h := NewHub(HubOptions{InstanceID: "master-a", Agents: agents, Limits: testLimits()})
	target := newTestSession(h, "target", 1)
	require.NoError(t, h.register(target))
	target.Cancel(errors.New("closing"))
	_, err := h.activeTarget(context.Background(), "target")
	require.ErrorIs(t, err, errSessionNotFound)
	requireClosed(t, target.Done(), "target Session.Done")
	require.NoError(t, h.Close(context.Background()))
}

func TestHubDrainAllStopsAdmissionWithoutCommittedStreams(t *testing.T) {
	h := NewHub(HubOptions{})
	active := newTestSession(h, "agent-a", 1)
	active.accepting.Store(true)
	candidate := newTestSession(h, "agent-a", 2)
	h.sessions["agent-a"] = &AgentSessions{
		Active: active, Candidate: candidate, Draining: map[uint64]*Session{},
	}

	require.NoError(t, h.DrainAll(context.Background()))
	require.False(t, active.accepting.Load())
	requireClosed(t, candidate.Done(), "candidate Session.Done")
	require.NoError(t, h.Close(context.Background()))
}

func TestHubDrainAllWaitsForCommittedSwitch(t *testing.T) {
	h := NewHub(HubOptions{})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	source.accepting.Store(true)
	target.accepting.Store(true)
	h.sessions["source"] = &AgentSessions{Active: source, Draining: map[uint64]*Session{}}
	h.sessions["target"] = &AgentSessions{Active: target, Draining: map[uint64]*Session{}}
	sw := newTestSwitch(h, source, target, wire.StreamID{1})
	require.NoError(t, h.addSwitch(sw))
	drainStarted := make(chan struct{})
	h.afterDrainAdmission = func() { close(drainStarted) }

	done := make(chan error, 1)
	go func() { done <- h.DrainAll(context.Background()) }()
	<-drainStarted
	select {
	case err := <-done:
		t.Fatalf("DrainAll returned before committed switch ended: %v", err)
	default:
	}
	h.removeSwitch(sw)
	require.NoError(t, <-done)
	require.NoError(t, h.Close(context.Background()))
}

func TestHubDrainAllHonorsCallerDeadline(t *testing.T) {
	h := NewHub(HubOptions{})
	source := newTestSession(h, "source", 1)
	target := newTestSession(h, "target", 2)
	sw := newTestSwitch(h, source, target, wire.StreamID{1})
	require.NoError(t, h.addSwitch(sw))
	ctx, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("drain deadline")
	cancel(cause)
	require.ErrorIs(t, h.DrainAll(ctx), cause)
	h.removeSwitch(sw)
	require.NoError(t, h.Close(context.Background()))
}

func TestHubLifecycleDrainsRealActiveCommittedAndDrainingSessions(t *testing.T) {
	agents := fakeAgents{
		agents: map[string]*models.Agent{
			"source": {AgentID: "source", Status: consts.StatusEnabled},
			"target": {AgentID: "target", Status: consts.StatusEnabled},
		},
		caps: map[string][]string{
			"source": {protocol.AgentCapabilityTunnelV1},
			"target": {protocol.AgentCapabilityTunnelV1},
		},
	}
	f := newRelayE2EFixture(t, agents)
	oldSourceConn, _ := f.connectDesired("source", 1)
	targetConn, _ := f.connectDesired("target", 1)
	defer oldSourceConn.Close()
	defer targetConn.Close()

	f.hub.mu.RLock()
	oldSource := f.hub.sessions["source"].Active
	target := f.hub.sessions["target"].Active
	f.hub.mu.RUnlock()
	sw := newTestSwitch(f.hub, oldSource, target, wire.StreamID{9})
	require.NoError(t, f.hub.attachSwitch(sw))

	replacementConn, _ := f.connectDesired("source", 2)
	defer replacementConn.Close()
	counts := f.hub.ResourceCounts()
	require.Equal(t, int64(2), counts.RelayActive)
	require.Equal(t, int64(1), counts.RelayDraining)
	require.Equal(t, int64(1), counts.RelayStreams)
	require.Equal(t, int64(3), counts.RelaySockets)

	drainStarted := make(chan struct{})
	f.hub.afterDrainAdmission = func() { close(drainStarted) }
	drainDone := make(chan error, 1)
	go func() { drainDone <- f.hub.DrainAll(context.Background()) }()
	<-drainStarted
	select {
	case err := <-drainDone:
		t.Fatalf("DrainAll returned before committed switch cleanup: %v", err)
	default:
	}
	f.hub.removeSwitch(sw)
	require.NoError(t, <-drainDone)
	require.NoError(t, f.hub.Close(context.Background()))
	require.Equal(t, app.ResourceCounts{}, f.hub.ResourceCounts())
}
