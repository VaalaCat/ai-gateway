package genericapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/sourcegraph/conc/pool"
)

var errRemoteHTTPResultComplete = errors.New("remote HTTP API result received")

type RemoteHTTPHandlerOptions struct {
	Direct       agentproxy.DirectHTTPAPITransportBuilder
	Relay        app.HTTPAPIStreamOpener
	GlobalProxy  string
	AddressTag   string
	PreferredTag string
}

type RemoteHTTPHandler struct{ options RemoteHTTPHandlerOptions }

func NewRemoteHTTPHandler(options RemoteHTTPHandlerOptions) *RemoteHTTPHandler {
	return &RemoteHTTPHandler{options: options}
}

func (h *RemoteHTTPHandler) Serve(ctx context.Context, rc *RequestContext) error {
	if ctx == nil || h == nil || rc == nil || rc.Context == nil || rc.Context.Request == nil ||
		rc.Agent.ExecutionAgentID == "" || rc.Agent.Target.AgentID != rc.Agent.ExecutionAgentID {
		return ErrExecutionUnavailable
	}
	if err := enableLocalHTTPFullDuplex(rc.Context.Writer); err != nil {
		return err
	}
	open := buildRemoteAPIOpen(ctx, rc)
	stream, path, err := h.open(ctx, rc, open)
	rc.Agent.AgentRoutePath = path
	if err != nil {
		return markAPIInfrastructureFailure(err)
	}
	defer stream.Close()
	result, err := runRemoteHTTPStream(ctx, stream, rc.Context.Request, rc.Context.Writer)
	if result != nil {
		rc.Execution = *result
		rc.UpstreamName = result.APIUpstreamName
	}
	return err
}

func (h *RemoteHTTPHandler) open(ctx context.Context, rc *RequestContext, open app.APIOpen) (app.HTTPAPIStream, app.RoutePath, error) {
	target := rc.Agent.Target
	if target.DirectInboundEnabled && h.options.Direct != nil {
		prepared, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
			AgentID: target.AgentID, HTTPAddresses: target.HTTPAddresses, AgentProxyURL: target.ProxyURL,
			GlobalProxyURL: h.options.GlobalProxy, AddressTag: h.options.AddressTag, PreferredTag: h.options.PreferredTag,
		})
		if err == nil {
			transport, buildErr := h.options.Direct.BuildDirectHTTPAPITransport(ctx, prepared)
			if buildErr == nil && transport != nil {
				reservation, acquireErr := transport.AcquireHTTPAPIStream(ctx)
				if acquireErr == nil && reservation != nil {
					defer reservation.Release()
					stream, openErr := reservation.OpenHTTPAPIStream(ctx, open)
					if openErr != nil {
						return nil, app.RoutePathDirect, openErr
					}
					return stream, app.RoutePathDirect, nil
				}
				if reservation != nil {
					reservation.Release()
				}
			}
		}
	}
	if !target.RelayInboundEnabled || h.options.Relay == nil {
		return nil, app.RoutePathRelay, ErrExecutionUnavailable
	}
	stream, err := h.options.Relay.OpenHTTPAPIStream(ctx, open)
	if err != nil || stream == nil {
		if err == nil {
			err = ErrExecutionUnavailable
		}
		return nil, app.RoutePathRelay, err
	}
	return stream, app.RoutePathRelay, nil
}

func buildRemoteAPIOpen(ctx context.Context, rc *RequestContext) app.APIOpen {
	request := rc.Context.Request
	remaining := time.Duration(0)
	if deadline, ok := ctx.Deadline(); ok {
		remaining = time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
	}
	trailerKeys := make([]string, 0, len(request.Trailer))
	for key := range request.Trailer {
		trailerKeys = append(trailerKeys, key)
	}
	return app.APIOpen{
		TargetAgentID: rc.Agent.ExecutionAgentID, RouteID: rc.Agent.AgentRouteID, RequestID: rc.RequestID,
		Method: request.Method, Path: request.URL.Path, Header: request.Header.Clone(), BodyLength: request.ContentLength,
		Remaining: remaining, Hop: 1, API: apiattempt.APIAttemptMeta{
			APIServiceID: rc.Service.ID, APIRouteID: rc.Route.ID, Protocol: apiattempt.APIProtocolHTTP,
			UserID: rc.UserID, GroupID: rc.GroupID, TokenID: rc.TokenID,
			Method: request.Method, Subpath: rc.Subpath, RawQuery: request.URL.RawQuery, RequestTrailerKeys: trailerKeys,
			TracePolicy: rc.TracePolicy,
		},
	}
}

func runRemoteHTTPStream(ctx context.Context, stream app.HTTPAPIStream, request *http.Request, writer http.ResponseWriter) (*apiattempt.APIExecutionResult, error) {
	if stream == nil || request == nil || writer == nil {
		return nil, ErrExecutionUnavailable
	}
	results := make(chan *apiattempt.APIExecutionResult, 1)
	workers := pool.New().WithContext(ctx).WithCancelOnError().WithFirstError()
	workers.Go(func(workerCtx context.Context) error { return sendRemoteHTTPRequest(workerCtx, stream, request) })
	workers.Go(func(workerCtx context.Context) error {
		result, err := receiveRemoteHTTPResponse(workerCtx, stream, writer)
		if result != nil {
			results <- result
			if request.Body != nil {
				_ = request.Body.Close()
			}
			stream.Cancel(errRemoteHTTPResultComplete)
			return errRemoteHTTPResultComplete
		}
		return err
	})
	err := workers.Wait()
	select {
	case result := <-results:
		return result, nil
	default:
		return nil, err
	}
}

func sendRemoteHTTPRequest(ctx context.Context, stream app.HTTPAPIStream, request *http.Request) error {
	body := request.Body
	if body == nil {
		body = http.NoBody
	}
	defer body.Close()
	buffer := make([]byte, 32<<10)
	for {
		count, err := body.Read(buffer)
		if count > 0 {
			if sendErr := stream.SendRequestData(ctx, buffer[:count]); sendErr != nil {
				return markAPIInfrastructureFailure(sendErr)
			}
		}
		if errors.Is(err, io.EOF) {
			return markAPIInfrastructureFailure(stream.EndRequest(ctx, wire.Trailers{Header: request.Trailer.Clone()}))
		}
		if err != nil {
			stream.Cancel(err)
			return err
		}
	}
}

func receiveRemoteHTTPResponse(ctx context.Context, stream app.HTTPAPIStream, writer http.ResponseWriter) (*apiattempt.APIExecutionResult, error) {
	for {
		event, err := stream.Receive(ctx)
		if err != nil {
			return nil, markAPIInfrastructureFailure(err)
		}
		switch event.Kind {
		case app.APIResponseHeaders:
			if event.Headers == nil {
				return nil, markAPIInfrastructureFailure(ErrExecutionUnavailable)
			}
			copyHTTPHeader(writer.Header(), http.Header(event.Headers.Header))
			for key := range event.Headers.Trailer {
				writer.Header().Add("Trailer", key)
			}
			writer.WriteHeader(event.Headers.StatusCode)
		case app.APIResponseData:
			if _, err = writer.Write(event.Data); err != nil {
				stream.Cancel(err)
				return nil, err
			}
			if flusher, ok := writer.(http.Flusher); ok {
				flusher.Flush()
			}
		case app.APIResponseEnd:
			if event.Trailers != nil {
				copyHTTPHeader(writer.Header(), http.Header(event.Trailers.Header))
			}
		case app.APIResponseResult:
			if event.Result == nil || event.Result.Validate() != nil {
				return nil, markAPIInfrastructureFailure(ErrExecutionUnavailable)
			}
			return event.Result, nil
		}
	}
}

func copyHTTPHeader(target, source http.Header) {
	for key, values := range source {
		target[key] = append([]string(nil), values...)
	}
}
