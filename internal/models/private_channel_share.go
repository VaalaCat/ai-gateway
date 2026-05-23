package models

const (
	PrivateShareTargetUser  = "user"
	PrivateShareTargetGroup = "group"
)

// PrivateChannelShare 是 v1 落地但 API 不暴露的旁路表。
// 一条记录代表一个分享条目：把某 channel 分享给一个 user 或一个 user_group。
type PrivateChannelShare struct {
	ID         uint   `gorm:"primaryKey" json:"id"`
	ChannelID  uint   `gorm:"index:idx_share_lookup;not null;uniqueIndex:uidx_share_triple" json:"channel_id"`
	TargetType string `gorm:"size:8;index:idx_share_lookup;uniqueIndex:uidx_share_triple" json:"target_type"`
	TargetID   uint   `gorm:"index:idx_share_lookup;uniqueIndex:uidx_share_triple" json:"target_id"`
	CreatedAt  int64  `gorm:"autoCreateTime" json:"created_at"`
}

func (PrivateChannelShare) TableName() string { return "private_channel_shares" }
