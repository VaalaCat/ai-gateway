package tunnel

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestDirectIngressAuthenticatesAndCompletesHandshake(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	fixture.targetLimits = testLimits(2)
	ingress, server := fixture.start(t)

	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(4)),
	)
	require.NoError(t, err)
	require.Equal(t, SessionDirectionDirectOutgoing, session.opts.Direction)
	require.Equal(t, fixture.targetLimits, session.limits)
	require.Eventually(t, func() bool { return ingress.connectionCount() == 1 }, time.Second, time.Millisecond)

	require.NoError(t, session.Close(t.Context()))
	require.Eventually(t, func() bool { return ingress.connectionCount() == 0 }, time.Second, time.Millisecond)
	require.NoError(t, ingress.Close(t.Context()))
	receiveWithDirectTimeout(t, ingress.Done())
}

func TestDirectIngressRejectsAuthenticationBeforeUpgrade(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	_, server := fixture.start(t)
	expires := fixture.now.Add(time.Hour)
	other := newDirectIngressFixture(t)
	tests := []struct {
		name   string
		ticket agentauth.ForwardTicket
		status int
	}{
		{name: "missing", status: http.StatusUnauthorized},
		{name: "malformed", ticket: "not-a-jwt", status: http.StatusUnauthorized},
		{name: "expired at exact boundary", ticket: fixture.sign("source-a", "agent-forward", expires), status: http.StatusUnauthorized},
		{name: "bad audience", ticket: fixture.sign("source-a", "agent-relay", fixture.now.Add(2*time.Hour)), status: http.StatusUnauthorized},
		{name: "bad signature", ticket: other.sign("source-a", "agent-forward", fixture.now.Add(2*time.Hour)), status: http.StatusUnauthorized},
		{name: "empty source", ticket: fixture.sign("", "agent-forward", fixture.now.Add(2*time.Hour)), status: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture.now = expires
			request, err := http.NewRequest(http.MethodGet, server.URL+DirectTunnelPath+"?target_agent_id=target-a", nil)
			require.NoError(t, err)
			if test.ticket != "" {
				request.Header.Set("Authorization", "Bearer "+string(test.ticket))
			}
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, test.status, response.StatusCode)
			require.NotEqual(t, http.StatusSwitchingProtocols, response.StatusCode)
		})
	}
}

func TestDirectLogsIngressWebSocketUpgradeFailureOnce(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	fixture := newDirectIngressFixture(t)
	fixture.logger = zap.New(core)
	fixture.suppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{})
	_, server := fixture.start(t)
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	httpEndpoint, err := url.Parse(endpoint)
	require.NoError(t, err)
	httpEndpoint.Scheme = "http"
	query := httpEndpoint.Query()
	query.Set("credential", "query-secret")
	httpEndpoint.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, httpEndpoint.String(), nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+string(fixture.ticket))
	response, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusBadRequest, response.StatusCode)
	require.Eventually(t, func() bool {
		return observed.FilterMessage("direct ingress rejected").Len() == 1
	}, time.Second, time.Millisecond)
	encoded := strings.ToLower(fmt.Sprint(observed.FilterMessage("direct ingress rejected").All()[0].ContextMap()))
	require.NotContains(t, encoded, strings.ToLower(string(fixture.ticket)))
	require.NotContains(t, encoded, "query-secret")
	require.NotContains(t, encoded, "credential")
}

func TestDirectIngressRejectsTargetSourceAndDrainBeforeUpgrade(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, server := fixture.start(t)
	ticket := fixture.sign("source-a", "agent-forward", fixture.now.Add(time.Hour))
	tests := []struct {
		name   string
		target string
		mutate func()
		status int
	}{
		{name: "target mismatch", target: "target-b", status: http.StatusForbidden},
		{name: "target missing", target: "target-a", mutate: func() { fixture.target = nil }, status: http.StatusForbidden},
		{name: "target record mismatch", target: "target-a", mutate: func() { fixture.target.AgentID = "target-other" }, status: http.StatusForbidden},
		{name: "target disabled", target: "target-a", mutate: func() { fixture.target.Status = consts.StatusDisabled }, status: http.StatusForbidden},
		{name: "source missing", target: "target-a", mutate: func() { fixture.source = nil }, status: http.StatusForbidden},
		{name: "source disabled", target: "target-a", mutate: func() { fixture.source.Status = consts.StatusDisabled }, status: http.StatusForbidden},
		{name: "draining", target: "target-a", mutate: func() {
			require.NoError(t, ingress.Drain(t.Context()))
		}, status: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture.source = &models.Agent{AgentID: "source-a", Status: consts.StatusEnabled}
			fixture.target = &models.Agent{AgentID: "target-a", Status: consts.StatusEnabled}
			if test.mutate != nil {
				test.mutate()
			}
			request, err := http.NewRequest(http.MethodGet, server.URL+DirectTunnelPath+"?target_agent_id="+test.target, nil)
			require.NoError(t, err)
			request.Header.Set("Authorization", "Bearer "+string(ticket))
			response, err := http.DefaultClient.Do(request)
			require.NoError(t, err)
			defer response.Body.Close()
			require.Equal(t, test.status, response.StatusCode)
		})
		if test.name == "draining" {
			break
		}
	}
}

func TestDirectIngressReplacementSameSource(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, server := fixture.start(t)
	first, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ingress.connectionCount() == 1 }, time.Second, time.Millisecond)

	// A second Confirmed handshake for the same Source replaces the active
	// session instead of being rejected.
	second, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	require.NotNil(t, second)

	// The prior active drains (it had no admitted streams) and the ingress
	// converges to a single active session for the Source.
	require.Eventually(t, func() bool {
		snapshot := ingress.Snapshot()
		return snapshot.Active == 1 && snapshot.Draining == 0
	}, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, second.Close(t.Context()))
	_ = first.Close(t.Context())
}

func TestDirectSettingsIngressLimitsChangeDrainsOldSession(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, server := fixture.start(t)
	first, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ingress.Snapshot().Active == 1 }, time.Second, time.Millisecond)
	updated := testLimits(2)
	updated.MaxDataBytes /= 2
	ingress.ApplyRuntimeSettings(DirectRuntimeSettings{
		Limits: updated, MaxSessions: 2, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	})
	require.Eventually(t, func() bool {
		snapshot := ingress.Snapshot()
		return snapshot.Active == 0 && snapshot.Draining == 0
	}, 2*time.Second, time.Millisecond)
	require.NoError(t, first.Close(t.Context()))
}

func TestDirectIngressDifferentSourceIsolated(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	fixture.findAgentByID = func(id string) *models.Agent {
		switch id {
		case "source-a", "source-b", "target-a":
			return &models.Agent{AgentID: id, Status: consts.StatusEnabled}
		}
		return nil
	}
	ingress, server := fixture.start(t)
	ticketB := fixture.sign("source-b", "agent-forward", fixture.now.Add(time.Hour))

	firstA, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	secondB, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequestForSource(server.URL, "source-b", ticketB, testLimits(2)),
	)
	require.NoError(t, err)

	// Distinct Sources hold independent active sessions.
	require.Eventually(t, func() bool { return ingress.Snapshot().Active == 2 }, 2*time.Second, 5*time.Millisecond)

	require.NoError(t, firstA.Close(t.Context()))
	require.NoError(t, secondB.Close(t.Context()))
}

func TestDirectIngressCapacityRejectionWhenFull(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	fixture.findAgentByID = func(id string) *models.Agent {
		switch id {
		case "source-a", "source-b", "target-a":
			return &models.Agent{AgentID: id, Status: consts.StatusEnabled}
		}
		return nil
	}
	fixture.maxSessions = 1
	ingress, server := fixture.start(t)

	// Hold one in-flight handshake for source-a (candidate) so the single slot
	// is occupied without any idle active to evict.
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	pending, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}})
	require.NoError(t, err)
	defer pending.Close()
	require.Eventually(t, func() bool { return ingress.connectionCount() == 1 }, time.Second, time.Millisecond)

	// A different Source at capacity, with no idle active to evict, is rejected.
	ticketB := fixture.sign("source-b", "agent-forward", fixture.now.Add(time.Hour))
	rejected, response, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer " + string(ticketB)}})
	require.Error(t, err)
	require.Nil(t, rejected)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	_ = response.Body.Close()
}

func TestDirectIngressReplacementRejectsBeforeUpgradeWhenDrainingFull(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	fixture.maxSessions = 1
	ingress, _ := fixture.start(t)
	settings := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	}
	active := &directIngressConnection{sourceAgentID: "source-a", session: newSession(newMemorySessionConn(), 1, settings.Limits, SessionOptions{Direction: SessionDirectionRelay})}
	draining := &directIngressConnection{sourceAgentID: "source-old", session: newSession(newMemorySessionConn(), 2, settings.Limits, SessionOptions{Direction: SessionDirectionRelay})}
	t.Cleanup(func() {
		ingress.releaseSource(active)
		ingress.releaseSource(draining)
		active.session.Close(context.Background())
		draining.session.Close(context.Background())
	})
	ingress.mu.Lock()
	ingress.sources[active.sourceAgentID] = &directSourceSlot{active: active}
	ingress.draining[draining] = struct{}{}
	ingress.mu.Unlock()

	tracked, status, code, _ := ingress.reserveSource(active.sourceAgentID, settings)
	require.Nil(t, tracked)
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, directReasonCapacity, code)
	snapshot := ingress.Snapshot()
	require.Equal(t, 1, snapshot.Active)
	require.Equal(t, 1, snapshot.Draining)
	require.Equal(t, 2, snapshot.Sockets)
}

func TestDirectIngressCloseCancelsPendingHandshakeAndWaitsDone(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, server := fixture.start(t)
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	header := http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return ingress.connectionCount() == 1 }, time.Second, time.Millisecond)

	require.NoError(t, ingress.Close(t.Context()))
	receiveWithDirectTimeout(t, ingress.Done())
	require.Zero(t, ingress.connectionCount())
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	require.NoError(t, ingress.Close(t.Context()))
}

func TestDirectIngressRejectsInvalidHelloAndHandshakeTimeout(t *testing.T) {
	tests := []struct {
		name string
		send func(*websocket.Conn) error
	}{
		{name: "binary", send: func(conn *websocket.Conn) error { return conn.WriteMessage(websocket.BinaryMessage, []byte(`{}`)) }},
		{name: "malformed", send: func(conn *websocket.Conn) error {
			return conn.WriteMessage(websocket.TextMessage, []byte(`{"protocol_version":`))
		}},
		{name: "wrong version", send: func(conn *websocket.Conn) error {
			return conn.WriteJSON(wire.DirectHello{ProtocolVersion: wire.ProtocolVersion + 1, Limits: testLimits(1)})
		}},
		{name: "timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDirectIngressFixture(t)
			fixture.handshakeTimeout = 20 * time.Millisecond
			ingress, server := fixture.start(t)
			endpoint, err := DirectWebSocketURL(server.URL, "target-a")
			require.NoError(t, err)
			header := http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}}
			conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
			require.NoError(t, err)
			if test.send != nil {
				require.NoError(t, test.send(conn))
			}
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
			_, _, err = conn.ReadMessage()
			require.Error(t, err)
			require.Eventually(t, func() bool { return ingress.connectionCount() == 0 }, time.Second, time.Millisecond)
		})
	}
}

func TestDirectIngressHandshakeUsesOneTotalDeadline(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	fixture.handshakeTimeout = time.Second
	nowCalls := make(chan struct{}, 8)
	fixture.ingressNow = func() time.Time {
		select {
		case nowCalls <- struct{}{}:
		default:
		}
		return time.Now()
	}
	_, server := fixture.start(t)
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	header := http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	require.NoError(t, err)
	defer conn.Close()

	receiveWithDirectTimeout(t, nowCalls) // ticket expiry check
	receiveWithDirectTimeout(t, nowCalls) // initial HELLO deadline
	waitDirectHandshakePhase(t, 600*time.Millisecond)
	require.NoError(t, conn.WriteJSON(wire.DirectHello{ProtocolVersion: wire.ProtocolVersion, Limits: testLimits(1)}))
	var ready wire.DirectReady
	require.NoError(t, conn.ReadJSON(&ready))
	require.NotZero(t, ready.SessionGeneration)

	waitDirectHandshakePhase(t, 600*time.Millisecond)
	require.NoError(t, conn.WriteJSON(wire.DirectAccepted{SessionGeneration: ready.SessionGeneration}))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err, "handshake exceeded its single total deadline")
}

func TestDirectIngressApplyRuntimeSettingsDoesNotHoldOwnerLockAcrossNow(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	fixture.ingressNow = func() time.Time {
		once.Do(func() { close(entered) })
		<-release
		return time.Now()
	}
	ingress, _ := fixture.start(t)
	defer unblock()

	applyDone := make(chan struct{})
	go func() {
		defer close(applyDone)
		ingress.ApplyRuntimeSettings(DirectRuntimeSettings{
			Limits: testLimits(1), MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second,
		})
	}()
	receiveWithDirectTimeout(t, entered)
	cancelDone := make(chan struct{})
	go func() { ingress.Cancel(); close(cancelDone) }()
	receiveWithDirectTimeout(t, cancelDone)
	unblock()
	receiveWithDirectTimeout(t, applyDone)
}

func TestDirectSettingsIngressApplyWinsBeforeStaleSessionInstall(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, _ := fixture.start(t)
	settingsA := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 2, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	}
	ingress.ApplyRuntimeSettings(settingsA)
	tracked, status, code, _ := ingress.reserveSource("source-a", settingsA)
	require.NotNil(t, tracked, "reserve failed: status=%d code=%s", status, code)
	tracked.limitsFingerprint = limitsFingerprint(settingsA.Limits)
	dialer := newBlockingDirectDialer()
	dialer.t = t
	session := dialer.buildSession()
	defer session.Close(context.Background())
	defer ingress.releaseSource(tracked)

	beforeLock := make(chan struct{})
	allowLock := make(chan struct{})
	ingress.beforeInstallSessionLock = func() {
		close(beforeLock)
		<-allowLock
	}
	installed := make(chan bool, 1)
	go func() {
		ok, _ := ingress.installSession(tracked, session, time.Now(), settingsA)
		installed <- ok
	}()
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.Limits.MaxDataBytes /= 2
	ingress.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	require.False(t, <-installed, "session sampled before settings B must not install after B is published")
	require.Zero(t, ingress.Snapshot().Active)
}

func TestDirectSettingsIngressSweepUsesPublishedIdleTimeout(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, _ := fixture.start(t)
	settingsA := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 2, IdleTimeout: time.Second, DrainTimeout: time.Second,
	}
	ingress.ApplyRuntimeSettings(settingsA)
	dialer := newBlockingDirectDialer()
	dialer.t = t
	session := dialer.buildSession()
	defer session.Close(context.Background())
	tracked := &directIngressConnection{
		sourceAgentID: "source-a", session: session, acceptedAt: fixture.now.Add(-2 * time.Second),
	}
	ingress.mu.Lock()
	ingress.sources[tracked.sourceAgentID] = &directSourceSlot{active: tracked}
	ingress.mu.Unlock()

	beforeLock := make(chan struct{})
	allowLock := make(chan struct{})
	sweepDone := make(chan struct{})
	var beforeOnce, doneOnce sync.Once
	beforeSweepLock := func() {
		beforeOnce.Do(func() {
			close(beforeLock)
			<-allowLock
		})
	}
	afterSweep := func() { doneOnce.Do(func() { close(sweepDone) }) }
	ingress.beforeSweepLock.Store(&beforeSweepLock)
	ingress.afterSweep.Store(&afterSweep)
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.IdleTimeout = time.Hour
	ingress.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	receiveWithDirectTimeout(t, sweepDone)
	require.Equal(t, 1, ingress.Snapshot().Active, "sweep sampled before settings B must honor B after taking owner lock")
}

func TestDirectIngressIdleTimeoutStartsAfterLastStream(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	clock := newPoolTestClock()
	fixture.ingressNow = clock.Now
	ingress, _ := fixture.start(t)
	settings := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 2, IdleTimeout: time.Second, DrainTimeout: time.Second,
	}
	ingress.ApplyRuntimeSettings(settings)
	tracked, status, code, _ := ingress.reserveSource("source-a", settings)
	require.NotNil(t, tracked, "reserve failed: status=%d code=%s", status, code)
	tracked.limitsFingerprint = limitsFingerprint(settings.Limits)
	session := newSession(newMemorySessionConn(), 1, settings.Limits, SessionOptions{Direction: SessionDirectionRelay})
	installed, _ := ingress.installSession(tracked, session, clock.Now(), settings)
	require.True(t, installed)
	t.Cleanup(func() { ingress.releaseSource(tracked) })

	stream := &Stream{session: session, id: testStreamID(1), generation: session.Generation()}
	require.NoError(t, session.admitStream(stream))
	clock.Advance(2 * time.Second)
	ingress.ApplyRuntimeSettings(settings)
	require.Equal(t, 1, ingress.Snapshot().Active)

	session.removeStream(stream)
	clock.Advance(time.Second - time.Nanosecond)
	ingress.ApplyRuntimeSettings(settings)
	require.Equal(t, 1, ingress.Snapshot().Active)
	clock.Advance(time.Nanosecond)
	ingress.ApplyRuntimeSettings(settings)
	require.Zero(t, ingress.Snapshot().Active)
}

func TestDirectIngressIdleLRUEvictsLongestActuallyIdleSession(t *testing.T) {
	clock := newPoolTestClock()
	fixture := newDirectIngressFixture(t)
	fixture.ingressNow = clock.Now
	fixture.maxSessions = 2
	ingress, _ := fixture.start(t)
	settings := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 2, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	}
	ingress.ApplyRuntimeSettings(settings)

	firstSession := newSession(newMemorySessionConn(), 1, settings.Limits, SessionOptions{
		Direction: SessionDirectionDirectIncoming, Now: clock.Now,
	})
	firstStream := &Stream{session: firstSession, id: testStreamID(1), generation: firstSession.Generation()}
	require.NoError(t, firstSession.admitStream(firstStream))
	first := &directIngressConnection{
		sourceAgentID: "source-a", session: firstSession, acceptedAt: clock.Now(),
		limitsFingerprint: limitsFingerprint(settings.Limits),
	}
	ingress.mu.Lock()
	ingress.sources[first.sourceAgentID] = &directSourceSlot{active: first}
	ingress.mu.Unlock()

	clock.Advance(time.Second)
	secondSession := newSession(newMemorySessionConn(), 2, settings.Limits, SessionOptions{
		Direction: SessionDirectionDirectIncoming, Now: clock.Now,
	})
	second := &directIngressConnection{
		sourceAgentID: "source-b", session: secondSession, acceptedAt: clock.Now(),
		limitsFingerprint: limitsFingerprint(settings.Limits),
	}
	ingress.mu.Lock()
	ingress.sources[second.sourceAgentID] = &directSourceSlot{active: second}
	ingress.mu.Unlock()

	clock.Advance(time.Second)
	firstSession.removeStream(firstStream)
	candidate, status, code, _ := ingress.reserveSource("source-c", settings)
	require.NotNil(t, candidate, "reserve failed: status=%d code=%s", status, code)

	ingress.mu.Lock()
	firstSlot := ingress.sources[first.sourceAgentID]
	secondSlot := ingress.sources[second.sourceAgentID]
	ingress.mu.Unlock()
	require.NotNil(t, firstSlot)
	require.Same(t, first, firstSlot.active, "the session that only just became idle must be retained")
	require.True(t, secondSlot == nil || secondSlot.active == nil, "the longest actually idle session must be evicted")

	ingress.releaseSource(candidate)
	ingress.releaseSource(first)
	ingress.releaseSource(second)
	require.NoError(t, firstSession.Close(t.Context()))
	require.NoError(t, secondSession.Close(t.Context()))
}

func TestDirectSettingsIngressReconcileBoundsDrainingSessions(t *testing.T) {
	for _, test := range []struct {
		name          string
		mutate        func(*DirectRuntimeSettings)
		wantAccepting int
	}{
		{
			name: "max sessions", wantAccepting: 1,
			mutate: func(settings *DirectRuntimeSettings) { settings.MaxSessions = 1 },
		},
		{name: "limits", mutate: func(settings *DirectRuntimeSettings) {
			settings.MaxSessions = 1
			settings.Limits.MaxDataBytes /= 2
		}, wantAccepting: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDirectIngressFixture(t)
			ingress, _ := fixture.start(t)
			initial := DirectRuntimeSettings{
				Limits: testLimits(2), MaxSessions: 3, IdleTimeout: time.Hour, DrainTimeout: time.Second,
			}
			ingress.runtimeSettings.Store(&initial)
			trackedSessions := make([]*directIngressConnection, 0, 3)
			for index, source := range []string{"source-a", "source-b", "source-c"} {
				session := newSession(newMemorySessionConn(), uint64(index+1), initial.Limits, SessionOptions{Direction: SessionDirectionRelay})
				stream := &Stream{session: session, id: testStreamID(byte(index + 1)), generation: session.Generation()}
				require.NoError(t, session.admitStream(stream))
				tracked := &directIngressConnection{
					sourceAgentID: source, session: session, acceptedAt: fixture.now,
					limitsFingerprint: limitsFingerprint(initial.Limits),
				}
				trackedSessions = append(trackedSessions, tracked)
				ingress.mu.Lock()
				ingress.sources[source] = &directSourceSlot{active: tracked}
				ingress.mu.Unlock()
				t.Cleanup(func() {
					session.removeStream(stream)
					ingress.releaseSource(tracked)
					session.Close(context.Background())
				})
			}

			updated := initial
			test.mutate(&updated)
			ingress.ApplyRuntimeSettings(updated)
			require.Len(t, trackedSessions, 3)
			snapshot := ingress.Snapshot()
			require.Equal(t, 2, snapshot.Active)
			require.Equal(t, 1, snapshot.Draining)
			require.Equal(t, 3, snapshot.Streams)
			require.Equal(t, 3, snapshot.Sockets)
			accepting := 0
			for _, tracked := range trackedSessions {
				if tracked.session.acceptsNew() {
					accepting++
				}
			}
			require.Equal(t, test.wantAccepting, accepting,
				"incoming sessions waiting for a draining slot must stop new OPEN admission")
		})
	}
}

func TestDirectSettingsIngressReconcileCountsReplacementCandidatesByAdmission(t *testing.T) {
	type slotShape struct {
		active    bool
		candidate bool
		age       time.Duration
	}
	tests := []struct {
		name          string
		slots         map[string]slotShape
		wantAccepting map[string]bool
	}{
		{
			name: "all active slots have replacement candidates",
			slots: map[string]slotShape{
				"source-a": {active: true, candidate: true, age: 2 * time.Second},
				"source-b": {active: true, candidate: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"source-a": false, "source-b": true},
		},
		{
			name: "replacement candidate does not hide oldest active",
			slots: map[string]slotShape{
				"source-a": {active: true, candidate: true, age: 2 * time.Second},
				"source-b": {active: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"source-a": false, "source-b": true},
		},
		{
			name: "candidate only slot retains projected capacity",
			slots: map[string]slotShape{
				"source-a": {candidate: true, age: 2 * time.Second},
				"source-b": {active: true, candidate: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"source-b": false},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			settings := normalizeDirectRuntimeSettings(DirectRuntimeSettings{
				Limits: testLimits(2), MaxSessions: 1, DrainTimeout: time.Second,
			})
			ingress := &DirectTunnelIngress{
				sources:  make(map[string]*directSourceSlot),
				draining: make(map[*directIngressConnection]struct{}),
			}
			ingress.draining[&directIngressConnection{}] = struct{}{}
			activeSessions := make(map[string]*Session)
			for source, shape := range test.slots {
				slot := &directSourceSlot{}
				if shape.active {
					session := newSessionValue(nil, uint64(len(activeSessions)+1), settings.Limits, SessionOptions{})
					require.True(t, session.acquireAdmission())
					activeSessions[source] = session
					slot.active = &directIngressConnection{
						sourceAgentID: source, session: session, acceptedAt: now.Add(-shape.age),
						limitsFingerprint: limitsFingerprint(settings.Limits),
					}
				}
				if shape.candidate {
					slot.candidate = &directIngressConnection{sourceAgentID: source}
				}
				ingress.sources[source] = slot
			}
			defer func() {
				for _, session := range activeSessions {
					session.releaseAdmission()
				}
			}()

			require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
			for source, want := range test.wantAccepting {
				require.Equal(t, want, activeSessions[source].acceptsNew(), source)
			}
			require.Equal(t, 1, ingress.projectedOccupiedSourceCountLocked())
			if slot := ingress.sources["source-a"]; slot != nil && !test.slots["source-a"].active {
				require.NotNil(t, slot.candidate, "candidate-only source must remain reserved")
			}
		})
	}
}

func TestDirectSettingsIngressReconcileReactivatesOnlyCapacityPendingAfterCandidateFailure(t *testing.T) {
	tests := []struct {
		name          string
		limitsChanged bool
		idleExpired   bool
		wantRecovered bool
	}{
		{name: "capacity only", wantRecovered: true},
		{name: "limits still require drain", limitsChanged: true},
		{name: "idle timeout still requires drain", idleExpired: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			settings := normalizeDirectRuntimeSettings(DirectRuntimeSettings{
				Limits: testLimits(2), MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second,
			})
			if test.idleExpired {
				settings.IdleTimeout = time.Second
			}
			active := newRunningDirectReconcileSession(1, settings.Limits, now)
			if test.idleExpired {
				active.setActivityNow(func() time.Time { return now }, now.Add(-2*time.Second))
			}
			fingerprint := limitsFingerprint(settings.Limits)
			if test.limitsChanged {
				oldLimits := settings.Limits
				oldLimits.MaxDataBytes /= 2
				fingerprint = limitsFingerprint(oldLimits)
			}
			candidate := &directIngressConnection{sourceAgentID: "source-a"}
			tracked := &directIngressConnection{
				sourceAgentID: "source-b", session: active, acceptedAt: now,
				limitsFingerprint: fingerprint,
			}
			blockingDrain := &directIngressConnection{sourceAgentID: "source-old"}
			ingress := &DirectTunnelIngress{
				sources: map[string]*directSourceSlot{
					"source-a": {candidate: candidate},
					"source-b": {active: tracked},
				},
				draining: map[*directIngressConnection]struct{}{blockingDrain: {}},
			}

			require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
			require.True(t, tracked.drainPending)
			require.False(t, active.acceptsNew())

			ingress.releaseSource(candidate) // candidate completion is rejected as stale at the new cap
			require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
			require.Equal(t, test.wantRecovered, active.acceptsNew())
			require.Equal(t, !test.wantRecovered, tracked.drainPending)

			if test.wantRecovered {
				delete(ingress.draining, blockingDrain)
				require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
				require.Same(t, tracked, ingress.sources["source-b"].active)
				require.True(t, active.acceptsNew())
				require.NotContains(t, ingress.draining, tracked)
			}
		})
	}
}

func TestDirectSettingsIngressReconcileWaitsForRunContextBeforeReactivation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	settings := normalizeDirectRuntimeSettings(DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	})
	active := newSessionValue(nil, 1, settings.Limits, SessionOptions{Now: func() time.Time { return now }})
	active.state = sessionStateRunning // Run publishes ctx in its next stateMu critical section.
	tracked := &directIngressConnection{
		sourceAgentID: "source-b", session: active, acceptedAt: now,
		limitsFingerprint: limitsFingerprint(settings.Limits),
	}
	candidate := &directIngressConnection{sourceAgentID: "source-a"}
	blockingDrain := &directIngressConnection{sourceAgentID: "source-old"}
	ingress := &DirectTunnelIngress{
		sources: map[string]*directSourceSlot{
			"source-a": {candidate: candidate},
			"source-b": {active: tracked},
		},
		draining: map[*directIngressConnection]struct{}{blockingDrain: {}},
	}

	require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
	require.True(t, tracked.drainPending)
	ingress.releaseSource(candidate)
	require.NotPanics(t, func() {
		require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
	})
	require.True(t, tracked.drainPending, "admission must stay pending until Run publishes its context")
	require.False(t, active.acceptsNew())

	active.stateMu.Lock()
	active.ctx = context.Background()
	active.stateMu.Unlock()
	require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
	require.False(t, tracked.drainPending)
	require.True(t, active.acceptsNew())

	delete(ingress.draining, blockingDrain)
	require.Empty(t, ingress.reconcileRuntimeLocked(now, settings))
	require.Same(t, tracked, ingress.sources["source-b"].active)
	require.NotContains(t, ingress.draining, tracked)
}

func TestDirectSettingsIngressReserveUsesPublishedCapacity(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, _ := fixture.start(t)
	settingsA := DirectRuntimeSettings{
		Limits: testLimits(2), MaxSessions: 2, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	}
	ingress.ApplyRuntimeSettings(settingsA)
	ingress.mu.Lock()
	ingress.sources["source-existing"] = &directSourceSlot{
		candidate: &directIngressConnection{sourceAgentID: "source-existing"},
	}
	ingress.mu.Unlock()

	beforeLock := make(chan struct{})
	allowLock := make(chan struct{})
	var once sync.Once
	ingress.beforeReserveSourceLock = func() {
		once.Do(func() {
			close(beforeLock)
			<-allowLock
		})
	}
	type reserveResult struct {
		tracked *directIngressConnection
		status  int
		code    string
	}
	reserved := make(chan reserveResult, 1)
	go func() {
		tracked, status, code, _ := ingress.reserveSource("source-b", settingsA)
		reserved <- reserveResult{tracked: tracked, status: status, code: code}
	}()
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.MaxSessions = 1
	ingress.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	result := <-reserved
	require.Nil(t, result.tracked)
	require.Equal(t, http.StatusServiceUnavailable, result.status)
	require.Equal(t, directReasonCapacity, result.code)
	require.Equal(t, 1, ingress.Snapshot().Candidates)
}

func TestDirectIngressHandshakeMessageReadLimitBoundary(t *testing.T) {
	for _, test := range []struct {
		name    string
		size    int
		allowed bool
	}{
		{name: "exact 64 KiB", size: directHandshakeReadLimit, allowed: true},
		{name: "64 KiB plus one", size: directHandshakeReadLimit + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDirectIngressFixture(t)
			ingress, server := fixture.start(t)
			endpoint, err := DirectWebSocketURL(server.URL, "target-a")
			require.NoError(t, err)
			header := http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}}
			conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
			require.NoError(t, err)
			defer conn.Close()
			hello, err := json.Marshal(wire.DirectHello{ProtocolVersion: wire.ProtocolVersion, Limits: testLimits(1)})
			require.NoError(t, err)
			require.LessOrEqual(t, len(hello), test.size)
			hello = append(hello, bytes.Repeat([]byte(" "), test.size-len(hello))...)
			require.Len(t, hello, test.size)
			require.NoError(t, conn.WriteMessage(websocket.TextMessage, hello))
			require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
			messageType, payload, err := conn.ReadMessage()
			if !test.allowed {
				require.Error(t, err)
				require.Eventually(t, func() bool { return ingress.connectionCount() == 0 }, time.Second, time.Millisecond)
				return
			}
			require.NoError(t, err)
			require.Equal(t, websocket.TextMessage, messageType)
			var ready wire.DirectReady
			require.NoError(t, json.Unmarshal(payload, &ready))
			require.NotZero(t, ready.SessionGeneration)
			require.NoError(t, conn.WriteJSON(wire.DirectAccepted{SessionGeneration: ready.SessionGeneration}))
			messageType, payload, err = conn.ReadMessage()
			require.NoError(t, err)
			require.Equal(t, websocket.TextMessage, messageType)
			var confirmed wire.DirectConfirmed
			require.NoError(t, json.Unmarshal(payload, &confirmed))
			require.Equal(t, ready.SessionGeneration, confirmed.SessionGeneration)
		})
	}
}

func TestDirectIngressSwitchesToBinarySessionReadLimitAfterConfirmed(t *testing.T) {
	limits := testLimits(1)
	limits.MaxMetadataBytes = wire.MaxV2PayloadBytes
	limits.MaxDataBytes = wire.MaxV2PayloadBytes
	limits.MaxQueuedSessionBytes = 2 * wire.MaxV2PayloadBytes
	fixture := newDirectIngressFixture(t)
	fixture.targetLimits = limits
	ingress, server := fixture.start(t)
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	header := http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}}
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, header)
	require.NoError(t, err)
	defer conn.Close()
	require.NoError(t, conn.WriteJSON(wire.DirectHello{ProtocolVersion: wire.ProtocolVersion, Limits: limits}))
	var ready wire.DirectReady
	require.NoError(t, conn.ReadJSON(&ready))
	require.Equal(t, limits, ready.Limits)
	require.NoError(t, conn.WriteJSON(wire.DirectAccepted{SessionGeneration: ready.SessionGeneration}))
	var confirmed wire.DirectConfirmed
	require.NoError(t, conn.ReadJSON(&confirmed))
	require.Equal(t, ready.SessionGeneration, confirmed.SessionGeneration)

	open := wire.Open{
		ProbePolicy: wire.ProbeBypassBusinessPolicy, Method: http.MethodGet, Path: "/ping",
		Header: map[string][]string{}, RemainingNanos: time.Second.Nanoseconds(),
		TargetAgentID: "target-a", ResponseWindow: limits.InitialStreamWindow,
	}
	payload, err := wire.EncodeMetadata(open, limits.MaxMetadataBytes)
	require.NoError(t, err)
	payload = append(payload, bytes.Repeat([]byte(" "), int(limits.MaxMetadataBytes)-len(payload))...)
	boundary, err := wire.Encode(wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: testStreamID(111), Sequence: 1, Payload: payload,
	}, limits)
	require.NoError(t, err)
	readLimit := sessionMessageReadLimit(limits)
	require.Len(t, boundary, int(readLimit))
	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, boundary))
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(time.Second)))
	messageType, response, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(response, limits)
	require.NoError(t, err)
	require.Equal(t, wire.FrameReady, frame.Type)

	require.NoError(t, conn.WriteMessage(websocket.BinaryMessage, make([]byte, readLimit+1)))
	_, _, err = conn.ReadMessage()
	require.Error(t, err)
	require.Eventually(t, func() bool { return ingress.connectionCount() == 0 }, time.Second, time.Millisecond)
}

func waitDirectHandshakePhase(t *testing.T, duration time.Duration) {
	t.Helper()
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-t.Context().Done():
		t.Fatal(context.Cause(t.Context()))
	}
}

type directIngressFixture struct {
	now              time.Time
	ingressNow       func() time.Time
	privateKey       ed25519.PrivateKey
	publicKey        ed25519.PublicKey
	source           *models.Agent
	target           *models.Agent
	findAgentByID    func(string) *models.Agent
	targetLimits     wire.Limits
	handshakeTimeout time.Duration
	maxSessions      int
	ticket           agentauth.ForwardTicket
	logger           *zap.Logger
	suppressor       *diagnostics.Suppressor
}

func newDirectIngressFixture(t *testing.T) *directIngressFixture {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	fixture := &directIngressFixture{
		now: time.Now().Truncate(time.Second), privateKey: privateKey, publicKey: publicKey,
		source:       &models.Agent{AgentID: "source-a", Status: consts.StatusEnabled},
		target:       &models.Agent{AgentID: "target-a", Status: consts.StatusEnabled},
		targetLimits: testLimits(4), handshakeTimeout: time.Minute,
	}
	fixture.ticket = fixture.sign("source-a", "agent-forward", fixture.now.Add(time.Hour))
	return fixture
}

func (f *directIngressFixture) start(t *testing.T) (*DirectTunnelIngress, *httptest.Server) {
	t.Helper()
	return f.startWithRouter(t, http.NotFoundHandler())
}

func (f *directIngressFixture) startWithRouter(t *testing.T, targetRouter http.Handler) (*DirectTunnelIngress, *httptest.Server) {
	t.Helper()
	ingressNow := f.ingressNow
	if ingressNow == nil {
		ingressNow = func() time.Time { return f.now }
	}
	handler := NewTargetHandler(TargetHandlerOptions{
		TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: targetRouter,
	})
	ingress := NewDirectTunnelIngress(DirectTunnelIngressOptions{
		TargetAgentID: "target-a",
		FindAgentByID: func(agentID string) *models.Agent {
			if f.findAgentByID != nil {
				return f.findAgentByID(agentID)
			}
			if f.source != nil && f.source.AgentID == agentID {
				copy := *f.source
				return &copy
			}
			if agentID == "target-a" && f.target != nil {
				copy := *f.target
				return &copy
			}
			return nil
		},
		LoadAuth: func() agentproxy.ForwardAuthSnapshot {
			return agentproxy.ForwardAuthSnapshot{
				Capabilities: []string{protocol.AgentCapabilityForwardV1},
				SigningKeys:  []agentauth.PublicKey{{KeyID: "direct-key", Algorithm: protocol.AgentAuthAlgorithmEdDSA, Key: append([]byte(nil), f.publicKey...)}},
			}
		},
		Limits: f.targetLimits, TargetHandler: handler, HandshakeTimeout: f.handshakeTimeout,
		DrainTimeout: time.Second, Now: ingressNow,
		Logger: f.logger, Suppressor: f.suppressor,
		MaxSessions: func() int {
			if f.maxSessions > 0 {
				return f.maxSessions
			}
			return maxDirectIngressSessions
		},
	})
	router := gin.New()
	router.GET(DirectTunnelPath, ingress.Handle)
	server := httptest.NewServer(router)
	t.Cleanup(func() {
		server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = ingress.Close(ctx)
	})
	return ingress, server
}

func TestDirectIngressLogsHandshakeRejectReplacementDrainAndClose(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	fixture := newDirectIngressFixture(t)
	fixture.logger = zap.New(core)
	fixture.suppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute})
	ingress, server := fixture.start(t)
	endpoint, err := DirectWebSocketURL(server.URL, "target-a")
	require.NoError(t, err)
	conn, _, err := websocket.DefaultDialer.Dial(endpoint, http.Header{"Authorization": {"Bearer " + string(fixture.ticket)}})
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(`{"ticket":"secret"}`)))
	_ = conn.Close()
	require.Eventually(t, func() bool { return observed.FilterMessage("direct ingress rejected").Len() == 1 }, time.Second, time.Millisecond)
	require.NotContains(t, strings.ToLower(observed.FilterMessage("direct ingress rejected").All()[0].ContextMap()["error"].(string)), "secret")

	first, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(t.Context(), fixture.dialRequest(server.URL, testLimits(2)))
	require.NoError(t, err)
	second, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(t.Context(), fixture.dialRequest(server.URL, testLimits(2)))
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		return observed.FilterMessage("direct ingress accepted").Len() == 2
	}, 2*time.Second, time.Millisecond, "second ingress was not installed before connection close")
	require.Eventually(t, func() bool { return ingress.Snapshot().Active == 1 && ingress.Snapshot().Draining == 0 }, 2*time.Second, time.Millisecond)
	require.NoError(t, second.Close(t.Context()))
	_ = first.Close(t.Context())
	require.Eventually(t, func() bool { return ingress.connectionCount() == 0 }, time.Second, time.Millisecond)

	messages := observedMessages(observed.All())
	require.Contains(t, messages, "direct session replaced")
	require.Contains(t, messages, "direct session draining")
	require.Contains(t, messages, "direct session closed")
}

func (f *directIngressFixture) dialRequest(targetURL string, limits wire.Limits) DirectSessionDialRequest {
	return f.dialRequestForSource(targetURL, "source-a", f.ticket, limits)
}

func (f *directIngressFixture) dialRequestForSource(targetURL, source string, ticket agentauth.ForwardTicket, limits wire.Limits) DirectSessionDialRequest {
	return DirectSessionDialRequest{
		SourceAgentID: source, TargetAgentID: "target-a", TargetURL: targetURL,
		Credential: agentauthcache.ForwardCredential{Ticket: ticket, ExpiresAt: f.now.Add(time.Hour)}, Limits: limits,
	}
}

func (f *directIngressFixture) sign(source, audience string, expiresAt time.Time) agentauth.ForwardTicket {
	claims := agentauth.ForwardClaims{
		SourceAgentID: source, Capability: protocol.AgentCapabilityForwardV1,
		RegisteredClaims: jwt.RegisteredClaims{
			Audience: jwt.ClaimStrings{audience}, ExpiresAt: jwt.NewNumericDate(expiresAt), IssuedAt: jwt.NewNumericDate(f.now),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims)
	token.Header["kid"] = "direct-key"
	raw, _ := token.SignedString(f.privateKey)
	return agentauth.ForwardTicket(raw)
}

func TestDirectIngressErrorsDoNotContainSecrets(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	ingress, server := fixture.start(t)
	secret := "ticket-secret-marker"
	endpoint := server.URL + DirectTunnelPath + "?target_agent_id=target-a&secret=" + secret
	request, err := http.NewRequest(http.MethodGet, endpoint, nil)
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+secret)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusUnauthorized, response.StatusCode)
	require.NotContains(t, strings.Join(response.Header.Values("Warning"), " "), secret)
	require.Zero(t, ingress.connectionCount())
}
