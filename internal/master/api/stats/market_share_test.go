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

func TestMarketShare_Admin_ByModel_ReturnsStackedSeries(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	date := "2026-05-20"
	start = mustUnixDate(t, date)
	end = start + 86400

	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ModelName: "gpt-4o",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 100, CompletionTokens: 50,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ModelName: "claude-3",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 10, CompletionTokens: 5,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket 2: %v", err)
	}

	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MarketShare(ctx, MarketShareRequest{Dim: "model", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("MarketShare: %v", err)
	}
	if len(resp.Buckets) != 1 {
		t.Fatalf("len(buckets) = %d, want 1", len(resp.Buckets))
	}
	if resp.Buckets[0].Series["gpt-4o"] != 150 {
		t.Fatalf("gpt-4o tokens = %d, want 150", resp.Buckets[0].Series["gpt-4o"])
	}
	if resp.Buckets[0].Series["claude-3"] != 15 {
		t.Fatalf("claude-3 tokens = %d, want 15", resp.Buckets[0].Series["claude-3"])
	}
	if len(resp.SeriesOrder) != 2 || resp.SeriesOrder[0] != "gpt-4o" {
		t.Fatalf("SeriesOrder = %v, want [gpt-4o claude-3] (gpt-4o has more tokens)", resp.SeriesOrder)
	}
}

func TestMarketShare_Admin_ByChannel_UsesChannelName(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	end := start + 86400

	if err := db.Create(&models.BillingHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "openai-shared", ModelName: "gpt-4o",
		OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 60, CompletionTokens: 40,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}

	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MarketShare(ctx, MarketShareRequest{Dim: "channel", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("MarketShare: %v", err)
	}
	if len(resp.SeriesOrder) != 1 || resp.SeriesOrder[0] != "openai-shared" {
		t.Fatalf("SeriesOrder = %v, want [openai-shared]", resp.SeriesOrder)
	}
	if resp.Buckets[0].Series["openai-shared"] != 100 {
		t.Fatalf("openai-shared tokens = %d, want 100", resp.Buckets[0].Series["openai-shared"])
	}
}

func TestMarketShare_NonAdmin_Returns403(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, false)
	_, err := h.MarketShare(ctx, MarketShareRequest{Dim: "model", Start: start, End: end, Gran: "day"})
	if err == nil {
		t.Fatalf("expected 403 for non-admin, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 403 {
		t.Fatalf("Status = %d, want 403", apiErr.Status)
	}
}

func TestMarketShare_InvalidDim_Returns400(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	_, err := h.MarketShare(ctx, MarketShareRequest{Dim: "author", Start: start, End: end, Gran: "day"})
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

func TestMarketShare_NoData_ReturnsEmptyBuckets(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MarketShare(ctx, MarketShareRequest{Dim: "model", Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("MarketShare: %v", err)
	}
	if len(resp.Buckets) != 0 {
		t.Fatalf("len(buckets) = %d, want 0", len(resp.Buckets))
	}
}

func TestMarketShareTopNRanksByWindowTokensAndFoldsExactOthers(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	h.Cache = dao.NewStatsCache()
	date := "2026-05-20"
	start := mustUnixDate(t, date)
	for i := 0; i < 7; i++ {
		if err := db.Create(&models.BillingHourlyBucket{
			Date: date, Hour: 10, ChannelID: uint(i + 1), ModelName: fmt.Sprintf("model-%02d", i),
			OwnerType: "admin", RequestCount: 100, PromptTokens: int64(70 - i),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.MarketShare(ctx, MarketShareRequest{Dim: "model", Start: start, End: start + 86400, Gran: "day", TopN: 5})
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.SeriesOrder; !reflect.DeepEqual(got, []string{"model-00", "model-01", "model-02", "model-03", "model-04", "others"}) {
		t.Fatalf("series order = %v", got)
	}
	if got := resp.Buckets[0].Series["others"]; got != 129 {
		t.Fatalf("others = %d, want 129", got)
	}
	resp, err = h.MarketShare(ctx, MarketShareRequest{Dim: "model", Start: start, End: start + 86400, Gran: "day", TopN: 10})
	if err != nil || len(resp.SeriesOrder) != 7 {
		t.Fatalf("top_n=10 response=%v err=%v", resp.SeriesOrder, err)
	}
}

func TestMarketShareRejectsInvalidTopN(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	_, err := h.MarketShare(makeDashboardCtx(t, application, 1, true), MarketShareRequest{Dim: "model", Start: start, End: end, TopN: 7})
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "InvalidTopN" {
		t.Fatalf("error = %v, want InvalidTopN", err)
	}
}
