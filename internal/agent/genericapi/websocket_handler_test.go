package genericapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	relaylimiter "github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

type capturedWebSocketHandshake struct {
	clientKey   atomic.Value
	providerKey atomic.Value
	protocols   atomic.Value
	extensions  atomic.Value
}

type postUpgradeWriteSpy struct {
	http.ResponseWriter
	hijacked   atomic.Bool
	writeCalls atomic.Int32
}

func (w *postUpgradeWriteSpy) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	connection, readWriter, err := w.ResponseWriter.(http.Hijacker).Hijack()
	if err == nil {
		w.hijacked.Store(true)
	}
	return connection, readWriter, err
}

func (w *postUpgradeWriteSpy) WriteHeader(statusCode int) {
	if w.hijacked.Load() {
		w.writeCalls.Add(1)
	}
	w.ResponseWriter.WriteHeader(statusCode)
}

func (w *postUpgradeWriteSpy) Write(data []byte) (int, error) {
	if w.hijacked.Load() {
		w.writeCalls.Add(1)
	}
	return w.ResponseWriter.Write(data)
}

type lifecycleWebSocketPermit struct{ releases atomic.Int32 }

func (p *lifecycleWebSocketPermit) Release() { p.releases.Add(1) }

type lifecycleWebSocketLimiter struct {
	permit *lifecycleWebSocketPermit
	calls  atomic.Int32
}

func (l *lifecycleWebSocketLimiter) Acquire(context.Context, APIRequestFacts) (APIPermit, error) {
	l.calls.Add(1)
	return l.permit, nil
}

type lifecycleWebSocketUsageBuilder struct {
	calls      atomic.Int32
	executions chan APIExecution
}

func (b *lifecycleWebSocketUsageBuilder) Build(execution APIExecution) protocol.APIUsageEntry {
	b.calls.Add(1)
	b.executions <- execution
	return protocol.APIUsageEntry{RequestID: execution.Request.RequestID}
}

type lifecycleWebSocketReporter struct {
	calls     atomic.Int32
	completed chan struct{}
}

func (r *lifecycleWebSocketReporter) EnqueueAPI(protocol.APIUsageEntry) error {
	r.calls.Add(1)
	if r.completed != nil {
		select {
		case r.completed <- struct{}{}:
		default:
		}
	}
	return nil
}

type capturedWebSocketUsageReporter struct {
	calls   atomic.Int32
	entries chan protocol.APIUsageEntry
}

func (r *capturedWebSocketUsageReporter) EnqueueAPI(entry protocol.APIUsageEntry) error {
	r.calls.Add(1)
	r.entries <- entry
	return nil
}

type closeTrackingReadCloser struct {
	io.ReadCloser
	closes *atomic.Int32
}

func (body *closeTrackingReadCloser) Close() error {
	body.closes.Add(1)
	return body.ReadCloser.Close()
}

type closeTrackingWebSocketDialer struct {
	dialer websocket.Dialer
	closes *atomic.Int32
}

func (d *closeTrackingWebSocketDialer) DialContext(
	ctx context.Context,
	url string,
	header http.Header,
) (*websocket.Conn, *http.Response, error) {
	connection, response, err := d.dialer.DialContext(ctx, url, header)
	if response != nil && response.Body != nil {
		response.Body = &closeTrackingReadCloser{ReadCloser: response.Body, closes: d.closes}
	}
	return connection, response, err
}

type fixedWebSocketRejectionDialer struct {
	status int
	header http.Header
	body   []byte
	calls  atomic.Int32
	closes atomic.Int32
}

type deadlineRecordingWebSocketDialer struct {
	hasDeadline atomic.Bool
	deadline    atomic.Int64
}

type safeErrorWebSocketDialer struct{}

func (safeErrorWebSocketDialer) DialContext(_ context.Context, target string, _ http.Header) (*websocket.Conn, *http.Response, error) {
	return nil, nil, &url.Error{Op: "dial", URL: target, Err: errors.New("connection refused")}
}

func (d *deadlineRecordingWebSocketDialer) DialContext(ctx context.Context, _ string, _ http.Header) (*websocket.Conn, *http.Response, error) {
	deadline, hasDeadline := ctx.Deadline()
	d.hasDeadline.Store(hasDeadline)
	if hasDeadline {
		d.deadline.Store(deadline.UnixNano())
	}
	return nil, nil, errors.New("provider dial failed")
}

func (d *fixedWebSocketRejectionDialer) DialContext(
	context.Context,
	string,
	http.Header,
) (*websocket.Conn, *http.Response, error) {
	d.calls.Add(1)
	body := &closeTrackingReadCloser{
		ReadCloser: io.NopCloser(bytes.NewReader(d.body)),
		closes:     &d.closes,
	}
	return nil, &http.Response{
		StatusCode:    d.status,
		Status:        fmt.Sprintf("%d provider rejection", d.status),
		Header:        d.header.Clone(),
		Body:          body,
		ContentLength: int64(len(d.body)),
	}, websocket.ErrBadHandshake
}

type localWebSocketPicker struct {
	lease     *APIUpstreamLease
	err       error
	calls     atomic.Int32
	backendID atomic.Uint32
}

type remoteWebSocketAgentPicker struct{}

func (remoteWebSocketAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	return AgentPick{
		ExecutionAgentID: "target-agent", AgentRouteID: 17,
		Target: models.Agent{AgentID: "target-agent", RelayInboundEnabled: true},
	}, nil
}

type allowExecutionCapabilityFinder struct{}

func (allowExecutionCapabilityFinder) SupportsGenericAPIExecution(string) bool { return true }

type echoWebSocketStream struct {
	events chan app.WebSocketEvent
	done   chan struct{}
}

type recordingRemoteWebSocketStream struct {
	acceptance          app.WebSocketAccepted
	result              apiattempt.APIExecutionResult
	resultContexts      chan recordedContext
	bridgeCloseContexts chan recordedContext
	stopBridgeOnMessage bool
	done                chan struct{}
	closeOnce           sync.Once
}

func newRecordingRemoteWebSocketStream() *recordingRemoteWebSocketStream {
	return &recordingRemoteWebSocketStream{
		acceptance: app.WebSocketAccepted{ProviderStatus: http.StatusSwitchingProtocols},
		result: apiattempt.APIExecutionResult{
			ProviderDispatchKnown: true, ProviderDispatched: true, UpstreamStatus: http.StatusSwitchingProtocols,
		},
		resultContexts: make(chan recordedContext, 1), bridgeCloseContexts: make(chan recordedContext, 1),
		stopBridgeOnMessage: true, done: make(chan struct{}),
	}
}

func (*recordingRemoteWebSocketStream) Open(context.Context, app.WebSocketOpen) (app.WebSocketAccepted, error) {
	return app.WebSocketAccepted{}, ErrExecutionUnavailable
}

func (s *recordingRemoteWebSocketStream) ProviderAcceptance() app.WebSocketAccepted {
	return s.acceptance
}

func (s *recordingRemoteWebSocketStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
	if event.Kind == app.WebSocketCloseEvent {
		s.bridgeCloseContexts <- recordContext(ctx)
		return nil
	}
	if s.stopBridgeOnMessage && event.Kind == app.WebSocketMessageStartEvent {
		return errors.New("stop remote bridge")
	}
	return nil
}

func (s *recordingRemoteWebSocketStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
	select {
	case <-ctx.Done():
		return app.WebSocketEvent{}, context.Cause(ctx)
	case <-s.done:
		return app.WebSocketEvent{}, context.Canceled
	}
}

func (s *recordingRemoteWebSocketStream) ReceiveResult(ctx context.Context) (apiattempt.APIExecutionResult, error) {
	s.resultContexts <- recordContext(ctx)
	return s.result, nil
}

func (s *recordingRemoteWebSocketStream) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	return nil
}

type queuedRemoteWebSocketOpener struct {
	streams chan app.WebSocketAPIStream
}

func (o *queuedRemoteWebSocketOpener) OpenWebSocketAPIStream(
	ctx context.Context,
	_ app.WebSocketOpen,
) (app.WebSocketAPIStream, error) {
	select {
	case stream := <-o.streams:
		return stream, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func TestRemoteWebSocketResultAndBridgeUseCurrentControlTimeout(t *testing.T) {
	tests := []struct {
		name     string
		finder   SettingsFinder
		timeout  time.Duration
		deadline bool
	}{
		{name: "default remains five seconds", timeout: 5 * time.Second, deadline: true},
		{name: "hot snapshot uses twenty seven milliseconds", finder: &mutableAgentSettingsFinder{settings: settings.AgentSettings{APIWebSocketControlWriteTimeoutMs: 27}}, timeout: 27 * time.Millisecond, deadline: true},
		{name: "zero disables deadline", finder: &mutableAgentSettingsFinder{}, deadline: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opener := &queuedRemoteWebSocketOpener{streams: make(chan app.WebSocketAPIStream, 1)}
			handler := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Relay: opener, Settings: test.finder})
			stream := newRecordingRemoteWebSocketStream()
			opener.streams <- stream

			serveRemoteWebSocketStream(t, handler)
			for _, got := range []recordedContext{<-stream.resultContexts, <-stream.bridgeCloseContexts} {
				if test.deadline {
					requireContextTimeout(t, got, test.timeout)
					continue
				}
				require.False(t, got.hasDeadline)
				require.True(t, got.cancellable)
			}
		})
	}
}

func TestRemoteWebSocketRejectionResultUsesCurrentControlTimeout(t *testing.T) {
	tests := []struct {
		name     string
		finder   SettingsFinder
		timeout  time.Duration
		deadline bool
	}{
		{name: "default remains five seconds", timeout: 5 * time.Second, deadline: true},
		{name: "hot snapshot uses twenty seven milliseconds", finder: &mutableAgentSettingsFinder{settings: settings.AgentSettings{APIWebSocketControlWriteTimeoutMs: 27}}, timeout: 27 * time.Millisecond, deadline: true},
		{name: "zero disables deadline", finder: &mutableAgentSettingsFinder{}, deadline: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := newRecordingRemoteWebSocketStream()
			stream.acceptance = app.WebSocketAccepted{
				ProviderStatus: http.StatusBadGateway,
				Rejection:      &app.WebSocketRejection{StatusCode: http.StatusBadGateway},
			}
			stream.result.UpstreamStatus = http.StatusBadGateway
			opener := &queuedRemoteWebSocketOpener{streams: make(chan app.WebSocketAPIStream, 1)}
			opener.streams <- stream
			handler := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Relay: opener, Settings: test.finder})

			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil)
			err := handler.Serve(t.Context(), newRemoteWebSocketRequestContext(ginContext))
			require.NoError(t, err)
			got := <-stream.resultContexts
			if test.deadline {
				requireContextTimeout(t, got, test.timeout)
				return
			}
			require.False(t, got.hasDeadline)
			require.True(t, got.cancellable)
		})
	}
}

func TestRemoteWebSocketRejectionPreservesExecutionErrorMessage(t *testing.T) {
	stream := newRecordingRemoteWebSocketStream()
	stream.acceptance = app.WebSocketAccepted{
		ProviderStatus: http.StatusBadGateway,
		Rejection:      &app.WebSocketRejection{StatusCode: http.StatusBadGateway},
	}
	stream.result.UpstreamStatus = http.StatusBadGateway
	stream.result.ErrorStage = "transport"
	stream.result.ErrorCode = CodeUnavailable
	stream.result.ErrorMessage = "dial tcp: connection refused"
	opener := &queuedRemoteWebSocketOpener{streams: make(chan app.WebSocketAPIStream, 1)}
	opener.streams <- stream
	handler := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Relay: opener})
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil)
	rc := newRemoteWebSocketRequestContext(ginContext)

	require.NoError(t, handler.Serve(t.Context(), rc))
	require.Equal(t, "dial tcp: connection refused", rc.Execution.ErrorMessage)
}

func serveRemoteWebSocketStream(t *testing.T, handler *RemoteWebSocketHandler) {
	t.Helper()
	serveResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ginContext, _ := gin.CreateTestContext(writer)
		ginContext.Request = request
		serveResult <- handler.Serve(request.Context(), newRemoteWebSocketRequestContext(ginContext))
	}))
	t.Cleanup(server.Close)

	client, _, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(server.URL, "http")+"/socket", nil,
	)
	require.NoError(t, err)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("stop")))
	_, _, _ = client.ReadMessage()
	_ = client.Close()
	select {
	case err = <-serveResult:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("remote WebSocket handler did not return")
	}
}

func newRemoteWebSocketRequestContext(ginContext *gin.Context) *RequestContext {
	return &RequestContext{
		Context: ginContext, RequestID: "remote-request", Protocol: ProtocolWebSocket,
		Service: protocol.SyncedAPIService{ID: 7},
		Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Protocols: []string{ProtocolWebSocket}},
		Agent: AgentPick{
			ExecutionAgentID: "target-agent",
			Target:           models.Agent{AgentID: "target-agent", RelayInboundEnabled: true},
		},
	}
}

func newEchoWebSocketStream() *echoWebSocketStream {
	return &echoWebSocketStream{events: make(chan app.WebSocketEvent, 16), done: make(chan struct{})}
}

func (*echoWebSocketStream) Open(context.Context, app.WebSocketOpen) (app.WebSocketAccepted, error) {
	return app.WebSocketAccepted{
		RequestWindow: 1 << 20, ProviderStatus: http.StatusSwitchingProtocols,
	}, nil
}

func (*echoWebSocketStream) ProviderAcceptance() app.WebSocketAccepted {
	return app.WebSocketAccepted{RequestWindow: 1 << 20, ProviderStatus: http.StatusSwitchingProtocols}
}

func (s *echoWebSocketStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
	select {
	case s.events <- event:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-s.done:
		return context.Canceled
	}
}

func (s *echoWebSocketStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
	select {
	case event := <-s.events:
		return event, nil
	case <-ctx.Done():
		return app.WebSocketEvent{}, context.Cause(ctx)
	case <-s.done:
		return app.WebSocketEvent{}, context.Canceled
	}
}

func (*echoWebSocketStream) ReceiveResult(context.Context) (apiattempt.APIExecutionResult, error) {
	return apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true, ProviderDispatched: true, UpstreamStatus: http.StatusSwitchingProtocols,
	}, nil
}

func (s *echoWebSocketStream) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

type echoWebSocketOpener struct {
	stream *echoWebSocketStream
	calls  atomic.Int32
	open   app.WebSocketOpen
}

type failingDirectWebSocketOpener struct {
	calls atomic.Int32
	err   error
}

func (o *failingDirectWebSocketOpener) OpenWebSocketAPIStream(
	context.Context,
	agentproxy.DirectSessionTarget,
	app.WebSocketOpen,
) (app.WebSocketAPIStream, error) {
	o.calls.Add(1)
	return nil, o.err
}

type recordingDirectWebSocketOpener struct {
	calls  atomic.Int32
	stream app.WebSocketAPIStream
}

func (o *recordingDirectWebSocketOpener) OpenWebSocketAPIStream(
	context.Context,
	agentproxy.DirectSessionTarget,
	app.WebSocketOpen,
) (app.WebSocketAPIStream, error) {
	o.calls.Add(1)
	return o.stream, nil
}

func (o *echoWebSocketOpener) OpenWebSocketAPIStream(_ context.Context, open app.WebSocketOpen) (app.WebSocketAPIStream, error) {
	o.calls.Add(1)
	o.open = open
	return o.stream, nil
}

func (p *localWebSocketPicker) Pick(backendID uint, _ apiattempt.APIProtocol, _ string) (*APIUpstreamLease, error) {
	p.calls.Add(1)
	p.backendID.Store(uint32(backendID))
	return p.lease, p.err
}

func TestWebSocketHandlerPicksTheRouteBackendAndReportsNoCandidateAsUnavailable(t *testing.T) {
	picker := &localWebSocketPicker{err: unavailableUpstream("no candidates for route backend")}
	handler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/radar", nil)
	rc := &RequestContext{
		Context: ginContext, Service: protocol.SyncedAPIService{ID: 7},
		Route: protocol.SyncedAPIRoute{ID: 9, BackendID: 20}, Protocol: ProtocolWebSocket, RequestID: "radar-request",
	}

	err := handler.Serve(t.Context(), rc)

	require.Error(t, err)
	require.Equal(t, uint32(20), picker.backendID.Load())
	require.Equal(t, int32(1), picker.calls.Load(), "one WebSocket connection must keep one frozen upstream lease")
	require.Equal(t, "upstream_pick", rc.Execution.ErrorStage)
	require.Equal(t, CodeUnavailable, rc.Execution.ErrorCode)
}

func TestWebSocketHandlerStoresSafeDialErrorMessage(t *testing.T) {
	const secret = "provider-secret"
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "query-auth", BaseURL: "https://upstream.example/socket", AuthType: "query",
			Credential: protocol.APIUpstreamCredential{QueryName: "token", QueryValue: secret},
		},
		permit: newLocalHTTPPermit(),
	}}
	handler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker, Dialer: safeErrorWebSocketDialer{}})
	rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil))
	rc.Protocol = ProtocolWebSocket

	err := handler.Serve(t.Context(), rc)

	require.Error(t, err)
	require.Equal(t, "transport", rc.Execution.ErrorStage)
	require.Contains(t, rc.Execution.ErrorMessage, "connection refused")
	require.NotContains(t, rc.Execution.ErrorMessage, secret)
	require.NotContains(t, rc.Execution.ErrorMessage, "upstream.example")
}

func TestWebSocketHandlerStoresPickerErrorMessageWithoutCredential(t *testing.T) {
	picker := &localWebSocketPicker{err: errors.New("no websocket upstream candidates")}
	handler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})
	rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil))
	rc.Protocol = ProtocolWebSocket

	err := handler.Serve(t.Context(), rc)

	require.EqualError(t, err, "no websocket upstream candidates")
	require.Equal(t, "no websocket upstream candidates", rc.Execution.ErrorMessage)
}

func TestWebSocketHandlerStoresClientUpgradeErrorMessage(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err == nil {
			_ = connection.Close()
		}
	}))
	t.Cleanup(provider.Close)
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "provider", BaseURL: provider.URL, AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	handler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})
	rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil))
	rc.Protocol = ProtocolWebSocket

	err := handler.Serve(t.Context(), rc)

	require.Error(t, err)
	require.Equal(t, "client_upgrade", rc.Execution.ErrorStage)
	require.NotEmpty(t, rc.Execution.ErrorMessage)
}

func TestWebSocketHandlerReadsLatestHandshakeTimeoutForEachDial(t *testing.T) {
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{permit: newLocalHTTPPermit(), Upstream: protocol.SyncedAPIUpstream{
		ID: 11, BackendID: 7, Name: "upstream", BaseURL: "https://upstream.example", AuthType: "none", Status: 1, Weight: 1,
	}}}
	dialer := &deadlineRecordingWebSocketDialer{}
	settingsFinder := &mutableAgentSettingsFinder{}
	handler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker, Dialer: dialer, Settings: settingsFinder})

	for _, timeout := range []int{15, 35} {
		settingsFinder.Update(settings.AgentSettings{APIWebSocketHandshakeTimeoutMs: timeout})
		recorder := httptest.NewRecorder()
		ginContext, _ := gin.CreateTestContext(recorder)
		ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil)
		err := handler.Serve(t.Context(), &RequestContext{
			Context: ginContext, Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9},
			Protocol: ProtocolWebSocket, RequestID: "request-1",
		})
		require.Error(t, err)
		require.True(t, dialer.hasDeadline.Load())
		remaining := time.Until(time.Unix(0, dialer.deadline.Load()))
		require.Greater(t, remaining, time.Duration(timeout-10)*time.Millisecond)
		require.LessOrEqual(t, remaining, time.Duration(timeout+10)*time.Millisecond)
	}

	settingsFinder.Update(settings.AgentSettings{APIWebSocketHandshakeTimeoutMs: 0})
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = httptest.NewRequest(http.MethodGet, "http://gateway.invalid/socket", nil)
	require.Error(t, handler.Serve(t.Context(), &RequestContext{Context: ginContext, Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9}, Protocol: ProtocolWebSocket, RequestID: "request-2"}))
	require.False(t, dialer.hasDeadline.Load())
}

// Removing the WebSocket protocol registration, the client upgrade, the
// provider dial, or either bridge copy direction must break this real-socket
// test at the public Generic API Gin route.
func TestWebSocketLocalEcho(t *testing.T) {
	var providerDials atomic.Int32
	providerDone := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerDials.Add(1)
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer close(providerDone)
		defer connection.Close()
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		_ = connection.WriteMessage(messageType, payload)
	}))
	t.Cleanup(provider.Close)

	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "echo", BaseURL: provider.URL, AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	webSocketHandler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &order,
			route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket},
		},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: webSocketHandler},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	clientRequestID := "client-local-websocket-id"
	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live",
		http.Header{consts.HeaderXRequestID: []string{clientRequestID}},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.NotEmpty(t, response.Header.Get(consts.HeaderXRequestID))
	require.NotEqual(t, clientRequestID, response.Header.Get(consts.HeaderXRequestID))
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("hello websocket")))
	messageType, payload, err := client.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("hello websocket"), payload)
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, int32(1), providerDials.Load())
	require.NoError(t, client.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"), time.Now().Add(time.Second)))
	select {
	case <-providerDone:
	case <-time.After(3 * time.Second):
		t.Fatal("provider websocket did not close")
	}
}

func TestWebSocketRemoteSessionProviderE2E(t *testing.T) {
	var providerDials atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerDials.Add(1)
		upgrader := websocket.Upgrader{
			EnableCompression: false, Subprotocols: []string{"chat.v1"},
			CheckOrigin: func(*http.Request) bool { return true },
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			messageType, payload, readErr := connection.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := connection.WriteMessage(messageType, payload); writeErr != nil {
				return
			}
		}
	}))
	t.Cleanup(provider.Close)

	upstreamPicker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "remote-provider", BaseURL: provider.URL, AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		WebSocketSubprotocols: []string{"chat.v1"},
	}
	targetHandler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: ServiceRoute{
			Service: service, Route: route, Protocol: ProtocolWebSocket,
		}},
		Picker: upstreamPicker,
	})
	sourceConnection, targetConnection := webSocketConnectionPair(t)
	limits := wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 32 << 10, InitialStreamWindow: 1 << 20,
		MaxQueuedSessionBytes: 2 << 20, MaxConcurrentStreams: 8,
	}
	sourceSession := agenttunnel.NewSession(sourceConnection, 1, limits, agenttunnel.SessionOptions{
		Direction: agenttunnel.SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	targetSession := agenttunnel.NewSession(targetConnection, 2, limits, agenttunnel.SessionOptions{
		Direction: agenttunnel.SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
		WebSocketTargetHandler: targetHandler,
	})
	sessionCtx, cancelSessions := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancelSessions()
		sourceSession.Cancel(context.Canceled)
		targetSession.Cancel(context.Canceled)
	})
	go func() { _ = sourceSession.Run(sessionCtx) }()
	go func() { _ = targetSession.Run(sessionCtx) }()

	usage := &lifecycleWebSocketUsageBuilder{executions: make(chan APIExecution, 1)}
	reporter := &lifecycleWebSocketReporter{}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{order: &order, route: ServiceRoute{
			Service: service, Route: route, Protocol: ProtocolWebSocket,
		}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: remoteWebSocketAgentPicker{},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Usage:                 usage, Reporter: reporter, SourceAgentID: "source-agent",
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{
			Relay: sourceSession,
		})},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{"chat.v1"}
	clientRequestID := "client-remote-websocket-id"
	client, response, err := dialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live",
		http.Header{consts.HeaderXRequestID: []string{clientRequestID}},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	responseRequestID := response.Header.Get(consts.HeaderXRequestID)
	require.NotEmpty(t, responseRequestID)
	require.NotEqual(t, clientRequestID, responseRequestID)
	require.Equal(t, "chat.v1", client.Subprotocol())
	require.NoError(t, client.SetReadDeadline(time.Now().Add(5*time.Second)))

	messages := []struct {
		messageType int
		payload     []byte
	}{
		{messageType: websocket.TextMessage, payload: []byte("remote text")},
		{messageType: websocket.TextMessage, payload: nil},
		{messageType: websocket.BinaryMessage, payload: []byte{0, 1, 2, 255}},
		{messageType: websocket.BinaryMessage, payload: make([]byte, 256<<10)},
	}
	for index := range messages[3].payload {
		messages[3].payload[index] = byte(index % 251)
	}
	for _, message := range messages {
		require.NoError(t, client.WriteMessage(message.messageType, message.payload))
		messageType, payload, readErr := client.ReadMessage()
		require.NoError(t, readErr)
		require.Equal(t, message.messageType, messageType)
		if len(message.payload) == 0 {
			require.Empty(t, payload)
		} else {
			require.Equal(t, message.payload, payload)
		}
	}
	require.NoError(t, client.WriteControl(
		websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
		time.Now().Add(time.Second),
	))
	_ = client.Close()

	var execution APIExecution
	select {
	case execution = <-usage.executions:
	case <-time.After(5 * time.Second):
		t.Fatal("remote WebSocket usage did not finalize after terminal result")
	}
	require.Equal(t, uint(11), execution.Result.APIUpstreamID)
	require.Equal(t, "remote-provider", execution.Result.APIUpstreamName)
	require.Equal(t, http.StatusSwitchingProtocols, execution.Result.UpstreamStatus)
	require.True(t, execution.Result.ProviderDispatchKnown)
	require.True(t, execution.Result.ProviderDispatched)
	require.Equal(t, websocket.CloseNormalClosure, execution.Result.WebSocketCloseCode)
	require.Empty(t, execution.Result.ErrorMessage)
	require.Equal(t, http.StatusSwitchingProtocols, execution.StatusCode)
	require.Equal(t, execution.Request.RequestID, responseRequestID)
	require.Equal(t, int32(1), providerDials.Load())
	require.Equal(t, int32(1), usage.calls.Load())
	require.Equal(t, int32(1), reporter.calls.Load())
}

type remoteWebSocketE2EFixture struct {
	gateway  *httptest.Server
	reporter *capturedWebSocketUsageReporter
}

func newRemoteWebSocketE2EFixture(
	t *testing.T,
	providerURL string,
	dialer WebSocketDialer,
	limitOverrides ...wire.Limits,
) remoteWebSocketE2EFixture {
	t.Helper()
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		WebSocketSubprotocols: []string{"chat.v1"},
	}
	upstreamPicker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "remote-provider", BaseURL: providerURL, AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	targetHandler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: ServiceRoute{
			Service: service, Route: route, Protocol: ProtocolWebSocket,
		}},
		Picker: upstreamPicker, Dialer: dialer,
	})
	sourceConnection, targetConnection := webSocketConnectionPair(t)
	limits := wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 32 << 10, InitialStreamWindow: 1 << 20,
		MaxQueuedSessionBytes: 2 << 20, MaxConcurrentStreams: 8,
	}
	if len(limitOverrides) > 0 {
		limits = limitOverrides[0]
	}
	sourceSession := agenttunnel.NewSession(sourceConnection, 1, limits, agenttunnel.SessionOptions{
		Direction: agenttunnel.SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
	})
	targetSession := agenttunnel.NewSession(targetConnection, 2, limits, agenttunnel.SessionOptions{
		Direction: agenttunnel.SessionDirectionRelay, PingInterval: time.Hour, PongTimeout: time.Hour,
		WebSocketTargetHandler: targetHandler,
	})
	sessionCtx, cancelSessions := context.WithCancel(t.Context())
	t.Cleanup(func() {
		cancelSessions()
		sourceSession.Cancel(context.Canceled)
		targetSession.Cancel(context.Canceled)
	})
	go func() { _ = sourceSession.Run(sessionCtx) }()
	go func() { _ = targetSession.Run(sessionCtx) }()

	reporter := &capturedWebSocketUsageReporter{entries: make(chan protocol.APIUsageEntry, 1)}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{order: &order, route: ServiceRoute{
			Service: service, Route: route, Protocol: ProtocolWebSocket,
		}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: remoteWebSocketAgentPicker{},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Usage:                 NewUsageBuilder(func() time.Time { return time.Unix(1_700_000_000, 0) }),
		Reporter:              reporter, SourceAgentID: "source-agent",
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{
			Relay: sourceSession,
		})},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)
	return remoteWebSocketE2EFixture{gateway: gateway, reporter: reporter}
}

func (f remoteWebSocketE2EFixture) receiveUsage(t *testing.T) protocol.APIUsageEntry {
	t.Helper()
	select {
	case entry := <-f.reporter.entries:
		return entry
	case <-time.After(5 * time.Second):
		t.Fatal("remote WebSocket usage did not finalize")
		return protocol.APIUsageEntry{}
	}
}

func TestWebSocketRemoteSessionProviderAcceptanceBoundaries(t *testing.T) {
	t.Run("provider accepts without subprotocol", func(t *testing.T) {
		var providerDials atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			providerDials.Add(1)
			upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err != nil {
				return
			}
			defer connection.Close()
			for {
				messageType, payload, readErr := connection.ReadMessage()
				if readErr != nil {
					return
				}
				if writeErr := connection.WriteMessage(messageType, payload); writeErr != nil {
					return
				}
			}
		}))
		t.Cleanup(provider.Close)
		fixture := newRemoteWebSocketE2EFixture(t, provider.URL, nil)
		dialer := *websocket.DefaultDialer
		dialer.Subprotocols = []string{"chat.v1"}
		client, response, err := dialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(fixture.gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		require.NoError(t, err)
		require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
		require.Empty(t, client.Subprotocol(), "Source must not invent the allowed subprotocol when provider selected none")
		require.NoError(t, client.WriteControl(
			websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
			time.Now().Add(time.Second),
		))
		_ = client.Close()
		entry := fixture.receiveUsage(t)
		require.Equal(t, uint(11), entry.APIUpstreamID)
		require.Equal(t, "remote-provider", entry.UpstreamName)
		require.Equal(t, http.StatusSwitchingProtocols, entry.StatusCode)
		require.True(t, entry.ProviderDispatchKnown)
		require.True(t, entry.ProviderDispatched)
		require.Equal(t, int32(1), providerDials.Load())
		require.Equal(t, int32(1), fixture.reporter.calls.Load())
	})

	t.Run("provider rejects upgrade", func(t *testing.T) {
		var providerDials atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			providerDials.Add(1)
			writer.Header().Set("Connection", "X-Provider-Hop")
			writer.Header().Set("X-Provider-Hop", "secret")
			writer.Header().Set("Upgrade", "websocket")
			writer.Header().Set("Sec-WebSocket-Accept", "forged")
			writer.Header().Set("X-Provider-Reject", "yes")
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte("upgrade rejected"))
		}))
		t.Cleanup(provider.Close)
		var providerBodyCloses atomic.Int32
		providerDialer := *websocket.DefaultDialer
		fixture := newRemoteWebSocketE2EFixture(t, provider.URL, &closeTrackingWebSocketDialer{
			dialer: providerDialer, closes: &providerBodyCloses,
		})
		dialer := *websocket.DefaultDialer
		dialer.Subprotocols = []string{"chat.v1"}
		client, response, err := dialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(fixture.gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		if client != nil {
			_ = client.Close()
		}
		require.Error(t, err)
		require.NotNil(t, response)
		defer response.Body.Close()
		require.Equal(t, http.StatusBadGateway, response.StatusCode)
		require.Equal(t, "yes", response.Header.Get("X-Provider-Reject"))
		require.Empty(t, response.Header.Get("Connection"))
		require.Empty(t, response.Header.Get("X-Provider-Hop"))
		require.Empty(t, response.Header.Get("Upgrade"))
		require.Empty(t, response.Header.Get("Sec-WebSocket-Accept"))
		body, readErr := io.ReadAll(response.Body)
		require.NoError(t, readErr)
		require.Equal(t, []byte("upgrade rejected"), body)
		entry := fixture.receiveUsage(t)
		require.Equal(t, uint(11), entry.APIUpstreamID)
		require.Equal(t, "remote-provider", entry.UpstreamName)
		require.Equal(t, http.StatusBadGateway, entry.StatusCode)
		require.True(t, entry.ProviderDispatchKnown)
		require.True(t, entry.ProviderDispatched)
		require.NotEmpty(t, entry.ErrorStage)
		require.NotEmpty(t, entry.ErrorCode)
		require.Equal(t, int32(1), providerDials.Load())
		require.Equal(t, int32(1), providerBodyCloses.Load())
		require.Equal(t, int32(1), fixture.reporter.calls.Load())
	})

	t.Run("provider informational status fails closed", func(t *testing.T) {
		providerDialer := &fixedWebSocketRejectionDialer{
			status: http.StatusEarlyHints,
			header: http.Header{"X-Provider-Early": {"yes"}},
			body:   []byte("not a terminal response"),
		}
		fixture := newRemoteWebSocketE2EFixture(t, "http://provider.invalid", providerDialer)
		client, response, err := websocket.DefaultDialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(fixture.gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		if client != nil {
			_ = client.Close()
		}
		require.Error(t, err)
		require.NotNil(t, response)
		defer response.Body.Close()
		require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
		require.Empty(t, response.Header.Get("X-Provider-Early"))
		entry := fixture.receiveUsage(t)
		require.Equal(t, http.StatusServiceUnavailable, entry.StatusCode)
		require.Equal(t, "remote-provider", entry.UpstreamName)
		require.Equal(t, int32(1), providerDialer.calls.Load())
		require.Equal(t, int32(1), providerDialer.closes.Load())
		require.Equal(t, int32(1), fixture.reporter.calls.Load())
	})

	t.Run("large provider rejection fits negotiated metadata", func(t *testing.T) {
		limits := wire.Limits{
			MaxMetadataBytes: 4 << 10, MaxDataBytes: 4 << 10, InitialStreamWindow: 64 << 10,
			MaxQueuedSessionBytes: 256 << 10, MaxConcurrentStreams: 8,
		}
		header := make(http.Header)
		for index := 0; index < 48; index++ {
			header.Set(fmt.Sprintf("X-Provider-%03d", index), strings.Repeat("v", 96))
		}
		header.Set("Content-Encoding", "gzip")
		body := bytes.Repeat([]byte("b"), int(limits.MaxMetadataBytes/2))
		providerDialer := &fixedWebSocketRejectionDialer{
			status: http.StatusBadGateway,
			header: header,
			body:   body,
		}
		fixture := newRemoteWebSocketE2EFixture(
			t, "http://provider.invalid", providerDialer, limits,
		)
		client, response, err := websocket.DefaultDialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(fixture.gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		if client != nil {
			_ = client.Close()
		}
		require.Error(t, err)
		require.NotNil(t, response)
		defer response.Body.Close()
		require.Equal(t, http.StatusBadGateway, response.StatusCode)
		require.Equal(t, "true", response.Header.Get("X-Vaala-Websocket-Rejection-Headers-Truncated"))
		require.Equal(t, "true", response.Header.Get("X-Vaala-Websocket-Rejection-Body-Truncated"))
		require.Empty(t, response.Header.Get("Content-Encoding"))
		gotBody, readErr := io.ReadAll(response.Body)
		require.NoError(t, readErr)
		require.Less(t, len(gotBody), len(body))
		require.True(t, bytes.HasPrefix(body, gotBody))
		entry := fixture.receiveUsage(t)
		require.Equal(t, http.StatusBadGateway, entry.StatusCode)
		require.Equal(t, "remote-provider", entry.UpstreamName)
		require.Equal(t, int32(1), providerDialer.calls.Load())
		require.Equal(t, int32(1), providerDialer.closes.Load())
		require.Equal(t, int32(1), fixture.reporter.calls.Load())
	})
}

func TestWebSocketQueryCredentialLocalOverridesClientAndBaseQuery(t *testing.T) {
	providerRequest := make(chan []string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		providerRequest <- []string{request.URL.Path, query.Get("base"), query.Get("room"), query.Get("token")}
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err == nil {
			defer connection.Close()
			_ = connection.WriteControl(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, "done"),
				time.Now().Add(time.Second),
			)
		}
	}))
	t.Cleanup(provider.Close)

	upstream := protocol.SyncedAPIUpstream{
		ID: 11, BackendID: 7, Name: "query-auth", BaseURL: provider.URL + "/base?base=one&token=base", AuthType: "query",
	}
	upstream.Credential.QueryName = "token"
	upstream.Credential.QueryValue = "provider-secret"
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{Upstream: upstream, permit: newLocalHTTPPermit()}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1, UpstreamPath: "/socket",
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &order,
			route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket},
		},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(),
		"ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live?room=blue&token=client",
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	t.Cleanup(func() { _ = client.Close() })
	select {
	case got := <-providerRequest:
		require.Equal(t, []string{"/base/socket", "one", "blue", "provider-secret"}, got)
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not observe WebSocket request")
	}
}

func TestWebSocketDoesNotWriteHTTPAfterUpgradeBridgeError(t *testing.T) {
	abortProvider := make(chan struct{})
	providerConnected := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		close(providerConnected)
		<-abortProvider
		_ = connection.UnderlyingConn().Close()
	}))
	t.Cleanup(provider.Close)

	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "abrupt-close", BaseURL: provider.URL, AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &order,
			route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket},
		},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})},
	})
	router := genericAPIRouter(handler)
	spyResult := make(chan *postUpgradeWriteSpy, 1)
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		spy := &postUpgradeWriteSpy{ResponseWriter: writer}
		router.ServeHTTP(spy, request)
		spyResult <- spy
	}))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	<-providerConnected
	close(abortProvider)
	_, _, err = client.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, websocket.CloseInternalServerErr, closeErr.Code)
	_ = client.Close()
	select {
	case spy := <-spyResult:
		require.True(t, spy.hijacked.Load())
		require.Zero(t, spy.writeCalls.Load(), "generic handler wrote an HTTP error after status 101")
	case <-time.After(3 * time.Second):
		t.Fatal("gateway handler did not return after provider bridge error")
	}
}

func testWebSocketHandshakeSafety(t *testing.T) {
	captured := &capturedWebSocketHandshake{}
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured.providerKey.Store(request.Header.Get("Sec-WebSocket-Key"))
		captured.protocols.Store(request.Header.Get("Sec-WebSocket-Protocol"))
		captured.extensions.Store(request.Header.Get("Sec-WebSocket-Extensions"))
		upgrader := websocket.Upgrader{
			EnableCompression: false, Subprotocols: []string{"blocked", "chat.v1"},
			CheckOrigin: func(*http.Request) bool { return true },
		}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err == nil {
			defer connection.Close()
			_, _, _ = connection.ReadMessage()
		}
	}))
	t.Cleanup(provider.Close)

	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "subprotocol", BaseURL: provider.URL, AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		WebSocketSubprotocols: []string{"chat.v1"},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})},
	})
	router := genericAPIRouter(handler)
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured.clientKey.Store(request.Header.Get("Sec-WebSocket-Key"))
		router.ServeHTTP(writer, request)
	}))
	t.Cleanup(gateway.Close)

	dialer := *websocket.DefaultDialer
	dialer.EnableCompression = true
	dialer.Subprotocols = []string{"blocked", "chat.v1"}
	client, response, err := dialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Equal(t, "chat.v1", client.Subprotocol())
	require.Empty(t, response.Header.Get("Sec-WebSocket-Extensions"))
	require.NotEqual(t, captured.clientKey.Load(), captured.providerKey.Load())
	require.Equal(t, "chat.v1", captured.protocols.Load())
	require.Equal(t, "", captured.extensions.Load())
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("done")))
}

func TestWebSocketNegotiatesAllowedSubprotocolAndDoesNotCopyHandshakeKey(t *testing.T) {
	testWebSocketHandshakeSafety(t)
}

func TestWebSocketDoesNotNegotiatePerMessageDeflate(t *testing.T) {
	testWebSocketHandshakeSafety(t)
}

func TestWebSocketConcurrencyPermitLivesForWholeConnectionAndUsageIsOnce(t *testing.T) {
	providerConnected := make(chan struct{})
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		close(providerConnected)
		defer connection.Close()
		for {
			messageType, payload, readErr := connection.ReadMessage()
			if readErr != nil {
				return
			}
			if writeErr := connection.WriteMessage(messageType, payload); writeErr != nil {
				return
			}
		}
	}))
	t.Cleanup(provider.Close)

	sourcePermit, executionPermit := &lifecycleWebSocketPermit{}, &lifecycleWebSocketPermit{}
	sourceLimiter := &lifecycleWebSocketLimiter{permit: sourcePermit}
	executionLimiter := &lifecycleWebSocketLimiter{permit: executionPermit}
	usage := &lifecycleWebSocketUsageBuilder{executions: make(chan APIExecution, 1)}
	reporter := &lifecycleWebSocketReporter{completed: make(chan struct{}, 1)}
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "lifecycle", BaseURL: provider.URL, AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, Limiter: sourceLimiter,
		AgentPicker: fixedExecutionAgentPicker{pick: AgentPick{ExecutionAgentID: "source-agent"}},
		Usage:       usage, Reporter: reporter, SourceAgentID: "source-agent",
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{
			Picker: picker, Limiter: executionLimiter,
		})},
	})
	handlerReturned := make(chan struct{})
	var handlerReturnOnce sync.Once
	router := genericAPIRouter(handler)
	gateway := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		router.ServeHTTP(writer, request)
		handlerReturnOnce.Do(func() { close(handlerReturned) })
	}))
	t.Cleanup(gateway.Close)
	client, _, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	require.NoError(t, err)
	<-providerConnected
	require.Equal(t, int32(1), sourceLimiter.calls.Load())
	require.Equal(t, int32(1), executionLimiter.calls.Load())
	require.Zero(t, sourcePermit.releases.Load())
	require.Zero(t, executionPermit.releases.Load())
	require.Zero(t, usage.calls.Load())

	require.NoError(t, client.WriteControl(
		websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "finished"),
		time.Now().Add(time.Second),
	))
	_ = client.Close()
	var execution APIExecution
	select {
	case execution = <-usage.executions:
	case <-time.After(3 * time.Second):
		t.Fatal("usage was not finalized after WebSocket close")
	}
	select {
	case <-reporter.completed:
	case <-time.After(3 * time.Second):
		t.Fatal("usage was not reported after WebSocket close")
	}
	select {
	case <-handlerReturned:
	case <-time.After(3 * time.Second):
		t.Fatal("WebSocket handler did not return after client close")
	}
	require.Equal(t, websocket.CloseNormalClosure, execution.Result.WebSocketCloseCode)
	require.Equal(t, int32(1), sourcePermit.releases.Load())
	require.Equal(t, int32(1), executionPermit.releases.Load())
	require.Equal(t, int32(1), usage.calls.Load())
	require.Equal(t, int32(1), reporter.calls.Load())
}

func TestWebSocketNeverReconnectsAfterProviderOrTunnelClose(t *testing.T) {
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}

	t.Run("provider close", func(t *testing.T) {
		var providerDials atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			providerDials.Add(1)
			upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
			connection, err := upgrader.Upgrade(writer, request, nil)
			if err == nil {
				_ = connection.Close()
			}
		}))
		t.Cleanup(provider.Close)
		picker := &localWebSocketPicker{lease: &APIUpstreamLease{
			Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, BaseURL: provider.URL, AuthType: "none"},
			permit:   newLocalHTTPPermit(),
		}}
		order := []string{}
		handler := NewHandler(HandlerOptions{
			Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
			Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
			Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})},
		})
		gateway := httptest.NewServer(genericAPIRouter(handler))
		t.Cleanup(gateway.Close)
		client, _, err := websocket.DefaultDialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		require.NoError(t, err)
		_, _, err = client.ReadMessage()
		require.Error(t, err)
		_ = client.Close()
		require.Never(t, func() bool { return picker.calls.Load() > 1 || providerDials.Load() > 1 }, 100*time.Millisecond, 10*time.Millisecond)
		require.Equal(t, int32(1), picker.calls.Load())
		require.Equal(t, int32(1), providerDials.Load())
	})

	t.Run("tunnel close", func(t *testing.T) {
		stream := newEchoWebSocketStream()
		require.NoError(t, stream.Close())
		opener := &echoWebSocketOpener{stream: stream}
		order := []string{}
		handler := NewHandler(HandlerOptions{
			Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
			Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: remoteWebSocketAgentPicker{},
			// behavior change: Generic API execution capability is required before remote dispatch.
			ExecutionCapabilities: allowExecutionCapabilityFinder{},
			Handlers:              map[string]ProtocolHandler{ProtocolWebSocket: NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Relay: opener})},
		})
		gateway := httptest.NewServer(genericAPIRouter(handler))
		t.Cleanup(gateway.Close)
		client, _, err := websocket.DefaultDialer.DialContext(
			t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
		)
		require.NoError(t, err)
		_, _, err = client.ReadMessage()
		require.Error(t, err)
		_ = client.Close()
		require.Never(t, func() bool { return opener.calls.Load() > 1 }, 100*time.Millisecond, 10*time.Millisecond)
		require.Equal(t, int32(1), opener.calls.Load())
	})
}

func TestWebSocketRateLimitRejectsImmediatelyWithoutWait(t *testing.T) {
	store := &terminalWaitStore{
		tryConcurrency: func(relaylimiter.BucketKey) (func(), bool) { return func() {}, false },
		wake:           make(chan struct{}), waiterAdded: make(chan struct{}),
	}
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{}}
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, Limiter: NewLimiterGate(waitLimiterIndex(5_000), store),
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)
	startedAt := time.Now()
	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	if client != nil {
		_ = client.Close()
	}
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, http.StatusTooManyRequests, response.StatusCode)
	require.Less(t, time.Since(startedAt), 100*time.Millisecond)
	require.Zero(t, picker.calls.Load())
	select {
	case <-store.waiterAdded:
		t.Fatal("WebSocket source limiter registered a waiter")
	default:
	}
}

// Replacing the remote protocol handler with the HTTP handler, opening more
// than one tunnel stream, or losing WebSocket event boundaries must break this
// public-route test.
func TestWebSocketLocalAndRemoteEchoTextBinaryAndLargeMessage(t *testing.T) {
	opener := &echoWebSocketOpener{stream: newEchoWebSocketStream()}
	remote := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Relay: opener})
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &order,
			route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket},
		},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{}, AgentPicker: remoteWebSocketAgentPicker{},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Handlers:              map[string]ProtocolHandler{ProtocolWebSocket: remote},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live?room=blue", nil,
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	t.Cleanup(func() { _ = client.Close() })
	require.NoError(t, client.SetReadDeadline(time.Now().Add(3*time.Second)))

	messages := []struct {
		messageType int
		payload     []byte
	}{
		{messageType: websocket.TextMessage, payload: []byte("remote text")},
		{messageType: websocket.TextMessage, payload: nil},
		{messageType: websocket.BinaryMessage, payload: []byte{0, 1, 2, 255}},
		{messageType: websocket.BinaryMessage, payload: make([]byte, 256<<10)},
	}
	for index := range messages[2].payload {
		messages[2].payload[index] = byte(index % 251)
	}
	for _, message := range messages {
		require.NoError(t, client.WriteMessage(message.messageType, message.payload))
		messageType, payload, readErr := client.ReadMessage()
		require.NoError(t, readErr)
		require.Equal(t, message.messageType, messageType)
		if len(message.payload) == 0 {
			require.Empty(t, payload)
		} else {
			require.Equal(t, message.payload, payload)
		}
	}
	require.Equal(t, int32(1), opener.calls.Load())
	require.Equal(t, "target-agent", opener.open.TargetAgentID)
	require.Equal(t, uint(17), opener.open.RouteID)
	require.Equal(t, "room=blue", opener.open.API.RawQuery)
}

// A Direct preparation failure happens before any remote transport is opened,
// so Relay can safely use the same frozen Agent and route.
func TestWebSocketRemoteDirectPrepareFailureFallsBackToRelay(t *testing.T) {
	direct := &failingDirectWebSocketOpener{err: errors.New("direct unavailable")}
	relay := &echoWebSocketOpener{stream: newEchoWebSocketStream()}
	remote := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Direct: direct, Relay: relay, AddressTag: "direct"})
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		AgentPicker: fixedExecutionAgentPicker{pick: AgentPick{
			ExecutionAgentID: "target-agent", AgentRouteID: 17,
			Target: models.Agent{
				AgentID: "target-agent", DirectInboundEnabled: true, RelayInboundEnabled: true,
				HTTPAddresses: `[]`,
			},
		}},
		// behavior change: Generic API execution capability is required before remote dispatch.
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Handlers:              map[string]ProtocolHandler{ProtocolWebSocket: remote},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.NotNil(t, response)
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.Zero(t, direct.calls.Load(), "a preparation failure must not invoke the Direct opener")
	require.Equal(t, int32(1), relay.calls.Load())
	require.Equal(t, "target-agent", relay.open.TargetAgentID)
	require.Equal(t, uint(17), relay.open.RouteID)
}

func TestWebSocketRemoteDirectOpenFailureIsTerminalWithoutRelay(t *testing.T) {
	errorDirect := &failingDirectWebSocketOpener{err: errors.New("direct open failed")}
	nilStreamDirect := &recordingDirectWebSocketOpener{}
	for _, test := range []struct {
		name   string
		direct agentproxy.DirectWebSocketAPIStreamOpener
		calls  *atomic.Int32
	}{
		{name: "opener error", direct: errorDirect, calls: &errorDirect.calls},
		{name: "nil stream", direct: nilStreamDirect, calls: &nilStreamDirect.calls},
	} {
		t.Run(test.name, func(t *testing.T) {
			relay := &echoWebSocketOpener{stream: newEchoWebSocketStream()}
			remote := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Direct: test.direct, Relay: relay, AddressTag: "direct"})
			rc := &RequestContext{Agent: AgentPick{Target: models.Agent{
				AgentID: "target-agent", DirectInboundEnabled: true, RelayInboundEnabled: true,
				HTTPAddresses: `[{"url":"http://direct.invalid","tag":"direct"}]`,
			}}}

			stream, path, err := remote.open(t.Context(), rc, app.WebSocketOpen{})

			require.Error(t, err)
			require.Nil(t, stream)
			require.Equal(t, app.RoutePathDirect, path)
			require.Equal(t, int32(1), test.calls.Load())
			require.Zero(t, relay.calls.Load(), "calling the Direct opener commits the transport choice")
		})
	}
}

// Once Direct has returned a stream and the client has accepted the 101
// upgrade, a bridge failure is terminal for that client connection. Relay must
// not replay the request after the provider has accepted it.
func TestWebSocketRemoteDirectAcceptedFailureDoesNotFallBackToRelay(t *testing.T) {
	directStream := newRecordingRemoteWebSocketStream()
	direct := &recordingDirectWebSocketOpener{stream: directStream}
	relay := &echoWebSocketOpener{stream: newEchoWebSocketStream()}
	remote := NewRemoteWebSocketHandler(RemoteWebSocketHandlerOptions{Direct: direct, Relay: relay, AddressTag: "direct"})
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder:     orderedServiceRouteFinder{order: &order, route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket}},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		AgentPicker: fixedExecutionAgentPicker{pick: AgentPick{
			ExecutionAgentID: "target-agent", AgentRouteID: 17,
			Target: models.Agent{
				AgentID: "target-agent", DirectInboundEnabled: true, RelayInboundEnabled: true,
				HTTPAddresses: `[{"url":"http://direct.invalid","tag":"direct"}]`,
			},
		}},
		ExecutionCapabilities: allowExecutionCapabilityFinder{},
		Handlers:              map[string]ProtocolHandler{ProtocolWebSocket: remote},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		t.Context(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Equal(t, http.StatusSwitchingProtocols, response.StatusCode)
	require.NoError(t, client.WriteMessage(websocket.TextMessage, []byte("accepted then bridge fails")))
	require.Eventually(t, func() bool { return len(directStream.resultContexts) == 1 }, 3*time.Second, 10*time.Millisecond)

	require.Equal(t, int32(1), direct.calls.Load())
	require.Zero(t, relay.calls.Load(), "accepted Direct work must not be replayed over Relay")
}

type fixedExecutionAgentPicker struct{ pick AgentPick }

func (p fixedExecutionAgentPicker) Pick(uint, uint, uint, string) (AgentPick, error) {
	return p.pick, nil
}

// A regression in WebSocket URL construction would let a non-HTTP admin base
// URL reach Gorilla's dialer after the real Gin upgrade request entered the
// Generic API pipeline.
func TestWebSocketMapsHTTPBaseURLAndRejectsUnsupportedScheme(t *testing.T) {
	for _, test := range []struct {
		base, wantScheme string
	}{
		{base: "http://provider.invalid/base", wantScheme: "ws"},
		{base: "https://provider.invalid/base", wantScheme: "wss"},
	} {
		t.Run(test.wantScheme, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/live?room=blue", nil)
			rc, _ := newLocalHTTPRequestContext(request)
			rc.Protocol = ProtocolWebSocket
			rc.Route.UpstreamPath = "/socket"
			target, _, err := buildWebSocketUpstreamRequest(rc, &APIUpstreamLease{Upstream: protocol.SyncedAPIUpstream{
				ID: 11, BackendID: 7, BaseURL: test.base, AuthType: "none",
			}})
			require.NoError(t, err)
			require.Equal(t, test.wantScheme, target.Scheme)
			require.Equal(t, "/base/socket", target.Path)
			require.Equal(t, "room=blue", target.RawQuery)
		})
	}

	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "unsupported", BaseURL: "ftp://provider.invalid", AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	webSocketHandler := NewWebSocketHandler(WebSocketHandlerOptions{Picker: picker})
	service := protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1}
	route := protocol.SyncedAPIRoute{
		ID: 9, ServiceID: 7, Slug: "live", Status: 1,
		Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
	}
	order := []string{}
	handler := NewHandler(HandlerOptions{
		Finder: orderedServiceRouteFinder{
			order: &order,
			route: ServiceRoute{Service: service, Route: route, Protocol: ProtocolWebSocket},
		},
		Permission: allowPermissionGate{}, Quota: allowQuotaGate{},
		Handlers: map[string]ProtocolHandler{ProtocolWebSocket: webSocketHandler},
	})
	gateway := httptest.NewServer(genericAPIRouter(handler))
	t.Cleanup(gateway.Close)

	client, response, err := websocket.DefaultDialer.DialContext(
		context.Background(), "ws"+strings.TrimPrefix(gateway.URL, "http")+"/v1/api/weather/live", nil,
	)
	if client != nil {
		t.Cleanup(func() { _ = client.Close() })
	}
	require.Error(t, err)
	require.NotNil(t, response)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.Equal(t, int32(1), picker.calls.Load())
}
