package models

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func AutoMigrate(db *gorm.DB) error {
	// Transitional legacy entrypoint for production startup and legacy fixtures until
	// the Server/DAO dual-database routing tasks enable the split layout end to end.
	// It intentionally keeps the mixed schema and never writes a split layout marker.
	if err := preBackfillChannelAutoBanRuntime(db); err != nil {
		return err
	}
	if err := db.AutoMigrate(
		&User{},
		&Token{},
		&Channel{},
		&ModelConfig{},
		&Agent{},
		&UsageLog{},
		&TokenDailyBilling{},
		&ChannelDailyBilling{},
		&EnrollmentToken{},
		&Setting{},
		&UsageLogTrace{},
		&AgentRoute{},
		&RequestLimiter{},
		&LimiterBinding{},
		&TokenTemplate{},
		&UserGroup{},
		&OAuthProvider{},
		&OAuthIdentity{},
		&ModelRouting{},
		&PrivateChannel{},
		&PrivateChannelShare{},
		&UsageHourlyBucket{},
		&UsageDurationHistogram{},
		&UsageTTFTHistogram{},
		&UsageTPSHistogram{},
		&UsageUserTTFTHistogram{},
		&UsageUserTPSHistogram{},
		&LogHistoryAggregateMerge{},
		&AdminScript{},
		&InviteCode{},
		&InviteRedemption{},
		&MasterSigningKey{},
	); err != nil {
		return err
	}
	if err := dropLegacyAgentRoutingColumn(db); err != nil {
		return err
	}

	if err := ensureUsageLogQueryIndexes(db); err != nil {
		return err
	}
	if err := ensureModelRoutingOwnerIndex(db); err != nil {
		return err
	}
	if err := backfillPasswordSet(db); err != nil {
		return err
	}
	if err := ensureUserEmailUniqueIndex(db); err != nil {
		return err
	}
	if err := dropLegacyChannelBillingIndex(db); err != nil {
		return err
	}
	if err := dropLegacyTraceRequestIDUniqueIndex(db); err != nil {
		return err
	}
	if err := backfillChannelAutoBanRuntime(db); err != nil {
		return err
	}
	return deleteLegacyRelayFallbackSetting(db)
}

func MigrateCoreDB(db *gorm.DB) error {
	if err := migrateSplitDatabase(db, DatabaseRoleCore, coreModels(), migrateCoreCleanup, preBackfillChannelAutoBanRuntime); err != nil {
		return fmt.Errorf("migrate core database: %w", err)
	}
	return nil
}

func MigrateLogDB(db *gorm.DB) error {
	if err := migrateSplitDatabase(db, DatabaseRoleLog, logModels(), ensureRequestLogQueryIndexes); err != nil {
		return fmt.Errorf("migrate log database: %w", err)
	}
	return nil
}

func migrateSplitDatabase(db *gorm.DB, role string, models []any, finish func(*gorm.DB) error, beforeMigrate ...func(*gorm.DB) error) error {
	if db == nil {
		return fmt.Errorf("%s database is nil", role)
	}
	if db.Error != nil {
		return fmt.Errorf("%s database is invalid: %w", role, db.Error)
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := ensureDatabaseLayout(tx, role); err != nil {
			return err
		}
		for _, before := range beforeMigrate {
			if err := before(tx); err != nil {
				return fmt.Errorf("prepare %s schema migration: %w", role, err)
			}
		}
		for _, model := range models {
			if err := tx.AutoMigrate(model); err != nil {
				return fmt.Errorf("migrate %s schema: %w", role, err)
			}
		}
		if err := finish(tx); err != nil {
			return fmt.Errorf("finish %s schema migration: %w", role, err)
		}
		return nil
	})
}

func ensureDatabaseLayout(db *gorm.DB, role string) error {
	if !db.Migrator().HasTable(&DatabaseLayout{}) {
		empty, err := databaseHasNoUserTables(db)
		if err != nil {
			return err
		}
		if !empty {
			return fmt.Errorf("unmarked non-empty database cannot be claimed as %q", role)
		}
		if err := db.AutoMigrate(&DatabaseLayout{}); err != nil {
			return fmt.Errorf("create database layout schema: %w", err)
		}
		marker := DatabaseLayout{ID: DatabaseLayoutID, Role: role, Version: DatabaseLayoutVersion}
		if err := db.Create(&marker).Error; err != nil {
			return fmt.Errorf("create database layout marker: %w", err)
		}
		return nil
	}
	var layouts []DatabaseLayout
	if err := db.Find(&layouts).Error; err != nil {
		return fmt.Errorf("invalid database layout: read marker: %w", err)
	}
	if len(layouts) == 0 {
		return fmt.Errorf("invalid database layout: marker table is empty")
	}
	if len(layouts) != 1 {
		return fmt.Errorf("invalid database layout: marker count is %d, want 1", len(layouts))
	}
	marker := layouts[0]
	if marker.ID != DatabaseLayoutID {
		return fmt.Errorf("invalid database layout: marker id is %d, want %d", marker.ID, DatabaseLayoutID)
	}
	if marker.Role != DatabaseRoleCore && marker.Role != DatabaseRoleLog {
		return fmt.Errorf("invalid database layout: unknown role %q", marker.Role)
	}
	if marker.Version != DatabaseLayoutVersion {
		return fmt.Errorf("invalid database layout: database layout version is %d, want %d", marker.Version, DatabaseLayoutVersion)
	}
	if err := validateDatabaseLayoutSchema(db); err != nil {
		return err
	}
	if marker.Role != role {
		return fmt.Errorf("database layout role is %q, cannot migrate as %q", marker.Role, role)
	}
	return nil
}

func databaseHasNoUserTables(db *gorm.DB) (bool, error) {
	var count int64
	if err := db.Raw(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%'`).Scan(&count).Error; err != nil {
		return false, fmt.Errorf("inspect database tables: %w", err)
	}
	return count == 0, nil
}

func validateDatabaseLayoutSchema(db *gorm.DB) error {
	type columnInfo struct {
		Name    string `gorm:"column:name"`
		NotNull int    `gorm:"column:notnull"`
		PK      int    `gorm:"column:pk"`
	}
	var columns []columnInfo
	if err := db.Raw(`PRAGMA table_info('database_layouts')`).Scan(&columns).Error; err != nil {
		return fmt.Errorf("inspect database layout schema: %w", err)
	}
	want := map[string]columnInfo{
		"id":      {Name: "id", NotNull: 1, PK: 1},
		"role":    {Name: "role", NotNull: 1},
		"version": {Name: "version", NotNull: 1},
	}
	if len(columns) != len(want) {
		return fmt.Errorf("invalid database layout schema: got %d columns", len(columns))
	}
	for _, column := range columns {
		expected, ok := want[column.Name]
		if !ok || column.NotNull != expected.NotNull || column.PK != expected.PK {
			return fmt.Errorf("invalid database layout schema: column %q has unexpected constraints", column.Name)
		}
	}
	var createSQL string
	if err := db.Raw(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'database_layouts'`).Scan(&createSQL).Error; err != nil {
		return fmt.Errorf("inspect database layout checks: %w", err)
	}
	compactSQL := strings.Map(func(r rune) rune {
		if r == '`' || r == '"' || r == '\'' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.ToLower(createSQL))
	for _, check := range []string{"check(id=1)", "check(rolein(core,log))"} {
		if !strings.Contains(compactSQL, check) {
			return fmt.Errorf("invalid database layout schema: missing %s", check)
		}
	}
	return nil
}

func coreModels() []any {
	return []any{
		&User{}, &Token{}, &Channel{}, &ModelConfig{}, &Agent{}, &EnrollmentToken{},
		&Setting{}, &AgentRoute{}, &RequestLimiter{}, &LimiterBinding{}, &TokenTemplate{},
		&UserGroup{}, &OAuthProvider{}, &OAuthIdentity{}, &ModelRouting{}, &PrivateChannel{},
		&PrivateChannelShare{}, &AdminScript{}, &InviteCode{}, &InviteRedemption{},
		&MasterSigningKey{}, &BillingLog{}, &HistoryMigration{}, &HistoryCursor{},
	}
}

func logModels() []any {
	return []any{
		&RequestLog{}, &RequestTrace{}, &UsageHourlyBucket{}, &UsageDurationHistogram{},
		&UsageTTFTHistogram{}, &UsageTPSHistogram{}, &UsageUserTTFTHistogram{},
		&UsageUserTPSHistogram{},
		&LogHistoryAggregateMerge{}, &HistoryCursor{}, &TokenDailyBilling{},
		&ChannelDailyBilling{}, &DailyBillingBackfill{},
	}
}

func migrateCoreCleanup(db *gorm.DB) error {
	steps := []func(*gorm.DB) error{
		dropLegacyAgentRoutingColumn,
		ensureModelRoutingOwnerIndex,
		backfillPasswordSet,
		ensureUserEmailUniqueIndex,
		dropLegacyChannelBillingIndex,
		backfillChannelAutoBanRuntime,
		deleteLegacyRelayFallbackSetting,
	}
	for _, step := range steps {
		if err := step(db); err != nil {
			return err
		}
	}
	return nil
}

func backfillChannelAutoBanRuntime(db *gorm.DB) error {
	return backfillChannelAutoBanRuntimeColumns(db)
}

func preBackfillChannelAutoBanRuntime(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	if db.Error != nil {
		return fmt.Errorf("database is invalid: %w", db.Error)
	}
	return backfillChannelAutoBanRuntimeColumns(db)
}

func backfillChannelAutoBanRuntimeColumns(db *gorm.DB) error {
	for _, table := range []string{"channels", "private_channels"} {
		if !db.Migrator().HasTable(table) {
			continue
		}
		if db.Migrator().HasColumn(table, "auto_ban_state") {
			if err := db.Exec("UPDATE " + table + " SET auto_ban_state = '{}' WHERE auto_ban_state IS NULL OR auto_ban_state = ''").Error; err != nil {
				return fmt.Errorf("backfill %s auto_ban_state: %w", table, err)
			}
		}
		if db.Migrator().HasColumn(table, "auto_ban_revision") {
			if err := db.Exec("UPDATE " + table + " SET auto_ban_revision = 0 WHERE auto_ban_revision IS NULL").Error; err != nil {
				return fmt.Errorf("backfill %s auto_ban_revision: %w", table, err)
			}
		}
	}
	return nil
}

func ensureRequestLogQueryIndexes(db *gorm.DB) error {
	indexes := []struct {
		name string
		sql  string
	}{
		{name: "idx_request_logs_created_id", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_created_id ON request_logs(created_at DESC, id DESC)`},
		{name: "idx_request_logs_user_created_model", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_user_created_model ON request_logs(user_id, created_at DESC, model_name)`},
		{name: "idx_request_logs_model_created_user", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_model_created_user ON request_logs(model_name, created_at DESC, user_id)`},
		{name: "idx_request_logs_status_created_duration", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_status_created_duration ON request_logs(status, created_at, duration)`},
		{name: "idx_request_logs_agent_status_created", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_agent_status_created ON request_logs(agent_id, status, created_at DESC)`},
		{name: "idx_request_logs_pchan_created_model", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_pchan_created_model ON request_logs(private_channel_id, created_at, model_name)`},
		{name: "idx_request_logs_window_stats", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_window_stats ON request_logs(created_at, status, duration)`},
		{name: "idx_request_logs_user_window_stats", sql: `CREATE INDEX IF NOT EXISTS idx_request_logs_user_window_stats ON request_logs(user_id, created_at, status, duration)`},
	}
	for _, index := range indexes {
		if db.Migrator().HasIndex(&RequestLog{}, index.name) {
			continue
		}
		if err := db.Exec(index.sql).Error; err != nil {
			return fmt.Errorf("create request log index %s: %w", index.name, err)
		}
	}
	return nil
}

func dropLegacyAgentRoutingColumn(db *gorm.DB) error {
	column := strings.Join([]string{"peer", "route", "mode"}, "_")
	if !db.Migrator().HasColumn(&Agent{}, column) {
		return nil
	}
	if err := db.Exec(
		"ALTER TABLE ? DROP COLUMN ?",
		clause.Table{Name: "agents"},
		clause.Column{Name: column},
	).Error; err != nil {
		return fmt.Errorf("drop legacy agent routing column: %w", err)
	}
	return nil
}

func deleteLegacyRelayFallbackSetting(db *gorm.DB) error {
	return db.Where("key = ?", legacyRelayFallbackSettingKey()).Delete(&Setting{}).Error
}

func legacyRelayFallbackSettingKey() string {
	return "agent." + strings.Join([]string{"relay", "fallback", "enabled"}, "_")
}

func ensureModelRoutingOwnerIndex(db *gorm.DB) error {
	const (
		currentIndex = "uidx_routing_owner_name"
		legacyIndex  = "uidx_routing_scope_user_name"
	)
	if !db.Migrator().HasIndex(&ModelRouting{}, currentIndex) {
		if err := db.Migrator().CreateIndex(&ModelRouting{}, currentIndex); err != nil {
			return fmt.Errorf("create model routing owner index: %w", err)
		}
	}
	if db.Migrator().HasIndex(&ModelRouting{}, legacyIndex) {
		if err := db.Migrator().DropIndex(&ModelRouting{}, legacyIndex); err != nil {
			return fmt.Errorf("drop legacy model routing owner index: %w", err)
		}
	}
	if !db.Migrator().HasIndex(&ModelRouting{}, currentIndex) {
		return fmt.Errorf("model routing owner index %q is missing", currentIndex)
	}
	return nil
}

func ensureUsageLogQueryIndexes(db *gorm.DB) error {
	indexes := []struct {
		name string
		sql  string
	}{
		{
			name: "idx_usage_logs_created_id",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_created_id ON usage_logs(created_at DESC, id DESC)`,
		},
		{
			name: "idx_usage_logs_user_created_id",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_user_created_id ON usage_logs(user_id, created_at DESC, id DESC)`,
		},
		{
			name: "idx_usage_logs_status_created_duration",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_status_created_duration ON usage_logs(status, created_at, duration)`,
		},
		{
			name: "idx_usage_logs_agent_status_created",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_agent_status_created ON usage_logs(agent_id, status, created_at DESC)`,
		},
		{
			name: "idx_usage_logs_pchan_created_model",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_pchan_created_model ON usage_logs(private_channel_id, created_at, model_name)`,
		},
		{
			name: "idx_usage_logs_model_created_id",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_model_created_id ON usage_logs(model_name, created_at DESC, id DESC)`,
		},
		{
			name: "idx_usage_logs_window_stats",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_window_stats ON usage_logs(created_at, status, duration)`,
		},
		{
			name: "idx_usage_logs_user_window_stats",
			sql:  `CREATE INDEX IF NOT EXISTS idx_usage_logs_user_window_stats ON usage_logs(user_id, created_at, status, duration)`,
		},
	}
	for _, idx := range indexes {
		if db.Migrator().HasIndex(&UsageLog{}, idx.name) {
			continue
		}
		if err := db.Exec(idx.sql).Error; err != nil {
			return err
		}
	}
	return nil
}

// backfillPasswordSet 把已经设过密码的存量用户标记为 PasswordSet=true。
// 仅对 password_set=0 且 password!=” 的行生效，可重复执行。
func backfillPasswordSet(db *gorm.DB) error {
	return db.Exec(`UPDATE users SET password_set = 1 WHERE password_set = 0 AND password != ''`).Error
}

// ensureUserEmailUniqueIndex 创建 email 字段的部分唯一索引（允许空串）。
// 可重复执行（IF NOT EXISTS）。
func ensureUserEmailUniqueIndex(db *gorm.DB) error {
	return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email != ''`).Error
}

// dropLegacyTraceRequestIDUniqueIndex 删除 usage_log_traces 表上的旧 request_id 单列唯一索引。
// 升级到逐 attempt 一行后,唯一键改为 (request_id, attempt_index) 复合索引
// (idx_trace_req_attempt),旧的单列唯一索引不再使用。
// SQLite IF EXISTS 幂等,重复执行或新装部署无旧索引时均安全。
func dropLegacyTraceRequestIDUniqueIndex(db *gorm.DB) error {
	return db.Exec(`DROP INDEX IF EXISTS idx_usage_log_traces_request_id`).Error
}

// dropLegacyChannelBillingIndex 删除 channel_daily_billings 表上的旧 unique
// 索引 idx_channel_daily_billing_date_channel——升级到 BYOK schema 后，
// 唯一键改成 (date, channel_id, private_channel_id) 三列联合
// (idx_cdb_date_channel_pchan)，旧索引不再使用。
// GORM AutoMigrate 不会自动 DROP 索引（怕丢数据），因此显式 drop 一次。
// SQLite IF EXISTS 幂等，重复执行无副作用；新装部署无旧索引也安全。
func dropLegacyChannelBillingIndex(db *gorm.DB) error {
	return db.Exec(`DROP INDEX IF EXISTS idx_channel_daily_billing_date_channel`).Error
}
