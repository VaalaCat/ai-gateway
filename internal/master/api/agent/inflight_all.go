package agent

import (
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
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
	if h.Connections == nil || h.HubCallSession == nil || h.GetOnlineAgentIDs == nil {
		return AllInflightResponse{}, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return AllInflightResponse{}, apiErr
	}
	resp := AllInflightResponse{
		Requests:     []GlobalInflightRow{},
		FailedAgents: []FailedAgent{},
	}

	ids := stableUniqueAgentIDs(h.GetOnlineAgentIDs())
	if len(ids) == 0 {
		return resp, nil
	}

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		return AllInflightResponse{}, api.InternalError("list agents failed", err)
	}

	byUID := make(map[string]models.Agent, len(agents))
	for _, a := range agents {
		byUID[a.AgentID] = a
	}
	h.enrichLastSeen(agents)
	batch := h.Connections.BuildMany(agents)
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return AllInflightResponse{}, apiErr
	}

	results := make([]nodeResult, len(ids))
	jobs := make(chan int)
	workerCount := len(ids)
	if workerCount > 16 {
		workerCount = 16
	}
	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for index := range jobs {
				uid := ids[index]
				ag, ok := byUID[uid]
				if !ok {
					results[index] = nodeResult{failed: &FailedAgent{Error: "agent_not_found"}}
					continue
				}
				snapshot, ok := batch.Items[uid]
				if !ok {
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID: ag.ID, AgentName: ag.Name, Error: "connection_snapshot_unavailable",
					}}
					continue
				}
				if snapshot.Control.State != "connected" || snapshot.Control.SessionGeneration == 0 {
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID: ag.ID, AgentName: ag.Name, Error: connectivity.DenialControlDisconnected,
					}}
					continue
				}
				if apiErr := requestContextAPIError(c); apiErr != nil {
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID: ag.ID, AgentName: ag.Name, Error: apiErr.Code,
					}}
					continue
				}

				raw, err := h.HubCallSession(uid, snapshot.Control.SessionGeneration, consts.RPCAgentInflight, nil, 5*time.Second)
				if err != nil {
					message := err.Error()
					if errors.Is(err, connectivity.ErrConnectionGenerationChanged) {
						message = connectivity.ErrorCodeConnectionGenerationChanged
					} else {
						message = agentOperationFailedMessage
					}
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID:   ag.ID,
						AgentName: ag.Name,
						Error:     message,
					}}
					continue
				}
				var snaps []inflight.Snapshot
				if err := json.Unmarshal(raw, &snaps); err != nil {
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID:   ag.ID,
						AgentName: ag.Name,
						Error:     invalidAgentResponseMessage,
					}}
					continue
				}
				rows := make([]GlobalInflightRow, 0, len(snaps))
				for _, s := range snaps {
					rows = append(rows, GlobalInflightRow{
						Snapshot:  s,
						AgentID:   ag.ID,
						AgentName: ag.Name,
					})
				}
				results[index] = nodeResult{rows: rows}
			}
		}()
	}
	for index := range ids {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return AllInflightResponse{}, apiErr
	}

	for _, r := range results {
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
	if h.Operations == nil {
		return InterruptResponse{}, api.InternalError("connection service not available", nil)
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return InterruptResponse{}, apiErr
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	ag, err := q.Agent().GetByID(req.AgentID)
	if err != nil {
		return InterruptResponse{}, api.NotFoundError("agent not found")
	}
	if apiErr := requestContextAPIError(c); apiErr != nil {
		return InterruptResponse{}, apiErr
	}
	interrupted, err := h.Operations.Interrupt(c.RequestContext(), protocol.OperationRequest{
		AgentID: ag.AgentID, RequestID: strconv.FormatInt(req.ID, 10),
	})
	if err != nil {
		return InterruptResponse{}, operationAPIError(err, connectivity.OperationInterrupt)
	}
	return InterruptResponse{Interrupted: interrupted}, nil
}
