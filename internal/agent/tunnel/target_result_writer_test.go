package tunnel

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestTargetExplicitResultFrameOrder(t *testing.T) {
	for _, test := range []struct {
		name string
		body []byte
		want []wire.Type
	}{
		{name: "body", body: []byte("x"), want: []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameAttemptResult, wire.FrameEnd}},
		{name: "zero body", want: []wire.Type{wire.FrameHeaders, wire.FrameAttemptResult, wire.FrameEnd}},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := runTargetAttempt(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
				w.WriteHeader(http.StatusOK)
				if len(test.body) > 0 {
					_, err := w.Write(test.body)
					require.NoError(t, err)
				}
				resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
				require.True(t, ok)
				require.NoError(t, resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
				}))
			})
			require.Equal(t, test.want, targetResponseFrameTypes(frames))
		})
	}
}

func TestTargetAttemptResultBeforeHeadersFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name        string
		afterResult func(*testing.T, http.ResponseWriter)
	}{
		{name: "explicit WriteHeader", afterResult: func(_ *testing.T, w http.ResponseWriter) {
			w.WriteHeader(http.StatusOK)
		}},
		{name: "empty Write", afterResult: func(t *testing.T, w http.ResponseWriter) {
			n, _ := w.Write(nil)
			require.Zero(t, n)
		}},
		{name: "nonempty Write", afterResult: func(t *testing.T, w http.ResponseWriter) {
			n, _ := w.Write([]byte("provider response"))
			require.Zero(t, n)
		}},
		{name: "Flush", afterResult: func(t *testing.T, w http.ResponseWriter) {
			flusher, ok := w.(http.Flusher)
			require.True(t, ok)
			flusher.Flush()
		}},
		{name: "implicit header"},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := runTargetAttempt(t, func(w http.ResponseWriter, r *http.Request) {
				resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
				require.True(t, ok)
				result := attemptwire.AttemptProxyResult{
					Kind: attemptwire.ResultExecutionRejected, ProviderResultKnown: true,
				}
				err := resultWriter.WriteAttemptResult(result)
				require.EqualError(t, err, "agent tunnel: attempt result written before response headers")
				require.ErrorIs(t, resultWriter.WriteAttemptResult(result), errAttemptResultAlreadyWritten)
				if test.afterResult != nil {
					test.afterResult(t, w)
				}
			})

			requireAttemptResultResetWithoutResultOrEnd(t, frames)
		})
	}
}

func TestTargetAttemptResultVsFirstWriteHeaderIsLinearized(t *testing.T) {
	frames := runTargetAttempt(t, func(w http.ResponseWriter, r *http.Request) {
		resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
		require.True(t, ok)
		result := attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
		}

		start := make(chan struct{})
		var group sync.WaitGroup
		var resultErr error
		group.Add(2)
		go func() {
			defer group.Done()
			<-start
			w.WriteHeader(http.StatusOK)
		}()
		go func() {
			defer group.Done()
			<-start
			resultErr = resultWriter.WriteAttemptResult(result)
		}()
		close(start)
		group.Wait()

		if resultErr != nil {
			require.EqualError(t, resultErr, "agent tunnel: attempt result written before response headers")
		}
	})

	types := targetResponseFrameTypes(frames)
	if types[len(types)-1] == wire.FrameEnd {
		require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameAttemptResult, wire.FrameEnd}, types)
		return
	}
	requireAttemptResultResetWithoutResultOrEnd(t, frames)
}

func TestTargetAttemptResultWriterFailuresResetAtAttemptResultWithoutEnd(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*testing.T, http.ResponseWriter, *http.Request)
	}{
		{name: "missing", run: func(_ *testing.T, w http.ResponseWriter, _ *http.Request) {
			w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
			w.WriteHeader(http.StatusOK)
		}},
		{name: "duplicate", run: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
			w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
			w.WriteHeader(http.StatusOK)
			resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
			require.True(t, ok)
			result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultExecutionRejected, ProviderResultKnown: true}
			require.NoError(t, resultWriter.WriteAttemptResult(result))
			require.ErrorIs(t, resultWriter.WriteAttemptResult(result), errAttemptResultAlreadyWritten)
		}},
		{name: "encode oversized", run: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
			w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
			w.WriteHeader(http.StatusOK)
			resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
			require.True(t, ok)
			require.ErrorIs(t, resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
				Kind:         attemptwire.ResultExecutionRejected,
				ErrorMessage: strings.Repeat("x", attemptwire.MaxResultWireBytes+1),
			}), attemptwire.ErrResultTooLarge)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			frames := runTargetAttempt(t, func(w http.ResponseWriter, r *http.Request) { test.run(t, w, r) })
			types := targetResponseFrameTypes(frames)
			require.NotContains(t, types, wire.FrameEnd)
			require.Equal(t, wire.FrameReset, frames[len(frames)-1].Type)
			var reset wire.Reset
			require.NoError(t, wire.DecodeMetadata(frames[len(frames)-1].Payload, &reset, wire.MaxMetadataBytes))
			require.Equal(t, "attempt_result", reset.Stage)
		})
	}
}

func requireAttemptResultResetWithoutResultOrEnd(t *testing.T, frames []wire.Frame) {
	t.Helper()
	types := targetResponseFrameTypes(frames)
	require.NotContains(t, types, wire.FrameAttemptResult)
	require.NotContains(t, types, wire.FrameEnd)
	require.Equal(t, wire.FrameReset, frames[len(frames)-1].Type)
	var reset wire.Reset
	require.NoError(t, wire.DecodeMetadata(frames[len(frames)-1].Payload, &reset, wire.MaxMetadataBytes))
	require.Equal(t, "attempt_result", reset.Stage)
}

func runTargetAttempt(t *testing.T, serve func(http.ResponseWriter, *http.Request)) []wire.Frame {
	t.Helper()
	limits := testLimits(1)
	handler := NewTargetHandler(TargetHandlerOptions{TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		serve(w, r)
	})})

	_, peer := startTestSession(t, limits, SessionOptions{Direction: SessionDirectionRelay, TargetHandler: handler})
	id := testStreamID(202)
	open := validTargetBoundOpen(limits, "/v1/responses")
	open.BodyLength = 0
	writeTargetOpen(t, peer, limits, id, open)
	require.Equal(t, wire.FrameReady, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id})
	require.Equal(t, wire.FrameCommitted, readPeerFrame(t, peer, limits).Type)
	writePeerFrame(t, peer, limits, wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id})

	frames := make([]wire.Frame, 0, 4)
	for {
		frame := readPeerFrame(t, peer, limits)
		frames = append(frames, frame)
		if frame.Type == wire.FrameEnd || frame.Type == wire.FrameReset {
			return frames
		}
	}
}

func targetResponseFrameTypes(frames []wire.Frame) []wire.Type {
	types := make([]wire.Type, 0, len(frames))
	for _, frame := range frames {
		if frame.Type != wire.FrameWindowUpdate {
			types = append(types, frame.Type)
		}
	}
	return types
}

func TestTargetStreamAttemptResultWriterRequiresHeadersAndUsesRawJSON(t *testing.T) {
	stream, writer := newAttemptResultTargetStream(t, 1<<20)
	resultWriter, ok := any(stream).(attemptwire.AttemptResultWriter)
	require.True(t, ok, "targetStream must implement AttemptResultWriter")
	require.NoError(t, enqueueAttemptResultHeaders(t, stream))

	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true,
		PromptTokens: 0, CompletionTokens: 0,
	}
	require.NoError(t, resultWriter.WriteAttemptResult(want))
	frames := acceptedFrames(writer, stream.id)
	require.Len(t, frames, 2)
	require.Equal(t, wire.FrameHeaders, frames[0].Type)
	require.Equal(t, wire.FrameAttemptResult, frames[1].Type)
	require.True(t, strings.HasPrefix(string(frames[1].Payload), "{"), "Result frame must contain raw JSON")
	got, err := attemptwire.DecodeResultJSON(frames[1].Payload)
	require.NoError(t, err)
	require.Equal(t, want, got)

	duplicateErr := resultWriter.WriteAttemptResult(want)
	require.Error(t, duplicateErr)
	require.Same(t, duplicateErr, resultWriter.WriteAttemptResult(want), "duplicate error must be stable")
	require.Len(t, acceptedFrames(writer, stream.id), 2, "duplicate writes must not enqueue another Result")
}

func TestTargetStreamAttemptResultWriterRecordsEncodeAndEnqueueFailures(t *testing.T) {
	t.Run("encode oversized", func(t *testing.T) {
		stream, writer := newAttemptResultTargetStream(t, 1<<20)
		resultWriter, ok := any(stream).(attemptwire.AttemptResultWriter)
		require.True(t, ok)
		require.NoError(t, enqueueAttemptResultHeaders(t, stream))
		err := resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultProxyRejected, ErrorMessage: strings.Repeat("x", attemptwire.MaxResultWireBytes+1),
		})
		require.ErrorIs(t, err, attemptwire.ErrResultTooLarge)
		require.Equal(t, []wire.Type{wire.FrameHeaders}, acceptedFrameTypes(writer, stream.id))
	})

	t.Run("enqueue failure", func(t *testing.T) {
		headersPayload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, wire.MaxMetadataBytes)
		require.NoError(t, err)
		stream, writer := newAttemptResultTargetStream(t, int64(wire.HeaderSize+len(headersPayload)))
		resultWriter, ok := any(stream).(attemptwire.AttemptResultWriter)
		require.True(t, ok)
		require.NoError(t, stream.sendResponseFrame(t.Context(), wire.FrameHeaders, headersPayload))
		err = resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultProxyRejected, ErrorMessage: strings.Repeat("x", 256),
		})
		require.ErrorIs(t, err, errQueueFrameTooLarge)
		require.Equal(t, []wire.Type{wire.FrameHeaders}, acceptedFrameTypes(writer, stream.id))
	})
}

func TestTargetStreamAttemptResultWriterUsesNegotiatedMetadataLimit(t *testing.T) {
	const limit = 4 * 1024

	t.Run("exact", func(t *testing.T) {
		stream, writer := newAttemptResultTargetStream(t, 1<<20)
		stream.session.limits.MaxMetadataBytes = limit
		require.NoError(t, enqueueAttemptResultHeaders(t, stream))
		want := tunnelResultWithJSONSize(t, limit)
		require.NoError(t, stream.WriteAttemptResult(want))
		frames := acceptedFrames(writer, stream.id)
		require.Equal(t, wire.FrameAttemptResult, frames[len(frames)-1].Type)
		require.Len(t, frames[len(frames)-1].Payload, limit)
	})

	t.Run("plus one", func(t *testing.T) {
		stream, _ := newAttemptResultTargetStream(t, 1<<20)
		stream.session.limits.MaxMetadataBytes = limit
		require.NoError(t, enqueueAttemptResultHeaders(t, stream))
		err := stream.WriteAttemptResult(tunnelResultWithJSONSize(t, limit+1))
		require.ErrorIs(t, err, attemptwire.ErrResultTooLarge)
	})
}

func tunnelResultWithJSONSize(t *testing.T, size int) attemptwire.AttemptProxyResult {
	t.Helper()
	result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProxyRejected, ErrorMessage: "x"}
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	fixedBytes := len(raw) - len(result.ErrorMessage)
	require.Greater(t, size, fixedBytes)
	result.ErrorMessage = strings.Repeat("x", size-fixedBytes)
	raw, err = json.Marshal(result)
	require.NoError(t, err)
	require.Len(t, raw, size)
	return result
}

func TestTargetAttemptResultRejectsAfterHeaderEnqueueFailure(t *testing.T) {
	result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
	resultPayload, err := attemptwire.EncodeResultJSON(result)
	require.NoError(t, err)
	stream, writer := newAttemptResultTargetStream(t, int64(wire.HeaderSize+len(resultPayload)))
	response := newTunnelResponseWriter(
		t.Context(), wire.MaxMetadataBytes, wire.MaxV2PayloadBytes, stream.sendResponseFrame,
	)
	response.Header().Set("X-Large", strings.Repeat("x", len(resultPayload)+256))
	response.WriteHeader(http.StatusOK)
	require.ErrorIs(t, response.resetError(), errQueueFrameTooLarge)

	err = stream.WriteAttemptResult(result)
	require.EqualError(t, err, "agent tunnel: attempt result written before response headers")
	require.Empty(t, acceptedFrames(writer, stream.id))
}

func enqueueAttemptResultHeaders(t *testing.T, stream *targetStream) error {
	t.Helper()
	payload, err := wire.EncodeMetadata(wire.Headers{StatusCode: http.StatusOK}, wire.MaxMetadataBytes)
	if err != nil {
		return err
	}
	return stream.sendResponseFrame(t.Context(), wire.FrameHeaders, payload)
}

func newAttemptResultTargetStream(t *testing.T, queueBytes int64) (*targetStream, *fairWriter) {
	t.Helper()
	ctx := t.Context()
	writer := newFairWriter(ctx, queueBytes, time.Second, func(wire.Frame) error { return nil })
	session := &Session{ctx: ctx, limits: testLimits(1), writer: writer}
	stream := &targetStream{session: session, id: testStreamID(201), ctx: ctx}
	return stream, writer
}

func acceptedFrames(writer *fairWriter, id wire.StreamID) []wire.Frame {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	frames := make([]wire.Frame, 0, len(writer.queues[id]))
	for _, queued := range writer.queues[id] {
		frames = append(frames, queued.frame)
	}
	return frames
}

func acceptedFrameTypes(writer *fairWriter, id wire.StreamID) []wire.Type {
	frames := acceptedFrames(writer, id)
	types := make([]wire.Type, 0, len(frames))
	for _, frame := range frames {
		types = append(types, frame.Type)
	}
	return types
}
