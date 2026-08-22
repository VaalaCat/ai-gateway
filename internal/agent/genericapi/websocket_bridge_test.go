package genericapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func webSocketConnectionPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	accepted := make(chan *websocket.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err == nil {
			accepted <- connection
		}
	}))
	t.Cleanup(server.Close)
	client, _, err := websocket.DefaultDialer.DialContext(t.Context(), "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	var serverSide *websocket.Conn
	select {
	case serverSide = <-accepted:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out accepting WebSocket pair")
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = serverSide.Close()
	})
	return client, serverSide
}

func TestWebSocketBridgeReadsControlWriteTimeoutAtConnectionStart(t *testing.T) {
	current := 13 * time.Millisecond
	bridge := WebSocketBridge{}.withControlWriteTimeout(func() time.Duration { return current })
	require.Equal(t, 13*time.Millisecond, bridge.controlWriteTimeout())

	current = 27 * time.Millisecond
	require.Equal(t, 27*time.Millisecond, bridge.controlWriteTimeout())

	current = 0
	require.Zero(t, bridge.controlWriteTimeout())
	require.Equal(t, webSocketControlWriteTimeout, (WebSocketBridge{}).controlWriteTimeout())
}

func TestWebSocketTargetHandlerAcceptsHotSettings(t *testing.T) {
	finder := &mutableAgentSettingsFinder{}
	handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{Settings: finder})
	require.Same(t, finder, handler.settings)
}

func TestWebSocketTargetHandlerPicksTheRouteBackendAndReportsNoCandidateAsUnavailable(t *testing.T) {
	stream := newSettingsTargetWebSocketStream()
	picker := &localWebSocketPicker{err: unavailableUpstream("no candidates for route backend")}
	route := ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, BackendID: 20, Slug: "radar", Status: 1,
			Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet}},
		Protocol: ProtocolWebSocket,
	}
	handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: route}, Picker: picker,
	})

	err := handler.serveStream(t.Context(), stream)

	require.Error(t, err)
	require.Equal(t, uint32(20), picker.backendID.Load())
	require.Equal(t, int32(1), picker.calls.Load(), "target Agent must not re-pick a WebSocket upstream")
	result := <-stream.results
	require.Equal(t, "upstream_pick", result.ErrorStage)
	require.Equal(t, CodeUnavailable, result.ErrorCode)
}

func TestWebSocketTargetSendsSafeErrorMessage(t *testing.T) {
	const secret = "provider-secret"
	stream := newSettingsTargetWebSocketStream()
	route := ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, BackendID: 20, Slug: "radar", Status: 1,
			Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet}},
		Protocol: ProtocolWebSocket,
	}
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 20, BaseURL: "https://upstream.example/socket", AuthType: "query",
			Credential: protocol.APIUpstreamCredential{QueryName: "token", QueryValue: secret},
		},
		permit: newLocalHTTPPermit(),
	}}
	handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: route}, Picker: picker, Dialer: safeErrorWebSocketDialer{},
	})

	require.Error(t, handler.serveStream(t.Context(), stream))
	result := <-stream.results
	require.Equal(t, "transport", result.ErrorStage)
	require.Contains(t, result.ErrorMessage, "connection refused")
	require.NotContains(t, result.ErrorMessage, secret)
	require.NotContains(t, result.ErrorMessage, "upstream.example")
}

type recordedContext struct {
	hasDeadline bool
	deadline    time.Time
	remaining   time.Duration
	cancellable bool
}

func recordContext(ctx context.Context) recordedContext {
	deadline, hasDeadline := ctx.Deadline()
	remaining := time.Duration(0)
	if hasDeadline {
		remaining = time.Until(deadline)
	}
	return recordedContext{
		hasDeadline: hasDeadline, deadline: deadline, remaining: remaining, cancellable: ctx.Done() != nil,
	}
}

func requireContextTimeout(t *testing.T, got recordedContext, want time.Duration) {
	t.Helper()
	require.True(t, got.hasDeadline)
	require.True(t, got.cancellable)
	require.Greater(t, got.remaining, want-10*time.Millisecond)
	require.LessOrEqual(t, got.remaining, want)
}

type recordingTargetWebSocketDialer struct {
	mu       sync.Mutex
	contexts []recordedContext
}

func (d *recordingTargetWebSocketDialer) DialContext(
	ctx context.Context,
	_ string,
	_ http.Header,
) (*websocket.Conn, *http.Response, error) {
	d.mu.Lock()
	d.contexts = append(d.contexts, recordContext(ctx))
	d.mu.Unlock()
	return nil, nil, errors.New("provider dial failed")
}

func (d *recordingTargetWebSocketDialer) contextAt(index int) recordedContext {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.contexts[index]
}

func TestWebSocketTargetReadsLatestHandshakeTimeoutForEveryServe(t *testing.T) {
	settingsFinder := &mutableAgentSettingsFinder{}
	dialer := &recordingTargetWebSocketDialer{}
	handler := newSettingsTargetWebSocketHandler(settingsFinder, dialer)
	stream := newSettingsTargetWebSocketStream()

	for index, timeout := range []int{15, 35, 0} {
		settingsFinder.Update(settings.AgentSettings{APIWebSocketHandshakeTimeoutMs: timeout})
		require.Error(t, handler.serveStream(t.Context(), stream))
		got := dialer.contextAt(index)
		if timeout == 0 {
			require.False(t, got.hasDeadline)
			require.True(t, got.cancellable)
			continue
		}
		requireContextTimeout(t, got, time.Duration(timeout)*time.Millisecond)
	}
}

func TestWebSocketTargetResultUsesLatestControlTimeout(t *testing.T) {
	t.Run("default remains five seconds without settings", func(t *testing.T) {
		stream := newSettingsTargetWebSocketStream()
		handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{})

		require.Error(t, handler.serveStream(t.Context(), stream))
		requireContextTimeout(t, <-stream.resultContexts, 5*time.Second)
	})

	t.Run("hot snapshot applies twenty seven milliseconds", func(t *testing.T) {
		stream := newSettingsTargetWebSocketStream()
		finder := &mutableAgentSettingsFinder{settings: settings.AgentSettings{APIWebSocketControlWriteTimeoutMs: 27}}
		handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{Settings: finder})

		require.Error(t, handler.serveStream(t.Context(), stream))
		requireContextTimeout(t, <-stream.resultContexts, 27*time.Millisecond)
	})

	t.Run("zero has no deadline but remains cancellable", func(t *testing.T) {
		stream := newSettingsTargetWebSocketStream()
		finder := &mutableAgentSettingsFinder{}
		handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{Settings: finder})

		require.Error(t, handler.serveStream(t.Context(), stream))
		got := <-stream.resultContexts
		require.False(t, got.hasDeadline)
		require.True(t, got.cancellable)
	})
}

type fakeWebSocketTargetStream struct {
	open           app.WebSocketOpen
	incoming       chan app.WebSocketEvent
	outgoing       chan app.WebSocketEvent
	accepted       chan app.WebSocketAccepted
	results        chan apiattempt.APIExecutionResult
	resultContexts chan recordedContext
	sendEvent      func(context.Context, app.WebSocketEvent) error
}

func (s *fakeWebSocketTargetStream) OpenMetadata() app.WebSocketOpen { return s.open }

func (s *fakeWebSocketTargetStream) Accept(ctx context.Context, accepted app.WebSocketAccepted) error {
	select {
	case s.accepted <- accepted:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *fakeWebSocketTargetStream) SendResult(ctx context.Context, result apiattempt.APIExecutionResult) error {
	if s.resultContexts != nil {
		s.resultContexts <- recordContext(ctx)
	}
	select {
	case s.results <- result:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func newSettingsTargetWebSocketStream() *fakeWebSocketTargetStream {
	return &fakeWebSocketTargetStream{
		open: app.WebSocketOpen{
			TargetAgentID: "target-agent", RouteID: 17, RequestID: "target-request",
			Path: "/v1/api/weather/live", Hop: 1,
			API: apiattempt.APIAttemptMeta{
				APIServiceID: 7, APIRouteID: 9, Protocol: apiattempt.APIProtocolWebSocket,
				Method: http.MethodGet,
			},
		},
		incoming: make(chan app.WebSocketEvent, 1), outgoing: make(chan app.WebSocketEvent, 1),
		accepted: make(chan app.WebSocketAccepted, 1), results: make(chan apiattempt.APIExecutionResult, 4),
		resultContexts: make(chan recordedContext, 4),
	}
}

func newSettingsTargetWebSocketHandler(
	settingsFinder SettingsFinder,
	dialer WebSocketDialer,
) *WebSocketTargetHandler {
	route := ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, Slug: "live", Status: 1,
			Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		},
		Protocol: ProtocolWebSocket,
	}
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "target", BaseURL: "https://upstream.example", AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	return NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: route}, Picker: picker, Dialer: dialer, Settings: settingsFinder,
	})
}

func (s *fakeWebSocketTargetStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
	if s.sendEvent != nil {
		return s.sendEvent(ctx, event)
	}
	select {
	case s.outgoing <- event:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func TestWebSocketTargetBridgeUsesCurrentControlTimeout(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.WriteMessage(websocket.TextMessage, []byte("stop target bridge"))
		_, _, _ = connection.ReadMessage()
	}))
	t.Cleanup(provider.Close)

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
			bridgeContext := make(chan recordedContext, 1)
			stream := newSettingsTargetWebSocketStream()
			stream.sendEvent = func(ctx context.Context, event app.WebSocketEvent) error {
				if event.Kind == app.WebSocketCloseEvent {
					bridgeContext <- recordContext(ctx)
					return nil
				}
				return errors.New("stop target bridge")
			}
			handler := newSettingsTargetWebSocketHandler(test.finder, nil)
			handler.picker = &localWebSocketPicker{lease: &APIUpstreamLease{
				Upstream: protocol.SyncedAPIUpstream{
					ID: 11, BackendID: 7, Name: "target", BaseURL: provider.URL, AuthType: "none",
				},
				permit: newLocalHTTPPermit(),
			}}

			require.Error(t, handler.serveStream(t.Context(), stream))
			got := <-bridgeContext
			if test.deadline {
				requireContextTimeout(t, got, test.timeout)
				return
			}
			require.False(t, got.hasDeadline)
			require.True(t, got.cancellable)
		})
	}
}

func (s *fakeWebSocketTargetStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
	select {
	case event := <-s.incoming:
		return event, nil
	case <-ctx.Done():
		return app.WebSocketEvent{}, context.Cause(ctx)
	}
}

type fixedServiceRouteByIDFinder struct{ route ServiceRoute }

func (f fixedServiceRouteByIDFinder) FindServiceRouteByID(serviceID, routeID uint) (ServiceRoute, error) {
	if f.route.Service.ID != serviceID || f.route.Route.ID != routeID {
		return ServiceRoute{}, ErrExecutionUnavailable
	}
	return f.route, nil
}

// If the target handler re-runs source gates, picks another upstream, dials
// twice, or fails to bridge the committed Task 15 event stream, this real
// provider socket test fails.
func TestWebSocketTargetPicksAndDialsProviderOnce(t *testing.T) {
	var providerDials atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		providerDials.Add(1)
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		messageType, payload, err := connection.ReadMessage()
		if err == nil {
			_ = connection.WriteMessage(messageType, payload)
		}
	}))
	t.Cleanup(provider.Close)

	picker := &localWebSocketPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, Name: "target-echo", BaseURL: provider.URL, AuthType: "none",
		},
		permit: newLocalHTTPPermit(),
	}}
	route := ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, Slug: "live", Status: 1,
			Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		},
		Protocol: ProtocolWebSocket,
	}
	handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: route}, Picker: picker,
	})
	stream := &fakeWebSocketTargetStream{
		open: app.WebSocketOpen{
			TargetAgentID: "target-agent", RouteID: 17, RequestID: "target-request", Path: "/v1/api/weather/live", Hop: 1,
			API: apiattempt.APIAttemptMeta{
				APIServiceID: 7, APIRouteID: 9, Protocol: apiattempt.APIProtocolWebSocket,
				Method: http.MethodGet, RawQuery: "room=blue",
			},
		},
		incoming: make(chan app.WebSocketEvent, 8), outgoing: make(chan app.WebSocketEvent, 8),
		accepted: make(chan app.WebSocketAccepted, 1), results: make(chan apiattempt.APIExecutionResult, 1),
	}
	ctx, cancel := context.WithCancel(t.Context())
	serveResult := make(chan error, 1)
	go func() { serveResult <- handler.serveStream(ctx, stream) }()

	stream.incoming <- app.WebSocketEvent{Kind: app.WebSocketMessageStartEvent, MessageID: 1, Type: app.WebSocketTextMessage}
	stream.incoming <- app.WebSocketEvent{Kind: app.WebSocketMessageDataEvent, MessageID: 1, Data: []byte("target echo")}
	stream.incoming <- app.WebSocketEvent{Kind: app.WebSocketMessageEndEvent, MessageID: 1}
	require.Equal(t, app.WebSocketMessageStartEvent, receiveTargetEvent(t, stream.outgoing).Kind)
	data := receiveTargetEvent(t, stream.outgoing)
	require.Equal(t, app.WebSocketMessageDataEvent, data.Kind)
	require.Equal(t, []byte("target echo"), data.Data)
	require.Equal(t, app.WebSocketMessageEndEvent, receiveTargetEvent(t, stream.outgoing).Kind)
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, int32(1), providerDials.Load())
	cancel()
	select {
	case <-serveResult:
	case <-time.After(3 * time.Second):
		t.Fatal("target handler did not stop after cancel")
	}
}

func TestWebSocketQueryCredentialTargetOverridesClientAndBaseQuery(t *testing.T) {
	providerRequest := make(chan []string, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		query := request.URL.Query()
		providerRequest <- []string{request.URL.Path, query.Get("base"), query.Get("room"), query.Get("token")}
		upgrader := websocket.Upgrader{EnableCompression: false, CheckOrigin: func(*http.Request) bool { return true }}
		connection, err := upgrader.Upgrade(writer, request, nil)
		if err == nil {
			defer connection.Close()
			_, _, _ = connection.ReadMessage()
		}
	}))
	t.Cleanup(provider.Close)

	upstream := protocol.SyncedAPIUpstream{
		ID: 11, BackendID: 7, Name: "target-query-auth", BaseURL: provider.URL + "/base?base=one&token=base", AuthType: "query",
	}
	upstream.Credential.QueryName = "token"
	upstream.Credential.QueryValue = "provider-secret"
	picker := &localWebSocketPicker{lease: &APIUpstreamLease{Upstream: upstream, permit: newLocalHTTPPermit()}}
	route := ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Slug: "weather", Status: 1},
		Route: protocol.SyncedAPIRoute{
			ID: 9, ServiceID: 7, Slug: "live", Status: 1, UpstreamPath: "/socket",
			Protocols: []string{ProtocolWebSocket}, AllowedMethods: []string{http.MethodGet},
		},
		Protocol: ProtocolWebSocket,
	}
	handler := NewWebSocketTargetHandler(WebSocketTargetHandlerOptions{
		Finder: fixedServiceRouteByIDFinder{route: route}, Picker: picker,
	})
	stream := &fakeWebSocketTargetStream{
		open: app.WebSocketOpen{
			TargetAgentID: "target-agent", RouteID: 17, RequestID: "target-request", Path: "/v1/api/weather/live", Hop: 1,
			API: apiattempt.APIAttemptMeta{
				APIServiceID: 7, APIRouteID: 9, Protocol: apiattempt.APIProtocolWebSocket,
				Method: http.MethodGet, RawQuery: "room=blue&token=client",
			},
		},
		incoming: make(chan app.WebSocketEvent, 8), outgoing: make(chan app.WebSocketEvent, 8),
		accepted: make(chan app.WebSocketAccepted, 1), results: make(chan apiattempt.APIExecutionResult, 1),
	}
	ctx, cancel := context.WithCancel(t.Context())
	serveResult := make(chan error, 1)
	go func() { serveResult <- handler.serveStream(ctx, stream) }()

	select {
	case got := <-providerRequest:
		require.Equal(t, []string{"/base/socket", "one", "blue", "provider-secret"}, got)
	case <-time.After(3 * time.Second):
		t.Fatal("provider did not observe target WebSocket request")
	}
	cancel()
	select {
	case <-serveResult:
	case <-time.After(3 * time.Second):
		t.Fatal("target handler did not stop after cancel")
	}
}

func TestWebSocketBridgeForwardsPingPongAndClose(t *testing.T) {
	externalClient, gatewayClient := webSocketConnectionPair(t)
	gatewayUpstream, provider := webSocketConnectionPair(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	resultC := make(chan WebSocketBridgeResult, 1)
	go func() {
		result, _ := (WebSocketBridge{}).ConnectionsWithResult(ctx, gatewayClient, gatewayUpstream)
		resultC <- result
	}()

	ping := make(chan string, 1)
	externalClient.SetPingHandler(func(data string) error {
		ping <- data
		return nil
	})
	require.NoError(t, provider.WriteControl(websocket.PingMessage, []byte("provider-ping"), time.Now().Add(time.Second)))
	require.NoError(t, provider.WriteMessage(websocket.TextMessage, []byte("after-ping")))
	messageType, payload, err := externalClient.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.TextMessage, messageType)
	require.Equal(t, []byte("after-ping"), payload)
	require.Equal(t, "provider-ping", <-ping)

	pong := make(chan string, 1)
	provider.SetPongHandler(func(data string) error {
		pong <- data
		return nil
	})
	require.NoError(t, externalClient.WriteControl(websocket.PongMessage, []byte("client-pong"), time.Now().Add(time.Second)))
	require.NoError(t, externalClient.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}))
	messageType, payload, err = provider.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, messageType)
	require.Equal(t, []byte{1, 2, 3}, payload)
	require.Equal(t, "client-pong", <-pong)

	require.NoError(t, externalClient.WriteControl(
		websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bridge-done"),
		time.Now().Add(time.Second),
	))
	_, _, err = provider.ReadMessage()
	var closeErr *websocket.CloseError
	require.ErrorAs(t, err, &closeErr)
	require.Equal(t, websocket.CloseNormalClosure, closeErr.Code)
	require.Equal(t, "bridge-done", closeErr.Text)
	select {
	case result := <-resultC:
		require.Equal(t, websocket.CloseNormalClosure, result.CloseCode)
	case <-time.After(3 * time.Second):
		t.Fatal("bridge did not return after close")
	}
}

type blockedWebSocketControlSend struct {
	event   app.WebSocketEvent
	context recordedContext
}

type blockingWebSocketControlStream struct {
	entered     chan blockedWebSocketControlSend
	exited      chan error
	release     chan struct{}
	releaseOnce sync.Once
}

func newBlockingWebSocketControlStream() *blockingWebSocketControlStream {
	return &blockingWebSocketControlStream{
		entered: make(chan blockedWebSocketControlSend, 2),
		exited:  make(chan error, 2),
		release: make(chan struct{}),
	}
}

func (s *blockingWebSocketControlStream) SendEvent(ctx context.Context, event app.WebSocketEvent) error {
	select {
	case s.entered <- blockedWebSocketControlSend{event: event, context: recordContext(ctx)}:
	case <-ctx.Done():
		return context.Cause(ctx)
	}

	var err error
	select {
	case <-ctx.Done():
		err = context.Cause(ctx)
	case <-s.release:
		err = errors.New("blocking control send released")
	}
	s.exited <- err
	return err
}

func (s *blockingWebSocketControlStream) ReceiveEvent(ctx context.Context) (app.WebSocketEvent, error) {
	<-ctx.Done()
	return app.WebSocketEvent{}, context.Cause(ctx)
}

func (s *blockingWebSocketControlStream) stop() {
	s.releaseOnce.Do(func() { close(s.release) })
}

func TestWebSocketStreamControlForwardersInheritBridgeContext(t *testing.T) {
	controls := []struct {
		name        string
		messageType int
		payload     []byte
		want        app.WebSocketEvent
	}{
		{
			name: "ping", messageType: websocket.PingMessage, payload: []byte("client-ping"),
			want: app.WebSocketEvent{Kind: app.WebSocketPingEvent, Data: []byte("client-ping")},
		},
		{
			name: "pong", messageType: websocket.PongMessage, payload: []byte("client-pong"),
			want: app.WebSocketEvent{Kind: app.WebSocketPongEvent, Data: []byte("client-pong")},
		},
		{
			name: "close", messageType: websocket.CloseMessage,
			payload: websocket.FormatCloseMessage(websocket.CloseNormalClosure, "client-close"),
			want: app.WebSocketEvent{
				Kind: app.WebSocketCloseEvent, Code: websocket.CloseNormalClosure, Reason: "client-close",
			},
		},
	}
	contexts := []struct {
		name         string
		timeout      time.Duration
		cancelParent bool
		wantDeadline bool
		wantCause    error
	}{
		{
			name: "parent cancel", timeout: time.Second, cancelParent: true,
			wantDeadline: true, wantCause: context.Canceled,
		},
		{
			name: "positive timeout", timeout: 100 * time.Millisecond,
			wantDeadline: true, wantCause: context.DeadlineExceeded,
		},
		{
			name: "zero timeout remains cancellable", cancelParent: true,
			wantCause: context.Canceled,
		},
	}

	for _, control := range controls {
		for _, contextCase := range contexts {
			t.Run(control.name+"/"+contextCase.name, func(t *testing.T) {
				// behavior change: control forwarding must be bounded by this connection's
				// bridge context even when the stream is applying backpressure.
				peer, gateway := webSocketConnectionPair(t)
				stream := newBlockingWebSocketControlStream()
				bridgeCtx, cancelBridge := context.WithCancel(t.Context())
				bridgeDone := make(chan error, 1)
				bridge := (WebSocketBridge{}).withControlWriteTimeout(
					func() time.Duration { return contextCase.timeout },
				)
				go func() {
					_, err := bridge.connectionAndEventStream(bridgeCtx, gateway, stream, nil)
					bridgeDone <- err
				}()

				finished := false
				defer func() {
					cancelBridge()
					stream.stop()
					if finished {
						return
					}
					select {
					case <-bridgeDone:
					case <-time.After(3 * time.Second):
					}
				}()

				require.NoError(t, peer.WriteControl(
					control.messageType, control.payload, time.Now().Add(time.Second),
				))
				var blocked blockedWebSocketControlSend
				select {
				case blocked = <-stream.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("control SendEvent was not entered")
				}
				require.Equal(t, control.want, blocked.event)
				if contextCase.wantDeadline {
					requireContextTimeout(t, blocked.context, contextCase.timeout)
				} else {
					require.False(t, blocked.context.hasDeadline)
					require.True(t, blocked.context.cancellable)
				}

				if contextCase.cancelParent {
					cancelBridge()
				}
				select {
				case err := <-stream.exited:
					require.ErrorIs(t, err, contextCase.wantCause)
				case <-time.After(time.Second):
					t.Fatal("blocked control SendEvent did not stop")
				}
				cancelBridge()
				select {
				case <-bridgeDone:
					finished = true
				case <-time.After(3 * time.Second):
					t.Fatal("WebSocket bridge did not stop")
				}
			})
		}
	}
}

func receiveTargetEvent(t *testing.T, events <-chan app.WebSocketEvent) app.WebSocketEvent {
	t.Helper()
	select {
	case event := <-events:
		return event
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for target WebSocket event")
		return app.WebSocketEvent{}
	}
}
