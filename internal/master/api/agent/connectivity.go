package agent

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type ConnectivityRequest struct {
	ID                        string              `uri:"id" binding:"required"`
	Scope                     protocol.ProbeScope `json:"scope"`
	ExpectedEpoch             string              `json:"expected_epoch"`
	ExpectedControlGeneration uint64              `json:"expected_control_generation,omitempty"`
	ExpectedRelayGeneration   uint64              `json:"expected_relay_generation,omitempty"`
}

type ConnectivityProgressRequest struct {
	ID      string `uri:"id" binding:"required"`
	ProbeID string `form:"probe_id"`
}

// behavior change: POST enqueues the scheduler-backed probe and returns its full acknowledgement.
func (h *Handler) CheckConnectivity(c *app.Context, req ConnectivityRequest) (api.Accepted[ProbeAck], error) {
	if h.Operations == nil {
		return api.Accepted[ProbeAck]{}, api.InternalError("operation service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return api.Accepted[ProbeAck]{}, apiErr
	}

	sourceAgent, err := findAgentByID(c, req.ID)
	if err != nil {
		return api.Accepted[ProbeAck]{}, err
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return api.Accepted[ProbeAck]{}, apiErr
	}
	if req.Scope.Kind == "" {
		req.Scope = protocol.ProbeScope{Kind: "all_enabled"}
	}
	ack, err := h.Operations.EnqueueProbe(c.RequestContext(), protocol.OperationRequest{
		AgentID:                   sourceAgent.AgentID,
		Operation:                 string(connectivity.OperationProbe),
		ExpectedEpoch:             req.ExpectedEpoch,
		ExpectedControlGeneration: req.ExpectedControlGeneration,
		ExpectedRelayGeneration:   req.ExpectedRelayGeneration,
	}, req.Scope)
	if err != nil {
		// behavior change: cancellation during enqueue takes precedence over operation errors.
		if apiErr := requestContextAPIError(c); apiErr != nil {
			return api.Accepted[ProbeAck]{}, apiErr
		}
		return api.Accepted[ProbeAck]{}, operationAPIError(err, connectivity.OperationProbe)
	}
	return api.Accepted[ProbeAck]{Body: ack}, nil
}

func (h *Handler) GetConnectivity(c *app.Context, req ConnectivityProgressRequest) (ManualProbeProgress, error) {
	if h.GetProbeProgress == nil {
		return ManualProbeProgress{}, api.InternalError("probe progress service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return ManualProbeProgress{}, apiErr
	}
	if req.ProbeID == "" {
		return ManualProbeProgress{}, api.ErrorWithCode(http.StatusBadRequest, "probe_id_required", "probe_id is required", nil)
	}
	agent, err := findAgentByID(c, req.ID)
	if err != nil {
		return ManualProbeProgress{}, err
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return ManualProbeProgress{}, apiErr
	}
	progress, ok := h.GetProbeProgress(agent.AgentID, req.ProbeID)
	if !ok {
		return ManualProbeProgress{}, api.ErrorWithCode(http.StatusNotFound, "probe_not_found", "probe progress not found", nil)
	}
	return progress, nil
}
