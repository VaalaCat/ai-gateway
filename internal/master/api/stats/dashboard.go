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
	q := dao.NewAdminQuery(dao.NewContext(c.App))

	kpis, err := q.Stats().DashboardKpis(r, s)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard kpis", err)
	}
	trend, err := q.Stats().HourlyTrend(r, s)
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
	if modelDist, err := q.Stats().Distribution("model", r, s); err == nil {
		resp.ModelDistribution = modelDist
	}
	users, _ := q.Stats().Leaderboard("user", "cost", 10, r, s)
	modelsL, _ := q.Stats().Leaderboard("model", "cost", 10, r, s)
	chansL, _ := q.Stats().Leaderboard("channel", "cost", 10, r, s)
	resp.Leaderboard = &LeaderboardBlock{
		Users:            users,
		Models:           modelsL,
		Channels:         chansL,
		AvailableMetrics: []string{"cost", "requests", "tokens", "tps", "ttft"},
	}
	byModel, _ := q.Stats().SpeedCompare("model", r, s)
	byChannel, _ := q.Stats().SpeedCompare("channel", r, s)
	resp.SpeedCompare = &SpeedCompareBlock{ByModel: byModel, ByChannel: byChannel}
	return resp, nil
}
