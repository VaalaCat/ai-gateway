package models

// InviteRedemption 记录一次成功的邀请注册(一行=一个被邀用户),用于审计。
// InviteeID 唯一:一个用户只能被邀请注册一次。
type InviteRedemption struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	InviteCodeID uint   `gorm:"index;not null" json:"invite_code_id"`
	Code         string `gorm:"size:32;index;not null" json:"code"`
	InviterID    uint   `gorm:"index;not null" json:"inviter_id"`
	InviteeID    uint   `gorm:"uniqueIndex;not null" json:"invitee_id"`
	CreatedAt    int64  `gorm:"autoCreateTime" json:"created_at"`
}
