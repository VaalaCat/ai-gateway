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
	); err != nil {
		return err
	}

	if err := backfillPasswordSet(db); err != nil {
		return err
	}
	return ensureUserEmailUniqueIndex(db)
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
