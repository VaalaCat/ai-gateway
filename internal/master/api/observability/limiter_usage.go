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
	"github.com/VaalaCat/ai-gateway/internal/master/controlrpc"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc/pool"
)

// LimiterBucketRow 是集群聚合后的一行 + per-agent 明细（饱和度看每节点，v1 本地执行=本地求和）。
type LimiterBucketRow struct {
	protocol.LimiterBucketStat
	PerAgent []AgentBucket `json:"per_agent"`
}

type AgentBucket struct {
	AgentID   uint   `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Occupied  int64  `json:"occupied"`
	Capacity  int64  `json:"capacity"`
	Waiters   int64  `json:"waiters"`
}

type LimiterUsageResponse struct {
	Buckets            []LimiterBucketRow `json:"buckets"`
	FailedAgents       []FailedAgent      `json:"failed_agents"`
	SuccessfulAgentIDs []uint             `json:"successful_agent_ids"`
}

type FailedAgent struct {
	AgentID   uint   `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Error     string `json:"error"`
	Code      string `json:"code,omitempty"`
	ElapsedMs int64  `json:"elapsed_ms"`
}

// GetLimiterUsage 在聚合预算内读取节点限流占用，并合并成功结果与失败节点。
func (h *Handler) GetLimiterUsage(c *app.Context, _ api.EmptyRequest) (LimiterUsageResponse, error) {
	resp := LimiterUsageResponse{Buckets: []LimiterBucketRow{}, FailedAgents: []FailedAgent{}, SuccessfulAgentIDs: []uint{}}
	if h.HubCallContext == nil || h.GetOnlineAgentIDs == nil {
		return resp, api.InternalError("hub not available", nil)
	}
	// 1. 为查询、排队和节点 RPC 共用本次聚合预算。
	started := time.Now()
	ctx, cancel := context.WithTimeout(c.RequestContext(), api.DiagnosticAggregateTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentLimiterUsage, started, err)
	}
	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		return resp, nil
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
		if ctx.Err() != nil {
			return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentLimiterUsage, started, ctx.Err())
		}
		return resp, api.InternalError("list agents failed", err)
	}
	byUID := map[string]models.Agent{}
	for _, a := range agents {
		byUID[a.AgentID] = a
	}
	type nodeRes struct {
		rows   []protocol.LimiterBucketStat
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
				raw, err = h.HubCallContext(nodeCtx, uid, consts.RPCAgentLimiterUsage, nil, api.DiagnosticNodeTimeout)
			}
			if err != nil {
				failure := api.DiagnosticError(c.Logger, uid, consts.RPCAgentLimiterUsage, started, err)
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: failure.Message, Code: failure.Code, ElapsedMs: time.Since(started).Milliseconds()}}
			}
			var rows []protocol.LimiterBucketStat
			if err := json.Unmarshal(raw, &rows); err != nil {
				failure := api.DiagnosticError(c.Logger, uid, consts.RPCAgentLimiterUsage, started, fmt.Errorf("%w: %v", controlrpc.ErrInvalidResponse, err))
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: failure.Message, Code: failure.Code, ElapsedMs: time.Since(started).Milliseconds()}}
			}
			return nodeRes{rows: rows, ag: ag}
		})
	}
	type key struct {
		lim    uint
		bucket string
	}
	agg := map[key]*LimiterBucketRow{}
	for _, r := range p.Wait() {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
			continue
		}
		resp.SuccessfulAgentIDs = append(resp.SuccessfulAgentIDs, r.ag.ID)
		for _, row := range r.rows {
			k := key{row.LimiterID, row.Bucket}
			cur, ok := agg[k]
			if !ok {
				cur = &LimiterBucketRow{LimiterBucketStat: protocol.LimiterBucketStat{
					LimiterID: row.LimiterID, Name: row.Name, Bucket: row.Bucket,
					Metric: row.Metric, KeyBy: row.KeyBy, WindowEndMs: row.WindowEndMs,
				}}
				agg[k] = cur
			}
			cur.Occupied += row.Occupied
			cur.Waiters += row.Waiters
			cur.Capacity += row.Capacity
			cur.PerAgent = append(cur.PerAgent, AgentBucket{
				AgentID: r.ag.ID, AgentName: r.ag.Name,
				Occupied: row.Occupied, Capacity: row.Capacity, Waiters: row.Waiters,
			})
		}
	}
	for _, v := range agg {
		resp.Buckets = append(resp.Buckets, *v)
	}
	sortLimiterResponse(&resp)
	if err := c.RequestContext().Err(); err == context.Canceled {
		return resp, api.DiagnosticError(c.Logger, "", consts.RPCAgentLimiterUsage, started, err)
	}
	return resp, nil
}

// sortLimiterResponse 对聚合结果做确定性排序,避免 map 迭代 / agent 完成顺序不定
// 导致限流 Tab 每次刷新行乱跳:Buckets 按 (LimiterID,Bucket),每行 PerAgent 按
// AgentID,FailedAgents 按 AgentID。
func sortLimiterResponse(resp *LimiterUsageResponse) {
	sort.Slice(resp.Buckets, func(i, j int) bool {
		a, b := resp.Buckets[i], resp.Buckets[j]
		if a.LimiterID != b.LimiterID {
			return a.LimiterID < b.LimiterID
		}
		return a.Bucket < b.Bucket
	})
	for i := range resp.Buckets {
		pa := resp.Buckets[i].PerAgent
		sort.Slice(pa, func(x, y int) bool { return pa[x].AgentID < pa[y].AgentID })
	}
	sort.Slice(resp.SuccessfulAgentIDs, func(i, j int) bool { return resp.SuccessfulAgentIDs[i] < resp.SuccessfulAgentIDs[j] })
	sort.Slice(resp.FailedAgents, func(i, j int) bool {
		return resp.FailedAgents[i].AgentID < resp.FailedAgents[j].AgentID
	})
}
