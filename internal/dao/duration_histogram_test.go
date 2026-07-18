package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
)

func TestBatchUpsertDurationHistogram_MergesSlotsAndMax(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	row := DurationHistogramRow{Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", MaxDurationMs: 4200}
	row.Hist[4] = 3 // [3000,5000)
	if err := m.BatchUpsertDurationHistogram([]DurationHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
	// 同维度再来一批:槽相加、max 取大
	row2 := row
	row2.Hist = [durhist.NumSlots]int64{}
	row2.Hist[4] = 2
	row2.Hist[11] = 1 // [45000,60000)
	row2.MaxDurationMs = 58000
	if err := m.BatchUpsertDurationHistogram([]DurationHistogramRow{row2}); err != nil {
		t.Fatal(err)
	}

	var got models.UsageDurationHistogram
	if err := db.Where("date = ? AND hour = ? AND channel_id = ?", "2026-07-08", 8, 5).First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.H4 != 5 || got.H11 != 1 {
		t.Fatalf("slots = h4:%d h11:%d, want 5/1", got.H4, got.H11)
	}
	if got.MaxDurationMs != 58000 {
		t.Fatalf("max = %d, want 58000 (取大不覆盖)", got.MaxDurationMs)
	}
	// max 回退不生效(再报一个更小的 max)
	row3 := row
	row3.Hist = [durhist.NumSlots]int64{}
	row3.MaxDurationMs = 100
	_ = m.BatchUpsertDurationHistogram([]DurationHistogramRow{row3})
	db.Where("date = ? AND hour = ?", "2026-07-08", 8).First(&got)
	if got.MaxDurationMs != 58000 {
		t.Fatalf("max regressed to %d, want stay 58000", got.MaxDurationMs)
	}
}

func TestUpsertDurationHistogram_FromLog(t *testing.T) {
	app, db := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()

	// success:成功日志按 duration 归槽
	log := &models.UsageLog{Status: 1, Duration: 9500, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600} // 2026-07-08 08:00 UTC
	if err := m.UpsertDurationHistogram(log); err != nil {
		t.Fatal(err)
	}
	var got models.UsageDurationHistogram
	if err := db.First(&got).Error; err != nil {
		t.Fatal(err)
	}
	if got.H6 != 1 { // 9500 ∈ [7500,10000) → slot 6
		t.Fatalf("h6 = %d, want 1", got.H6)
	}
	if got.MaxDurationMs != 9500 {
		t.Fatalf("max = %d, want 9500", got.MaxDurationMs)
	}

	// failure 日志不入直方图(口径 status=1)
	fail := &models.UsageLog{Status: 0, Duration: 99999, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600}
	if err := m.UpsertDurationHistogram(fail); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&models.UsageDurationHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rows = %d, want 1 (failed log 不产生新行)", cnt)
	}
}

func TestBatchUpsertDurationHistogram_EmptyAndNil(t *testing.T) { // boundary
	app, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertDurationHistogram(nil); err != nil {
		t.Fatalf("nil rows: %v", err)
	}
	if err := m.UpsertDurationHistogram(nil); err != nil {
		t.Fatalf("nil log: %v", err)
	}
}

func TestRebuildIncludesDurationHistogram(t *testing.T) {
	app, db := setupTestApp(t)
	// 造一条历史成功日志 → 全量 rebuild → 侧表应被回填
	log := models.UsageLog{RequestID: "r-hist-1", Status: 1, Duration: 9500, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", CreatedAt: 1783497600}
	if err := db.Create(&log).Error; err != nil {
		t.Fatal(err)
	}
	m := NewAdminMutation(NewContext(app)).Billing()
	// 空 Targets = 全量重建（含 duration_histogram）；StartDate/EndDate 按既有 rebuild
	// 测试(billing_test.go)的调用方式限定范围 —— 空日期范围会触发 gorm 的
	// "WHERE conditions required" 全表 DELETE 保护,故显式给出覆盖该日志的日期。
	if _, err := m.RebuildDailyRollups(BillingRebuildFilter{
		StartDate: "2026-07-08", EndDate: "2026-07-08",
	}); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&models.UsageDurationHistogram{}).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("rebuild 后侧表行数 = %d, want 1", cnt)
	}
}
