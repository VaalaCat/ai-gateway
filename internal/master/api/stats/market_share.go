package stats

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// MarketShareRequest 是 /stats/market-share 的入参。
// dim 只接受 "model"|"channel"(author 本期不支持,见 dao.ErrUnsupportedMarketShareDim);
// start/end 缺省同 DashboardRequest 约定(end 缺省 now、start 缺省 end-86400)。
type MarketShareRequest struct {
	Dim   string `form:"dim"`
	Start int64  `form:"start"`
	End   int64  `form:"end"`
	Gran  string `form:"gran"`
	Model string `form:"model"`
	TopN  int    `form:"top_n"`
}

// MarketShareResponse 是 /stats/market-share 的返回,直接透传 dao.CostTrendStacked
// (buckets + series_order),供前端纵向堆叠柱状图使用。
type MarketShareResponse struct {
	Buckets     []dao.StackedBucket `json:"buckets"`
	SeriesOrder []string            `json:"series_order"`
}

// MarketShare 返回按 model/channel 维度分组的 token 量堆叠时间序列。admin-only。
func (h *Handler) MarketShare(c *app.Context, req MarketShareRequest) (MarketShareResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || !scope.IsAdmin {
		return MarketShareResponse{}, api.ForbiddenError("admin only")
	}
	if req.Dim != "model" && req.Dim != "channel" {
		return MarketShareResponse{}, api.BadRequestError("dim must be \"model\" or \"channel\"", nil)
	}
	topN, err := api.ParseTopN(req.TopN)
	if err != nil {
		return MarketShareResponse{}, err
	}

	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return MarketShareResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	load := func(ctx context.Context) (any, error) {
		q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
		stacked, err := q.Stats().MarketShareTrend(req.Dim, r, toDaoScope(scope), topN, dao.ObsFilter{ModelName: req.Model})
		if err != nil {
			return MarketShareResponse{}, api.InternalError("market share trend query failed", err)
		}
		return MarketShareResponse{Buckets: stacked.Buckets, SeriesOrder: stacked.SeriesOrder}, nil
	}
	if h.Cache == nil {
		value, err := load(c.RequestContext())
		return value.(MarketShareResponse), err
	}
	value, err := h.Cache.Get(c.RequestContext(), dao.QueryKey{
		Name: "stats.market-share", From: r.Start, To: r.End, Gran: string(r.Gran),
		Scope: "admin", Model: req.Model, Dim: req.Dim, TopN: topN,
	}, load)
	if err != nil {
		return MarketShareResponse{}, err
	}
	response, ok := value.(MarketShareResponse)
	if !ok {
		return MarketShareResponse{}, fmt.Errorf("market share cache returned %T", value)
	}
	return response, nil
}
