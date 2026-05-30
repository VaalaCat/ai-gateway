package models

import "gorm.io/datatypes"

// ScriptScope 决定一个脚本对哪些请求生效。
// ChannelIDs 与 ModelNames 均为空 = 全局；否则命中的 channel 或 model
// 命中任一列表即生效。
type ScriptScope struct {
	ChannelIDs []uint   `json:"channel_ids"`
	ModelNames []string `json:"model_names"`
}

// AdminScript 是管理员编写的动态 goja 脚本。仅管理员可管理。
type AdminScript struct {
	ID        uint                            `gorm:"primaryKey" json:"id"`
	Name      string                          `gorm:"size:128;uniqueIndex" json:"name"`
	Code      string                          `gorm:"type:text" json:"code"`
	Enabled   bool                            `json:"enabled"`
	Priority  int                             `gorm:"default:0" json:"priority"`
	Scope     datatypes.JSONType[ScriptScope] `json:"scope"`
	CreatedAt int64                           `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64                           `gorm:"autoUpdateTime" json:"updated_at"`
}
