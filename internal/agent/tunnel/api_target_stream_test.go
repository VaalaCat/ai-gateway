package tunnel

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestExistingLLMTunnelRequestTrailerBehaviorUnchanged(t *testing.T) {
	handler := NewTargetHandler(TargetHandlerOptions{
		TargetAgentID: "target-a", RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler(),
	})
	open := validBoundTunnelOpen("/v1/responses")
	open.Header = map[string][]string{
		"Content-Type": {"application/json"}, "Trailer": {"X-Checksum"}, "X-Checksum": {"must-not-become-a-trailer"},
	}
	req, err := handler.BuildRequest(
		t.Context(), open, testStreamID(230), io.NopCloser(strings.NewReader("")), agentproxy.IngressKindRelayTunnel,
	)
	require.NoError(t, err)
	require.Empty(t, req.Header.Values("Trailer"))
	require.Empty(t, req.Trailer, "LLM request trailers remain unsupported and stripped")
}

func TestAPITargetStreamRejectsMalformedOpenAndOversizedDataWithoutPoisoningNextStream(t *testing.T) {
	limits := apiTestLimits(4)
	frames := make(chan wire.Frame, 8)
	newTarget := func() *apiTargetStream {
		return newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
			frames <- frame
			return nil
		})
	}
	bad := newTarget()
	err := bad.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: testStreamID(211), Sequence: 1,
		Payload: make([]byte, limits.MaxMetadataBytes+1),
	})
	requireHTTPAPIProtocolError(t, err, "open")
	require.Equal(t, wire.FrameReset, (<-frames).Type)

	healthy := newTarget()
	openPayload, err := wire.EncodeMetadata(apiWireOpen(validAPIOpen(), limits.InitialStreamWindow), limits.MaxMetadataBytes)
	require.NoError(t, err)
	require.NoError(t, healthy.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: testStreamID(212), Sequence: 1, Payload: openPayload,
	}))
	require.Equal(t, wire.FrameReady, (<-frames).Type)
	require.NoError(t, healthy.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: testStreamID(212), Sequence: 2,
	}))
	require.Equal(t, wire.FrameCommitted, (<-frames).Type)
	err = healthy.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: testStreamID(212), Sequence: 3,
		Payload: []byte("toolarge"),
	})
	requireHTTPAPIProtocolError(t, err, "request_data")
	require.Equal(t, wire.FrameReset, (<-frames).Type)
}

func TestAPITargetStreamRejectsUndeclaredRequestTrailerAndDataAfterEnd(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	open := validAPIOpen()
	open.API.RequestTrailerKeys = []string{"X-Declared"}
	require.NoError(t, pair.source.Open(t.Context(), open))

	err := pair.source.EndRequest(t.Context(), wire.Trailers{Header: map[string][]string{"X-Other": {"bad"}}})
	requireHTTPAPIProtocolError(t, err, "request_end")

	pair = newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	err = pair.target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: pair.id, Sequence: 4, Payload: []byte("x"),
	})
	requireHTTPAPIProtocolError(t, err, "request_data")
}

func TestAPITargetStreamCancelUnblocksResponseWindowWithoutLeak(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
	require.NoError(t, pair.target.SendResponseData(t.Context(), []byte("full")))

	blocked := make(chan error, 1)
	go func() { blocked <- pair.target.SendResponseData(t.Context(), []byte("x")) }()
	select {
	case err := <-blocked:
		t.Fatalf("response sender did not block at zero credit: %v", err)
	case <-time.After(20 * time.Millisecond):
	}

	require.NoError(t, pair.source.sendFrame(t.Context(), wire.FrameCancel, nil))
	select {
	case err := <-blocked:
		requireContextCancellation(t, err)
	case <-time.After(time.Second):
		t.Fatal("cancel did not unblock the response sender")
	}
}

func TestAPIStreamOpenRejectsZeroReadyWindowAndUnblocksCaller(t *testing.T) {
	limits := apiTestLimits(4)
	sent := make(chan wire.Frame, 4)
	source := newAPIStream(testStreamID(220), limits, func(_ context.Context, frame wire.Frame) error {
		sent <- frame
		return nil
	})
	opened := make(chan error, 1)
	go func() { opened <- source.Open(t.Context(), validAPIOpen()) }()
	require.Equal(t, wire.FrameOpen, (<-sent).Type)
	payload, err := wire.EncodeMetadata(wire.Ready{}, limits.MaxMetadataBytes)
	require.NoError(t, err)
	err = source.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReady,
		StreamID: testStreamID(220), Sequence: 1, Payload: payload,
	})
	requireHTTPAPIProtocolError(t, err, "window")
	select {
	case err = <-opened:
		requireHTTPAPIProtocolError(t, err, "window")
	case <-time.After(time.Second):
		t.Fatal("invalid READY did not unblock Open")
	}
}

func TestAPITargetStreamSlimsOversizedTraceAndFailsClosedWhenRequiredResultCannotFit(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusBadGateway}))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	result := apiattempt.APIExecutionResult{
		APIUpstreamID: 19, UpstreamStatus: http.StatusBadGateway,
		ProviderDispatchKnown: true, ProviderDispatched: true,
		RequestBytes: 23, ResponseBytes: 29, ErrorStage: "response_body", ErrorCode: "upstream_interrupted",
		Trace: &apiattempt.APIExecutionTrace{
			RequestHeaders: map[string][]string{"X-Debug": {strings.Repeat("h", 6000)}},
			ResponseBody: &apiattempt.APIBodyCapture{
				Data: strings.Repeat("body", 2000), CapturedBytes: 8000, TotalBytes: 9000, Truncated: true,
			},
		},
	}
	require.NoError(t, pair.target.SendResult(t.Context(), result))
	for _, kind := range []app.APIResponseEventKind{app.APIResponseHeaders, app.APIResponseEnd} {
		event, err := pair.source.Receive(t.Context())
		require.NoError(t, err)
		require.Equal(t, kind, event.Kind)
	}
	event, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseResult, event.Kind)
	require.EqualValues(t, 19, event.Result.APIUpstreamID)
	require.True(t, event.Result.ProviderDispatchKnown)
	require.Equal(t, "upstream_interrupted", event.Result.ErrorCode)
	require.NotNil(t, event.Result.Trace)
	require.True(t, event.Result.Trace.ResponseBody.Truncated)
	require.Less(t, len(event.Result.Trace.ResponseBody.Data), 8000)

	pair = newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusBadGateway}))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	pair.target.limits.MaxMetadataBytes = 128
	err = pair.target.SendResult(t.Context(), apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true, ErrorStage: strings.Repeat("a", 64), ErrorCode: strings.Repeat("b", 128),
	})
	require.ErrorIs(t, err, apiattempt.ErrAPIResultTooLarge)
	var protocolErr *app.HTTPAPIProtocolError
	require.True(t, errors.As(err, &protocolErr), "impossible terminal result must fail closed with a protocol reset")
	require.Equal(t, "result", protocolErr.Stage)
	require.True(t, channelContainsFrame(pair.targetFrames, wire.FrameReset))
}

func apiWireOpen(open app.APIOpen, responseWindow int64) wire.Open {
	meta := open.API
	return wire.Open{
		Method: open.Method, Path: open.Path, Header: map[string][]string(open.Header.Clone()), BodyLength: open.BodyLength,
		RemainingNanos: open.Remaining.Nanoseconds(), RequestID: open.RequestID, TargetAgentID: open.TargetAgentID,
		RouteID: open.RouteID, Hop: open.Hop, ResponseWindow: responseWindow, API: &meta,
	}
}
