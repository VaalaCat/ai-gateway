package agent

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type AgentOperationRequest struct {
	ID     string `uri:"id" binding:"required"`
	Action string `uri:"operation" binding:"required"`
	protocol.OperationRequest
}

type AgentOperationResponse struct {
	protocol.OperationAck
}

func (AgentOperationResponse) StatusCode() int { return http.StatusAccepted }

type expectedGenerationKind uint8

const (
	expectedControlGeneration expectedGenerationKind = iota
	expectedRelayGeneration
)

var agentOperationGenerationRequirements = map[connectivity.Operation]expectedGenerationKind{
	connectivity.OperationFullSync:           expectedControlGeneration,
	connectivity.OperationProbe:              expectedControlGeneration,
	connectivity.OperationRelayReconnect:     expectedControlGeneration,
	connectivity.OperationDirectCircuitReset: expectedControlGeneration,
	connectivity.OperationInterrupt:          expectedControlGeneration,
	connectivity.OperationRelayDrain:         expectedRelayGeneration,
	connectivity.OperationRelayDisconnect:    expectedRelayGeneration,
}

func (h *Handler) Operation(c *app.Context, req AgentOperationRequest) (AgentOperationResponse, error) {
	if h.Operations == nil {
		return AgentOperationResponse{}, api.InternalError("operation service not available", nil)
	}
	req.OperationRequest.Operation = req.Action
	operation := connectivity.Operation(req.Action)
	requirement, ok := agentOperationGenerationRequirements[operation]
	if !ok {
		return AgentOperationResponse{}, invalidOperationRequest(operation, "the requested operation is invalid")
	}
	if req.ExpectedEpoch == "" {
		return AgentOperationResponse{}, invalidOperationRequest(operation, "expected_epoch is required")
	}
	if requirement == expectedControlGeneration && req.ExpectedControlGeneration == 0 {
		return AgentOperationResponse{}, invalidOperationRequest(operation, "expected_control_generation is required")
	}
	if requirement == expectedRelayGeneration && req.ExpectedRelayGeneration == 0 {
		return AgentOperationResponse{}, invalidOperationRequest(operation, "expected_relay_generation is required")
	}
	agent, err := findAgentByID(c, req.ID)
	if err != nil {
		return AgentOperationResponse{}, err
	}
	// behavior change: URL IDs are database keys; control operations use the stored logical AgentID.
	req.OperationRequest.AgentID = agent.AgentID
	ack, err := h.Operations.Execute(c.RequestContext(), req.OperationRequest)
	if err != nil {
		return AgentOperationResponse{}, operationAPIError(err, operation)
	}
	return AgentOperationResponse{OperationAck: ack}, nil
}

func invalidOperationRequest(operation connectivity.Operation, message string) *api.APIError {
	return &api.APIError{
		Status: http.StatusBadRequest, Code: "operation_invalid", Message: message,
		Details: map[string]any{"operation": operation},
	}
}
