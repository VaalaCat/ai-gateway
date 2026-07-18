package agent

import (
	"encoding/json"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AgentIDQuery uses ?id= query parameter (no dynamic route segment per project convention).
type AgentIDQuery struct {
	ID string `form:"id" binding:"required"`
}

// GetInflight fetches the in-flight request snapshot from a remote agent.
func (h *Handler) GetInflight(c *app.Context, req AgentIDQuery) (json.RawMessage, error) {
	return h.callDiagnostic(c, req, consts.RPCAgentInflight, 10*time.Second)
}

// GetGoroutines fetches a goroutine dump from a remote agent (admin only).
func (h *Handler) GetGoroutines(c *app.Context, req AgentIDQuery) (json.RawMessage, error) {
	return h.callDiagnostic(c, req, consts.RPCAgentGoroutines, 15*time.Second)
}

func (h *Handler) callDiagnostic(c *app.Context, req AgentIDQuery, method string, timeout time.Duration) (json.RawMessage, error) {
	if h.Connections == nil || h.HubCallSession == nil {
		return nil, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return nil, apiErr
	}
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	agent, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext())).Agent().GetByID(uint(id))
	if err != nil {
		return nil, api.NotFoundError("agent not found")
	}
	snapshot := h.Connections.Build(*agent)
	if snapshot.Control.State != "connected" || snapshot.Control.SessionGeneration == 0 {
		return nil, controlDisconnectedAPIError()
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return nil, apiErr
	}
	result, err := h.HubCallSession(agent.AgentID, snapshot.Control.SessionGeneration, method, nil, timeout)
	if err != nil {
		return nil, operationAPIError(err, "")
	}
	return result, nil
}
