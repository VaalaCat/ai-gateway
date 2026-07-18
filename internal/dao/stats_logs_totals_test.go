package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// 2026-07-08 08:00-09:00 UTC 窗口内:2 成功(9500ms/58000ms)+ 1 失败
const histTestHourStart = int64(1783497600)

func seedRollups(t *testing.T, app *testApp) {
	t.Helper()
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertHourlyBucket([]HourlyBucketRow{{
		Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1",
		RequestCount: 3, SuccessCount: 2, FailedCount: 1, UpdatedAt: histTestHourStart,
	}}); err != nil {
		t.Fatal(err)
	}
	var row DurationHistogramRow
	row.Date, row.Hour, row.ChannelID, row.ModelName, row.AgentID = "2026-07-08", 8, 5, "gpt-4o", "a1"
	row.Hist[6] = 1  // 9500
	row.Hist[11] = 1 // 58000
	row.MaxDurationMs = 58000
	row.UpdatedAt = histTestHourStart
	if err := m.BatchUpsertDurationHistogram([]DurationHistogramRow{row}); err != nil {
		t.Fatal(err)
	}
}

func TestLogsTotals_AdminUsesRollups(t *testing.T) {
	app, _ := setupTestApp(t)
	seedRollups(t, app)
	// 故意不写 usage_logs 原表——若结果非零,证明读的是 rollup
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 || got.Failed != 1 {
		t.Fatalf("total/failed = %d/%d, want 3/1 (from rollups)", got.Total, got.Failed)
	}
	if got.SlowestMs != 58000 {
		t.Fatalf("slowest = %d, want 58000", got.SlowestMs)
	}
	// p95 over {9500,58000}:直方图插值应落在 slot11 [45000,60000) 内
	// (EstimatePercentile 对非溢出槽用 Edges[i] 当槽上界,不是 maxMs——见 durhist.go,
	// 故上界是 60000 而非 58000)
	if got.P95Ms < 45000 || got.P95Ms > 60000 {
		t.Fatalf("p95 = %d, want within [45000,60000]", got.P95Ms)
	}
}

func TestLogsTotals_FallsBackToRawWhenRollupEmpty(t *testing.T) {
	app, db := setupTestApp(t)
	// 只写原表,不写 rollup(模拟未回填窗口)
	db.Create(&models.UsageLog{RequestID: "r1", Status: 1, Duration: 5000, CreatedAt: histTestHourStart + 10})
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Fatalf("total = %d, want 1 (raw fallback)", got.Total)
	}
}

func TestLogsTotals_UserScopeStaysOnRaw(t *testing.T) {
	app, db := setupTestApp(t)
	seedRollups(t, app) // rollup 有数据但不带 user 维度
	db.Create(&models.UsageLog{RequestID: "r1", UserID: 42, Status: 1, Duration: 5000, CreatedAt: histTestHourStart + 10})
	db.Create(&models.UsageLog{RequestID: "r2", UserID: 7, Status: 1, Duration: 8000, CreatedAt: histTestHourStart + 20})
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: false, UserID: 42})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 {
		t.Fatalf("user-scope total = %d, want 1 (raw path with user filter)", got.Total)
	}
}

// seedBucketOnly 只写 usage_hourly_bucket,不写 usage_duration_histograms——
// 模拟"直方图侧表比小时桶新,部署后/rebuild 前尚未回填"的历史窗口。
func seedBucketOnly(t *testing.T, app *testApp) {
	t.Helper()
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertHourlyBucket([]HourlyBucketRow{{
		Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1",
		RequestCount: 3, SuccessCount: 2, FailedCount: 1, UpdatedAt: histTestHourStart,
	}}); err != nil {
		t.Fatal(err)
	}
}

func TestLogsTotals_FallsBackToRawWhenHistogramNotBackfilled(t *testing.T) {
	app, db := setupTestApp(t)
	seedBucketOnly(t, app) // bucket 有成功请求,但直方图侧表全空(未回填)
	db.Create(&models.UsageLog{RequestID: "r1", Status: 1, Duration: 5000, CreatedAt: histTestHourStart + 10})
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	// bucket 的 Total 会是 3,若走了 rollup 路径这里就该是 3——断言 1 证明确实回退到了原表。
	if got.Total != 1 {
		t.Fatalf("total = %d, want 1 (raw fallback, not bucket's 3)", got.Total)
	}
	if got.P95Ms <= 0 {
		t.Fatalf("p95 = %d, want > 0 (real value from raw, not zeroed histogram)", got.P95Ms)
	}
	if got.SlowestMs <= 0 {
		t.Fatalf("slowest = %d, want > 0 (real value from raw)", got.SlowestMs)
	}
}

func TestLogsTotals_AllFailedStaysOnRollupsWithoutHistogram(t *testing.T) {
	app, _ := setupTestApp(t)
	m := NewAdminMutation(NewContext(app)).Billing()
	if err := m.BatchUpsertHourlyBucket([]HourlyBucketRow{{
		Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1",
		RequestCount: 3, SuccessCount: 0, FailedCount: 3, UpdatedAt: histTestHourStart,
	}}); err != nil {
		t.Fatal(err)
	}
	// 无直方图行、无原表行——全失败窗口本就不该产生直方图数据,不应触发回退。
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 3 {
		t.Fatalf("total = %d, want 3 (rollup path; raw fallback has no rows so would be 0)", got.Total)
	}
	if got.P95Ms != 0 {
		t.Fatalf("p95 = %d, want 0 (no successful requests)", got.P95Ms)
	}
}

func TestLogsTotals_EmptyWindow(t *testing.T) { // boundary
	app, _ := setupTestApp(t)
	q := NewAdminQuery(NewContext(app)).Stats()
	got, err := q.LogsTotals(ObsRange{Start: histTestHourStart, End: histTestHourStart + 3600, Gran: GranHour}, Scope{IsAdmin: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 0 || got.P95Ms != 0 || len(got.SparkTotal) != 24 {
		t.Fatalf("empty window: %+v", got)
	}
}
