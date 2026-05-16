package models

type EnrollmentToken struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	Token     string `gorm:"uniqueIndex;size:128" json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	CreatedAt int64  `gorm:"autoCreateTime" json:"created_at"`
}
