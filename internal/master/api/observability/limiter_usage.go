package observability

import (
	"encoding/json"
	"sort"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
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
	Buckets      []LimiterBucketRow `json:"buckets"`
	FailedAgents []FailedAgent      `json:"failed_agents"`
}

type FailedAgent struct {
	AgentID   uint   `json:"agent_id"`
	AgentName string `json:"agent_name"`
	Error     string `json:"error"`
}

func (h *Handler) GetLimiterUsage(c *app.Context, _ api.EmptyRequest) (LimiterUsageResponse, error) {
	resp := LimiterUsageResponse{Buckets: []LimiterBucketRow{}, FailedAgents: []FailedAgent{}}
	if h.HubCall == nil || h.GetOnlineAgentIDs == nil {
		return resp, api.InternalError("hub not available", nil)
	}
	ids := h.GetOnlineAgentIDs()
	if len(ids) == 0 {
		return resp, nil
	}
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	agents, err := q.Agent().ListByAgentIDs(ids)
	if err != nil {
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
			raw, err := h.HubCall(uid, consts.RPCAgentLimiterUsage, nil, 5*time.Second)
			if err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: err.Error()}}
			}
			var rows []protocol.LimiterBucketStat
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: "decode: " + err.Error()}}
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
	sort.Slice(resp.FailedAgents, func(i, j int) bool {
		return resp.FailedAgents[i].AgentID < resp.FailedAgents[j].AgentID
	})
}
