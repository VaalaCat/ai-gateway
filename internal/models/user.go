package models

type User struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Username    string `gorm:"uniqueIndex;size:64" json:"username"`
	Email       string `gorm:"size:191" json:"email"`
	DisplayName string `gorm:"size:64" json:"display_name"`
	AvatarURL   string `gorm:"size:512" json:"avatar_url"`
	Password    string `gorm:"size:128" json:"password,omitempty"`
	Role        int    `gorm:"default:1" json:"role"`
	Status      int    `gorm:"default:1" json:"status"`
	GroupID     uint   `gorm:"index;default:1" json:"group_id"`
	Quota       int64  `gorm:"default:0" json:"quota"`
	UsedQuota   int64  `gorm:"default:0" json:"used_quota"`
	CreatedAt   int64  `gorm:"autoCreateTime" json:"created_at"`
	PasswordSet bool   `gorm:"default:false" json:"password_set"`
	UpdatedAt   int64  `gorm:"autoUpdateTime" json:"updated_at"`
}
