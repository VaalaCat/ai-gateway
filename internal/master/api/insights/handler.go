package insights

import (
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
)

// Handler 是 /v1/insights 端点的容器。
// 实际 entity-type 维度的查询由 InsightProvider 实现 (见 provider.go);
// Handler 仅做参数校验、type 路由和响应组装。
type Handler struct {
	// Tracker 用于 EnrichLastSeen，覆盖 agent Meta 的 last_seen。
	// nil 时跳过 enrich（DB 值 fallback）。
	Tracker *msync.HeartbeatTracker
}
