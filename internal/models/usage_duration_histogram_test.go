package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newMigratedDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestUsageDurationHistogram_MigrateAndUniqueDim(t *testing.T) {
	db := newMigratedDB(t)
	row := UsageDurationHistogram{Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1", H4: 3, MaxDurationMs: 4200}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	dup := UsageDurationHistogram{Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1"}
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("duplicate dimension must violate unique index")
	}
}

func TestUsageDurationHistogram_DistinctDimsCoexist(t *testing.T) {
	db := newMigratedDB(t)
	a := UsageDurationHistogram{Date: "2026-07-08", Hour: 8, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1"}
	b := UsageDurationHistogram{Date: "2026-07-08", Hour: 9, ChannelID: 5, ModelName: "gpt-4o", AgentID: "a1"} // 只差 hour
	c := UsageDurationHistogram{Date: "2026-07-08", Hour: 8, ChannelID: 0, PrivateChannelID: 7, ModelName: "gpt-4o", AgentID: "a1"} // BYOK 行
	for _, r := range []*UsageDurationHistogram{&a, &b, &c} {
		if err := db.Create(r).Error; err != nil {
			t.Fatalf("create %+v: %v", r, err)
		}
	}
}

func TestUsageLogsCompositeIndexesExist(t *testing.T) {
	db := newMigratedDB(t)
	for _, idx := range []string{"idx_usage_logs_window_stats", "idx_usage_logs_user_window_stats"} {
		if !db.Migrator().HasIndex(&UsageLog{}, idx) {
			t.Fatalf("index %s missing", idx)
		}
	}
}
