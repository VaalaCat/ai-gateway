package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAPIStreamRequestAndResponseTrailerRoundTrip(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	open := validAPIOpen()
	open.API.RequestTrailerKeys = []string{"Digest", "X-Request-Final"}
	require.NoError(t, pair.source.Open(t.Context(), open))

	require.NoError(t, pair.source.SendRequestData(t.Context(), []byte("body")))
	var requestBody []byte
	for len(requestBody) < len("body") {
		requestData, err := pair.target.ReceiveRequest(t.Context())
		require.NoError(t, err)
		require.Equal(t, apiRequestData, requestData.Kind)
		requestBody = append(requestBody, requestData.Data...)
	}
	require.Equal(t, []byte("body"), requestBody)

	requestFinal := wire.Trailers{Header: map[string][]string{
		"Digest": {"sha-256=abc"}, "X-Request-Final": {"done"},
	}}
	require.NoError(t, pair.source.EndRequest(t.Context(), requestFinal))
	requestEnd, err := pair.target.ReceiveRequest(t.Context())
	require.NoError(t, err)
	require.Equal(t, apiRequestEnd, requestEnd.Kind)
	require.Equal(t, requestFinal, requestEnd.Trailers)

	headers := wire.Headers{StatusCode: http.StatusCreated,
		Header:  map[string][]string{"Content-Type": {"application/json"}},
		Trailer: map[string][]string{"X-Response-Final": nil},
	}
	require.NoError(t, pair.target.SendHeaders(t.Context(), headers))
	gotHeaders, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseHeaders, gotHeaders.Kind)
	require.Equal(t, &headers, gotHeaders.Headers)

	require.NoError(t, pair.target.SendResponseData(t.Context(), []byte("ok")))
	gotData, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseData, gotData.Kind)
	require.Equal(t, []byte("ok"), gotData.Data)

	responseFinal := wire.Trailers{Header: map[string][]string{"X-Response-Final": {"complete"}}}
	require.NoError(t, pair.target.EndResponse(t.Context(), responseFinal))
	gotEnd, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseEnd, gotEnd.Kind)
	require.Equal(t, &responseFinal, gotEnd.Trailers)

	wantResult := apiattempt.APIExecutionResult{
		APIUpstreamID: 17, UpstreamStatus: http.StatusCreated,
		ProviderDispatchKnown: true, ProviderDispatched: true,
		RequestBytes: 4, ResponseBytes: 2, FirstByteMs: 3,
	}
	require.NoError(t, pair.target.SendResult(t.Context(), wantResult))
	gotResult, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	require.Equal(t, app.APIResponseResult, gotResult.Kind)
	require.Equal(t, &wantResult, gotResult.Result)
}

func TestAPIStreamRejectsDataBeforeOpenAndDuplicateEnd(t *testing.T) {
	limits := apiTestLimits(4)
	targetFrames := make(chan wire.Frame, 4)
	target := newAPITargetStream(limits, func(_ context.Context, frame wire.Frame) error {
		targetFrames <- frame
		return nil
	})
	err := target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: testStreamID(201), Sequence: 1, Payload: []byte("x"),
	})
	requireHTTPAPIProtocolError(t, err, "open")
	require.Equal(t, wire.FrameReset, (<-targetFrames).Type)

	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	err = pair.source.EndRequest(t.Context(), wire.Trailers{})
	requireHTTPAPIProtocolError(t, err, "request_end")
	require.True(t, channelContainsFrame(pair.sourceFrames, wire.FrameReset))
}

func TestAPIStreamRejectsWrongDirectionDuplicateResultAndMalformedMetadata(t *testing.T) {
	pair := newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))

	err := pair.source.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
		StreamID: pair.id, Sequence: 3, Payload: []byte("x"),
	})
	requireHTTPAPIProtocolError(t, err, "direction")

	pair = newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{
		StatusCode: http.StatusOK, Trailer: map[string][]string{"X-Final": nil},
	}))
	_, err = pair.source.Receive(t.Context())
	require.NoError(t, err)
	err = pair.target.EndResponse(t.Context(), wire.Trailers{Header: map[string][]string{"Bad Key": {"x"}}})
	requireHTTPAPIProtocolError(t, err, "response_end")

	pair = newAPIStateMachinePair(t, 8)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusNoContent}))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	result := apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	require.NoError(t, pair.target.SendResult(t.Context(), result))
	err = pair.target.SendResult(t.Context(), result)
	requireHTTPAPIProtocolError(t, err, "result")
}

func TestAPIStreamWindowsBoundSlowUploadAndDownload(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))

	uploadDone := make(chan error, 1)
	go func() { uploadDone <- pair.source.SendRequestData(t.Context(), []byte("abcdefgh")) }()
	require.Eventually(t, func() bool { return pair.target.requestCredit.Available() == 0 }, time.Second, time.Millisecond)
	select {
	case err := <-uploadDone:
		t.Fatalf("upload escaped the request window before consumption: %v", err)
	default:
	}
	uploaded := 0
	for uploaded < 8 {
		event, err := pair.target.ReceiveRequest(t.Context())
		require.NoError(t, err)
		require.Equal(t, apiRequestData, event.Kind)
		uploaded += len(event.Data)
		require.GreaterOrEqual(t, pair.target.requestCredit.Available(), int64(0))
	}
	require.NoError(t, <-uploadDone)
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))

	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
	_, err := pair.source.Receive(t.Context())
	require.NoError(t, err)
	downloadDone := make(chan error, 1)
	go func() { downloadDone <- pair.target.SendResponseData(t.Context(), []byte("abcdefgh")) }()
	require.Eventually(t, func() bool { return pair.source.responseCredit.Available() == 0 }, time.Second, time.Millisecond)
	select {
	case err := <-downloadDone:
		t.Fatalf("download escaped the response window before consumption: %v", err)
	default:
	}
	downloaded := 0
	for downloaded < 8 {
		event, receiveErr := pair.source.Receive(t.Context())
		require.NoError(t, receiveErr)
		require.Equal(t, app.APIResponseData, event.Kind)
		downloaded += len(event.Data)
		require.GreaterOrEqual(t, pair.source.responseCredit.Available(), int64(0))
	}
	require.NoError(t, <-downloadDone)
}

func TestAPIStreamRejectsZeroAndOverflowWindowUpdates(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	zero, err := wire.EncodeMetadata(wire.WindowUpdate{}, pair.limits.MaxMetadataBytes)
	require.NoError(t, err)
	err = pair.target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate,
		StreamID: pair.id, Sequence: 3, Payload: zero,
	})
	requireHTTPAPIProtocolError(t, err, "window")

	pair = newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	overflow, err := wire.EncodeMetadata(wire.WindowUpdate{Bytes: 5}, pair.limits.MaxMetadataBytes)
	require.NoError(t, err)
	err = pair.target.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameWindowUpdate,
		StreamID: pair.id, Sequence: 3, Payload: overflow,
	})
	requireHTTPAPIProtocolError(t, err, "window")
}

func TestAPIStreamOpenOwnsRequestTrailerDeclarations(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	open := validAPIOpen()
	open.API.RequestTrailerKeys = []string{"X-Original"}
	require.NoError(t, pair.source.Open(t.Context(), open))
	open.API.RequestTrailerKeys[0] = "X-Mutated"

	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{
		Header: map[string][]string{"X-Original": {"final"}},
	}))
	event, err := pair.target.ReceiveRequest(t.Context())
	require.NoError(t, err)
	require.Equal(t, apiRequestEnd, event.Kind)
	require.Equal(t, "final", event.Trailers.Header["X-Original"][0])
}

func TestAPIStreamDeliversBufferedDataAfterResultWithoutLateWindowUpdate(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
	require.NoError(t, pair.target.SendResponseData(t.Context(), []byte("body")))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendResult(t.Context(), apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true, ProviderDispatched: true, ResponseBytes: 4,
	}))
	drainAPIFrames(pair.sourceFrames)

	for _, kind := range []app.APIResponseEventKind{
		app.APIResponseHeaders, app.APIResponseData, app.APIResponseData, app.APIResponseEnd, app.APIResponseResult,
	} {
		event, err := pair.source.Receive(t.Context())
		require.NoError(t, err)
		require.Equal(t, kind, event.Kind)
	}
	_, err := pair.source.Receive(t.Context())
	require.ErrorIs(t, err, io.EOF)
	require.False(t, channelContainsFrame(pair.sourceFrames, wire.FrameWindowUpdate),
		"a terminal stream must not emit credit after its Result")
}

func TestAPIStreamRejectsSemanticallyInvalidResultAndResetsPeer(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	payload, err := json.Marshal(apiattempt.APIExecutionResult{ProviderDispatchKnown: false})
	require.NoError(t, err)
	err = pair.source.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameAPIResult, StreamID: pair.id, Sequence: 5, Payload: payload,
	})
	requireHTTPAPIProtocolError(t, err, "result")
	require.True(t, channelContainsFrame(pair.sourceFrames, wire.FrameReset))
}

func TestAPIStreamRejectsInvalidRawResultEncodingAndResetsPeer(t *testing.T) {
	pair := newAPIStateMachinePair(t, 4)
	require.NoError(t, pair.source.Open(t.Context(), validAPIOpen()))
	require.NoError(t, pair.source.EndRequest(t.Context(), wire.Trailers{}))
	require.NoError(t, pair.target.SendHeaders(t.Context(), wire.Headers{StatusCode: http.StatusOK}))
	require.NoError(t, pair.target.EndResponse(t.Context(), wire.Trailers{}))
	payload := append([]byte(`{"provider_dispatch_known":true,"api_upstream_name":"`), []byte{0xff, '"', '}'}...)
	err := pair.source.acceptFrame(t.Context(), wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameAPIResult, StreamID: pair.id, Sequence: 5, Payload: payload,
	})
	requireHTTPAPIProtocolError(t, err, "result")
	require.True(t, channelContainsFrame(pair.sourceFrames, wire.FrameReset))
}

type apiStateMachinePair struct {
	id           wire.StreamID
	limits       wire.Limits
	source       *APIStream
	target       *apiTargetStream
	sourceFrames chan wire.Frame
	targetFrames chan wire.Frame
}

func newAPIStateMachinePair(t *testing.T, window int64) *apiStateMachinePair {
	t.Helper()
	pair := &apiStateMachinePair{
		id: testStreamID(200), limits: apiTestLimits(window),
		sourceFrames: make(chan wire.Frame, 128), targetFrames: make(chan wire.Frame, 128),
	}
	pair.source = newAPIStream(pair.id, pair.limits, func(ctx context.Context, frame wire.Frame) error {
		pair.sourceFrames <- frame
		decoded, err := apiCodecRoundTrip(frame, pair.limits)
		if err != nil {
			return err
		}
		return pair.target.acceptFrame(ctx, decoded)
	})
	pair.target = newAPITargetStream(pair.limits, func(ctx context.Context, frame wire.Frame) error {
		pair.targetFrames <- frame
		decoded, err := apiCodecRoundTrip(frame, pair.limits)
		if err != nil {
			return err
		}
		return pair.source.acceptFrame(ctx, decoded)
	})
	return pair
}

func apiCodecRoundTrip(frame wire.Frame, limits wire.Limits) (wire.Frame, error) {
	encoded, err := wire.Encode(frame, limits)
	if err != nil {
		return wire.Frame{}, err
	}
	return wire.Decode(encoded, limits)
}

func apiTestLimits(window int64) wire.Limits {
	return wire.Limits{
		MaxMetadataBytes: 4096, MaxDataBytes: 3, InitialStreamWindow: window,
		MaxQueuedSessionBytes: 4096, MaxConcurrentStreams: 8,
	}
}

func validAPIOpen() app.APIOpen {
	return app.APIOpen{
		TargetAgentID: "agent-b", RouteID: 3, RequestID: "request-1",
		Method: http.MethodPost, Path: "/v1/api/events/append", Header: http.Header{"Content-Type": {"application/json"}},
		BodyLength: -1, Remaining: time.Minute, Hop: 1,
		API: apiattempt.APIAttemptMeta{
			APIServiceID: 7, APIRouteID: 9, Protocol: apiattempt.APIProtocolHTTP,
			Method: http.MethodPost, Subpath: "/append", RawQuery: "sync=true",
		},
	}
}

func requireHTTPAPIProtocolError(t *testing.T, err error, stage string) {
	t.Helper()
	var protocolErr *app.HTTPAPIProtocolError
	require.ErrorAs(t, err, &protocolErr)
	require.Equal(t, stage, protocolErr.Stage)
	require.Equal(t, wire.ErrorCodeRelayProtocol, protocolErr.Code)
}

func channelContainsFrame(frames <-chan wire.Frame, frameType wire.Type) bool {
	for {
		select {
		case frame := <-frames:
			if frame.Type == frameType {
				return true
			}
		default:
			return false
		}
	}
}

func drainAPIFrames(frames <-chan wire.Frame) {
	for {
		select {
		case <-frames:
		default:
			return
		}
	}
}

func requireContextCancellation(t *testing.T, err error) {
	t.Helper()
	require.True(t, errors.Is(err, context.Canceled) || errors.Is(err, errStreamClosed), err)
}
