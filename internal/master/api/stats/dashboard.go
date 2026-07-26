package stats

import (
	"context"
	"errors"
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
	load := func(ctx context.Context) (any, error) {
		return h.loadDashboard(ctx, c, r, s, filter)
	}
	if h.Cache == nil || (h.LogDatabaseReady != nil && !h.LogDatabaseReady()) {
		value, err := load(c.RequestContext())
		return value.(DashboardResponse), err
	}
	scopeName := "user"
	if s.IsAdmin {
		scopeName = "admin"
	}
	value, err := h.Cache.Get(c.RequestContext(), dao.QueryKey{
		Name: "stats.dashboard", From: r.Start, To: r.End, Gran: string(r.Gran),
		Scope: scopeName, UserID: filter.EffectiveUserID(s), Model: filter.ModelName,
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

func (h *Handler) loadDashboard(ctx context.Context, c *app.Context, r dao.ObsRange, s dao.Scope, filter dao.ObsFilter) (DashboardResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, ctx)
	finder := h.dashboardDataFinder(c.App, ctx)
	mode, err := daoCtx.DatabaseLayoutMode()
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard database layout", err)
	}
	var kpis dao.KpiBundle
	if mode == app.DatabaseLayoutSplit {
		kpis, err = finder.CoreDashboardKpis(r, s, filter)
	} else {
		kpis, err = dao.NewAdminQuery(daoCtx).Stats().DashboardKpis(r, s, filter)
	}
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard kpis", err)
	}
	coreTrend, err := dashboardCoreTrend(finder, mode, r, s, filter)
	if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard core trend", err)
	}
	resp := DashboardResponse{
		Kpis:       kpis,
		Trend:      billingTrendBlock(coreTrend),
		DataStatus: DataStatus{LogDB: "available"},
	}
	if h.LogDatabaseReady != nil && !h.LogDatabaseReady() {
		resp.DataStatus.LogDB = "unavailable"
		return resp, nil
	}
	if _, err := daoCtx.LogDB(); errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		resp.DataStatus.LogDB = "unavailable"
		return resp, nil
	} else if err != nil {
		return DashboardResponse{}, api.InternalError("dashboard log database", err)
	}

	logMetrics, successRate, err := loadDashboardLogMetrics(finder, r, s, filter)
	if err != nil {
		if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
			return degradeDashboardLogMetrics(resp), nil
		}
		return DashboardResponse{}, api.InternalError("dashboard log metrics", err)
	}
	resp.Kpis.SuccessRate = successRate
	resp.LogMetrics = logMetrics
	return resp, nil
}

func dashboardCoreTrend(finder DashboardDataFinder, mode app.DatabaseLayoutMode, r dao.ObsRange, s dao.Scope, filter dao.ObsFilter) ([]dao.TimeBucket, error) {
	if mode == app.DatabaseLayoutSplit {
		return finder.CoreDashboardTrend(r, s, filter)
	}
	return finder.HourlyTrend(r, s, filter)
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

func loadDashboardLogMetrics(finder DashboardDataFinder, r dao.ObsRange, s dao.Scope, filter dao.ObsFilter) (*LogMetrics, *dao.KpiMetric, error) {
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
	if err := loadDashboardRankings(finder, metrics, r, s, filter); err != nil {
		return nil, nil, err
	}
	return metrics, successRate, nil
}

func loadDashboardRankings(finder DashboardDataFinder, metrics *LogMetrics, r dao.ObsRange, s dao.Scope, filter dao.ObsFilter) error {
	users, err := finder.Leaderboard("user", "tokens", 10, r, s, filter)
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return err
	}
	models, err := finder.Leaderboard("model", "tokens", 10, r, s, filter)
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return err
	}
	channels, err := finder.Leaderboard("channel", "tokens", 10, r, s, filter)
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return err
	}
	metrics.Leaderboard = &LeaderboardBlock{
		Users: users, Models: models, Channels: channels,
		AvailableMetrics: []string{"cost", "requests", "tokens", "tps", "ttft"},
	}
	byModel, err := finder.SpeedCompare("model", r, s, filter)
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return err
	}
	byChannel, err := finder.SpeedCompare("channel", r, s, filter)
	if errors.Is(err, dao.ErrLogDatabaseUnavailable) {
		return err
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

func degradeDashboardLogMetrics(resp DashboardResponse) DashboardResponse {
	resp.DataStatus.LogDB = "unavailable"
	resp.Kpis.SuccessRate = nil
	resp.LogMetrics = nil
	return resp
}
