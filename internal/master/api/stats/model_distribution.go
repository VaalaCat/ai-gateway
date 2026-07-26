package stats

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type ModelDistributionRequest struct {
	Start  int64  `form:"start"`
	End    int64  `form:"end"`
	Gran   string `form:"gran"`
	Model  string `form:"model"`
	UserID uint   `form:"user_id"`
	TopN   int    `form:"top_n"`
}

type ModelDistributionResponse struct {
	Buckets     []dao.Bucket `json:"buckets"`
	SeriesOrder []string     `json:"series_order"`
}

func (h *Handler) ModelDistribution(c *app.Context, req ModelDistributionRequest) (ModelDistributionResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil || !scope.IsAdmin {
		return ModelDistributionResponse{}, api.ForbiddenError("admin only")
	}
	topN, err := api.ParseTopN(req.TopN)
	if err != nil {
		return ModelDistributionResponse{}, err
	}
	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return ModelDistributionResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity", map[string]any{"gran": string(r.Gran)})
	}
	filter := dao.ObsFilter{ModelName: req.Model, UserID: req.UserID}
	load := func(ctx context.Context) (any, error) {
		q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
		buckets, err := q.Stats().ModelDistribution(r, toDaoScope(scope), topN, filter)
		if err != nil {
			return ModelDistributionResponse{}, api.InternalError("model distribution query failed", err)
		}
		order := make([]string, len(buckets))
		for i := range buckets {
			order[i] = buckets[i].Name
		}
		return ModelDistributionResponse{Buckets: buckets, SeriesOrder: order}, nil
	}
	if h.Cache == nil {
		value, err := load(c.RequestContext())
		return value.(ModelDistributionResponse), err
	}
	value, err := h.Cache.Get(c.RequestContext(), dao.QueryKey{
		Name: "stats.model-distribution", From: r.Start, To: r.End, Gran: string(r.Gran),
		Scope: "admin", UserID: filter.UserID, Model: filter.ModelName, TopN: topN,
	}, load)
	if err != nil {
		return ModelDistributionResponse{}, err
	}
	response, ok := value.(ModelDistributionResponse)
	if !ok {
		return ModelDistributionResponse{}, fmt.Errorf("model distribution cache returned %T", value)
	}
	return response, nil
}
