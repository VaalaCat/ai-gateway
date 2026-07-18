package stats

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Dashboard 组合 Phase 2 的 DAO 方法 (DashboardKpis / HourlyTrend / Distribution /
// Leaderboard / SpeedCompare)，按 admin/user scope 返回不同的字段集。
//
// admin: 全部字段 (kpis + trend + model_distribution + leaderboard + speed_compare)。
// user:  仅 kpis + trend；admin-only 字段通过 omitempty 隐藏。
//
// 窗口超过 ObsRange.Validate() 上限时返回 400 RangeOutOfBounds (结构化 code，前端 i18n 用)。
func (h *Handler) Dashboard(c *app.Context, req DashboardRequest) (DashboardResponse, error) {
	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return DashboardResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	scope := middleware.GetScope(c.Context)
	s := toDaoScope(scope)
	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))

	// 越权防护:非 admin 不能按别人 user_id 筛(DAO 的 EffectiveUserID 也会兜底)。
	filter := dao.ObsFilter{ModelName: req.Model, UserID: req.UserID}
	if !s.IsAdmin {
		filter.UserID = 0
	}

	kpis, err := q.Stats().DashboardKpis(r, s, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard kpis", err)
	}
	trend, err := q.Stats().HourlyTrend(r, s, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard trend", err)
	}

	resp := DashboardResponse{
		Kpis:  kpis,
		Trend: TrendBlock{Buckets: trend, Metrics: []string{"cost", "requests", "tokens"}},
	}
	if !s.IsAdmin {
		return resp, nil
	}

	// admin 专属：model distribution + leaderboard 三维 + speed compare 两维。
	// 各子查询失败不阻断主响应：dashboard 是 best-effort 聚合面板，单项失败时退化为 nil。
	if modelDist, err := q.Stats().Distribution("model", r, s, filter); err == nil {
		resp.ModelDistribution = modelDist
	}
	users, _ := q.Stats().Leaderboard("user", "tokens", 10, r, s, filter)
	modelsL, _ := q.Stats().Leaderboard("model", "tokens", 10, r, s, filter)
	chansL, _ := q.Stats().Leaderboard("channel", "tokens", 10, r, s, filter)
	resp.Leaderboard = &LeaderboardBlock{
		Users:            users,
		Models:           modelsL,
		Channels:         chansL,
		AvailableMetrics: []string{"cost", "requests", "tokens", "tps", "ttft"},
	}
	byModel, _ := q.Stats().SpeedCompare("model", r, s, filter)
	byChannel, _ := q.Stats().SpeedCompare("channel", r, s, filter)
	resp.SpeedCompare = &SpeedCompareBlock{ByModel: byModel, ByChannel: byChannel}
	return resp, nil
}
