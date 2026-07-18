package models

import (
	"fmt"

	"gorm.io/gorm"
)

func AutoMigrate(db *gorm.DB) error {
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
		&AdminScript{},
		&InviteCode{},
		&InviteRedemption{},
		&MasterSigningKey{},
	); err != nil {
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
	return dropLegacyTraceRequestIDUniqueIndex(db)
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
