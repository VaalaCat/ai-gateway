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
	"github.com/sourcegraph/conc/pool"
)

// agentBreaker 是 agent.breakers RPC 返回的单渠道熔断器快照(与 resilience.BreakerSnapshot 同构)。
type agentBreaker struct {
	Source      string  `json:"source"`
	ChannelID   uint    `json:"channel_id"`
	State       string  `json:"state"`
	RemainingMs int64   `json:"remaining_ms"`
	Failures    int     `json:"failures"`
	Successes   int     `json:"successes"`
	FailureRate float64 `json:"failure_rate"`
}

// AgentBreakerCell 是某渠道在某个 agent 上的熔断器状态(渠道行展开后每 agent 一格)。
type AgentBreakerCell struct {
	AgentID     uint    `json:"agent_id"`
	AgentName   string  `json:"agent_name"`
	State       string  `json:"state"`
	RemainingMs int64   `json:"remaining_ms"`
	Failures    int     `json:"failures"`
	Successes   int     `json:"successes"`
	FailureRate float64 `json:"failure_rate"`
}

// ChannelBreakerRow 是看板一行:一个渠道跨所有 agent 的聚合 + 各 agent 明细。
type ChannelBreakerRow struct {
	Source      string             `json:"source"`
	ChannelID   uint               `json:"channel_id"`
	WorstState  string             `json:"worst_state"`  // Open>HalfOpen>Closed 取最坏
	OpenAgents  int                `json:"open_agents"`  // state ∈ {open, half-open} 的 agent 数
	TotalAgents int                `json:"total_agents"` // 有该 breaker 的 agent 数
	Agents      []AgentBreakerCell `json:"agents"`
}

type BreakerBoardResponse struct {
	Channels     []ChannelBreakerRow `json:"channels"`
	FailedAgents []FailedAgent       `json:"failed_agents"`
}

// GetBreakerBoard 扇出 agent.breakers 到所有在线 agent,按 (source, channel_id) 聚合成
// 渠道行(含各 agent 明细 + 最坏状态 + 异常 agent 数),并确定性排序。
func (h *Handler) GetBreakerBoard(c *app.Context, _ api.EmptyRequest) (BreakerBoardResponse, error) {
	resp := BreakerBoardResponse{Channels: []ChannelBreakerRow{}, FailedAgents: []FailedAgent{}}
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
		rows   []agentBreaker
		ag     models.Agent
		failed *FailedAgent
	}
	p := pool.NewWithResults[nodeRes]().WithMaxGoroutines(16)
	for _, uid := range ids {
		ag := byUID[uid]
		p.Go(func() nodeRes {
			raw, err := h.HubCall(uid, consts.RPCAgentBreakers, nil, 5*time.Second)
			if err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: err.Error()}}
			}
			var rows []agentBreaker
			if err := json.Unmarshal(raw, &rows); err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: "decode: " + err.Error()}}
			}
			return nodeRes{rows: rows, ag: ag}
		})
	}
	type key struct {
		source string
		id     uint
	}
	agg := map[key]*ChannelBreakerRow{}
	for _, r := range p.Wait() {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
			continue
		}
		for _, b := range r.rows {
			k := key{b.Source, b.ChannelID}
			cur, ok := agg[k]
			if !ok {
				cur = &ChannelBreakerRow{Source: b.Source, ChannelID: b.ChannelID}
				agg[k] = cur
			}
			cur.Agents = append(cur.Agents, AgentBreakerCell{
				AgentID: r.ag.ID, AgentName: r.ag.Name,
				State: b.State, RemainingMs: b.RemainingMs,
				Failures: b.Failures, Successes: b.Successes, FailureRate: b.FailureRate,
			})
		}
	}
	for _, row := range agg {
		row.TotalAgents = len(row.Agents)
		row.WorstState, row.OpenAgents = summarizeBreakerAgents(row.Agents)
		resp.Channels = append(resp.Channels, *row)
	}
	sortBreakerBoard(&resp)
	return resp, nil
}

// breakerStateRank Open>HalfOpen>Closed(越大越坏)。
func breakerStateRank(s string) int {
	switch s {
	case "open":
		return 2
	case "half-open":
		return 1
	default:
		return 0
	}
}

// summarizeBreakerAgents 返回最坏状态 + 异常(open/half-open)的 agent 数。
func summarizeBreakerAgents(cells []AgentBreakerCell) (worst string, open int) {
	worst = "closed"
	for _, cell := range cells {
		if breakerStateRank(cell.State) > breakerStateRank(worst) {
			worst = cell.State
		}
		if cell.State == "open" || cell.State == "half-open" {
			open++
		}
	}
	return worst, open
}

// sortBreakerBoard 确定性排序,避免 map/agent 完成顺序导致看板刷新行乱跳。
func sortBreakerBoard(resp *BreakerBoardResponse) {
	sort.Slice(resp.Channels, func(i, j int) bool {
		a, b := resp.Channels[i], resp.Channels[j]
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		return a.ChannelID < b.ChannelID
	})
	for i := range resp.Channels {
		ag := resp.Channels[i].Agents
		sort.Slice(ag, func(x, y int) bool { return ag[x].AgentID < ag[y].AgentID })
	}
	sort.Slice(resp.FailedAgents, func(i, j int) bool {
		return resp.FailedAgents[i].AgentID < resp.FailedAgents[j].AgentID
	})
}
