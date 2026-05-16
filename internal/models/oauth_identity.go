package models

type OAuthIdentity struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	UserID      uint   `gorm:"index" json:"user_id"`
	ProviderID  uint   `gorm:"index;uniqueIndex:uk_provider_subject,priority:1" json:"provider_id"`
	Subject     string `gorm:"size:255;uniqueIndex:uk_provider_subject,priority:2" json:"subject"`
	Email       string `gorm:"size:255" json:"email"`
	DisplayName string `gorm:"size:128" json:"display_name"`
	CreatedAt   int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   int64  `gorm:"autoUpdateTime" json:"updated_at"`
}
