package models

import (
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestAutoMigrate(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	tables := []string{"users", "tokens", "channels", "model_configs", "agents", "usage_logs", "o_auth_providers", "o_auth_identities"}
	for _, table := range tables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestAutoMigrate_AddsCreatedAtIndexesForUsageTables(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	assertHasCreatedAtIndex := func(table string) {
		rows, err := sqlDB.Query("PRAGMA index_list(" + "'" + table + "'" + ")")
		if err != nil {
			t.Fatalf("query index_list for %s: %v", table, err)
		}
		defer rows.Close()

		found := false
		for rows.Next() {
			var seq int
			var name string
			var unique int
			var origin string
			var partial int
			if err := rows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				t.Fatalf("scan index_list for %s: %v", table, err)
			}
			if strings.Contains(name, "created_at") {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected %s to have a created_at index", table)
		}
	}

	assertHasCreatedAtIndex("usage_logs")
	assertHasCreatedAtIndex("usage_log_traces")
}

func TestAutoMigrate_BillingTables(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, table := range []string{"token_daily_billings", "channel_daily_billings"} {
		table := table
		t.Run(table, func(t *testing.T) {
			if !db.Migrator().HasTable(table) {
				t.Fatalf("expected table %s to be created", table)
			}
		})
	}
}

func TestAutoMigrate_UsageLogRequestIDUnique(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	log1 := UsageLog{RequestID: "req-duplicate-check", UserID: 1, TokenID: 1, ChannelID: 1}
	if err := db.Create(&log1).Error; err != nil {
		t.Fatalf("create first usage log: %v", err)
	}

	log2 := UsageLog{RequestID: "req-duplicate-check", UserID: 1, TokenID: 1, ChannelID: 1}
	if err := db.Create(&log2).Error; err == nil {
		t.Fatal("expected duplicate request_id insert to fail")
	}
}

func TestAutoMigrate_UsageLogChannelSnapshotColumns(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, column := range []string{"channel_name", "channel_type"} {
		column := column
		t.Run(column, func(t *testing.T) {
			if !db.Migrator().HasColumn(&UsageLog{}, column) {
				t.Fatalf("expected usage_logs to have column %s", column)
			}
		})
	}
}

func TestUserCRUD(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	user := User{Username: "admin", Password: "hashed", Role: 2, Status: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	var found User
	db.First(&found, user.ID)
	if found.Username != "admin" {
		t.Errorf("got %s, want admin", found.Username)
	}
}

func TestTokenCRUD(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	user := User{Username: "testuser", Password: "hashed", Role: 1, Status: 1}
	db.Create(&user)

	token := Token{UserID: user.ID, Key: "sk-test123", Name: "test", Status: 1, ExpiredAt: -1}
	if err := db.Create(&token).Error; err != nil {
		t.Fatalf("create token: %v", err)
	}

	var found Token
	db.Where("key = ?", "sk-test123").First(&found)
	if found.UserID != user.ID {
		t.Errorf("got user_id %d, want %d", found.UserID, user.ID)
	}
}

func TestTokenTemplate_AllowedChannelIDs_Roundtrip(t *testing.T) {
	db := setupTestDB(t)

	tpl := TokenTemplate{
		Name:              "t1",
		Models:            "[]",
		ExpiryDays:        -1,
		Status:            1,
		AllowedChannelIDs: datatypes.JSONSlice[uint]{3, 7, 9},
	}
	if err := db.Create(&tpl).Error; err != nil {
		t.Fatalf("create: %v", err)
	}

	var got TokenTemplate
	if err := db.First(&got, tpl.ID).Error; err != nil {
		t.Fatalf("get: %v", err)
	}
	want := []uint{3, 7, 9}
	if !reflect.DeepEqual([]uint(got.AllowedChannelIDs), want) {
		t.Fatalf("AllowedChannelIDs = %v, want %v", got.AllowedChannelIDs, want)
	}
}

func TestUsageLog_TraceFieldsMigrate(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if err := db.AutoMigrate(&UsageLog{}, &UsageLogTrace{}); err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}
	log := UsageLog{
		RequestID:          "req-trace-test",
		ErrorStage:         "outbound_encode",
		InboundDecodeMs:    1,
		OutboundEncodeMs:   2,
		UpstreamDispatchMs: 100,
		UpstreamDecodeMs:   5,
		ClientEncodeMs:     3,
	}
	if err := db.Create(&log).Error; err != nil {
		t.Fatalf("Create with trace fields failed: %v", err)
	}
	var got UsageLog
	if err := db.First(&got, "request_id = ?", "req-trace-test").Error; err != nil {
		t.Fatalf("Read back failed: %v", err)
	}
	if got.ErrorStage != "outbound_encode" {
		t.Errorf("ErrorStage = %q, want outbound_encode", got.ErrorStage)
	}
	if got.UpstreamDispatchMs != 100 {
		t.Errorf("UpstreamDispatchMs = %d, want 100", got.UpstreamDispatchMs)
	}
}

func TestPrivateChannelMigration(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&PrivateChannel{}, &PrivateChannelShare{}); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"owner_id", "name", "type", "key_cipher", "key_last4",
		"base_url", "models", "model_mapping", "weight", "priority", "status"} {
		if !db.Migrator().HasColumn(&PrivateChannel{}, col) {
			t.Errorf("column %s missing on private_channels", col)
		}
	}
	for _, col := range []string{"channel_id", "target_type", "target_id"} {
		if !db.Migrator().HasColumn(&PrivateChannelShare{}, col) {
			t.Errorf("column %s missing on private_channel_shares", col)
		}
	}
}

func TestUserGroupBYOKColumns(t *testing.T) {
	db := setupTestDB(t)
	if err := db.AutoMigrate(&UserGroup{}); err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"byok_enabled", "byok_max_channels"} {
		if !db.Migrator().HasColumn(&UserGroup{}, col) {
			t.Errorf("column %s missing on user_groups", col)
		}
	}
}

func TestToken_AllowedChannelIDs_Roundtrip(t *testing.T) {
	db := setupTestDB(t)

	tok := Token{
		Key:               "sk-test",
		Name:              "t1",
		Status:            1,
		ExpiredAt:         -1,
		AllowedChannelIDs: datatypes.JSONSlice[uint]{3, 7},
	}
	if err := db.Create(&tok).Error; err != nil {
		t.Fatalf("create: %v", err)
	}
	var got Token
	if err := db.First(&got, tok.ID).Error; err != nil {
		t.Fatalf("get: %v", err)
	}
	want := []uint{3, 7}
	if !reflect.DeepEqual([]uint(got.AllowedChannelIDs), want) {
		t.Fatalf("AllowedChannelIDs = %v, want %v", got.AllowedChannelIDs, want)
	}
}
