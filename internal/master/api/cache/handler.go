package cache

import (
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
)

// Handler 依赖 Hub 提供 online 列表 + per-agent runtime；
// 用 DAO 把 agent_id → name 拼出来。
type Handler struct {
	GetOnlineAgentIDs func() []string
	GetRuntime        func(agentID string) *msync.AgentRuntime
	// Tracker 用于 EnrichLastSeen，覆盖 stats 中 agent 的 last_seen。
	// nil 时跳过 enrich（DB 值 fallback）。
	Tracker *msync.HeartbeatTracker
}
