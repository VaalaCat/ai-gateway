package stats

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// mustUnixDate parses a YYYY-MM-DD date into the UTC midnight unix timestamp,
// so fixtures can pin a fixed date instead of relying on "today".
func mustUnixDate(t *testing.T, date string) int64 {
	t.Helper()
	tm, err := time.Parse("2006-01-02", date)
	if err != nil {
		t.Fatalf("parse date %q: %v", date, err)
	}
	return tm.UTC().Unix()
}

func TestChannelModelBreakdown_Admin_ReturnsRows(t *testing.T) {
	h, db, application := newDashboardTestCtx(t)
	start, end := dayRange()
	date := "2026-05-20"
	// re-anchor start/end to the seeded date to avoid relying on "today" for a fixed fixture
	start = mustUnixDate(t, date)
	end = start + 86400

	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "ch-5", ModelName: "gpt-4o",
		AgentID: "ag-1", OwnerType: "admin", RequestCount: 3, SuccessCount: 3,
		PromptTokens: 30, CompletionTokens: 15, TotalCost: 300, RawCost: 400,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket: %v", err)
	}
	if err := db.Create(&models.UsageHourlyBucket{
		Date: date, Hour: 10, ChannelID: 5, ChannelName: "ch-5", ModelName: "claude-3",
		AgentID: "ag-1", OwnerType: "admin", RequestCount: 1, SuccessCount: 1,
		PromptTokens: 10, CompletionTokens: 5, TotalCost: 50, RawCost: 50,
	}).Error; err != nil {
		t.Fatalf("seed hourly bucket 2: %v", err)
	}

	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.ChannelModelBreakdown(ctx, ChannelModelBreakdownRequest{ChannelID: 5, Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("ChannelModelBreakdown: %v", err)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2", len(resp.Rows))
	}
	if resp.Rows[0].ModelName != "gpt-4o" {
		t.Fatalf("rows[0].ModelName = %q, want gpt-4o (higher total_cost first)", resp.Rows[0].ModelName)
	}
	if resp.Rows[0].TotalCost != 300 || resp.Rows[0].RawCost != 400 {
		t.Fatalf("rows[0] billed/raw = %d/%d, want 300/400", resp.Rows[0].TotalCost, resp.Rows[0].RawCost)
	}
	if resp.Rows[1].ModelName != "claude-3" {
		t.Fatalf("rows[1].ModelName = %q, want claude-3", resp.Rows[1].ModelName)
	}
}

func TestChannelModelBreakdown_NonAdmin_Returns403(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, false)
	_, err := h.ChannelModelBreakdown(ctx, ChannelModelBreakdownRequest{ChannelID: 5, Start: start, End: end, Gran: "day"})
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

func TestChannelModelBreakdown_NoData_ReturnsEmptyRows(t *testing.T) {
	h, _, application := newDashboardTestCtx(t)
	start, end := dayRange()
	ctx := makeDashboardCtx(t, application, 1, true)
	resp, err := h.ChannelModelBreakdown(ctx, ChannelModelBreakdownRequest{ChannelID: 999, Start: start, End: end, Gran: "day"})
	if err != nil {
		t.Fatalf("ChannelModelBreakdown: %v", err)
	}
	if len(resp.Rows) != 0 {
		t.Fatalf("len(rows) = %d, want 0", len(resp.Rows))
	}
}
