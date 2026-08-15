package genericapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/genericapipath"
	"github.com/gorilla/websocket"
)

type WebSocketUpstreamPicker interface {
	Pick(uint, apiattempt.APIProtocol, string) (*APIUpstreamLease, error)
}

type WebSocketDialer interface {
	DialContext(context.Context, string, http.Header) (*websocket.Conn, *http.Response, error)
}

type WebSocketHandlerOptions struct {
	Picker   WebSocketUpstreamPicker
	Dialer   WebSocketDialer
	Limiter  SourceLimiter
	Settings SettingsFinder
}

// WebSocketHandler executes one local Generic API WebSocket against one frozen
// upstream. It upgrades and dials at most once and never retries or reconnects.
type WebSocketHandler struct {
	picker   WebSocketUpstreamPicker
	dialer   WebSocketDialer
	limiter  SourceLimiter
	bridge   WebSocketBridge
	settings SettingsFinder
}

func NewWebSocketHandler(options WebSocketHandlerOptions) *WebSocketHandler {
	dialer := options.Dialer
	if dialer == nil {
		dialer = newWebSocketDialerWithoutHandshakeCeiling()
	}
	return &WebSocketHandler{picker: options.Picker, dialer: dialer, limiter: options.Limiter, settings: options.Settings}
}

func newWebSocketDialerWithoutHandshakeCeiling() *websocket.Dialer {
	dialer := *websocket.DefaultDialer
	dialer.EnableCompression = false
	dialer.HandshakeTimeout = 0
	return &dialer
}

func (h *WebSocketHandler) Serve(ctx context.Context, rc *RequestContext) error {
	if err := validLocalWebSocketRequest(ctx, h, rc); err != nil {
		return err
	}
	startedAt := time.Now()
	result := &apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	var executionErr error
	defer func() { rc.Execution = *result }()

	lease, err := h.picker.Pick(rc.Route.BackendID, apiattempt.APIProtocolWebSocket, rc.RequestID)
	if err != nil || lease == nil || !lease.valid() {
		result.ErrorStage, result.ErrorCode = "upstream_pick", CodeUnavailable
		if err == nil {
			err = ErrExecutionUnavailable
		}
		return err
	}
	defer func() {
		lease.Finish(APIBreakerCompletion{Result: result, Err: executionErr, ClientAbort: clientAbortReason(ctx)})
	}()
	result.APIUpstreamID, result.APIUpstreamName = lease.Upstream.ID, lease.Upstream.Name
	rc.UpstreamName = lease.Upstream.Name

	permit, err := h.acquireUpstreamLimiter(ctx, rc, lease.Upstream.ID)
	if err != nil {
		mergeRateLimitResult(result, rateLimitResult(err))
		result.ErrorStage, result.ErrorCode = "limiter", ErrorCode(err)
		return err
	}
	mergeRateLimitResult(result, rateLimitResult(permit))
	if permit != nil {
		defer permit.Release()
	}

	target, header, err := buildWebSocketUpstreamRequest(rc, lease)
	if err != nil {
		result.ErrorStage, result.ErrorCode = "request_build", ErrorCode(err)
		return err
	}
	result.ProviderDispatched = true
	dialContext, cancelDial := webSocketHandshakeContext(ctx, h.settings)
	defer cancelDial()
	upstream, response, err := h.dialer.DialContext(dialContext, target.String(), header)
	if response != nil {
		result.UpstreamStatus = response.StatusCode
	}
	if err != nil {
		result.ErrorStage, result.ErrorCode = "transport", ErrorCode(err)
		executionErr = err
		return err
	}
	defer upstream.Close()
	result.UpstreamStatus = http.StatusSwitchingProtocols
	result.FirstByteMs = int(time.Since(startedAt) / time.Millisecond)

	responseHeader := make(http.Header)
	responseHeader.Set(consts.HeaderXRequestID, rc.RequestID)
	if selected := upstream.Subprotocol(); selected != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", selected)
	}
	upgrader := websocket.Upgrader{EnableCompression: false}
	client, err := upgrader.Upgrade(rc.Context.Writer, rc.Context.Request, responseHeader)
	if err != nil {
		result.ErrorStage, result.ErrorCode = "client_upgrade", ErrorCode(err)
		executionErr = err
		return err
	}
	rc.ClientUpgradeCommitted = true
	rc.ClientStatusCode = http.StatusSwitchingProtocols
	bridge := h.bridge.withControlWriteTimeout(h.currentControlWriteTimeout)
	bridgeResult, bridgeErr := bridge.ConnectionsWithResult(ctx, client, upstream)
	result.WebSocketCloseCode = bridgeResult.CloseCode
	executionErr = bridgeErr
	if bridgeErr != nil {
		result.ErrorStage, result.ErrorCode = "transport", ErrorCode(bridgeErr)
	}
	return nil
}

func webSocketHandshakeContext(ctx context.Context, settingsFinder SettingsFinder) (context.Context, context.CancelFunc) {
	if settingsFinder == nil {
		return ctx, func() {}
	}
	timeout := time.Duration(settingsFinder.Settings().APIWebSocketHandshakeTimeoutMs) * time.Millisecond
	if timeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, timeout)
}

func (h *WebSocketHandler) currentControlWriteTimeout() time.Duration {
	if h == nil {
		return webSocketControlWriteTimeout
	}
	return webSocketControlWriteTimeoutFrom(h.settings)
}

func webSocketControlWriteTimeoutFrom(settingsFinder SettingsFinder) time.Duration {
	if settingsFinder == nil {
		return webSocketControlWriteTimeout
	}
	return time.Duration(settingsFinder.Settings().APIWebSocketControlWriteTimeoutMs) * time.Millisecond
}

func webSocketControlContext(
	ctx context.Context,
	settingsFinder SettingsFinder,
) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	timeout := webSocketControlWriteTimeoutFrom(settingsFinder)
	if timeout <= 0 {
		return context.WithCancel(detached)
	}
	return context.WithTimeout(detached, timeout)
}

func validLocalWebSocketRequest(ctx context.Context, h *WebSocketHandler, rc *RequestContext) error {
	if ctx == nil || h == nil || h.picker == nil || h.dialer == nil || rc == nil || rc.Context == nil ||
		rc.Context.Request == nil || rc.Context.Writer == nil || rc.Service.ID == 0 || rc.RequestID == "" ||
		rc.Protocol != ProtocolWebSocket {
		return fmt.Errorf("%w: invalid local WebSocket handler input", ErrExecutionUnavailable)
	}
	return nil
}

func (h *WebSocketHandler) acquireUpstreamLimiter(ctx context.Context, rc *RequestContext, upstreamID uint) (APIPermit, error) {
	if h.limiter == nil {
		return nil, nil
	}
	return h.limiter.Acquire(ctx, APIRequestFacts{
		UserID: rc.UserID, GroupID: rc.GroupID, TokenID: rc.TokenID,
		APIServiceID: rc.Service.ID, APIRouteID: rc.Route.ID, APIUpstreamID: upstreamID, RequestID: rc.RequestID,
		NoWait: true,
	})
}

func buildWebSocketUpstreamRequest(rc *RequestContext, lease *APIUpstreamLease) (*url.URL, http.Header, error) {
	target, err := (UpstreamURLBuilder{}).Build(
		lease.Upstream, rc.Route.UpstreamPath, rc.Subpath, rc.Context.Request.URL.RawQuery,
	)
	if err != nil {
		return nil, nil, err
	}
	switch target.Scheme {
	case "http":
		target.Scheme = "ws"
	case "https":
		target.Scheme = "wss"
	default:
		return nil, nil, genericapipath.ErrUnsafeUpstreamURL
	}
	header := (HeaderBuilder{}).BuildWebSocket(
		rc.Context.Request.Header, lease.Upstream, lease.Upstream.Credential, rc.Route.WebSocketSubprotocols,
	)
	return target, header, nil
}
