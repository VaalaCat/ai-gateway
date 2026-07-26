package stats

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestMetricTrend_Admin_ByModel_ReturnsStackedSeries(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	end := start + 86400

	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ModelName: "gpt-4o", AgentID: "ag-1",
		OwnerType: "admin", RequestCount: 10, TotalCost: 500,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ModelName: "claude-3", AgentID: "ag-1",
		OwnerType: "admin", RequestCount: 2, TotalCost: 900,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket 2: %v", err)
	}

	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("MetricTrend: %v", err)
	}
	if len(resp.Buckets) != 1 {
		t.Fatalf("len(buckets) = %d, want 1", len(resp.Buckets))
	}
	if resp.Buckets[0].Series["gpt-4o"] != 500 {
		t.Fatalf("gpt-4o cost = %v, want 500", resp.Buckets[0].Series["gpt-4o"])
	}
	if resp.Buckets[0].Series["claude-3"] != 900 {
		t.Fatalf("claude-3 cost = %v, want 900", resp.Buckets[0].Series["claude-3"])
	}
	// ranked by requests (10 > 2), not by cost (900 > 500).
	if len(resp.SeriesOrder) != 2 || resp.SeriesOrder[0] != "gpt-4o" {
		t.Fatalf("SeriesOrder = %v, want [gpt-4o claude-3] (gpt-4o has more requests)", resp.SeriesOrder)
	}
}

func TestMetricTrend_NonAdmin_IsLockedToSelf(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	date := time.Unix(start, 0).UTC().Format("2006-01-02")
	for _, log := range []models.UsageLog{
		{RequestID: "self", UserID: 1, ModelName: "self-model", Status: 1, TotalCost: 11, CreatedAt: start + 1},
		{RequestID: "other", UserID: 2, ModelName: "other-model", Status: 1, TotalCost: 99, CreatedAt: start + 1},
	} {
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	_ = date
	ctx := makeDashboardCtx(t, application, 1, false)
	resp, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.SeriesOrder) != 1 || resp.SeriesOrder[0] != "self-model" {
		t.Fatalf("series = %v, want self-model only", resp.SeriesOrder)
	}
	_, err = h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", UserID: 2, Start: start, End: end, Gran: "day"})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("other user error = %v, want 403", err)
	}
}

func TestMetricTrend_AdminUserCacheKeysAreIsolated(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.Cache = dao.NewStatsCache()
	start, end := dayRange()
	for _, log := range []models.UsageLog{
		{RequestID: "user-one", UserID: 1, ModelName: "model-one", Status: 1, TotalCost: 11, CreatedAt: start + 1},
		{RequestID: "user-two", UserID: 2, ModelName: "model-two", Status: 1, TotalCost: 22, CreatedAt: start + 1},
	} {
		if err := db.Create(&log).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := makeDashboardCtx(t, application, 99, true)
	one, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", UserID: 1, Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatal(err)
	}
	two, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", UserID: 2, Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(one.SeriesOrder, []string{"model-one"}) || !reflect.DeepEqual(two.SeriesOrder, []string{"model-two"}) {
		t.Fatalf("cache leaked user series: one=%v two=%v", one.SeriesOrder, two.SeriesOrder)
	}
}

func TestMetricTrend_InvalidDim_Returns400(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	_, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "author", Start: start, End: end, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 400 for unsupported dim, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}
}

func TestMetricTrend_InvalidMetric_Returns400(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	_, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "latency_p99", Dim: "model", Start: start, End: end, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 400 for unsupported metric, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}
}

func TestMetricTrend_NoData_ReturnsEmptyBuckets(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "ttft", Dim: "model", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("MetricTrend: %v", err)
	}
	if len(resp.Buckets) != 0 {
		t.Fatalf("len(buckets) = %d, want 0", len(resp.Buckets))
	}
}

func TestMetricTrendTopNAndRankingStayRequestCountBasedAcrossMetrics(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.Cache = dao.NewStatsCache()
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	for i := 0; i < 7; i++ {
		if err := db.Create(&models.UsageHourlyBucket{
			Date: date, Hour: 10, ChannelID: uint(i + 1), ModelName: fmt.Sprintf("model-%02d", i), AgentID: fmt.Sprintf("agent-%02d", i),
			OwnerType: "admin", RequestCount: int64(70 - i), TotalCost: int64(i+1) * 1000,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := makeDashboardCtx(t, application, 1, true)
	for _, topN := range []int{0, 5, 10, 20} {
		cost, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "cost", Dim: "model", Start: start, End: start + 86400, Gran: "day", TopN: topN})
		if err != nil {
			t.Fatalf("cost top_n=%d: %v", topN, err)
		}
		requests, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: "requests", Dim: "model", Start: start, End: start + 86400, Gran: "day", TopN: topN})
		if err != nil {
			t.Fatalf("requests top_n=%d: %v", topN, err)
		}
		if !reflect.DeepEqual(cost.SeriesOrder, requests.SeriesOrder) {
			t.Fatalf("top_n=%d metric changed ranking: cost=%v requests=%v", topN, cost.SeriesOrder, requests.SeriesOrder)
		}
		want := 7
		if topN == 0 || topN == 5 {
			want = 6
		}
		if len(cost.SeriesOrder) != want {
			t.Fatalf("top_n=%d series=%v", topN, cost.SeriesOrder)
		}
	}
}

func TestMetricTrendRejectsInvalidTopN(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	_, err := h.MetricTrend(makeDashboardCtx(t, application, 1, true), MetricTrendRequest{Metric: "cost", Dim: "model", Start: start, End: end, TopN: -1})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "InvalidTopN" {
		t.Fatalf("error = %v, want InvalidTopN", err)
	}
}

func TestMetricTrendMetricStatContract(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)

	valid := []struct {
		metric, stat string
		estimated    bool
	}{
		{metric: "ttft", stat: ""},
		{metric: "ttft", stat: "avg"},
		{metric: "ttft", stat: "p95", estimated: true},
		{metric: "tps", stat: ""},
		{metric: "tps", stat: "avg"},
		{metric: "tps", stat: "p5", estimated: true},
		{metric: "cost", stat: ""},
		{metric: "requests", stat: ""},
		{metric: "tokens", stat: ""},
		{metric: "cache_hit_rate", stat: ""},
	}
	for _, test := range valid {
		t.Run(test.metric+"_"+test.stat, func(t *testing.T) {
			resp, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: test.metric, Stat: test.stat, Dim: "model", Start: start, End: end, Gran: "day"})
			if err != nil {
				t.Fatal(err)
			}
			wantStat := test.stat
			if wantStat == "" {
				switch test.metric {
				case "ttft", "tps":
					wantStat = "avg"
				case "cache_hit_rate":
					wantStat = "ratio"
				default:
					wantStat = "sum"
				}
			}
			if resp.Metric != test.metric || resp.Stat != wantStat || resp.Estimated != test.estimated {
				t.Fatalf("response contract = metric:%q stat:%q estimated:%v", resp.Metric, resp.Stat, resp.Estimated)
			}
		})
	}

	invalid := []struct{ metric, stat string }{
		{metric: "ttft", stat: "p5"},
		{metric: "tps", stat: "p95"},
		{metric: "cost", stat: "avg"},
		{metric: "requests", stat: "p95"},
	}
	for _, test := range invalid {
		_, err := h.MetricTrend(ctx, MetricTrendRequest{Metric: test.metric, Stat: test.stat, Dim: "model", Start: start, End: end, Gran: "day"})
		var apiErr *api.APIError
		if !errors.As(err, &apiErr) || apiErr.Status != 400 || apiErr.Code != "InvalidMetricStat" {
			t.Fatalf("metric=%s stat=%s error=%v, want 400 InvalidMetricStat", test.metric, test.stat, err)
		}
	}
}

func TestMetricTrendValidationOrderForUserPercentiles(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	contexts := []struct {
		name      string
		ctx       *app.Context
		reqUserID uint
	}{
		{name: "ordinary", ctx: makeDashboardCtx(t, application, 7, false)},
		{name: "admin_locked", ctx: makeDashboardCtx(t, application, 1, true), reqUserID: 7},
	}
	for _, subject := range contexts {
		t.Run(subject.name, func(t *testing.T) {
			for _, pair := range []struct{ metric, stat string }{{"tps", "p95"}, {"ttft", "p5"}} {
				_, err := h.MetricTrend(subject.ctx, MetricTrendRequest{Metric: pair.metric, Stat: pair.stat, Dim: "channel", UserID: subject.reqUserID, Start: start, End: end, Gran: "day"})
				var apiErr *api.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != "InvalidMetricStat" {
					t.Fatalf("metric=%s stat=%s error=%v, want InvalidMetricStat", pair.metric, pair.stat, err)
				}
			}
			_, err := h.MetricTrend(subject.ctx, MetricTrendRequest{Metric: "tps", Stat: "p95", Dim: "author", UserID: subject.reqUserID, Start: start, End: end, Gran: "day"})
			var apiErr *api.APIError
			if !errors.As(err, &apiErr) || apiErr.Code == "InvalidMetricStat" || apiErr.Message != "dim must be \"model\" or \"channel\"" {
				t.Fatalf("invalid dim/stat error=%v, want dim validation first", err)
			}
		})
	}
}
