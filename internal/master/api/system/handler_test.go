package system

import (
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestCleanupHourlyDeletesEveryLogAggregateInOneTransaction(t *testing.T) {
	core := setupTestDB(t)
	logDB := setupTestDB(t)
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	oldDate := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	newDate := time.Now().UTC().Format("2006-01-02")
	seedHourlyCleanupRows(t, logDB, oldDate, newDate)
	requireNoBillingHourlyDelete(t, core, oldDate)

	h := &Handler{}
	c := newTestContextWithApp(t, application)
	preview, err := h.CleanupPreview(c, CleanupPreviewRequest{Target: "hourly_buckets", RetainDays: 7})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Total != 12 || preview.ToDelete != 6 || len(preview.Tables) != 6 {
		t.Fatalf("preview=%+v, want six per-table counts and totals 12/6", preview)
	}
	for _, table := range preview.Tables {
		if table.Total != 2 || table.ToDelete != 1 {
			t.Fatalf("preview table=%+v, want total=2 to_delete=1", table)
		}
	}

	resp, err := h.Cleanup(c, cleanupRequest("hourly_buckets", 7))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Deleted != 6 {
		t.Fatalf("Deleted=%d, want 6", resp.Deleted)
	}
	for _, table := range dao.LogCleanupTables("hourly_buckets", app.DatabaseLayoutSplit) {
		var count int64
		if err := logDB.Table(table.Name).Where("date = ?", oldDate).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("%s old count=%d err=%v", table.Name, count, err)
		}
	}
}

func TestCleanupHourlyRollsBackWhenThirdTableFails(t *testing.T) {
	core := setupTestDB(t)
	logDB := setupTestDB(t)
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	oldDate := time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02")
	newDate := time.Now().UTC().Format("2006-01-02")
	seedHourlyCleanupRows(t, logDB, oldDate, newDate)
	if err := logDB.Migrator().DropTable(&models.UsageTTFTHistogram{}); err != nil {
		t.Fatal(err)
	}

	_, err := (&Handler{}).Cleanup(newTestContextWithApp(t, application), cleanupRequest("hourly_buckets", 7))
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	for _, table := range dao.LogCleanupTables("hourly_buckets", app.DatabaseLayoutSplit)[:2] {
		var count int64
		if err := logDB.Table(table.Name).Where("date = ?", oldDate).Count(&count).Error; err != nil || count != 1 {
			t.Fatalf("%s rollback count=%d err=%v", table.Name, count, err)
		}
	}
}

func cleanupRequest(target string, retainDays int) CleanupRequest {
	return CleanupRequest{Target: target, RetainDays: retainDays, CutoffUnix: time.Now().UTC().AddDate(0, 0, -retainDays).Unix()}
}

func TestCleanupRejectsInvalidCutoff(t *testing.T) {
	db := setupTestDB(t)
	h := &Handler{}
	c := newTestContext(t, db)
	now := time.Now().UTC()
	for _, req := range []CleanupRequest{
		{Target: "logs", RetainDays: 7},
		{Target: "logs", RetainDays: 7, CutoffUnix: now.Add(time.Hour).Unix()},
		{Target: "logs", RetainDays: 7, CutoffUnix: now.AddDate(0, 0, -30).Unix()},
	} {
		if _, err := h.Cleanup(c, req); err == nil {
			t.Fatalf("Cleanup(%+v) succeeded, want invalid cutoff", req)
		}
	}
}

func newTestContextWithApp(t *testing.T, application app.Application) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	return &app.Context{Context: ginCtx, App: application, OwnerContext: t.Context()}
}

func seedHourlyCleanupRows(t *testing.T, db *gorm.DB, oldDate, newDate string) {
	t.Helper()
	if err := db.AutoMigrate(&models.UsageUserTTFTHistogram{}, &models.UsageUserTPSHistogram{}); err != nil {
		t.Fatal(err)
	}
	rows := []any{
		&models.UsageHourlyBucket{Date: oldDate, ModelName: "old"}, &models.UsageHourlyBucket{Date: newDate, ModelName: "new"},
		&models.UsageDurationHistogram{Date: oldDate, ModelName: "old"}, &models.UsageDurationHistogram{Date: newDate, ModelName: "new"},
		&models.UsageTTFTHistogram{Date: oldDate, ModelName: "old"}, &models.UsageTTFTHistogram{Date: newDate, ModelName: "new"},
		&models.UsageTPSHistogram{Date: oldDate, ModelName: "old"}, &models.UsageTPSHistogram{Date: newDate, ModelName: "new"},
		&models.UsageUserTTFTHistogram{Date: oldDate, UserID: 1, ModelName: "old"}, &models.UsageUserTTFTHistogram{Date: newDate, UserID: 1, ModelName: "new"},
		&models.UsageUserTPSHistogram{Date: oldDate, UserID: 1, ModelName: "old"}, &models.UsageUserTPSHistogram{Date: newDate, UserID: 1, ModelName: "new"},
	}
	for _, row := range rows {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func requireNoBillingHourlyDelete(t *testing.T, db *gorm.DB, date string) {
	t.Helper()
	if err := db.AutoMigrate(&models.BillingHourlyBucket{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.BillingHourlyBucket{Date: date, ModelName: "billing"}).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		var count int64
		if err := db.Model(&models.BillingHourlyBucket{}).Where("date = ?", date).Count(&count).Error; err != nil || count != 1 {
			t.Errorf("billing hourly count=%d err=%v, want preserved", count, err)
		}
	})
}

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	models.AutoMigrate(db)
	return db
}

func newTestContext(t *testing.T, db *gorm.DB) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	testApp := app.NewApplication()
	testApp.SetCoreDB(db)
	return &app.Context{
		Context:      ginCtx,
		App:          testApp,
		OwnerContext: t.Context(),
	}
}

func TestStats_ReturnsTableCounts(t *testing.T) {
	db := setupTestDB(t)

	// Insert test data
	db.Create(&models.User{Username: "u1", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.User{Username: "u2", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.Token{UserID: 1, Key: "sk-1", Name: "t1", Status: 1, ExpiredAt: -1})

	h := &Handler{ConnectedCount: func() int { return 3 }}
	c := newTestContext(t, db)

	resp, err := h.Stats(c, StatsRequest{})
	if err != nil {
		t.Fatalf("Stats returned error: %v", err)
	}

	// Check table stats
	tableCounts := make(map[string]int64)
	for _, ts := range resp.Tables {
		tableCounts[ts.Name] = ts.Count
	}

	if tableCounts["users"] != 2 {
		t.Errorf("users count = %d, want 2", tableCounts["users"])
	}
	if tableCounts["tokens"] != 1 {
		t.Errorf("tokens count = %d, want 1", tableCounts["tokens"])
	}

	// Check system info
	if resp.System.GoVersion != runtime.Version() {
		t.Errorf("GoVersion = %q, want %q", resp.System.GoVersion, runtime.Version())
	}
	if resp.System.UptimeSec < 0 {
		t.Errorf("UptimeSec = %d, want >= 0", resp.System.UptimeSec)
	}
	if resp.System.OnlineAgents != 3 {
		t.Errorf("OnlineAgents = %d, want 3", resp.System.OnlineAgents)
	}
}

func TestCleanupPreview_ReturnsCorrectCounts(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now()
	oldTime := now.AddDate(0, 0, -30).Unix() // 30 days ago
	newTime := now.Unix()

	// Insert old traces
	for i := 0; i < 5; i++ {
		db.Create(&models.UsageLogTrace{
			RequestID: "old-" + string(rune('a'+i)),
			CreatedAt: oldTime,
		})
	}
	// Insert recent traces
	for i := 0; i < 3; i++ {
		db.Create(&models.UsageLogTrace{
			RequestID: "new-" + string(rune('a'+i)),
			CreatedAt: newTime,
		})
	}

	h := &Handler{}
	c := newTestContext(t, db)

	resp, err := h.CleanupPreview(c, CleanupPreviewRequest{
		Target:     "traces",
		RetainDays: 7,
	})
	if err != nil {
		t.Fatalf("CleanupPreview returned error: %v", err)
	}

	if resp.Total != 8 {
		t.Errorf("Total = %d, want 8", resp.Total)
	}
	if resp.ToDelete != 5 {
		t.Errorf("ToDelete = %d, want 5", resp.ToDelete)
	}
	if resp.Target != "traces" {
		t.Errorf("Target = %q, want %q", resp.Target, "traces")
	}
	if resp.RetainDays != 7 {
		t.Errorf("RetainDays = %d, want 7", resp.RetainDays)
	}
}

func TestCleanup_DeletesOldRecords(t *testing.T) {
	db := setupTestDB(t)

	now := time.Now()
	oldTime := now.AddDate(0, 0, -30).Unix()
	newTime := now.Unix()

	// Insert old traces
	for i := 0; i < 4; i++ {
		db.Create(&models.UsageLogTrace{
			RequestID: "old-" + string(rune('a'+i)),
			CreatedAt: oldTime,
		})
	}
	// Insert recent traces
	for i := 0; i < 2; i++ {
		db.Create(&models.UsageLogTrace{
			RequestID: "new-" + string(rune('a'+i)),
			CreatedAt: newTime,
		})
	}

	h := &Handler{}
	c := newTestContext(t, db)

	resp, err := h.Cleanup(c, cleanupRequest("traces", 7))
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}

	if resp.Deleted != 4 {
		t.Errorf("Deleted = %d, want 4", resp.Deleted)
	}

	// Verify remaining records
	var remaining int64
	db.Model(&models.UsageLogTrace{}).Count(&remaining)
	if remaining != 2 {
		t.Errorf("remaining records = %d, want 2", remaining)
	}
}

func TestCleanupPreview_HourlyBuckets_CountsByDate(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().UTC()

	// 4 old rows (30 天前的 date), 2 new rows (今天)
	oldDate := now.AddDate(0, 0, -30).Format("2006-01-02")
	newDate := now.Format("2006-01-02")
	for i := 0; i < 4; i++ {
		db.Create(&models.UsageHourlyBucket{
			Date: oldDate, Hour: i, ChannelID: 1, ModelName: "m", AgentID: "a",
		})
	}
	for i := 0; i < 2; i++ {
		db.Create(&models.UsageHourlyBucket{
			Date: newDate, Hour: i, ChannelID: 1, ModelName: "m", AgentID: "a",
		})
	}

	h := &Handler{}
	c := newTestContext(t, db)
	resp, err := h.CleanupPreview(c, CleanupPreviewRequest{
		Target: "hourly_buckets", RetainDays: 7,
	})
	if err != nil {
		t.Fatalf("CleanupPreview returned error: %v", err)
	}
	if resp.Total != 6 {
		t.Errorf("Total = %d, want 6", resp.Total)
	}
	if resp.ToDelete != 4 {
		t.Errorf("ToDelete = %d, want 4", resp.ToDelete)
	}
}

func TestCleanup_HourlyBuckets_DeletesByDate(t *testing.T) {
	db := setupTestDB(t)
	now := time.Now().UTC()

	oldDate := now.AddDate(0, 0, -30).Format("2006-01-02")
	newDate := now.Format("2006-01-02")
	for i := 0; i < 3; i++ {
		db.Create(&models.UsageHourlyBucket{
			Date: oldDate, Hour: i, ChannelID: 1, ModelName: "m", AgentID: "a",
		})
	}
	for i := 0; i < 2; i++ {
		db.Create(&models.UsageHourlyBucket{
			Date: newDate, Hour: i, ChannelID: 1, ModelName: "m", AgentID: "a",
		})
	}

	h := &Handler{}
	c := newTestContext(t, db)
	resp, err := h.Cleanup(c, cleanupRequest("hourly_buckets", 7))
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if resp.Deleted != 3 {
		t.Errorf("Deleted = %d, want 3", resp.Deleted)
	}

	var remaining int64
	db.Model(&models.UsageHourlyBucket{}).Count(&remaining)
	if remaining != 2 {
		t.Errorf("remaining = %d, want 2", remaining)
	}
}

func TestCleanup_InvalidTarget_Rejected(t *testing.T) {
	// binding `oneof=traces logs hourly_buckets` 应拒绝其他值。
	// 直接走 handler 不会触发 binding (binding 在 gin 层);这里测的是
	// switch 默认分支的"未删除"语义:target=foo 时 Cleanup 应不删任何行
	// (deleted=0) 且不报 error。
	db := setupTestDB(t)
	db.Create(&models.UsageHourlyBucket{
		Date: "2026-05-01", Hour: 0, ChannelID: 1, ModelName: "m", AgentID: "a",
	})
	h := &Handler{}
	c := newTestContext(t, db)
	resp, err := h.Cleanup(c, cleanupRequest("unknown_target", 7))
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if resp.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", resp.Deleted)
	}
}
