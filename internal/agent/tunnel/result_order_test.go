package tunnel

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAttemptResultOrderAcceptsHeadersDataResultEnd(t *testing.T) {
	stream := committedReceiveTestStream(t)
	stream.kind = streamKindAttempt
	result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
	resultPayload, err := attemptwire.EncodeResultJSON(result)
	require.NoError(t, err)

	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	require.False(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameResponseData, Sequence: 4, StreamID: stream.id, Payload: []byte("ok"),
	}))
	require.False(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: resultPayload,
	}))
	require.Equal(t, receiveResult, stream.receivePhase)
	require.False(t, stream.terminalSet, "Result is not terminal")
	require.True(t, stream.handleFrame(wire.Frame{Type: wire.FrameEnd, Sequence: 6, StreamID: stream.id}))
	require.Equal(t, result, stream.attemptResult)
	require.True(t, stream.resultSet)
}

func TestAttemptResultOrderRejectsMissingDuplicateEarlyAndLateData(t *testing.T) {
	resultPayload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)

	tests := []struct {
		name   string
		frames func(*Stream) []wire.Frame
	}{
		{name: "missing", frames: func(stream *Stream) []wire.Frame {
			return []wire.Frame{responseHeadersFrame(t, stream, 3), {Type: wire.FrameEnd, Sequence: 4, StreamID: stream.id}}
		}},
		{name: "duplicate", frames: func(stream *Stream) []wire.Frame {
			return []wire.Frame{responseHeadersFrame(t, stream, 3),
				{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: resultPayload},
				{Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: resultPayload}}
		}},
		{name: "before headers", frames: func(stream *Stream) []wire.Frame {
			return []wire.Frame{{Type: wire.FrameAttemptResult, Sequence: 3, StreamID: stream.id, Payload: resultPayload}}
		}},
		{name: "data after result", frames: func(stream *Stream) []wire.Frame {
			return []wire.Frame{responseHeadersFrame(t, stream, 3),
				{Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: resultPayload},
				{Type: wire.FrameResponseData, Sequence: 5, StreamID: stream.id, Payload: []byte("late")}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := committedReceiveTestStream(t)
			stream.kind = streamKindAttempt
			var terminal bool
			for _, frame := range test.frames(stream) {
				terminal = stream.handleFrame(frame)
				if terminal {
					break
				}
			}
			require.True(t, terminal)
			set, terminalErr := stream.getTerminal()
			require.True(t, set)
			require.ErrorIs(t, terminalErr, errStreamProtocol)
		})
	}
}

func TestProbeRejectsAttemptResult(t *testing.T) {
	stream := committedReceiveTestStream(t)
	stream.kind = streamKindProbe
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	payload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)
	require.True(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
	}))
}

func TestAttemptResultPayloadAndMissingEndRemainTerminalErrors(t *testing.T) {
	t.Run("malformed payload", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: []byte(`{"kind":`),
		}))
		stream.responseData.Close()
		result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.Zero(t, result)
		require.ErrorIs(t, err, errStreamProtocol)
	})

	t.Run("reset after valid result", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		want := attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true, Dispatches: 1,
		}
		payload, err := attemptwire.EncodeResultJSON(want)
		require.NoError(t, err)
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
		}))
		resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameReset, Sequence: 5, StreamID: stream.id, Payload: resetPayload,
		}))
		stream.responseData.Close()
		result, terminalErr := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.Equal(t, want, result)
		var resetErr *StreamResetError
		require.ErrorAs(t, terminalErr, &resetErr)
	})

	t.Run("connection loss before End", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		type copyResult struct {
			result attemptwire.AttemptProxyResult
			err    error
		}
		done := make(chan copyResult, 1)
		go func() {
			result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
			done <- copyResult{result: result, err: err}
		}()
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		want := attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ProviderDispatched: true, Dispatches: 2,
			PromptTokens: 17, CompletionTokens: 3, ErrorMessage: "provider failed",
		}
		payload, err := attemptwire.EncodeResultJSON(want)
		require.NoError(t, err)
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
		}))
		connectionErr := errors.New("connection lost before End")
		stream.setTerminal(connectionErr)
		stream.responseData.Close()
		got := <-done
		require.Equal(t, want, got.result)
		require.ErrorIs(t, got.err, connectionErr)
	})
}

func TestRemoteResultProtocolViolationTerminatesDirectSession(t *testing.T) {
	validPayload, err := attemptwire.EncodeResultJSON(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.NoError(t, err)

	for _, test := range []struct {
		name   string
		frames func(wire.StreamID) []wire.Frame
	}{
		{name: "malformed", frames: func(id wire.StreamID) []wire.Frame {
			return []wire.Frame{{Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 4, Payload: []byte(`{"kind":`)}}
		}},
		{name: "duplicate", frames: func(id wire.StreamID) []wire.Frame {
			return []wire.Frame{
				{Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 4, Payload: validPayload},
				{Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 5, Payload: validPayload},
			}
		}},
		{name: "out of order", frames: func(id wire.StreamID) []wire.Frame {
			return []wire.Frame{{Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: id, Sequence: 5, Payload: validPayload}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits(2)
			session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionDirectOutgoing})
			stream, open := committedTestStream(t, session, peer, limits, limits.InitialStreamWindow)
			writePeerFrame(t, peer, limits, wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameHeaders, StreamID: open.StreamID, Sequence: 3,
				Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, limits),
			})
			for _, frame := range test.frames(open.StreamID) {
				writePeerFrame(t, peer, limits, frame)
			}

			receiveWithDirectTimeout(t, session.Done())
			require.Equal(t, wire.Committed, stream.CommitState(), "session poisoning must not make a committed attempt replayable")
			_, err := session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
			require.Error(t, err, "a protocol-polluted session must reject new admission")
		})
	}
}

func TestCopyAttemptResponsePreservesResultWhenStreamIsCanceledAfterResult(t *testing.T) {
	stream := committedReceiveTestStream(t)
	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 2, PromptTokens: 17, ErrorMessage: "provider failed",
	}
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
	}))
	connectionErr := errors.New("session connection lost")
	stream.Cancel(connectionErr)
	stream.responseData.Close()

	result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
	require.Equal(t, want, result)
	require.ErrorIs(t, err, connectionErr)
	result, err = stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
	require.Zero(t, result)
	require.ErrorIs(t, err, errCopyStarted)
}

func TestCopyAttemptResponseAllowsOnlyOneLateConsumerAfterCancel(t *testing.T) {
	t.Run("without Result", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		connectionErr := errors.New("session connection lost before Result")
		stream.Cancel(connectionErr)

		result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.Zero(t, result)
		require.ErrorIs(t, err, connectionErr)
		result, err = stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.Zero(t, result)
		require.ErrorIs(t, err, errCopyStarted)
	})

	t.Run("clean End", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		payload, err := attemptwire.EncodeResultJSON(want)
		require.NoError(t, err)
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
		}))
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameEnd, Sequence: 5, StreamID: stream.id,
		}))
		stream.responseData.Close()

		result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.NoError(t, err)
		require.Equal(t, want, result)
		result, err = stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		require.Zero(t, result)
		require.ErrorIs(t, err, errCopyStarted)
	})
}

func TestConcurrentCopyAttemptResponseHasSingleConsumer(t *testing.T) {
	stream := committedReceiveTestStream(t)
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	firstStarted := &headerSignalWriter{ResponseWriter: httptest.NewRecorder(), wroteHeader: make(chan struct{})}
	firstDone := make(chan attemptCopyResult, 1)
	go func() {
		result, err := stream.CopyAttemptResponse(t.Context(), firstStarted)
		firstDone <- attemptCopyResult{result: result, err: err}
	}()
	select {
	case <-firstStarted.wroteHeader:
	case <-time.After(time.Second):
		t.Fatal("first CopyAttemptResponse did not claim the response")
	}

	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
	}))
	resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
	require.True(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameReset, Sequence: 5, StreamID: stream.id, Payload: resetPayload,
	}))

	secondResult, secondErr := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
	require.Zero(t, secondResult)
	require.ErrorIs(t, secondErr, errCopyStarted)
	stream.responseData.Close()
	first := <-firstDone
	require.Equal(t, want, first.result)
	var resetErr *StreamResetError
	require.ErrorAs(t, first.err, &resetErr)
}

func TestCopyAttemptResponseReturnsFirstTerminalError(t *testing.T) {
	t.Run("Reset before caller cancellation", func(t *testing.T) {
		stream, want := streamWithHeadersAndResult(t)
		copyCtx, cancel := context.WithCancelCause(t.Context())
		writer := &headerSignalWriter{ResponseWriter: httptest.NewRecorder(), wroteHeader: make(chan struct{})}
		done := make(chan attemptCopyResult, 1)
		go func() {
			result, err := stream.CopyAttemptResponse(copyCtx, writer)
			done <- attemptCopyResult{result: result, err: err}
		}()
		<-writer.wroteHeader
		resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameReset, Sequence: 5, StreamID: stream.id, Payload: resetPayload,
		}))
		stream.responseData.Close()
		cancel(context.Canceled)

		got := <-done
		require.Equal(t, want, got.result)
		var resetErr *StreamResetError
		require.ErrorAs(t, got.err, &resetErr)
	})

	t.Run("Reset before writer error", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameResponseData, Sequence: 4, StreamID: stream.id, Payload: []byte("x"),
		}))
		writerErr := errors.New("client writer failed")
		writer := &blockingErrorWriter{
			ResponseWriter: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), err: writerErr,
		}
		done := make(chan attemptCopyResult, 1)
		go func() {
			result, err := stream.CopyAttemptResponse(t.Context(), writer)
			done <- attemptCopyResult{result: result, err: err}
		}()
		<-writer.started
		want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
		payload, err := attemptwire.EncodeResultJSON(want)
		require.NoError(t, err)
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: payload,
		}))
		resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameReset, Sequence: 6, StreamID: stream.id, Payload: resetPayload,
		}))
		stream.responseData.Close()
		close(writer.release)

		got := <-done
		require.Equal(t, want, got.result)
		var resetErr *StreamResetError
		require.ErrorAs(t, got.err, &resetErr)
		require.ErrorIs(t, got.err, writerErr)
	})

	t.Run("caller cancellation before Reset", func(t *testing.T) {
		stream, want := streamWithHeadersAndResult(t)
		copyCtx, cancel := context.WithCancelCause(t.Context())
		writer := &headerSignalWriter{ResponseWriter: httptest.NewRecorder(), wroteHeader: make(chan struct{})}
		done := make(chan attemptCopyResult, 1)
		go func() {
			result, err := stream.CopyAttemptResponse(copyCtx, writer)
			done <- attemptCopyResult{result: result, err: err}
		}()
		<-writer.wroteHeader
		cancel(context.Canceled)
		resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameReset, Sequence: 5, StreamID: stream.id, Payload: resetPayload,
		}))
		stream.responseData.Close()
		got := <-done

		require.Equal(t, want, got.result)
		var resetErr *StreamResetError
		require.ErrorAs(t, got.err, &resetErr)
	})

	t.Run("writer error before Reset", func(t *testing.T) {
		stream := committedReceiveTestStream(t)
		require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameResponseData, Sequence: 4, StreamID: stream.id, Payload: []byte("x"),
		}))
		want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
		payload, err := attemptwire.EncodeResultJSON(want)
		require.NoError(t, err)
		require.False(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameAttemptResult, Sequence: 5, StreamID: stream.id, Payload: payload,
		}))
		writerErr := errors.New("client writer failed first")
		writer := &blockingErrorWriter{
			ResponseWriter: httptest.NewRecorder(), started: make(chan struct{}), release: make(chan struct{}), err: writerErr,
		}
		close(writer.release)
		done := copyAttemptAsync(stream, t.Context(), writer)
		resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
		require.True(t, stream.handleFrame(wire.Frame{
			Type: wire.FrameReset, Sequence: 6, StreamID: stream.id, Payload: resetPayload,
		}))
		stream.responseData.Close()
		got := <-done

		require.Equal(t, want, got.result)
		var resetErr *StreamResetError
		require.ErrorAs(t, got.err, &resetErr)
	})
}

func TestCopyAttemptResponsePreCanceledCallerPreservesBufferedResultAndTerminalError(t *testing.T) {
	stream, want := streamWithHeadersAndResult(t)
	resetPayload := mustMetadata(t, wire.Reset{Code: wire.ErrorCodeSessionClosed, Stage: "peer"}, stream.session.limits)
	require.True(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameReset, Sequence: 5, StreamID: stream.id, Payload: resetPayload,
	}))
	copyCtx, cancel := context.WithCancel(t.Context())
	cancel()
	writer := &headerSignalWriter{ResponseWriter: httptest.NewRecorder(), wroteHeader: make(chan struct{})}

	got, err := stream.CopyAttemptResponse(copyCtx, writer)
	require.Equal(t, want, got)
	var resetErr *StreamResetError
	require.ErrorAs(t, err, &resetErr)
	select {
	case <-writer.wroteHeader:
		t.Fatal("pre-canceled caller received buffered response headers")
	default:
	}
}

func streamWithHeadersAndResult(t *testing.T) (*Stream, attemptwire.AttemptProxyResult) {
	t.Helper()
	stream := committedReceiveTestStream(t)
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}
	require.False(t, stream.handleFrame(responseHeadersFrame(t, stream, 3)))
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	require.False(t, stream.handleFrame(wire.Frame{
		Type: wire.FrameAttemptResult, Sequence: 4, StreamID: stream.id, Payload: payload,
	}))
	return stream, want
}

func TestSessionDisconnectAfterAttemptResultPreservesResultAndCause(t *testing.T) {
	limits := testLimits(1)
	session, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay})
	stream, err := session.OpenAttemptStream(t.Context(), validBoundRelayRequest("/v1/responses"))
	require.NoError(t, err)
	open := readPeerFrame(t, peer, limits)
	require.Equal(t, wire.FrameOpen, open.Type)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: open.StreamID,
		Payload: mustMetadata(t, wire.Ready{RequestWindow: limits.InitialStreamWindow}, limits),
	})
	commitDone := make(chan error, 1)
	go func() { commitDone <- stream.Commit(t.Context()) }()
	require.Equal(t, wire.FrameCommit, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommitted, StreamID: open.StreamID,
	})
	require.NoError(t, <-commitDone)
	require.NoError(t, stream.Upload(t.Context(), strings.NewReader("")))
	require.Equal(t, wire.FrameRequestEnd, readPeerFrame(t, peer, limits).Type)

	done := make(chan attemptCopyResult, 1)
	go func() {
		result, err := stream.CopyAttemptResponse(t.Context(), httptest.NewRecorder())
		done <- attemptCopyResult{result: result, err: err}
	}()
	headers := responseHeadersFrame(t, stream, 3)
	headers.Version = wire.ProtocolVersion
	writePeerFrame(t, peer, limits, headers)
	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 1, PromptTokens: 13, ResponseStarted: true,
	}
	payload, err := attemptwire.EncodeResultJSON(want)
	require.NoError(t, err)
	writePeerFrame(t, peer, limits, wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameAttemptResult, StreamID: open.StreamID, Payload: payload,
	})
	require.Eventually(t, func() bool {
		_, resultSet, _, _ := stream.getAttemptResultAndTerminal()
		return resultSet
	}, time.Second, time.Millisecond, "attempt Result was not decoded before disconnect")
	require.NoError(t, peer.Close())

	got := <-done
	require.Equal(t, want, got.result)
	require.Error(t, got.err)
}

type attemptCopyResult struct {
	result attemptwire.AttemptProxyResult
	err    error
}

type headerSignalWriter struct {
	http.ResponseWriter
	wroteHeader chan struct{}
}

func (w *headerSignalWriter) WriteHeader(statusCode int) {
	w.ResponseWriter.WriteHeader(statusCode)
	close(w.wroteHeader)
}

type blockingErrorWriter struct {
	http.ResponseWriter
	started chan struct{}
	release chan struct{}
	err     error
}

func (w *blockingErrorWriter) Write([]byte) (int, error) {
	close(w.started)
	<-w.release
	return 0, w.err
}

func responseHeadersFrame(t *testing.T, stream *Stream, sequence uint32) wire.Frame {
	t.Helper()
	return wire.Frame{
		Type: wire.FrameHeaders, Sequence: sequence, StreamID: stream.id,
		Payload: mustMetadata(t, wire.Headers{StatusCode: http.StatusOK}, stream.session.limits),
	}
}
