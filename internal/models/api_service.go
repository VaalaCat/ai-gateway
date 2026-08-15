package models

import (
	"fmt"
	"regexp"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"gorm.io/gorm"
)

var apiSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._~-]*$`)

// APIService is an externally invokable generic API product. PricePerCall is
// measured in quota units: 100000 is one USD and zero means free.
type APIService struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Slug         string `gorm:"uniqueIndex;size:64;not null" json:"slug"`
	Name         string `gorm:"size:128;not null" json:"name"`
	Description  string `gorm:"type:text" json:"description"`
	PricePerCall int64  `gorm:"not null;default:0;check:chk_api_service_price_per_call,price_per_call >= 0" json:"price_per_call"`
	Status       int    `gorm:"not null;default:1" json:"status"`
	CreatedAt    int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt    int64  `gorm:"autoUpdateTime" json:"updated_at"`
}

func (s *APIService) Validate() error {
	if err := validateAPIServiceSlug(s.Slug); err != nil {
		return err
	}
	if s.Name == "" {
		return fmt.Errorf("api service name must not be empty")
	}
	if err := validateAPIServicePrice(s.PricePerCall); err != nil {
		return err
	}
	if err := validateAPIStatus("api service", int64(s.Status)); err != nil {
		return err
	}
	return nil
}

// BeforeCreate protects the initial persisted contract. Partial updates use
// zero-value GORM receivers, so later DAO code must load, patch, and call
// Validate explicitly instead of relying on a full-object update hook.
func (s *APIService) BeforeCreate(*gorm.DB) error { return s.Validate() }

func (s *APIService) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		return s.Validate()
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "Slug", validate: func(value any) error {
			slug, err := apiPatchString(value, "api service slug")
			if err != nil {
				return err
			}
			return validateAPIServiceSlug(slug)
		}},
		apiPatchField{name: "PricePerCall", validate: func(value any) error {
			price, err := apiPatchInt(value, "api service price_per_call")
			if err != nil {
				return err
			}
			return validateAPIServicePrice(price)
		}},
		apiPatchField{name: "Status", validate: func(value any) error {
			status, err := apiPatchInt(value, "api service status")
			if err != nil {
				return err
			}
			return validateAPIStatus("api service", status)
		}},
	)
}

func validateAPIServiceSlug(slug string) error {
	if !apiSlugPattern.MatchString(slug) {
		return fmt.Errorf("api service slug must be lowercase URL-safe, got %q", slug)
	}
	return nil
}

func validateAPIServicePrice(price int64) error {
	if price < 0 {
		return fmt.Errorf("api service price_per_call must be >= 0, got %d", price)
	}
	return nil
}

func validateAPIStatus(subject string, status int64) error {
	if status != consts.StatusEnabled && status != consts.StatusDisabled {
		return fmt.Errorf("%s status must be enabled or disabled, got %d", subject, status)
	}
	return nil
}
