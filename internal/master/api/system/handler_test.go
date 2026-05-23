package system

import (
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models.AutoMigrate(db)
	return db
}

func newTestContext(db *gorm.DB) *app.Context {
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	testApp := app.NewApplication()
	testApp.SetDB(db)
	return &app.Context{
		Context: ginCtx,
		App:     testApp,
	}
}

func TestStats_ReturnsTableCounts(t *testing.T) {
	db := setupTestDB(t)

	// Insert test data
	db.Create(&models.User{Username: "u1", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.User{Username: "u2", Password: "p", Role: 1, Status: 1, Quota: 100})
	db.Create(&models.Token{UserID: 1, Key: "sk-1", Name: "t1", Status: 1, ExpiredAt: -1})

	h := &Handler{ConnectedCount: func() int { return 3 }}
	c := newTestContext(db)

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
	c := newTestContext(db)

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
	c := newTestContext(db)

	resp, err := h.Cleanup(c, CleanupRequest{
		Target:     "traces",
		RetainDays: 7,
	})
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
	c := newTestContext(db)
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
	c := newTestContext(db)
	resp, err := h.Cleanup(c, CleanupRequest{
		Target: "hourly_buckets", RetainDays: 7,
	})
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
	c := newTestContext(db)
	resp, err := h.Cleanup(c, CleanupRequest{
		Target: "unknown_target", RetainDays: 7,
	})
	if err != nil {
		t.Fatalf("Cleanup returned error: %v", err)
	}
	if resp.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", resp.Deleted)
	}
}
