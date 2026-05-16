package models

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPasswordSetBackfill(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Exec(`CREATE TABLE users (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		username TEXT,
		password TEXT,
		role INTEGER,
		status INTEGER,
		group_id INTEGER,
		quota INTEGER,
		used_quota INTEGER,
		created_at INTEGER,
		updated_at INTEGER
	)`).Error; err != nil {
		t.Fatalf("create legacy: %v", err)
	}
	db.Exec(`INSERT INTO users (username, password) VALUES ('legacy_with_pw', 'hash')`)
	db.Exec(`INSERT INTO users (username, password) VALUES ('legacy_blank', '')`)

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	type row struct {
		Username    string
		PasswordSet bool
	}
	var rows []row
	db.Raw(`SELECT username, password_set FROM users ORDER BY id`).Scan(&rows)
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	if !rows[0].PasswordSet {
		t.Errorf("legacy_with_pw should have password_set=true")
	}
	if rows[1].PasswordSet {
		t.Errorf("legacy_blank should have password_set=false")
	}

	// Re-running should be idempotent
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	var rows2 []row
	db.Raw(`SELECT username, password_set FROM users ORDER BY id`).Scan(&rows2)
	if len(rows2) != 2 || rows2[0].PasswordSet != rows[0].PasswordSet || rows2[1].PasswordSet != rows[1].PasswordSet {
		t.Fatalf("re-migrate changed state: %+v vs %+v", rows, rows2)
	}
}
