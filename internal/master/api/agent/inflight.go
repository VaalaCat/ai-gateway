package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AgentIDQuery uses ?id= query parameter (no dynamic route segment per project convention).
type AgentIDQuery struct {
	ID string `form:"id" binding:"required"`
}

// GetInflight fetches the in-flight request snapshot from a remote agent.
func (h *Handler) GetInflight(c *app.Context, req AgentIDQuery) (json.RawMessage, error) {
	return h.callDiagnostic(c, req, consts.RPCAgentInflight, api.DiagnosticNodeTimeout)
}

// GetGoroutines fetches a goroutine dump from a remote agent (admin only).
func (h *Handler) GetGoroutines(c *app.Context, req AgentIDQuery) (json.RawMessage, error) {
	return h.callDiagnostic(c, req, consts.RPCAgentGoroutines, 15*time.Second)
}

// callDiagnostic 查询节点连接，并在单节点预算内读取诊断结果。
func (h *Handler) callDiagnostic(c *app.Context, req AgentIDQuery, method string, timeout time.Duration) (json.RawMessage, error) {
	if h.Connections == nil || h.HubCallSession == nil {
		return nil, api.InternalError("connection service not available", nil)
	}
	if err := c.RequestContext().Err(); err != nil {
		return nil, api.DiagnosticError(c.Logger, "", method, time.Now(), err)
	}
	// 1. 单节点预算覆盖查询、连接快照和 RPC。
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.RequestContext(), timeout)
	defer cancel()
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	agent, err := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx)).Agent().GetByID(uint(id))
	if err != nil {
		if ctx.Err() != nil {
			return nil, api.DiagnosticError(c.Logger, "", method, started, ctx.Err())
		}
		return nil, api.NotFoundError("agent not found")
	}
	snapshot, err := h.Connections.BuildContext(ctx, *agent)
	if err != nil {
		if ctx.Err() != nil {
			return nil, api.DiagnosticError(c.Logger, agent.AgentID, method, started, ctx.Err())
		}
		if apiErr := requestContextAPIError(c); apiErr != nil {
			return nil, apiErr
		}
		return nil, api.InternalError("build connection snapshot failed", err)
	}
	if snapshot.Control.State != "connected" || snapshot.Control.SessionGeneration == 0 {
		return nil, controlDisconnectedAPIError()
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return nil, apiErr
	}
	result, err := h.HubCallSession(ctx, agent.AgentID, snapshot.Control.SessionGeneration, method, nil, timeout)
	if err != nil {
		return nil, api.DiagnosticError(c.Logger, agent.AgentID, method, started, err)
	}
	// 2. 检查在途响应，空列表仍属于成功。
	if method == consts.RPCAgentInflight {
		var snapshots []inflight.Snapshot
		if err := json.Unmarshal(result, &snapshots); err != nil {
			return nil, api.DiagnosticError(c.Logger, agent.AgentID, method, started, fmt.Errorf("%w: %v", controlrpc.ErrInvalidResponse, err))
		}
	}
	return result, nil
}
