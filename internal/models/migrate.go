package models

import "gorm.io/gorm"

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
		&TokenTemplate{},
		&UserGroup{},
		&OAuthProvider{},
		&OAuthIdentity{},
		&ModelRouting{},
		&PrivateChannel{},
		&PrivateChannelShare{},
		&UsageHourlyBucket{},
	); err != nil {
		return err
	}

	if err := backfillPasswordSet(db); err != nil {
		return err
	}
	if err := ensureUserEmailUniqueIndex(db); err != nil {
		return err
	}
	return dropLegacyChannelBillingIndex(db)
}

// backfillPasswordSet 把已经设过密码的存量用户标记为 PasswordSet=true。
// 仅对 password_set=0 且 password!='' 的行生效，可重复执行。
func backfillPasswordSet(db *gorm.DB) error {
	return db.Exec(`UPDATE users SET password_set = 1 WHERE password_set = 0 AND password != ''`).Error
}

// ensureUserEmailUniqueIndex 创建 email 字段的部分唯一索引（允许空串）。
// 可重复执行（IF NOT EXISTS）。
func ensureUserEmailUniqueIndex(db *gorm.DB) error {
	return db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email ON users(email) WHERE email != ''`).Error
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
