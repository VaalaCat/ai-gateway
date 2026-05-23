// internal/models/user_group.go
package models

import "gorm.io/datatypes"

type UserGroup struct {
	ID                uint                      `gorm:"primaryKey" json:"id"`
	Name              string                    `gorm:"uniqueIndex;size:64;not null" json:"name"`
	Description       string                    `gorm:"size:255" json:"description"`
	Status            int                       `gorm:"default:1" json:"status"`
	AllowedChannelIDs datatypes.JSONSlice[uint] `gorm:"type:text" json:"allowed_channel_ids"`
	Models            string                    `gorm:"type:text" json:"models"`
	BYOKEnabled     *bool `gorm:"default:null" json:"byok_enabled"`
	BYOKMaxChannels *int  `gorm:"default:null" json:"byok_max_channels"`
	CreatedAt       int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt       int64 `gorm:"autoUpdateTime" json:"updated_at"`
}
