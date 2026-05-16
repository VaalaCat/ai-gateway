package models

import (
	"time"

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
