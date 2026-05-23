package log

import (
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

// seedInsightsLog 写 usage_logs 一行;暴露 status/duration/error_stage 三个字段
// 让 totals / spark / error_by_stage 三路径都被覆盖。
func seedInsightsLog(t *testing.T, db *gorm.DB, userID uint, ts int64, status, duration int, stage, reqID string) {
	t.Helper()
	if err := db.Select("*").Create(&models.UsageLog{
		UserID:    userID,
		ChannelID: 5,
		ModelName: "gpt-4o",
		AgentID:   "ag-1",
		Status:    status,
		Duration:  duration,
		ErrorStage: stage,
		RequestID: reqID,
		CreatedAt: ts,
	}).Error; err != nil {
		t.Fatalf("seed usage log: %v", err)
	}
}

func insightsDayWindow() (int64, int64) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	end := start + 86400
	return start, end
}

func TestLogsInsights_Admin_PopulatesTotalsAndStages(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	start, end := insightsDayWindow()
	// 3 条成功 (duration 100/200/300), 2 条失败 (error stage upstream_dispatch + outbound_encode).
	seedInsightsLog(t, db, 1, start+3600, 1, 100, "", "ok-1")
	seedInsightsLog(t, db, 1, start+3700, 1, 200, "", "ok-2")
	seedInsightsLog(t, db, 1, start+3800, 1, 300, "", "ok-3")
	seedInsightsLog(t, db, 2, start+3900, 0, 0, "upstream_dispatch", "fail-1")
	seedInsightsLog(t, db, 2, start+4000, 0, 0, "outbound_encode", "fail-2")

	ctx := makeCtx(application, 1, true)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("Insights admin: %v", err)
	}
	if resp.Totals.Total != 5 {
		t.Fatalf("Totals.Total = %d, want 5", resp.Totals.Total)
	}
	if resp.Totals.Failed != 2 {
		t.Fatalf("Totals.Failed = %d, want 2", resp.Totals.Failed)
	}
	// p95 over 3 successes (100, 200, 300); offset = 3*95/100 = 2 → 第 3 个 = 300。
	if resp.Totals.P95Ms != 300 {
		t.Fatalf("Totals.P95Ms = %d, want 300", resp.Totals.P95Ms)
	}
	if resp.Totals.SlowestMs != 300 {
		t.Fatalf("Totals.SlowestMs = %d, want 300", resp.Totals.SlowestMs)
	}
	if len(resp.Totals.SparkTotal) != 24 {
		t.Fatalf("SparkTotal len = %d, want 24", len(resp.Totals.SparkTotal))
	}
	if len(resp.Totals.SparkFailed) != 24 {
		t.Fatalf("SparkFailed len = %d, want 24", len(resp.Totals.SparkFailed))
	}
	if len(resp.Totals.SparkP95) != 24 {
		t.Fatalf("SparkP95 len = %d, want 24", len(resp.Totals.SparkP95))
	}
	if len(resp.ErrorByStage) != 2 {
		t.Fatalf("ErrorByStage len = %d, want 2 (upstream_dispatch + outbound_encode)", len(resp.ErrorByStage))
	}
	// 各占 0.5 比例。
	for _, e := range resp.ErrorByStage {
		if e.Count != 1 {
			t.Fatalf("stage %q: count = %d, want 1", e.Stage, e.Count)
		}
		if e.Ratio < 0.49 || e.Ratio > 0.51 {
			t.Fatalf("stage %q: ratio = %f, want ~0.5", e.Stage, e.Ratio)
		}
	}
}

func TestLogsInsights_User_OnlyOwnLogs_NoErrorByStage(t *testing.T) {
	h, db, application := newLogTestCtx(t)
	start, end := insightsDayWindow()
	// user 1: 1 成功 (200ms); user 2: 1 失败 → 给 user1 scope 看应只统计自己。
	seedInsightsLog(t, db, 1, start+3600, 1, 200, "", "u1-ok")
	seedInsightsLog(t, db, 2, start+3700, 0, 0, "upstream_dispatch", "u2-fail")

	ctx := makeCtx(application, 1, false)
	resp, err := h.Insights(ctx, InsightsRequest{Start: start, End: end})
	if err != nil {
		t.Fatalf("Insights user: %v", err)
	}
	if resp.Totals.Total != 1 {
		t.Fatalf("user: Totals.Total = %d, want 1 (only own log)", resp.Totals.Total)
	}
	if resp.Totals.Failed != 0 {
		t.Fatalf("user: Totals.Failed = %d, want 0", resp.Totals.Failed)
	}
	// p95 over 1 success (200): offset = 1*95/100 = 0 → 200。
	if resp.Totals.P95Ms != 200 {
		t.Fatalf("user: Totals.P95Ms = %d, want 200", resp.Totals.P95Ms)
	}
	// ErrorByStage 在非 admin 下应为空数组 (DAO 返回 nil 被规范化为 [])。
	if resp.ErrorByStage == nil {
		t.Fatalf("user: ErrorByStage should be non-nil empty slice")
	}
	if len(resp.ErrorByStage) != 0 {
		t.Fatalf("user: ErrorByStage len = %d, want 0", len(resp.ErrorByStage))
	}
}

func TestLogsInsights_RangeOutOfBounds_Returns400(t *testing.T) {
	h, _, application := newLogTestCtx(t)
	now := time.Now().UTC().Unix()
	// logs/insights gran 固定 hour,max 7 天;给 10 天必越界。
	start := now - 10*86400
	ctx := makeCtx(application, 1, true)
	_, err := h.Insights(ctx, InsightsRequest{Start: start, End: now})
	if err == nil {
		t.Fatalf("expected 400 RangeOutOfBounds, got nil")
	}
	var apiErr *api.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v (%T), want *api.APIError", err, err)
	}
	if apiErr.Status != 400 {
		t.Fatalf("Status = %d, want 400", apiErr.Status)
	}
	if apiErr.Code != "RangeOutOfBounds" {
		t.Fatalf("Code = %q, want RangeOutOfBounds", apiErr.Code)
	}
	if got, _ := apiErr.Details["gran"].(string); got != "hour" {
		t.Fatalf("Details.gran = %q, want hour", got)
	}
}
