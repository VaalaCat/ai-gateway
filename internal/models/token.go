package models

import "gorm.io/datatypes"

type Token struct {
	ID                uint                      `gorm:"primaryKey" json:"id"`
	UserID            uint                      `gorm:"index" json:"user_id"`
	Key               string                    `gorm:"uniqueIndex;size:64" json:"key"`
	Name              string                    `gorm:"size:64" json:"name"`
	Status            int                       `gorm:"default:1" json:"status"`
	ExpiredAt         int64                     `gorm:"default:-1" json:"expired_at"`
	Models            string                    `gorm:"type:text" json:"models"`
	TemplateID        *uint                     `gorm:"index" json:"template_id"`
	TraceEnabled      bool                      `gorm:"default:false" json:"trace_enabled"`
	TraceMode         TokenTraceMode            `gorm:"size:16;default:full" json:"trace_mode"`
	APIRoleMode       APIRoleMode               `gorm:"size:16;not null;default:inherit;check:chk_tokens_api_role_mode,api_role_mode IN ('inherit','explicit')" json:"api_role_mode"`
	BYOKOnly          bool                      `gorm:"default:false" json:"byok_only"`
	AllowedChannelIDs datatypes.JSONSlice[uint] `gorm:"type:text" json:"allowed_channel_ids"`
	CreatedAt         int64                     `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt         int64                     `gorm:"autoUpdateTime" json:"updated_at"`
}
