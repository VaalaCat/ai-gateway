package tunnel

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestRelaySessionErrorRingIsBoundedAndCopyIsolated(t *testing.T) {
	session := newTestSession(nil, "agent-a", 1)
	require.False(t, session.recordError(diagnostics.Event{}))
	for index := range 25 {
		require.True(t, session.recordError(diagnostics.Event{
			Code: fmt.Sprintf("relay-%02d", index), Stage: "protocol", At: time.Unix(int64(index), 0),
		}))
	}

	first := session.snapshot()
	require.Len(t, first.RecentErrors, 20)
	require.Equal(t, "relay-05", first.RecentErrors[0].Code)
	first.RecentErrors[0].Code = "mutated"
	require.Equal(t, "relay-05", session.snapshot().RecentErrors[0].Code)
}

func TestRelaySessionReplacementOwnsIndependentErrorRing(t *testing.T) {
	hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	t.Cleanup(func() { require.NoError(t, hub.Close(context.Background())) })
	old := newTestSession(hub, "agent-a", 1)
	current := newTestSession(hub, "agent-a", 2)
	require.NoError(t, hub.register(old))
	require.NoError(t, hub.register(current))
	require.NoError(t, hub.promote(current))

	require.True(t, old.recordError(diagnostics.Event{Code: "stale", Stage: "read"}))
	require.True(t, current.recordError(diagnostics.Event{Code: "current", Stage: "read"}))
	snapshot, ok := hub.Snapshot("agent-a")
	require.True(t, ok)
	require.Len(t, snapshot.RecentErrors, 1)
	require.Equal(t, "current", snapshot.RecentErrors[0].Code)
}

func TestSessionSyntheticResetMetricsOnlyCountSuccessfulWireWrites(t *testing.T) {
	tests := []struct {
		name  string
		code  string
		stage string
		want  pkgmetrics.Stage
	}{
		{name: "admission overloaded", code: wire.ErrorCodeRelayOverloaded, stage: "admission", want: pkgmetrics.StageSelect},
		{name: "target unavailable", code: consts.RouteErrorRelayNotReady, stage: "target", want: pkgmetrics.StageSelect},
		{name: "duplicate protocol", code: wire.ErrorCodeRelayProtocol, stage: "duplicate", want: pkgmetrics.StageProtocol},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metrics := newTunnelMetricRecorder()
			hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
			session, conn := liveTestSession(hub, "source", 1)
			t.Cleanup(func() {
				session.Cancel(errors.New("cleanup"))
				require.NoError(t, hub.Close(context.Background()))
			})
			require.NoError(t, session.sendResetCode(wire.StreamID{41}, test.code, test.stage))
			raw := <-conn.writes
			frame := decodeCapturedFrame(t, raw)
			var reset wire.Reset
			require.NoError(t, wire.DecodeMetadata(frame.Payload, &reset, testLimits().MaxMetadataBytes))
			require.Equal(t, test.code, reset.Code)
			require.Equal(t, float64(len(raw)), metrics.byteCount(pkgmetrics.DirectionInbound))
			require.Equal(t, []struct {
				stage     pkgmetrics.Stage
				committed bool
			}{{stage: test.want, committed: false}}, metrics.resetEvents())
		})
	}

	t.Run("write failure is not counted", func(t *testing.T) {
		metrics := newTunnelMetricRecorder()
		hub := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits(), Metrics: metrics})
		conn := newControlledSessionConn()
		conn.writeErr = errors.New("write failed")
		ctx, cancel := context.WithCancelCause(context.Background())
		session := newSession(hub, conn, "source", 1, 1, testLimits(), ctx, cancel)
		session.writerStarted.Store(true)
		go session.writer.run()
		require.Error(t, session.sendResetCode(wire.StreamID{42}, wire.ErrorCodeRelayProtocol, "protocol"))
		require.Empty(t, metrics.resetEvents())
		require.Zero(t, metrics.byteCount(pkgmetrics.DirectionInbound))
		session.Cancel(errors.New("cleanup"))
		require.NoError(t, hub.Close(context.Background()))
	})
}

type controlledSessionConn struct {
	closed       chan struct{}
	closeOnce    sync.Once
	writeStarted chan struct{}
	writeOnce    sync.Once
	writeRelease chan struct{}
	writeErr     error
}

type scriptedSessionConn struct {
	reads     chan []byte
	writes    chan []byte
	closed    chan struct{}
	closeOnce sync.Once
}

type deadlineClock struct {
	mu      sync.Mutex
	now     time.Time
	changed chan struct{}
}

func newDeadlineClock(now time.Time) *deadlineClock {
	return &deadlineClock{now: now, changed: make(chan struct{})}
}

func (c *deadlineClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *deadlineClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	close(c.changed)
	c.changed = make(chan struct{})
	c.mu.Unlock()
}
func (c *deadlineClock) snapshot() (time.Time, <-chan struct{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now, c.changed
}

type deadlineBlockingConn struct {
	reads       chan []byte
	closed      chan struct{}
	closeOnce   sync.Once
	deadlineSet chan time.Time
	clock       *deadlineClock
	wantTimeout time.Duration
	deadlineMu  sync.Mutex
	deadline    time.Time
}

func newDeadlineBlockingConn(clock *deadlineClock, timeout time.Duration) *deadlineBlockingConn {
	return &deadlineBlockingConn{reads: make(chan []byte, 1), closed: make(chan struct{}), deadlineSet: make(chan time.Time, 1), clock: clock, wantTimeout: timeout}
}

func (c *deadlineBlockingConn) ReadMessage() (int, []byte, error) {
	select {
	case raw := <-c.reads:
		return websocket.BinaryMessage, raw, nil
	case <-c.closed:
		return 0, nil, errors.New("closed")
	}
}

func (c *deadlineBlockingConn) WriteMessage(int, []byte) error {
	c.deadlineMu.Lock()
	deadline := c.deadline
	c.deadlineMu.Unlock()
	for {
		now, changed := c.clock.snapshot()
		if !now.Before(deadline) {
			return errors.New("write deadline exceeded")
		}
		select {
		case <-changed:
		case <-c.closed:
			return errors.New("closed")
		}
	}
}

func (c *deadlineBlockingConn) SetWriteDeadline(deadline time.Time) error {
	now := c.clock.Now()
	if deadline.IsZero() || !deadline.After(now) || !deadline.Equal(now.Add(c.wantTimeout)) {
		return errors.New("invalid future write deadline")
	}
	c.deadlineMu.Lock()
	c.deadline = deadline
	c.deadlineMu.Unlock()
	c.deadlineSet <- deadline
	return nil
}

func (c *deadlineBlockingConn) Close() error { c.closeOnce.Do(func() { close(c.closed) }); return nil }

func newScriptedSessionConn() *scriptedSessionConn {
	return &scriptedSessionConn{reads: make(chan []byte, 16), writes: make(chan []byte, 16), closed: make(chan struct{})}
}

func (c *scriptedSessionConn) ReadMessage() (int, []byte, error) {
	select {
	case raw := <-c.reads:
		return websocket.BinaryMessage, raw, nil
	case <-c.closed:
		return 0, nil, errors.New("connection closed")
	}
}

func (c *scriptedSessionConn) WriteMessage(_ int, raw []byte) error {
	select {
	case c.writes <- append([]byte(nil), raw...):
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	}
}

func (c *scriptedSessionConn) SetWriteDeadline(time.Time) error { return nil }

func (c *scriptedSessionConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func sendSessionFrame(t *testing.T, conn *scriptedSessionConn, limits wire.Limits, frame wire.Frame) {
	t.Helper()
	raw, err := wire.Encode(frame, limits)
	require.NoError(t, err)
	conn.reads <- raw
}

func receiveSessionFrame(t *testing.T, conn *scriptedSessionConn, limits wire.Limits) wire.Frame {
	t.Helper()
	select {
	case raw := <-conn.writes:
		frame, err := wire.Decode(raw, limits)
		require.NoError(t, err)
		return frame
	case <-time.After(time.Second):
		t.Fatal("session did not write a frame")
		return wire.Frame{}
	}
}

func decodeCapturedFrame(t *testing.T, raw []byte) wire.Frame {
	t.Helper()
	frame, err := wire.Decode(raw, testLimits())
	require.NoError(t, err)
	return frame
}

func newControlledSessionConn() *controlledSessionConn {
	return &controlledSessionConn{
		closed: make(chan struct{}), writeStarted: make(chan struct{}), writeRelease: make(chan struct{}),
	}
}

func (c *controlledSessionConn) ReadMessage() (int, []byte, error) {
	<-c.closed
	return 0, nil, errors.New("connection closed")
}

func (c *controlledSessionConn) WriteMessage(int, []byte) error {
	c.writeOnce.Do(func() { close(c.writeStarted) })
	if c.writeErr != nil {
		return c.writeErr
	}
	select {
	case <-c.writeRelease:
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	}
}

func (c *controlledSessionConn) SetWriteDeadline(time.Time) error { return nil }

func (c *controlledSessionConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func requireClosed(t *testing.T, ch <-chan struct{}, label string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s did not close", label)
	}
}

func newTestSession(h *Hub, agentID string, generation uint64) *Session {
	ctx, cancel := context.WithCancelCause(context.Background())
	return newSession(h, nil, agentID, generation, generation, testLimits(), ctx, cancel)
}

func TestSessionDuplicateStreamIDAndPointerGenerationRemoval(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	s := newTestSession(h, "agent-a", 4)
	id := wire.StreamID{1}
	sw := newTestSwitch(h, s, newTestSession(h, "agent-b", 5), id)
	require.NoError(t, s.addLeg(id, sw))
	require.ErrorIs(t, s.addLeg(id, sw), errDuplicateStreamID)
	s.removeLeg(id, sw, 3)
	require.Same(t, sw, s.lookupLeg(id), "wrong generation cannot remove current leg")
	s.removeLeg(id, sw, 4)
	require.Nil(t, s.lookupLeg(id))
}

func TestSessionUnknownDataResetsThenClosesAtThreshold(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	s := newTestSession(h, "agent-a", 1)
	for i := 0; i < protocolErrorLimit-1; i++ {
		require.NoError(t, s.noteUnknownData(time.Now()))
	}
	require.ErrorIs(t, s.noteUnknownData(time.Now()), errProtocolThreshold)
}

func TestSessionTombstoneDropsLateDataAndIsBounded(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	s, conn := liveTestSession(h, "agent-a", 1)
	base := time.Unix(10_000, 0)
	var nanos atomic.Int64
	nanos.Store(base.UnixNano())
	s.now = func() time.Time { return time.Unix(0, nanos.Load()) }
	for i := 0; i < tombstoneLimit+1; i++ {
		id := wire.StreamID{byte(i), byte(i >> 8)}
		sw := newTestSwitch(h, s, newTestSession(h, "peer", uint64(i+2)), id)
		require.NoError(t, s.addLeg(id, sw))
		s.removeLeg(id, sw, s.generation)
	}
	require.Equal(t, tombstoneLimit, s.tombstoneCount())
	newest := wire.StreamID{byte(tombstoneLimit % 256), byte(tombstoneLimit >> 8)}
	require.NoError(t, s.dispatch(wire.Frame{Type: wire.FrameResponseData, StreamID: newest, Payload: []byte("late")}))
	require.Equal(t, uint64(1), s.lateData.Load())
	select {
	case raw := <-conn.writes:
		t.Fatalf("tombstoned DATA was forwarded/reset: %d bytes", len(raw))
	default:
	}
	nanos.Store(base.Add(tombstoneTTL + time.Nanosecond).UnixNano())
	require.NoError(t, s.dispatch(wire.Frame{Type: wire.FrameResponseData, StreamID: newest, Payload: []byte("expired")}))
	require.Equal(t, uint64(1), s.lateData.Load())
	require.Equal(t, wire.FrameReset, decodeCapturedFrame(t, <-conn.writes).Type)
	s.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "Session cleanup")
}

func TestSessionTombstoneExpiresAtExactlyThirtySeconds(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	s := newTestSession(h, "agent-a", 1)
	created := time.Unix(40_000, 0)
	id := wire.StreamID{77}
	s.addTombstone(id, created)
	require.True(t, s.isTombstoned(id, created.Add(tombstoneTTL-time.Nanosecond)))
	require.False(t, s.isTombstoned(id, created.Add(tombstoneTTL)))
}

func TestSessionProtocolErrorWindowEvictsExpiredEntriesThroughReader(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	base := time.Unix(20_000, 0)
	var nanos atomic.Int64
	nanos.Store(base.UnixNano())
	s.now = func() time.Time { return time.Unix(0, nanos.Load()) }
	go s.run()
	for i := 0; i < protocolErrorLimit-1; i++ {
		sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: wire.StreamID{byte(i + 1)}, Sequence: 1})
		require.Equal(t, wire.FrameReset, receiveSessionFrame(t, conn, testLimits()).Type)
	}
	nanos.Store(base.Add(protocolErrorTTL + time.Nanosecond).UnixNano())
	sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: wire.StreamID{40}, Sequence: 1})
	require.Equal(t, wire.FrameReset, receiveSessionFrame(t, conn, testLimits()).Type)
	select {
	case <-s.Done():
		t.Fatal("expired protocol errors closed Session")
	default:
	}
	s.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "Session cleanup")
}

func TestSessionCancelRepeatedlyDoesNotWait(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	s := newTestSession(h, "agent-a", 1)
	for i := 0; i < 10; i++ {
		s.Cancel(errors.New("closed"))
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("session did not close")
	}
}

func TestSessionCancelBeforeRunReturnsImmediatelyAndDoneEventuallyCloses(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newControlledSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	returned := make(chan struct{})
	go func() {
		s.Cancel(errors.New("cancel before run"))
		close(returned)
	}()
	requireClosed(t, returned, "Cancel")
	requireClosed(t, s.Done(), "Session.Done")
}

func TestSessionRunAfterContextCancellationStillJoinsCoordinator(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newControlledSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	cancel(errors.New("cancel before Run"))
	returned := make(chan struct{})
	go func() { s.run(); close(returned) }()
	requireClosed(t, returned, "Session.run")
	requireClosed(t, s.Done(), "Session.Done")
}

func TestSessionWriterFailureClosesReaderAndSession(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newControlledSessionConn()
	conn.writeErr = errors.New("write failed")
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	runReturned := make(chan struct{})
	go func() {
		s.run()
		close(runReturned)
	}()
	require.NoError(t, s.enqueue(context.Background(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: wire.StreamID{1}, Sequence: 1,
	}))
	requireClosed(t, conn.writeStarted, "writer start")
	requireClosed(t, s.Done(), "Session.Done after writer failure")
	requireClosed(t, runReturned, "Session.run after writer failure")
}

func TestProtocolResetWriteDeadlineClosesBlockedWriterSession(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	base := time.Unix(30_000, 0)
	clock := newDeadlineClock(base)
	conn := newDeadlineBlockingConn(clock, defaultWriteTimeout)
	require.Error(t, conn.SetWriteDeadline(time.Time{}))
	require.Error(t, conn.SetWriteDeadline(base.Add(-time.Nanosecond)))
	require.Error(t, conn.SetWriteDeadline(base.Add(defaultWriteTimeout+time.Nanosecond)))
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	s.writer.now = clock.Now
	go s.run()
	raw, err := wire.Encode(wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: wire.StreamID{29}, Sequence: 1}, testLimits())
	require.NoError(t, err)
	conn.reads <- raw
	select {
	case deadline := <-conn.deadlineSet:
		require.Equal(t, base.Add(defaultWriteTimeout), deadline)
	case <-time.After(time.Second):
		t.Fatal("writer did not set a future deadline")
	}
	clock.Advance(defaultWriteTimeout - time.Nanosecond)
	select {
	case <-s.Done():
		t.Fatal("Session closed before write deadline")
	default:
	}
	clock.Advance(time.Nanosecond)
	requireClosed(t, s.Done(), "Session.Done after RESET write timeout")
	requireClosed(t, conn.closed, "socket close after RESET write timeout")
}

func TestSessionWriterFairnessPreservesPerStreamSequence(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	conn := &captureConn{writes: make(chan []byte, 8)}
	writer := newSessionWriter(ctx, newSessionBudget(1024), conn)
	streamA := wire.StreamID{1}
	streamB := wire.StreamID{2}
	for _, frame := range []wire.Frame{
		{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: streamA, Sequence: 1, Payload: []byte("a1")},
		{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: streamA, Sequence: 2, Payload: []byte("a2")},
		{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: streamA, Sequence: 3, Payload: []byte("a3")},
		{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: streamB, Sequence: 1, Payload: []byte("b1")},
	} {
		require.NoError(t, writer.enqueue(ctx, frame))
	}
	go writer.run()
	got := make([]wire.Frame, 0, 4)
	for len(got) < 4 {
		got = append(got, decodeCapturedFrame(t, <-conn.writes))
	}
	require.Equal(t, []wire.StreamID{streamA, streamB, streamA, streamA}, []wire.StreamID{got[0].StreamID, got[1].StreamID, got[2].StreamID, got[3].StreamID})
	require.Equal(t, []uint32{1, 1, 2, 3}, []uint32{got[0].Sequence, got[1].Sequence, got[2].Sequence, got[3].Sequence})
	cancel(errors.New("cleanup"))
	requireClosed(t, writer.done, "writer.Done")
}

func TestSessionProtocolErrorsResetStreamsAndCloseAtEighth(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "agent-a", 1, 1, testLimits(), ctx, cancel)
	go s.run()
	for i := 1; i <= 8; i++ {
		id := wire.StreamID{byte(i)}
		sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: id, Sequence: 1})
		reset := receiveSessionFrame(t, conn, testLimits())
		require.Equal(t, wire.FrameReset, reset.Type)
		if i < 8 {
			select {
			case <-s.Done():
				t.Fatalf("session closed after only %d protocol errors", i)
			default:
			}
		}
	}
	requireClosed(t, s.Done(), "Session.Done after eighth protocol error")
}

func TestSessionSequenceErrorResetsOnlyOffendingStream(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "source", 1, 1, testLimits(), ctx, cancel)
	target := newTestSession(h, "target", 2)
	id := wire.StreamID{31}
	sw := newTestSwitch(h, s, target, id)
	require.NoError(t, s.addLeg(id, sw))
	go s.run()
	sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 2, Payload: []byte("bad sequence")})
	reset := receiveSessionFrame(t, conn, testLimits())
	require.Equal(t, wire.FrameReset, reset.Type)
	select {
	case <-s.Done():
		t.Fatal("one sequence error closed the session")
	default:
	}
	s.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "Session.Done cleanup")
}

func TestSessionSequenceOverflowResetsOnlyOffendingStream(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "source", 1, 1, testLimits(), ctx, cancel)
	target := newTestSession(h, "target", 2)
	id := wire.StreamID{30}
	sw := newTestSwitch(h, s, target, id)
	sw.observedSequence[s] = math.MaxUint32
	require.NoError(t, s.addLeg(id, sw))
	go s.run()
	sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 0})
	require.Equal(t, wire.FrameReset, receiveSessionFrame(t, conn, testLimits()).Type)
	select {
	case <-s.Done():
		t.Fatal("sequence overflow closed Session before threshold")
	default:
	}
	s.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSessionDuplicateOpenResetsOffenderWithoutForwardingToUnopenedPeer(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "source", 1, 1, testLimits(), ctx, cancel)
	target, targetConn := liveTestSession(h, "target", 2)
	id := wire.StreamID{32}
	sw := newTestSwitch(h, s, target, id)
	require.NoError(t, s.addLeg(id, sw))
	require.NoError(t, target.addLeg(id, sw))
	go s.run()
	payload, err := wire.EncodeMetadata(wire.Open{TargetAgentID: "target"}, testLimits().MaxMetadataBytes)
	require.NoError(t, err)
	sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1, Payload: payload})
	offenderFrame := receiveSessionFrame(t, conn, testLimits())
	require.Equal(t, wire.FrameReset, offenderFrame.Type)
	require.Equal(t, uint32(1), offenderFrame.Sequence)
	var offenderReset wire.Reset
	require.NoError(t, wire.DecodeMetadata(offenderFrame.Payload, &offenderReset, testLimits().MaxMetadataBytes))
	require.Equal(t, wire.ErrorCodeRelayProtocol, offenderReset.Code)
	require.Equal(t, "protocol", offenderReset.Stage)
	require.False(t, offenderReset.Committed)
	requireClosed(t, sw.Done(), "Switch.Done after duplicate OPEN RESET")
	select {
	case raw := <-targetConn.writes:
		forwarded, decodeErr := wire.Decode(raw, testLimits())
		require.NoError(t, decodeErr)
		t.Fatalf("duplicate OPEN sent unopened peer frame %v", forwarded.Type)
	default:
	}
	s.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSessionStreamCapacityResetsAsOverloaded(t *testing.T) {
	limits := testLimits()
	limits.MaxConcurrentStreams = 1
	gate := &AdmissionGate{}
	gate.Set(true)
	agents := fakeAgents{
		agents: map[string]*models.Agent{"target": {AgentID: "target", Status: consts.StatusEnabled}},
		caps:   map[string][]string{"target": {protocol.AgentCapabilityTunnelV1}},
	}
	h := NewHub(HubOptions{InstanceID: "master-a", Agents: agents, Admission: gate, Limits: limits})
	targetCtx, targetCancel := context.WithCancelCause(context.Background())
	target := newSession(h, nil, "target", 2, 2, limits, targetCtx, targetCancel)
	require.NoError(t, h.register(target))
	occupied := newTestSwitch(h, target, target, wire.StreamID{99})
	require.NoError(t, target.addLeg(occupied.id, occupied))

	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	source := newSession(h, conn, "source", 1, 1, limits, ctx, cancel)
	go source.run()
	payload, err := wire.EncodeMetadata(wire.Open{TargetAgentID: "target"}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	sendSessionFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: wire.StreamID{33}, Sequence: 1, Payload: payload})
	frame := receiveSessionFrame(t, conn, limits)
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(frame.Payload, &reset, limits.MaxMetadataBytes))
	require.Equal(t, wire.ErrorCodeRelayOverloaded, reset.Code)
	require.Equal(t, "admission", reset.Stage)

	source.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, source.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}

func TestSessionMalformedOpenUsesProtocolErrorThreshold(t *testing.T) {
	gate := &AdmissionGate{}
	gate.Set(true)
	h := NewHub(HubOptions{InstanceID: "master-a", Admission: gate, Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "source", 1, 1, testLimits(), ctx, cancel)
	go s.run()
	for i := 1; i <= protocolErrorLimit; i++ {
		sendSessionFrame(t, conn, testLimits(), wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: wire.StreamID{byte(80 + i)}, Sequence: 1, Payload: []byte("{"),
		})
		require.Equal(t, wire.FrameReset, receiveSessionFrame(t, conn, testLimits()).Type)
		if i < protocolErrorLimit {
			select {
			case <-s.Done():
				t.Fatalf("malformed OPEN %d closed session before threshold", i)
			default:
			}
		}
	}
	requireClosed(t, s.Done(), "Session.Done after malformed OPEN threshold")
}

func TestSessionOldGenerationFrameResetsOnlyBoundStream(t *testing.T) {
	h := NewHub(HubOptions{InstanceID: "master-a", Limits: testLimits()})
	conn := newScriptedSessionConn()
	ctx, cancel := context.WithCancelCause(context.Background())
	s := newSession(h, conn, "source", 5, 5, testLimits(), ctx, cancel)
	target := newTestSession(h, "target", 6)
	id := wire.StreamID{90}
	sw := newTestSwitch(h, s, target, id)
	sw.sourceGeneration = 4
	require.NoError(t, s.addLeg(id, sw))
	go s.run()
	sendSessionFrame(t, conn, testLimits(), wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 1, Payload: []byte("stale")})
	require.Equal(t, wire.FrameReset, receiveSessionFrame(t, conn, testLimits()).Type)
	select {
	case <-s.Done():
		t.Fatal("old-generation stream frame closed session")
	default:
	}
	s.Cancel(errors.New("cleanup"))
	target.Cancel(errors.New("cleanup"))
	requireClosed(t, s.Done(), "source cleanup")
	requireClosed(t, target.Done(), "target cleanup")
}
