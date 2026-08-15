package api_role

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type ListRequest struct {
	api.PaginationQuery
	Search     string `form:"search"`
	Status     *int   `form:"status"`
	Assignable *bool  `form:"assignable"`
}

type ListBindingRequest struct {
	api.PaginationQuery
	PrincipalType *models.APIPrincipalType `form:"principal_type"`
	PrincipalID   *uint                    `form:"principal_id"`
	RoleID        *uint                    `form:"role_id"`
}

type RoleResponse struct {
	models.Role
	Permissions []models.Permission `json:"permissions"`
	Members     []RoleMemberRequest `json:"members"`
}

type RoleMemberRequest struct {
	PrincipalType models.APIPrincipalType `json:"principal_type" binding:"required"`
	PrincipalID   uint                    `json:"principal_id" binding:"required"`
}

type PermissionRequest struct {
	Resource   models.APIResource         `json:"resource" binding:"required"`
	ResourceID uint                       `json:"resource_id"`
	Action     models.APIPermissionAction `json:"action" binding:"required"`
}

type CreateRequest struct {
	Key         string              `json:"key" binding:"required,max=64"`
	Name        string              `json:"name" binding:"required,max=128"`
	Description string              `json:"description"`
	Status      *int                `json:"status"`
	Permissions []PermissionRequest `json:"permissions"`
	Members     []RoleMemberRequest `json:"members"`
}

type UpdateRequest struct {
	ID          string              `uri:"id" binding:"required"`
	Key         string              `json:"key" binding:"required,max=64"`
	Name        string              `json:"name" binding:"required,max=128"`
	Description string              `json:"description"`
	Status      *int                `json:"status"`
	Permissions []PermissionRequest `json:"permissions"`
	Members     []RoleMemberRequest `json:"members"`
}

type IDRequest struct {
	ID string `uri:"id" binding:"required"`
}

type CreateBindingRequest struct {
	PrincipalType models.APIPrincipalType `json:"principal_type" binding:"required"`
	PrincipalID   uint                    `json:"principal_id"`
	RoleID        uint                    `json:"role_id"`
}

type UpdateBindingRequest struct {
	ID            string                  `uri:"id" binding:"required"`
	PrincipalType models.APIPrincipalType `json:"principal_type" binding:"required"`
	PrincipalID   uint                    `json:"principal_id"`
	RoleID        uint                    `json:"role_id"`
}
