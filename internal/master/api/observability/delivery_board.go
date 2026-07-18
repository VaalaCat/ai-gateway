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

// DeliveryQueueItem 与 agent 侧 reporter.QueueItemSnapshot(旁路重试队列单条记录)同构。
type DeliveryQueueItem struct {
	RequestID    string `json:"request_id"`
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
	Items         []DeliveryQueueItem `json:"items"`
}

// AgentQueueRow 是看板一行:单个 agent 的 usage 投递两级队列快照。
type AgentQueueRow struct {
	AgentID       uint                `json:"agent_id"`
	AgentName     string              `json:"agent_name"`
	StoreLen      int                 `json:"store_len"`
	RetryLen      int                 `json:"retry_len"`
	TotalBytes    int                 `json:"total_bytes"` // store_bytes+retry_bytes
	OldestTs      int64               `json:"oldest_ts"`
	LastSuccessAt int64               `json:"last_success_at"`
	LastError     string              `json:"last_error"`
	Inflight      int                 `json:"inflight"`
	Items         []DeliveryQueueItem `json:"items"`
}

type DeliveryBoardResponse struct {
	Agents       []AgentQueueRow `json:"agents"`
	FailedAgents []FailedAgent   `json:"failed_agents"`
}

// GetDeliveryBoard 扇出 agent.usageQueue 到所有在线 agent,汇总成每 agent 一行的投递
// 看板(镜像 GetBreakerBoard 的扇出/隔离/排序骨架)。
func (h *Handler) GetDeliveryBoard(c *app.Context, _ api.EmptyRequest) (DeliveryBoardResponse, error) {
	resp := DeliveryBoardResponse{Agents: []AgentQueueRow{}, FailedAgents: []FailedAgent{}}
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
		row    AgentQueueRow
		ag     models.Agent
		failed *FailedAgent
	}
	p := pool.NewWithResults[nodeRes]().WithMaxGoroutines(16)
	for _, uid := range ids {
		ag := byUID[uid]
		p.Go(func() nodeRes {
			raw, err := h.HubCall(uid, consts.RPCAgentUsageQueue, nil, 5*time.Second)
			if err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: err.Error()}}
			}
			var snap agentQueueSnapshot
			if err := json.Unmarshal(raw, &snap); err != nil {
				return nodeRes{ag: ag, failed: &FailedAgent{AgentID: ag.ID, AgentName: ag.Name, Error: "decode: " + err.Error()}}
			}
			if snap.Items == nil {
				// agent 旁路队列为空时 snapshot 的 items 是 nil slice,原样透传会序列化成
				// JSON null,前端 .map 直接崩——看板行本身仍要展示(store 可能有积压)。
				snap.Items = []DeliveryQueueItem{}
			}
			return nodeRes{ag: ag, row: AgentQueueRow{
				AgentID: ag.ID, AgentName: ag.Name,
				StoreLen: snap.StoreLen, RetryLen: snap.RetryLen,
				TotalBytes:    snap.StoreBytes + snap.RetryBytes,
				OldestTs:      snap.OldestTs,
				LastSuccessAt: snap.LastSuccessAt,
				LastError:     snap.LastError,
				Inflight:      snap.Inflight,
				Items:         snap.Items,
			}}
		})
	}
	for _, r := range p.Wait() {
		if r.failed != nil {
			resp.FailedAgents = append(resp.FailedAgents, *r.failed)
			continue
		}
		resp.Agents = append(resp.Agents, r.row)
	}
	sortDeliveryBoard(&resp)
	return resp, nil
}

// sortDeliveryBoard 确定性排序,避免扇出完成顺序不定导致看板刷新行乱跳。
func sortDeliveryBoard(resp *DeliveryBoardResponse) {
	sort.Slice(resp.Agents, func(i, j int) bool { return resp.Agents[i].AgentID < resp.Agents[j].AgentID })
	sort.Slice(resp.FailedAgents, func(i, j int) bool {
		return resp.FailedAgents[i].AgentID < resp.FailedAgents[j].AgentID
	})
}
