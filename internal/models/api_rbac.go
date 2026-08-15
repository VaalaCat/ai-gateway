package models

import (
	"fmt"

	"gorm.io/gorm"
)

type APIRoleMode string

const (
	APIRoleModeInherit  APIRoleMode = "inherit"
	APIRoleModeExplicit APIRoleMode = "explicit"
	GatewayAdminRoleKey             = "gateway_admin"
)

type APIRoleKind string

const (
	APIRoleKindCustom  APIRoleKind = "custom"
	APIRoleKindManaged APIRoleKind = "managed"
)

type APIResource string

const (
	APIResourceService APIResource = "api_service"
	APIResourceRoute   APIResource = "api_route"
)

type APIPermissionAction string

const (
	APIPermissionInvoke APIPermissionAction = "invoke"
)

type APIPrincipalType string

const (
	APIPrincipalUser      APIPrincipalType = "user"
	APIPrincipalUserGroup APIPrincipalType = "user_group"
	APIPrincipalToken     APIPrincipalType = "token"
)

type Role struct {
	ID          uint        `gorm:"primaryKey" json:"id"`
	Key         string      `gorm:"uniqueIndex;size:64;not null" json:"key"`
	Name        string      `gorm:"size:128;not null" json:"name"`
	Description string      `gorm:"type:text" json:"description"`
	Kind        APIRoleKind `gorm:"size:16;not null;default:custom;index;check:chk_api_role_kind,kind IN ('custom','managed')" json:"kind"`
	BuiltIn     bool        `gorm:"not null;default:false" json:"built_in"`
	Status      int         `gorm:"not null;default:1" json:"status"`
	CreatedAt   int64       `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   int64       `gorm:"autoUpdateTime" json:"updated_at"`
}

type Permission struct {
	ID         uint                `gorm:"primaryKey" json:"id"`
	Resource   APIResource         `gorm:"size:32;not null;uniqueIndex:idx_api_permission_resource_target_action;check:chk_api_permission_resource,resource IN ('api_service','api_route')" json:"resource"`
	ResourceID uint                `gorm:"not null;uniqueIndex:idx_api_permission_resource_target_action;check:chk_api_permission_route_scope,resource <> 'api_route' OR resource_id > 0" json:"resource_id"`
	Action     APIPermissionAction `gorm:"size:16;not null;uniqueIndex:idx_api_permission_resource_target_action;check:chk_api_permission_resource_action,action = 'invoke'" json:"action"`
	CreatedAt  int64               `gorm:"autoCreateTime" json:"created_at"`
}

type RolePermission struct {
	ID           uint  `gorm:"primaryKey" json:"id"`
	RoleID       uint  `gorm:"not null;uniqueIndex:idx_api_role_permission" json:"role_id"`
	PermissionID uint  `gorm:"not null;uniqueIndex:idx_api_role_permission" json:"permission_id"`
	CreatedAt    int64 `gorm:"autoCreateTime" json:"created_at"`
}

type RoleBinding struct {
	ID            uint             `gorm:"primaryKey" json:"id"`
	PrincipalType APIPrincipalType `gorm:"size:16;not null;uniqueIndex:idx_api_role_binding_principal_role;check:chk_api_role_binding_principal_type,principal_type IN ('user','user_group','token')" json:"principal_type"`
	PrincipalID   uint             `gorm:"not null;uniqueIndex:idx_api_role_binding_principal_role" json:"principal_id"`
	RoleID        uint             `gorm:"not null;uniqueIndex:idx_api_role_binding_principal_role" json:"role_id"`
	CreatedAt     int64            `gorm:"autoCreateTime" json:"created_at"`
}

func (r *Role) Validate() error {
	if r.Kind == "" {
		r.Kind = APIRoleKindCustom
	}
	if r.Kind != APIRoleKindCustom && r.Kind != APIRoleKindManaged {
		return fmt.Errorf("api role kind is invalid: %q", r.Kind)
	}
	if r.Key == "" {
		return fmt.Errorf("role key must not be empty")
	}
	if r.Name == "" {
		return fmt.Errorf("role name must not be empty")
	}
	if err := validateAPIStatus("role", int64(r.Status)); err != nil {
		return err
	}
	return nil
}

func (p *Permission) Validate() error {
	actions, ok := validAPIPermissionActions[p.Resource]
	if !ok {
		return fmt.Errorf("api permission resource is invalid: %q", p.Resource)
	}
	if _, ok := actions[p.Action]; !ok {
		return fmt.Errorf("api permission action %q is invalid for resource %q", p.Action, p.Resource)
	}
	if p.Resource == APIResourceRoute && p.ResourceID == 0 {
		return fmt.Errorf("api route permission resource_id must be greater than zero")
	}
	return nil
}

var validAPIPermissionActions = map[APIResource]map[APIPermissionAction]struct{}{
	APIResourceService: {APIPermissionInvoke: {}},
	APIResourceRoute:   {APIPermissionInvoke: {}},
}

func ManagedAPIRoleKey(principalType APIPrincipalType, principalID uint) string {
	return fmt.Sprintf("managed:%s:%d", principalType, principalID)
}

func (b *RoleBinding) Validate() error {
	switch b.PrincipalType {
	case APIPrincipalUser, APIPrincipalUserGroup, APIPrincipalToken:
	default:
		return fmt.Errorf("api role binding principal_type is invalid: %q", b.PrincipalType)
	}
	if b.PrincipalID == 0 {
		return fmt.Errorf("api role binding principal_id must not be zero")
	}
	if b.RoleID == 0 {
		return fmt.Errorf("api role binding role_id must not be zero")
	}
	return nil
}

// Creation receives complete objects. Partial update receivers do not, so DAO
// code validates an explicitly loaded and patched model before updating it.
func (r *Role) BeforeCreate(*gorm.DB) error        { return r.Validate() }
func (p *Permission) BeforeCreate(*gorm.DB) error  { return p.Validate() }
func (b *RoleBinding) BeforeCreate(*gorm.DB) error { return b.Validate() }

func (r *Role) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		return r.Validate()
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "Key", validate: nonEmptyRoleKey},
		apiPatchField{name: "Name", validate: nonEmptyRoleName},
		apiPatchField{name: "Kind", validate: validateAPIRoleKind},
		apiPatchField{name: "Status", validate: func(value any) error {
			status, err := apiPatchInt(value, "role status")
			if err != nil {
				return err
			}
			return validateAPIStatus("role", status)
		}},
	)
}

func (p *Permission) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		return p.Validate()
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "Resource", validate: validateAPIPermissionResource},
		apiPatchField{name: "Action", validate: validateAPIPermissionAction},
	)
}

func (b *RoleBinding) BeforeUpdate(tx *gorm.DB) error {
	if apiFullObjectUpdate(tx) {
		return b.Validate()
	}
	return apiValidatePatch(tx,
		apiPatchField{name: "PrincipalType", validate: validateAPIRoleBindingPrincipalType},
		apiPatchField{name: "PrincipalID", validate: validateAPIRoleBindingNonZero("principal_id")},
		apiPatchField{name: "RoleID", validate: validateAPIRoleBindingNonZero("role_id")},
	)
}

func nonEmptyRoleKey(value any) error {
	key, err := apiPatchString(value, "role key")
	if err != nil {
		return err
	}
	if key == "" {
		return fmt.Errorf("role key must not be empty")
	}
	return nil
}

func nonEmptyRoleName(value any) error {
	name, err := apiPatchString(value, "role name")
	if err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("role name must not be empty")
	}
	return nil
}

func validateAPIPermissionResource(value any) error {
	resource, err := apiPatchString(value, "api permission resource")
	if err != nil {
		return err
	}
	if _, ok := validAPIPermissionActions[APIResource(resource)]; !ok {
		return fmt.Errorf("api permission resource is invalid: %q", resource)
	}
	return nil
}

func validateAPIPermissionAction(value any) error {
	action, err := apiPatchString(value, "api permission action")
	if err != nil {
		return err
	}
	for _, actions := range validAPIPermissionActions {
		if _, ok := actions[APIPermissionAction(action)]; ok {
			return nil
		}
	}
	return fmt.Errorf("api permission action is invalid: %q", action)
}

func validateAPIRoleKind(value any) error {
	kind, err := apiPatchString(value, "api role kind")
	if err != nil {
		return err
	}
	if APIRoleKind(kind) != APIRoleKindCustom && APIRoleKind(kind) != APIRoleKindManaged {
		return fmt.Errorf("api role kind is invalid: %q", kind)
	}
	return nil
}

func validateAPIRoleBindingPrincipalType(value any) error {
	principalType, err := apiPatchString(value, "api role binding principal_type")
	if err != nil {
		return err
	}
	switch APIPrincipalType(principalType) {
	case APIPrincipalUser, APIPrincipalUserGroup, APIPrincipalToken:
		return nil
	default:
		return fmt.Errorf("api role binding principal_type is invalid: %q", principalType)
	}
}

func validateAPIRoleBindingNonZero(field string) func(any) error {
	return func(value any) error {
		id, err := apiPatchInt(value, "api role binding "+field)
		if err != nil {
			return err
		}
		if id <= 0 {
			return fmt.Errorf("api role binding %s must not be zero", field)
		}
		return nil
	}
}
