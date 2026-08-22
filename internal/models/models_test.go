package models

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

type indexColumn struct {
	name string
	desc bool
}

type legacyModelConfigWithoutMetadata struct {
	ID              uint   `gorm:"primaryKey"`
	ModelName       string `gorm:"uniqueIndex;size:128"`
	InputPrice      float64
	OutputPrice     float64
	CacheReadPrice  float64
	CacheWritePrice float64
	Status          int
	CreatedAt       int64
	UpdatedAt       int64
}

type legacyChannelWithoutAutoBanRuntime struct {
	ID         uint `gorm:"primaryKey"`
	Name       string
	Type       int
	Status     int
	AutoBan    int
	LimitState string `gorm:"type:text"`
}

func (legacyChannelWithoutAutoBanRuntime) TableName() string { return "channels" }

type legacyPrivateChannelWithoutAutoBanRuntime struct {
	ID      uint `gorm:"primaryKey"`
	OwnerID uint
	Name    string
	Type    int
	Status  int
	AutoBan int
}

func (legacyPrivateChannelWithoutAutoBanRuntime) TableName() string { return "private_channels" }

func (legacyModelConfigWithoutMetadata) TableName() string { return "model_configs" }

func TestAutoMigrate(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	tables := []string{"users", "tokens", "channels", "model_configs", "agents", "usage_logs", "o_auth_providers", "o_auth_identities", "master_signing_keys"}
	for _, table := range tables {
		if !db.Migrator().HasTable(table) {
			t.Errorf("table %s not created", table)
		}
	}
}

func TestAutoMigrateCoreAPIRoutingTargetModels(t *testing.T) {
	// This catches a core migration that creates generic API tables without the
	// backend ownership boundary or omits the persisted route request example.
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	require.NoError(t, MigrateCoreDB(db))
	require.True(t, db.Migrator().HasTable(&APIBackend{}))
	require.True(t, db.Migrator().HasColumn(&APIService{}, "openapi_document"))
	require.True(t, db.Migrator().HasColumn(&APIRoute{}, "backend_id"))
	require.True(t, db.Migrator().HasColumn(&APIRoute{}, "example_request"))
	require.True(t, db.Migrator().HasColumn(&APIRoute{}, "openapi_paths"))
	require.True(t, db.Migrator().HasColumn(&APIUpstream{}, "backend_id"))
	require.False(t, db.Migrator().HasColumn(&APIUpstream{}, "api_service_id"))
}

func TestAutoMigrateAddsAutoBanRuntimeColumns(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, model := range []any{&Channel{}, &PrivateChannel{}} {
		for _, column := range []string{"auto_ban_state", "auto_ban_revision"} {
			if !db.Migrator().HasColumn(model, column) {
				t.Fatalf("%T missing %s", model, column)
			}
		}
	}
}

func TestAutoMigrateBackfillsLegacyAutoBanRuntimeState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	defer sqlDB.Close()

	require.NoError(t, db.AutoMigrate(&legacyChannelWithoutAutoBanRuntime{}, &legacyPrivateChannelWithoutAutoBanRuntime{}))
	require.NoError(t, db.Create(&legacyChannelWithoutAutoBanRuntime{ID: 1, Name: "legacy", Type: 1, Status: 1, LimitState: "{}"}).Error)
	require.NoError(t, db.Create(&legacyPrivateChannelWithoutAutoBanRuntime{ID: 1, OwnerID: 7, Name: "legacy-private", Type: 1, Status: 1}).Error)

	require.NoError(t, AutoMigrate(db))
	for _, model := range []any{&Channel{}, &PrivateChannel{}} {
		for _, column := range []string{"auto_ban_state", "auto_ban_revision"} {
			require.True(t, db.Migrator().HasColumn(model, column))
		}
	}
	for _, table := range []string{"channels", "private_channels"} {
		var nullRuntimeValues int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM "+table+" WHERE auto_ban_state IS NULL OR auto_ban_revision IS NULL").Scan(&nullRuntimeValues).Error)
		require.Zerof(t, nullRuntimeValues, "%s legacy rows retain NULL runtime values", table)
	}

	var channel Channel
	require.NoError(t, db.First(&channel, 1).Error)
	require.Equal(t, ChannelDisableState{}, channel.AutoBanState.Data())
	require.Zero(t, channel.AutoBanRevision)
	var privateChannel PrivateChannel
	require.NoError(t, db.First(&privateChannel, 1).Error)
	require.Equal(t, ChannelDisableState{}, privateChannel.AutoBanState.Data())
	require.Zero(t, privateChannel.AutoBanRevision)

	require.NoError(t, db.Model(&Channel{}).Where("id = ?", channel.ID).Update("auto_ban_revision", gorm.Expr("auto_ban_revision + ?", 1)).Error)
	require.NoError(t, db.First(&channel, channel.ID).Error)
	require.Equal(t, uint64(1), channel.AutoBanRevision)
}

func TestMigrationsPreBackfillNullableAutoBanRuntimeColumns(t *testing.T) {
	tests := []struct {
		name    string
		migrate func(*gorm.DB) error
		prepare func(*gorm.DB) error
	}{
		{
			name:    "legacy",
			migrate: AutoMigrate,
			prepare: func(db *gorm.DB) error { return nil },
		},
		{
			name:    "split core",
			migrate: MigrateCoreDB,
			prepare: func(db *gorm.DB) error {
				if err := db.AutoMigrate(&DatabaseLayout{}); err != nil {
					return err
				}
				return db.Create(&DatabaseLayout{ID: DatabaseLayoutID, Role: DatabaseRoleCore, Version: DatabaseLayoutVersion}).Error
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
			require.NoError(t, err)
			if tt.prepare != nil {
				require.NoError(t, tt.prepare(db))
			}
			require.NoError(t, db.Exec(`CREATE TABLE channels (id integer primary key, name text, type integer, status integer, auto_ban integer, limit_state text, auto_ban_state text NULL, auto_ban_revision integer NULL); INSERT INTO channels VALUES (1, 'legacy', 1, 1, 0, '{}', NULL, NULL)`).Error)
			require.NoError(t, db.Exec(`CREATE TABLE private_channels (id integer primary key, owner_id integer, name text, type integer, status integer, auto_ban integer, auto_ban_state text NULL, auto_ban_revision integer NULL); INSERT INTO private_channels VALUES (1, 7, 'legacy-private', 1, 1, 0, NULL, NULL)`).Error)

			require.NoError(t, tt.migrate(db))
			require.NoError(t, tt.migrate(db))
			for _, table := range []string{"channels", "private_channels"} {
				var nulls int64
				require.NoError(t, db.Raw("SELECT COUNT(*) FROM "+table+" WHERE auto_ban_state IS NULL OR auto_ban_revision IS NULL").Scan(&nulls).Error)
				require.Zero(t, nulls)
			}
			var channel Channel
			require.NoError(t, db.First(&channel, 1).Error)
			require.Equal(t, ChannelDisableState{}, channel.AutoBanState.Data())
			require.Zero(t, channel.AutoBanRevision)
			var privateChannel PrivateChannel
			require.NoError(t, db.First(&privateChannel, 1).Error)
			require.Equal(t, ChannelDisableState{}, privateChannel.AutoBanState.Data())
			require.Zero(t, privateChannel.AutoBanRevision)
			require.NoError(t, db.Model(&Channel{}).Where("id = 1").Update("auto_ban_revision", gorm.Expr("auto_ban_revision + ?", 1)).Error)
			require.NoError(t, db.First(&channel, 1).Error)
			require.Equal(t, uint64(1), channel.AutoBanRevision)
		})
	}
}

func TestMigrateCoreDBDoesNotPreBackfillBeforeLayoutValidation(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`CREATE TABLE channels (id integer primary key, auto_ban_state text NULL, auto_ban_revision integer NULL); INSERT INTO channels VALUES (1, NULL, NULL)`).Error)
	require.Error(t, MigrateCoreDB(db))
	var nulls int64
	require.NoError(t, db.Raw(`SELECT COUNT(*) FROM channels WHERE auto_ban_state IS NULL OR auto_ban_revision IS NULL`).Scan(&nulls).Error)
	require.Equal(t, int64(1), nulls)
}

func TestChannelPublicDisplayNameSchema(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	if !db.Migrator().HasColumn(&Channel{}, "public_display_name") {
		t.Fatal("channels is missing public_display_name")
	}
	if db.Migrator().HasColumn(&PrivateChannel{}, "public_display_name") {
		t.Fatal("private_channels must not have public_display_name")
	}
}

func TestModelMetadataSchema(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, column := range []string{"synced_metadata", "metadata_override"} {
		if !db.Migrator().HasColumn(&ModelConfig{}, column) {
			t.Fatalf("model_configs is missing %s", column)
		}
	}
}

func TestModelMetadataSchemaMigratesExistingRowsToEmptyJSON(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	defer sqlDB.Close()

	if err := db.AutoMigrate(&legacyModelConfigWithoutMetadata{}); err != nil {
		t.Fatalf("create legacy model_configs: %v", err)
	}
	legacy := legacyModelConfigWithoutMetadata{ModelName: "legacy-model", Status: 1}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("create legacy model row: %v", err)
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("migrate legacy model_configs: %v", err)
	}
	var nullMetadataCount int64
	if err := db.Raw(`SELECT COUNT(*) FROM model_configs WHERE synced_metadata IS NULL OR metadata_override IS NULL`).Scan(&nullMetadataCount).Error; err != nil {
		t.Fatalf("count NULL metadata columns: %v", err)
	}
	if nullMetadataCount != 0 {
		t.Fatalf("migrated model rows retain %d NULL metadata values", nullMetadataCount)
	}
	var migrated ModelConfig
	if err := db.First(&migrated, legacy.ID).Error; err != nil {
		t.Fatalf("read migrated model row: %v", err)
	}
	if got := migrated.SyncedMetadata.Data(); !reflect.DeepEqual(got, ModelMetadata{}) {
		t.Fatalf("synced metadata = %+v, want empty object", got)
	}
	if got := migrated.MetadataOverride.Data(); !reflect.DeepEqual(got, ModelMetadataOverride{}) {
		t.Fatalf("metadata override = %+v, want empty object", got)
	}
	if err := db.Exec(`UPDATE model_configs SET synced_metadata = NULL WHERE id = ?`, legacy.ID).Error; err == nil {
		t.Fatal("synced_metadata must reject NULL after migration")
	}
}

func TestMasterSigningKeyPrivateKeyJSONIsolation(t *testing.T) {
	privateMarker := []byte("task8-private-key-marker-never-publish")
	one := uint8(1)
	key := MasterSigningKey{
		KeyID:      strings.Repeat("a", 64),
		PublicKey:  []byte("public-key-material"),
		PrivateKey: privateMarker,
		ActiveSlot: &one,
		CreatedAt:  123,
	}

	raw, err := json.Marshal(key)
	if err != nil {
		t.Fatalf("marshal master signing key: %v", err)
	}
	serialized := string(raw)
	for _, forbidden := range []string{
		string(privateMarker),
		base64.StdEncoding.EncodeToString(privateMarker),
		"PrivateKey",
		"private_key",
		"ActiveSlot",
		"active_slot",
	} {
		if strings.Contains(serialized, forbidden) {
			t.Fatal("master signing key JSON exposed private signing state")
		}
	}
}

func TestMasterSigningKeyActiveSlotIsUnique(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	one := uint8(1)
	first := MasterSigningKey{
		KeyID:      strings.Repeat("a", 64),
		PublicKey:  []byte("public-a"),
		PrivateKey: []byte("private-a"),
		ActiveSlot: &one,
	}
	if err := db.Create(&first).Error; err != nil {
		t.Fatalf("create first active key: %v", err)
	}
	second := MasterSigningKey{
		KeyID:      strings.Repeat("b", 64),
		PublicKey:  []byte("public-b"),
		PrivateKey: []byte("private-b"),
		ActiveSlot: &one,
	}
	if err := db.Create(&second).Error; err == nil {
		t.Fatal("expected unique active_slot index to reject a second active key")
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

func TestAutoMigrate_AddsUsageLogQueryIndexes(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	want := map[string][]indexColumn{
		"idx_usage_logs_created_id": {
			{name: "created_at", desc: true},
			{name: "id", desc: true},
		},
		"idx_usage_logs_user_created_id": {
			{name: "user_id"},
			{name: "created_at", desc: true},
			{name: "id", desc: true},
		},
		"idx_usage_logs_status_created_duration": {
			{name: "status"},
			{name: "created_at"},
			{name: "duration"},
		},
		"idx_usage_logs_agent_status_created": {
			{name: "agent_id"},
			{name: "status"},
			{name: "created_at", desc: true},
		},
		"idx_usage_logs_pchan_created_model": {
			{name: "private_channel_id"},
			{name: "created_at"},
			{name: "model_name"},
		},
		"idx_usage_logs_model_created_id": {
			{name: "model_name"},
			{name: "created_at", desc: true},
			{name: "id", desc: true},
		},
	}
	for name, wantColumns := range want {
		name, wantColumns := name, wantColumns
		t.Run(name, func(t *testing.T) {
			if !db.Migrator().HasIndex(&UsageLog{}, name) {
				t.Fatalf("expected usage_logs to have index %s", name)
			}

			if !usageLogHasIndex(t, sqlDB, name) {
				t.Fatalf("PRAGMA index_list did not include %s", name)
			}

			gotColumns := usageLogIndexColumns(t, sqlDB, name)
			if !reflect.DeepEqual(gotColumns, wantColumns) {
				t.Fatalf("index %s columns = %+v, want %+v", name, gotColumns, wantColumns)
			}
		})
	}

	if err := AutoMigrate(db); err != nil {
		t.Fatalf("second AutoMigrate should be idempotent: %v", err)
	}
}

func usageLogHasIndex(t *testing.T, sqlDB interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, name string) bool {
	t.Helper()

	rows, err := sqlDB.Query("PRAGMA index_list('usage_logs')")
	if err != nil {
		t.Fatalf("query index_list: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var seq int
		var gotName string
		var unique int
		var origin string
		var partial int
		if err := rows.Scan(&seq, &gotName, &unique, &origin, &partial); err != nil {
			t.Fatalf("scan index_list: %v", err)
		}
		if gotName == name {
			return true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_list: %v", err)
	}
	return false
}

func usageLogIndexColumns(t *testing.T, sqlDB interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, name string) []indexColumn {
	t.Helper()

	rows, err := sqlDB.Query("PRAGMA index_xinfo('" + name + "')")
	if err != nil {
		t.Fatalf("query index_xinfo for %s: %v", name, err)
	}
	defer rows.Close()

	var columns []indexColumn
	for rows.Next() {
		var seqno int
		var cid int
		var colName sql.NullString
		var desc int
		var coll string
		var key int
		if err := rows.Scan(&seqno, &cid, &colName, &desc, &coll, &key); err != nil {
			t.Fatalf("scan index_xinfo for %s: %v", name, err)
		}
		if key == 0 || !colName.Valid {
			continue
		}
		columns = append(columns, indexColumn{name: colName.String, desc: desc == 1})
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate index_xinfo for %s: %v", name, err)
	}
	return columns
}

func TestAutoMigrate_KeepsLogOwnedDailyTablesForLegacyLayout(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, table := range []string{"token_daily_billings", "channel_daily_billings"} {
		table := table
		t.Run(table, func(t *testing.T) {
			if !db.Migrator().HasTable(table) {
				t.Fatalf("legacy log-owned table %s must be created", table)
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

func TestAutoMigrate_UsageLogAgentRouteScalars(t *testing.T) {
	db := setupTestDB(t)
	sqlDB, _ := db.DB()
	defer sqlDB.Close()

	for _, column := range []string{"route_source_agent_id", "agent_route_id", "agent_route_path"} {
		if !db.Migrator().HasColumn(&UsageLog{}, column) {
			t.Errorf("expected usage_logs to have column %s", column)
		}
	}
	for _, index := range []string{
		"idx_usage_logs_route_source_agent_id",
		"idx_usage_logs_agent_route_id",
		"idx_usage_logs_agent_route_path",
	} {
		if !db.Migrator().HasIndex(&UsageLog{}, index) {
			t.Errorf("expected usage_logs to have index %s", index)
		}
	}

	type fieldContract struct {
		name string
		tag  string
	}
	for _, contract := range []fieldContract{
		{name: "RouteSourceAgentID", tag: "size:64;index"},
		{name: "AgentRouteID", tag: "index"},
		{name: "AgentRoutePath", tag: "size:16;index"},
	} {
		field, ok := reflect.TypeOf(UsageLog{}).FieldByName(contract.name)
		if !ok {
			t.Errorf("UsageLog.%s is missing", contract.name)
			continue
		}
		if got := field.Tag.Get("gorm"); got != contract.tag {
			t.Errorf("UsageLog.%s gorm tag = %q, want %q", contract.name, got, contract.tag)
		}
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

func TestTokenTraceModeMigrateDefaultsFull(t *testing.T) {
	db := setupTestDB(t)

	defaultMode := Token{Key: "sk-trace-default", Name: "default", Status: 1}
	if err := db.Create(&defaultMode).Error; err != nil {
		t.Fatal(err)
	}
	var got Token
	if err := db.First(&got, defaultMode.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.TraceMode != TokenTraceModeFull {
		t.Fatalf("TraceMode=%q want=%q", got.TraceMode, TokenTraceModeFull)
	}

	headers := Token{Key: "sk-trace-headers", Name: "headers", Status: 1, TraceMode: TokenTraceModeHeaders}
	if err := db.Create(&headers).Error; err != nil {
		t.Fatal(err)
	}
	got = Token{}
	if err := db.First(&got, headers.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.TraceMode != TokenTraceModeHeaders {
		t.Fatalf("TraceMode=%q want=%q", got.TraceMode, TokenTraceModeHeaders)
	}
}

func TestTokenTraceModeMigrateBackfillsLegacyRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec(`CREATE TABLE tokens (id integer primary key, key text, name text, status integer)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`INSERT INTO tokens (id, key, name, status) VALUES (1, 'sk-legacy', 'legacy', 1)`).Error; err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	var got Token
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.TraceMode != TokenTraceModeFull {
		t.Fatalf("TraceMode=%q want=%q", got.TraceMode, TokenTraceModeFull)
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
		RequestID:            "req-trace-test",
		TraceRetentionStatus: TraceRetentionHeadersOnly,
		ErrorStage:           "outbound_encode",
		InboundDecodeMs:      1,
		OutboundEncodeMs:     2,
		UpstreamDispatchMs:   100,
		UpstreamDecodeMs:     5,
		ClientEncodeMs:       3,
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
	if got.TraceRetentionStatus != TraceRetentionHeadersOnly {
		t.Errorf("TraceRetentionStatus = %q, want headers_only", got.TraceRetentionStatus)
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
