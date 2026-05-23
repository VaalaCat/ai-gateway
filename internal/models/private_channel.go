package models

import "gorm.io/datatypes"

// PrivateChannel 是用户自助上传的 BYOK channel。
// 与 admin Channel 共享的标量字段抽进 ChannelCore（gorm embed 自动展开为列）；
// 仅保留 BYOK 专属字段（Owner/KeyCipher/KeyLast4），以及 Go 类型与 admin 不同的
// Models / ModelMapping（强类型 JSON）。Name / Status / Tag 重声明以覆盖
// ChannelCore 的 gorm tag——Name/Status 挂 owner 维度复合索引，Tag 加单列
// index 与 admin Channel 对齐（admin Channel.Tag 历来带 index，重构不改变现状）。
type PrivateChannel struct {
	ChannelCore

	OwnerID   uint   `gorm:"index:idx_pchan_owner_status;not null;uniqueIndex:uidx_pchan_owner_name" json:"owner_id"`
	KeyCipher []byte `gorm:"type:blob" json:"-"`
	KeyLast4  string `gorm:"size:8" json:"key_last4"`

	// Strongly-typed BYOK-only fields (admin Channel stores these as CSV/JSON text).
	Models       datatypes.JSONSlice[string]           `gorm:"type:text" json:"models"`
	ModelMapping datatypes.JSONType[map[string]string] `gorm:"type:text" json:"model_mapping"`

	// Redeclared to override the ChannelCore gorm tag. Name/Status carry composite
	// indexes scoped to (owner_id, ...); Tag carries a single-column index to match
	// admin Channel.Tag behavior. Same JSON tags preserve the wire shape.
	Name   string `gorm:"size:64;uniqueIndex:uidx_pchan_owner_name" json:"name"`
	Status int    `gorm:"index:idx_pchan_owner_status;default:1" json:"status"`
	Tag    string `gorm:"size:64;index" json:"tag"`
}

func (PrivateChannel) TableName() string { return "private_channels" }
