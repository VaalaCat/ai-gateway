package tunnel

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestDeterministicOpenCommitTimeoutStopsStream(t *testing.T) {
	clock := newManualClock(time.Unix(100, 0))
	conn := newMemorySessionConn()
	session := newSession(conn, 1, testLimits(1), SessionOptions{
		OpenCommitTimeout: 30 * time.Second,
		PingInterval:      time.Hour,
		PongTimeout:       time.Hour,
		clock:             clock,
	})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	<-session.started
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	require.Equal(t, wire.FrameOpen, decodeMemoryWrite(t, <-conn.writes, session.limits).Type)
	require.Eventually(t, func() bool { return clock.EventCount() >= 2 }, time.Second, time.Millisecond)
	clock.Advance(30 * time.Second)
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("manual clock did not fire open-commit timeout")
	}
	session.Cancel(context.Canceled)
	<-runDone
	require.Zero(t, clock.EventCount())
}

func TestDeterministicWriteDeadlineAndFailure(t *testing.T) {
	now := time.Unix(200, 0)
	clock := newManualClock(now)
	conn := newMemorySessionConn()
	writeErr := errors.New("memory write failed")
	conn.writeErr = writeErr
	session := newSession(conn, 1, testLimits(1), SessionOptions{WriteTimeout: 15 * time.Second, clock: clock})
	err := session.writeFrame(testFrame(1, 'x'))
	require.ErrorIs(t, err, writeErr)
	conn.mu.Lock()
	require.Equal(t, now.Add(15*time.Second), conn.writeDeadline)
	conn.mu.Unlock()
}

func TestDeterministicPongExtendsReadDeadline(t *testing.T) {
	now := time.Unix(300, 0)
	clock := newManualClock(now)
	conn := newMemorySessionConn()
	session := newSession(conn, 1, testLimits(1), SessionOptions{PongTimeout: time.Minute, clock: clock})
	session.configureReader()
	conn.mu.Lock()
	first := conn.readDeadline
	conn.mu.Unlock()
	clock.Advance(10 * time.Second)
	require.NoError(t, conn.triggerPong())
	conn.mu.Lock()
	second := conn.readDeadline
	conn.mu.Unlock()
	require.Equal(t, first.Add(10*time.Second), second)
}

func TestDeterministicUnknownDataRollingWindowExpires(t *testing.T) {
	clock := newManualClock(time.Unix(400, 0))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	conn := newMemorySessionConn()
	session := newSession(conn, 1, testLimits(1), SessionOptions{clock: clock})
	session.ctx = ctx
	session.writer = newFairWriter(ctx, session.limits.MaxQueuedSessionBytes, time.Second, func(wire.Frame) error { return nil })
	for i := byte(1); i <= 7; i++ {
		require.NoError(t, session.handleUnknownData(ctx, testStreamID(i)))
	}
	clock.Advance(61 * time.Second)
	for i := byte(11); i <= 17; i++ {
		require.NoError(t, session.handleUnknownData(ctx, testStreamID(i)))
	}
	require.ErrorIs(t, session.handleUnknownData(ctx, testStreamID(18)), errUnknownStreamData)
}

func TestMemorySessionHasOneReaderAndOneWriterOwner(t *testing.T) {
	clock := newManualClock(time.Unix(500, 0))
	conn := newMemorySessionConn()
	limits := testLimits(4)
	session := newSession(conn, 1, limits, SessionOptions{PingInterval: time.Hour, PongTimeout: time.Hour, clock: clock})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	<-session.started
	for i := 0; i < 4; i++ {
		_, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
		require.NoError(t, err)
	}
	for range 4 {
		<-conn.writes
	}
	require.EqualValues(t, 1, conn.maxReaders.Load())
	require.EqualValues(t, 1, conn.maxWriters.Load())
	session.Cancel(context.Canceled)
	<-runDone
}

func TestMemorySessionOneByteStressDoesNotBlockOtherStreamOrPong(t *testing.T) {
	conn := newMemorySessionConn()
	limits := testLimits(2)
	limits.MaxDataBytes = 64 * 1024
	limits.InitialStreamWindow = 256 * 1024
	limits.MaxQueuedSessionBytes = 1024 * 1024
	session := newSession(conn, 1, limits, SessionOptions{PingInterval: time.Hour, PongTimeout: time.Hour})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	<-session.started
	first, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	second, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	<-conn.writes
	<-conn.writes
	sendMemoryFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: first.id, Sequence: 1,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits)})
	commitDone := make(chan error, 1)
	go func() { commitDone <- first.Commit(t.Context()) }()
	<-conn.writes
	sendMemoryFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: first.id, Sequence: 2})
	require.NoError(t, <-commitDone)
	sendMemoryFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: first.id, Sequence: 3,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits)})
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for sequence := uint32(4); sequence < 1004; sequence++ {
			sendMemoryFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameResponseData,
				StreamID: first.id, Sequence: sequence, Payload: []byte("x")})
		}
		sendMemoryFrame(t, conn, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: second.id, Sequence: 1,
			Payload: mustMetadata(t, wire.Ready{RequestWindow: 0}, limits)})
	}()
	select {
	case <-sent:
	case <-time.After(time.Second):
		t.Fatal("one-byte fragments blocked Session reader")
	}
	require.NoError(t, waitResult(t.Context(), second.ready))
	require.NoError(t, conn.triggerPong())
	first.Cancel(context.Canceled)
	second.Cancel(context.Canceled)
	session.Cancel(context.Canceled)
	<-runDone
	require.Zero(t, session.incomingSize())
}

func sendMemoryFrame(t *testing.T, conn *memorySessionConn, limits wire.Limits, frame wire.Frame) {
	t.Helper()
	payload, err := wire.Encode(frame, limits)
	require.NoError(t, err)
	conn.inbound <- memoryMessage{messageType: websocket.BinaryMessage, payload: payload}
}

func TestManualClockStopsTimersAndTickersIdempotently(t *testing.T) {
	clock := newManualClock(time.Unix(600, 0))
	timer := clock.NewTimer(time.Second)
	ticker := clock.NewTicker(time.Second)
	require.True(t, timer.Stop())
	require.False(t, timer.Stop())
	require.True(t, ticker.Stop())
	require.False(t, ticker.Stop())
	clock.Advance(time.Hour)
	select {
	case <-timer.Chan():
		t.Fatal("stopped timer fired")
	default:
	}
	select {
	case <-ticker.Chan():
		t.Fatal("stopped ticker fired")
	default:
	}
}

func TestDeterministicWindowStallTimeout(t *testing.T) {
	clock := newManualClock(time.Unix(700, 0))
	window := newCreditWindowWithClock(0, clock)
	done := make(chan error, 1)
	go func() { done <- window.Take(t.Context(), 1, time.Minute) }()
	require.Eventually(t, func() bool { return clock.EventCount() == 1 }, time.Second, time.Millisecond)
	clock.Advance(time.Minute)
	require.ErrorIs(t, <-done, errWindowStalled)
	require.Zero(t, clock.EventCount())
}

func TestDeterministicControlSendTimeoutStopsOwner(t *testing.T) {
	clock := newManualClock(time.Unix(800, 0))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	w := newFairWriter(ctx, 512, 15*time.Second, func(wire.Frame) error {
		once.Do(func() { close(started); <-release })
		return nil
	})
	w.clock = clock
	go w.Run()
	fill := wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: testStreamID(91), Payload: make([]byte, 512-wire.HeaderSize),
	}
	require.NoError(t, w.Enqueue(t.Context(), fill, nil))
	<-started
	session := &Session{
		generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts:       defaultSessionOptions(SessionOptions{WriteTimeout: 15 * time.Second, OpenCommitTimeout: time.Hour, clock: clock}),
		streams:    make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, clock.Now),
	}
	stream := newStream(session, ctx, t.Context(), testStreamID(53), 0)
	go stream.run()
	stream.Cancel(context.Canceled)
	require.Eventually(t, func() bool { return clock.EventCount() >= 2 }, time.Second, time.Millisecond)
	clock.Advance(15 * time.Second)
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("manual control-send timeout did not stop stream owner")
	}
	close(release)
	cancel()
	<-w.Done()
	require.Zero(t, clock.EventCount())
}

func TestDeterministicRemainingDeadlinePrecedesOpenCommit(t *testing.T) {
	clock := newManualClock(time.Unix(900, 0))
	conn := newMemorySessionConn()
	session := newSession(conn, 1, testLimits(1), SessionOptions{
		OpenCommitTimeout: 30 * time.Second, PingInterval: time.Hour, PongTimeout: time.Hour, clock: clock,
	})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	<-session.started
	request := validBoundRelayRequest("/v1/responses")
	request.Remaining = 10 * time.Second
	stream, err := session.OpenStream(t.Context(), request)
	require.NoError(t, err)
	<-conn.writes
	require.Eventually(t, func() bool { return clock.EventCount() >= 3 }, time.Second, time.Millisecond)
	clock.Advance(10 * time.Second)
	select {
	case <-stream.Done():
		require.ErrorIs(t, context.Cause(stream.ctx), context.DeadlineExceeded)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("manual Remaining deadline did not cancel stream")
	}
	session.Cancel(context.Canceled)
	<-runDone
}

func TestDeterministicOpenCommitPrecedesRemainingDeadline(t *testing.T) {
	clock := newManualClock(time.Unix(950, 0))
	conn := newMemorySessionConn()
	session := newSession(conn, 1, testLimits(1), SessionOptions{
		OpenCommitTimeout: 30 * time.Second, PingInterval: time.Hour, PongTimeout: time.Hour, clock: clock,
	})
	runDone := make(chan struct{})
	go func() { defer close(runDone); _ = session.Run(t.Context()) }()
	<-session.started
	request := validBoundRelayRequest("/v1/responses")
	request.Remaining = 40 * time.Second
	stream, err := session.OpenStream(t.Context(), request)
	require.NoError(t, err)
	<-conn.writes
	require.Eventually(t, func() bool { return clock.EventCount() >= 3 }, time.Second, time.Millisecond)
	clock.Advance(30 * time.Second)
	select {
	case <-stream.Done():
		require.ErrorIs(t, context.Cause(stream.ctx), errOpenCommitTimeout)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("open-commit timeout did not precede later Remaining")
	}
	session.Cancel(context.Canceled)
	<-runDone
}

func TestDeterministicRemainingAndWindowStallOrdering(t *testing.T) {
	tests := []struct {
		name      string
		remaining time.Duration
		stall     time.Duration
		want      error
	}{
		{name: "remaining_first", remaining: 10 * time.Second, stall: 20 * time.Second, want: context.DeadlineExceeded},
		{name: "stall_first", remaining: 20 * time.Second, stall: 10 * time.Second, want: errWindowStalled},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clock := newManualClock(time.Unix(975, 0))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
			session := &Session{generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
				opts:    defaultSessionOptions(SessionOptions{OpenCommitTimeout: time.Hour, WindowStallTimeout: test.stall, clock: clock}),
				streams: make(map[wire.StreamID]*Stream), tombstones: newTombstoneStore(8, time.Second, clock.Now)}
			stream := newStream(session, ctx, t.Context(), testStreamID(70), test.remaining)
			stream.commitState.Store(uint32(wire.Committed))
			stream.requestWindow.Set(0)
			go stream.run()
			done := make(chan error, 1)
			go func() { done <- stream.Upload(t.Context(), bytes.NewBufferString("x")) }()
			require.Eventually(t, func() bool { return clock.EventCount() >= 3 }, time.Second, time.Millisecond)
			if test.remaining < test.stall {
				clock.Advance(test.remaining)
			} else {
				clock.Advance(test.stall)
			}
			require.ErrorIs(t, <-done, test.want)
			stream.Cancel(context.Canceled)
			<-stream.Done()
		})
	}
}

type memoryMessage struct {
	messageType int
	payload     []byte
}

type memorySessionConn struct {
	mu            sync.Mutex
	inbound       chan memoryMessage
	writes        chan memoryMessage
	closed        chan struct{}
	closeOnce     sync.Once
	pongHandler   func(string) error
	readDeadline  time.Time
	readLimit     int64
	writeDeadline time.Time
	writeErr      error
	activeReaders atomic.Int32
	maxReaders    atomic.Int32
	activeWriters atomic.Int32
	maxWriters    atomic.Int32
}

func newMemorySessionConn() *memorySessionConn {
	return &memorySessionConn{
		inbound: make(chan memoryMessage, 32), writes: make(chan memoryMessage, 32), closed: make(chan struct{}),
	}
}

func (c *memorySessionConn) ReadMessage() (int, []byte, error) {
	active := c.activeReaders.Add(1)
	updateMax(&c.maxReaders, active)
	defer c.activeReaders.Add(-1)
	select {
	case message := <-c.inbound:
		c.mu.Lock()
		limit := c.readLimit
		c.mu.Unlock()
		if limit > 0 && int64(len(message.payload)) > limit {
			return 0, nil, errors.New("memory websocket: read limit exceeded")
		}
		return message.messageType, message.payload, nil
	case <-c.closed:
		return 0, nil, errSessionClosed
	}
}

func (c *memorySessionConn) SetReadLimit(limit int64) {
	c.mu.Lock()
	c.readLimit = limit
	c.mu.Unlock()
}

func (c *memorySessionConn) WriteMessage(messageType int, payload []byte) error {
	active := c.activeWriters.Add(1)
	updateMax(&c.maxWriters, active)
	defer c.activeWriters.Add(-1)
	c.mu.Lock()
	err := c.writeErr
	c.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case c.writes <- memoryMessage{messageType: messageType, payload: append([]byte(nil), payload...)}:
		return nil
	case <-c.closed:
		return errSessionClosed
	}
}

func (c *memorySessionConn) WriteControl(messageType int, payload []byte, _ time.Time) error {
	return c.WriteMessage(messageType, payload)
}

func (c *memorySessionConn) SetReadDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.readDeadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *memorySessionConn) SetWriteDeadline(deadline time.Time) error {
	c.mu.Lock()
	c.writeDeadline = deadline
	c.mu.Unlock()
	return nil
}

func (c *memorySessionConn) SetPongHandler(handler func(string) error) {
	c.mu.Lock()
	c.pongHandler = handler
	c.mu.Unlock()
}

func (c *memorySessionConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *memorySessionConn) triggerPong() error {
	c.mu.Lock()
	handler := c.pongHandler
	c.mu.Unlock()
	return handler("")
}

func updateMax(maximum *atomic.Int32, value int32) {
	for current := maximum.Load(); value > current; current = maximum.Load() {
		if maximum.CompareAndSwap(current, value) {
			return
		}
	}
}

func decodeMemoryWrite(t *testing.T, message memoryMessage, limits wire.Limits) wire.Frame {
	t.Helper()
	require.Equal(t, websocket.BinaryMessage, message.messageType)
	frame, err := wire.Decode(message.payload, limits)
	require.NoError(t, err)
	return frame
}

type manualClock struct {
	mu     sync.Mutex
	now    time.Time
	events map[*manualClockEvent]struct{}
}

type manualClockEvent struct {
	clock    *manualClock
	deadline time.Time
	interval time.Duration
	channel  chan time.Time
	active   bool
}

func newManualClock(now time.Time) *manualClock {
	return &manualClock{now: now, events: make(map[*manualClockEvent]struct{})}
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) NewTimer(duration time.Duration) sessionTimer {
	return c.newEvent(duration, 0)
}

func (c *manualClock) NewTicker(duration time.Duration) sessionTicker {
	return c.newEvent(duration, duration)
}

func (c *manualClock) newEvent(duration, interval time.Duration) *manualClockEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	event := &manualClockEvent{
		clock: c, deadline: c.now.Add(duration), interval: interval, channel: make(chan time.Time, 1), active: true,
	}
	c.events[event] = struct{}{}
	return event
}

func (c *manualClock) Advance(duration time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(duration)
	now := c.now
	var due []*manualClockEvent
	for event := range c.events {
		if !event.active || event.deadline.After(now) {
			continue
		}
		due = append(due, event)
		if event.interval == 0 {
			event.active = false
			delete(c.events, event)
		} else {
			for !event.deadline.After(now) {
				event.deadline = event.deadline.Add(event.interval)
			}
		}
	}
	c.mu.Unlock()
	for _, event := range due {
		select {
		case event.channel <- now:
		default:
		}
	}
}

func (c *manualClock) EventCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.events)
}

func (e *manualClockEvent) Chan() <-chan time.Time { return e.channel }

func (e *manualClockEvent) Stop() bool {
	e.clock.mu.Lock()
	defer e.clock.mu.Unlock()
	if !e.active {
		return false
	}
	e.active = false
	delete(e.clock.events, e)
	return true
}
