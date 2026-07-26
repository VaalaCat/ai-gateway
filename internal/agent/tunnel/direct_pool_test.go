package tunnel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	pkgagentauth "github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	poolpkg "github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

// ---- controllable clock -------------------------------------------------

type poolTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func newPoolTestClock() *poolTestClock { return &poolTestClock{now: time.Now().Truncate(time.Second)} }

func (c *poolTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *poolTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// ---- static credential reader -------------------------------------------

type staticForwardCredentials struct {
	mu   sync.Mutex
	cred agentauthcache.ForwardCredential
	err  error
}

func (s *staticForwardCredentials) CachedForwardCredential() (agentauthcache.ForwardCredential, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cred, s.err
}

func (s *staticForwardCredentials) set(cred agentauthcache.ForwardCredential, err error) {
	s.mu.Lock()
	s.cred, s.err = cred, err
	s.mu.Unlock()
}

// ---- counting session conn ----------------------------------------------

type countingSessionConn struct {
	sessionConn
	limits wire.Limits
	closes atomic.Int64
	opens  atomic.Int64
}

func (c *countingSessionConn) Close() error {
	c.closes.Add(1)
	return c.sessionConn.Close()
}

func (c *countingSessionConn) WriteMessage(messageType int, payload []byte) error {
	if messageType == websocket.BinaryMessage {
		frame, err := wire.Decode(payload, c.limits)
		if err == nil && frame.Type == wire.FrameOpen {
			c.opens.Add(1)
		}
	}
	return c.sessionConn.WriteMessage(messageType, payload)
}

// ---- blocking fake dialer -----------------------------------------------

type fakeDialResult struct {
	session *Session
	err     error
}

type directSessionDialerFunc func(context.Context, DirectSessionDialRequest) (*Session, error)

func (f directSessionDialerFunc) DialDirectSession(ctx context.Context, req DirectSessionDialRequest) (*Session, error) {
	return f(ctx, req)
}

type blockingDirectDialer struct {
	t        *testing.T
	limits   wire.Limits
	calls    atomic.Int64
	gen      atomic.Uint64
	release  chan fakeDialResult
	failMode atomic.Bool
	failErr  atomic.Value // error

	mu    sync.Mutex
	conns []*countingSessionConn
}

func newBlockingDirectDialer() *blockingDirectDialer {
	return &blockingDirectDialer{limits: testLimits(64), release: make(chan fakeDialResult, 16)}
}

func (d *blockingDirectDialer) DialDirectSession(ctx context.Context, _ DirectSessionDialRequest) (*Session, error) {
	d.calls.Add(1)
	// Once a persistent failure is armed, every dial (including retries from
	// waiters that arrive after the shared candidate already failed) fails the
	// same way, mirroring a dialer that keeps erroring rather than hanging.
	if d.failMode.Load() {
		return nil, d.failErr.Load().(error)
	}
	select {
	case result := <-d.release:
		if result.err != nil {
			return nil, result.err
		}
		return result.session, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// buildSession must run on the test goroutine (websocketPair uses require).
func (d *blockingDirectDialer) buildSession() *Session {
	client, peer := websocketPair(d.t)
	go func() {
		for {
			if _, _, err := peer.ReadMessage(); err != nil {
				return
			}
		}
	}()
	counting := &countingSessionConn{sessionConn: client, limits: d.limits}
	d.mu.Lock()
	d.conns = append(d.conns, counting)
	d.mu.Unlock()
	generation := d.gen.Add(1)
	return newSession(counting, generation, d.limits, SessionOptions{
		Direction: SessionDirectionDirectOutgoing, IngressKind: agentproxy.IngressKindDirectTunnel,
		PingInterval: time.Hour, PongTimeout: time.Hour,
	})
}

func (d *blockingDirectDialer) releaseHealthySession() {
	d.release <- fakeDialResult{session: d.buildSession()}
}

func (d *blockingDirectDialer) releaseError(err error) {
	d.release <- fakeDialResult{err: err}
}

// failAllDials arms a persistent failure and unblocks any in-flight dial so
// that every current and future dial fails deterministically.
func (d *blockingDirectDialer) failAllDials(err error) {
	d.failErr.Store(err)
	d.failMode.Store(true)
	d.release <- fakeDialResult{err: err}
}

func (d *blockingDirectDialer) closeCounts() []int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	counts := make([]int64, len(d.conns))
	for i, conn := range d.conns {
		counts[i] = conn.closes.Load()
	}
	return counts
}

func (d *blockingDirectDialer) openCount() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	var count int64
	for _, conn := range d.conns {
		count += conn.opens.Load()
	}
	return count
}

// ---- pool construction --------------------------------------------------

type poolLimits struct {
	maxSessions int
	idleTimeout time.Duration
}

type testPool struct {
	*DirectSessionPool
	clock     *poolTestClock
	creds     *staticForwardCredentials
	maxRef    atomic.Int64
	idleRef   atomic.Int64
	limitsRef atomic.Value // wire.Limits
}

func (tp *testPool) setLimits(limits wire.Limits) { tp.limitsRef.Store(limits) }

func newTestDirectPool(t *testing.T, dialer DirectSessionDialer, lim poolLimits) *testPool {
	t.Helper()
	clock := newPoolTestClock()
	creds := &staticForwardCredentials{cred: agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-a"), ExpiresAt: clock.Now().Add(time.Hour),
	}}
	tp := &testPool{clock: clock, creds: creds}
	if lim.maxSessions == 0 {
		lim.maxSessions = 8
	}
	if lim.idleTimeout == 0 {
		lim.idleTimeout = time.Minute
	}
	tp.maxRef.Store(int64(lim.maxSessions))
	tp.idleRef.Store(int64(lim.idleTimeout))
	tp.limitsRef.Store(testLimits(64))
	pool := NewDirectSessionPool(DirectSessionPoolOptions{
		SourceAgentID: "agent-a",
		Dialer:        dialer,
		Credentials:   creds,
		Limits:        func() wire.Limits { return tp.limitsRef.Load().(wire.Limits) },
		MaxSessions:   func() int { return int(tp.maxRef.Load()) },
		IdleTimeout:   func() time.Duration { return time.Duration(tp.idleRef.Load()) },
		DrainTimeout:  func() time.Duration { return 30 * time.Second },
		Now:           clock.Now,
		Logger:        zap.NewNop(),
	})
	tp.DirectSessionPool = pool
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = pool.Close(ctx)
		select {
		case <-pool.Done():
		case <-ctx.Done():
			t.Error("pool did not close")
		}
	})
	return tp
}

// ---- request/target helpers ---------------------------------------------

func directTarget(agentID string) agentproxy.DirectSessionTarget {
	parsed, _ := url.Parse("https://" + agentID + ".example:8443")
	return agentproxy.DirectSessionTarget{
		TargetAgentID: agentID, AddressFingerprint: "fp-" + agentID, WebSocketURL: parsed,
	}
}

func directTargetWithURL(agentID, rawURL string) agentproxy.DirectSessionTarget {
	parsed, _ := url.Parse(rawURL)
	target := directTarget(agentID)
	target.WebSocketURL = parsed
	return target
}

func directTargetWithProxy(agentID, rawProxy string) agentproxy.DirectSessionTarget {
	proxy, _ := url.Parse(rawProxy)
	target := directTarget(agentID)
	target.ProxyURL = proxy
	return target
}

func directAttemptRequest(agentID string) app.AttemptStreamRequest {
	return app.AttemptStreamRequest{
		TargetAgentID: agentID, Method: http.MethodPost, Path: attemptwire.EndpointPath,
		Hop: 1, Attempt: validTunnelAttemptMeta(),
	}
}

type openInFlight struct {
	err    chan error
	stream chan app.AttemptStream
}

func startOpen(pool *DirectSessionPool, ctx context.Context, target agentproxy.DirectSessionTarget) openInFlight {
	inflight := openInFlight{err: make(chan error, 1), stream: make(chan app.AttemptStream, 1)}
	go func() {
		stream, err := pool.OpenAttemptStream(ctx, target, directAttemptRequest(target.TargetAgentID))
		inflight.stream <- stream
		inflight.err <- err
	}()
	return inflight
}

// ---- tests --------------------------------------------------------------

func target(agentID string) agentproxy.DirectSessionTarget { return directTarget(agentID) }

func attemptRequest() app.AttemptStreamRequest { return directAttemptRequest("agent-b") }

func acquireAndOpenDirectAttemptStream(
	t *testing.T, transport agentproxy.DirectAttemptTransport,
) (agentproxy.DirectAttemptStreamReservation, app.AttemptStream) {
	t.Helper()
	reservation, err := transport.AcquireAttemptStream(t.Context())
	require.NoError(t, err)
	require.NotNil(t, reservation)
	stream, err := reservation.OpenAttemptStream(t.Context(), attemptRequest())
	reservation.Release()
	require.NoError(t, err)
	require.NotNil(t, stream)
	return reservation, stream
}

type directForwardReplayBody struct{}

func (directForwardReplayBody) Size() int64                  { return 0 }
func (directForwardReplayBody) Open() (io.ReadCloser, error) { return http.NoBody, nil }
func (directForwardReplayBody) Bytes(int64) ([]byte, error)  { return nil, nil }
func (directForwardReplayBody) Close() error                 { return nil }

func directForwardRequest(target agentproxy.DirectSessionTarget) agentproxy.DirectRequest {
	return agentproxy.DirectRequest{
		Target: target, RouteID: 7, RequestID: "request-a", Hop: 1,
		Request: &http.Request{Method: http.MethodPost, Header: make(http.Header)},
		Body:    directForwardReplayBody{}, Attempt: validTunnelAttemptMeta(),
	}
}

func forwardDirectForCircuitTest(
	t *testing.T, forwarder *agentproxy.DirectForwarder, target agentproxy.DirectSessionTarget,
) agentproxy.AttemptTransportOutcome {
	t.Helper()
	return forwarder.Forward(t.Context(), directForwardRequest(target), httptest.NewRecorder())
}

func newPoolCircuitForwarder(t *testing.T, pool *DirectSessionPool, threshold int) *agentproxy.DirectForwarder {
	t.Helper()
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: pool, CircuitFailureThreshold: threshold, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })
	return forwarder
}

type selectedDirectTransport struct {
	transport agentproxy.DirectAttemptTransport
}

type selectedDirectTransportBuilder struct {
	selected atomic.Pointer[selectedDirectTransport]
}

func (b *selectedDirectTransportBuilder) selectTransport(transport agentproxy.DirectAttemptTransport) {
	b.selected.Store(&selectedDirectTransport{transport: transport})
}

func (b *selectedDirectTransportBuilder) BuildDirectAttemptTransport(
	context.Context, agentproxy.DirectSessionTarget,
) (agentproxy.DirectAttemptTransport, error) {
	selected := b.selected.Load()
	if selected == nil {
		return nil, errors.New("test direct transport is not selected")
	}
	return selected.transport, nil
}

type failingSelectedDirectTransport struct {
	identity           agentproxy.DirectTransportIdentity
	addressFingerprint string
	err                error
}

func (t *failingSelectedDirectTransport) TransportIdentity() agentproxy.DirectTransportIdentity {
	return t.identity
}

func (t *failingSelectedDirectTransport) AcquireAttemptStream(
	context.Context,
) (agentproxy.DirectAttemptStreamReservation, error) {
	return &failingSelectedDirectReservation{transport: t}, nil
}

type failingSelectedDirectReservation struct {
	transport *failingSelectedDirectTransport
}

func (r *failingSelectedDirectReservation) TransportIdentity() agentproxy.DirectTransportIdentity {
	return r.transport.identity
}

func (r *failingSelectedDirectReservation) AddressFingerprint() string {
	return r.transport.addressFingerprint
}

func (r *failingSelectedDirectReservation) OpenAttemptStream(
	context.Context, app.AttemptStreamRequest,
) (app.AttemptStream, error) {
	return nil, r.transport.err
}

func (*failingSelectedDirectReservation) Release() {}

func TestDirectSessionPoolForwarderOpenCircuitRejectsFallbackBeforeFrameOpen(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	firstTarget := directTargetWithURL("agent-b", "https://first.example:8443")
	firstTarget.AddressFingerprint = "fp-first"
	firstTransport, err := pool.BuildDirectAttemptTransport(t.Context(), firstTarget)
	require.NoError(t, err)
	dialer.releaseHealthySession()
	_, firstStream := acquireAndOpenDirectAttemptStream(t, firstTransport)
	require.NoError(t, firstStream.Close())
	require.Eventually(t, func() bool {
		return dialer.openCount() == 1 && pool.Snapshot().Streams == 0
	}, time.Second, time.Millisecond)

	secondTarget := directTargetWithURL("agent-b", "https://second.example:9443")
	secondTarget.AddressFingerprint = "fp-second"
	secondTransport, err := pool.BuildDirectAttemptTransport(t.Context(), secondTarget)
	require.NoError(t, err)
	transports := &selectedDirectTransportBuilder{}
	transports.selectTransport(&failingSelectedDirectTransport{
		identity: firstTransport.TransportIdentity(), addressFingerprint: firstTarget.AddressFingerprint,
		err: directEndpointError("dial", "failed", "wss://first.example"+DirectTunnelPath),
	})
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: transports, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	failed := forwardDirectForCircuitTest(t, forwarder, firstTarget)
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
	transports.selectTransport(secondTransport)
	dialer.releaseError(errors.New("replacement dial failed"))
	denied := forwardDirectForCircuitTest(t, forwarder, secondTarget)
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, denied.Code)
	require.Never(t, func() bool { return dialer.openCount() != 1 }, 100*time.Millisecond, time.Millisecond,
		"an OPEN actual circuit must reject the reservation before FrameOpen")
	require.Eventually(t, func() bool {
		pool.mu.Lock()
		active := pool.slots[firstTarget.TargetAgentID].active
		pool.mu.Unlock()
		return active != nil && active.idle()
	}, time.Second, time.Millisecond, "rejected reservation did not release session admission")
}

func TestDirectAttemptStreamReservationReleasePreventsOpen(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	transport, err := pool.BuildDirectAttemptTransport(t.Context(), target("agent-b"))
	require.NoError(t, err)
	dialer.releaseHealthySession()
	reservation, err := transport.AcquireAttemptStream(t.Context())
	require.NoError(t, err)

	reservation.Release()
	reservation.Release()
	stream, err := reservation.OpenAttemptStream(t.Context(), attemptRequest())
	require.Error(t, err)
	require.Nil(t, stream)
	require.Zero(t, dialer.openCount())
	requireDirectActiveSessionIdle(t, pool.DirectSessionPool, "agent-b")
}

func TestDirectAttemptStreamReservationCanceledOpenReleasesAdmission(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	transport, err := pool.BuildDirectAttemptTransport(t.Context(), target("agent-b"))
	require.NoError(t, err)
	dialer.releaseHealthySession()
	reservation, err := transport.AcquireAttemptStream(t.Context())
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	stream, err := reservation.OpenAttemptStream(ctx, attemptRequest())
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, stream)
	reservation.Release()
	reservation.Release()
	require.Zero(t, dialer.openCount())
	requireDirectActiveSessionIdle(t, pool.DirectSessionPool, "agent-b")
}

type reservationOwnerRaceResult struct {
	stream app.AttemptStream
	err    error
	open   bool
}

func TestDirectAttemptStreamReservationConcurrentOpenAndRelease(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	transport, err := pool.BuildDirectAttemptTransport(t.Context(), target("agent-b"))
	require.NoError(t, err)
	dialer.releaseHealthySession()

	for range 16 {
		reservation, acquireErr := transport.AcquireAttemptStream(t.Context())
		require.NoError(t, acquireErr)
		before := dialer.openCount()
		workers := poolpkg.NewWithResults[reservationOwnerRaceResult]().WithMaxGoroutines(2)
		workers.Go(func() reservationOwnerRaceResult {
			stream, openErr := reservation.OpenAttemptStream(t.Context(), attemptRequest())
			return reservationOwnerRaceResult{stream: stream, err: openErr, open: true}
		})
		workers.Go(func() reservationOwnerRaceResult {
			reservation.Release()
			return reservationOwnerRaceResult{}
		})

		results := workers.Wait()
		opened := false
		for _, result := range results {
			if !result.open {
				continue
			}
			if result.err == nil {
				opened = true
				require.NotNil(t, result.stream)
				require.NoError(t, result.stream.Close())
			} else {
				require.Nil(t, result.stream)
			}
		}
		reservation.Release()
		wantOpens := before
		if opened {
			wantOpens++
		}
		require.Eventually(t, func() bool { return dialer.openCount() == wantOpens }, time.Second, time.Millisecond)
		second, secondErr := reservation.OpenAttemptStream(t.Context(), attemptRequest())
		require.Error(t, secondErr)
		require.Nil(t, second)
		require.Never(t, func() bool { return dialer.openCount() != wantOpens }, 10*time.Millisecond, time.Millisecond)
	}
	requireDirectActiveSessionIdle(t, pool.DirectSessionPool, "agent-b")
}

func requireDirectActiveSessionIdle(t *testing.T, pool *DirectSessionPool, targetAgentID string) {
	t.Helper()
	require.Eventually(t, func() bool {
		pool.mu.Lock()
		slot := pool.slots[targetAgentID]
		var active *Session
		if slot != nil {
			active = slot.active
		}
		pool.mu.Unlock()
		return active != nil && active.idle()
	}, time.Second, time.Millisecond)
}

func TestDirectSessionPoolForwarderCircuitCountsOnlyPeerTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name          string
		stage         string
		code          string
		credentialErr bool
		wantCode      string
		wantCircuit   bool
		wantDials     int64
	}{
		{name: "credential cache", credentialErr: true, wantCode: agentproxy.CodeDirectAuthUnavailable},
		{name: "proxy config", stage: "proxy", code: "invalid", wantCode: agentproxy.CodeDirectDisabled, wantDials: 3},
		{name: "dial", stage: "dial", code: "failed", wantCode: agentproxy.CodeDirectConnect, wantCircuit: true, wantDials: 2},
		{name: "authenticated handshake", stage: "confirmed", code: "invalid", wantCode: agentproxy.CodeDirectConnect, wantCircuit: true, wantDials: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			dialer := directSessionDialerFunc(func(context.Context, DirectSessionDialRequest) (*Session, error) {
				calls.Add(1)
				return nil, directEndpointError(test.stage, test.code, "wss://target.example"+DirectTunnelPath)
			})
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
			if test.credentialErr {
				pool.creds.set(agentauthcache.ForwardCredential{}, errors.New("credential unavailable"))
			}
			forwarder := newPoolCircuitForwarder(t, pool.DirectSessionPool, 2)
			for range 2 {
				outcome := forwardDirectForCircuitTest(t, forwarder, target("agent-b"))
				require.Equal(t, test.wantCode, outcome.Code)
			}

			afterThreshold := forwardDirectForCircuitTest(t, forwarder, target("agent-b"))
			if test.wantCircuit {
				require.Equal(t, agentproxy.CodeDirectCircuitOpen, afterThreshold.Code)
			} else {
				require.Equal(t, test.wantCode, afterThreshold.Code)
			}
			require.Equal(t, test.wantDials, calls.Load())
		})
	}
}

func TestDirectSessionPoolForwarderCircuitUsesTransportIdentity(t *testing.T) {
	for _, test := range []struct {
		name   string
		first  agentproxy.DirectSessionTarget
		mutate func(*testPool) agentproxy.DirectSessionTarget
	}{
		{
			name:  "credential generation",
			first: target("agent-b"),
			mutate: func(pool *testPool) agentproxy.DirectSessionTarget {
				pool.creds.set(agentauthcache.ForwardCredential{
					Ticket: pkgagentauth.ForwardTicket("rotated-ticket-secret"), ExpiresAt: pool.clock.Now().Add(time.Hour),
				}, nil)
				return target("agent-b")
			},
		},
		{
			name:  "effective proxy",
			first: directTargetWithProxy("agent-b", "http://old-user:old-password@proxy.example:8080/path?token=old-query"),
			mutate: func(*testPool) agentproxy.DirectSessionTarget {
				return directTargetWithProxy("agent-b", "http://new-user:new-password@proxy.example:8080/path?token=new-query")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int64
			dialer := directSessionDialerFunc(func(context.Context, DirectSessionDialRequest) (*Session, error) {
				calls.Add(1)
				return nil, directEndpointError("dial", "failed", "wss://target.example"+DirectTunnelPath)
			})
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
			forwarder := newPoolCircuitForwarder(t, pool.DirectSessionPool, 1)

			failed := forwardDirectForCircuitTest(t, forwarder, test.first)
			require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
			blocked := forwardDirectForCircuitTest(t, forwarder, test.first)
			require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code, "the same transport identity must stay blocked")
			require.Equal(t, int64(1), calls.Load())

			changed := test.mutate(pool)
			afterChange := forwardDirectForCircuitTest(t, forwarder, changed)
			require.Equal(t, agentproxy.CodeDirectConnect, afterChange.Code, "a changed transport identity must bypass the old circuit")
			require.Equal(t, int64(2), calls.Load())
		})
	}
}

func TestDirectSessionPoolForwarderCanonicalProxyKeepsSameCircuitIdentity(t *testing.T) {
	var calls atomic.Int64
	dialer := directSessionDialerFunc(func(context.Context, DirectSessionDialRequest) (*Session, error) {
		calls.Add(1)
		return nil, directEndpointError("dial", "failed", "wss://target.example"+DirectTunnelPath)
	})
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	forwarder := newPoolCircuitForwarder(t, pool.DirectSessionPool, 1)
	firstTarget := directTargetWithProxy(
		"agent-b", "HTTP://User:pa%73s@PROXY.EXAMPLE:080/route%7E?b=two&a=one%20value#first",
	)
	equivalentTarget := directTargetWithProxy(
		"agent-b", "http://User:pass@proxy.example/route~?a=one+value&b=two#second",
	)

	failed := forwardDirectForCircuitTest(t, forwarder, firstTarget)
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
	blocked := forwardDirectForCircuitTest(t, forwarder, equivalentTarget)
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code)
	require.Equal(t, int64(1), calls.Load(), "dial-equivalent proxies must share one circuit identity")
}

func TestDirectAttemptTransportBuildFreezesCredentialAndTarget(t *testing.T) {
	requests := make(chan DirectSessionDialRequest, 1)
	dialer := directSessionDialerFunc(func(_ context.Context, req DirectSessionDialRequest) (*Session, error) {
		requests <- req
		return nil, directEndpointError("dial", "failed", "wss://target.example"+DirectTunnelPath)
	})
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	originalTarget := directTargetWithProxy(
		"agent-b", "http://old-user:old-password@old-proxy.example:8080/path?token=old-query",
	)
	originalURL := originalTarget.WebSocketURL.String()
	originalProxy := originalTarget.ProxyURL.String()

	transport, err := pool.BuildDirectAttemptTransport(t.Context(), originalTarget)
	require.NoError(t, err)
	originalTarget.WebSocketURL.Host = "mutated-target.example:9443"
	originalTarget.ProxyURL.Host = "mutated-proxy.example:9443"
	originalTarget.ProxyURL.User = url.UserPassword("mutated-user", "mutated-password")
	originalTarget.ProxyURL.RawQuery = "token=mutated-query"
	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("mutated-ticket-secret"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)

	reservation, err := transport.AcquireAttemptStream(t.Context())
	if reservation != nil {
		reservation.Release()
	}
	require.Error(t, err)
	dialRequest := <-requests
	require.Equal(t, originalURL, dialRequest.TargetURL)
	require.Equal(t, originalProxy, dialRequest.ProxyURL.String())
	require.Equal(t, pkgagentauth.ForwardTicket("ticket-a"), dialRequest.Credential.Ticket)

	encodedIdentity := fmt.Sprint(transport.TransportIdentity())
	for _, secret := range []string{
		"old-user", "old-password", "old-query", "ticket-a", "mutated-user", "mutated-password", "mutated-query", "mutated-ticket-secret",
	} {
		require.NotContains(t, encodedIdentity, secret)
	}
}

func TestDirectAttemptTransportOpenRefreshesRuntimeLimitsWithoutRereadingCredential(t *testing.T) {
	requests := make(chan DirectSessionDialRequest, 4)
	dialer := directSessionDialerFunc(func(_ context.Context, req DirectSessionDialRequest) (*Session, error) {
		requests <- req
		return nil, directEndpointError("dial", "failed", "wss://target.example"+DirectTunnelPath)
	})
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	transport, err := pool.BuildDirectAttemptTransport(t.Context(), target("agent-b"))
	require.NoError(t, err)
	updatedLimits := testLimits(32)
	pool.setLimits(updatedLimits)
	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("rotated-ticket-secret"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	reservation, err := transport.AcquireAttemptStream(ctx)
	if reservation != nil {
		reservation.Release()
	}
	require.Error(t, err)
	dialRequest := <-requests
	require.Equal(t, updatedLimits, dialRequest.Limits)
	require.Equal(t, pkgagentauth.ForwardTicket("ticket-a"), dialRequest.Credential.Ticket)
	require.Len(t, requests, 0, "stale limits must be refreshed before starting a candidate")
}

func TestDirectSessionPoolConcurrentOpenDialsOnce(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	workers := poolpkg.NewWithResults[app.AttemptStream]().WithMaxGoroutines(32)
	for range 32 {
		workers.Go(func() app.AttemptStream {
			stream, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
			require.NoError(t, err)
			return stream
		})
	}
	dialer.releaseHealthySession()
	streams := workers.Wait()
	require.Equal(t, int64(1), dialer.calls.Load())
	require.Len(t, streams, 32)
}

func TestDirectSessionPoolDifferentSnapshotWaitsThenStartsOwnCandidate(t *testing.T) {
	for _, test := range []struct {
		name   string
		first  agentproxy.DirectSessionTarget
		mutate func(*testPool) agentproxy.DirectSessionTarget
	}{
		{
			name:  "credential",
			first: target("agent-b"),
			mutate: func(pool *testPool) agentproxy.DirectSessionTarget {
				pool.creds.set(agentauthcache.ForwardCredential{
					Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
				}, nil)
				return target("agent-b")
			},
		},
		{
			name:  "proxy",
			first: directTargetWithProxy("agent-b", "http://proxy-a.example:8080"),
			mutate: func(*testPool) agentproxy.DirectSessionTarget {
				return directTargetWithProxy("agent-b", "http://proxy-b.example:8080")
			},
		},
		{
			name:  "address",
			first: directTargetWithURL("agent-b", "https://address-a.example:8443"),
			mutate: func(*testPool) agentproxy.DirectSessionTarget {
				changed := directTargetWithURL("agent-b", "https://address-b.example:9443")
				changed.AddressFingerprint = "fp-agent-b-changed"
				return changed
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := newBlockingDirectDialer()
			dialer.t = t
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
			first := startOpen(pool.DirectSessionPool, t.Context(), test.first)
			require.Eventually(t, func() bool { return dialer.calls.Load() == 1 }, time.Second, time.Millisecond)

			secondTarget := test.mutate(pool)
			second := startOpen(pool.DirectSessionPool, t.Context(), secondTarget)
			dialer.releaseHealthySession()
			require.NoError(t, <-first.err)
			firstStream := <-first.stream

			require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond,
				"a different snapshot consumed the first candidate instead of starting its own")
			dialer.releaseHealthySession()
			require.NoError(t, <-second.err)
			secondStream := <-second.stream
			require.NoError(t, firstStream.Close())
			require.NoError(t, secondStream.Close())
		})
	}
}

func TestDirectSessionPoolDifferentSnapshotIgnoresPriorCandidateFailure(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 1 }, time.Second, time.Millisecond)

	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseError(directEndpointError("dial", "failed", "wss://agent-b.example"+DirectTunnelPath))
	require.Error(t, <-first.err)

	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond,
		"a different snapshot propagated the prior candidate failure")
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	require.NoError(t, (<-second.stream).Close())
}

func TestDirectSessionPoolWaiterCancellationDoesNotCancelCandidate(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2})
	canceled, cancel := context.WithCancel(t.Context())
	first := startOpen(pool.DirectSessionPool, canceled, target("agent-b"))
	// ensure the first waiter created the candidate before the second joins.
	require.Eventually(t, func() bool { return pool.Snapshot().Candidates == 1 }, time.Second, time.Millisecond)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	cancel()
	require.ErrorIs(t, <-first.err, context.Canceled)
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	require.Equal(t, int64(1), dialer.calls.Load())
}

func TestDirectSessionPoolCandidateFailureWakesAllWaiters(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2})
	waiters := make([]openInFlight, 4)
	for i := range waiters {
		waiters[i] = startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	}
	require.Eventually(t, func() bool { return pool.Snapshot().Candidates == 1 }, time.Second, time.Millisecond)
	dialer.failAllDials(errors.New("dial boom"))
	for i := range waiters {
		require.Error(t, <-waiters[i].err, "waiter %d", i)
	}
	require.GreaterOrEqual(t, dialer.calls.Load(), int64(1))
	require.Equal(t, 0, pool.Snapshot().Active)
}

func TestDirectSessionPoolReusesHealthyActive(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	stream, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	require.NoError(t, err)
	require.NotNil(t, stream)
	require.Equal(t, int64(1), dialer.calls.Load())
	require.Equal(t, 1, pool.Snapshot().Active)
}

func TestDirectPoolDialFailureRecoveryAndLifecycleLogsUseRealPaths(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})
	pool.logs = newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute}))
	for range 3 {
		opened := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
		dialer.releaseError(errors.New("token=secret dial failed"))
		require.Error(t, <-opened.err)
	}
	opened := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-opened.err)
	stream := <-opened.stream

	updated := testLimits(4)
	updated.MaxDataBytes /= 2
	pool.ApplyRuntimeSettings(DirectRuntimeSettings{Limits: updated, MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second})
	require.NoError(t, stream.Close())
	require.Eventually(t, func() bool { return pool.Snapshot().Draining == 0 }, time.Second, time.Millisecond)

	messages := observedMessages(observed.All())
	require.Contains(t, messages, "direct dial recovered")
	require.Contains(t, messages, "direct session draining")
	require.Contains(t, messages, "direct session closed")
	for _, entry := range observed.All() {
		if value, ok := entry.ContextMap()["error"].(string); ok {
			require.NotContains(t, strings.ToLower(value), "secret")
		}
	}
}

func TestDirectSettingsLimitsChangeStopsAdmissionUntilReplacement(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Hour})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	oldStream := <-first.stream
	updated := testLimits(4)
	updated.MaxDataBytes /= 2
	pool.ApplyRuntimeSettings(DirectRuntimeSettings{
		Limits: updated, MaxSessions: 4, IdleTimeout: time.Hour, DrainTimeout: time.Second,
	})
	snapshot := pool.Snapshot()
	require.Zero(t, snapshot.Active)
	require.Zero(t, snapshot.Candidates)
	require.Equal(t, 1, snapshot.Draining,
		"limits update must transfer ownership to draining without waiting for another request")
	require.Equal(t, 1, snapshot.Streams)
	require.Equal(t, 1, snapshot.Sockets)
	require.Equal(t, 2, snapshot.Timers)
	directStream, ok := oldStream.(*Stream)
	require.True(t, ok)
	select {
	case <-directStream.Done():
		t.Fatal("an admitted stream was closed instead of finishing on the draining session")
	default:
	}
	require.NoError(t, oldStream.Close())
	require.Eventually(t, func() bool { return pool.Snapshot().Draining == 0 }, 2*time.Second, time.Millisecond)
}

func TestDirectPoolSnapshotKeepsTotalCurrentAndSingleSessionPeak(t *testing.T) {
	pool := NewDirectSessionPool(DirectSessionPoolOptions{
		SourceAgentID: "source-a",
		Limits:        func() wire.Limits { return testLimits(2) },
		MaxSessions:   func() int { return 2 },
		IdleTimeout:   func() time.Duration { return time.Hour },
		DrainTimeout:  func() time.Duration { return time.Second },
	})
	t.Cleanup(func() { require.NoError(t, pool.Close(context.Background())) })

	first := newSessionValue(nil, 1, testLimits(1), SessionOptions{})
	second := newSessionValue(nil, 2, testLimits(1), SessionOptions{})
	require.NoError(t, first.reserveIncoming(7))
	require.NoError(t, second.reserveIncoming(11))
	pool.mu.Lock()
	pool.slots["target-a"] = &directPoolSlot{target: "target-a", active: first}
	pool.slots["target-b"] = &directPoolSlot{target: "target-b", active: second}
	pool.mu.Unlock()

	snapshot := pool.Snapshot()
	require.EqualValues(t, 18, snapshot.BufferedBytes)
	require.EqualValues(t, 11, snapshot.MaxSessionPeakBufferedBytes,
		"an owner snapshot must not add historical peaks from independent sessions")
}

func TestDirectPoolApplyRuntimeSettingsDoesNotHoldOwnerLockAcrossNow(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	pool := NewDirectSessionPool(DirectSessionPoolOptions{
		Limits: func() wire.Limits { return testLimits(1) }, MaxSessions: func() int { return 1 },
		IdleTimeout: func() time.Duration { return time.Hour }, DrainTimeout: func() time.Duration { return time.Second },
		Now: func() time.Time {
			once.Do(func() { close(entered) })
			<-release
			return time.Now()
		},
	})
	defer func() {
		unblock()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = pool.Close(ctx)
	}()

	applyDone := make(chan struct{})
	go func() {
		defer close(applyDone)
		pool.ApplyRuntimeSettings(DirectRuntimeSettings{
			Limits: testLimits(1), MaxSessions: 1, IdleTimeout: time.Hour, DrainTimeout: time.Second,
		})
	}()
	receiveWithDirectTimeout(t, entered)
	cancelDone := make(chan struct{})
	go func() { pool.startClose(); close(cancelDone) }()
	receiveWithDirectTimeout(t, cancelDone)
	unblock()
	receiveWithDirectTimeout(t, applyDone)
}

func TestDirectSettingsPoolApplyWinsBeforeStaleCandidatePromotion(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2, idleTimeout: time.Hour})
	settingsA := pool.currentRuntimeSettings()
	pool.ApplyRuntimeSettings(settingsA)
	target := target("agent-b")
	key, _, err := pool.desiredKey(target)
	require.NoError(t, err)
	candidate := &directDialCandidate{
		target: target.TargetAgentID, key: key, frozen: target, done: make(chan struct{}),
		fingerprint: directDesiredFingerprint{Limits: limitsFingerprint(settingsA.Limits)},
	}
	pool.mu.Lock()
	pool.slots[target.TargetAgentID] = &directPoolSlot{target: target.TargetAgentID, candidate: candidate}
	pool.mu.Unlock()
	session := dialer.buildSession()
	defer session.Close(context.Background())

	beforeLock := make(chan struct{})
	allowLock := make(chan struct{})
	pool.beforeFinishCandidateLock = func() {
		close(beforeLock)
		<-allowLock
	}
	finishDone := make(chan bool, 1)
	go func() { finishDone <- pool.finishCandidate(candidate, session, nil) }()
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.Limits.MaxDataBytes /= 2
	pool.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	require.False(t, <-finishDone, "candidate sampled before settings B must be stale after B is published")
	require.ErrorIs(t, candidate.err, errDirectCandidateStale)
	require.Zero(t, pool.Snapshot().Active)
}

func TestDirectSettingsPoolSweepUsesPublishedIdleTimeout(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2, idleTimeout: time.Second})
	settingsA := pool.currentRuntimeSettings()
	pool.ApplyRuntimeSettings(settingsA)
	session := dialer.buildSession()
	defer session.Close(context.Background())
	pool.mu.Lock()
	pool.slots["agent-b"] = &directPoolSlot{
		target: "agent-b", active: session, lastUsed: pool.clock.Now().Add(-2 * time.Second),
	}
	pool.mu.Unlock()

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
	pool.beforeSweepLock.Store(&beforeSweepLock)
	pool.afterSweep.Store(&afterSweep)
	pool.signalChanged()
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.IdleTimeout = time.Hour
	pool.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	receiveWithDirectTimeout(t, sweepDone)
	require.Equal(t, 1, pool.Snapshot().Active, "sweep sampled before settings B must honor B after taking owner lock")
}

func TestDirectSettingsPoolAcquireUsesPublishedCapacityBeforeDial(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2, idleTimeout: time.Hour})
	settingsA := pool.currentRuntimeSettings()
	pool.ApplyRuntimeSettings(settingsA)
	existing := &directDialCandidate{target: "agent-existing", done: make(chan struct{})}
	pool.mu.Lock()
	pool.slots[existing.target] = &directPoolSlot{target: existing.target, candidate: existing}
	pool.mu.Unlock()

	beforeLock := make(chan struct{})
	allowLock := make(chan struct{})
	var once sync.Once
	pool.beforeAcquireLock = func() {
		once.Do(func() {
			close(beforeLock)
			<-allowLock
		})
	}
	dialer.releaseError(errors.New("unexpected stale candidate dial"))
	openDone := make(chan error, 1)
	go func() {
		_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
		openDone <- err
	}()
	receiveWithDirectTimeout(t, beforeLock)

	settingsB := settingsA
	settingsB.MaxSessions = 1
	pool.ApplyRuntimeSettings(settingsB)
	close(allowLock)
	require.Error(t, <-openDone)
	require.Zero(t, dialer.calls.Load(), "acquire must not start a candidate that current settings make impossible")
	require.Equal(t, 1, pool.Snapshot().Candidates)
}

func TestDirectSettingsMaxAndIdleReconcileImmediately(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2, idleTimeout: time.Hour})
	for _, id := range []string{"agent-b", "agent-c"} {
		opened := startOpen(pool.DirectSessionPool, t.Context(), target(id))
		dialer.releaseHealthySession()
		require.NoError(t, <-opened.err)
		require.NoError(t, (<-opened.stream).Close())
	}
	require.Equal(t, 2, pool.Snapshot().Active)
	pool.clock.Advance(time.Second)
	pool.ApplyRuntimeSettings(DirectRuntimeSettings{
		Limits: testLimits(4), MaxSessions: 1, IdleTimeout: time.Nanosecond, DrainTimeout: time.Second,
	})
	require.Eventually(t, func() bool { return pool.Snapshot().Active == 0 }, time.Second, time.Millisecond)
}

func TestDirectSettingsReconcileBoundsDrainingSessions(t *testing.T) {
	for _, test := range []struct {
		name            string
		mutate          func(*DirectRuntimeSettings)
		wantAccepting   int
		wantFinalActive int
	}{
		{
			name: "max sessions", wantAccepting: 1, wantFinalActive: 1,
			mutate: func(settings *DirectRuntimeSettings) { settings.MaxSessions = 1 },
		},
		{name: "limits", mutate: func(settings *DirectRuntimeSettings) {
			settings.MaxSessions = 1
			settings.Limits.MaxDataBytes /= 2
		}, wantAccepting: 0, wantFinalActive: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := newBlockingDirectDialer()
			dialer.t = t
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 3, idleTimeout: time.Hour})
			streams := make([]app.AttemptStream, 0, 3)
			for _, agentID := range []string{"agent-b", "agent-c", "agent-d"} {
				opened := startOpen(pool.DirectSessionPool, t.Context(), target(agentID))
				dialer.releaseHealthySession()
				require.NoError(t, <-opened.err)
				streams = append(streams, <-opened.stream)
			}

			settings := pool.currentRuntimeSettings()
			test.mutate(&settings)
			pool.ApplyRuntimeSettings(settings)
			snapshot := pool.Snapshot()
			require.Equal(t, 2, snapshot.Active)
			require.Equal(t, 1, snapshot.Draining)
			require.Equal(t, 3, snapshot.Streams)
			require.Equal(t, 3, snapshot.Sockets)
			pool.mu.Lock()
			accepting := 0
			for _, slot := range pool.slots {
				if slot.active != nil && slot.active.acceptsNew() {
					accepting++
				}
			}
			pool.mu.Unlock()
			require.Equal(t, test.wantAccepting, accepting,
				"sessions waiting for a draining slot must stop new admission immediately")

			for _, stream := range streams {
				require.NoError(t, stream.Close())
			}
			require.Eventually(t, func() bool {
				snapshot := pool.Snapshot()
				return snapshot.Active == test.wantFinalActive && snapshot.Draining == 0
			}, 3*time.Second, 5*time.Millisecond)
		})
	}
}

func TestDirectSettingsReconcileCountsReplacementCandidatesByAdmission(t *testing.T) {
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
				"agent-a": {active: true, candidate: true, age: 2 * time.Second},
				"agent-b": {active: true, candidate: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"agent-a": false, "agent-b": true},
		},
		{
			name: "replacement candidate does not hide oldest active",
			slots: map[string]slotShape{
				"agent-a": {active: true, candidate: true, age: 2 * time.Second},
				"agent-b": {active: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"agent-a": false, "agent-b": true},
		},
		{
			name: "candidate only slot retains projected capacity",
			slots: map[string]slotShape{
				"agent-a": {candidate: true, age: 2 * time.Second},
				"agent-b": {active: true, candidate: true, age: time.Second},
			},
			wantAccepting: map[string]bool{"agent-b": false},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			settings := normalizeDirectRuntimeSettings(DirectRuntimeSettings{
				Limits: testLimits(2), MaxSessions: 1, DrainTimeout: time.Second,
			})
			pool := &DirectSessionPool{
				opts:  DirectSessionPoolOptions{Now: func() time.Time { return now }},
				slots: make(map[string]*directPoolSlot), drains: make(map[*Session]struct{}),
			}
			pool.drains[newSessionValue(nil, 99, settings.Limits, SessionOptions{})] = struct{}{}
			activeSessions := make(map[string]*Session)
			for target, shape := range test.slots {
				slot := &directPoolSlot{
					target: target, lastUsed: now.Add(-shape.age),
					fingerprint: directDesiredFingerprint{Limits: limitsFingerprint(settings.Limits)},
				}
				if shape.active {
					slot.active = newSessionValue(nil, uint64(len(activeSessions)+1), settings.Limits, SessionOptions{})
					require.True(t, slot.active.acquireAdmission())
					activeSessions[target] = slot.active
				}
				if shape.candidate {
					slot.candidate = &directDialCandidate{target: target, done: make(chan struct{})}
				}
				pool.slots[target] = slot
			}
			defer func() {
				for _, session := range activeSessions {
					session.releaseAdmission()
				}
			}()

			require.Empty(t, pool.reconcileLocked(now, settings))
			for target, want := range test.wantAccepting {
				require.Equal(t, want, activeSessions[target].acceptsNew(), target)
			}
			require.Equal(t, 1, pool.projectedOccupiedSlotCountLocked())
			if slot := pool.slots["agent-a"]; slot != nil && !test.slots["agent-a"].active {
				require.NotNil(t, slot.candidate, "candidate-only slot must remain reserved")
			}
		})
	}
}

func newRunningDirectReconcileSession(generation uint64, limits wire.Limits, now time.Time) *Session {
	session := newSessionValue(nil, generation, limits, SessionOptions{Now: func() time.Time { return now }})
	session.state = sessionStateRunning
	session.ctx = context.Background()
	return session
}

func TestDirectSettingsReconcileReactivatesOnlyCapacityPendingAfterCandidateFailure(t *testing.T) {
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
			candidateSlot := &directPoolSlot{
				target: "agent-a", candidate: &directDialCandidate{target: "agent-a", done: make(chan struct{})},
			}
			activeSlot := &directPoolSlot{
				target: "agent-b", active: active, lastUsed: now,
				fingerprint: directDesiredFingerprint{Limits: fingerprint},
			}
			blockingDrain := newSessionValue(nil, 2, settings.Limits, SessionOptions{})
			pool := &DirectSessionPool{
				opts: DirectSessionPoolOptions{Now: func() time.Time { return now }},
				slots: map[string]*directPoolSlot{
					"agent-a": candidateSlot,
					"agent-b": activeSlot,
				},
				drains: map[*Session]struct{}{blockingDrain: {}},
			}

			require.Empty(t, pool.reconcileLocked(now, settings))
			require.True(t, activeSlot.drainPending)
			require.False(t, active.acceptsNew())

			candidateSlot.candidate = nil // candidate completion is rejected as stale at the new cap
			pool.gcSlotLocked("agent-a", candidateSlot)
			require.Empty(t, pool.reconcileLocked(now, settings))
			require.Equal(t, test.wantRecovered, active.acceptsNew())
			require.Equal(t, !test.wantRecovered, activeSlot.drainPending)

			if test.wantRecovered {
				delete(pool.drains, blockingDrain)
				require.Empty(t, pool.reconcileLocked(now, settings))
				require.Same(t, active, activeSlot.active)
				require.True(t, active.acceptsNew())
				require.NotContains(t, pool.drains, active)
			}
		})
	}
}

func TestDirectSessionPoolReplacementOnCredentialChange(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	oldStream := <-first.stream

	// change credential -> desired fingerprint changes -> replacement.
	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	// The old session keeps its in-flight stream; once it completes the old
	// session goes idle and finishes draining.
	require.NoError(t, oldStream.Close())
	require.Eventually(t, func() bool {
		snap := pool.Snapshot()
		return snap.Active == 1 && snap.Draining == 0
	}, 2*time.Second, 5*time.Millisecond)
}

// assertDirectReplacement drives the shared replacement flow: an active is
// established for firstTarget, a replacement trigger is applied, a second Open
// on secondTarget must start a NEW dial (not reuse), and once the old session's
// in-flight stream completes the old active drains losslessly to a single
// healthy active.
func assertDirectReplacement(
	t *testing.T, pool *testPool, dialer *blockingDirectDialer,
	firstTarget agentproxy.DirectSessionTarget, mutate func(), secondTarget agentproxy.DirectSessionTarget,
) {
	t.Helper()
	first := startOpen(pool.DirectSessionPool, t.Context(), firstTarget)
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	oldStream := <-first.stream

	mutate()

	second := startOpen(pool.DirectSessionPool, t.Context(), secondTarget)
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond,
		"replacement did not trigger a new dial")
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	// The old session keeps its admitted stream until it completes, then drains.
	require.NoError(t, oldStream.Close())
	require.Eventually(t, func() bool {
		snap := pool.Snapshot()
		return snap.Active == 1 && snap.Draining == 0
	}, 2*time.Second, 5*time.Millisecond)
}

func TestDirectSessionPoolReplacementOnURLChange(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	assertDirectReplacement(t, pool, dialer,
		directTargetWithURL("agent-b", "https://agent-b.example:8443"),
		func() {},
		directTargetWithURL("agent-b", "https://agent-b.example:9443"),
	)
}

func TestDirectSessionPoolPreparedAddressHotUpdateReplacesSession(t *testing.T) {
	firstListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	secondListener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = firstListener.Close()
		_ = secondListener.Close()
	})
	agentID := fmt.Sprintf("agent-cache-%d", time.Now().UnixNano())
	prepare := func(listener net.Listener) agentproxy.DirectSessionTarget {
		t.Helper()
		addresses, marshalErr := json.Marshal([]agentproxy.Address{{URL: "http://" + listener.Addr().String()}})
		require.NoError(t, marshalErr)
		prepared, prepareErr := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
			AgentID: agentID, HTTPAddresses: string(addresses),
		})
		require.NoError(t, prepareErr)
		return prepared
	}

	firstTarget := prepare(firstListener)
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Hour})
	first := startOpen(pool.DirectSessionPool, t.Context(), firstTarget)
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	firstStream := <-first.stream
	require.NoError(t, firstListener.Close())

	secondTarget := prepare(secondListener)
	require.NotEqual(t, firstTarget.AddressFingerprint, secondTarget.AddressFingerprint)
	require.Equal(t, "http://"+secondListener.Addr().String(), secondTarget.WebSocketURL.String())
	second := startOpen(pool.DirectSessionPool, t.Context(), secondTarget)
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	secondStream := <-second.stream

	require.NoError(t, firstStream.Close())
	require.NoError(t, secondStream.Close())
}

func TestDirectSessionPoolReplacementOnProxyChange(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	assertDirectReplacement(t, pool, dialer,
		directTarget("agent-b"), // no proxy
		func() {},
		directTargetWithProxy("agent-b", "http://proxy.example:1080"),
	)
}

func TestDirectSessionPoolReplacementOnEffectiveProxyChange(t *testing.T) {
	for _, test := range []struct {
		name   string
		before string
		after  string
	}{
		{name: "username", before: "http://alice:pass@proxy.example:8080/path?mode=one", after: "http://bob:pass@proxy.example:8080/path?mode=one"},
		{name: "password", before: "http://alice:first@proxy.example:8080/path?mode=one", after: "http://alice:second@proxy.example:8080/path?mode=one"},
		{name: "password presence", before: "http://alice@proxy.example:8080/path?mode=one", after: "http://alice:@proxy.example:8080/path?mode=one"},
		{name: "query", before: "http://alice:pass@proxy.example:8080/path?mode=one", after: "http://alice:pass@proxy.example:8080/path?mode=two"},
	} {
		t.Run(test.name, func(t *testing.T) {
			dialer := newBlockingDirectDialer()
			dialer.t = t
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
			assertDirectReplacement(t, pool, dialer,
				directTargetWithProxy("agent-b", test.before),
				func() {},
				directTargetWithProxy("agent-b", test.after),
			)
		})
	}
}

func TestDirectSessionPoolReusesCanonicalEquivalentProxy(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Hour})
	firstTarget := directTargetWithProxy(
		"agent-b",
		"http://User:pa%73s@PROXY.EXAMPLE:080/route%7E?b=two&a=one%20value#first",
	)
	secondTarget := directTargetWithProxy(
		"agent-b",
		"HTTP://User:pass@proxy.example/route~?a=one+value&b=two#second",
	)
	first := startOpen(pool.DirectSessionPool, t.Context(), firstTarget)
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	require.NoError(t, (<-first.stream).Close())
	require.Eventually(t, func() bool { return pool.Snapshot().Streams == 0 }, time.Second, time.Millisecond)

	second := startOpen(pool.DirectSessionPool, t.Context(), secondTarget)
	select {
	case err := <-second.err:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("canonical equivalent proxy started a replacement dial")
	}
	require.Equal(t, int64(1), dialer.calls.Load())
	require.NoError(t, (<-second.stream).Close())
}

func TestDirectSessionPoolReusesDialEquivalentSocksProxyScheme(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Hour})
	first := startOpen(pool.DirectSessionPool, t.Context(), directTargetWithProxy(
		"agent-b", "socks5://user:pass@PROXY.EXAMPLE:1080",
	))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	require.NoError(t, (<-first.stream).Close())

	second := startOpen(pool.DirectSessionPool, t.Context(), directTargetWithProxy(
		"agent-b", "SOCKS5H://user:pass@proxy.example:1080",
	))
	select {
	case err := <-second.err:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("dial-equivalent socks proxy scheme started a replacement")
	}
	require.Equal(t, int64(1), dialer.calls.Load())
	require.NoError(t, (<-second.stream).Close())
}

func TestDirectSessionPoolProxyFingerprintDoesNotExposeSecrets(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	pool := newTestDirectPool(t, nil, poolLimits{maxSessions: 4})
	pool.logs = newDirectLogs(zap.New(core), diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}))
	target := directTargetWithProxy(
		"agent-b",
		"http://proxy-user-secret:proxy-password-secret@proxy.example:8080/path?token=proxy-query-secret",
	)

	key, _, err := pool.desiredKey(target)
	require.NoError(t, err)
	_, openErr := pool.OpenAttemptStream(t.Context(), target, attemptRequest())
	require.Error(t, openErr)
	require.Len(t, key.Proxy, sha256.Size*2)

	encoded := fmt.Sprint(key, openErr, pool.Snapshot(), observed.All())
	for _, secret := range []string{
		"proxy-user-secret", "proxy-password-secret", "proxy-query-secret", "proxy.example",
	} {
		require.NotContains(t, encoded, secret)
	}
}

func TestDirectSessionPoolReplacementOnLimitsChange(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	assertDirectReplacement(t, pool, dialer,
		target("agent-b"),
		func() { pool.setLimits(testLimits(32)) },
		target("agent-b"),
	)
}

func TestDirectSessionPoolReplacementFailureKeepsHealthyActive(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)

	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseError(errors.New("replacement dial failed"))
	// replacement failed but the old healthy active is borrowed instead.
	require.NoError(t, <-second.err)
	require.NotNil(t, <-second.stream)
	require.Equal(t, 1, pool.Snapshot().Active)
}

func TestDirectSessionPoolReplacementFailureReportsBorrowedTransport(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	firstTarget := directTargetWithURL("agent-b", "https://first.example:8443")
	firstTarget.AddressFingerprint = "fp-first"
	firstTransport, err := pool.BuildDirectAttemptTransport(t.Context(), firstTarget)
	require.NoError(t, err)
	dialer.releaseHealthySession()
	firstReservation, firstStream := acquireAndOpenDirectAttemptStream(t, firstTransport)
	require.Equal(t, firstTransport.TransportIdentity(), firstReservation.TransportIdentity())
	require.Equal(t, firstTarget.AddressFingerprint, firstReservation.AddressFingerprint())

	secondTarget := directTargetWithURL("agent-b", "https://second.example:9443")
	secondTarget.AddressFingerprint = "fp-second"
	secondTransport, err := pool.BuildDirectAttemptTransport(t.Context(), secondTarget)
	require.NoError(t, err)
	require.NotEqual(t, firstTransport.TransportIdentity(), secondTransport.TransportIdentity())
	dialer.releaseError(errors.New("replacement dial failed"))
	secondReservation, secondStream := acquireAndOpenDirectAttemptStream(t, secondTransport)

	require.Equal(t, firstTransport.TransportIdentity(), secondReservation.TransportIdentity())
	require.Equal(t, firstTarget.AddressFingerprint, secondReservation.AddressFingerprint())
	require.NotEqual(t, secondTransport.TransportIdentity(), secondReservation.TransportIdentity())
	require.NoError(t, secondStream.Close())
	require.NoError(t, firstStream.Close())
}

func TestDirectSessionPoolReplacementFailureAtCapacityKeepsHealthyActive(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	oldStream := <-first.stream

	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseError(errors.New("replacement dial failed"))
	require.NoError(t, <-second.err)
	newStream := <-second.stream
	require.Equal(t, 1, pool.Snapshot().Active)
	require.Zero(t, pool.Snapshot().Draining)

	require.NoError(t, newStream.Close())
	require.NoError(t, oldStream.Close())
}

func TestDirectSessionPoolPromotionRechecksDrainingCapacity(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})
	settings := pool.currentRuntimeSettings()
	target := target("agent-b")
	key, _, err := pool.desiredKey(target)
	require.NoError(t, err)
	candidate := &directDialCandidate{
		target: target.TargetAgentID, key: key, frozen: target, done: make(chan struct{}),
		fingerprint: directDesiredFingerprint{Limits: limitsFingerprint(settings.Limits)},
	}
	oldSession := dialer.buildSession()
	newSession := dialer.buildSession()
	blockingDrain := dialer.buildSession()
	defer oldSession.Close(context.Background())
	defer newSession.Close(context.Background())
	defer blockingDrain.Close(context.Background())
	pool.mu.Lock()
	pool.slots[target.TargetAgentID] = &directPoolSlot{
		target: target.TargetAgentID, active: oldSession, candidate: candidate,
	}
	pool.drains[blockingDrain] = struct{}{}
	pool.mu.Unlock()

	require.False(t, pool.finishCandidate(candidate, newSession, nil))
	require.True(t, IsDirectCapacityRejection(candidate.err), "want capacity rejection, got %v", candidate.err)
	pool.mu.Lock()
	require.Same(t, oldSession, pool.slots[target.TargetAgentID].active)
	require.Len(t, pool.drains, 1)
	pool.mu.Unlock()
}

func TestDirectSessionPoolReplacementAtFullDrainFallsBackToHealthyActive(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})

	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	firstStream := <-first.stream

	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	secondStream := <-second.stream
	require.Eventually(t, func() bool { return pool.Snapshot().Draining == 1 }, time.Second, time.Millisecond)

	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-c"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	third := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.NoError(t, <-third.err)
	thirdStream := <-third.stream
	require.Equal(t, int64(2), dialer.calls.Load(), "a full drain registry must defer replacement without rejecting healthy reuse")

	require.NoError(t, thirdStream.Close())
	require.NoError(t, secondStream.Close())
	require.NoError(t, firstStream.Close())
}

func TestDirectSessionPoolCapacityFallbackReportsBorrowedTransport(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})
	firstTarget := directTargetWithURL("agent-b", "https://first.example:8443")
	firstTarget.AddressFingerprint = "fp-first"
	firstTransport, err := pool.BuildDirectAttemptTransport(t.Context(), firstTarget)
	require.NoError(t, err)
	dialer.releaseHealthySession()
	firstReservation, firstStream := acquireAndOpenDirectAttemptStream(t, firstTransport)
	require.Equal(t, firstTransport.TransportIdentity(), firstReservation.TransportIdentity())
	require.Equal(t, firstTarget.AddressFingerprint, firstReservation.AddressFingerprint())

	secondTarget := directTargetWithURL("agent-b", "https://second.example:9443")
	secondTarget.AddressFingerprint = "fp-second"
	secondTransport, err := pool.BuildDirectAttemptTransport(t.Context(), secondTarget)
	require.NoError(t, err)
	dialer.releaseHealthySession()
	secondReservation, secondStream := acquireAndOpenDirectAttemptStream(t, secondTransport)
	require.Equal(t, secondTransport.TransportIdentity(), secondReservation.TransportIdentity())
	require.Equal(t, secondTarget.AddressFingerprint, secondReservation.AddressFingerprint())
	require.Eventually(t, func() bool { return pool.Snapshot().Draining == 1 }, time.Second, time.Millisecond)

	thirdTarget := directTargetWithURL("agent-b", "https://third.example:10443")
	thirdTarget.AddressFingerprint = "fp-third"
	thirdTransport, err := pool.BuildDirectAttemptTransport(t.Context(), thirdTarget)
	require.NoError(t, err)
	thirdReservation, thirdStream := acquireAndOpenDirectAttemptStream(t, thirdTransport)

	require.Equal(t, int64(2), dialer.calls.Load(), "capacity fallback must not start a third dial")
	require.Equal(t, secondTransport.TransportIdentity(), thirdReservation.TransportIdentity())
	require.Equal(t, secondTarget.AddressFingerprint, thirdReservation.AddressFingerprint())
	require.NotEqual(t, thirdTransport.TransportIdentity(), thirdReservation.TransportIdentity())
	require.NoError(t, thirdStream.Close())
	require.NoError(t, secondStream.Close())
	require.NoError(t, firstStream.Close())
}

func TestDirectSessionPoolIdleTimeoutBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		elapsed time.Duration
		evicted bool
	}{
		{name: "before", elapsed: 30 * time.Second, evicted: false},
		{name: "equal", elapsed: time.Minute, evicted: true},
		{name: "after", elapsed: 90 * time.Second, evicted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dialer := newBlockingDirectDialer()
			dialer.t = t
			pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Minute})
			first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
			dialer.releaseHealthySession()
			require.NoError(t, <-first.err)
			require.NoError(t, (<-first.stream).Close())
			require.Eventually(t, func() bool { return pool.Snapshot().Streams == 0 }, time.Second, time.Millisecond)
			require.Equal(t, 1, pool.Snapshot().Active)

			pool.clock.Advance(tc.elapsed)
			if tc.evicted {
				require.Eventually(t, func() bool { return pool.Snapshot().Active == 0 }, 2*time.Second, 5*time.Millisecond)
			} else {
				require.Never(t, func() bool { return pool.Snapshot().Active == 0 }, 200*time.Millisecond, 20*time.Millisecond)
			}
		})
	}
}

func TestDirectSessionPoolIdleTimeoutStartsAfterLastStream(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4, idleTimeout: time.Minute})
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	stream := <-first.stream

	pool.clock.Advance(2 * time.Minute)
	require.Never(t, func() bool { return pool.Snapshot().Active != 1 }, 200*time.Millisecond, 10*time.Millisecond)

	require.NoError(t, stream.Close())
	require.Eventually(t, func() bool { return pool.Snapshot().Streams == 0 }, time.Second, time.Millisecond)
	pool.clock.Advance(time.Minute - time.Nanosecond)
	require.Never(t, func() bool { return pool.Snapshot().Active == 0 }, 100*time.Millisecond, 10*time.Millisecond)
	pool.clock.Advance(time.Nanosecond)
	require.Eventually(t, func() bool { return pool.Snapshot().Active == 0 }, time.Second, time.Millisecond)
}

func TestDirectSessionPoolIdleLRUEvictionAtCapacity(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})

	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	require.NoError(t, (<-first.stream).Close()) // complete the stream -> active goes idle
	require.Equal(t, 1, pool.Snapshot().Active)

	// capacity is 1; opening a second, idle-but-different target must evict
	// the oldest idle active to make room.
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-c"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	require.Eventually(t, func() bool {
		snap := pool.Snapshot()
		return snap.Active == 1 && snap.Draining == 0
	}, 2*time.Second, 5*time.Millisecond)
}

func TestDirectSessionPoolIdleLRUEvictsLongestActuallyIdleSession(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 2, idleTimeout: time.Hour})

	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-a"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	firstStream := <-first.stream

	pool.clock.Advance(time.Second)
	second := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-second.err)
	require.NoError(t, (<-second.stream).Close())

	pool.clock.Advance(time.Second)
	require.NoError(t, firstStream.Close())

	third := startOpen(pool.DirectSessionPool, t.Context(), target("agent-c"))
	dialer.releaseHealthySession()
	require.NoError(t, <-third.err)
	require.NoError(t, (<-third.stream).Close())

	pool.mu.Lock()
	firstSlot := pool.slots["agent-a"]
	secondSlot := pool.slots["agent-b"]
	pool.mu.Unlock()
	require.NotNil(t, firstSlot)
	require.NotNil(t, firstSlot.active, "the session that only just became idle must be retained")
	require.True(t, secondSlot == nil || secondSlot.active == nil, "the longest actually idle session must be evicted")
}

func TestDirectSessionPoolCapacityRejectionWhenDrainingFull(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 1, idleTimeout: time.Hour})

	// Build one busy active with an open (non-idle) stream.
	first := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	dialer.releaseHealthySession()
	require.NoError(t, <-first.err)
	require.NotNil(t, <-first.stream) // keep the stream open -> active is not idle

	// Force the active into draining by requesting a replacement (credential change).
	pool.creds.set(agentauthcache.ForwardCredential{
		Ticket: pkgagentauth.ForwardTicket("ticket-b"), ExpiresAt: pool.clock.Now().Add(time.Hour),
	}, nil)
	replacement := startOpen(pool.DirectSessionPool, t.Context(), target("agent-b"))
	require.Eventually(t, func() bool { return dialer.calls.Load() == 2 }, time.Second, time.Millisecond)
	dialer.releaseHealthySession()
	require.NoError(t, <-replacement.err)
	require.Eventually(t, func() bool {
		snap := pool.Snapshot()
		return snap.Active == 1 && snap.Draining == 1
	}, 2*time.Second, 5*time.Millisecond)

	// active(1) + draining(1) both at the cap of 1: a new key must be rejected.
	_, err := pool.OpenAttemptStream(t.Context(), target("agent-d"), directAttemptRequest("agent-d"))
	require.Error(t, err)
	require.True(t, IsDirectCapacityRejection(err), "want capacity rejection, got %v", err)
}

func TestDirectSessionPoolCredentialUnavailable(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	pool.creds.set(agentauthcache.ForwardCredential{}, errors.New("no credential"))
	_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	require.Error(t, err)
	require.False(t, IsDirectCapacityRejection(err))
	require.Equal(t, int64(0), dialer.calls.Load())
}

func TestDirectSessionPoolClosedRejectsOpen(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, pool.Close(ctx))
	<-pool.Done()
	_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	require.Error(t, err)
	require.ErrorIs(t, err, errDirectPoolClosed)
	requireDirectOpenFailure(t, err, "pool", "direct_closed", false)
}

func TestDirectSessionPoolDrainingRejectsOpenWithoutCountingForCircuit(t *testing.T) {
	dialer := newBlockingDirectDialer()
	dialer.t = t
	pool := newTestDirectPool(t, dialer, poolLimits{maxSessions: 4})
	require.NoError(t, pool.Drain(t.Context()))

	_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	require.ErrorIs(t, err, errDirectPoolDraining)
	requireDirectOpenFailure(t, err, "pool", "direct_closed", false)
	require.Zero(t, dialer.calls.Load())
}

func TestDirectSessionPoolNilDialerIsPolicyDisabled(t *testing.T) {
	pool := newTestDirectPool(t, nil, poolLimits{maxSessions: 4})

	_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	require.Error(t, err)
	requireDirectOpenFailure(t, err, "pool", "policy_disabled", false)
}

func TestDirectSessionPoolAttemptEnforcesSourceOutboundButProbeBypassesBusinessGate(t *testing.T) {
	pool := newTestDirectPool(t, nil, poolLimits{maxSessions: 4})
	pool.opts.DirectOutboundEnabled = func() bool { return false }

	_, err := pool.OpenAttemptStream(t.Context(), target("agent-b"), attemptRequest())
	requirePolicyOpenFailure(t, err, consts.RouteErrorSourceDirectOutboundDisabled)

	_, err = pool.OpenProbeStream(t.Context(), target("agent-b"), agentproxy.ProbeStreamRequest{
		Policy: wire.ProbeBypassBusinessPolicy, TargetAgentID: "agent-b", Remaining: time.Second,
	})
	requireDirectOpenFailure(t, err, "pool", "policy_disabled", false)
}

func TestDirectSessionPoolCandidateClosedRaceDoesNotCountForCircuit(t *testing.T) {
	pool := newTestDirectPool(t, newBlockingDirectDialer(), poolLimits{maxSessions: 4})
	candidate := &directDialCandidate{
		frozen: target("agent-b"), done: make(chan struct{}), err: errDirectPoolClosed,
	}
	close(candidate.done)

	_, err := pool.waitCandidate(t.Context(), candidate)
	require.ErrorIs(t, err, errDirectPoolClosed)
	requireDirectOpenFailure(t, err, "pool", "direct_closed", false)
}

func TestDirectSessionPoolCandidateFailureCountsForCircuitOnce(t *testing.T) {
	candidate := &directDialCandidate{
		frozen: target("agent-b"), done: make(chan struct{}),
		err: directCandidateFailure(
			directEndpointError("dial", "failed", "wss://agent-b.example"+DirectTunnelPath),
			"wss://agent-b.example"+DirectTunnelPath,
		),
	}
	close(candidate.done)

	countingClaims := 0
	for range 4 {
		_, err := (&DirectSessionPool{done: make(chan struct{})}).waitCandidate(t.Context(), candidate)
		var failure interface{ CountsForCircuit() bool }
		require.ErrorAs(t, err, &failure)
		if failure.CountsForCircuit() {
			countingClaims++
		}
	}
	require.Equal(t, 1, countingClaims)
}

func requireDirectOpenFailure(t *testing.T, err error, stage, code string, counts bool) {
	t.Helper()
	var failure interface {
		Stage() string
		ReasonCode() string
		CountsForCircuit() bool
	}
	require.ErrorAs(t, err, &failure)
	require.Equal(t, stage, failure.Stage())
	require.Equal(t, code, failure.ReasonCode())
	require.Equal(t, counts, failure.CountsForCircuit())
}
