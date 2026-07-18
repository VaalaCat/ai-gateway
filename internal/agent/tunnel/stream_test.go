package tunnel

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func TestStreamCommitUploadAndCopyResponse(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	request := validBoundRelayRequest("/v1/responses")
	request.BodyLength = 6
	stream, err := session.OpenStream(t.Context(), request)
	require.NoError(t, err)
	open := readPeerFrame(t, peer, limits)

	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: open.StreamID, Sequence: 1,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: 3}, limits)})
	commitDone := make(chan error, 1)
	go func() { commitDone <- stream.Commit(t.Context()) }()
	commit := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameCommit, commit.Type)
	require.EqualValues(t, 2, commit.Sequence)
	require.Equal(t, wire.CommitUncertain, stream.CommitState())
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: open.StreamID, Sequence: 2})
	require.NoError(t, <-commitDone)
	require.Equal(t, wire.Committed, stream.CommitState())

	uploadDone := make(chan error, 1)
	go func() { uploadDone <- stream.Upload(t.Context(), bytes.NewBufferString("abcdef")) }()
	data := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameRequestData, data.Type)
	require.EqualValues(t, 3, data.Sequence)
	require.Equal(t, "abc", string(data.Payload))
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate, StreamID: open.StreamID, Sequence: 3,
		Payload: mustMetadata(t, wire.WindowUpdate{Bytes: 3}, limits)})
	data = readPeerFrame(t, peer, limits)
	require.Equal(t, "def", string(data.Payload))
	require.EqualValues(t, 4, data.Sequence)
	requestEnd := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameRequestEnd, requestEnd.Type)
	require.EqualValues(t, 5, requestEnd.Sequence)
	require.NoError(t, <-uploadDone)

	responseDone := make(chan error, 1)
	recorder := httptest.NewRecorder()
	go func() { responseDone <- stream.CopyResponse(t.Context(), recorder) }()
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID, Sequence: 4,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusCreated, Header: http.Header{"X-Relay": {"yes"}}}, limits)})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: open.StreamID, Sequence: 5, Payload: []byte("xyz")})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID, Sequence: 6})
	require.NoError(t, <-responseDone)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "yes", recorder.Header().Get("X-Relay"))
	require.Equal(t, "xyz", recorder.Body.String())
}

func TestStreamRejectsInvalidResponseHeaderBeforeWriterMutation(t *testing.T) {
	for _, header := range []map[string][]string{
		{"Bad Header": {"value"}},
		{"X-Test": {"bad\r\nvalue"}},
	} {
		limits := testLimits(1)
		session, peer := startTestSession(t, limits, SessionOptions{})
		stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
		writer := &mutationTrackingWriter{header: make(http.Header)}
		done := make(chan error, 1)
		go func() { done <- stream.CopyResponse(t.Context(), writer) }()
		require.Eventually(t, func() bool {
			stream.responseOwner.mu.Lock()
			defer stream.responseOwner.mu.Unlock()
			return stream.responseOwner.claimed
		}, time.Second, time.Millisecond)
		writePeerFrame(t, peer, limits, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
			Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK, Header: header}, limits),
		})
		require.Equal(t, wire.FrameReset, readPeerFrame(t, peer, limits).Type)
		require.ErrorIs(t, <-done, errStreamProtocol)
		require.Zero(t, writer.headerCalls)
		require.Zero(t, writer.writeCalls)
	}
}

func TestStreamCommitDeadlineWhileWaitingForReady(t *testing.T) {
	session, peer := startTestSession(t, testLimits(1), SessionOptions{})
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	_ = readPeerFrame(t, peer, testLimits(1))
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, stream.Commit(ctx), context.DeadlineExceeded)
	require.Equal(t, wire.PreCommit, stream.CommitState())
}

func TestStreamOpenCommitTimeoutCancelsPreCommitAndReturnsAdmission(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{OpenCommitTimeout: 20 * time.Millisecond})
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	_ = readPeerFrame(t, peer, limits)
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("pre-commit stream did not time out")
	}
	second, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	second.Cancel(context.Canceled)
}

func TestStreamOpenCommitTimeoutCancelsCommitUncertain(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{OpenCommitTimeout: 20 * time.Millisecond})
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	open := readPeerFrame(t, peer, limits)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits),
	})
	commitDone := make(chan error, 1)
	go func() { commitDone <- stream.Commit(t.Context()) }()
	require.Equal(t, wire.FrameCommit, readPeerFrame(t, peer, limits).Type)
	require.Equal(t, wire.CommitUncertain, stream.CommitState())
	select {
	case err := <-commitDone:
		require.ErrorIs(t, err, errOpenCommitTimeout)
	case <-time.After(time.Second):
		t.Fatal("commit uncertain stream waited forever")
	}
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("commit uncertain stream owner did not exit")
	}
}

func TestStreamRequestDeadlineWinsOverOpenCommitTimeout(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{OpenCommitTimeout: time.Second})
	request := validBoundRelayRequest("/v1/responses")
	request.Remaining = 20 * time.Millisecond
	stream, err := session.OpenStream(t.Context(), request)
	require.NoError(t, err)
	open := readPeerFrame(t, peer, limits)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits),
	})
	commitDone := make(chan error, 1)
	go func() { commitDone <- stream.Commit(t.Context()) }()
	require.Equal(t, wire.FrameCommit, readPeerFrame(t, peer, limits).Type)
	select {
	case err := <-commitDone:
		require.ErrorIs(t, err, context.DeadlineExceeded)
	case <-time.After(time.Second):
		t.Fatal("short request deadline did not cancel Commit")
	}
}

func TestStreamCommitResultIsRepeatable(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, _ := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	for range 2 {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		require.NoError(t, stream.Commit(ctx))
		cancel()
	}
}

func TestStreamCommitSocketFailureRemainsUncertain(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	socketErr := errors.New("socket failed after accept")
	entered := make(chan struct{})
	release := make(chan struct{})
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error {
		close(entered)
		<-release
		return socketErr
	})
	session := &Session{
		generation: 1,
		limits:     testLimits(1),
		opts:       defaultSessionOptions(SessionOptions{}),
		ctx:        ctx,
		writer:     w,
		streams:    make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(1, time.Second, time.Now),
	}
	stream := newStream(session, ctx, t.Context(), testStreamID(42), 0)
	w.onError = stream.Cancel
	stream.signalReady(nil)
	go w.Run()
	go stream.run()
	commitDone := make(chan error, 1)
	go func() { commitDone <- stream.Commit(t.Context()) }()
	<-entered
	require.Equal(t, wire.CommitUncertain, stream.CommitState())
	close(release)
	require.ErrorIs(t, <-commitDone, socketErr)
	require.Equal(t, wire.CommitUncertain, stream.CommitState())
	<-stream.Done()
	<-w.Done()
}

func TestStreamUploadNoDeadlineStopsAtWindowStallTimeout(t *testing.T) {
	limits := testLimits(1)
	limits.InitialStreamWindow = 1
	session, peer := startTestSession(t, limits, SessionOptions{WindowStallTimeout: 20 * time.Millisecond})
	stream, open := committedTestStream(t, session, peer, limits, 1)
	done := make(chan error, 1)
	go func() { done <- stream.Upload(t.Context(), bytes.NewBufferString("ab")) }()
	frame := readPeerFrame(t, peer, limits)
	require.Equal(t, "a", string(frame.Payload))
	require.Error(t, <-done)
	_ = open
}

func TestStreamRequestDeadlineWinsOverWindowStall(t *testing.T) {
	limits := testLimits(1)
	limits.InitialStreamWindow = 1
	session, peer := startTestSession(t, limits, SessionOptions{WindowStallTimeout: time.Second})
	stream, _ := committedTestStream(t, session, peer, limits, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := stream.Upload(ctx, bytes.NewBufferString("ab"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestStreamDuplicateTerminalAndLateDataUseTombstone(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, 3)
	recorder := httptest.NewRecorder()
	done := make(chan error, 1)
	go func() { done <- stream.CopyResponse(t.Context(), recorder) }()
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits)})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID})
	require.NoError(t, <-done)
	require.NoError(t, stream.Close())
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID})
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: open.StreamID, Payload: []byte("old")})
	require.Never(t, func() bool {
		select {
		case <-session.Done():
			return true
		default:
			return false
		}
	}, 30*time.Millisecond, 5*time.Millisecond)
}

func TestStreamFastHeadersEndReturnsSuccessDeterministically(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	done := make(chan error, 1)
	go func() { done <- stream.CopyResponse(t.Context(), httptest.NewRecorder()) }()
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusNoContent}, limits),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
	})
	require.NoError(t, <-done)
}

func TestStreamBufferedHeadersWinOverTerminalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 200 {
		headers := make(chan headersResult, 1)
		headers <- headersResult{headers: wire.Headers{StatusCode: http.StatusOK}}
		got, err := waitHeaders(ctx, headers)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, got.StatusCode)
	}
}

func TestStreamCancelWakesBlockedUpload(t *testing.T) {
	limits := testLimits(1)
	limits.InitialStreamWindow = 1
	session, peer := startTestSession(t, limits, SessionOptions{WindowStallTimeout: time.Second})
	stream, _ := committedTestStream(t, session, peer, limits, 1)
	done := make(chan error, 1)
	go func() { done <- stream.Upload(t.Context(), io.LimitReader(zeroReader{}, 2)) }()
	_ = readPeerFrame(t, peer, limits)
	stream.Cancel(context.Canceled)
	require.Error(t, <-done)
}

func TestStreamCancelClosesBlockedUploadReader(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{WindowStallTimeout: time.Second})
	stream, _ := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	src := newBlockingReadCloser()
	uploadDone := make(chan error, 1)
	go func() { uploadDone <- stream.Upload(t.Context(), src) }()
	<-src.started
	stream.Cancel(context.Canceled)
	select {
	case err := <-uploadDone:
		require.Error(t, err)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Upload remained blocked after stream cancellation")
	}
	select {
	case <-stream.Done():
	case <-time.After(50 * time.Millisecond):
		t.Fatal("stream owner waited forever for blocked Upload")
	}
}

func TestStreamFailedEnqueueDoesNotConsumeSequence(t *testing.T) {
	ctx, cancelSession := context.WithCancel(t.Context())
	defer cancelSession()
	written := make(chan wire.Frame, 2)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		written <- frame
		return nil
	})
	session := &Session{
		generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	stream := newStream(session, ctx, t.Context(), testStreamID(61), 0)
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, stream.enqueue(cancelled, wire.FrameWindowUpdate, []byte(`{"bytes":1}`)), context.Canceled)
	go w.Run()
	require.NoError(t, stream.enqueue(t.Context(), wire.FrameRequestEnd, nil))
	require.EqualValues(t, 2, (<-written).Sequence)
	stream.abortBeforeRun(context.Canceled)
}

func TestStreamConcurrentEnqueueCommitsFIFOSequences(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	written := make(chan wire.Frame, 2)
	w := newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error { written <- frame; return nil })
	go w.Run()
	session := &Session{generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(62), 0)
	results := make(chan error, 2)
	go func() { results <- stream.enqueue(t.Context(), wire.FrameRequestData, []byte("a")) }()
	go func() { results <- stream.enqueue(t.Context(), wire.FrameRequestData, []byte("b")) }()
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	first, second := <-written, <-written
	require.EqualValues(t, 2, first.Sequence)
	require.EqualValues(t, 3, second.Sequence)
	stream.abortBeforeRun(context.Canceled)
}

func TestStreamPostTerminalCopyDrainsBufferedDataWithoutWindowUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	limits := testLimits(1)
	limits.MaxDataBytes = 1
	limits.InitialStreamWindow = 1000
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	session := &Session{generation: 1, limits: limits, ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(63), 0)
	stream.receivePhase = receiveCommitted
	stream.receiveSeq = 2
	go stream.run()
	stream.inbound <- wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, Sequence: 3, StreamID: stream.id,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits)}
	for i := uint32(4); i < 1004; i++ {
		stream.inbound <- wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameResponseData, Sequence: i, StreamID: stream.id, Payload: []byte("x")}
	}
	stream.inbound <- wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, Sequence: 1004, StreamID: stream.id}
	require.Never(t, func() bool {
		select {
		case <-stream.Done():
			return true
		default:
			return false
		}
	}, 20*time.Millisecond, time.Millisecond)
	recorder := httptest.NewRecorder()
	require.NoError(t, stream.CopyResponse(t.Context(), recorder))
	<-stream.Done()
	require.Len(t, recorder.Body.Bytes(), 1000)
	queuedBytes, queuedStreams := w.stats()
	require.Zero(t, queuedBytes)
	require.Zero(t, queuedStreams)
}

func TestStreamCloseAbandonsUnclaimedSuccessfulResponse(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	session := &Session{generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(64), 0)
	stream.receivePhase = receiveCommitted
	stream.receiveSeq = 2
	go stream.run()
	stream.inbound <- wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameHeaders, Sequence: 3, StreamID: stream.id,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, session.limits)}
	stream.inbound <- wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameEnd, Sequence: 4, StreamID: stream.id}
	require.Eventually(t, stream.isTerminalSuccess, time.Second, time.Millisecond)
	require.Never(t, func() bool {
		select {
		case <-stream.Done():
			return true
		default:
			return false
		}
	}, 20*time.Millisecond, time.Millisecond)
	require.NoError(t, stream.Close())
	recorder := httptest.NewRecorder()
	require.Error(t, stream.CopyResponse(t.Context(), recorder))
	require.Zero(t, recorder.Body.Len())
}

func TestStreamCopyCallerCancelJoinsDoneAndRejectsSecondConsumer(t *testing.T) {
	ctx, cancelSession := context.WithCancel(t.Context())
	defer cancelSession()
	w := newFairWriter(ctx, 4096, time.Second, func(wire.Frame) error { return nil })
	session := &Session{generation: 1, limits: testLimits(1), ctx: ctx, writer: w,
		opts: defaultSessionOptions(SessionOptions{}), streams: make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now)}
	stream := newStream(session, ctx, t.Context(), testStreamID(65), 0)
	go stream.run()
	caller, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- stream.CopyResponse(caller, httptest.NewRecorder()) }()
	require.Eventually(t, func() bool {
		stream.responseOwner.mu.Lock()
		defer stream.responseOwner.mu.Unlock()
		return stream.responseOwner.claimed
	}, time.Second, time.Millisecond)
	require.ErrorIs(t, stream.CopyResponse(t.Context(), httptest.NewRecorder()), errCopyStarted)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("Done did not join cancelled response consumer")
	}
}

func TestStreamCopyResponseQueueWaitHonorsCallerCancel(t *testing.T) {
	session, writer, release, cancelSession := blockedWriterSession(t, 20*time.Millisecond)
	defer cancelSession()
	defer release()
	stream := newStream(session, session.ctx, t.Context(), testStreamID(51), 0)
	stream.receivePhase = receiveCommitted
	stream.receiveSeq = 2
	go stream.run()
	stream.inbound <- wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, Sequence: 3, StreamID: stream.id,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, session.limits),
	}
	stream.inbound <- wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameResponseData, Sequence: 4, StreamID: stream.id, Payload: []byte("abc"),
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	recorder := newWriteNotifyingRecorder()
	go func() { done <- stream.CopyResponse(ctx, recorder) }()
	select {
	case <-recorder.wrote:
	case <-time.After(time.Second):
		t.Fatal("CopyResponse did not write response DATA")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(50 * time.Millisecond):
		t.Fatal("WINDOW_UPDATE queue wait ignored caller cancellation")
	}
	stream.Cancel(context.Canceled)
	_ = writer
}

func TestStreamCancelBoundedWhenControlQueueIsFull(t *testing.T) {
	session, _, release, cancelSession := blockedWriterSession(t, 20*time.Millisecond)
	defer cancelSession()
	defer release()
	stream := newStream(session, session.ctx, t.Context(), testStreamID(52), 0)
	go stream.run()
	stream.Cancel(context.Canceled)
	select {
	case <-stream.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("stream owner blocked indefinitely enqueueing RESET")
	}
}

func blockedWriterSession(t *testing.T, writeTimeout time.Duration) (*Session, *fairWriter, func(), context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce sync.Once
	var releaseOnce sync.Once
	w := newFairWriter(ctx, 512, writeTimeout, func(wire.Frame) error {
		startOnce.Do(func() { close(started); <-release })
		return nil
	})
	go w.Run()
	fill := wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: testStreamID(90), Payload: make([]byte, 512-wire.HeaderSize),
	}
	require.NoError(t, w.Enqueue(t.Context(), fill, nil))
	<-started
	limits := testLimits(2)
	session := &Session{
		generation: 1, limits: limits, ctx: ctx, writer: w,
		opts:       defaultSessionOptions(SessionOptions{WriteTimeout: writeTimeout, OpenCommitTimeout: time.Second}),
		streams:    make(map[wire.StreamID]*Stream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	releaseFn := func() { releaseOnce.Do(func() { close(release) }) }
	cancelFn := func() {
		releaseFn()
		cancel()
		<-w.Done()
	}
	return session, w, releaseFn, cancelFn
}

func committedTestStream(t *testing.T, session *Session, peer *websocket.Conn, limits wire.Limits, requestWindow int64) (*Stream, wire.Frame) {
	t.Helper()
	stream, err := session.OpenStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	open := readPeerFrame(t, peer, limits)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: requestWindow}, limits)})
	done := make(chan error, 1)
	go func() { done <- stream.Commit(t.Context()) }()
	require.Equal(t, wire.FrameCommit, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: open.StreamID})
	require.NoError(t, <-done)
	return stream, open
}

func mustMetadata[T any](t *testing.T, value T, limits wire.Limits) []byte {
	t.Helper()
	payload, err := wire.EncodeMetadata(value, limits.MaxMetadataBytes)
	require.NoError(t, err)
	return payload
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0
	}
	return len(p), nil
}

type blockingReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func TestStreamCopyResponseAppliesFinalTrailersFromEnd(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK, Trailer: http.Header{"X-Usage": nil}}, limits),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Trailers{Header: http.Header{"X-Usage": {"tokens=7"}}}, limits),
	})
	recorder := httptest.NewRecorder()
	require.NoError(t, stream.CopyResponse(t.Context(), recorder))
	require.Equal(t, "tokens=7", recorder.Result().Trailer.Get("X-Usage"))
}

func TestStreamCopiesLateDynamicFinalTrailerOverHTTP(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	copyDone := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		copyDone <- stream.CopyResponse(t.Context(), w)
	}))
	defer server.Close()
	type clientResult struct {
		response *http.Response
		body     []byte
		err      error
	}
	clientDone := make(chan clientResult, 1)
	go func() {
		response, err := server.Client().Get(server.URL)
		if err != nil {
			clientDone <- clientResult{err: err}
			return
		}
		body, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		clientDone <- clientResult{response: response, body: body, err: readErr}
	}()

	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusCreated}, limits),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: open.StreamID, Payload: []byte("xyz"),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Trailers{
			Header: http.Header{"X-Usage": {"tokens=7"}}, Dynamic: []string{"X-Usage"},
		}, limits),
	})

	require.NoError(t, <-copyDone)
	result := <-clientDone
	require.NoError(t, result.err)
	require.Equal(t, http.StatusCreated, result.response.StatusCode)
	require.Equal(t, "xyz", string(result.body))
	require.Empty(t, result.response.Header.Values("X-Usage"))
	require.Equal(t, "tokens=7", result.response.Trailer.Get("X-Usage"))
}

func TestStreamRejectsUndeclaredFinalTrailerWithoutDynamicMarker(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	recorder := httptest.NewRecorder()
	copyDone := make(chan error, 1)
	go func() { copyDone <- stream.CopyResponse(t.Context(), recorder) }()
	require.Eventually(t, func() bool {
		stream.responseOwner.mu.Lock()
		defer stream.responseOwner.mu.Unlock()
		return stream.responseOwner.claimed
	}, time.Second, time.Millisecond)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Trailers{Header: http.Header{"X-Usage": {"tokens=7"}}}, limits),
	})
	require.ErrorIs(t, <-copyDone, errStreamProtocol)
	require.Empty(t, recorder.Result().Trailer.Get("X-Usage"))
}

func TestStreamRejectsInvalidDynamicFinalTrailerMetadata(t *testing.T) {
	tests := []struct {
		name     string
		declared http.Header
		final    wire.Trailers
	}{
		{
			name:  "duplicate dynamic name",
			final: wire.Trailers{Header: http.Header{"X-Usage": {"tokens=7"}}, Dynamic: []string{"X-Usage", "x-usage"}},
		},
		{
			name:     "dynamic name already declared",
			declared: http.Header{"X-Usage": nil},
			final:    wire.Trailers{Header: http.Header{"X-Usage": {"tokens=7"}}, Dynamic: []string{"X-Usage"}},
		},
		{
			name:  "dynamic name without final value",
			final: wire.Trailers{Dynamic: []string{"X-Usage"}},
		},
		{
			name:  "invalid dynamic name",
			final: wire.Trailers{Header: http.Header{"X-Usage": {"tokens=7"}}, Dynamic: []string{"Bad Key"}},
		},
		{
			name:  "invalid dynamic value",
			final: wire.Trailers{Header: http.Header{"X-Usage": {"bad\r\nvalue"}}, Dynamic: []string{"X-Usage"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			limits := testLimits(1)
			session, peer := startTestSession(t, limits, SessionOptions{})
			stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
			recorder := httptest.NewRecorder()
			copyDone := make(chan error, 1)
			go func() { copyDone <- stream.CopyResponse(t.Context(), recorder) }()
			require.Eventually(t, func() bool {
				stream.responseOwner.mu.Lock()
				defer stream.responseOwner.mu.Unlock()
				return stream.responseOwner.claimed
			}, time.Second, time.Millisecond)
			writePeerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
				Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK, Trailer: tt.declared}, limits),
			})
			writePeerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
				Payload: mustMetadata(t, tt.final, limits),
			})
			require.ErrorIs(t, <-copyDone, errStreamProtocol)
			require.Empty(t, recorder.Result().Trailer.Get("X-Usage"))
		})
	}
}

func TestStreamRejectsInvalidFinalTrailersWithoutInjection(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{})
	stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
	recorder := httptest.NewRecorder()
	copyDone := make(chan error, 1)
	go func() { copyDone <- stream.CopyResponse(t.Context(), recorder) }()
	require.Eventually(t, func() bool {
		stream.responseOwner.mu.Lock()
		defer stream.responseOwner.mu.Unlock()
		return stream.responseOwner.claimed
	}, time.Second, time.Millisecond)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK, Trailer: http.Header{"X-Usage": nil}}, limits),
	})
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameEnd, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Trailers{Header: http.Header{"Content-Length": {"bad"}}}, limits),
	})
	require.ErrorIs(t, <-copyDone, errStreamProtocol)
	require.Empty(t, recorder.Result().Trailer.Get("Content-Length"))
}

type writeNotifyingRecorder struct {
	*httptest.ResponseRecorder
	wrote chan struct{}
	once  sync.Once
}

func newWriteNotifyingRecorder() *writeNotifyingRecorder {
	return &writeNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), wrote: make(chan struct{})}
}

func (r *writeNotifyingRecorder) Write(payload []byte) (int, error) {
	n, err := r.ResponseRecorder.Write(payload)
	r.once.Do(func() { close(r.wrote) })
	return n, err
}

func newBlockingReadCloser() *blockingReadCloser {
	return &blockingReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (r *blockingReadCloser) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.closed
	return 0, context.Canceled
}

func (r *blockingReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}
