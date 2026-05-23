package models

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"gorm.io/gorm"
)

// SeedDefaultUserGroup ensures the id=1 default user_group exists, and backfills
// users whose group_id is 0 / NULL to 1. Idempotent.
func SeedDefaultUserGroup(db *gorm.DB) error {
	var count int64
	if err := db.Model(&UserGroup{}).Where("id = 1").Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		now := time.Now().Unix()
		err := db.Exec(`INSERT INTO user_groups
			(id, name, description, status, allowed_channel_ids, models, created_at, updated_at)
			VALUES (1, 'default', 'default user group', 1, '[]', '', ?, ?)`, now, now).Error
		if err != nil {
			return err
		}
	}
	return db.Model(&User{}).Where("group_id = 0 OR group_id IS NULL").Update("group_id", 1).Error
}

// SeedBYOKSettings 把 BYOK 相关默认 settings 写入；仅在 key 不存在时插入，
// 已有值（如 admin 改过的）保留不覆盖。idempotent，可在每次启动调用。
func SeedBYOKSettings(db *gorm.DB) error {
	defaults := []Setting{
		{Key: consts.SettingKeyBYOKEnabled, Value: consts.BYOKDefaultEnabledStr},
		{Key: consts.SettingKeyBYOKMaxChannelsPerUser, Value: consts.BYOKDefaultMaxChannelsPerUserStr},
		{Key: consts.SettingKeyBYOKBillingMode, Value: consts.BYOKDefaultBillingMode},
		{Key: consts.SettingKeyBYOKServiceFeeRatio, Value: consts.BYOKDefaultServiceFeeRatioStr},
		{Key: consts.SettingKeyBYOKBaseURLAllowlist, Value: consts.BYOKDefaultBaseURLAllowlistStr}, // 仅 admin 追加部分
	}
	for _, s := range defaults {
		if err := db.Where("key = ?", s.Key).Attrs(s).
			FirstOrCreate(&Setting{Key: s.Key}).Error; err != nil {
			return err
		}
	}
	return nil
}
