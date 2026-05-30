package models

// InviteCode 是一张邀请码。CreatorID 标识归属(用于普通用户限额与审计)。
// "有效"= UsedCount < MaxUses 且 (ExpiresAt == 0 或 ExpiresAt > now)。
type InviteCode struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Code      string `gorm:"uniqueIndex;size:32;not null" json:"code"`
	CreatorID uint   `gorm:"index;not null" json:"creator_id"`
	MaxUses   int    `gorm:"not null;default:1" json:"max_uses"`
	UsedCount int    `gorm:"not null;default:0" json:"used_count"`
	ExpiresAt int64  `gorm:"default:0" json:"expires_at"`
	Note      string `gorm:"size:128" json:"note"`
	CreatedAt int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"autoUpdateTime" json:"updated_at"`
}
