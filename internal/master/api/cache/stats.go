package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Stats 返回所有 agent 的 cache 快照 + 集群聚合。
//
// online 仅以 Hub 的 WebSocket 连接为准（Hub.IsOnline 等价于
// "这个 agent_id 在 GetOnlineAgentIDs 返回值里"），不依赖 last_seen。
// offline agent 也会列在 agents[] 里（带最近 last_seen），但
// 不计入 cluster 聚合。
func (h *Handler) Stats(c *app.Context, _ api.EmptyRequest) (StatsResponse, error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	// 一次性拉所有 agent；admin 部署规模不会超过这个上限。
	agents, _, err := q.Agent().List(
		dao.ListOptions{Page: 1, PageSize: 10000},
		dao.AgentListFilter{},
	)
	if err != nil {
		return StatsResponse{}, api.InternalError("list agents failed", err)
	}

	onlineSet := map[string]struct{}{}
	for _, id := range h.GetOnlineAgentIDs() {
		onlineSet[id] = struct{}{}
	}

	snapshots := make([]AgentCacheSnapshot, 0, len(agents))
	aggInputs := make([]AgentSnapshot, 0, len(agents))
	for _, a := range agents {
		_, online := onlineSet[a.AgentID]
		var stats map[string]protocol.CacheEntityStats
		if online {
			if rt := h.GetRuntime(a.AgentID); rt != nil {
				// Hub 更新 runtime 时整体替换指针而非原地改字段，所以这里读旧快照是安全的。
				stats = rt.CacheStats
			}
		}
		snapshots = append(snapshots, AgentCacheSnapshot{
			AgentID:    a.AgentID,
			Name:       a.Name,
			Online:     online,
			LastSeen:   a.LastSeen,
			CacheStats: stats,
		})
		aggInputs = append(aggInputs, AgentSnapshot{
			AgentID:    a.AgentID,
			Online:     online,
			CacheStats: stats,
		})
	}

	if h.Tracker != nil {
		msync.EnrichLastSeen(h.Tracker, snapshots,
			func(it AgentCacheSnapshot) string { return it.AgentID },
			func(it AgentCacheSnapshot) int64 { return it.LastSeen },
			func(it *AgentCacheSnapshot, ts int64) { it.LastSeen = ts },
		)
	}

	return StatsResponse{
		Agents:  snapshots,
		Cluster: Aggregate(aggInputs),
	}, nil
}
