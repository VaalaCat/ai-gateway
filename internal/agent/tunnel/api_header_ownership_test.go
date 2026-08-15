package tunnel

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAPIStreamOpenFiltersUnsafeHeadersAndOwnsCallerValues(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	open := validAPIOpen()
	businessValues := []string{"before"}
	open.Header = http.Header{
		"X-Business":          businessValues,
		"Connection":          {"X-Connection-Secret"},
		"X-Connection-Secret": {"secret"},
		"Authorization":       {"Bearer client-secret"},
		"Forwarded":           {"for=attacker"},
		"X-Forwarded-For":     {"203.0.113.10"},
		"X-Vaala-Forged":      {"internal"},
		"Trailer":             {"X-Forged-Trailer"},
		"Transfer-Encoding":   {"chunked"},
	}
	require.NoError(t, pair.source.Open(t.Context(), open))
	businessValues[0] = "after"
	metadata := pair.target.OpenMetadata()
	require.Equal(t, map[string][]string{"X-Business": {"before"}}, metadata.Header)
}

func TestAPIStreamOpenRejectsInvalidRequestLineAndHeadersBeforeSending(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*app.APIOpen)
	}{
		{name: "invalid method", mutate: func(open *app.APIOpen) { open.Method = "BAD METHOD"; open.API.Method = open.Method }},
		{name: "absolute request URI", mutate: func(open *app.APIOpen) { open.Path = "https://attacker.example/path" }},
		{name: "fragment request URI", mutate: func(open *app.APIOpen) { open.Path = "/path#fragment" }},
		{name: "invalid header name", mutate: func(open *app.APIOpen) { open.Header["Bad Header"] = []string{"value"} }},
		{name: "invalid header value", mutate: func(open *app.APIOpen) { open.Header["X-Test"] = []string{"ok\r\nInjected: value"} }},
		{name: "unknown trace mode", mutate: func(open *app.APIOpen) {
			open.API.TracePolicy = apiattempt.APITracePolicy{Mode: "future", MaxBodyBytes: 4096}
		}},
		{name: "trace body limit above bound", mutate: func(open *app.APIOpen) {
			open.API.TracePolicy = apiattempt.APITracePolicy{Mode: apiattempt.APITraceModeFull, MaxBodyBytes: apiattempt.MaxTraceBodyBytes + 1}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var openFrames int
			stream := newAPIStream(testStreamID(250), apiTestLimits(8), func(context.Context, wire.Frame) error {
				openFrames++
				return nil
			})
			open := validAPIOpen()
			test.mutate(&open)
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			err := stream.Open(ctx, open)
			requireHTTPAPIProtocolError(t, err, "open")
			require.Zero(t, openFrames)
		})
	}
}

func TestAPITargetOpenNormalizesUntrustedWireHeaders(t *testing.T) {
	limits := apiTestLimits(8)
	target := newAPITargetStream(limits, func(context.Context, wire.Frame) error { return nil })
	open := apiWireOpen(validAPIOpen(), limits.InitialStreamWindow)
	open.Header = map[string][]string{
		"X-Business": {"kept"}, "Authorization": {"secret"}, "Forwarded": {"for=attacker"},
		"X-Forwarded-Host": {"attacker.example"}, "X-Vaala-Forged": {"internal"},
		"Connection": {"X-Hop"}, "X-Hop": {"secret"}, "Trailer": {"X-Final"},
	}
	payload, err := wire.EncodeMetadata(open, limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: testStreamID(251), Sequence: 1, Payload: payload,
	}))
	metadata := target.OpenMetadata()
	require.Equal(t, map[string][]string{"X-Business": {"kept"}}, metadata.Header)
}

func TestAPIStreamOpenRejectsUnsafeRequestTrailerDeclarationsBeforeSending(t *testing.T) {
	for _, test := range apiUnsafeRequestTrailerCases() {
		t.Run(test.name, func(t *testing.T) {
			var openFrames int
			unexpectedOpen := errors.New("unsafe trailer declaration reached FrameOpen")
			stream := newAPIStream(testStreamID(255), apiTestLimits(8), func(_ context.Context, frame wire.Frame) error {
				if frame.Type == wire.FrameOpen {
					openFrames++
					return unexpectedOpen
				}
				return nil
			})
			open := validAPIOpen()
			for name, values := range test.header {
				open.Header[name] = append([]string(nil), values...)
			}
			open.API.RequestTrailerKeys = []string{test.key}

			err := stream.Open(t.Context(), open)
			requireHTTPAPIProtocolError(t, err, "open")
			require.Zero(t, openFrames, "unsafe trailer declaration must fail before FrameOpen")
		})
	}
}

func TestAPITargetOpenRejectsUnsafeWireRequestTrailerDeclarations(t *testing.T) {
	for _, test := range apiUnsafeRequestTrailerCases() {
		t.Run(test.name, func(t *testing.T) {
			limits := apiTestLimits(8)
			var readyFrames, resetFrames int
			target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
				switch frame.Type {
				case wire.FrameReady:
					readyFrames++
				case wire.FrameReset:
					resetFrames++
				}
				return nil
			})
			open := apiWireOpen(validAPIOpen(), limits.InitialStreamWindow)
			for name, values := range test.header {
				open.Header[name] = append([]string(nil), values...)
			}
			open.API.RequestTrailerKeys = []string{test.key}
			payload, err := wire.EncodeMetadata(open, limits.MaxMetadataBytes)
			require.NoError(t, err)

			err = target.acceptFrame(t.Context(), wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameOpen,
				StreamID: testStreamID(215), Sequence: 1, Payload: payload,
			})
			requireHTTPAPIProtocolError(t, err, "open")
			require.Zero(t, readyFrames)
			require.Equal(t, 1, resetFrames)
		})
	}
}

func TestAPITrailerSafetyRejectsUnsafeResponseDeclarationsAndFinalValues(t *testing.T) {
	for _, key := range apiUnsafeTrailerKeys() {
		t.Run(key, func(t *testing.T) {
			values := map[string][]string{key: {"unsafe"}}
			_, err := normalizeAPIRequestTrailers(wire.Trailers{Header: values}, []string{key})
			require.Error(t, err, "request final Trailer value must apply the ordinary Header safety boundary")

			_, err = normalizeAPIResponseHeaders(wire.Headers{
				StatusCode: http.StatusOK,
				Trailer:    map[string][]string{key: nil},
			})
			require.Error(t, err, "response Trailer declaration must apply the ordinary Header safety boundary")

			_, err = normalizeAPIResponseTrailers(
				wire.Trailers{Header: values},
				http.Header{http.CanonicalHeaderKey(key): nil},
			)
			require.Error(t, err, "response final Trailer value must apply the ordinary Header safety boundary")
		})
	}
}

func TestAPIResponseTrailerDeclarationsRejectOrdinaryHeaderConflictsAtBothBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		header  http.Header
		trailer http.Header
	}{
		{
			name: "ordinary Header overlap",
			header: http.Header{
				"X-Final": {"ordinary"},
			},
			trailer: http.Header{"x-final": nil},
		},
		{
			name: "ordinary Connection nominated field",
			header: http.Header{
				"Connection": {"X-Hop"},
			},
			trailer: http.Header{"x-hop": nil},
		},
	}
	for _, test := range tests {
		t.Run(test.name+"/target round trip", func(t *testing.T) {
			pair := newAPIStateMachinePair(t, 8)
			require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
			drainAPIFrames(pair.targetFrames)

			err := pair.target.SendHeaders(t.Context(), wire.Headers{
				StatusCode: http.StatusOK,
				Header:     map[string][]string(test.header),
				Trailer:    map[string][]string(test.trailer),
			})
			requireHTTPAPIProtocolError(t, err, "headers")
			var headerFrames, resetFrames int
		frames:
			for {
				select {
				case frame := <-pair.targetFrames:
					switch frame.Type {
					case wire.FrameHeaders:
						headerFrames++
					case wire.FrameReset:
						resetFrames++
					}
				default:
					require.Zero(t, headerFrames, "invalid declarations must fail before Headers publish")
					require.Equal(t, 1, resetFrames)
					break frames
				}
			}
			_, err = pair.source.Receive(t.Context())
			requireHTTPAPIProtocolError(t, err, "headers")
		})

		t.Run(test.name+"/source wire decode", func(t *testing.T) {
			limits := apiTestLimits(8)
			frames := make(chan wire.Frame, 2)
			source := newAPIStream(testStreamID(217), limits, func(_ context.Context, frame wire.Frame) error {
				frames <- frame
				return nil
			})
			source.stateMu.Lock()
			source.requestPhase = apiSourceRequestStreaming
			source.stateMu.Unlock()
			payload, err := wire.EncodeMetadata(wire.Headers{
				StatusCode: http.StatusOK,
				Header:     map[string][]string(test.header),
				Trailer:    map[string][]string(test.trailer),
			}, limits.MaxMetadataBytes)
			require.NoError(t, err)

			err = source.acceptFrame(t.Context(), wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameHeaders,
				StreamID: source.id, Sequence: 1, Payload: payload,
			})
			requireHTTPAPIProtocolError(t, err, "headers")
			require.True(t, channelContainsFrame(frames, wire.FrameReset))
		})
	}
}

type apiUnsafeRequestTrailerCase struct {
	name   string
	key    string
	header http.Header
}

func apiUnsafeRequestTrailerCases() []apiUnsafeRequestTrailerCase {
	tests := make([]apiUnsafeRequestTrailerCase, 0, len(apiUnsafeTrailerKeys())+2)
	for _, key := range apiUnsafeTrailerKeys() {
		tests = append(tests, apiUnsafeRequestTrailerCase{name: key, key: key})
	}
	return append(tests,
		apiUnsafeRequestTrailerCase{
			name: "Connection nominated field", key: "X-Hop",
			header: http.Header{"Connection": {"X-Hop"}, "X-Hop": {"ordinary"}},
		},
		apiUnsafeRequestTrailerCase{
			name: "ordinary Header overlap", key: "X-Business",
			header: http.Header{"X-Business": {"ordinary"}},
		},
	)
}

func apiUnsafeTrailerKeys() []string {
	return []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"TE", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Host",
		"Authorization", "Forwarded", "X-Forwarded-For", "X-Vaala-Custom", consts.HeaderXAgentID,
	}
}

func TestAPIResponseHeaderEventDoesNotOwnInternalTrailerDeclarations(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	headers := wire.Headers{
		StatusCode: http.StatusOK,
		Header:     map[string][]string{"X-Business": {"before"}},
		Trailer:    map[string][]string{"X-Final": nil},
	}
	require.NoError(t, pair.target.SendHeaders(t.Context(), headers))
	headers.Header["X-Business"][0] = "after"
	delete(headers.Trailer, "X-Final")
	event, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseHeaders, event.Kind)
	require.Equal(t, "before", event.Headers.Header["X-Business"][0])
	delete(event.Headers.Trailer, "X-Final")

	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{
		Header: map[string][]string{"X-Final": {"complete"}},
	}))
	end, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseEnd, end.Kind)
	require.Equal(t, "complete", end.Trailers.Header["X-Final"][0])
}
