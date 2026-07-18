package agent

import (
	"errors"
	"sync"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
)

const fullSyncMaxConcurrency = 16

type fullSyncTarget struct {
	agent models.Agent
}

func (h *Handler) FullSync(c *app.Context, req FullSyncRequest) (FullSyncResponse, error) {
	if h.Operations == nil {
		return FullSyncResponse{}, api.InternalError("connection service not available", nil)
	}
	if !req.All && len(req.AgentIDs) == 0 {
		return FullSyncResponse{}, api.BadRequestError("agent_ids or all=true is required", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return FullSyncResponse{}, apiErr
	}

	targetIDs := req.AgentIDs
	if req.All {
		if h.GetOnlineAgentIDs == nil {
			return FullSyncResponse{}, api.InternalError("online agent source not available", nil)
		}
		targetIDs = h.GetOnlineAgentIDs()
	}
	targetIDs = stableUniqueAgentIDs(targetIDs)
	if len(targetIDs) == 0 {
		return FullSyncResponse{Results: []FullSyncResult{}}, nil
	}

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	agents, err := q.Agent().ListByAgentIDs(targetIDs)
	if err != nil {
		return FullSyncResponse{}, api.InternalError("list agents failed", err)
	}
	byAgentID := make(map[string]models.Agent, len(agents))
	for _, agent := range agents {
		byAgentID[agent.AgentID] = agent
	}

	results := make([]FullSyncResult, len(targetIDs))
	targets := make([]fullSyncTarget, len(targetIDs))
	runnable := make([]int, 0, len(targetIDs))
	for i, agentID := range targetIDs {
		agent, ok := byAgentID[agentID]
		if !ok {
			results[i] = FullSyncResult{AgentID: agentID, Error: "agent is offline"}
			continue
		}
		operationReq := protocol.OperationRequest{AgentID: agent.AgentID, Operation: string(connectivity.OperationFullSync)}
		if err := h.Operations.Authorize(c.RequestContext(), operationReq); err != nil {
			return FullSyncResponse{}, operationAPIError(err, connectivity.OperationFullSync)
		}
		targets[i] = fullSyncTarget{agent: agent}
		runnable = append(runnable, i)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return FullSyncResponse{}, apiErr
	}
	if len(runnable) == 0 {
		return FullSyncResponse{Results: results}, nil
	}

	var staleOnce sync.Once
	var staleErr error
	var canceledOnce sync.Once
	var canceledErr *api.APIError
	workers := pool.New().WithMaxGoroutines(fullSyncMaxConcurrency)
	for _, index := range runnable {
		index := index
		workers.Go(func() {
			target := targets[index]
			if apiErr := requestContextAPIError(c); apiErr != nil {
				canceledOnce.Do(func() { canceledErr = apiErr })
				results[index] = FullSyncResult{AgentID: target.agent.AgentID, Error: apiErr.Message}
				return
			}

			response, err := h.Operations.ForceFullSync(c.RequestContext(), protocol.OperationRequest{AgentID: target.agent.AgentID})
			if err != nil {
				if errors.Is(err, connectivity.ErrConnectionGenerationChanged) {
					staleOnce.Do(func() { staleErr = err })
				}
				errorMessage := agentOperationFailedMessage
				if errors.Is(err, masteroperations.ErrInvalidAgentResponse) {
					errorMessage = invalidAgentResponseMessage
				}
				results[index] = FullSyncResult{AgentID: target.agent.AgentID, Error: errorMessage}
				return
			}
			results[index] = FullSyncResult{
				AgentID: target.agent.AgentID, Success: true,
				Version: response.Version, DurationMs: response.DurationMs,
			}
		})
	}
	workers.Wait()

	if staleErr != nil {
		return FullSyncResponse{}, operationAPIError(staleErr, connectivity.OperationFullSync)
	}
	if canceledErr != nil {
		return FullSyncResponse{}, canceledErr
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return FullSyncResponse{}, apiErr
	}
	return FullSyncResponse{Results: results}, nil
}

func stableUniqueAgentIDs(agentIDs []string) []string {
	unique := make([]string, 0, len(agentIDs))
	seen := make(map[string]struct{}, len(agentIDs))
	for _, agentID := range agentIDs {
		if _, ok := seen[agentID]; ok {
			continue
		}
		seen[agentID] = struct{}{}
		unique = append(unique, agentID)
	}
	return unique
}
