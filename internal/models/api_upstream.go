package models

import (
	"fmt"
	"net/url"

	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type APIUpstreamAuthType string

const (
	APIUpstreamAuthNone   APIUpstreamAuthType = "none"
	APIUpstreamAuthBearer APIUpstreamAuthType = "bearer"
	APIUpstreamAuthHeader APIUpstreamAuthType = "header"
	APIUpstreamAuthQuery  APIUpstreamAuthType = "query"
	APIUpstreamAuthBasic  APIUpstreamAuthType = "basic"
)

// APIUpstream belongs to one APIBackend and can be shared by every route that
// selects that backend.
type APIUpstream struct {
	ID        uint                `gorm:"primaryKey" json:"id"`
	BackendID uint                `gorm:"not null;uniqueIndex:idx_api_upstream_backend_name" json:"backend_id"`
	Name      string              `gorm:"size:64;not null;uniqueIndex:idx_api_upstream_backend_name" json:"name"`
	BaseURL   string              `gorm:"size:2048;not null" json:"base_url"`
	Weight    int                 `gorm:"not null;default:1;check:chk_api_upstream_weight,weight > 0" json:"weight"`
	Priority  int                 `gorm:"not null;default:0;index" json:"priority"`
	AuthType  APIUpstreamAuthType `gorm:"size:16;not null;default:none;check:chk_api_upstream_auth_type,auth_type IN ('none','bearer','header','query','basic')" json:"auth_type"`

	// CredentialCiphertext and ProxyURLCiphertext are opaque encrypted bytes
	// represented as text. They must never be JSON encoded because encryption
	// framing and key rotation consume their exact stored representation.
	CredentialCiphertext string                                `gorm:"type:text" json:"credential_ciphertext"`
	ProxyURLCiphertext   string                                `gorm:"type:text" json:"proxy_url_ciphertext"`
	HeaderOverride       datatypes.JSONType[map[string]string] `gorm:"type:text;not null;default:'{}'" json:"header_override"`

	Status    int   `gorm:"not null;default:1" json:"status"`
	CreatedAt int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"autoUpdateTime" json:"updated_at"`
}

func (u *APIUpstream) Validate() error {
	if u.BackendID == 0 {
		return fmt.Errorf("api upstream backend_id must not be zero")
	}
	if u.Name == "" {
		return fmt.Errorf("api upstream name must not be empty")
	}
	if err := validateAPIUpstreamURL(u.BaseURL); err != nil {
		return err
	}
	if err := validateAPIUpstreamWeight(int64(u.Weight)); err != nil {
		return err
	}
	if err := validateAPIUpstreamAuthType(string(u.AuthType)); err != nil {
		return err
	}
	if err := validateAPIStatus("api upstream", int64(u.Status)); err != nil {
		return err
	}
	return nil
}

// BeforeCreate validates the complete upstream. DAO update paths must validate
// a loaded, patched object because a partial GORM update has no full receiver.
func (u *APIUpstream) BeforeCreate(*gorm.DB) error { return u.Validate() }

func (u *APIUpstream) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		if err := u.Validate(); err != nil {
			return err
		}
		return u.validatePersistedBackend(tx)
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "BackendID", validate: rejectAPIUpstreamBackendPartialUpdate},
		apiPatchField{name: "BaseURL", validate: func(value any) error {
			baseURL, err := apiPatchString(value, "api upstream base_url")
			if err != nil {
				return err
			}
			return validateAPIUpstreamURL(baseURL)
		}},
		apiPatchField{name: "Weight", validate: func(value any) error {
			weight, err := apiPatchInt(value, "api upstream weight")
			if err != nil {
				return err
			}
			return validateAPIUpstreamWeight(weight)
		}},
		apiPatchField{name: "AuthType", validate: func(value any) error {
			authType, err := apiPatchString(value, "api upstream auth_type")
			if err != nil {
				return err
			}
			return validateAPIUpstreamAuthType(authType)
		}},
		apiPatchField{name: "Status", validate: func(value any) error {
			status, err := apiPatchInt(value, "api upstream status")
			if err != nil {
				return err
			}
			return validateAPIStatus("api upstream", status)
		}},
	)
}

func (u *APIUpstream) validatePersistedBackend(tx *gorm.DB) error {
	var persisted APIUpstream
	if err := tx.Session(&gorm.Session{NewDB: true}).Select("backend_id").First(&persisted, u.ID).Error; err != nil {
		return err
	}
	if persisted.BackendID != u.BackendID {
		return fmt.Errorf("api upstream backend_id cannot be changed")
	}
	return nil
}

func rejectAPIUpstreamBackendPartialUpdate(any) error {
	return fmt.Errorf("partial api upstream backend_id updates are not supported")
}

func validateAPIUpstreamURL(baseURL string) error {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("api upstream base_url must be an absolute http/https URL without userinfo, got %q", baseURL)
	}
	return nil
}

func validateAPIUpstreamWeight(weight int64) error {
	if weight <= 0 {
		return fmt.Errorf("api upstream weight must be > 0, got %d", weight)
	}
	return nil
}

func validateAPIUpstreamAuthType(authType string) error {
	switch APIUpstreamAuthType(authType) {
	case APIUpstreamAuthNone, APIUpstreamAuthBearer, APIUpstreamAuthHeader, APIUpstreamAuthQuery, APIUpstreamAuthBasic:
		return nil
	default:
		return fmt.Errorf("api upstream auth_type is invalid: %q", authType)
	}
}
