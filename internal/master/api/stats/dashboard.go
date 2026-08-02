package stats

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// Dashboard 组合 Phase 2 的 DAO 方法 (DashboardKpis / HourlyTrend /
// Leaderboard / SpeedCompare)，按 admin/user scope 返回不同的字段集。
//
// admin: 全部字段 (kpis + trend + leaderboard + speed_compare)。
// user:  仅 kpis + trend；admin-only 字段通过 omitempty 隐藏。
//
// 窗口超过 ObsRange.Validate() 上限时返回 400 RangeOutOfBounds (结构化 code，前端 i18n 用)。
func (h *Handler) Dashboard(c *app.Context, req DashboardRequest) (DashboardResponse, error) {
	topN, err := parseDashboardTopN(req.TopN)
	if err != nil {
		return DashboardResponse{}, err
	}
	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return DashboardResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	scope := middleware.GetScope(c.Context)
	s := toDaoScope(scope)
	// 越权防护:非 admin 不能按别人 user_id 筛(DAO 的 EffectiveUserID 也会兜底)。
	filter := dao.ObsFilter{ModelName: req.Model, UserID: req.UserID}
	if !s.IsAdmin {
		filter.UserID = 0
	}
	if h.LogDatabaseReady != nil && !h.LogDatabaseReady() {
		return DashboardResponse{}, api.InternalError("dashboard log database", dao.ErrLogDatabaseUnavailable)
	}
	if _, err := dao.NewContextWithContext(c.App, c.RequestContext()).LogDB(); err != nil {
		return DashboardResponse{}, api.InternalError("dashboard log database", err)
	}
	load := func(ctx context.Context) (any, error) {
		return h.loadDashboard(ctx, c, r, s, topN, filter)
	}
	if h.Cache == nil {
		value, err := load(c.RequestContext())
		return value.(DashboardResponse), err
	}
	scopeName := "user"
	if s.IsAdmin {
		scopeName = "admin"
	}
	value, err := h.Cache.Get(c.RequestContext(), dao.QueryKey{
		Name: "stats.dashboard", From: r.Start, To: r.End, Gran: string(r.Gran),
		Scope: scopeName, UserID: filter.EffectiveUserID(s), Model: filter.ModelName, TopN: topN,
	}, load)
	if err != nil {
		return DashboardResponse{}, err
	}
	response, ok := value.(DashboardResponse)
	if !ok {
		return DashboardResponse{}, fmt.Errorf("stats dashboard cache returned %T", value)
	}
	return response, nil
}

func parseDashboardTopN(raw int) (int, error) {
	if raw == 0 {
		return 10, nil
	}
	return api.ParseTopN(raw)
}

func (h *Handler) loadDashboard(ctx context.Context, c *app.Context, r dao.ObsRange, s dao.Scope, topN int, filter dao.ObsFilter) (DashboardResponse, error) {
	finder := h.dashboardDataFinder(c.App, ctx)
	kpis, err := finder.DashboardKpis(r, s, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard kpis", err)
	}
	requestTrend, err := dashboardTrendFromRequestFacts(finder, r, s, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard request trend", err)
	}
	resp := DashboardResponse{
		Kpis:       kpis,
		Trend:      billingTrendBlock(requestTrend),
		DataStatus: DataStatus{LogDB: "available"},
	}
	logMetrics, successRate, err := loadDashboardLogMetrics(finder, r, s, topN, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard log metrics", err)
	}
	resp.Kpis.SuccessRate = successRate
	resp.LogMetrics = logMetrics
	return resp, nil
}

func dashboardTrendFromRequestFacts(finder DashboardDataFinder, r dao.ObsRange, s dao.Scope, filter dao.ObsFilter) ([]dao.TimeBucket, error) {
	return finder.DashboardTrend(r, s, filter)
}

func billingTrendBlock(buckets []dao.TimeBucket) TrendBlock {
	billing := make([]BillingTrendBucket, 0, len(buckets))
	for _, bucket := range buckets {
		billing = append(billing, BillingTrendBucket{
			Ts: bucket.Ts, Label: bucket.Label, Cost: bucket.Cost,
			Requests: bucket.Requests, Tokens: bucket.Tokens,
		})
	}
	return TrendBlock{Buckets: billing, Metrics: []string{"cost", "requests", "tokens"}}
}

func loadDashboardLogMetrics(finder DashboardDataFinder, r dao.ObsRange, s dao.Scope, topN int, filter dao.ObsFilter) (*LogMetrics, *dao.KpiMetric, error) {
	var successRate *dao.KpiMetric
	if s.IsAdmin {
		metric, err := finder.DashboardSuccessRate(r, s, filter)
		if err != nil {
			return nil, nil, err
		}
		successRate = &metric
	}
	trend, err := finder.HourlyTrend(r, s, filter)
	if err != nil {
		return nil, nil, err
	}
	metrics := &LogMetrics{Trend: performanceTrendBlock(trend)}
	if !s.IsAdmin {
		return metrics, nil, nil
	}
	if err := loadDashboardRankings(finder, metrics, r, s, topN, filter); err != nil {
		return nil, nil, err
	}
	return metrics, successRate, nil
}

func loadDashboardRankings(finder DashboardDataFinder, metrics *LogMetrics, r dao.ObsRange, s dao.Scope, topN int, filter dao.ObsFilter) error {
	users, err := finder.Leaderboard("user", "tokens", 10, r, s, filter)
	if err != nil {
		return err
	}
	models, err := finder.Leaderboard("model", "tokens", 10, r, s, filter)
	if err != nil {
		return err
	}
	channels, err := finder.Leaderboard("channel", "tokens", 10, r, s, filter)
	if err != nil {
		return err
	}
	metrics.Leaderboard = &LeaderboardBlock{
		Users: users, Models: models, Channels: channels,
		AvailableMetrics: []string{"cost", "requests", "tokens", "tps", "ttft"},
	}
	byModel, err := finder.SpeedCompare("model", r, s, topN, filter)
	if err != nil {
		return err
	}
	byChannel, err := finder.SpeedCompare("channel", r, s, topN, filter)
	if err != nil {
		return err
	}
	if byModel == nil {
		byModel = []dao.SpeedRow{}
	}
	if byChannel == nil {
		byChannel = []dao.SpeedRow{}
	}
	metrics.SpeedCompare = &SpeedCompareBlock{ByModel: byModel, ByChannel: byChannel}
	return nil
}

func (h *Handler) dashboardDataFinder(application app.Application, ctx context.Context) DashboardDataFinder {
	if h.DashboardDataFinder != nil {
		return h.DashboardDataFinder(application, ctx)
	}
	return dao.NewAdminQuery(dao.NewContextWithContext(application, ctx)).Stats()
}

func performanceTrendBlock(buckets []dao.TimeBucket) PerformanceTrendBlock {
	performance := make([]PerformanceBucket, 0, len(buckets))
	for _, bucket := range buckets {
		performance = append(performance, PerformanceBucket{
			Ts: bucket.Ts, Label: bucket.Label, TTFTMs: bucket.TTFTMs,
			TPS: bucket.TPS, CacheHitRate: bucket.CacheHitRate,
		})
	}
	return PerformanceTrendBlock{Buckets: performance, Metrics: []string{"ttft", "tps", "cache_hit_rate"}}
}
