package stats

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// metricTrendValidMetrics is the exhaustive set MetricTrendGrouped understands.
// Mirrors dao.isValidTrendMetric (unexported); kept in sync manually since the
// handler needs to 400 before ever calling into the DAO layer.
var metricTrendValidMetrics = map[string]bool{
	"cost": true, "requests": true, "tokens": true,
	"ttft": true, "tps": true, "cache_hit_rate": true,
}

// MetricTrendRequest is /stats/metric-trend's input. Metric selects which
// value is charted (cost|requests|tokens|ttft|tps|cache_hit_rate); Dim selects
// the grouping (model|channel, author unsupported — no author data source).
type MetricTrendRequest struct {
	Metric string `form:"metric"`
	Stat   string `form:"stat"`
	Dim    string `form:"dim"`
	Start  int64  `form:"start"`
	End    int64  `form:"end"`
	Gran   string `form:"gran"`
	Model  string `form:"model"`
	TopN   int    `form:"top_n"`
	UserID uint   `form:"user_id"`
}

// MetricTrendResponse directly transcribes dao.MetricTrendStacked (buckets +
// series_order) for the frontend's multi-line trend chart.
type MetricTrendResponse struct {
	Metric      string                    `json:"metric"`
	Stat        string                    `json:"stat"`
	Unit        string                    `json:"unit"`
	Estimated   bool                      `json:"estimated"`
	Buckets     []dao.MetricStackedBucket `json:"buckets"`
	SeriesOrder []string                  `json:"series_order"`
}

// MetricTrend returns a per-model/channel time series for one metric, split
// into up to top-N series + an "others" fold. Ordinary users are locked to
// their own usage; admins may query global usage or lock to one user.
func (h *Handler) MetricTrend(c *app.Context, req MetricTrendRequest) (MetricTrendResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return MetricTrendResponse{}, api.ForbiddenError("authentication required")
	}
	effectiveUserID := req.UserID
	cacheScope := "admin"
	if !scope.IsAdmin {
		cacheScope = "user"
		effectiveUserID = scope.UserID
		if req.UserID != 0 && req.UserID != scope.UserID {
			return MetricTrendResponse{}, api.ForbiddenError("cannot query another user")
		}
	}
	if req.Dim != "model" && req.Dim != "channel" {
		return MetricTrendResponse{}, api.BadRequestError("dim must be \"model\" or \"channel\"", nil)
	}
	if !metricTrendValidMetrics[req.Metric] {
		return MetricTrendResponse{}, api.BadRequestError(
			"metric must be one of cost|requests|tokens|ttft|tps|cache_hit_rate", nil)
	}
	if !validMetricStat(req.Metric, req.Stat) {
		return MetricTrendResponse{}, api.ErrorWithCode(400, "InvalidMetricStat",
			"stat is not supported for metric", map[string]any{"metric": req.Metric, "stat": req.Stat})
	}
	stat := canonicalMetricStat(req.Metric, req.Stat)
	if effectiveUserID != 0 && req.Dim != "model" && (req.Metric == "ttft" || req.Metric == "tps") && (stat == "p95" || stat == "p5") {
		return MetricTrendResponse{}, api.BadRequestError("user percentile only supports model dimension", nil)
	}
	topN, err := api.ParseTopN(req.TopN)
	if err != nil {
		return MetricTrendResponse{}, err
	}

	r := parseObsRange(req.Start, req.End, req.Gran)
	if err := r.Validate(); err != nil {
		return MetricTrendResponse{}, api.ErrorWithCode(400, "RangeOutOfBounds",
			"range exceeds max days for granularity",
			map[string]any{"gran": string(r.Gran)})
	}

	load := func(ctx context.Context) (any, error) {
		q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
		stacked, err := q.Stats().MetricTrendGrouped(req.Metric, stat, req.Dim, r, toDaoScope(scope), topN, dao.ObsFilter{UserID: effectiveUserID, ModelName: req.Model})
		if err != nil {
			return MetricTrendResponse{}, api.InternalError("metric trend query failed", err)
		}
		return MetricTrendResponse{Metric: req.Metric, Stat: stat, Unit: metricTrendUnit(req.Metric), Estimated: stat == "p95" || stat == "p5", Buckets: stacked.Buckets, SeriesOrder: stacked.SeriesOrder}, nil
	}
	if h.Cache == nil {
		value, err := load(c.RequestContext())
		return value.(MetricTrendResponse), err
	}
	value, err := h.Cache.Get(c.RequestContext(), dao.QueryKey{
		Name: "stats.metric-trend", From: r.Start, To: r.End, Gran: string(r.Gran),
		Scope: cacheScope, UserID: effectiveUserID, Model: req.Model, Dim: req.Dim, Metric: req.Metric, Stat: stat, TopN: topN,
	}, load)
	if err != nil {
		return MetricTrendResponse{}, err
	}
	response, ok := value.(MetricTrendResponse)
	if !ok {
		return MetricTrendResponse{}, fmt.Errorf("metric trend cache returned %T", value)
	}
	return response, nil
}

func canonicalMetricStat(metric, stat string) string {
	if stat != "" {
		return stat
	}
	switch metric {
	case "ttft", "tps":
		return "avg"
	case "cache_hit_rate":
		return "ratio"
	default:
		return "sum"
	}
}

func validMetricStat(metric, stat string) bool {
	if stat == "" {
		return true
	}
	switch metric {
	case "ttft":
		return stat == "avg" || stat == "p95"
	case "tps":
		return stat == "avg" || stat == "p5"
	default:
		return false
	}
}

func metricTrendUnit(metric string) string {
	switch metric {
	case "cost":
		return "quota"
	case "requests":
		return "requests"
	case "tokens":
		return "tokens"
	case "ttft":
		return "ms"
	case "tps":
		return "tokens/s"
	case "cache_hit_rate":
		return "percent"
	default:
		return ""
	}
}
