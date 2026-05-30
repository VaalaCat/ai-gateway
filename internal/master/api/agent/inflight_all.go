package agent

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc/pool"
)

// GlobalInflightRow 是单条在途请求，附带节点标识。
type GlobalInflightRow struct {
	inflight.Snapshot
	AgentID   uint   `json:"agent_id"`
	AgentName string `json:"agent_name"`
}

// FailedAgent 记录某节点拉取失败的原因。
type FailedAgent struct {
	AgentID   uint   `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Error     string `json:"error"`
}

// AllInflightResponse 是 GetAllInflight 的响应体。
type AllInflightResponse struct {
	Requests     []GlobalInflightRow `json:"requests"`
	FailedAgents []FailedAgent       `json:"failed_agents"`
}

type nodeResult struct {
	rows   []GlobalInflightRow
	failed *FailedAgent
}

// GetAllInflight 并发拉取所有在线节点的 agent.inflight，合并成扁平列表；
// 某节点出错/超时进 failed_agents，不阻塞其余节点。
func (h *Handler) GetAllInflight(c *app.Context, _ api.EmptyRequest) (AllInflightResponse, error) {
	if h.HubCall == nil || h.GetOnlineAgentIDs == nil {
		return AllInflightResponse{}, api.InternalError("hub not available", nil)
	}
	resp := AllInflightResponse{
		Requests:     []GlobalInflightRow{},
		FailedAgents: []FailedAgent{},
	}

	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		return resp, nil
	}

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		return AllInflightResponse{}, api.InternalError("list agents failed", err)
	}

	byUID := make(map[string]models.Agent, len(agents))
	for _, a := range agents {
		byUID[a.AgentID] = a
	}

	p := pool.NewWithResults[nodeResult]().WithMaxGoroutines(16)
	for _, uid := range ids {
		ag := byUID[uid]
		p.Go(func() nodeResult {
			raw, err := h.HubCall(uid, consts.RPCAgentInflight, nil, 5*time.Second)
			if err != nil {
				return nodeResult{failed: &FailedAgent{
					AgentID:   ag.ID,
					AgentName: ag.Name,
					Error:     err.Error(),
				}}
			}
			var snaps []inflight.Snapshot
			if err := json.Unmarshal(raw, &snaps); err != nil {
				return nodeResult{failed: &FailedAgent{
					AgentID:   ag.ID,
					AgentName: ag.Name,
					Error:     "decode: " + err.Error(),
				}}
			}
			rows := make([]GlobalInflightRow, 0, len(snaps))
			for _, s := range snaps {
				rows = append(rows, GlobalInflightRow{
					Snapshot:  s,
					AgentID:   ag.ID,
					AgentName: ag.Name,
				})
			}
			return nodeResult{rows: rows}
		})
	}

	for _, r := range p.Wait() {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
		}
		resp.Requests = append(resp.Requests, r.rows...)
	}
	return resp, nil
}

// InterruptRequest 指定要打断的节点 DB id 和在途请求 id。
type InterruptRequest struct {
	AgentID uint  `json:"agent_id" binding:"required"`
	ID      int64 `json:"id" binding:"required"`
}

// InterruptResponse 反映打断是否成功。
type InterruptResponse struct {
	Interrupted bool `json:"interrupted"`
}

// Interrupt 把打断指令转发给目标节点的 agent.interrupt RPC。
func (h *Handler) Interrupt(c *app.Context, req InterruptRequest) (InterruptResponse, error) {
	if h.HubCall == nil {
		return InterruptResponse{}, api.InternalError("hub not available", nil)
	}
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	ag, err := q.Agent().GetByID(req.AgentID)
	if err != nil {
		return InterruptResponse{}, api.NotFoundError("agent not found")
	}
	raw, err := h.HubCall(ag.AgentID, consts.RPCAgentInterrupt, map[string]any{"id": req.ID}, 10*time.Second)
	if err != nil {
		return InterruptResponse{}, api.BadRequestError(fmt.Sprintf("interrupt failed: %v", err), nil)
	}
	var res InterruptResponse
	if err := json.Unmarshal(raw, &res); err != nil {
		return InterruptResponse{}, api.InternalError("invalid interrupt response", err)
	}
	return res, nil
}
