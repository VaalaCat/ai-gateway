package tunnel

import (
	"context"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

var (
	_ app.HTTPAPIStreamOpener                   = (*Session)(nil)
	_ app.HTTPAPIStreamOpener                   = (*Manager)(nil)
	_ agentproxy.DirectHTTPAPIStreamOpener      = (*DirectSessionPool)(nil)
	_ agentproxy.DirectHTTPAPIStreamReservation = (*directHTTPAPIStreamReservation)(nil)
)

func TestAPISessionIntegrationStreamsRelayAndDirectWithTrailersAndReclaimsRegistries(t *testing.T) {
	for _, direction := range []SessionDirection{SessionDirectionRelay, SessionDirectionDirectOutgoing} {
		direction := direction
		t.Run(apiSessionDirectionName(direction), func(t *testing.T) {
			limits := testLimits(4)
			requestSeen := make(chan struct{}, 1)
			sourceAgentSeen := make(chan string, 1)
			handler := APITargetHandlerFunc(func(ctx context.Context, stream *APITargetStream) error {
				open := stream.OpenMetadata()
				require.Equal(t, "request-api-session", open.RequestID)
				sourceAgentSeen <- open.SourceAgentID
				var requestBody []byte
				for {
					event, err := stream.ReceiveRequest(ctx)
					require.NoError(t, err)
					switch event.Kind {
					case APIRequestData:
						requestBody = append(requestBody, event.Data...)
					case APIRequestEnd:
						require.Equal(t, []byte("request-body"), requestBody)
						require.Equal(t, "request-final", http.Header(event.Trailers.Header).Get("X-Request-Final"))
						requestSeen <- struct{}{}
						return sendAPISessionResponse(ctx, stream)
					}
				}
			})
			source, target := startAPISessionPair(t, limits, direction, handler)

			open := validAPIOpen()
			open.RequestID = "request-api-session"
			open.BodyLength = int64(len("request-body"))
			open.API.RequestTrailerKeys = []string{"X-Request-Final"}
			stream, err := source.OpenHTTPAPIStream(t.Context(), open)
			require.NoError(t, err)
			if direction == SessionDirectionDirectOutgoing {
				require.Equal(t, "agent-a", <-sourceAgentSeen)
			} else {
				<-sourceAgentSeen
			}
			require.Equal(t, 1, source.StreamCount())
			require.Equal(t, 1, target.StreamCount())
			require.NoError(t, stream.SendRequestData(t.Context(), []byte("request-body")))
			require.NoError(t, stream.EndRequest(t.Context(), wire.Trailers{Header: http.Header{
				"X-Request-Final": {"request-final"},
			}}))

			var responseBody []byte
			var kinds []app.APIResponseEventKind
			for {
				event, receiveErr := stream.Receive(t.Context())
				require.NoError(t, receiveErr)
				kinds = append(kinds, event.Kind)
				if event.Kind == app.APIResponseData {
					responseBody = append(responseBody, event.Data...)
				}
				if event.Kind == app.APIResponseEnd {
					require.Equal(t, "response-final", http.Header(event.Trailers.Header).Get("X-Response-Final"))
				}
				if event.Kind == app.APIResponseResult {
					require.EqualValues(t, 17, event.Result.APIUpstreamID)
					break
				}
			}
			require.Equal(t, app.APIResponseHeaders, kinds[0])
			require.Equal(t, app.APIResponseEnd, kinds[len(kinds)-2])
			require.Equal(t, app.APIResponseResult, kinds[len(kinds)-1])
			for _, kind := range kinds[1 : len(kinds)-2] {
				require.Equal(t, app.APIResponseData, kind)
			}
			require.Equal(t, []byte("response-body"), responseBody)
			_, err = stream.Receive(t.Context())
			require.ErrorIs(t, err, io.EOF)
			require.NoError(t, stream.Close())
			select {
			case <-requestSeen:
			case <-time.After(time.Second):
				t.Fatal("API target handler did not consume the request")
			}
			require.Eventually(t, func() bool {
				return source.StreamCount() == 0 && target.StreamCount() == 0
			}, time.Second, time.Millisecond)
		})
	}
}

func TestAPISessionCancelReclaimsQueuedBudgetsRegistriesAndTombstones(t *testing.T) {
	responseQueued := make(chan error, 1)
	handler := APITargetHandlerFunc(func(ctx context.Context, stream *APITargetStream) error {
		if err := stream.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK}); err != nil {
			responseQueued <- err
			return err
		}
		if err := stream.SendResponseData(ctx, []byte("rsp")); err != nil {
			responseQueued <- err
			return err
		}
		responseQueued <- nil
		<-ctx.Done()
		return context.Cause(ctx)
	})
	source, target := startAPISessionPair(t, testLimits(4), SessionDirectionRelay, handler)
	opened, err := source.OpenHTTPAPIStream(t.Context(), validAPIOpen())
	require.NoError(t, err)
	stream := opened.(*APIStream)
	select {
	case responseErr := <-responseQueued:
		require.NoError(t, responseErr)
	case <-time.After(time.Second):
		t.Fatal("API target handler did not queue its response")
	}
	require.NoError(t, stream.SendRequestData(t.Context(), []byte("req")))
	require.Eventually(t, func() bool {
		return source.incomingSize() == 3 && target.incomingSize() == 3
	}, time.Second, time.Millisecond)
	stream.Cancel(context.Canceled)
	require.NoError(t, stream.Close())
	require.Eventually(t, func() bool {
		return source.StreamCount() == 0 && target.StreamCount() == 0 &&
			source.incomingSize() == 0 && target.incomingSize() == 0 &&
			source.tombstones.Contains(stream.id) && target.tombstones.Contains(stream.id)
	}, time.Second, time.Millisecond)
}

func TestAPIStreamCloseAfterResultDrainsUnreadResponseBudget(t *testing.T) {
	responseSent := make(chan error, 1)
	handler := APITargetHandlerFunc(func(ctx context.Context, stream *APITargetStream) error {
		if err := stream.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK}); err != nil {
			responseSent <- err
			return err
		}
		if err := stream.SendResponseData(ctx, []byte("rsp")); err != nil {
			responseSent <- err
			return err
		}
		if err := stream.EndResponse(ctx, wire.Trailers{}); err != nil {
			responseSent <- err
			return err
		}
		err := stream.SendResult(ctx, apiattempt.APIExecutionResult{ProviderDispatchKnown: true})
		responseSent <- err
		return err
	})
	source, target := startAPISessionPair(t, testLimits(4), SessionDirectionRelay, handler)
	opened, err := source.OpenHTTPAPIStream(t.Context(), validAPIOpen())
	require.NoError(t, err)
	stream := opened.(*APIStream)
	select {
	case responseErr := <-responseSent:
		require.NoError(t, responseErr)
	case <-time.After(time.Second):
		t.Fatal("API target handler did not publish its terminal response")
	}
	require.Eventually(t, func() bool {
		return source.StreamCount() == 0 && target.StreamCount() == 0 && source.incomingSize() == 3
	}, time.Second, time.Millisecond, "Result must remove both registries while retaining unread response data")

	require.NoError(t, stream.Close())
	require.NoError(t, stream.Close(), "Close must remain idempotent after terminal drain")
	require.Eventually(t, func() bool {
		return source.incomingSize() == 0
	}, time.Second, time.Millisecond, "Close must release unread ResponseData after Result removed the registry")
}

func TestAPISessionTargetResultCodecFailureResetsSourceAndReclaimsRegistries(t *testing.T) {
	responseEnded := make(chan struct{})
	allowInvalidResult := make(chan struct{})
	handlerDone := make(chan error, 1)
	handler := APITargetHandlerFunc(func(ctx context.Context, stream *APITargetStream) error {
		if err := stream.SendHeaders(ctx, wire.Headers{StatusCode: http.StatusOK}); err != nil {
			handlerDone <- err
			return err
		}
		if err := stream.EndResponse(ctx, wire.Trailers{}); err != nil {
			handlerDone <- err
			return err
		}
		close(responseEnded)
		<-allowInvalidResult
		err := stream.SendResult(ctx, apiattempt.APIExecutionResult{})
		handlerDone <- err
		return err
	})
	source, target := startAPISessionPair(t, testLimits(4), SessionDirectionRelay, handler)
	stream, err := source.OpenHTTPAPIStream(t.Context(), validAPIOpen())
	require.NoError(t, err)
	select {
	case <-responseEnded:
	case <-time.After(time.Second):
		t.Fatal("API target handler did not publish Headers and End")
	}
	for _, want := range []app.APIResponseEventKind{app.APIResponseHeaders, app.APIResponseEnd} {
		event, receiveErr := stream.Receive(t.Context())
		require.NoError(t, receiveErr)
		require.Equal(t, want, event.Kind)
	}
	close(allowInvalidResult)
	require.Error(t, <-handlerDone)

	receiveCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	_, err = stream.Receive(receiveCtx)
	requireHTTPAPIProtocolError(t, err, "result")
	require.Eventually(t, func() bool {
		return source.StreamCount() == 0 && target.StreamCount() == 0
	}, time.Second, time.Millisecond)
}

func sendAPISessionResponse(ctx context.Context, stream *APITargetStream) error {
	if err := stream.SendHeaders(ctx, wire.Headers{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"text/plain"}},
		Trailer:    http.Header{"X-Response-Final": nil},
	}); err != nil {
		return err
	}
	if err := stream.SendResponseData(ctx, []byte("response-body")); err != nil {
		return err
	}
	if err := stream.EndResponse(ctx, wire.Trailers{Header: http.Header{
		"X-Response-Final": {"response-final"},
	}}); err != nil {
		return err
	}
	return stream.SendResult(ctx, apiattempt.APIExecutionResult{
		APIUpstreamID: 17, UpstreamStatus: http.StatusCreated,
		ProviderDispatchKnown: true, ProviderDispatched: true,
		RequestBytes: int64(len("request-body")), ResponseBytes: int64(len("response-body")),
	})
}

func startAPISessionPair(
	t *testing.T,
	limits wire.Limits,
	direction SessionDirection,
	handler APITargetHandler,
) (*Session, *Session) {
	t.Helper()
	sourceConn, targetConn := websocketPair(t)
	sourceOpts := SessionOptions{Direction: direction, PingInterval: time.Hour, PongTimeout: time.Hour}
	targetOpts := SessionOptions{
		Direction: direction, PingInterval: time.Hour, PongTimeout: time.Hour,
		TargetHandler: NewTargetHandler(TargetHandlerOptions{
			TargetAgentID: "agent-b", DirectInboundEnabled: func() bool { return true },
			RelayInboundEnabled: func() bool { return true }, Router: http.NotFoundHandler(),
		}),
		APITargetHandler: handler,
	}
	if direction == SessionDirectionDirectOutgoing {
		targetOpts.Direction = SessionDirectionDirectIncoming
		targetOpts.BoundSourceAgentID = "agent-a"
		targetOpts.AdmissionDeadline = time.Now().Add(time.Hour)
		targetOpts.SourceEnabled = func(sourceID string) bool { return sourceID == "agent-a" }
		targetOpts.TargetStatusEnabled = func() bool { return true }
	}
	source := NewSession(sourceConn, 21, limits, sourceOpts)
	target := NewSession(targetConn, 22, limits, targetOpts)
	var runs sync.WaitGroup
	runs.Add(2)
	go func() { defer runs.Done(); _ = source.Run(t.Context()) }()
	go func() { defer runs.Done(); _ = target.Run(t.Context()) }()
	t.Cleanup(func() {
		source.Cancel(context.Canceled)
		target.Cancel(context.Canceled)
		runs.Wait()
	})
	return source, target
}

func apiSessionDirectionName(direction SessionDirection) string {
	if direction == SessionDirectionDirectOutgoing {
		return "direct"
	}
	return "relay"
}
