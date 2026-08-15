package genericapi

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// RequestExecutor executes one request after its service, route, and execution
// Agent have been frozen by the source pipeline.
type RequestExecutor interface {
	Execute(context.Context, *RequestContext) error
}

// Executor dispatches requests by their registered transport protocol.
type Executor struct {
	handlers map[string]ProtocolHandler
}

func NewExecutor(handlers map[string]ProtocolHandler) *Executor {
	registered := make(map[string]ProtocolHandler, len(handlers))
	for protocol, handler := range handlers {
		if protocol != "" && handler != nil {
			registered[protocol] = handler
		}
	}
	return &Executor{handlers: registered}
}

func (e *Executor) Execute(ctx context.Context, request *RequestContext) error {
	if ctx == nil || e == nil || request == nil {
		return ErrExecutionUnavailable
	}
	handler := e.handlers[request.Protocol]
	if handler == nil {
		return ErrExecutionUnavailable
	}
	return handler.Serve(ctx, request)
}

// ExecutionRouter chooses only the transport owner for an already frozen
// execution Agent. Neither branch may select an Agent again.
type ExecutionRouter struct {
	sourceAgentID string
	local         ProtocolHandler
	remote        ProtocolHandler
}

func NewExecutionRouter(sourceAgentID string, local, remote ProtocolHandler) *ExecutionRouter {
	return &ExecutionRouter{sourceAgentID: sourceAgentID, local: local, remote: remote}
}

func (r *ExecutionRouter) Serve(ctx context.Context, request *RequestContext) error {
	if ctx == nil || r == nil || r.sourceAgentID == "" || request == nil || request.Agent.ExecutionAgentID == "" {
		return ErrExecutionUnavailable
	}
	if request.Agent.ExecutionAgentID == r.sourceAgentID {
		if r.local == nil {
			return ErrExecutionUnavailable
		}
		request.Agent.AgentRoutePath = app.RoutePathLocal
		return r.local.Serve(ctx, request)
	}
	if r.remote == nil {
		return ErrExecutionUnavailable
	}
	return r.remote.Serve(ctx, request)
}
