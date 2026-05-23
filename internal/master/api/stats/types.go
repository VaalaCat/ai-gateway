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

// DashboardRequest 是 /v1/stats/dashboard 的入参。
// start/end 为 unix 秒；end 缺省取 now、start 缺省取 end-86400；gran 缺省 "day"。
type DashboardRequest struct {
	Start int64  `form:"start"`
	End   int64  `form:"end"`
	Gran  string `form:"gran"`
}

// DashboardResponse 是 /v1/stats/dashboard 的统一返回。
// admin scope 返回全部字段；user scope 仅 Kpis + Trend，其余 omitempty。
type DashboardResponse struct {
	Kpis              dao.KpiBundle      `json:"kpis"`
	Trend             TrendBlock         `json:"trend"`
	ModelDistribution []dao.Bucket       `json:"model_distribution,omitempty"`
	Leaderboard       *LeaderboardBlock  `json:"leaderboard,omitempty"`
	SpeedCompare      *SpeedCompareBlock `json:"speed_compare,omitempty"`
}

// TrendBlock 包含 HourlyTrend 输出 + 当前可选 metric 列表（前端 metric 切换用）。
type TrendBlock struct {
	Buckets []dao.TimeBucket `json:"buckets"`
	Metrics []string         `json:"metrics"`
}

// LeaderboardBlock 聚合三个维度 (user/model/channel) 的 cost top10 + 可选 metric 列表。
type LeaderboardBlock struct {
	Users            []dao.LeaderRow `json:"users"`
	Models           []dao.LeaderRow `json:"models"`
	Channels         []dao.LeaderRow `json:"channels"`
	AvailableMetrics []string        `json:"available_metrics"`
}

// SpeedCompareBlock 聚合 model/channel 维度 SpeedCompare。
type SpeedCompareBlock struct {
	ByModel   []dao.SpeedRow `json:"by_model"`
	ByChannel []dao.SpeedRow `json:"by_channel"`
}
