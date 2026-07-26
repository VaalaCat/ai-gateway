package models

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func openSplitTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func TestMigrateCoreDBOwnsOnlyCoreTables(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	for _, table := range []string{
		"users", "tokens", "channels", "model_configs", "agents", "enrollment_tokens",
		"settings", "agent_routes", "request_limiters", "limiter_bindings", "token_templates",
		"user_groups", "o_auth_providers", "o_auth_identities", "model_routings",
		"private_channels", "private_channel_shares", "admin_scripts", "invite_codes",
		"invite_redemptions", "master_signing_keys", "token_daily_billings",
		"channel_daily_billings", "billing_logs", "billing_hourly_buckets",
		"billing_projection_receipts", "database_layouts",
	} {
		require.Truef(t, db.Migrator().HasTable(table), "core table %s missing", table)
	}
	for _, table := range []string{
		"usage_logs", "usage_log_traces", "request_logs", "request_traces",
		"usage_hourly_buckets", "usage_duration_histograms", "usage_ttft_histograms",
		"usage_tps_histograms", "usage_user_ttft_histograms", "usage_user_tps_histograms",
	} {
		require.Falsef(t, db.Migrator().HasTable(table), "log table %s leaked into core DB", table)
	}
	assertNoForeignKeys(t, db)
}

func TestMigrateLogDBOwnsOnlyLogTables(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateLogDB(db))

	for _, table := range []string{
		"request_logs", "request_traces", "usage_hourly_buckets", "usage_duration_histograms",
		"usage_ttft_histograms", "usage_tps_histograms", "usage_user_ttft_histograms",
		"usage_user_tps_histograms", "database_layouts",
	} {
		require.Truef(t, db.Migrator().HasTable(table), "log table %s missing", table)
	}
	for _, table := range []string{
		"users", "tokens", "channels", "settings", "billing_logs", "billing_hourly_buckets",
		"token_daily_billings", "channel_daily_billings", "usage_logs", "usage_log_traces",
	} {
		require.Falsef(t, db.Migrator().HasTable(table), "core or legacy table %s leaked into log DB", table)
	}
	assertNoForeignKeys(t, db)
}

func TestMigrationStateTablesCoreOwnership(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	require.True(t, db.Migrator().HasTable(&HistoryMigration{}))
	require.True(t, db.Migrator().HasTable(&HistoryCursor{}))
}

func TestMigrationStateTablesLogOwnership(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateLogDB(db))

	require.False(t, db.Migrator().HasTable(&HistoryMigration{}))
	require.True(t, db.Migrator().HasTable(&HistoryCursor{}))
}

func TestDatabaseLayoutRoleVersionIdempotenceAndConflict(t *testing.T) {
	tests := []struct {
		name     string
		migrate  func(*gorm.DB) error
		conflict func(*gorm.DB) error
		wantRole string
	}{
		{name: "core", migrate: MigrateCoreDB, conflict: MigrateLogDB, wantRole: DatabaseRoleCore},
		{name: "log", migrate: MigrateLogDB, conflict: MigrateCoreDB, wantRole: DatabaseRoleLog},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openSplitTestDB(t)
			require.NoError(t, tt.migrate(db))
			require.NoError(t, tt.migrate(db), "same role/version migration must be idempotent")

			var layouts []DatabaseLayout
			require.NoError(t, db.Find(&layouts).Error)
			require.Equal(t, []DatabaseLayout{{ID: DatabaseLayoutID, Role: tt.wantRole, Version: DatabaseLayoutVersion}}, layouts)

			err := tt.conflict(db)
			require.Error(t, err)
			require.Contains(t, err.Error(), "database layout role")
		})
	}
}

func TestDatabaseLayoutRejectsVersionMismatchAndDuplicateRole(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))
	require.Error(t, db.Create(&DatabaseLayout{Role: DatabaseRoleCore, Version: DatabaseLayoutVersion}).Error)
	require.NoError(t, db.Model(&DatabaseLayout{}).Where("role = ?", DatabaseRoleCore).Update("version", DatabaseLayoutVersion+1).Error)
	require.ErrorContains(t, MigrateCoreDB(db), "database layout version")
}

func TestSplitMigrateRejectsUnmarkedNonEmptyDatabasesWithoutChanges(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *gorm.DB)
		migrate func(*gorm.DB) error
		count   func(*testing.T, *gorm.DB) int64
	}{
		{
			name: "core schema as log",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.AutoMigrate(&User{}))
				require.NoError(t, db.Create(&User{Username: "preserved-core-user"}).Error)
			},
			migrate: MigrateLogDB,
			count: func(t *testing.T, db *gorm.DB) int64 {
				var count int64
				require.NoError(t, db.Model(&User{}).Where("username = ?", "preserved-core-user").Count(&count).Error)
				return count
			},
		},
		{
			name: "log-like schema as core",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.AutoMigrate(&RequestLog{}))
				require.NoError(t, db.Create(&RequestLog{RequestID: "preserved-request"}).Error)
			},
			migrate: MigrateCoreDB,
			count: func(t *testing.T, db *gorm.DB) int64 {
				var count int64
				require.NoError(t, db.Model(&RequestLog{}).Where("request_id = ?", "preserved-request").Count(&count).Error)
				return count
			},
		},
		{
			name: "legacy mixed schema as core",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, AutoMigrate(db))
				require.NoError(t, db.Create(&UsageLog{RequestID: "preserved-legacy-core"}).Error)
			},
			migrate: MigrateCoreDB,
			count: func(t *testing.T, db *gorm.DB) int64 {
				var count int64
				require.NoError(t, db.Model(&UsageLog{}).Where("request_id = ?", "preserved-legacy-core").Count(&count).Error)
				return count
			},
		},
		{
			name: "legacy mixed schema as log",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, AutoMigrate(db))
				require.NoError(t, db.Create(&UsageLog{RequestID: "preserved-legacy-log"}).Error)
			},
			migrate: MigrateLogDB,
			count: func(t *testing.T, db *gorm.DB) int64 {
				var count int64
				require.NoError(t, db.Model(&UsageLog{}).Where("request_id = ?", "preserved-legacy-log").Count(&count).Error)
				return count
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openSplitTestDB(t)
			tt.prepare(t, db)
			before := userTableNames(t, db)

			err := tt.migrate(db)
			require.ErrorContains(t, err, "unmarked non-empty database")
			require.Equal(t, before, userTableNames(t, db))
			require.False(t, db.Migrator().HasTable(&DatabaseLayout{}))

			require.EqualValues(t, 1, tt.count(t, db))
		})
	}
}

func TestSplitMigrateRejectsEmptyOrDamagedLayoutMarker(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, *gorm.DB)
	}{
		{
			name: "empty marker table",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.AutoMigrate(&DatabaseLayout{}))
			},
		},
		{
			name: "weak schema with valid-looking row",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Exec(`CREATE TABLE database_layouts (id integer primary key, role text, version integer)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO database_layouts (id, role, version) VALUES (1, 'core', ?)`, DatabaseLayoutVersion).Error)
			},
		},
		{
			name: "multiple rows",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Exec(`CREATE TABLE database_layouts (id integer, role text, version integer)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO database_layouts VALUES (1, 'core', ?), (2, 'log', ?)`, DatabaseLayoutVersion, DatabaseLayoutVersion).Error)
			},
		},
		{
			name: "wrong fixed id",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Exec(`CREATE TABLE database_layouts (id integer primary key, role text not null, version integer not null)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO database_layouts VALUES (7, 'core', ?)`, DatabaseLayoutVersion).Error)
			},
		},
		{
			name: "unknown role",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Exec(`CREATE TABLE database_layouts (id integer primary key, role text not null, version integer not null)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO database_layouts VALUES (1, 'archive', ?)`, DatabaseLayoutVersion).Error)
			},
		},
		{
			name: "empty role",
			prepare: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Exec(`CREATE TABLE database_layouts (id integer primary key, role text not null, version integer not null)`).Error)
				require.NoError(t, db.Exec(`INSERT INTO database_layouts VALUES (1, '', ?)`, DatabaseLayoutVersion).Error)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openSplitTestDB(t)
			tt.prepare(t, db)
			before := userTableNames(t, db)
			err := MigrateCoreDB(db)
			require.ErrorContains(t, err, "invalid database layout")
			require.Equal(t, before, userTableNames(t, db))
			require.False(t, db.Migrator().HasTable(&User{}))
		})
	}
}

func TestDatabaseLayoutDatabaseConstraints(t *testing.T) {
	for name, statements := range map[string][]string{
		"null role":    {`INSERT INTO database_layouts (id, role, version) VALUES (1, NULL, 1)`},
		"empty role":   {`INSERT INTO database_layouts (id, role, version) VALUES (1, '', 1)`},
		"unknown role": {`INSERT INTO database_layouts (id, role, version) VALUES (1, 'archive', 1)`},
		"null version": {`INSERT INTO database_layouts (id, role, version) VALUES (1, 'log', NULL)`},
		"wrong id":     {`INSERT INTO database_layouts (id, role, version) VALUES (2, 'log', 1)`},
		"second marker": {
			`INSERT INTO database_layouts (id, role, version) VALUES (1, 'core', 1)`,
			`INSERT INTO database_layouts (id, role, version) VALUES (2, 'log', 1)`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			db := openSplitTestDB(t)
			require.NoError(t, db.AutoMigrate(&DatabaseLayout{}))
			for _, statement := range statements[:len(statements)-1] {
				require.NoError(t, db.Exec(statement).Error)
			}
			require.Error(t, db.Exec(statements[len(statements)-1]).Error)
		})
	}

	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))
	var count int64
	require.NoError(t, db.Model(&DatabaseLayout{}).Count(&count).Error)
	require.EqualValues(t, 1, count)
	var marker DatabaseLayout
	require.NoError(t, db.First(&marker).Error)
	require.Equal(t, DatabaseLayout{ID: 1, Role: DatabaseRoleCore, Version: DatabaseLayoutVersion}, marker)
}

func TestSplitMigrateRejectsNilAndDatabaseErrors(t *testing.T) {
	require.ErrorContains(t, MigrateCoreDB(nil), "core")
	require.ErrorContains(t, MigrateLogDB(nil), "log")

	for _, migrate := range []func(*gorm.DB) error{MigrateCoreDB, MigrateLogDB} {
		db := openSplitTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Close())
		require.Error(t, migrate(db))
	}
}

type migrationRollbackProbe struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"index:idx_migration_rollback_probe_name"`
}

type migrationUnsupportedModel struct {
	ID     uint `gorm:"primaryKey"`
	Broken chan string
}

func TestMigrateSplitDatabaseRollsBackFinishFailureOnDisk(t *testing.T) {
	sentinel := errors.New("finish sentinel")
	t.Run("fresh database rolls back marker model index and row", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "finish-fresh.db")
		db, sqlDB := openFileSplitTestDB(t, path)
		err := migrateSplitDatabase(db, DatabaseRoleCore, []any{&migrationRollbackProbe{}}, func(tx *gorm.DB) error {
			require.True(t, tx.Migrator().HasTable(&DatabaseLayout{}))
			require.True(t, tx.Migrator().HasTable(&migrationRollbackProbe{}))
			require.True(t, tx.Migrator().HasIndex(&migrationRollbackProbe{}, "idx_migration_rollback_probe_name"))
			require.NoError(t, tx.Create(&migrationRollbackProbe{Name: "must-roll-back"}).Error)
			var count int64
			require.NoError(t, tx.Model(&migrationRollbackProbe{}).Count(&count).Error)
			require.EqualValues(t, 1, count)
			return sentinel
		})
		require.ErrorIs(t, err, sentinel)
		require.NoError(t, sqlDB.Close())

		reopened, reopenedSQL := openFileSplitTestDB(t, path)
		defer reopenedSQL.Close()
		require.Empty(t, userTableNames(t, reopened))
		require.False(t, reopened.Migrator().HasTable(&DatabaseLayout{}))
		require.False(t, reopened.Migrator().HasTable(&migrationRollbackProbe{}))
		require.False(t, reopened.Migrator().HasIndex(&migrationRollbackProbe{}, "idx_migration_rollback_probe_name"))
	})

	t.Run("existing database rolls back cleanup data change", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "finish-existing.db")
		db, sqlDB := openFileSplitTestDB(t, path)
		require.NoError(t, MigrateCoreDB(db))
		setting := Setting{Key: legacyRelayFallbackSettingKey(), Value: "preserve-me"}
		require.NoError(t, db.Create(&setting).Error)

		err := migrateSplitDatabase(db, DatabaseRoleCore, []any{&migrationRollbackProbe{}}, func(tx *gorm.DB) error {
			require.NoError(t, deleteLegacyRelayFallbackSetting(tx))
			var count int64
			require.NoError(t, tx.Model(&Setting{}).Where("key = ?", setting.Key).Count(&count).Error)
			require.Zero(t, count)
			return sentinel
		})
		require.ErrorIs(t, err, sentinel)
		require.NoError(t, sqlDB.Close())

		reopened, reopenedSQL := openFileSplitTestDB(t, path)
		defer reopenedSQL.Close()
		var count int64
		require.NoError(t, reopened.Model(&Setting{}).Where("key = ?", setting.Key).Count(&count).Error)
		require.EqualValues(t, 1, count)
		require.False(t, reopened.Migrator().HasTable(&migrationRollbackProbe{}))
		require.False(t, reopened.Migrator().HasIndex(&migrationRollbackProbe{}, "idx_migration_rollback_probe_name"))
		var marker DatabaseLayout
		require.NoError(t, reopened.First(&marker).Error)
		require.Equal(t, DatabaseLayout{ID: DatabaseLayoutID, Role: DatabaseRoleCore, Version: DatabaseLayoutVersion}, marker)
	})
}

func TestMigrateSplitDatabaseRollsBackMidAutoMigrateFailureOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "automigrate-failure.db")
	db, sqlDB := openFileSplitTestDB(t, path)
	finishCalled := false
	err := migrateSplitDatabase(db, DatabaseRoleCore, []any{
		&migrationRollbackProbe{},
		&migrationUnsupportedModel{},
	}, func(*gorm.DB) error {
		finishCalled = true
		return nil
	})
	require.ErrorContains(t, err, "migrate core schema")
	require.False(t, finishCalled)
	require.NoError(t, sqlDB.Close())

	reopened, reopenedSQL := openFileSplitTestDB(t, path)
	defer reopenedSQL.Close()
	require.Empty(t, userTableNames(t, reopened))
	require.False(t, reopened.Migrator().HasTable(&DatabaseLayout{}))
	require.False(t, reopened.Migrator().HasTable(&migrationRollbackProbe{}))
	require.False(t, reopened.Migrator().HasIndex(&migrationRollbackProbe{}, "idx_migration_rollback_probe_name"))
}

func TestBillingLogSchemaAndRawTotal(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	for _, column := range []string{
		"id", "request_id", "user_id", "token_id", "token_name", "channel_id",
		"private_channel_id", "owner_type", "channel_name", "channel_type", "model_name",
		"prompt_tokens", "completion_tokens", "cache_read_tokens", "cache_write_tokens",
		"input_cost", "output_cost", "cache_read_cost", "cache_write_cost", "total_cost",
		"raw_input_cost", "raw_output_cost", "raw_cache_read_cost", "raw_cache_write_cost",
		"billing_factor", "price_ratio", "free", "status", "created_at",
	} {
		require.Truef(t, db.Migrator().HasColumn(&BillingLog{}, column), "billing_logs.%s missing", column)
	}
	for _, column := range []string{
		"duration", "first_response_ms", "inbound_protocol", "agent_id", "error_message",
		"rate_limit_decision", "has_trace",
	} {
		require.Falsef(t, db.Migrator().HasColumn(&BillingLog{}, column), "log-only column %s leaked", column)
	}

	first := BillingLog{RequestID: "billing-duplicate", RawInputCost: int64Pointer(11), RawCacheReadCost: int64Pointer(3), TotalCost: 2}
	require.EqualValues(t, 14, first.RawTotal())
	require.NoError(t, db.Create(&first).Error)
	require.Error(t, db.Create(&BillingLog{RequestID: first.RequestID}).Error)

	legacy := BillingLog{RequestID: "billing-legacy", TotalCost: 37}
	require.EqualValues(t, 37, legacy.RawTotal())
	require.NoError(t, db.Create(&legacy).Error)
	var rawInput sql.NullInt64
	var factor sql.NullFloat64
	require.NoError(t, db.Raw(`SELECT raw_input_cost, billing_factor FROM billing_logs WHERE request_id = ?`, legacy.RequestID).Row().Scan(&rawInput, &factor))
	require.False(t, rawInput.Valid)
	require.False(t, factor.Valid)
}

func TestBillingHourlyBucketDimensionsAndIndexes(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	base := BillingHourlyBucket{Date: "2026-07-23", Hour: 8, UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "gpt-5"}
	require.NoError(t, db.Create(&base).Error)
	dup := base
	dup.ID = 0
	require.Error(t, db.Create(&dup).Error)
	for _, row := range []BillingHourlyBucket{
		{Date: base.Date, Hour: base.Hour, UserID: 2, TokenID: base.TokenID, ChannelID: base.ChannelID, OwnerType: base.OwnerType, ModelName: base.ModelName},
		{Date: base.Date, Hour: base.Hour, UserID: base.UserID, TokenID: 9, ChannelID: base.ChannelID, OwnerType: base.OwnerType, ModelName: base.ModelName},
		{Date: base.Date, Hour: base.Hour, UserID: base.UserID, TokenID: base.TokenID, PrivateChannelID: 7, OwnerType: "private", ModelName: base.ModelName},
		{Date: base.Date, Hour: base.Hour, UserID: base.UserID, TokenID: base.TokenID, ChannelID: base.ChannelID, OwnerType: base.OwnerType, ModelName: "gpt-5-mini"},
	} {
		require.NoError(t, db.Create(&row).Error)
	}

	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_bucket", []indexColumn{
		{name: "date"}, {name: "hour"}, {name: "user_id"}, {name: "token_id"},
		{name: "channel_id"}, {name: "private_channel_id"}, {name: "owner_type"}, {name: "model_name"},
	})
	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_model_user", []indexColumn{
		{name: "model_name"}, {name: "date"}, {name: "hour"}, {name: "user_id"},
	})
	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_user_token", []indexColumn{
		{name: "user_id"}, {name: "token_id"}, {name: "date"}, {name: "hour"}, {name: "model_name"},
	})
	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_user_window", []indexColumn{
		{name: "user_id"}, {name: "date"}, {name: "hour"}, {name: "model_name"},
	})
	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_window_user", []indexColumn{
		{name: "date"}, {name: "hour"}, {name: "user_id"},
	})
}

func TestBillingProjectionReceiptUsesRequestIDAsPrimaryKey(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	receipt := BillingProjectionReceipt{RequestID: "request-1", BillingLogID: 1, State: BillingProjectionPending}
	require.NoError(t, db.Create(&receipt).Error)
	require.Error(t, db.Create(&BillingProjectionReceipt{RequestID: receipt.RequestID}).Error)
	require.True(t, db.Migrator().HasColumn(&BillingProjectionReceipt{}, "billing_log_id"))
	require.True(t, db.Migrator().HasColumn(&BillingProjectionReceipt{}, "state"))
	require.True(t, db.Migrator().HasColumn(&BillingProjectionReceipt{}, "projected_at_unix"))
	assertIndexColumns(t, db, "billing_projection_receipts", "idx_billing_projection_pending", []indexColumn{
		{name: "state"}, {name: "billing_log_id"},
	})
	require.Error(t, db.Create(&BillingProjectionReceipt{
		RequestID: "invalid-state", BillingLogID: 2, State: BillingProjectionState("invalid"),
	}).Error)
}

func TestMigrateCoreDBCreatesFreshProjectionBaselineAtZero(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	var baseline BillingProjectionBaseline
	require.NoError(t, db.First(&baseline, BillingProjectionBaselineID).Error)
	require.Zero(t, baseline.BillingLogHighWatermark)
}

func TestMigrateCoreDBCapturesExistingBillingLogHighWatermarkWithoutReceipts(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))
	require.NoError(t, db.Migrator().DropTable(&BillingProjectionReceipt{}, &BillingProjectionBaseline{}))
	require.NoError(t, db.Create(&BillingLog{RequestID: "legacy-1"}).Error)
	require.NoError(t, db.Create(&BillingLog{RequestID: "legacy-2"}).Error)

	require.NoError(t, MigrateCoreDB(db))
	var baseline BillingProjectionBaseline
	require.NoError(t, db.First(&baseline, BillingProjectionBaselineID).Error)
	require.Equal(t, uint(2), baseline.BillingLogHighWatermark)
	var receipts int64
	require.NoError(t, db.Model(&BillingProjectionReceipt{}).Count(&receipts).Error)
	require.Zero(t, receipts)
}

func TestMigrateCoreDBBackfillsLegacyProjectionReceiptsAsApplied(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))
	require.NoError(t, db.Migrator().DropTable(&BillingProjectionReceipt{}, &BillingProjectionBaseline{}))
	fact := BillingLog{RequestID: "legacy-receipt"}
	require.NoError(t, db.Create(&fact).Error)
	require.NoError(t, db.Exec(`CREATE TABLE billing_projection_receipts (
		request_id text PRIMARY KEY,
		projected_at_unix integer NOT NULL
	)`).Error)
	require.NoError(t, db.Exec(`INSERT INTO billing_projection_receipts(request_id, projected_at_unix) VALUES (?, ?)`, fact.RequestID, 123).Error)

	require.NoError(t, MigrateCoreDB(db))
	var receipt BillingProjectionReceipt
	require.NoError(t, db.First(&receipt, "request_id = ?", fact.RequestID).Error)
	require.Equal(t, fact.ID, receipt.BillingLogID)
	require.Equal(t, BillingProjectionApplied, receipt.State)
}

func TestMigrateCoreDBRepairsStaleBillingHourlyUserWindowIndex(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))
	require.NoError(t, db.Exec(`DROP INDEX idx_bhb_user_window`).Error)
	require.NoError(t, db.Exec(`CREATE INDEX idx_bhb_user_window ON billing_hourly_buckets(user_id, token_id, date, hour)`).Error)

	require.NoError(t, MigrateCoreDB(db))
	assertIndexColumns(t, db, "billing_hourly_buckets", "idx_bhb_user_window", []indexColumn{
		{name: "user_id"}, {name: "date"}, {name: "hour"}, {name: "model_name"},
	})
}

func TestRequestLogAndTraceSchemaIndexes(t *testing.T) {
	db := openSplitTestDB(t)
	require.Equal(t, "request_logs", (RequestLog{}).TableName())
	require.Equal(t, "request_traces", (RequestTrace{}).TableName())
	assertSameFieldAndGORMTags(t, reflect.TypeOf(UsageLog{}), reflect.TypeOf(RequestLog{}))
	assertSameFieldAndGORMTags(t, reflect.TypeOf(UsageLogTrace{}), reflect.TypeOf(RequestTrace{}))
	require.NoError(t, MigrateLogDB(db))
	require.False(t, db.Migrator().HasTable("usage_logs"))

	log := RequestLog{RequestID: "request-duplicate"}
	require.NoError(t, db.Create(&log).Error)
	require.Error(t, db.Create(&RequestLog{RequestID: log.RequestID}).Error)
	trace := RequestTrace{RequestID: log.RequestID, AttemptIndex: 1}
	require.NoError(t, db.Create(&trace).Error)
	require.Error(t, db.Create(&RequestTrace{RequestID: log.RequestID, AttemptIndex: 1}).Error)
	require.NoError(t, db.Create(&RequestTrace{RequestID: log.RequestID, AttemptIndex: 2}).Error)

	for name, columns := range map[string][]indexColumn{
		"idx_request_logs_created_id":              {{name: "created_at", desc: true}, {name: "id", desc: true}},
		"idx_request_logs_user_created_model":      {{name: "user_id"}, {name: "created_at", desc: true}, {name: "model_name"}},
		"idx_request_logs_model_created_user":      {{name: "model_name"}, {name: "created_at", desc: true}, {name: "user_id"}},
		"idx_request_logs_status_created_duration": {{name: "status"}, {name: "created_at"}, {name: "duration"}},
		"idx_request_logs_agent_status_created":    {{name: "agent_id"}, {name: "status"}, {name: "created_at", desc: true}},
		"idx_request_logs_pchan_created_model":     {{name: "private_channel_id"}, {name: "created_at"}, {name: "model_name"}},
	} {
		assertIndexColumns(t, db, "request_logs", name, columns)
	}
	assertIndexColumns(t, db, "request_traces", "idx_trace_req_attempt", []indexColumn{{name: "request_id"}, {name: "attempt_index"}})
}

func TestUsageUserHistogramsSchemaAndDimensions(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, MigrateLogDB(db))

	tests := []struct {
		name      string
		table     string
		model     any
		indexName string
		maxColumn string
		create    func(date string, hour int, userID uint, model string) any
	}{
		{name: "ttft", table: "usage_user_ttft_histograms", model: &UsageUserTTFTHistogram{}, indexName: "idx_uutth_bucket", maxColumn: "max_first_response_ms", create: func(date string, hour int, userID uint, model string) any {
			return &UsageUserTTFTHistogram{Date: date, Hour: hour, UserID: userID, ModelName: model}
		}},
		{name: "tps", table: "usage_user_tps_histograms", model: &UsageUserTPSHistogram{}, indexName: "idx_uutps_bucket", maxColumn: "max_tps", create: func(date string, hour int, userID uint, model string) any {
			return &UsageUserTPSHistogram{Date: date, Hour: hour, UserID: userID, ModelName: model}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for slot := 0; slot < 17; slot++ {
				require.Truef(t, db.Migrator().HasColumn(tt.model, fmt.Sprintf("h%d", slot)), "slot H%d missing", slot)
			}
			require.True(t, db.Migrator().HasColumn(tt.model, tt.maxColumn))
			assertIndexColumns(t, db, tt.table, tt.indexName, []indexColumn{{name: "date"}, {name: "hour"}, {name: "user_id"}, {name: "model_name"}})
			base := tt.create("2026-07-23", 8, 1, "gpt-5")
			require.NoError(t, db.Create(base).Error)
			require.Error(t, db.Create(tt.create("2026-07-23", 8, 1, "gpt-5")).Error)
			for _, row := range []any{
				tt.create("2026-07-23", 9, 1, "gpt-5"),
				tt.create("2026-07-23", 8, 2, "gpt-5"),
				tt.create("2026-07-23", 8, 1, "gpt-5-mini"),
			} {
				require.NoError(t, db.Create(row).Error)
			}
		})
	}
}

func TestLegacyAutoMigrateKeepsLegacySchemaWithoutLayoutMarker(t *testing.T) {
	db := openSplitTestDB(t)
	require.NoError(t, AutoMigrate(db))
	for _, table := range []string{"usage_logs", "usage_log_traces", "usage_hourly_buckets", "usage_ttft_histograms", "usage_tps_histograms", "usage_user_ttft_histograms", "usage_user_tps_histograms"} {
		require.True(t, db.Migrator().HasTable(table))
	}
	for _, table := range []string{"database_layouts", "request_logs", "request_traces", "billing_logs", "billing_hourly_buckets"} {
		require.False(t, db.Migrator().HasTable(table))
	}
}

func assertNoForeignKeys(t *testing.T, db *gorm.DB) {
	t.Helper()
	var tables []string
	require.NoError(t, db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables).Error)
	for _, table := range tables {
		var count int
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM pragma_foreign_key_list(?)", table).Scan(&count).Error)
		require.Zerof(t, count, "table %s has foreign keys", table)
	}
}

func assertIndexColumns(t *testing.T, db *gorm.DB, table, name string, want []indexColumn) {
	t.Helper()
	sqlDB, err := db.DB()
	require.NoError(t, err)
	rows, err := sqlDB.Query("PRAGMA index_list('" + table + "')")
	require.NoError(t, err)
	defer rows.Close()
	found := false
	for rows.Next() {
		var seq, unique, partial int
		var gotName, origin string
		require.NoError(t, rows.Scan(&seq, &gotName, &unique, &origin, &partial))
		found = found || gotName == name
	}
	require.NoError(t, rows.Err())
	require.Truef(t, found, "index %s missing from %s", name, table)
	require.True(t, reflect.DeepEqual(usageLogIndexColumns(t, sqlDB, name), want), "index %s columns mismatch", name)
}

func int64Pointer(value int64) *int64 { return &value }

func assertSameFieldAndGORMTags(t *testing.T, want, got reflect.Type) {
	t.Helper()
	require.Equal(t, want.NumField(), got.NumField())
	for i := 0; i < want.NumField(); i++ {
		require.Equal(t, want.Field(i).Name, got.Field(i).Name)
		require.Equal(t, want.Field(i).Tag.Get("gorm"), got.Field(i).Tag.Get("gorm"))
	}
}

func userTableNames(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var tables []string
	require.NoError(t, db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&tables).Error)
	sort.Strings(tables)
	return tables
}

func openFileSplitTestDB(t *testing.T, path string) (*gorm.DB, *sql.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	return db, sqlDB
}
