package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSeedDefaultUserGroup_CreatesAndBackfills(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	// Old user: force group_id=0 via raw SQL to simulate pre-migration data
	u := User{Username: "old", Password: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE users SET group_id = 0 WHERE id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}

	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatal(err)
	}

	var g UserGroup
	if err := db.First(&g, 1).Error; err != nil {
		t.Fatalf("default group missing: %v", err)
	}
	if g.Name != "default" {
		t.Fatalf("default name = %q", g.Name)
	}

	var got User
	db.First(&got, u.ID)
	if got.GroupID != 1 {
		t.Fatalf("backfill failed, GroupID = %d", got.GroupID)
	}
}

func TestSeedDefaultUserGroup_Idempotent(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	AutoMigrate(db)
	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatal(err)
	}
	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("second call: %v", err)
	}

	var n int64
	db.Model(&UserGroup{}).Where("id = 1").Count(&n)
	if n != 1 {
		t.Fatalf("default group count = %d (want 1)", n)
	}
}

func TestSeedBYOKSettings_FirstStartInserts(t *testing.T) {
	db := setupTestDB(t)

	if err := SeedBYOKSettings(db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Model(&Setting{}).Where("key LIKE ?", "byok_%").Count(&count)
	if count != 5 {
		t.Fatalf("expected 5 byok_* settings, got %d", count)
	}
}

func TestSeedBYOKSettings_DoesNotOverrideExisting(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&Setting{Key: "byok_enabled", Value: "false"}) // admin 改过

	if err := SeedBYOKSettings(db); err != nil {
		t.Fatal(err)
	}

	var s Setting
	db.Where("key = ?", "byok_enabled").First(&s)
	if s.Value != "false" {
		t.Fatalf("byok_enabled overwritten: %q", s.Value)
	}
}
