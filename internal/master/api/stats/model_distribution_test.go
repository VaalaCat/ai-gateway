package stats

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestModelDistributionRanksByWindowRequestCountAndFoldsOthers(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.Cache = dao.NewStatsCache()
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	for i := 0; i < 7; i++ {
		if err := db.Create(&models.BillingHourlyBucket{
			Date: date, Hour: 10, ChannelID: uint(i + 1), ModelName: fmt.Sprintf("model-%02d", i),
			OwnerType: "admin", RequestCount: int64(70 - i), TotalCost: int64(i+1) * 10000,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.ModelDistribution(ctx, ModelDistributionRequest{Start: start, End: start + 86400, Gran: "day", TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{"model-00", "model-01", "model-02", "model-03", "model-04", "others"}
	if !reflect.DeepEqual(resp.SeriesOrder, wantOrder) {
		t.Fatalf("series order = %v, want %v", resp.SeriesOrder, wantOrder)
	}
	if got := resp.Buckets[len(resp.Buckets)-1]; got.Name != "others" || got.Value != 129 {
		t.Fatalf("others = %+v, want value 129", got)
	}
	var ratioSum float64
	for _, bucket := range resp.Buckets {
		ratioSum += bucket.Ratio
	}
	if ratioSum < 0.999999 || ratioSum > 1.000001 {
		t.Fatalf("ratio sum = %f", ratioSum)
	}

	resp10, err := h.ModelDistribution(ctx, ModelDistributionRequest{Start: start, End: start + 86400, Gran: "day", TopN: 10})
	if err != nil || len(resp10.Buckets) != 7 {
		t.Fatalf("top_n=10 buckets=%v err=%v", resp10.Buckets, err)
	}
}

func TestModelDistributionRejectsInvalidTopNAndNonAdmin(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	_, err := h.ModelDistribution(makeDashboardCtx(t, application, 1, true), ModelDistributionRequest{Start: start, End: end, TopN: -20})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "InvalidTopN" {
		t.Fatalf("invalid top_n error = %v", err)
	}
	_, err = h.ModelDistribution(makeDashboardCtx(t, application, 1, false), ModelDistributionRequest{Start: start, End: end})
	if !errors.As(err, &apiErr) || apiErr.Status != 403 {
		t.Fatalf("non-admin error = %v", err)
	}
}

func TestModelDistributionDefaultTopNIsFive(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	for i := 0; i < 6; i++ {
		if err := db.Create(&models.BillingHourlyBucket{Date: date, Hour: i, ChannelID: uint(i + 1), ModelName: fmt.Sprintf("m%d", i), OwnerType: "admin", RequestCount: int64(i + 1)}).Error; err != nil {
			t.Fatal(err)
		}
	}
	resp, err := h.ModelDistribution(makeDashboardCtx(t, application, 1, true), ModelDistributionRequest{Start: start, End: start + 86400})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.SeriesOrder) != 6 || resp.SeriesOrder[5] != "others" {
		t.Fatalf("default series = %v", resp.SeriesOrder)
	}
}
