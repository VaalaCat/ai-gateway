package genericapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countingProtocolHandler struct{ calls atomic.Int32 }

func (h *countingProtocolHandler) Serve(context.Context, *RequestContext) error {
	h.calls.Add(1)
	return nil
}

type loopbackAPIOpener struct {
	target *APITargetHandler
	calls  atomic.Int32
	open   app.APIOpen
}

func (o *loopbackAPIOpener) OpenHTTPAPIStream(ctx context.Context, open app.APIOpen) (app.HTTPAPIStream, error) {
	o.calls.Add(1)
	o.open = open
	stream := newLoopbackHTTPAPIStream(open)
	go func() { stream.targetDone <- o.target.serveStream(ctx, stream) }()
	return stream, nil
}

type loopbackHTTPAPIStream struct {
	open       wire.Open
	requests   chan agenttunnel.APIRequestEvent
	responses  chan app.APIResponseEvent
	targetDone chan error
	closeOnce  sync.Once
}

func newLoopbackHTTPAPIStream(open app.APIOpen) *loopbackHTTPAPIStream {
	meta := open.API
	return &loopbackHTTPAPIStream{
		open: wire.Open{
			Method: open.Method, Path: open.Path, Header: map[string][]string(open.Header.Clone()), BodyLength: open.BodyLength,
			RequestID: open.RequestID, SourceAgentID: "source-a", TargetAgentID: open.TargetAgentID, RouteID: open.RouteID, Hop: open.Hop, API: &meta,
		},
		requests: make(chan agenttunnel.APIRequestEvent, 8), responses: make(chan app.APIResponseEvent, 8), targetDone: make(chan error, 1),
	}
}

func (s *loopbackHTTPAPIStream) Open(context.Context, app.APIOpen) error { return nil }
func (s *loopbackHTTPAPIStream) OpenMetadata() wire.Open                 { return s.open }

func (s *loopbackHTTPAPIStream) SendRequestData(_ context.Context, data []byte) error {
	s.requests <- agenttunnel.APIRequestEvent{Kind: agenttunnel.APIRequestData, Data: append([]byte(nil), data...)}
	return nil
}

func (s *loopbackHTTPAPIStream) EndRequest(_ context.Context, trailers wire.Trailers) error {
	s.requests <- agenttunnel.APIRequestEvent{Kind: agenttunnel.APIRequestEnd, Trailers: trailers}
	return nil
}

func (s *loopbackHTTPAPIStream) ReceiveRequest(ctx context.Context) (agenttunnel.APIRequestEvent, error) {
	select {
	case event := <-s.requests:
		return event, nil
	case <-ctx.Done():
		return agenttunnel.APIRequestEvent{}, context.Cause(ctx)
	}
}

func (s *loopbackHTTPAPIStream) SendHeaders(_ context.Context, headers wire.Headers) error {
	s.responses <- app.APIResponseEvent{Kind: app.APIResponseHeaders, Headers: &headers}
	return nil
}

func (s *loopbackHTTPAPIStream) SendResponseData(_ context.Context, data []byte) error {
	s.responses <- app.APIResponseEvent{Kind: app.APIResponseData, Data: append([]byte(nil), data...)}
	return nil
}

func (s *loopbackHTTPAPIStream) EndResponse(_ context.Context, trailers wire.Trailers) error {
	s.responses <- app.APIResponseEvent{Kind: app.APIResponseEnd, Trailers: &trailers}
	return nil
}

func (s *loopbackHTTPAPIStream) SendResult(_ context.Context, result apiattempt.APIExecutionResult) error {
	s.responses <- app.APIResponseEvent{Kind: app.APIResponseResult, Result: &result}
	return nil
}

func (s *loopbackHTTPAPIStream) Receive(ctx context.Context) (app.APIResponseEvent, error) {
	select {
	case event := <-s.responses:
		return event, nil
	case <-ctx.Done():
		return app.APIResponseEvent{}, context.Cause(ctx)
	}
}

func (s *loopbackHTTPAPIStream) Cancel(error) {}
func (s *loopbackHTTPAPIStream) Close() error {
	select {
	case err := <-s.targetDone:
		return err
	default:
		return nil
	}
}

func TestExecutionRouterRemoteKeepsFrozenAgentAndDispatchesOneFrozenUpstream(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, ForwardSubpath: true},
	}}
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "Remote Primary", BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	var providerCalls atomic.Int32
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
	}))
	executionLimiter := &executionLimiterSpy{}
	target := NewAPITargetHandler(finder, NewHTTPHandler(picker, transport, executionLimiter))
	relay := &loopbackAPIOpener{target: target}
	remote := NewRemoteHTTPHandler(RemoteHTTPHandlerOptions{Relay: relay})
	local := &countingProtocolHandler{}
	routed := NewExecutionRouter("source-a", local, remote)

	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/v1/api/weather/forecast", nil)
	rc := &RequestContext{
		Context: ginContext, Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9},
		Protocol: ProtocolHTTP, RequestID: "request-loopback", Agent: AgentPick{
			ExecutionAgentID: "execution-a", AgentRouteID: 42,
			Target: models.Agent{AgentID: "execution-a", RelayInboundEnabled: true},
		},
		UserID: 5, GroupID: 3, TokenID: 2,
	}

	require.NoError(t, routed.Serve(t.Context(), rc))
	require.Zero(t, local.calls.Load())
	require.Equal(t, int32(1), relay.calls.Load())
	require.Equal(t, "execution-a", relay.open.TargetAgentID)
	require.Equal(t, uint(42), relay.open.RouteID)
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, int32(1), providerCalls.Load())
	require.Equal(t, uint(5), executionLimiter.facts.UserID)
	require.Equal(t, uint(3), executionLimiter.facts.GroupID)
	require.Equal(t, uint(2), executionLimiter.facts.TokenID)
	require.True(t, rc.Execution.ProviderDispatched)
	require.Equal(t, uint(11), rc.Execution.APIUpstreamID)
	require.Equal(t, "Remote Primary", rc.UpstreamName)
}

func TestExecutionRouterUsesLocalOnlyForSourceAgent(t *testing.T) {
	local := &countingProtocolHandler{}
	remote := &countingProtocolHandler{}
	handler := NewExecutionRouter("source-a", local, remote)
	rc := &RequestContext{Agent: AgentPick{ExecutionAgentID: "source-a"}}

	require.NoError(t, handler.Serve(t.Context(), rc))
	require.Equal(t, int32(1), local.calls.Load())
	require.Zero(t, remote.calls.Load())

	rc.Agent.ExecutionAgentID = "execution-a"
	require.NoError(t, handler.Serve(t.Context(), rc))
	require.Equal(t, int32(1), remote.calls.Load())
	require.ErrorIs(t, NewExecutionRouter("", local, remote).Serve(t.Context(), rc), ErrExecutionUnavailable)
	require.ErrorIs(t, NewExecutionRouter("source-a", nil, remote).Serve(t.Context(), &RequestContext{Agent: AgentPick{ExecutionAgentID: "source-a"}}), ErrExecutionUnavailable)
	require.ErrorIs(t, NewExecutionRouter("source-a", local, nil).Serve(t.Context(), rc), ErrExecutionUnavailable)
}

func TestExecutorDispatchesRegisteredProtocolAndRejectsInvalidInput(t *testing.T) {
	httpHandler := &countingProtocolHandler{}
	webSocketHandler := &countingProtocolHandler{}
	executor := NewExecutor(map[string]ProtocolHandler{
		ProtocolHTTP: httpHandler, ProtocolWebSocket: webSocketHandler,
	})

	require.NoError(t, executor.Execute(t.Context(), &RequestContext{Protocol: ProtocolHTTP}))
	require.Equal(t, int32(1), httpHandler.calls.Load())
	require.Zero(t, webSocketHandler.calls.Load())

	require.NoError(t, executor.Execute(t.Context(), &RequestContext{Protocol: ProtocolWebSocket}))
	require.Equal(t, int32(1), webSocketHandler.calls.Load())
	require.ErrorIs(t, executor.Execute(t.Context(), &RequestContext{Protocol: "unknown"}), ErrExecutionUnavailable)
	require.ErrorIs(t, executor.Execute(nil, &RequestContext{Protocol: ProtocolHTTP}), ErrExecutionUnavailable)
	require.ErrorIs(t, executor.Execute(t.Context(), nil), ErrExecutionUnavailable)
	require.ErrorIs(t, (*Executor)(nil).Execute(t.Context(), &RequestContext{Protocol: ProtocolHTTP}), ErrExecutionUnavailable)
}
