package stats

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type Handler struct {
	ConnectedCount      func() int
	Cache               *dao.StatsCache
	LogDatabaseReady    func() bool
	DashboardDataFinder func(app.Application, context.Context) DashboardDataFinder
}

type DashboardDataFinder interface {
	CoreDashboardKpis(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.KpiBundle, error)
	CoreDashboardTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error)
	DashboardSuccessRate(dao.ObsRange, dao.Scope, dao.ObsFilter) (dao.KpiMetric, error)
	HourlyTrend(dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.TimeBucket, error)
	Leaderboard(string, string, int, dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.LeaderRow, error)
	SpeedCompare(string, dao.ObsRange, dao.Scope, dao.ObsFilter) ([]dao.SpeedRow, error)
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
	Start  int64  `form:"start"`
	End    int64  `form:"end"`
	Gran   string `form:"gran"`
	Model  string `form:"model"`
	UserID uint   `form:"user_id"`
}

// DashboardResponse keeps core billing facts concrete and groups every
// log-backed chart under the nullable LogMetrics section.
type DashboardResponse struct {
	Kpis              dao.KpiBundle `json:"kpis"`
	Trend             TrendBlock    `json:"trend"`
	LogMetrics        *LogMetrics   `json:"log_metrics"`
	DataStatus        DataStatus    `json:"data_status"`
}

type DataStatus struct {
	LogDB string `json:"log_db"`
}

type LogMetrics struct {
	Trend        PerformanceTrendBlock `json:"trend"`
	Leaderboard  *LeaderboardBlock     `json:"leaderboard,omitempty"`
	SpeedCompare *SpeedCompareBlock    `json:"speed_compare,omitempty"`
}

type PerformanceTrendBlock struct {
	Buckets []PerformanceBucket `json:"buckets"`
	Metrics []string            `json:"metrics"`
}

type PerformanceBucket struct {
	Ts           int64   `json:"ts"`
	Label        string  `json:"label"`
	TTFTMs       int64   `json:"ttft_ms"`
	TPS          float64 `json:"tps"`
	CacheHitRate float64 `json:"cache_hit_rate"`
}

// TrendBlock contains only core billing metrics.
type TrendBlock struct {
	Buckets []BillingTrendBucket `json:"buckets"`
	Metrics []string             `json:"metrics"`
}

type BillingTrendBucket struct {
	Ts       int64  `json:"ts"`
	Label    string `json:"label"`
	Cost     int64  `json:"cost"`
	Requests int64  `json:"requests"`
	Tokens   int64  `json:"tokens"`
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
