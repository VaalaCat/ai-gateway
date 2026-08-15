package api_access_grant

import (
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

type PrincipalRef struct {
	Type models.APIPrincipalType
	ID   uint
}

type GrantScope string

const (
	GrantScopeService GrantScope = "service"
	GrantScopeRoutes  GrantScope = "routes"
)

type ReplaceGrantRequest struct {
	Scope    GrantScope `json:"scope" binding:"required"`
	RouteIDs []uint     `json:"route_ids"`
}

type ConfiguredGrant struct {
	PrincipalType models.APIPrincipalType `json:"principal_type"`
	PrincipalID   uint                    `json:"principal_id"`
	APIServiceID  uint                    `json:"api_service_id"`
	Scope         GrantScope              `json:"scope"`
	RouteIDs      []uint                  `json:"route_ids"`
}

type EffectiveAccess struct {
	Scope    GrantScope `json:"scope"`
	RouteIDs []uint     `json:"route_ids"`
}

type AccessSource string

const (
	AccessSourceManaged    AccessSource = "managed"
	AccessSourceCustomRole AccessSource = "custom_role"
	AccessSourceUserGroup  AccessSource = "user_group"
)

type AccessGrantResponse struct {
	PrincipalType  models.APIPrincipalType `json:"principal_type"`
	PrincipalID    uint                    `json:"principal_id"`
	PrincipalLabel string                  `json:"principal_label"`
	APIServiceID   uint                    `json:"api_service_id"`
	APIServiceName string                  `json:"api_service_name"`
	Configured     *ConfiguredGrant        `json:"configured,omitempty"`
	Effective      EffectiveAccess         `json:"effective"`
	Sources        []AccessSource          `json:"sources"`
}

type ListRequest struct {
	api.PaginationQuery
	PrincipalType *models.APIPrincipalType `form:"principal_type"`
	PrincipalID   *uint                    `form:"principal_id"`
	APIServiceID  *uint                    `form:"api_service_id"`
	Search        string                   `form:"search"`
}

type EffectiveRequest struct {
	PrincipalType models.APIPrincipalType `form:"principal_type" binding:"required"`
	PrincipalID   uint                    `form:"principal_id" binding:"required"`
	APIServiceID  uint                    `form:"api_service_id" binding:"required"`
}
