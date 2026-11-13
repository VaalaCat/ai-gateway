package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/monitoring"
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc/pool"
)

// DeliveryQueueItem 与 agent 侧 reporter.QueueItemSnapshot(旁路重试队列单条记录)同构。
type DeliveryQueueItem struct {
	RequestID    string `json:"request_id"`
	QueueID      string `json:"queue_id,omitempty"`
	UsageType    string `json:"usage_type,omitempty"`
	Bytes        int    `json:"bytes"`
	Attempts     int    `json:"attempts"`
	DegradeLevel int    `json:"degrade_level"`
	NextAt       int64  `json:"next_at"`
}

// agentQueueSnapshot 是 agent.usageQueue RPC 返回的线格式(与 reporter.QueueSnapshot 同构)。
type agentQueueSnapshot struct {
	StoreLen      int                 `json:"store_len"`
	StoreBytes    int                 `json:"store_bytes"`
	RetryLen      int                 `json:"retry_len"`
	RetryBytes    int                 `json:"retry_bytes"`
	OldestTs      int64               `json:"oldest_ts"`
	LastSuccessAt int64               `json:"last_success_at"`
	LastError     string              `json:"last_error"`
	Inflight      int                 `json:"inflight"`
	StoreDropped  uint64              `json:"store_dropped"`
	RetryDropped  uint64              `json:"retry_dropped"`
	TraceSlimmed  uint64              `json:"trace_slimmed"`
	Items         []DeliveryQueueItem `json:"items"`
}

// AgentQueueRow 是看板一行:单个 agent 的 usage 投递两级队列快照。
type AgentQueueRow struct {
	AgentID            uint                `json:"agent_id"`
	AgentName          string              `json:"agent_name"`
	StoreLen           int                 `json:"store_len"`
	RetryLen           int                 `json:"retry_len"`
	TotalBytes         int                 `json:"total_bytes"` // store_bytes+retry_bytes
	OldestTs           int64               `json:"oldest_ts"`
	LastSuccessAt      int64               `json:"last_success_at"`
	LastError          string              `json:"last_error"`
	Inflight           int                 `json:"inflight"`
	SharedUsageDropped uint64              `json:"shared_usage_dropped"`
	APITraceSlimmed    uint64              `json:"api_trace_slimmed"`
	Items              []DeliveryQueueItem `json:"items"`
}

type DeliveryBoardResponse struct {
	Agents             []AgentQueueRow       `json:"agents"`
	FailedAgents       []FailedAgent         `json:"failed_agents"`
	SuccessfulAgentIDs []uint                `json:"successful_agent_ids"`
	LogBacklog         monitoring.LogBacklog `json:"log_backlog"`
}

// GetDeliveryBoard 扇出 agent.usageQueue 到所有在线 agent,汇总成每 agent 一行的投递
// 看板(镜像 GetBreakerBoard 的扇出/隔离/排序骨架)。
func (h *Handler) GetDeliveryBoard(c *app.Context, _ api.EmptyRequest) (DeliveryBoardResponse, error) {
	resp := DeliveryBoardResponse{Agents: []AgentQueueRow{}, FailedAgents: []FailedAgent{}, SuccessfulAgentIDs: []uint{}}
	if h != nil && h.LogBacklogSnapshot != nil {
		resp.LogBacklog = h.LogBacklogSnapshot()
	}
	if h.HubCallContext == nil || h.GetOnlineAgentIDs == nil {
		return resp, api.InternalError("hub not available", nil)
	}
	// 1. 为查询、排队和节点 RPC 共用本次聚合预算。
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.RequestContext(), api.DiagnosticAggregateTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentUsageQueue, started, err)
	}
	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		return resp, nil
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		if ctx.Err() != nil {
			return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentUsageQueue, started, ctx.Err())
		}
		return resp, api.InternalError("list agents failed", err)
	}
	byUID := map[string]models.Agent{}
	for _, a := range agents {
		byUID[a.AgentID] = a
	}
	type nodeRes struct {
		row    AgentQueueRow
		ag     models.Agent
		failed *FailedAgent
	}
	p := pool.NewWithResults[nodeRes]().WithMaxGoroutines(16)
	for _, uid := range ids {
		ag := byUID[uid]
		p.Go(func() nodeRes {
			if ag.ID == 0 {
				return nodeRes{failed: &FailedAgent{Code: "agent_operation_failed", Error: "agent not found", ElapsedMs: time.Since(started).Milliseconds()}}
			}
			nodeCtx, cancelNode := context.WithTimeout(ctx, api.DiagnosticNodeTimeout)
			defer cancelNode()
			var raw json.RawMessage
			err := nodeCtx.Err()
			if err == nil {
				raw, err = h.HubCallContext(nodeCtx, uid, consts.RPCAgentUsageQueue, nil, api.DiagnosticNodeTimeout)
			}
			if err != nil {
				failure := api.DiagnosticError(c.Logger, uid, consts.RPCAgentUsageQueue, started, err)
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: failure.Message, Code: failure.Code, ElapsedMs: time.Since(started).Milliseconds()}}
			}
			var snap agentQueueSnapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				failure := api.DiagnosticError(c.Logger, uid, consts.RPCAgentUsageQueue, started, fmt.Errorf("%w: %v", controlrpc.ErrInvalidResponse, err))
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: failure.Message, Code: failure.Code, ElapsedMs: time.Since(started).Milliseconds()}}
			}
			if snap.Items == nil {
				// agent 旁路队列为空时 snapshot 的 items 是 nil slice,原样透传会序列化成
				// JSON null,前端 .map 直接崩——看板行本身仍要展示(store 可能有积压)。
				snap.Items = []DeliveryQueueItem{}
			}
			return nodeRes{ag: ag, row: AgentQueueRow{
				AgentID: ag.ID, AgentName: ag.Name,
				StoreLen: snap.StoreLen, RetryLen: snap.RetryLen,
				TotalBytes:         snap.StoreBytes + snap.RetryBytes,
				OldestTs:           snap.OldestTs,
				LastSuccessAt:      snap.LastSuccessAt,
				LastError:          snap.LastError,
				Inflight:           snap.Inflight,
				SharedUsageDropped: snap.StoreDropped + snap.RetryDropped,
				APITraceSlimmed:    snap.TraceSlimmed,
				Items:              snap.Items,
			}}
		})
	}
	for _, r := range p.Wait() {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
			continue
		}
		resp.SuccessfulAgentIDs = append(resp.SuccessfulAgentIDs, r.ag.ID)
		resp.Agents = append(resp.Agents, r.row)
	}
	sortDeliveryBoard(&resp)
	if err := c.RequestContext().Err(); err == context.Canceled {
		return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentUsageQueue, started, err)
	}
	return resp, nil
}

// sortDeliveryBoard 确定性排序,避免扇出完成顺序不定导致看板刷新行乱跳。
func sortDeliveryBoard(resp *DeliveryBoardResponse) {
	sort.Slice(resp.Agents, func(i, j int) bool { return resp.Agents[i].AgentID < resp.Agents[j].AgentID })
	sort.Slice(resp.SuccessfulAgentIDs, func(i, j int) bool { return resp.SuccessfulAgentIDs[i] < resp.SuccessfulAgentIDs[j] })
	sort.Slice(resp.FailedAgents, func(i, j int) bool {
		return resp.FailedAgents[i].AgentID < resp.FailedAgents[j].AgentID
	})
}
