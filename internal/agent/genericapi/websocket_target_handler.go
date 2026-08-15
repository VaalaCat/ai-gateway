package genericapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/genericapipath"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type webSocketTargetStream interface {
	OpenMetadata() app.WebSocketOpen
	Accept(context.Context, app.WebSocketAccepted) error
	SendResult(context.Context, apiattempt.APIExecutionResult) error
	webSocketEventStream
}

type WebSocketTargetHandlerOptions struct {
	Finder   APIServiceRouteByIDFinder
	Picker   WebSocketUpstreamPicker
	Dialer   WebSocketDialer
	Limiter  SourceLimiter
	Settings SettingsFinder
}

// WebSocketTargetHandler is the execution-Agent consumer of one committed
// tunnel stream. Source authorization and Agent selection are absent here.
type WebSocketTargetHandler struct {
	finder   APIServiceRouteByIDFinder
	picker   WebSocketUpstreamPicker
	dialer   WebSocketDialer
	limiter  SourceLimiter
	bridge   WebSocketBridge
	settings SettingsFinder
}

func NewWebSocketTargetHandler(options WebSocketTargetHandlerOptions) *WebSocketTargetHandler {
	dialer := options.Dialer
	if dialer == nil {
		dialer = newWebSocketDialerWithoutHandshakeCeiling()
	}
	return &WebSocketTargetHandler{
		finder: options.Finder, picker: options.Picker, dialer: dialer, limiter: options.Limiter, settings: options.Settings,
	}
}

func (h *WebSocketTargetHandler) ServeWebSocketAPI(ctx context.Context, stream *agenttunnel.WebSocketTargetStream) error {
	if stream == nil {
		return ErrExecutionUnavailable
	}
	return h.serveStream(ctx, stream)
}

func (h *WebSocketTargetHandler) serveStream(ctx context.Context, stream webSocketTargetStream) (returnErr error) {
	if ctx == nil || stream == nil {
		return ErrExecutionUnavailable
	}
	var settingsFinder SettingsFinder
	if h != nil {
		settingsFinder = h.settings
	}
	result := &apiattempt.APIExecutionResult{ProviderDispatchKnown: true}
	defer func() {
		resultCtx, cancel := webSocketControlContext(ctx, settingsFinder)
		defer cancel()
		if err := stream.SendResult(resultCtx, *result); returnErr == nil && err != nil {
			returnErr = err
		}
	}()
	if h == nil || h.finder == nil || h.picker == nil || h.dialer == nil {
		result.ErrorStage, result.ErrorCode = "handler", CodeUnavailable
		return ErrExecutionUnavailable
	}
	open := stream.OpenMetadata()
	route, err := h.findRoute(open)
	if err != nil {
		result.ErrorStage, result.ErrorCode = "route", ErrorCode(err)
		return err
	}
	lease, err := h.picker.Pick(route.Route.BackendID, apiattempt.APIProtocolWebSocket, open.RequestID)
	if err != nil || lease == nil || !lease.valid() {
		result.ErrorStage, result.ErrorCode = "upstream_pick", CodeUnavailable
		if err == nil {
			err = ErrExecutionUnavailable
		}
		return err
	}
	result.APIUpstreamID, result.APIUpstreamName = lease.Upstream.ID, lease.Upstream.Name
	var executionErr error
	defer func() { lease.Finish(APIBreakerCompletion{Result: result, Err: executionErr}) }()

	permit, err := h.acquireLimiter(ctx, open, route, lease.Upstream.ID)
	if err != nil {
		mergeRateLimitResult(result, rateLimitResult(err))
		result.ErrorStage, result.ErrorCode = "limiter", ErrorCode(err)
		executionErr = err
		return err
	}
	mergeRateLimitResult(result, rateLimitResult(permit))
	if permit != nil {
		defer permit.Release()
	}
	target, header, err := buildTargetWebSocketRequest(open, route.Route, lease.Upstream)
	if err != nil {
		result.ErrorStage, result.ErrorCode = "request_build", ErrorCode(err)
		executionErr = err
		return err
	}
	result.ProviderDispatched = true
	dialContext, cancelDial := webSocketHandshakeContext(ctx, h.settings)
	defer cancelDial()
	upstream, response, err := h.dialer.DialContext(dialContext, target.String(), header)
	if response != nil {
		result.UpstreamStatus = response.StatusCode
		if response.Body != nil {
			defer response.Body.Close()
		}
	}
	if err != nil {
		result.ErrorStage, result.ErrorCode = "transport", ErrorCode(err)
		executionErr = err
		if response != nil {
			rejection, rejectionErr := snapshotWebSocketRejection(response, webSocketRejectionMetadataLimit(stream))
			if rejectionErr != nil {
				result.ErrorStage, result.ErrorCode = "rejection", ErrorCode(rejectionErr)
				executionErr = rejectionErr
				return rejectionErr
			}
			result.ResponseBytes = int64(len(rejection.Body))
			if rejectionErr = stream.Accept(ctx, app.WebSocketAccepted{
				ProviderStatus: response.StatusCode, Rejection: &rejection,
			}); rejectionErr != nil {
				result.ErrorStage, result.ErrorCode = "rejection", ErrorCode(rejectionErr)
				executionErr = rejectionErr
				return rejectionErr
			}
		}
		return err
	}
	defer upstream.Close()
	result.UpstreamStatus = http.StatusSwitchingProtocols
	if err = stream.Accept(ctx, app.WebSocketAccepted{
		Subprotocol: upstream.Subprotocol(), ProviderStatus: http.StatusSwitchingProtocols,
	}); err != nil {
		result.ErrorStage, result.ErrorCode = "accept", ErrorCode(err)
		executionErr = err
		return err
	}
	bridge := h.bridge.withControlWriteTimeout(h.currentControlWriteTimeout)
	bridgeResult, bridgeErr := bridge.connectionAndEventStream(ctx, upstream, stream, nil)
	result.WebSocketCloseCode = bridgeResult.CloseCode
	executionErr = bridgeErr
	if bridgeErr != nil {
		result.ErrorStage, result.ErrorCode = "transport", ErrorCode(bridgeErr)
	}
	return executionErr
}

func (h *WebSocketTargetHandler) currentControlWriteTimeout() time.Duration {
	if h == nil {
		return webSocketControlWriteTimeout
	}
	return webSocketControlWriteTimeoutFrom(h.settings)
}

func webSocketRejectionMetadataLimit(stream webSocketTargetStream) int64 {
	type metadataLimiter interface{ MetadataLimit() int64 }
	if limited, ok := stream.(metadataLimiter); ok && limited.MetadataLimit() > 0 {
		return limited.MetadataLimit()
	}
	return wire.MaxMetadataBytes
}

func snapshotWebSocketRejection(response *http.Response, metadataLimit int64) (app.WebSocketRejection, error) {
	if response == nil || !wire.ValidWebSocketRejectionStatus(response.StatusCode) || metadataLimit <= 0 {
		return app.WebSocketRejection{}, fmt.Errorf("%w: invalid provider WebSocket rejection", ErrExecutionUnavailable)
	}
	result := app.WebSocketRejection{
		StatusCode: response.StatusCode,
		Header:     sanitizeWebSocketRejectionHeader(response.Header),
	}
	if response.Body == nil {
		return result, nil
	}
	// JSON encodes []byte as base64. Half the negotiated metadata budget keeps
	// the body read bounded while leaving room for the typed envelope/headers.
	bodyLimit := metadataLimit / 2
	body, err := io.ReadAll(io.LimitReader(response.Body, bodyLimit+1))
	if err != nil {
		return app.WebSocketRejection{}, err
	}
	if int64(len(body)) > bodyLimit {
		body = body[:bodyLimit]
		result.BodyTruncated = true
	}
	if response.ContentLength > int64(len(body)) {
		result.BodyTruncated = true
	}
	result.Body = body
	return result, nil
}

func sanitizeWebSocketRejectionHeader(source http.Header) http.Header {
	connectionNames := connectionHeaderNames(source)
	result := make(http.Header)
	for name, values := range source {
		lower := strings.ToLower(name)
		if unsafeRequestHeader(name, connectionNames) || lower == "content-length" ||
			strings.HasPrefix(lower, "sec-websocket-") || !validHeaderValues(values) {
			continue
		}
		result[http.CanonicalHeaderKey(name)] = append([]string(nil), values...)
	}
	return result
}

func (h *WebSocketTargetHandler) findRoute(open app.WebSocketOpen) (ServiceRoute, error) {
	if open.RequestID == "" || open.Path == "" || open.Hop != 1 || open.API.APIServiceID == 0 ||
		open.API.APIRouteID == 0 || open.API.Protocol != apiattempt.APIProtocolWebSocket ||
		open.API.Method != http.MethodGet || !open.API.TracePolicy.Valid() {
		return ServiceRoute{}, ErrExecutionUnavailable
	}
	route, err := h.finder.FindServiceRouteByID(open.API.APIServiceID, open.API.APIRouteID)
	if err != nil || route.Service.ID != open.API.APIServiceID || route.Route.ID != open.API.APIRouteID ||
		!routeSupportsProtocol(route.Route.Protocols, ProtocolWebSocket) {
		return ServiceRoute{}, ErrExecutionUnavailable
	}
	return route, nil
}

func (h *WebSocketTargetHandler) acquireLimiter(
	ctx context.Context,
	open app.WebSocketOpen,
	route ServiceRoute,
	upstreamID uint,
) (APIPermit, error) {
	if h.limiter == nil {
		return nil, nil
	}
	return h.limiter.Acquire(ctx, APIRequestFacts{
		UserID: open.API.UserID, GroupID: open.API.GroupID, TokenID: open.API.TokenID,
		APIServiceID: route.Service.ID, APIRouteID: route.Route.ID, APIUpstreamID: upstreamID, RequestID: open.RequestID,
		NoWait: true,
	})
}

func buildTargetWebSocketRequest(
	open app.WebSocketOpen,
	route protocol.SyncedAPIRoute,
	upstream protocol.SyncedAPIUpstream,
) (*url.URL, http.Header, error) {
	target, err := (UpstreamURLBuilder{}).Build(upstream, route.UpstreamPath, open.API.Subpath, open.API.RawQuery)
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
	header := (HeaderBuilder{}).BuildWebSocket(open.Header, upstream, upstream.Credential, route.WebSocketSubprotocols)
	if target.Host == "" {
		return nil, nil, fmt.Errorf("%w: empty WebSocket upstream host", genericapipath.ErrUnsafeUpstreamURL)
	}
	return target, header, nil
}
