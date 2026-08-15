package genericapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gorilla/websocket"
)

type RemoteWebSocketHandlerOptions struct {
	Direct         agentproxy.DirectWebSocketAPIStreamOpener
	Relay          app.WebSocketAPIStreamOpener
	TargetSupports func(string) bool
	GlobalProxy    string
	AddressTag     string
	PreferredTag   string
	Settings       SettingsFinder
}

// RemoteWebSocketHandler opens a typed stream to the already frozen execution
// Agent. It may fall back only when Direct target preparation fails. Once the
// Direct opener is called, that transport choice is terminal because the
// remote side may already have committed work.
type RemoteWebSocketHandler struct {
	options RemoteWebSocketHandlerOptions
	bridge  WebSocketBridge
}

func NewRemoteWebSocketHandler(options RemoteWebSocketHandlerOptions) *RemoteWebSocketHandler {
	return &RemoteWebSocketHandler{options: options}
}

func (h *RemoteWebSocketHandler) Serve(ctx context.Context, rc *RequestContext) error {
	if ctx == nil || h == nil || rc == nil || rc.Context == nil ||
		rc.Context.Request == nil || rc.Context.Writer == nil || rc.Agent.ExecutionAgentID == "" {
		return ErrExecutionUnavailable
	}
	if rc.Agent.Target.AgentID != "" && rc.Agent.Target.AgentID != rc.Agent.ExecutionAgentID {
		return ErrExecutionUnavailable
	}
	if h.options.TargetSupports != nil && !h.options.TargetSupports(rc.Agent.ExecutionAgentID) {
		return ErrExecutionUnavailable
	}
	stream, path, err := h.open(ctx, rc, buildRemoteWebSocketOpen(ctx, rc))
	if err != nil || stream == nil {
		var executionErr *app.WebSocketExecutionError
		if errors.As(err, &executionErr) {
			rc.Execution = executionErr.Result
			rc.UpstreamName = executionErr.Result.APIUpstreamName
		}
		if err == nil {
			err = ErrExecutionUnavailable
		}
		return markAPIInfrastructureFailure(err)
	}
	defer stream.Close()
	rc.Agent.AgentRoutePath = path

	accepted := stream.ProviderAcceptance()
	if accepted.ProviderStatus != http.StatusSwitchingProtocols {
		return h.writeProviderRejection(ctx, rc, stream, accepted)
	}
	if !acceptedWebSocketSubprotocol(rc.Context.Request, rc.Route.WebSocketSubprotocols, accepted.Subprotocol) {
		return ErrExecutionUnavailable
	}
	responseHeader := make(http.Header)
	responseHeader.Set(consts.HeaderXRequestID, rc.RequestID)
	if accepted.Subprotocol != "" {
		responseHeader.Set("Sec-WebSocket-Protocol", accepted.Subprotocol)
	}
	upgrader := websocket.Upgrader{EnableCompression: false}
	client, err := upgrader.Upgrade(rc.Context.Writer, rc.Context.Request, responseHeader)
	if err != nil {
		return err
	}
	rc.ClientUpgradeCommitted = true
	rc.ClientStatusCode = http.StatusSwitchingProtocols
	bridge := h.bridge.withControlWriteTimeout(h.currentControlWriteTimeout)
	bridgeResult, bridgeErr := bridge.ConnectionAndStreamWithResult(ctx, client, stream)
	resultCtx, cancel := webSocketControlContext(ctx, h.options.Settings)
	defer cancel()
	result, resultErr := stream.ReceiveResult(resultCtx)
	if resultErr != nil {
		rc.Execution.ProviderDispatchKnown = false
		rc.Execution.WebSocketCloseCode = bridgeResult.CloseCode
		rc.Execution.ErrorStage, rc.Execution.ErrorCode = "transport", ErrorCode(resultErr)
		return nil
	}
	rc.Execution = result
	rc.UpstreamName = result.APIUpstreamName
	if rc.Execution.WebSocketCloseCode == 0 {
		rc.Execution.WebSocketCloseCode = bridgeResult.CloseCode
	}
	if bridgeErr != nil && rc.Execution.ErrorStage == "" {
		rc.Execution.ErrorStage, rc.Execution.ErrorCode = "transport", ErrorCode(bridgeErr)
	}
	return nil
}

func (h *RemoteWebSocketHandler) writeProviderRejection(
	ctx context.Context,
	rc *RequestContext,
	stream app.WebSocketAPIStream,
	accepted app.WebSocketAccepted,
) error {
	if accepted.Rejection == nil || !wire.ValidWebSocketRejectionStatus(accepted.ProviderStatus) ||
		accepted.Rejection.StatusCode != accepted.ProviderStatus {
		return ErrExecutionUnavailable
	}
	resultCtx, cancel := webSocketControlContext(ctx, h.options.Settings)
	defer cancel()
	result, err := stream.ReceiveResult(resultCtx)
	if err != nil {
		return markAPIInfrastructureFailure(err)
	}
	if result.UpstreamStatus != accepted.Rejection.StatusCode {
		return ErrExecutionUnavailable
	}
	rc.Execution = result
	rc.UpstreamName = result.APIUpstreamName
	rc.ClientStatusCode = accepted.Rejection.StatusCode
	header := sanitizeWebSocketRejectionHeader(http.Header(accepted.Rejection.Header))
	if accepted.Rejection.HeaderTruncated {
		header.Set("X-Vaala-Websocket-Rejection-Headers-Truncated", "true")
	}
	if accepted.Rejection.BodyTruncated {
		header.Set("X-Vaala-Websocket-Rejection-Body-Truncated", "true")
	}
	for name, values := range header {
		for _, value := range values {
			rc.Context.Writer.Header().Add(name, value)
		}
	}
	rc.Context.Writer.WriteHeader(accepted.Rejection.StatusCode)
	if len(accepted.Rejection.Body) == 0 {
		return nil
	}
	_, err = rc.Context.Writer.Write(accepted.Rejection.Body)
	return err
}

func (h *RemoteWebSocketHandler) currentControlWriteTimeout() time.Duration {
	if h == nil {
		return webSocketControlWriteTimeout
	}
	return webSocketControlWriteTimeoutFrom(h.options.Settings)
}

func acceptedWebSocketSubprotocol(request *http.Request, allowed []string, accepted string) bool {
	if accepted == "" {
		return true
	}
	clientOffered := false
	for _, offered := range websocket.Subprotocols(request) {
		if offered == accepted {
			clientOffered = true
			break
		}
	}
	if !clientOffered {
		return false
	}
	for _, protocol := range allowed {
		if protocol == accepted {
			return true
		}
	}
	return false
}

func (h *RemoteWebSocketHandler) open(
	ctx context.Context,
	rc *RequestContext,
	open app.WebSocketOpen,
) (app.WebSocketAPIStream, app.RoutePath, error) {
	target := rc.Agent.Target
	if target.DirectInboundEnabled && h.options.Direct != nil {
		prepared, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
			AgentID: target.AgentID, HTTPAddresses: target.HTTPAddresses, AgentProxyURL: target.ProxyURL,
			GlobalProxyURL: h.options.GlobalProxy, AddressTag: h.options.AddressTag, PreferredTag: h.options.PreferredTag,
		})
		if err == nil {
			stream, openErr := h.options.Direct.OpenWebSocketAPIStream(ctx, prepared, open)
			if openErr != nil {
				return nil, app.RoutePathDirect, openErr
			}
			if stream == nil {
				return nil, app.RoutePathDirect, ErrExecutionUnavailable
			}
			return stream, app.RoutePathDirect, nil
		}
	}
	if !target.RelayInboundEnabled && target.AgentID != "" || h.options.Relay == nil {
		return nil, app.RoutePathRelay, ErrExecutionUnavailable
	}
	stream, err := h.options.Relay.OpenWebSocketAPIStream(ctx, open)
	if err != nil || stream == nil {
		if err == nil {
			err = ErrExecutionUnavailable
		}
		return nil, app.RoutePathRelay, err
	}
	return stream, app.RoutePathRelay, nil
}

func buildRemoteWebSocketOpen(ctx context.Context, rc *RequestContext) app.WebSocketOpen {
	request := rc.Context.Request
	remaining := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
	}
	header := request.Header.Clone()
	selected := selectedWebSocketSubprotocol(header, rc.Route.WebSocketSubprotocols)
	for _, name := range []string{
		"Connection", "Upgrade", "Sec-WebSocket-Key", "Sec-WebSocket-Accept",
		"Sec-WebSocket-Version", "Sec-WebSocket-Extensions",
	} {
		header.Del(name)
	}
	header.Del("Sec-WebSocket-Protocol")
	if selected != "" {
		header.Set("Sec-WebSocket-Protocol", selected)
	}
	return app.WebSocketOpen{
		TargetAgentID: rc.Agent.ExecutionAgentID, RouteID: rc.Agent.AgentRouteID, RequestID: rc.RequestID,
		Path: request.URL.Path, Header: header, Remaining: remaining, Hop: 1,
		API: apiattempt.APIAttemptMeta{
			APIServiceID: rc.Service.ID, APIRouteID: rc.Route.ID, Protocol: apiattempt.APIProtocolWebSocket,
			UserID: rc.UserID, GroupID: rc.GroupID, TokenID: rc.TokenID,
			Method: http.MethodGet, Subpath: rc.Subpath, RawQuery: request.URL.RawQuery, TracePolicy: rc.TracePolicy,
		},
	}
}
