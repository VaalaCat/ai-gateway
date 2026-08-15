package api_catalog

import (
	"errors"
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apiaccessgrant "github.com/VaalaCat/ai-gateway/internal/master/api/api_access_grant"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

// Handler serves the authenticated user-facing API catalog. It deliberately
// projects safe response DTOs instead of returning management models.
type Handler struct{ App app.Application }

type ServiceListRequest struct {
	api.PaginationQuery
	Search  string `form:"search"`
	TokenID uint   `form:"token_id"`
}

type RouteListRequest struct {
	api.PaginationQuery
	ServiceID uint   `form:"service_id" binding:"required"`
	Search    string `form:"search"`
	TokenID   uint   `form:"token_id"`
}

type ServiceDetailRequest struct {
	ID      uint `form:"id" binding:"required"`
	TokenID uint `form:"token_id"`
}

type EffectiveRequest struct {
	ServiceID uint `form:"service_id" binding:"required"`
	TokenID   uint `form:"token_id"`
}

type ServiceResponse struct {
	ID          uint   `json:"id"`
	Slug        string `json:"slug"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RouteResponse struct {
	ID                    uint                     `json:"id"`
	APIServiceID          uint                     `json:"api_service_id"`
	Slug                  string                   `json:"slug"`
	Protocols             []models.APIProtocol     `json:"protocols"`
	AllowedMethods        []string                 `json:"allowed_methods"`
	WebSocketSubprotocols []string                 `json:"websocket_subprotocols"`
	ExampleRequest        models.APIRequestExample `json:"example_request"`
}

func (h *Handler) ListServices(c *app.Context, req ServiceListRequest) (api.PaginatedResponse[ServiceResponse], error) {
	scope, err := catalogScope(c, req.TokenID)
	if err != nil {
		return api.PaginatedResponse[ServiceResponse]{}, catalogScopeAPIError(err)
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	rows, total, err := catalogQuery(c).APIService().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		// behavior change: the selected Token's service scope bounds search,
		// count, and page selection in SQL.
		dao.APIServiceFilter{Search: req.Search, Status: enabledStatus(), IDs: scope.ServiceIDs()},
	)
	if err != nil {
		return api.PaginatedResponse[ServiceResponse]{}, api.InternalError("list API catalog services failed", err)
	}
	return api.PaginatedResponse[ServiceResponse]{Data: projectServices(rows), Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) ServiceDetail(c *app.Context, req ServiceDetailRequest) (ServiceResponse, error) {
	scope, err := catalogScope(c, req.TokenID)
	if err != nil {
		return ServiceResponse{}, catalogScopeAPIError(err)
	}
	service, err := requireCatalogService(c, scope, req.ID)
	if err != nil {
		return ServiceResponse{}, err
	}
	return projectService(*service), nil
}

func (h *Handler) ListRoutes(c *app.Context, req RouteListRequest) (api.PaginatedResponse[RouteResponse], error) {
	scope, err := catalogScope(c, req.TokenID)
	if err != nil {
		return api.PaginatedResponse[RouteResponse]{}, catalogScopeAPIError(err)
	}
	routeIDs, visible := scope.RouteIDsFor(req.ServiceID)
	if !visible {
		return api.PaginatedResponse[RouteResponse]{}, catalogResourceNotFound()
	}
	if _, err := requireCatalogService(c, scope, req.ServiceID); err != nil {
		return api.PaginatedResponse[RouteResponse]{}, err
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	rows, total, err := catalogQuery(c).APIRoute().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		// behavior change: a route-only scope bounds search, count, and page
		// selection in SQL; nil preserves a service-wide or admin scope.
		dao.APIRouteFilter{APIServiceID: &req.ServiceID, Search: req.Search, Status: enabledStatus(), IDs: routeIDs},
	)
	if err != nil {
		return api.PaginatedResponse[RouteResponse]{}, api.InternalError("list API catalog routes failed", err)
	}
	return api.PaginatedResponse[RouteResponse]{Data: projectRoutes(rows), Total: total, Page: page, PageSize: pageSize}, nil
}

func (h *Handler) Effective(c *app.Context, req EffectiveRequest) (apiaccessgrant.EffectiveAccess, error) {
	scope, err := catalogScope(c, req.TokenID)
	if err != nil {
		return apiaccessgrant.EffectiveAccess{}, catalogScopeAPIError(err)
	}
	if _, err := requireCatalogService(c, scope, req.ServiceID); err != nil {
		return apiaccessgrant.EffectiveAccess{}, err
	}
	return catalogEffectiveAccess(scope, req.ServiceID), nil
}

func catalogEffectiveAccess(scope CatalogRequestScope, serviceID uint) apiaccessgrant.EffectiveAccess {
	if scope.AdminAll || containsCatalogID(scope.ServiceWideIDs, serviceID) {
		return apiaccessgrant.EffectiveAccess{Scope: apiaccessgrant.GrantScopeService, RouteIDs: []uint{}}
	}
	routeIDs, _ := scope.RouteIDsFor(serviceID)
	return apiaccessgrant.EffectiveAccess{
		Scope:    apiaccessgrant.GrantScopeRoutes,
		RouteIDs: append([]uint{}, (*routeIDs)...),
	}
}

func catalogQuery(c *app.Context) dao.AdminQuery {
	return dao.NewAdminQuery(catalogDAOContext(c))
}

func catalogDAOContext(c *app.Context) dao.Context {
	return dao.NewContextWithContext(c.App, c.RequestContext())
}

func catalogScope(c *app.Context, tokenID uint) (CatalogRequestScope, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return CatalogRequestScope{}, ErrCatalogAccessUnavailable
	}
	viewer := CatalogViewer{UserID: scope.UserID, IsAdmin: scope.IsAdmin}
	return NewCatalogRequestScopeFinder(catalogDAOContext(c)).Find(c.RequestContext(), viewer, tokenID)
}

func requireCatalogService(c *app.Context, scope CatalogRequestScope, serviceID uint) (*models.APIService, error) {
	if _, visible := scope.RouteIDsFor(serviceID); !visible {
		return nil, catalogResourceNotFound()
	}
	service, err := catalogQuery(c).APIService().GetByID(serviceID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && service != nil && service.Status != consts.StatusEnabled) {
		return nil, catalogResourceNotFound()
	}
	if err != nil || service == nil {
		return nil, catalogScopeAPIError(ErrCatalogAccessUnavailable)
	}
	return service, nil
}

func catalogScopeAPIError(err error) error {
	switch {
	case errors.Is(err, ErrCatalogTokenRequired):
		return api.ErrorWithCode(http.StatusBadRequest, "token_required", ErrCatalogTokenRequired.Error(), nil)
	case errors.Is(err, ErrCatalogTokenUnavailable):
		return api.ErrorWithCode(http.StatusNotFound, "token_not_available", ErrCatalogTokenUnavailable.Error(), nil)
	default:
		return api.ErrorWithCode(http.StatusServiceUnavailable, "catalog_access_unavailable", ErrCatalogAccessUnavailable.Error(), nil)
	}
}

func catalogResourceNotFound() error {
	return api.ErrorWithCode(http.StatusNotFound, "resource_not_found", consts.ErrNotFound, nil)
}

func enabledStatus() *int {
	status := consts.StatusEnabled
	return &status
}

func projectServices(rows []models.APIService) []ServiceResponse {
	result := make([]ServiceResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, projectService(row))
	}
	return result
}

func projectService(row models.APIService) ServiceResponse {
	return ServiceResponse{ID: row.ID, Slug: row.Slug, Name: row.Name, Description: row.Description}
}

func projectRoutes(rows []models.APIRoute) []RouteResponse {
	result := make([]RouteResponse, 0, len(rows))
	for _, row := range rows {
		result = append(result, RouteResponse{
			ID: row.ID, APIServiceID: row.APIServiceID, Slug: row.Slug,
			Protocols: row.Protocols, AllowedMethods: row.AllowedMethods, WebSocketSubprotocols: row.WebSocketSubprotocols,
			ExampleRequest: cloneRequestExample(row.ExampleRequest.Data()),
		})
	}
	return result
}

func cloneRequestExample(example models.APIRequestExample) models.APIRequestExample {
	headers := make(map[string]string, len(example.Headers))
	for key, value := range example.Headers {
		headers[key] = value
	}
	example.Headers = headers
	return example
}
