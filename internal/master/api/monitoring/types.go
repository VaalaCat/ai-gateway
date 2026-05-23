package monitoring

import "github.com/VaalaCat/ai-gateway/internal/dao"

// InsightsRequest 是 /v1/monitoring/insights 入参。
// start/end 为 unix 秒, end 缺省 now, start 缺省 end-86400;
// gran 缺省 "day"。窗口超限会被 ObsRange.Validate 拦下。
type InsightsRequest struct {
	Start int64  `form:"start"`
	End   int64  `form:"end"`
	Gran  string `form:"gran"`
}

// InsightsResponse 是 /v1/monitoring/insights 返回的整体结构。
//
// KpiRings 顶部的 5 个圆环 (success/cache/agents/tps/error);
// Channels/Agents 是两个 entity 维度的明细列表;
// Errors 是 stage + channel 双维度失败分布。
//
// 整页 admin-only;user scope 在 handler 层直接 403。
type InsightsResponse struct {
	KpiRings KpiRings            `json:"kpi_rings"`
	Channels []dao.ChannelMetric `json:"channels"`
	Agents   []dao.AgentMetric   `json:"agents"`
	Errors   ErrorBundles        `json:"errors"`
}

// KpiRings 是 5 个并列的环形 KPI 卡片。
// 字段名与前端组件直接对齐 (success/cache/agents/tps/error)。
type KpiRings struct {
	Success KpiRing `json:"success"`
	Cache   KpiRing `json:"cache"`
	Agents  KpiRing `json:"agents"`
	TPS     KpiRing `json:"tps"`
	Error   KpiRing `json:"error"`
}

// KpiRing 是单个环形 KPI 卡片的统一形态。
//
// Value 是 any 因为不同卡片含义不同:
//   success/cache/tps/error → 数字 (int64 / float64)
//   agents                  → 形如 "3/4" 的字符串 (online/total)
//
// WarnAbove 仅 error 卡片填充,前端高于阈值时变红。
type KpiRing struct {
	Ratio     float64  `json:"ratio"`
	Value     any      `json:"value"`
	Sub       string   `json:"sub"`
	WarnAbove *float64 `json:"warn_above,omitempty"`
}

// ErrorBundles 是 monitoring 页底部失败分布的两个维度 (stage / channel)。
type ErrorBundles struct {
	ByStage   []dao.ErrBucket `json:"by_stage"`
	ByChannel []dao.ErrBucket `json:"by_channel"`
}
