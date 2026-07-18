package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type FullSyncOperator interface {
	FullSync(ctx context.Context) error
}

type RelayReconnectOperator interface {
	Reconnect(ctx context.Context) error
}

type DirectCircuitOperator interface {
	ResetCircuit(targetAgentID, addressFingerprint string)
}

type AgentOperationSources struct {
	Syncer   FullSyncOperator
	Tunnel   RelayReconnectOperator
	Circuits DirectCircuitOperator
	Inflight *inflight.Registry
}

type agentOperationFunc func(context.Context, protocol.OperationRequest) error

type AgentOperationHandler struct {
	handlers map[connectivity.Operation]agentOperationFunc
}

func NewAgentOperationHandler(sources AgentOperationSources) *AgentOperationHandler {
	handler := &AgentOperationHandler{}
	handler.handlers = map[connectivity.Operation]agentOperationFunc{
		connectivity.OperationFullSync: func(ctx context.Context, _ protocol.OperationRequest) error {
			if sources.Syncer == nil {
				return errors.New("full sync operator is not available")
			}
			return sources.Syncer.FullSync(ctx)
		},
		connectivity.OperationRelayReconnect: func(ctx context.Context, _ protocol.OperationRequest) error {
			if sources.Tunnel == nil {
				return errors.New("relay reconnect operator is not available")
			}
			return sources.Tunnel.Reconnect(ctx)
		},
		connectivity.OperationDirectCircuitReset: func(_ context.Context, req protocol.OperationRequest) error {
			if req.TargetAgentID == "" {
				return errors.New("target_agent_id is required")
			}
			if sources.Circuits == nil {
				return errors.New("direct circuit operator is not available")
			}
			sources.Circuits.ResetCircuit(req.TargetAgentID, "")
			return nil
		},
		connectivity.OperationInterrupt: func(_ context.Context, req protocol.OperationRequest) error {
			id, err := strconv.ParseInt(req.RequestID, 10, 64)
			if err != nil || id <= 0 {
				return errors.New("request_id must be a positive integer")
			}
			if sources.Inflight == nil {
				return errors.New("inflight registry is not available")
			}
			sources.Inflight.Interrupt(id)
			return nil
		},
	}
	return handler
}

func (h *AgentOperationHandler) Handle(ctx context.Context, params json.RawMessage) (any, error) {
	if ctx == nil {
		return nil, errors.New("agent operation: nil context")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	var req protocol.OperationRequest
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, fmt.Errorf("invalid operation params: %w", err)
	}
	operation := connectivity.Operation(req.Operation)
	handler, ok := h.handlers[operation]
	if !ok {
		return nil, fmt.Errorf("unsupported agent operation %q", req.Operation)
	}
	if err := handler(ctx, req); err != nil {
		return nil, err
	}
	return protocol.OperationAck{
		OperationID: req.RequestID,
		State:       "accepted",
	}, nil
}
