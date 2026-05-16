package agent

import (
	"encoding/json"
	gosync "sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func (h *Handler) FullSync(c *app.Context, req FullSyncRequest) (FullSyncResponse, error) {
	if h.HubCall == nil {
		return FullSyncResponse{}, api.InternalError("hub not available", nil)
	}

	if !req.All && len(req.AgentIDs) == 0 {
		return FullSyncResponse{}, api.BadRequestError("agent_ids or all=true is required", nil)
	}

	// Determine target agent IDs
	targetIDs := req.AgentIDs
	if req.All {
		targetIDs = h.GetOnlineAgentIDs()
	}

	// Build a set of online agent IDs for quick lookup
	onlineSet := make(map[string]struct{})
	for _, id := range h.GetOnlineAgentIDs() {
		onlineSet[id] = struct{}{}
	}

	results := make([]FullSyncResult, len(targetIDs))
	var wg gosync.WaitGroup

	for i, agentID := range targetIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()
			if _, online := onlineSet[id]; !online {
				results[idx] = FullSyncResult{
					AgentID: id,
					Success: false,
					Error:   "agent is offline",
				}
				return
			}

			raw, err := h.HubCall(id, consts.RPCSyncForceFullSync, nil, 30*time.Second)
			if err != nil {
				results[idx] = FullSyncResult{
					AgentID: id,
					Success: false,
					Error:   err.Error(),
				}
				return
			}

			var resp protocol.ForceFullSyncResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				results[idx] = FullSyncResult{
					AgentID: id,
					Success: false,
					Error:   "invalid response: " + err.Error(),
				}
				return
			}

			results[idx] = FullSyncResult{
				AgentID:    id,
				Success:    true,
				Version:    resp.Version,
				DurationMs: resp.DurationMs,
			}
		}(i, agentID)
	}

	wg.Wait()
	return FullSyncResponse{Results: results}, nil
}
