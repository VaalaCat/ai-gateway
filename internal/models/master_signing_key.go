package models

type MasterSigningKey struct {
	KeyID      string `gorm:"primaryKey;size:64"`
	PublicKey  []byte `gorm:"type:blob;not null"`
	PrivateKey []byte `gorm:"type:blob;not null" json:"-"`
	ActiveSlot *uint8 `gorm:"uniqueIndex" json:"-"`
	CreatedAt  int64  `gorm:"autoCreateTime"`
}
