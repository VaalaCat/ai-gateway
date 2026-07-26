package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestAttemptStreamRequestCancellationDetachesStreamAndPreservesRemainingDeadline(t *testing.T) {
	clock := newManualClock(time.Unix(1_700_000_000, 0))
	sessionCtx, cancelSession := context.WithCancel(t.Context())
	t.Cleanup(cancelSession)
	session := newSessionValue(nil, 1, testLimits(1), SessionOptions{Direction: SessionDirectionRelay, clock: clock})
	session.ctx = sessionCtx
	requestCtx, cancelRequest := context.WithCancel(t.Context())
	stream := newStream(session, sessionCtx, testStreamID(101), 7*time.Second, "request-a")
	operationCtx, stopOperation := stream.operationContext(requestCtx)
	defer stopOperation()

	cancelRequest()
	require.Eventually(t, func() bool {
		return errors.Is(context.Cause(operationCtx), context.Canceled)
	}, time.Second, time.Millisecond)
	require.NoError(t, stream.ctx.Err(), "caller cancellation must not own the stream lifetime")
	require.Equal(t, "request-a", stream.requestID)
	clock.Advance(7 * time.Second)
	require.Eventually(t, func() bool {
		return errors.Is(context.Cause(stream.ctx), context.DeadlineExceeded)
	}, time.Second, time.Millisecond)
}

func TestAttemptResponseDrainCancellationBeforeResponseDoesNotDrain(t *testing.T) {
	stream, clock, _ := newAttemptDrainTestStream(t, SessionDirectionRelay)
	caller, cancel := context.WithCancel(t.Context())
	done := copyAttemptAsync(stream, caller, httptest.NewRecorder())
	cancel()

	got := receiveAttemptCopy(t, done)
	require.Zero(t, got.result)
	require.ErrorIs(t, got.err, context.Canceled)
	require.ErrorIs(t, context.Cause(stream.ctx), context.Canceled)
	require.Zero(t, clock.EventCount(), "response-start cancellation must not create a drain timer")
}

func TestAttemptResponseDrainPreCanceledCallerDoesNotConsumeBufferedHeaders(t *testing.T) {
	stream, clock, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	caller, cancel := context.WithCancel(t.Context())
	cancel()
	writer := newHeaderNotifyingRecorder()

	got := receiveAttemptCopy(t, copyAttemptAsync(stream, caller, writer))
	require.Zero(t, got.result)
	require.ErrorIs(t, got.err, context.Canceled)
	select {
	case <-writer.wroteHeader:
		t.Fatal("pre-canceled caller received buffered response headers")
	default:
	}
	require.Zero(t, clock.EventCount(), "pre-response cancellation must not create a drain timer")
	require.Zero(t, logs.Len(), "pre-response cancellation must not emit a drain-finished log")
}

func TestAttemptResponseDrainTimesOutWithoutResultAndReturnsCallerCancellation(t *testing.T) {
	stream, clock, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	caller, cancel := context.WithCancel(t.Context())
	writer := newHeaderNotifyingRecorder()
	done := copyAttemptAsync(stream, caller, writer)
	<-writer.wroteHeader
	cancel()
	waitForDrainTimer(t, clock)
	requireNoAttemptCopy(t, done)

	clock.Advance(attemptResultDrainTimeout)
	got := receiveAttemptCopy(t, done)
	require.Zero(t, got.result)
	require.ErrorIs(t, got.err, context.Canceled)
	require.ErrorIs(t, context.Cause(stream.ctx), errAttemptResultDrainTimeout)
	requireDrainLog(t, logs, "timeout", 0, false, false, "")
}

func TestAttemptResponseDrainCallerDeadlineAfterResponseDoesNotDrain(t *testing.T) {
	stream, clock, _ := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	caller, cancel := context.WithCancelCause(t.Context())
	writer := newHeaderNotifyingRecorder()
	done := copyAttemptAsync(stream, caller, writer)
	<-writer.wroteHeader
	cancel(context.DeadlineExceeded)

	got := receiveAttemptCopy(t, done)
	require.Zero(t, got.result)
	require.ErrorIs(t, got.err, context.DeadlineExceeded)
	require.ErrorIs(t, context.Cause(stream.ctx), context.DeadlineExceeded)
	require.Zero(t, clock.EventCount(), "caller deadline must not create a drain timer")
}

func TestAttemptResponseDrainReturnsLateResultAfterFullyDeliveredBody(t *testing.T) {
	stream, _, logs, outgoing := newAttemptDrainTestStreamWithFrames(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	require.False(t, stream.handleFrame(responseDataFrame(stream, 4, "complete")))
	caller, cancel := context.WithCancel(t.Context())
	writer := httptest.NewRecorder()
	done := copyAttemptAsync(stream, caller, writer)
	require.EqualValues(t, len("complete"), readWindowUpdate(t, outgoing).Bytes)
	cancel()

	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true}
	finishAttemptStream(t, stream, 5, want)
	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.NoError(t, got.err)
	require.Equal(t, "complete", writer.Body.String())
	require.Zero(t, stream.session.incomingSize())
	requireDrainLog(t, logs, "none", 0, true, true, string(attemptwire.ResultSucceeded))
}

func TestAttemptResponseDrainDiscardsPartialAndLateBodyButPreservesWriterErrorAndResult(t *testing.T) {
	stream, _, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	require.False(t, stream.handleFrame(responseDataFrame(stream, 4, "first")))
	writerErr := errors.New("client writer failed")
	writer := &countingErrorWriter{ResponseWriter: httptest.NewRecorder(), n: 2, err: writerErr, wrote: make(chan struct{})}
	done := copyAttemptAsync(stream, t.Context(), writer)
	<-writer.wrote
	require.False(t, stream.handleFrame(responseDataFrame(stream, 5, "late")))
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ResponseStarted: true}
	finishAttemptStream(t, stream, 6, want)

	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.ErrorIs(t, got.err, writerErr)
	require.Zero(t, stream.session.incomingSize(), "all response chunks must release incoming budget")
	requireDrainLog(t, logs, "other", 7, true, true, string(attemptwire.ResultProviderFailed))
}

func TestAttemptResponseDrainPreservesResultWhenEndTimesOut(t *testing.T) {
	stream, clock, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	caller, cancel := context.WithCancel(t.Context())
	writer := newHeaderNotifyingRecorder()
	done := copyAttemptAsync(stream, caller, writer)
	<-writer.wroteHeader
	cancel()
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload}))
	waitForDrainTimer(t, clock)
	clock.Advance(attemptResultDrainTimeout)

	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.ErrorIs(t, got.err, errAttemptResultDrainTimeout)
	requireDrainLog(t, logs, "timeout", 0, true, false, string(attemptwire.ResultSucceeded))
}

func TestAttemptResponseDrainRetriesCanceledWindowUpdateBeforeTargetContinues(t *testing.T) {
	stream, clock, logs, outgoing := newRunningAttemptDrainTestStream(t)
	stream.inbound <- responseHeadersFrame(t, stream, 3)
	stream.inbound <- responseDataFrame(stream, 4, "one")
	caller, cancel := context.WithCancel(t.Context())
	writer := &cancelingFullWriter{ResponseWriter: httptest.NewRecorder(), cancel: cancel}
	done := copyAttemptAsync(stream, caller, writer)

	select {
	case frame := <-outgoing:
		require.Equal(t, wire.FrameWindowUpdate, frame.Type)
		var update wire.WindowUpdate
		require.NoError(t, wire.DecodeMetadata(frame.Payload, &update, stream.session.limits.MaxMetadataBytes))
		require.EqualValues(t, 3, update.Bytes)
	case <-time.After(50 * time.Millisecond):
		clock.Advance(attemptResultDrainTimeout)
		_ = receiveAttemptCopy(t, done)
		t.Fatal("target did not receive WindowUpdate for the consumed chunk")
	}
	require.Never(t, func() bool { return len(outgoing) > 0 }, 20*time.Millisecond, time.Millisecond,
		"consumed chunk emitted more than one WindowUpdate before target continued")

	stream.inbound <- responseDataFrame(stream, 5, "two")
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	stream.inbound <- wire.Frame{Type: wire.FrameAttemptResult, Sequence: 6, StreamID: stream.id, Payload: payload}
	stream.inbound <- wire.Frame{Type: wire.FrameEnd, Sequence: 7, StreamID: stream.id}

	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.ErrorIs(t, got.err, context.Canceled)
	require.Zero(t, stream.session.incomingSize())
	select {
	case <-stream.Done():
	case <-time.After(time.Second):
		t.Fatal("stream.run did not finalize the drained stream")
	}
	requireDrainLog(t, logs, "canceled", 3, true, true, string(attemptwire.ResultSucceeded))
}

func TestAttemptResponseDrainWriteErrorWithCallerDeadlineTerminatesImmediately(t *testing.T) {
	stream, clock, _ := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	require.False(t, stream.handleFrame(responseDataFrame(stream, 4, "abc")))
	caller, cancel := context.WithCancelCause(t.Context())
	writerErr := errors.New("client write failed at deadline")
	writer := &causeOnWriteWriter{
		ResponseWriter: httptest.NewRecorder(), n: 1, err: writerErr,
		cancel: func() { cancel(context.DeadlineExceeded) },
	}
	done := copyAttemptAsync(stream, caller, writer)

	select {
	case got := <-done:
		require.Zero(t, got.result)
		require.ErrorIs(t, got.err, writerErr)
		require.ErrorIs(t, got.err, context.DeadlineExceeded)
		require.Zero(t, clock.EventCount(), "caller deadline must not start result drain")
	case <-time.After(50 * time.Millisecond):
		clock.Advance(attemptResultDrainTimeout)
		_ = receiveAttemptCopy(t, done)
		t.Fatal("Write failure crossed with caller deadline entered result drain")
	}
}

func TestAttemptResponseDrainWriteAndTransportErrorsTerminateAndRemainDiscoverable(t *testing.T) {
	stream, clock, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	require.False(t, stream.handleFrame(responseDataFrame(stream, 4, "abc")))
	transportErr := errors.New("window update transport unavailable")
	writerCtx, cancelWriter := context.WithCancelCause(t.Context())
	cancelWriter(transportErr)
	stream.session.writer = newFairWriter(writerCtx, 4096, time.Second, func(wire.Frame) error { return nil })
	writeErr := errors.New("client writer failed too")
	writer := &countingErrorWriter{ResponseWriter: httptest.NewRecorder(), n: 1, err: writeErr, wrote: make(chan struct{})}
	done := copyAttemptAsync(stream, t.Context(), writer)

	select {
	case got := <-done:
		require.Zero(t, got.result)
		require.ErrorIs(t, got.err, writeErr)
		require.ErrorIs(t, got.err, transportErr)
		require.Zero(t, clock.EventCount(), "transport failure must not start result drain")
		require.Zero(t, logs.Len(), "immediate transport failure must not emit a drain-finished log")
	case <-time.After(50 * time.Millisecond):
		clock.Advance(attemptResultDrainTimeout)
		_ = receiveAttemptCopy(t, done)
		t.Fatal("confirmed transport failure entered result drain")
	}
}

func TestAttemptResponseDrainJoinsReadTerminalAndOriginalErrors(t *testing.T) {
	stream, _, _ := newAttemptDrainTestStream(t, SessionDirectionRelay)
	resetErr := &StreamResetError{reset: wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}}
	stream.setTerminal(resetErr)
	_, _, _, snapshotErr := stream.finishDrainSnapshot(errAttemptResultDrainTimeout)
	require.ErrorIs(t, snapshotErr, errAttemptResultDrainTimeout)
	var gotReset *StreamResetError
	require.ErrorAs(t, snapshotErr, &gotReset)

	requestErr := context.Canceled
	_, resultErr := finishAttemptResponseDrain(attemptResponseDrain{
		result: attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}, resultSet: true,
		err: snapshotErr,
	}, requestErr, false)
	require.ErrorIs(t, resultErr, errAttemptResultDrainTimeout)
	require.ErrorIs(t, resultErr, requestErr)
	require.ErrorAs(t, resultErr, &gotReset)

	_, discardedErr := finishAttemptResponseDrain(attemptResponseDrain{
		result: attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}, resultSet: true,
		endReceived: true, discardedResponseBytes: 1, err: snapshotErr,
	}, requestErr, false)
	require.ErrorIs(t, discardedErr, requestErr)
	require.ErrorIs(t, discardedErr, errAttemptResultDrainTimeout)
	require.ErrorAs(t, discardedErr, &gotReset)
}

func TestFinishAttemptResponseDrainPreservesWriterErrorContainingCallerCancellation(t *testing.T) {
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
	writerErr := errors.New("writer failed after full write")
	originalErr := errors.Join(writerErr, context.Canceled)

	got, err := finishAttemptResponseDrain(attemptResponseDrain{
		result: want, resultSet: true, endReceived: true,
	}, originalErr, false)

	require.Equal(t, want, got)
	require.ErrorIs(t, err, writerErr)
	require.ErrorIs(t, err, context.Canceled)
}

func TestAttemptResponseDrainFullWriteErrorAfterSuccessfulFinalizeReturnsResultAndWriterError(t *testing.T) {
	stream, _, logs, _ := newRunningAttemptDrainTestStream(t)
	stream.inbound <- responseHeadersFrame(t, stream, 3)
	stream.inbound <- responseDataFrame(stream, 4, "one")
	writerErr := errors.New("writer reported failure after accepting all bytes")
	writer := &blockingFullErrorWriter{
		ResponseWriter: httptest.NewRecorder(), err: writerErr,
		started: make(chan struct{}), release: make(chan struct{}),
	}
	done := copyAttemptAsync(stream, t.Context(), writer)
	<-writer.started
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	stream.inbound <- wire.Frame{Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: payload}
	stream.inbound <- wire.Frame{Type: wire.FrameEnd, Sequence: 6, StreamID: stream.id}
	require.Eventually(t, func() bool {
		return stream.isTerminalSuccess() && stream.ctx.Err() != nil
	}, time.Second, time.Millisecond, "stream.run did not enter successful finalize before Write returned")
	close(writer.release)

	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.ErrorIs(t, got.err, writerErr)
	requireDrainLog(t, logs, "other", 0, true, true, string(attemptwire.ResultSucceeded))
}

func TestAttemptResponseDrainValidatesResponseWriterCount(t *testing.T) {
	fullCountErr := errors.New("writer reported an error after accepting the full chunk")
	tests := []struct {
		name       string
		n          int
		err        error
		wantErr    error
		wantReason string
	}{
		{name: "negative", n: -1, wantErr: errInvalidWriteCount, wantReason: "other"},
		{name: "over length", n: 4, wantErr: errInvalidWriteCount, wantReason: "other"},
		{name: "short without error", n: 1, wantErr: io.ErrShortWrite, wantReason: "other"},
		{name: "full count with error", n: 3, err: fullCountErr, wantErr: fullCountErr, wantReason: "other"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream, _, logs := newAttemptDrainTestStream(t, SessionDirectionRelay)
			require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
			require.False(t, stream.handleFrame(responseDataFrame(stream, 4, "abc")))
			writer := &countingErrorWriter{ResponseWriter: httptest.NewRecorder(), n: test.n, err: test.err, wrote: make(chan struct{})}
			done := copyAttemptAsync(stream, t.Context(), writer)
			<-writer.wrote
			want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
			finishAttemptStream(t, stream, 5, want)

			got := receiveAttemptCopy(t, done)
			require.Equal(t, want, got.result)
			if test.wantErr == nil {
				require.NoError(t, got.err)
			} else {
				require.ErrorIs(t, got.err, test.wantErr)
			}
			require.Zero(t, stream.session.incomingSize())
			wantDiscarded := int64(3 - max(test.n, 0))
			if test.n < 0 || test.n > 3 {
				wantDiscarded = 3
			}
			requireDrainLog(t, logs, test.wantReason, wantDiscarded, true, true, string(attemptwire.ResultSucceeded))
		})
	}
}

func TestAttemptResponseDrainDirectOutgoingUsesBoundedDirectLog(t *testing.T) {
	stream, clock, logs := newAttemptDrainTestStream(t, SessionDirectionDirectOutgoing)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	caller, cancel := context.WithCancel(t.Context())
	writer := newHeaderNotifyingRecorder()
	done := copyAttemptAsync(stream, caller, writer)
	<-writer.wroteHeader
	cancel()
	waitForDrainTimer(t, clock)
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
	finishAttemptStream(t, stream, 4, want)

	got := receiveAttemptCopy(t, done)
	require.Equal(t, want, got.result)
	require.NoError(t, got.err)
	requireDrainLog(t, logs, "none", 0, true, true, string(attemptwire.ResultSucceeded))
	fields := logs.All()[0].ContextMap()
	require.Equal(t, "direct", fields["route_path"])
	require.Equal(t, "direct_outgoing", fields["session_direction"])
}

func newAttemptDrainTestStream(t *testing.T, direction SessionDirection) (*Stream, *manualClock, *observer.ObservedLogs) {
	stream, clock, logs, _ := newAttemptDrainTestStreamWithFrames(t, direction)
	return stream, clock, logs
}

func newAttemptDrainTestStreamWithFrames(t *testing.T, direction SessionDirection) (*Stream, *manualClock, *observer.ObservedLogs, <-chan wire.Frame) {
	t.Helper()
	clock := newManualClock(time.Unix(1_700_000_000, 0))
	core, observed := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	opts := defaultSessionOptions(SessionOptions{Direction: direction, Logger: logger, clock: clock})
	if direction == SessionDirectionDirectOutgoing {
		opts.directLogs = newDirectLogs(logger, diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}))
		opts.directSourceAgentID = "source-a"
		opts.directTargetAgentID = "target-a"
	}
	limits := testLimits(2)
	limits.MaxDataBytes = 64
	limits.InitialStreamWindow = 64
	session := newSessionValue(nil, 1, limits, opts)
	session.ctx = ctx
	outgoing := make(chan wire.Frame, 16)
	session.writer = newFairWriter(ctx, 4096, time.Second, func(frame wire.Frame) error {
		outgoing <- frame
		return nil
	})
	go session.writer.Run()
	stream := newStream(session, ctx, testStreamID(102), 0, "request-a")
	stream.kind = streamKindAttempt
	stream.receivePhase = receiveCommitted
	stream.receiveSeq = 2
	stream.commitState.Store(uint32(wire.Committed))
	return stream, clock, observed, outgoing
}

func newRunningAttemptDrainTestStream(t *testing.T) (*Stream, *manualClock, *observer.ObservedLogs, <-chan wire.Frame) {
	t.Helper()
	stream, clock, logs, outgoing := newAttemptDrainTestStreamWithFrames(t, SessionDirectionRelay)
	stream.session.limits.MaxDataBytes = 3
	stream.session.limits.InitialStreamWindow = 3
	stream.responseData = newResponseBuffer(3)
	stream.responseWindow = newCreditWindowWithClock(3, clock)
	require.NoError(t, stream.session.admitStream(stream))
	go stream.run()
	return stream, clock, logs, outgoing
}

func responseDataFrame(stream *Stream, sequence uint32, body string) wire.Frame {
	return wire.Frame{Type: wire.FrameResponseData, Sequence: sequence, StreamID: stream.id, Payload: []byte(body)}
}

func finishAttemptStream(t *testing.T, stream *Stream, sequence uint32, result attemptwire.AttemptProxyResult) {
	t.Helper()
	payload, err := attemptwire.EncodeResultJSON(result)
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{Type: wire.FrameAttemptResult, Sequence: sequence, StreamID: stream.id, Payload: payload}))
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameEnd, Sequence: sequence + 1, StreamID: stream.id}))
	stream.responseData.Close()
}

func copyAttemptAsync(stream *Stream, ctx context.Context, writer http.ResponseWriter) <-chan attemptCopyResult {
	done := make(chan attemptCopyResult, 1)
	go func() {
		result, err := stream.CopyAttemptResponse(ctx, writer)
		done <- attemptCopyResult{result: result, err: err}
	}()
	return done
}

func receiveAttemptCopy(t *testing.T, done <-chan attemptCopyResult) attemptCopyResult {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(time.Second):
		t.Fatal("CopyAttemptResponse did not finish")
		return attemptCopyResult{}
	}
}

func requireNoAttemptCopy(t *testing.T, done <-chan attemptCopyResult) {
	t.Helper()
	select {
	case got := <-done:
		t.Fatalf("CopyAttemptResponse finished before drain deadline: result=%+v err=%v", got.result, got.err)
	default:
	}
}

func waitForDrainTimer(t *testing.T, clock *manualClock) {
	t.Helper()
	require.Eventually(t, func() bool { return clock.EventCount() > 0 }, time.Second, time.Millisecond)
}

func requireDrainLog(t *testing.T, logs *observer.ObservedLogs, reason string, discarded int64, resultReceived, endReceived bool, resultKind string) {
	t.Helper()
	require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, time.Millisecond)
	entry := logs.All()[0]
	require.Contains(t, entry.Message, "attempt result drain finished")
	fields := entry.ContextMap()
	require.Equal(t, reason, fields["reason_code"])
	require.EqualValues(t, discarded, fields["discarded_response_bytes"])
	require.Equal(t, resultReceived, fields["result_received"])
	require.Equal(t, endReceived, fields["end_received"])
	require.Equal(t, resultKind, fields["result_kind"])
	require.Equal(t, "request-a", fields["request_id"])
	require.NotContains(t, fields, "body")
	require.NotContains(t, fields, "result_json")
}

func readWindowUpdate(t *testing.T, outgoing <-chan wire.Frame) wire.WindowUpdate {
	t.Helper()
	select {
	case frame := <-outgoing:
		require.Equal(t, wire.FrameWindowUpdate, frame.Type)
		var update wire.WindowUpdate
		require.NoError(t, wire.DecodeMetadata(frame.Payload, &update, wire.MaxMetadataBytes))
		return update
	case <-time.After(time.Second):
		t.Fatal("WindowUpdate was not written")
		return wire.WindowUpdate{}
	}
}

type cancelingFullWriter struct {
	http.ResponseWriter
	cancel context.CancelFunc
}

func (w *cancelingFullWriter) Write(payload []byte) (int, error) {
	n, err := w.ResponseWriter.Write(payload)
	w.cancel()
	return n, err
}

type causeOnWriteWriter struct {
	http.ResponseWriter
	n      int
	err    error
	cancel func()
}

func (w *causeOnWriteWriter) Write([]byte) (int, error) {
	w.cancel()
	return w.n, w.err
}

type blockingFullErrorWriter struct {
	http.ResponseWriter
	err     error
	started chan struct{}
	release chan struct{}
}

func (w *blockingFullErrorWriter) Write(payload []byte) (int, error) {
	close(w.started)
	<-w.release
	n, _ := w.ResponseWriter.Write(payload)
	return n, w.err
}

type headerNotifyingRecorder struct {
	*httptest.ResponseRecorder
	wroteHeader chan struct{}
	once        sync.Once
}

func newHeaderNotifyingRecorder() *headerNotifyingRecorder {
	return &headerNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), wroteHeader: make(chan struct{})}
}

func (w *headerNotifyingRecorder) WriteHeader(statusCode int) {
	w.ResponseRecorder.WriteHeader(statusCode)
	w.once.Do(func() { close(w.wroteHeader) })
}

type bodyNotifyingRecorder struct {
	*httptest.ResponseRecorder
	wroteBody chan struct{}
	once      sync.Once
}

func newBodyNotifyingRecorder() *bodyNotifyingRecorder {
	return &bodyNotifyingRecorder{ResponseRecorder: httptest.NewRecorder(), wroteBody: make(chan struct{})}
}

func (w *bodyNotifyingRecorder) Write(payload []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(payload)
	w.once.Do(func() { close(w.wroteBody) })
	return n, err
}

type countingErrorWriter struct {
	http.ResponseWriter
	n     int
	err   error
	wrote chan struct{}
	once  sync.Once
}

func (w *countingErrorWriter) Write([]byte) (int, error) {
	w.once.Do(func() { close(w.wrote) })
	return w.n, w.err
}
