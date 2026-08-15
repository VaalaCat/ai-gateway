package genericapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type blockingRemoteRequestBody struct {
	closed chan struct{}
	once   sync.Once
}

func newBlockingRemoteRequestBody() *blockingRemoteRequestBody {
	return &blockingRemoteRequestBody{closed: make(chan struct{})}
}

func (b *blockingRemoteRequestBody) Read([]byte) (int, error) {
	<-b.closed
	return 0, errors.New("request body closed after remote result")
}

func (b *blockingRemoteRequestBody) Close() error {
	b.once.Do(func() { close(b.closed) })
	return nil
}

type scriptedRemoteHTTPStream struct {
	events          []app.APIResponseEvent
	resultDelivered chan struct{}
	resultOnce      sync.Once
	canceled        chan struct{}
	cancelOnce      sync.Once
	sendErr         error
}

func newScriptedRemoteHTTPStream(events ...app.APIResponseEvent) *scriptedRemoteHTTPStream {
	return &scriptedRemoteHTTPStream{
		events: events, resultDelivered: make(chan struct{}), canceled: make(chan struct{}),
	}
}

func (*scriptedRemoteHTTPStream) Open(context.Context, app.APIOpen) error { return nil }
func (s *scriptedRemoteHTTPStream) SendRequestData(context.Context, []byte) error {
	return s.sendErr
}
func (*scriptedRemoteHTTPStream) EndRequest(context.Context, wire.Trailers) error { return nil }
func (s *scriptedRemoteHTTPStream) Receive(ctx context.Context) (app.APIResponseEvent, error) {
	if len(s.events) > 0 {
		event := s.events[0]
		s.events = s.events[1:]
		if event.Kind == app.APIResponseResult {
			s.resultOnce.Do(func() { close(s.resultDelivered) })
		}
		return event, nil
	}
	select {
	case <-s.canceled:
		return app.APIResponseEvent{}, context.Canceled
	case <-ctx.Done():
		return app.APIResponseEvent{}, context.Cause(ctx)
	}
}
func (s *scriptedRemoteHTTPStream) Cancel(error) {
	s.cancelOnce.Do(func() { close(s.canceled) })
}
func (*scriptedRemoteHTTPStream) Close() error { return nil }

func TestRemoteHTTPImmediateResultWinsOverBlockedRequestSender(t *testing.T) {
	body := newBlockingRemoteRequestBody()
	request := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/v1/api/weather/current", body)
	result := apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true,
		RateLimitDecision:     "rejected",
	}
	stream := newScriptedRemoteHTTPStream(
		app.APIResponseEvent{Kind: app.APIResponseHeaders, Headers: &wire.Headers{StatusCode: http.StatusTooManyRequests}},
		app.APIResponseEvent{Kind: app.APIResponseEnd, Trailers: &wire.Trailers{}},
		app.APIResponseEvent{Kind: app.APIResponseResult, Result: &result},
	)
	w := httptest.NewRecorder()
	type outcome struct {
		result *apiattempt.APIExecutionResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		got, err := runRemoteHTTPStream(t.Context(), stream, request, w)
		done <- outcome{result: got, err: err}
	}()
	<-stream.resultDelivered

	returnedBeforeManualClose := true
	var got outcome
	select {
	case got = <-done:
	case <-time.After(100 * time.Millisecond):
		returnedBeforeManualClose = false
		require.NoError(t, body.Close())
		got = <-done
	}
	require.True(t, returnedBeforeManualClose, "terminal Result must stop a still-blocked request sender")
	require.NoError(t, got.err)
	require.Equal(t, &result, got.result)
	require.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestRemoteHTTPNoResultReturnsGenuineSenderError(t *testing.T) {
	senderErr := errors.New("request upload failed")
	stream := newScriptedRemoteHTTPStream()
	stream.sendErr = senderErr
	request := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/v1/api/weather/current", io.NopCloser(&oneByteReader{}))

	result, err := runRemoteHTTPStream(t.Context(), stream, request, httptest.NewRecorder())
	require.Nil(t, result)
	require.ErrorIs(t, err, senderErr)
}

type oneByteReader struct{ read bool }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	p[0] = 'x'
	return 1, nil
}

func testGinContext(t *testing.T, request *http.Request) *gin.Context {
	t.Helper()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = request
	return context
}

type unavailableDirectAPIBuilder struct{ builds int }

func (b *unavailableDirectAPIBuilder) BuildDirectHTTPAPITransport(context.Context, agentproxy.DirectSessionTarget) (agentproxy.DirectHTTPAPITransport, error) {
	b.builds++
	return nil, errors.New("direct unavailable before open")
}

type recordingRelayAPIOpener struct {
	calls int
	open  app.APIOpen
	err   error
}

func (o *recordingRelayAPIOpener) OpenHTTPAPIStream(_ context.Context, open app.APIOpen) (app.HTTPAPIStream, error) {
	o.calls++
	o.open = open
	return nil, o.err
}

func TestRemoteHTTPDirectPreOpenFailureFallsBackToRelayForSameFrozenAgent(t *testing.T) {
	direct := &unavailableDirectAPIBuilder{}
	relay := &recordingRelayAPIOpener{err: errors.New("relay unavailable")}
	handler := NewRemoteHTTPHandler(RemoteHTTPHandlerOptions{Direct: direct, Relay: relay, AddressTag: "direct"})
	requestURL, err := url.Parse("http://gateway.invalid/v1/api/weather/current")
	require.NoError(t, err)
	rc := &RequestContext{
		Context: testGinContext(t, &http.Request{Method: http.MethodGet, URL: requestURL, Header: make(http.Header)}),
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9},
		Protocol: ProtocolHTTP, RequestID: "request", Agent: AgentPick{
			ExecutionAgentID: "target-a", AgentRouteID: 42,
			Target: models.Agent{AgentID: "target-a", Status: 1, HTTPAddresses: `[{"url":"http://target-a.invalid:8080","tag":"direct"}]`, DirectInboundEnabled: true, RelayInboundEnabled: true},
		},
	}

	require.Error(t, handler.Serve(t.Context(), rc))
	require.Equal(t, 1, direct.builds)
	require.Equal(t, 1, relay.calls)
	require.Equal(t, "target-a", relay.open.TargetAgentID)
	require.Equal(t, uint(42), relay.open.RouteID)
	require.Equal(t, app.RoutePathRelay, rc.Agent.AgentRoutePath)
}
