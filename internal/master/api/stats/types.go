package stats

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
)

type Handler struct {
	ConnectedCount func() int
}

type OverviewResponse struct {
	// Admin-only fields (nil for normal users)
	Users           *int64 `json:"users,omitempty"`
	Channels        *int64 `json:"channels,omitempty"`
	Models          *int64 `json:"models,omitempty"`
	Agents          *int64 `json:"agents,omitempty"`
	ConnectedAgents *int   `json:"connected_agents,omitempty"`

	// Common fields
	Tokens    int64 `json:"tokens"`
	UsageLogs int64 `json:"usage_logs"`
	TotalCost int64 `json:"total_cost"`

	// User-only fields (nil for admin)
	Quota     *int64 `json:"quota,omitempty"`
	UsedQuota *int64 `json:"used_quota,omitempty"`
}

type TrendRequest struct {
	Days string `form:"days"`
}

type TrendResponse struct {
	Items []dao.TrendItem `json:"items"`
}
