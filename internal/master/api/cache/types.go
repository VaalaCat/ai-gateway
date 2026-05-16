package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// AgentCacheSnapshot 是单 agent 在 /api/admin/cache/stats 响应里的一项。
type AgentCacheSnapshot struct {
	AgentID    string                               `json:"agent_id"`
	Name       string                               `json:"name"`
	Online     bool                                 `json:"online"`
	LastSeen   int64                                `json:"last_seen"`
	CacheStats map[string]protocol.CacheEntityStats `json:"cache_stats,omitempty"`
}

// StatsResponse 是 /api/admin/cache/stats 的整体响应。
type StatsResponse struct {
	Agents  []AgentCacheSnapshot          `json:"agents"`
	Cluster map[string]ClusterEntityStats `json:"cluster"`
}
