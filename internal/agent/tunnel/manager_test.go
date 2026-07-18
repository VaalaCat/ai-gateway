package tunnel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestManagerCandidateFailureLogsAreSuppressedAndSanitized(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	core, observed := observer.New(zap.DebugLevel)
	manager := NewManager(ManagerOptions{
		SourceID: "agent-a", Logger: zap.New(core), Now: func() time.Time { return now },
		Suppressor: diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute}),
	})
	manager.desired = Desired{Mode: "custom", EffectiveURI: "wss://user:pass@relay.example/ws?token=secret"}
	manager.desiredGen = 1
	failure := candidateResult{
		attempt: 1, gen: 1, uri: manager.desired.EffectiveURI,
		err: errors.New("dial wss://user:pass@relay.example/ws?token=secret failed with secret credential"),
	}

	for range 2 {
		manager.candidate = managerSlot{attempt: 1, desiredGen: 1, uri: failure.uri}
		manager.handleCandidateResult(failure)
	}
	require.Equal(t, 1, observed.Len())
	require.Equal(t, zap.WarnLevel, observed.All()[0].Level)
	require.Equal(t, "redacted", observed.All()[0].ContextMap()["error"])

	now = now.Add(time.Minute)
	manager.candidate = managerSlot{attempt: 1, desiredGen: 1, uri: failure.uri}
	manager.handleCandidateResult(failure)
	require.Equal(t, 2, observed.Len())
	require.EqualValues(t, 1, observed.All()[1].ContextMap()["suppressed_count"])
	for _, entry := range observed.All() {
		require.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), "token=secret")
		require.NotContains(t, entry.Message+fmt.Sprint(entry.ContextMap()), "user:pass")
	}
}

func TestSanitizeManagerErrorFailsClosedAfterURIRedaction(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		uri    string
		want   string
		absent []string
	}{
		{
			name: "unrelated authorization secret", err: errors.New("Authorization: Bearer unrelated-secret"),
			uri: "wss://relay.example/ws", want: "redacted", absent: []string{"Authorization", "Bearer", "unrelated-secret"},
		},
		{
			name: "unrelated websocket userinfo", err: errors.New("dial wss://user:pass@other.example/ws"),
			uri: "wss://relay.example/ws", want: "redacted", absent: []string{"user:pass", "other.example"},
		},
		{name: "safe transport error", err: errors.New("connection refused"), uri: "wss://relay.example/ws", want: "connection refused"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := sanitizeManagerError(test.err, test.uri)
			require.Equal(t, test.want, got)
			for _, secret := range test.absent {
				require.NotContains(t, got, secret)
			}
		})
	}
}

func TestManagerAuthAndProtocolCandidateFailuresLogError(t *testing.T) {
	for _, test := range []struct {
		name    string
		failure error
	}{
		{name: "auth", failure: errors.New("relay authentication failed")},
		{name: "protocol", failure: errors.New("relay protocol mismatch")},
	} {
		t.Run(test.name, func(t *testing.T) {
			core, observed := observer.New(zap.DebugLevel)
			manager := NewManager(ManagerOptions{SourceID: "agent-a", Logger: zap.New(core)})
			manager.desired = Desired{Mode: "custom", EffectiveURI: "wss://relay.example/ws"}
			manager.desiredGen = 1
			manager.candidate = managerSlot{attempt: 1, desiredGen: 1, uri: manager.desired.EffectiveURI}
			manager.handleCandidateResult(candidateResult{
				attempt: 1, gen: 1, uri: manager.desired.EffectiveURI, err: test.failure,
			})
			require.Equal(t, 1, observed.Len())
			require.Equal(t, zap.ErrorLevel, observed.All()[0].Level)
		})
	}
}

func TestManagerSuccessfulCandidateRecoversSuppressedAuthFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	core, observed := observer.New(zap.DebugLevel)
	suppressor := diagnostics.NewSuppressor(diagnostics.SuppressorOptions{Window: time.Minute})
	manager := NewManager(ManagerOptions{
		SourceID: "agent-a", Logger: zap.New(core), Limits: testLimits(1),
		Now: func() time.Time { return now }, Suppressor: suppressor,
	})
	manager.desired = Desired{Mode: "custom", EffectiveURI: "wss://relay.example/ws"}
	manager.desiredGen = 1
	failure := candidateResult{
		attempt: 1, gen: 1, uri: manager.desired.EffectiveURI,
		err: errors.New("relay authentication failed for ticket secret-ticket"),
	}

	for range 2 {
		manager.candidate = managerSlot{attempt: 1, desiredGen: 1, uri: failure.uri}
		manager.handleCandidateResult(failure)
	}
	require.Equal(t, 1, observed.Len(), "the duplicate auth failure should be suppressed")
	require.Equal(t, zap.ErrorLevel, observed.All()[0].Level)
	authKey := diagnostics.SuppressionKey{
		Source: "agent-a", Target: "master", PathKind: "relay", Stage: "candidate", ReasonCode: "relay_auth",
	}
	require.True(t, suppressor.Contains(authKey))

	candidate := newSession(newMemorySessionConn(), 34, testLimits(1), SessionOptions{})
	candidateCtx, cancelCandidate := context.WithCancel(t.Context())
	candidateDone := make(chan error, 1)
	go func() { candidateDone <- candidate.Run(candidateCtx) }()
	<-candidate.started
	t.Cleanup(func() {
		cancelCandidate()
		<-candidateDone
	})
	manager.candidate = managerSlot{attempt: 2, desiredGen: 1, uri: failure.uri}
	manager.handleCandidateResult(candidateResult{
		attempt: 2, gen: 1, uri: failure.uri, session: candidate,
	})

	recovery := observed.FilterMessage("relay operation recovered").All()
	require.Len(t, recovery, 1)
	require.Equal(t, zap.InfoLevel, recovery[0].Level)
	require.Equal(t, "relay_auth", recovery[0].ContextMap()["reason_code"])
	require.EqualValues(t, 1, recovery[0].ContextMap()["suppressed_count"])
	require.False(t, suppressor.Contains(authKey))
	for _, entry := range observed.All() {
		text := entry.Message + fmt.Sprint(entry.ContextMap())
		require.NotContains(t, text, "secret-ticket")
		require.NotContains(t, text, "ticket ")
	}
}

func TestManagerConfigTransitionLogsInfoWithoutURI(t *testing.T) {
	core, observed := observer.New(zap.DebugLevel)
	manager := NewManager(ManagerOptions{SourceID: "agent-a", Logger: zap.New(core)})
	reply := make(chan uint64, 1)
	manager.handleApply(applyCommand{
		desired: Desired{Mode: "custom", ConfiguredURI: "wss://relay.example/ws?token=secret", EffectiveURI: "wss://relay.example/ws?token=secret"},
		reply:   reply,
	})

	require.Equal(t, uint64(1), <-reply)
	require.Equal(t, 1, observed.Len())
	require.Equal(t, zap.InfoLevel, observed.All()[0].Level)
	require.Equal(t, "relay config changed", observed.All()[0].Message)
	require.NotContains(t, fmt.Sprint(observed.All()[0].ContextMap()), "relay.example")
}

func TestManagerSnapshotProjectsOnlyActiveSessionRecentErrors(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	old := newSessionValue(nil, 1, testLimits(1), SessionOptions{})
	current := newSessionValue(nil, 2, testLimits(1), SessionOptions{})
	old.recordError(diagnostics.Event{Code: "stale", Stage: "read", At: time.Unix(1, 0)})
	current.recordError(diagnostics.Event{Code: "current", Stage: "read", At: time.Unix(2, 0)})
	manager.active = managerSlot{session: current, desiredGen: 1}
	manager.activeRef.Store(current)
	manager.refreshSnapshot()

	snapshot := manager.Snapshot()
	require.Len(t, snapshot.RecentErrors, 1)
	require.Equal(t, "current", snapshot.RecentErrors[0].Code)
	snapshot.RecentErrors[0].Code = "mutated"
	old.recordError(diagnostics.Event{Code: "stale-late", Stage: "read", At: time.Unix(3, 0)})

	again := manager.Snapshot()
	require.Len(t, again.RecentErrors, 1)
	require.Equal(t, "current", again.RecentErrors[0].Code)
}

type nilManagerDialer struct{ marker string }

func (d *nilManagerDialer) Dial(context.Context, string, agentauth.RelayTicket, uint64) (*Session, error) {
	return nil, errors.New(d.marker)
}

type nilManagerTickets struct{ marker string }

func (p *nilManagerTickets) RelayTicket(context.Context, uint64) (agentauth.RelayTicket, error) {
	return "", errors.New(p.marker)
}

type managerTicketStub struct{}

func (managerTicketStub) RelayTicket(context.Context, uint64) (agentauth.RelayTicket, error) {
	return agentauth.RelayTicket("ticket"), nil
}

type managerDialResult struct {
	session *Session
	err     error
}

type managerDialCall struct {
	ctx        context.Context
	rawURI     string
	generation uint64
}

type managerDialerStub struct {
	mu              sync.Mutex
	results         chan managerDialResult
	calls           chan managerDialCall
	runtimeSettings func() (wire.Limits, time.Duration)
}

func (d *managerDialerStub) managerRuntimeSettings() (wire.Limits, time.Duration) {
	if d.runtimeSettings == nil {
		return wire.Limits{}, 0
	}
	return d.runtimeSettings()
}

type blockingControlSessionConn struct {
	*memorySessionConn
	controlStarted chan struct{}
	startOnce      sync.Once
}

type blockingCloseSessionConn struct {
	*memorySessionConn
	closeStarted chan struct{}
	releaseClose chan struct{}
	startOnce    sync.Once
	releaseOnce  sync.Once
	closeCalls   atomic.Int32
}

func (c *blockingCloseSessionConn) release() {
	c.releaseOnce.Do(func() { close(c.releaseClose) })
}

func newBlockingCloseSessionConn() *blockingCloseSessionConn {
	return &blockingCloseSessionConn{
		memorySessionConn: newMemorySessionConn(),
		closeStarted:      make(chan struct{}),
		releaseClose:      make(chan struct{}),
	}
}

func (c *blockingCloseSessionConn) Close() error {
	c.closeCalls.Add(1)
	c.startOnce.Do(func() { close(c.closeStarted) })
	<-c.releaseClose
	return c.memorySessionConn.Close()
}

func newBlockingControlSessionConn() *blockingControlSessionConn {
	return &blockingControlSessionConn{
		memorySessionConn: newMemorySessionConn(),
		controlStarted:    make(chan struct{}),
	}
}

func (c *blockingControlSessionConn) WriteControl(int, []byte, time.Time) error {
	c.startOnce.Do(func() { close(c.controlStarted) })
	<-c.closed
	return errSessionClosed
}

func newManagerDialerStub() *managerDialerStub {
	return &managerDialerStub{
		results: make(chan managerDialResult, 8),
		calls:   make(chan managerDialCall, 8),
	}
}

func (d *managerDialerStub) Dial(ctx context.Context, rawURI string, _ agentauth.RelayTicket, generation uint64) (*Session, error) {
	d.calls <- managerDialCall{ctx: ctx, rawURI: rawURI, generation: generation}
	select {
	case result := <-d.results:
		return result.session, result.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func TestManagerPublicMethodSetIsLocked(t *testing.T) {
	typeOfManager := reflect.TypeOf((*Manager)(nil))
	methods := make([]string, 0, typeOfManager.NumMethod())
	for i := 0; i < typeOfManager.NumMethod(); i++ {
		methods = append(methods, typeOfManager.Method(i).Name)
	}
	require.Equal(t, []string{
		"Apply", "Close", "Disconnect", "Done", "Drain", "OpenStream", "Reconnect", "Run", "Snapshot",
	}, methods)
}

func TestManagerApplyUsesExactASCIIWhitespaceForRelayURIs(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })

	manager.Apply(Desired{
		Mode:          "custom",
		ConfiguredURI: "\vwss://relay.example/configured\v",
		EffectiveURI:  "\vwss://relay.example/effective\v",
	})
	require.Equal(t, Desired{
		Mode:          "custom",
		ConfiguredURI: "wss://relay.example/configured",
		EffectiveURI:  "wss://relay.example/effective",
	}, manager.Snapshot().Desired)

	manager.Apply(Desired{
		Mode:          "custom",
		ConfiguredURI: "\u00a0wss://relay.example/configured\u00a0",
		EffectiveURI:  "\u00a0wss://relay.example/effective\u00a0",
	})
	require.Equal(t, Desired{
		Mode:          "custom",
		ConfiguredURI: "\u00a0wss://relay.example/configured\u00a0",
		EffectiveURI:  "\u00a0wss://relay.example/effective\u00a0",
	}, manager.Snapshot().Desired)
}

func TestManagerOptionsFieldSetIsLocked(t *testing.T) {
	typeOfOptions := reflect.TypeOf(ManagerOptions{})
	fields := make([]string, 0, typeOfOptions.NumField())
	for i := 0; i < typeOfOptions.NumField(); i++ {
		fields = append(fields, typeOfOptions.Field(i).Name)
	}
	require.Equal(t, []string{
		"SourceID", "Dialer", "Tickets", "Limits", "DrainTimeout", "BackoffMin", "BackoffMax", "Logger", "Now", "Suppressor",
	}, fields)
}

func TestManagerApplyUsesMonotonicDesiredGenerationAndCancelsOlderDial(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(4)})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()

	firstGeneration := manager.Apply(Desired{Mode: "custom", ConfiguredURI: "ws://relay-a/ws", EffectiveURI: "ws://relay-a/ws"})
	first := <-dialer.calls
	require.Equal(t, firstGeneration, first.generation)
	require.Equal(t, "ws://relay-a/ws", first.rawURI)

	secondGeneration := manager.Apply(Desired{Mode: "custom", ConfiguredURI: "ws://relay-b/ws", EffectiveURI: "ws://relay-b/ws"})
	require.Greater(t, secondGeneration, firstGeneration)
	select {
	case <-first.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("newer Apply did not cancel the older dial")
	}
	second := <-dialer.calls
	require.Equal(t, secondGeneration, second.generation)

	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
	select {
	case <-manager.Done():
	default:
		t.Fatal("manager Done is open")
	}
}

func TestManagerCandidateFailureKeepsActiveUntilReadyReplacement(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(4),
		BackoffMin: time.Hour, BackoffMax: time.Hour,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()

	old := newSession(newMemorySessionConn(), 101, testLimits(4), SessionOptions{})
	dialer.results <- managerDialResult{session: old}
	firstGeneration := manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay-a/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool {
		snapshot := manager.Snapshot()
		return snapshot.ActiveGeneration == firstGeneration && snapshot.SessionGeneration == 101 && snapshot.AcceptingNewStreams
	}, time.Second, time.Millisecond)

	dialer.results <- managerDialResult{err: errors.New("authentication failed")}
	secondGeneration := manager.Apply(Desired{Mode: "custom", EffectiveURI: "wss://relay-b/ws?token=secret"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().LastError != "" }, time.Second, time.Millisecond)
	failed := manager.Snapshot()
	require.Equal(t, firstGeneration, failed.ActiveGeneration)
	require.True(t, failed.AcceptingNewStreams)
	require.NotContains(t, failed.LastError, "secret")

	replacement := newSession(newMemorySessionConn(), 202, testLimits(4), SessionOptions{})
	dialer.results <- managerDialResult{session: replacement}
	require.NoError(t, manager.Reconnect(t.Context()))
	require.Eventually(t, func() bool {
		snapshot := manager.Snapshot()
		return snapshot.ActiveGeneration == secondGeneration && snapshot.SessionGeneration == 202
	}, time.Second, time.Millisecond)
	select {
	case <-old.Done():
	case <-time.After(time.Second):
		t.Fatal("promoted replacement did not drain the old active session")
	}

	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerDisabledStopsAdmissionAndConvergesWithoutRetry(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(4)})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()

	active := newSession(newMemorySessionConn(), 303, testLimits(4), SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "inherit", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().AcceptingNewStreams }, time.Second, time.Millisecond)

	disabledGeneration := manager.Apply(Desired{Mode: "disabled"})
	require.Eventually(t, func() bool {
		snapshot := manager.Snapshot()
		return snapshot.DesiredGeneration == disabledGeneration && !snapshot.AcceptingNewStreams &&
			snapshot.Availability == "unavailable" && snapshot.Convergence == "converged" && snapshot.RetryAt == 0
	}, time.Second, time.Millisecond)

	select {
	case call := <-dialer.calls:
		t.Fatalf("disabled desired unexpectedly dialed generation %d", call.generation)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerRetryBackoffIsBoundedFromOneToThirtySeconds(t *testing.T) {
	dialer := newManagerDialerStub()
	now := time.Unix(1_000, 0)
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1), Now: func() time.Time { return now },
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	dialer.results <- managerDialResult{err: errors.New("dial failed")}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "wss://relay.example/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().RetryAt != 0 }, time.Second, time.Millisecond)
	retryAt := manager.Snapshot().RetryAt
	require.GreaterOrEqual(t, retryAt, now.Add(time.Second).Unix())
	require.LessOrEqual(t, retryAt, now.Add(30*time.Second).Unix())
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerActiveDisconnectClearsMatchingGenerationAndRetries(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1),
		BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()

	first := newSession(newMemorySessionConn(), 11, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: first}
	desiredGeneration := manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 11 }, time.Second, time.Millisecond)
	first.Cancel(errors.New("transport closed"))

	second := newSession(newMemorySessionConn(), 22, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: second}
	retryCall := <-dialer.calls
	require.Equal(t, desiredGeneration, retryCall.generation)
	require.Eventually(t, func() bool {
		snapshot := manager.Snapshot()
		return snapshot.ActiveGeneration == desiredGeneration && snapshot.SessionGeneration == 22
	}, time.Second, time.Millisecond)

	// A late completion from the old pointer/generation cannot clear the replacement.
	lateHandled := make(chan struct{})
	manager.deliverEvent(t.Context(), managerEvent{
		ended:   &sessionEnd{session: first, desiredGen: desiredGeneration, sessionGen: first.Generation(), err: errors.New("late completion")},
		handled: lateHandled,
	})
	<-lateHandled
	require.Equal(t, uint64(22), manager.Snapshot().SessionGeneration)
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestSessionCancelCoordinatesBlockedConnectionCloseOnlyOnce(t *testing.T) {
	conn := newBlockingCloseSessionConn()
	t.Cleanup(conn.release)
	session := newSession(conn, 24, testLimits(1), SessionOptions{})
	for range 100 {
		session.Cancel(context.Canceled)
	}
	select {
	case <-conn.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("Cancel did not start connection close coordinator")
	}
	require.EqualValues(t, 1, conn.closeCalls.Load())
	select {
	case <-session.Done():
		t.Fatal("Done closed before blocked connection close joined")
	default:
	}
	conn.release()
	select {
	case <-session.Done():
	case <-time.After(time.Second):
		t.Fatal("Session did not finish after connection close unblocked")
	}
}

func TestManagerDisconnectReturnsWhileSessionCloseIsBlocked(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1)})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	conn := newBlockingCloseSessionConn()
	t.Cleanup(conn.release)
	active := newSession(conn, 23, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 23 }, time.Second, time.Millisecond)

	returned := make(chan struct{})
	go func() {
		manager.Disconnect(errors.New("disconnect now"))
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Disconnect waited for Session.Close")
	}
	select {
	case <-conn.closeStarted:
	case <-time.After(time.Second):
		t.Fatal("supervisor did not start closing disconnected Session")
	}
	applyDone := make(chan uint64, 1)
	go func() { applyDone <- manager.Apply(Desired{Mode: "disabled"}) }()
	select {
	case generation := <-applyDone:
		require.Greater(t, generation, uint64(1))
	case <-time.After(100 * time.Millisecond):
		t.Fatal("blocked Session.Close stalled manager supervisor Apply")
	}
	closeCtx, cancelClose := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancelClose()
	require.ErrorIs(t, manager.Close(closeCtx), context.DeadlineExceeded)
	select {
	case <-manager.Done():
		t.Fatal("Done closed before blocked Session.Close joined")
	default:
	}
	conn.release()
	select {
	case <-manager.Done():
	case <-time.After(time.Second):
		t.Fatal("manager did not finish after Session.Close unblocked")
	}
	cancelRun()
	require.Error(t, <-runDone)
}

func TestManagerCandidateThatEndsBeforePromotionIsNeverLeftActive(t *testing.T) {
	manager := NewManager(ManagerOptions{
		Limits:     testLimits(1),
		BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
	})
	closedConn := newMemorySessionConn()
	require.NoError(t, closedConn.Close())
	deadCandidate := newSession(closedConn, 31, testLimits(1), SessionOptions{})
	deadCandidate.Cancel(errors.New("transport closed"))
	manager.desired = Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"}
	manager.desiredGen = 1
	manager.attempt = 1
	manager.candidate = managerSlot{uri: "ws://relay/ws", desiredGen: 1, attempt: 1}

	// A select may observe the end event before the already-enqueued result.
	manager.handleEvent(managerEvent{ended: &sessionEnd{session: deadCandidate, desiredGen: 1, sessionGen: 31, err: errors.New("transport closed")}})
	require.Nil(t, manager.active.session, "end-before-result must not expose candidate as active")
	manager.handleEvent(managerEvent{candidate: &candidateResult{attempt: 1, gen: 1, uri: "ws://relay/ws", session: deadCandidate}})
	require.False(t, manager.Snapshot().AcceptingNewStreams)
	require.Zero(t, manager.Snapshot().SessionGeneration)
	require.NotZero(t, manager.Snapshot().RetryAt)
}

func TestManagerCandidateCanceledAfterPrecheckDoesNotReplaceHealthyActive(t *testing.T) {
	manager := NewManager(ManagerOptions{Limits: testLimits(1), BackoffMin: time.Hour, BackoffMax: time.Hour})
	old := newSession(newMemorySessionConn(), 32, testLimits(1), SessionOptions{})
	candidate := newSession(newMemorySessionConn(), 33, testLimits(1), SessionOptions{})
	oldCtx, cancelOld := context.WithCancel(t.Context())
	candidateCtx, cancelCandidate := context.WithCancel(t.Context())
	oldDone := make(chan error, 1)
	candidateDone := make(chan error, 1)
	go func() { oldDone <- old.Run(oldCtx) }()
	go func() { candidateDone <- candidate.Run(candidateCtx) }()
	<-old.started
	<-candidate.started
	t.Cleanup(func() {
		cancelOld()
		cancelCandidate()
		<-oldDone
		<-candidateDone
	})

	manager.desired = Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"}
	manager.desiredGen = 1
	manager.attempt = 1
	manager.active = managerSlot{session: old, uri: "ws://relay/ws", desiredGen: 1}
	manager.activeRef.Store(old)
	manager.candidate = managerSlot{uri: "ws://relay/ws", desiredGen: 1, attempt: 1}
	checked := make(chan struct{})
	release := make(chan struct{})
	manager.beforeCandidateActivation = func() {
		close(checked)
		<-release
	}

	promoted := make(chan struct{})
	go func() {
		manager.handleCandidateResult(candidateResult{
			attempt: 1, gen: 1, uri: "ws://relay/ws", session: candidate,
		})
		close(promoted)
	}()
	<-checked
	candidate.Cancel(errors.New("candidate canceled after precheck"))
	<-candidate.Done()
	close(release)
	<-promoted

	require.Same(t, old, manager.active.session)
	require.Same(t, old, manager.activeRef.Load())
	require.True(t, old.acceptsNew())
}

func TestManagerReconnectHealthyActiveWaitsForActualReplacement(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1)})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	first := newSession(newMemorySessionConn(), 41, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: first}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 41 }, time.Second, time.Millisecond)

	second := newSession(newMemorySessionConn(), 42, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: second}
	reconnectDone := make(chan error, 1)
	go func() { reconnectDone <- manager.Reconnect(t.Context()) }()
	select {
	case call := <-dialer.calls:
		require.Equal(t, uint64(1), call.generation)
	case <-time.After(time.Second):
		t.Fatal("Reconnect did not dial a replacement for healthy active")
	}
	require.NoError(t, <-reconnectDone)
	require.Equal(t, uint64(42), manager.Snapshot().SessionGeneration)
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerReconnectContextCancellationCancelsOnlyCandidateDial(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1)})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	active := newSession(newMemorySessionConn(), 43, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 43 }, time.Second, time.Millisecond)

	reconnectCtx, cancelReconnect := context.WithCancel(t.Context())
	reconnectDone := make(chan error, 1)
	go func() { reconnectDone <- manager.Reconnect(reconnectCtx) }()
	candidateCall := <-dialer.calls
	cancelReconnect()
	require.ErrorIs(t, <-reconnectDone, context.Canceled)
	select {
	case <-candidateCall.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("Reconnect cancellation did not cancel candidate dial")
	}
	require.Equal(t, uint64(43), manager.Snapshot().SessionGeneration)
	require.True(t, manager.Snapshot().AcceptingNewStreams)
	cancelRun()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerTypedNilDependenciesFailClosed(t *testing.T) {
	var nilDialer *nilManagerDialer
	var nilTickets *nilManagerTickets
	for _, opts := range []ManagerOptions{
		{Dialer: nilDialer, Tickets: managerTicketStub{}, Limits: testLimits(1), BackoffMin: time.Hour, BackoffMax: time.Hour},
		{Dialer: newManagerDialerStub(), Tickets: nilTickets, Limits: testLimits(1), BackoffMin: time.Hour, BackoffMax: time.Hour},
	} {
		manager := NewManager(opts)
		runCtx, cancel := context.WithCancel(t.Context())
		runDone := make(chan error, 1)
		go func() { runDone <- manager.Run(runCtx) }()
		manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
		require.Eventually(t, func() bool { return manager.Snapshot().LastError != "" }, time.Second, time.Millisecond)
		require.False(t, manager.Snapshot().AcceptingNewStreams)
		cancel()
		require.ErrorIs(t, <-runDone, context.Canceled)
	}
}

func TestManagerDesiredGenerationExhaustionFailsClosed(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	manager.desiredGen = math.MaxUint64
	generation := manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws?token=secret"})
	require.Equal(t, uint64(math.MaxUint64), generation)
	snapshot := manager.Snapshot()
	require.False(t, snapshot.AcceptingNewStreams)
	require.Equal(t, "unavailable", snapshot.Availability)
	require.Contains(t, snapshot.LastError, "generation exhausted")
	require.NotContains(t, snapshot.LastError, "secret")
	require.Zero(t, snapshot.RetryAt)
	require.NoError(t, manager.Close(t.Context()))
}

func TestManagerCloseBeforeRunAndConcurrentRunJoinOneFinalizer(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close(t.Context()) }()
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(t.Context()) }()
	require.NoError(t, <-closeDone)
	require.Error(t, <-runDone)
	require.NoError(t, manager.Close(t.Context()))
	select {
	case <-manager.Done():
	default:
		t.Fatal("Done must close only after the shared finalizer completes")
	}
}

func TestManagerApplyRefreshesRuntimeSettingsForControlledReplacement(t *testing.T) {
	dialer := newManagerDialerStub()
	initialLimits := testLimits(1)
	currentLimits := initialLimits
	currentDrainTimeout := time.Second
	dialer.runtimeSettings = func() (wire.Limits, time.Duration) { return currentLimits, currentDrainTimeout }
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: initialLimits, DrainTimeout: currentDrainTimeout,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	first := newSession(newMemorySessionConn(), 51, initialLimits, SessionOptions{})
	dialer.results <- managerDialResult{session: first}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 51 }, time.Second, time.Millisecond)

	nextLimits := testLimits(2)
	currentLimits = nextLimits
	currentDrainTimeout = 2 * time.Second
	second := newSession(newMemorySessionConn(), 52, nextLimits, SessionOptions{})
	dialer.results <- managerDialResult{session: second}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	select {
	case <-dialer.calls:
	case <-time.After(time.Second):
		t.Fatal("limits reconfigure did not dial a controlled replacement")
	}
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 52 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerRejectsCandidateThatExceedsConfiguredLimits(t *testing.T) {
	dialer := newManagerDialerStub()
	configured := testLimits(1)
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: configured,
		BackoffMin: time.Hour, BackoffMax: time.Hour,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	granted := configured
	granted.MaxConcurrentStreams = 2
	dialer.results <- managerDialResult{session: newSession(newMemorySessionConn(), 53, granted, SessionOptions{})}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().LastError != "" }, time.Second, time.Millisecond)
	require.Contains(t, manager.Snapshot().LastError, "limits exceed")
	require.Zero(t, manager.Snapshot().SessionGeneration)
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerApplyBeforeRunDefersDialUntilSupervisorIsAttached(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1)})
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	select {
	case <-dialer.calls:
		t.Fatal("Apply before Run started a dial")
	case <-time.After(10 * time.Millisecond):
	}

	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	select {
	case <-dialer.calls:
	case <-time.After(time.Second):
		t.Fatal("Run did not reconcile the pending Apply")
	}
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerDrainContextCancellationDoesNotAbandonSessionDrain(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1), DrainTimeout: time.Second})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	active := newSession(newMemorySessionConn(), 61, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 61 }, time.Second, time.Millisecond)
	require.True(t, active.acquireAdmission())

	drainCtx, cancelDrain := context.WithCancel(t.Context())
	drainDone := make(chan error, 1)
	go func() { drainDone <- manager.Drain(drainCtx) }()
	require.Eventually(t, func() bool { return !manager.Snapshot().AcceptingNewStreams }, time.Second, time.Millisecond)
	cancelDrain()
	require.ErrorIs(t, <-drainDone, context.Canceled)
	active.releaseAdmission()
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("drain was abandoned after caller context cancellation")
	}
	cancelRun()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerDrainWaitsForCommittedStream(t *testing.T) {
	dialer := newManagerDialerStub()
	limits := testLimits(1)
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: limits, DrainTimeout: time.Second})
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	client, peer := websocketPair(t)
	active := NewSession(client, 62, limits, SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().SessionGeneration == 62 }, time.Second, time.Millisecond)
	stream, _ := committedTestStream(t, active, peer, limits, limits.InitialStreamWindow)

	drainDone := make(chan error, 1)
	go func() { drainDone <- manager.Drain(t.Context()) }()
	require.Eventually(t, func() bool { return !manager.Snapshot().AcceptingNewStreams }, time.Second, time.Millisecond)
	select {
	case err := <-drainDone:
		t.Fatalf("Drain returned before committed stream ended: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	stream.Cancel(context.Canceled)
	require.NoError(t, <-drainDone)
	cancelRun()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerBackoffSequenceStaysWithinExponentialJitterBounds(t *testing.T) {
	manager := NewManager(ManagerOptions{BackoffMin: time.Second, BackoffMax: 30 * time.Second})
	t.Cleanup(func() { require.NoError(t, manager.Close(context.Background())) })
	bases := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 30 * time.Second}
	for _, base := range bases {
		delay := manager.nextBackoffLocked()
		require.GreaterOrEqual(t, delay, base)
		upper := base + base/5
		if upper > 30*time.Second {
			upper = 30 * time.Second
		}
		require.LessOrEqual(t, delay, upper)
	}
}

func TestManagerFinalizerClearsRetainedSessionsAndEventsBeforeDone(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	stale := newSession(newMemorySessionConn(), 71, testLimits(1), SessionOptions{})
	manager.draining[stale] = stale.Generation()
	manager.events <- managerEvent{ended: &sessionEnd{session: stale, sessionGen: stale.Generation()}}
	require.NoError(t, manager.Close(context.Background()))
	require.Empty(t, manager.draining)
	require.Nil(t, manager.active.session)
	require.Nil(t, manager.candidate.session)
	require.Zero(t, len(manager.events))
	select {
	case <-manager.Done():
	default:
		t.Fatal("Done closed before final retained state cleanup")
	}
}

func TestManagerDoneRejectsConcurrentOperationsWithoutRetainingReferences(t *testing.T) {
	manager := NewManager(ManagerOptions{})
	require.NoError(t, manager.Close(t.Context()))
	<-manager.Done()

	retained := newSession(newMemorySessionConn(), 73, testLimits(1), SessionOptions{})
	const callers = 256
	finished := make(chan struct{}, callers)
	for range callers {
		go func() {
			manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay.invalid/ws"})
			_ = manager.Reconnect(t.Context())
			_ = manager.Drain(t.Context())
			manager.Disconnect(errors.New("post-Done disconnect"))
			manager.deliverEvent(context.Background(), managerEvent{ended: &sessionEnd{session: retained, sessionGen: retained.Generation()}})
			finished <- struct{}{}
		}()
	}
	for range callers {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("post-Done operation did not return")
		}
	}

	require.Zero(t, len(manager.commands))
	require.Zero(t, len(manager.disconnects))
	require.Zero(t, len(manager.events))
}

func TestManagerCanceledDialWorkerJoinsUnclaimedSession(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1)})
	ctx, cancel := context.WithCancelCause(context.Background())
	conn := newBlockingControlSessionConn()
	t.Cleanup(func() { require.NoError(t, conn.Close()) })
	session := newSession(conn, 72, testLimits(1), SessionOptions{PingInterval: time.Millisecond})
	dialer.results <- managerDialResult{session: session}
	manager.workers.Add(1)
	go manager.dialCandidate(ctx, 1, 1, "ws://relay/ws")
	<-dialer.calls
	require.Eventually(t, func() bool { return len(manager.events) != 0 }, time.Second, time.Millisecond)
	select {
	case <-conn.controlStarted:
	case <-time.After(time.Second):
		t.Fatal("candidate writer did not enter the blocking control write")
	}
	cancel(context.Canceled)
	joined := make(chan struct{})
	go func() { manager.workers.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(time.Second):
		t.Fatal("canceled dial worker did not close and join unclaimed Session")
	}
	require.NoError(t, manager.Close(context.Background()))
}

func TestManagerDrainTimeoutClosesBorrowedActive(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1), DrainTimeout: 10 * time.Millisecond,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()
	active := newSession(newMemorySessionConn(), 1, testLimits(1), SessionOptions{})
	dialer.results <- managerDialResult{session: active}
	manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay/ws"})
	<-dialer.calls
	require.Eventually(t, func() bool { return manager.Snapshot().AcceptingNewStreams }, time.Second, time.Millisecond)
	require.True(t, active.acquireAdmission())
	manager.Apply(Desired{Mode: "disabled"})
	select {
	case <-active.Done():
	case <-time.After(time.Second):
		t.Fatal("drain timeout did not close active session")
	}
	active.releaseAdmission()
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestManagerFiveHundredReplaceDrainCyclesCloseEverySession(t *testing.T) {
	dialer := newManagerDialerStub()
	manager := NewManager(ManagerOptions{
		Dialer: dialer, Tickets: managerTicketStub{}, Limits: testLimits(1), DrainTimeout: time.Second,
	})
	runCtx, cancel := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- manager.Run(runCtx) }()

	sessions := make([]*Session, 0, 500)
	for i := 1; i <= 500; i++ {
		session := newSession(newMemorySessionConn(), uint64(i), testLimits(1), SessionOptions{})
		sessions = append(sessions, session)
		dialer.results <- managerDialResult{session: session}
		generation := manager.Apply(Desired{Mode: "custom", EffectiveURI: "ws://relay.example/ws?generation=" + fmt.Sprint(i)})
		<-dialer.calls
		require.Eventually(t, func() bool {
			return manager.Snapshot().ActiveGeneration == generation
		}, time.Second, time.Millisecond, "cycle %d", i)
	}
	manager.Apply(Desired{Mode: "disabled"})
	for i, session := range sessions {
		select {
		case <-session.Done():
		case <-time.After(time.Second):
			t.Fatalf("session %d leaked after replace/drain cycles", i+1)
		}
	}
	cancel()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

var _ Dialer = (*managerDialerStub)(nil)
var _ TicketProvider = managerTicketStub{}
var _ = wire.PreCommit
