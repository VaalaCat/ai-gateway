package tunnel

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type capturedTargetFrame struct {
	type_   wire.Type
	payload []byte
}

func TestTunnelResponseWriterPreservesOrderedBinaryPayloads(t *testing.T) {
	contentTypes := []string{
		"application/json", "text/event-stream", "multipart/form-data; boundary=x",
		"image/png", "audio/mpeg",
	}
	bytes := []byte{0, 1, 2, 0xff, '\n', 0}
	for _, contentType := range contentTypes {
		t.Run(contentType, func(t *testing.T) {
			frames := make([]capturedTargetFrame, 0, 4)
			writer := newTunnelResponseWriter(t.Context(), 4096, 3, func(_ context.Context, typ wire.Type, payload []byte) error {
				frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
				return nil
			})
			writer.Header().Set("Content-Type", contentType)
			n, err := writer.Write(bytes)
			require.NoError(t, err)
			require.Equal(t, len(bytes), n)
			require.NoError(t, writer.finish())

			require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameResponseData, wire.FrameEnd}, frameTypes(frames))
			var headers wire.Headers
			require.NoError(t, wire.DecodeMetadata(frames[0].payload, &headers, 4096))
			require.Equal(t, http.StatusOK, headers.StatusCode)
			require.Equal(t, contentType, http.Header(headers.Header).Get("Content-Type"))
			require.Equal(t, bytes, append(frames[1].payload, frames[2].payload...))
		})
	}
}

func TestTunnelResponseWriterFlushAndTrailers(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header().Set("Trailer", "X-Usage")
	writer.Header().Set("X-Usage", "tokens=3")
	writer.Header().Set("Connection", "keep-alive")
	writer.Flush()
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameEnd}, frameTypes(frames))
	var headers wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &headers, 4096))
	require.Empty(t, http.Header(headers.Header).Get("Connection"))
	require.Equal(t, "tokens=3", http.Header(headers.Trailer).Get("X-Usage"))
}

func TestTunnelResponseWriterIgnoresInformationalStatusUntilFinalResponse(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header().Set("X-Phase", "early")
	writer.WriteHeader(http.StatusEarlyHints)
	writer.Header().Set("X-Phase", "final")
	writer.WriteHeader(http.StatusCreated)
	_, err := writer.Write([]byte("body"))
	require.NoError(t, err)
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameEnd}, frameTypes(frames))
	var headers wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &headers, 4096))
	require.Equal(t, http.StatusCreated, headers.StatusCode)
	require.Equal(t, "final", http.Header(headers.Header).Get("X-Phase"))
}

func TestTunnelResponseWriterRejectsSwitchingProtocolsWithoutHeaders(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 1)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.WriteHeader(http.StatusSwitchingProtocols)
	require.Error(t, writer.finish())
	require.Error(t, writer.resetError())
	require.Empty(t, frames)
	require.False(t, writer.wroteHeader)
}

func TestTunnelResponseWriterValidatesCanonicalizesAndStripsResponseHeaders(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 2)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header()["connection"] = []string{"x-hop"}
	writer.Header()["x-hop"] = []string{"remove"}
	writer.Header()["content-length"] = []string{"99"}
	writer.Header()["X-Business"] = []string{"canonical"}
	writer.Header()["X-bUsInEsS"] = []string{"mixed"}
	writer.Header()["x-business"] = []string{"lowercase"}
	writer.Header().Set(consts.HeaderXAgentForwardTicket, "must-not-escape")
	writer.Header().Set(consts.HeaderXAgentRouteID, "999")
	writer.WriteHeader(http.StatusOK)
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameEnd}, frameTypes(frames))
	var headers wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &headers, 4096))
	got := http.Header(headers.Header)
	require.Equal(t, []string{"canonical", "mixed", "lowercase"}, got.Values("X-Business"))
	for _, key := range []string{"Connection", "X-Hop", "Content-Length", consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID} {
		require.Empty(t, got.Values(key), key)
	}
	for key := range got {
		require.Equal(t, http.CanonicalHeaderKey(key), key)
	}
}

func TestTunnelResponseWriterStripsDeclaredAndDynamicReservedTrailers(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header().Set("Trailer", "X-Usage, "+consts.HeaderXAgentForwardTicket)
	_, err := writer.Write([]byte("event"))
	require.NoError(t, err)
	writer.Header().Set("X-Usage", "tokens=7")
	writer.Header().Set(consts.HeaderXAgentForwardTicket, "declared-secret")
	writer.Header().Set(http.TrailerPrefix+consts.HeaderXAgentRouteID, "dynamic-secret")
	require.NoError(t, writer.finish())

	var initial wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &initial, 4096))
	require.Contains(t, http.Header(initial.Trailer), "X-Usage")
	require.NotContains(t, http.Header(initial.Trailer), consts.HeaderXAgentForwardTicket)
	var final wire.Trailers
	require.NoError(t, wire.DecodeMetadata(frames[len(frames)-1].payload, &final, 4096))
	require.Equal(t, "tokens=7", http.Header(final.Header).Get("X-Usage"))
	require.Empty(t, http.Header(final.Header).Get(consts.HeaderXAgentForwardTicket))
	require.Empty(t, http.Header(final.Header).Get(consts.HeaderXAgentRouteID))
	require.NotContains(t, final.Dynamic, consts.HeaderXAgentRouteID)
}

func TestTunnelResponseWriterRejectsInvalidOrdinaryHeaderBeforeFrames(t *testing.T) {
	tests := []http.Header{
		{"Bad Header": {"value"}},
		{"X-Test": {"bad\r\nvalue"}},
	}
	for _, header := range tests {
		writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(context.Context, wire.Type, []byte) error {
			t.Fatal("invalid response header emitted a frame")
			return nil
		})
		for key, values := range header {
			writer.Header()[key] = values
		}
		_, err := writer.Write([]byte("body"))
		require.Error(t, err)
		require.Error(t, writer.resetError())
		require.False(t, writer.wroteHeader)
	}
}

func TestResponseHeadersPreservesDeclaredAndPrefixedTrailerValues(t *testing.T) {
	header, trailer, keys, err := responseHeaders(http.Header{
		"Trailer":                      {"X-Usage"},
		"X-Usage":                      {"declared"},
		http.TrailerPrefix + "X-Usage": {"prefixed"},
	})
	require.NoError(t, err)
	require.Empty(t, header.Values("X-Usage"))
	require.Equal(t, []string{"declared", "prefixed"}, trailer.Values("X-Usage"))
	require.Equal(t, []string{"X-Usage"}, keys)
}

func TestTunnelResponseWriterSendsFinalTrailerValuesAtEnd(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header().Set("Trailer", "X-Usage")
	_, err := writer.Write([]byte("event"))
	require.NoError(t, err)
	writer.Header().Set("X-Usage", "tokens=7")
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameEnd}, frameTypes(frames))
	var initial wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &initial, 4096))
	require.Contains(t, http.Header(initial.Trailer), "X-Usage")
	var final wire.Trailers
	require.NoError(t, wire.DecodeMetadata(frames[2].payload, &final, 4096))
	require.Equal(t, "tokens=7", http.Header(final.Header).Get("X-Usage"))
}

func TestTunnelResponseWriterSendsLateTrailerPrefixAtEnd(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	_, err := writer.Write([]byte("event"))
	require.NoError(t, err)
	writer.Header()[http.TrailerPrefix+"X-Usage"] = []string{"tokens=7"}
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameEnd}, frameTypes(frames))
	var initial wire.Headers
	require.NoError(t, wire.DecodeMetadata(frames[0].payload, &initial, 4096))
	require.NotContains(t, http.Header(initial.Trailer), "X-Usage")
	var final wire.Trailers
	require.NoError(t, wire.DecodeMetadata(frames[2].payload, &final, 4096))
	require.Equal(t, "tokens=7", http.Header(final.Header).Get("X-Usage"))
	require.Equal(t, []string{"X-Usage"}, final.Dynamic)
}

func TestTunnelResponseWriterRejectsInvalidLateTrailerPrefixWithoutEnd(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value string
	}{
		{name: "invalid name", key: "Bad Key", value: "tokens=7"},
		{name: "invalid value", key: "X-Usage", value: "bad\r\nvalue"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			frames := make([]capturedTargetFrame, 0, 2)
			writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
				frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
				return nil
			})
			_, err := writer.Write([]byte("event"))
			require.NoError(t, err)
			writer.Header()[http.TrailerPrefix+tt.key] = []string{tt.value}
			require.ErrorIs(t, writer.finish(), errStreamProtocol)
			require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData}, frameTypes(frames))
		})
	}
}

func TestTunnelResponseWriterDoesNotPromoteLateOrdinaryHeaderToTrailer(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 3)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	_, err := writer.Write([]byte("event"))
	require.NoError(t, err)
	writer.Header().Set("X-Usage", "tokens=7")
	require.NoError(t, writer.finish())

	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData, wire.FrameEnd}, frameTypes(frames))
	require.Empty(t, frames[2].payload)
}

func TestTunnelResponseWriterRejectsForbiddenTrailerBeforeFrames(t *testing.T) {
	frames := 0
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(context.Context, wire.Type, []byte) error {
		frames++
		return nil
	})
	writer.Header().Set("Trailer", "Content-Length")
	writer.Header().Set("Content-Length", "12")
	_, err := writer.Write([]byte("body"))
	require.ErrorIs(t, err, errStreamProtocol)
	require.Zero(t, frames)
	require.False(t, writer.wroteHeader)
}

func TestTunnelResponseWriterRejectsLateInvalidTrailerValueWithoutEnd(t *testing.T) {
	frames := make([]capturedTargetFrame, 0, 2)
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(_ context.Context, typ wire.Type, payload []byte) error {
		frames = append(frames, capturedTargetFrame{type_: typ, payload: append([]byte(nil), payload...)})
		return nil
	})
	writer.Header().Set("Trailer", "X-Usage")
	_, err := writer.Write([]byte("event"))
	require.NoError(t, err)
	writer.Header().Set("X-Usage", "bad\r\nvalue")
	require.ErrorIs(t, writer.finish(), errStreamProtocol)
	require.Equal(t, []wire.Type{wire.FrameHeaders, wire.FrameResponseData}, frameTypes(frames))
}

func TestTunnelResponseWriterStopsOnCancellationAndSenderError(t *testing.T) {
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(errors.New("source cancelled"))
	writer := newTunnelResponseWriter(ctx, 4096, 16, func(context.Context, wire.Type, []byte) error {
		t.Fatal("sender called after cancellation")
		return nil
	})
	_, err := writer.Write([]byte("x"))
	require.ErrorContains(t, err, "source cancelled")

	want := errors.New("queue rejected")
	writer = newTunnelResponseWriter(t.Context(), 4096, 16, func(context.Context, wire.Type, []byte) error { return want })
	_, err = writer.Write([]byte("x"))
	require.ErrorIs(t, err, want)
	require.ErrorIs(t, writer.finish(), want)
}

func TestTunnelResponseWriterBlockedSendDoesNotHoldStateLock(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	blocked := false
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(context.Context, wire.Type, []byte) error {
		if !blocked {
			blocked = true
			close(entered)
			<-release
		}
		return nil
	})

	writeDone := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte("x"))
		writeDone <- err
	}()
	<-entered

	stateDone := make(chan error, 1)
	go func() { stateDone <- writer.resetError() }()
	select {
	case err := <-stateDone:
		require.NoError(t, err)
	case <-time.After(100 * time.Millisecond):
		close(release)
		require.NoError(t, <-writeDone)
		<-stateDone
		t.Fatal("blocked frame send held the response writer state lock")
	}

	close(release)
	require.NoError(t, <-writeDone)
}

func TestTunnelResponseWriterDoesNotImplementHijacker(t *testing.T) {
	writer := newTunnelResponseWriter(t.Context(), 4096, 16, func(context.Context, wire.Type, []byte) error { return nil })
	_, hijacker := any(writer).(http.Hijacker)
	require.False(t, hijacker)
	_, flusher := any(writer).(http.Flusher)
	require.True(t, flusher)
}

func frameTypes(frames []capturedTargetFrame) []wire.Type {
	types := make([]wire.Type, len(frames))
	for i := range frames {
		types[i] = frames[i].type_
	}
	return types
}
