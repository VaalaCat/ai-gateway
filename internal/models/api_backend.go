package models

import (
	"fmt"

	"gorm.io/gorm"
)

// APIBackend groups the upstream pool selected by routes in one API service.
type APIBackend struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	APIServiceID uint   `gorm:"not null;uniqueIndex:idx_api_backend_service_name" json:"api_service_id"`
	Name         string `gorm:"size:64;not null;uniqueIndex:idx_api_backend_service_name" json:"name"`
	CreatedAt    int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    int64  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (b *APIBackend) Validate() error {
	if b.APIServiceID == 0 {
		return fmt.Errorf("api backend api_service_id must not be zero")
	}
	if b.Name == "" {
		return fmt.Errorf("api backend name must not be empty")
	}
	return nil
}

func (b *APIBackend) BeforeCreate(*gorm.DB) error { return b.Validate() }

func (b *APIBackend) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		if err := b.Validate(); err != nil {
			return err
		}
		var persisted APIBackend
		if err := tx.Session(&gorm.Session{NewDB: true}).Select("api_service_id").First(&persisted, b.ID).Error; err != nil {
			return err
		}
		if persisted.APIServiceID != b.APIServiceID {
			return fmt.Errorf("api backend api_service_id cannot be changed")
		}
		return nil
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "APIServiceID", validate: rejectAPIBackendServicePartialUpdate},
		apiPatchField{name: "Name", validate: func(value any) error {
			name, err := apiPatchString(value, "api backend name")
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("api backend name must not be empty")
			}
			return nil
		}},
	)
}

func rejectAPIBackendServicePartialUpdate(any) error {
	return fmt.Errorf("partial api backend api_service_id updates are not supported")
}
