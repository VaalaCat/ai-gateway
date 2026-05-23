package log

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// parseInsightsRange 是 /v1/logs/insights 端点的 query 缺省值解析:
// end<=0 → now; start<=0 → end-86400; gran 始终 hour (logs/insights 的 spark 固定 24-slot hour)。
// 不跨 package 复用 stats.parseObsRange,故意复制一份保持 package 独立。
func parseInsightsRange(start, end int64) dao.ObsRange {
	if end <= 0 {
		end = time.Now().UTC().Unix()
	}
	if start <= 0 {
		start = end - 86400
	}
	return dao.ObsRange{Start: start, End: end, Gran: dao.GranHour}
}

func toDaoScope(s *middleware.RequestScope) dao.Scope {
	if s == nil {
		return dao.Scope{}
	}
	return dao.Scope{IsAdmin: s.IsAdmin, UserID: s.UserID}
}

// Insights 返回 logs 维度的洞察:
//  1. totals: total/failed/p95/最慢请求 + 三条 24h spark;
//  2. error_by_stage: 失败请求按 stage 分布 (admin-only;user scope 该字段为空)。
//
// 窗口超过 ObsRange.Validate() 上限时返回 400 RangeOutOfBounds (gran=hour 上限 7 天)。
func (h *Handler) Insights(c *app.Context, req InsightsRequest) (InsightsResponse, error) {
	r := parseInsightsRange(req.Start, req.End)
	if err := r.Validate(); err != nil {
		return InsightsResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	scope := middleware.GetScope(c.Context)
	s := toDaoScope(scope)
	q := dao.NewAdminQuery(dao.NewContext(c.App))

	totals, err := q.Stats().LogsTotals(r, s)
	if err != nil {
		return InsightsResponse{}, api.InternalError("logs insights totals", err)
	}

	// ErrorDistribution 内部已对非 admin 返回 nil; nil → []ErrBucket{} 让前端拿到稳定数组。
	stageDist, err := q.Stats().ErrorDistribution("stage", r, s)
	if err != nil {
		return InsightsResponse{}, api.InternalError("logs insights error distribution", err)
	}
	if stageDist == nil {
		stageDist = []dao.ErrBucket{}
	}
	return InsightsResponse{Totals: totals, ErrorByStage: stageDist}, nil
}
