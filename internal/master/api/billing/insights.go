package billing

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// billing insights 默认 top-N model;超出折叠为 "others"。
const insightsTopModels = 5

// parseInsightsRange 是 billing/insights 端点专用 query 缺省值解析:
// end<=0 → now; start<=0 → end-86400; gran!="hour" → GranDay。
// (与 stats.parseObsRange 同语义, 但故意复制一份避免跨 package 依赖。)
func parseInsightsRange(start, end int64, gran string) dao.ObsRange {
	if end <= 0 {
		end = time.Now().UTC().Unix()
	}
	if start <= 0 {
		start = end - 86400
	}
	g := dao.GranDay
	if gran == "hour" {
		g = dao.GranHour
	}
	return dao.ObsRange{Start: start, End: end, Gran: g}
}

func toDaoScope(s *middleware.RequestScope) dao.Scope {
	if s == nil {
		return dao.Scope{}
	}
	return dao.Scope{IsAdmin: s.IsAdmin, UserID: s.UserID}
}

// Insights 返回 billing 维度的两块洞察:
//  1. cost_trend_stacked: 时间桶 × model_name 堆叠成本 (Phase 1 仅 stack=model);
//  2. cache_saving: 缓存命中带来的 tokens / cost 节省概览。
//
// stack 参数当前仅 model 生效; user/channel 等其它值会被静默回退为 model
// (Phase 1 是 admin 观测面板,等用户需要再开)。
//
// 窗口超过 ObsRange.Validate() 上限时返回 400 RangeOutOfBounds。
func (h *Handler) Insights(c *app.Context, req InsightsRequest) (InsightsResponse, error) {
	r := parseInsightsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return InsightsResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	scope := middleware.GetScope(c.Context)
	s := toDaoScope(scope)
	q := dao.NewAdminQuery(dao.NewContext(c.App))

	stacked, err := q.Stats().CostTrendStackedByModel(r, s, insightsTopModels)
	if err != nil {
		return InsightsResponse{}, api.InternalError("billing insights cost trend", err)
	}
	saving, err := q.Stats().CacheSaving(r, s)
	if err != nil {
		return InsightsResponse{}, api.InternalError("billing insights cache saving", err)
	}
	return InsightsResponse{
		CostTrendStacked: stacked,
		CacheSaving:      saving,
	}, nil
}
