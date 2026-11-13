package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
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
	Code      string `json:"code,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// AllInflightResponse 是 GetAllInflight 的响应体。
type AllInflightResponse struct {
	Requests           []GlobalInflightRow `json:"requests"`
	FailedAgents       []FailedAgent       `json:"failed_agents"`
	SuccessfulAgentIDs []uint              `json:"successful_agent_ids"`
}

type nodeResult struct {
	agentID uint
	rows    []GlobalInflightRow
	failed  *FailedAgent
}

// GetAllInflight 并发拉取所有在线节点的 agent.inflight，合并成扁平列表；
// 某节点出错/超时进 failed_agents，不阻塞其余节点。
func (h *Handler) GetAllInflight(c *app.Context, _ api.EmptyRequest) (AllInflightResponse, error) {
	if h.Connections == nil || h.HubCallSession == nil || h.GetOnlineAgentIDs == nil {
		return AllInflightResponse{}, api.InternalError("connection service not available", nil)
	}
	if err := c.RequestContext().Err(); err != nil {
		return AllInflightResponse{}, api.DiagnosticError(c.Logger, "", consts.RPCAgentInflight, time.Now(), err)
	}
	resp := AllInflightResponse{
		Requests:           []GlobalInflightRow{},
		FailedAgents:       []FailedAgent{},
		SuccessfulAgentIDs: []uint{},
	}

	// 1. 为查询、排队和所有节点共用聚合预算。
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.RequestContext(), api.DiagnosticAggregateTimeout)
	defer cancel()

	ids := stableUniqueAgentIDs(h.GetOnlineAgentIDs())
	if len(ids) == 0 {
		return resp, nil
	}

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		if ctx.Err() != nil {
			return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentInflight, started, ctx.Err())
		}
		return AllInflightResponse{}, api.InternalError("list agents failed", err)
	}

	byUID := make(map[string]models.Agent, len(agents))
	for _, a := range agents {
		byUID[a.AgentID] = a
	}
	h.enrichLastSeen(agents)
	batch, err := h.Connections.BuildManyContext(ctx, agents)
	if err != nil {
		if ctx.Err() != nil {
			return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentInflight, started, ctx.Err())
		}
		if apiErr := requestContextAPIError(c); apiErr != nil {
			return AllInflightResponse{}, apiErr
		}
		return AllInflightResponse{}, api.InternalError("build connection snapshot failed", err)
	}
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
					results[index] = nodeResult{failed: &FailedAgent{Error: "agent_not_found", Code: "agent_operation_failed"}}
					continue
				}
				snapshot, ok := batch.Items[uid]
				if !ok {
					results[index] = nodeResult{failed: &FailedAgent{
						AgentID: ag.ID, AgentName: ag.Name, Error: "connection_snapshot_unavailable", Code: "agent_operation_failed",
					}}
					continue
				}
				fail := func(err error) nodeResult {
					failure := api.DiagnosticError(c.Logger, uid, consts.RPCAgentInflight, started, err)
					return nodeResult{failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: failure.Message, Code: failure.Code, ElapsedMs: time.Since(started).Milliseconds()}}
				}
				if snapshot.Control.State != "connected" || snapshot.Control.SessionGeneration == 0 {
					results[index] = fail(controlrpc.ErrDisconnected)
					continue
				}
				nodeCtx, cancelNode := context.WithTimeout(ctx, api.DiagnosticNodeTimeout)
				var raw json.RawMessage
				err := nodeCtx.Err()
				if err == nil {
					raw, err = h.HubCallSession(nodeCtx, uid, snapshot.Control.SessionGeneration, consts.RPCAgentInflight, nil, api.DiagnosticNodeTimeout)
				}
				cancelNode()
				if err != nil {
					results[index] = fail(err)
					continue
				}
				var snaps []inflight.Snapshot
				if err := json.Unmarshal(raw, &snaps); err != nil {
					results[index] = fail(fmt.Errorf("%w: %v", controlrpc.ErrInvalidResponse, err))
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
				results[index] = nodeResult{rows: rows, agentID: ag.ID}
			}
		}()
	}
	for index := range ids {
		jobs <- index
	}
	close(jobs)
	workers.Wait()
	if c.RequestContext().Err() == context.Canceled {
		return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentInflight, started, context.Canceled)
	}

	for _, r := range results {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
		} else {
			resp.SuccessfulAgentIDs = append(resp.SuccessfulAgentIDs, r.agentID)
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
	if err := c.RequestContext().Err(); err != nil {
		return InterruptResponse{}, api.DiagnosticError(c.Logger, "", consts.RPCAgentInterrupt, time.Now(), err)
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	ag, err := q.Agent().GetByID(req.AgentID)
	if err != nil {
		return InterruptResponse{}, api.NotFoundError("agent not found")
	}
	if err := c.RequestContext().Err(); err != nil {
		return InterruptResponse{}, api.DiagnosticError(c.Logger, "", consts.RPCAgentInterrupt, time.Now(), err)
	}
	// 1. 发出中断命令，等待节点确认。
	started := time.Now()
	interrupted, err := h.Operations.Interrupt(c.RequestContext(), protocol.OperationRequest{
		AgentID: ag.AgentID, RequestID: strconv.FormatInt(req.ID, 10),
	})
	// 2. 超时或取消仅表示未收到确认，权限与代次错误沿用操作分类。
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			failure := api.DiagnosticError(c.Logger, ag.AgentID, consts.RPCAgentInterrupt, started, err)
			failure.Message = "the interrupt result could not be confirmed; refresh the request status"
			failure.Details = map[string]any{"operation": connectivity.OperationInterrupt}
			return InterruptResponse{}, failure
		}
		return InterruptResponse{}, operationAPIError(err, connectivity.OperationInterrupt)
	}
	return InterruptResponse{Interrupted: interrupted}, nil
}
