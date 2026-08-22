package api_openapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	coreopenapi "github.com/VaalaCat/ai-gateway/internal/pkg/apiopenapi"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type IDRequest struct {
	ID uint `uri:"id" binding:"required"`
}

type ServiceDocumentUpdate struct {
	ID        uint                          `json:"id" binding:"required"`
	UpdatedAt int64                         `json:"updated_at" binding:"required"`
	Document  models.OpenAPIServiceDocument `json:"document" binding:"required"`
}

type RouteDocumentUpdate struct {
	ID        uint                              `json:"id" binding:"required"`
	UpdatedAt int64                             `json:"updated_at" binding:"required"`
	Paths     map[string]models.OpenAPIPathItem `json:"paths" binding:"required"`
}

type UpdateRequest struct {
	ID      uint                  `uri:"id" json:"-" binding:"required"`
	Service ServiceDocumentUpdate `json:"service" binding:"required"`
	Routes  []RouteDocumentUpdate `json:"routes" binding:"required"`
}

type DocumentResponse struct {
	DocumentSnapshot
	Export json.RawMessage `json:"export"`
}

func (h *Handler) Get(c *app.Context, req IDRequest) (DocumentResponse, error) {
	snapshot, err := h.Finder.Find(dao.NewContextWithContext(c.App, c.RequestContext()), req.ID)
	if err != nil {
		return DocumentResponse{}, documentReadError(err)
	}
	return buildDocumentResponse(snapshot, gatewayServer(c, snapshot.Service.Slug))
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (DocumentResponse, error) {
	if req.ID == 0 || req.Service.ID != req.ID {
		return DocumentResponse{}, api.ErrorWithCode(http.StatusBadRequest, "service_id_mismatch", "OpenAPI service id does not match request path", nil)
	}
	ctx := dao.NewContextWithContext(c.App, c.RequestContext())
	if err := updateOpenAPIDocuments(ctx, req, gatewayBase(c)); err != nil {
		return DocumentResponse{}, documentUpdateError(err)
	}
	snapshot, err := h.Finder.Find(ctx, req.ID)
	if err != nil {
		return DocumentResponse{}, documentReadError(err)
	}
	return buildDocumentResponse(snapshot, gatewayServer(c, snapshot.Service.Slug))
}

func buildDocumentResponse(snapshot DocumentSnapshot, server string) (DocumentResponse, error) {
	raw, err := BuildExport(snapshot, server)
	if err != nil {
		return DocumentResponse{}, api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_document", "platform OpenAPI document cannot be exported", nil)
	}
	return DocumentResponse{DocumentSnapshot: snapshot, Export: raw}, nil
}

// BuildExport produces the standard public OpenAPI representation for a
// document snapshot without exposing management-only route fields.
func BuildExport(snapshot DocumentSnapshot, server string) ([]byte, error) {
	return coreopenapi.BuildDocument(snapshotServiceModel(snapshot), snapshotRouteModels(snapshot), server)
}

// GatewayServer returns the public gateway URL used by exported documents.
func GatewayServer(c *app.Context, slug string) string {
	return gatewayServer(c, slug)
}

func updateOpenAPIDocuments(ctx dao.Context, req UpdateRequest, gatewayBaseURL string) error {
	return runOpenAPIDocumentWriteTx(ctx, func(tx dao.Context) error {
		service, err := lockUpdatedService(tx, req)
		if err != nil {
			return err
		}
		routes, err := lockUpdatedRoutes(tx, req)
		if err != nil {
			return err
		}
		if err := requireCompleteRouteSet(tx, req.ID, routes); err != nil {
			return err
		}
		if _, err := coreopenapi.BuildDocument(*service, routes, gatewayServerFromBase(gatewayBaseURL, service.Slug)); err != nil {
			return api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_update", "OpenAPI document update is inconsistent", nil)
		}
		return persistOpenAPIDocuments(tx, service, routes)
	})
}

func lockUpdatedService(ctx dao.Context, req UpdateRequest) (*models.APIService, error) {
	service, err := dao.NewAdminQuery(ctx).APIService().LockByID(req.ID)
	if err != nil {
		return nil, err
	}
	if service.UpdatedAt != req.Service.UpdatedAt {
		return nil, versionConflict("service", service.ID)
	}
	service.OpenAPIDocument = datatypes.NewJSONType(req.Service.Document)
	if err := service.NormalizeForWrite(); err != nil {
		return nil, api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_update", "OpenAPI service document is invalid", nil)
	}
	if err := service.Validate(); err != nil {
		return nil, err
	}
	return service, nil
}

func lockUpdatedRoutes(ctx dao.Context, req UpdateRequest) ([]models.APIRoute, error) {
	updates := append([]RouteDocumentUpdate(nil), req.Routes...)
	sort.Slice(updates, func(i, j int) bool { return updates[i].ID < updates[j].ID })
	seen := make(map[uint]struct{}, len(updates))
	routes := make([]models.APIRoute, 0, len(updates))
	for _, update := range updates {
		if update.ID == 0 {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_update", "route id must not be zero", nil)
		}
		if _, duplicate := seen[update.ID]; duplicate {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_update", "route appears more than once", nil)
		}
		seen[update.ID] = struct{}{}
		route, err := dao.NewAdminQuery(ctx).APIRoute().LockByID(update.ID)
		if err != nil {
			return nil, err
		}
		if route.APIServiceID != req.ID {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "route_service_mismatch", "route does not belong to the API service", map[string]any{"route_id": update.ID})
		}
		if route.UpdatedAt != update.UpdatedAt {
			return nil, versionConflict("route", route.ID)
		}
		route.OpenAPIPaths = datatypes.NewJSONType(update.Paths)
		if err := route.NormalizeForWrite(); err != nil {
			return nil, api.ErrorWithCode(http.StatusBadRequest, "invalid_openapi_update", "OpenAPI route document is invalid", map[string]any{"route_id": route.ID})
		}
		if err := route.Validate(); err != nil {
			return nil, err
		}
		routes = append(routes, *route)
	}
	return routes, nil
}

func requireCompleteRouteSet(ctx dao.Context, serviceID uint, routes []models.APIRoute) error {
	existing, _, err := dao.NewAdminQuery(ctx).APIRoute().List(
		dao.ListOptions{Page: 1, PageSize: int(^uint(0) >> 1)}, dao.APIRouteFilter{APIServiceID: &serviceID},
	)
	if err != nil {
		return err
	}
	if len(existing) != len(routes) {
		return api.ErrorWithCode(http.StatusBadRequest, "route_set_mismatch", "OpenAPI update must include every route", nil)
	}
	ids := make(map[uint]struct{}, len(routes))
	for _, route := range routes {
		ids[route.ID] = struct{}{}
	}
	for _, route := range existing {
		if _, included := ids[route.ID]; !included {
			return api.ErrorWithCode(http.StatusBadRequest, "route_set_mismatch", "OpenAPI update must include every route", nil)
		}
	}
	return nil
}

func persistOpenAPIDocuments(ctx dao.Context, service *models.APIService, routes []models.APIRoute) error {
	expectedServiceVersion := service.UpdatedAt
	service.UpdatedAt = nextDocumentVersion(service.UpdatedAt)
	result := ctx.GetCoreDB().Model(&models.APIService{}).Where("id = ? AND updated_at = ?", service.ID, expectedServiceVersion).UpdateColumns(map[string]any{
		"openapi_document": service.OpenAPIDocument, "updated_at": service.UpdatedAt,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return versionConflict("service", service.ID)
	}
	for index := range routes {
		expectedRouteVersion := routes[index].UpdatedAt
		routes[index].UpdatedAt = nextDocumentVersion(routes[index].UpdatedAt)
		result = ctx.GetCoreDB().Model(&models.APIRoute{}).Where("id = ? AND updated_at = ?", routes[index].ID, expectedRouteVersion).UpdateColumns(map[string]any{
			"openapi_paths": routes[index].OpenAPIPaths, "updated_at": routes[index].UpdatedAt,
		})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return versionConflict("route", routes[index].ID)
		}
	}
	return nil
}

func runOpenAPIDocumentWriteTx(ctx dao.Context, fn func(dao.Context) error) error {
	db := ctx.GetCoreDB()
	if db.Dialector.Name() != "sqlite" {
		return dao.RunInCoreTx[dao.Context](ctx, fn)
	}
	return db.Connection(func(connection *gorm.DB) error {
		if err := connection.Exec("BEGIN IMMEDIATE").Error; err != nil {
			return err
		}
		txDB := connection.Session(&gorm.Session{NewDB: true, SkipDefaultTransaction: true})
		finished := false
		defer func() {
			if !finished {
				_ = connection.Exec("ROLLBACK").Error
			}
		}()
		if err := fn(ctx.WithCoreTx(txDB)); err != nil {
			return err
		}
		if err := connection.Exec("COMMIT").Error; err != nil {
			return err
		}
		finished = true
		return nil
	})
}

func nextDocumentVersion(current int64) int64 {
	now := time.Now().Unix()
	if now <= current {
		return current + 1
	}
	return now
}

func versionConflict(resource string, id uint) error {
	return api.ErrorWithCode(http.StatusConflict, "openapi_version_conflict", "OpenAPI document was changed by another request", map[string]any{
		"resource": resource, "id": id,
	})
}

func documentReadError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError("API service not found")
	}
	return api.InternalError("load OpenAPI document failed", err)
}

func documentUpdateError(err error) error {
	var apiErr *api.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return api.NotFoundError("OpenAPI resource not found")
	}
	return api.InternalError("update OpenAPI document failed", err)
}

func gatewayServer(c *app.Context, slug string) string {
	return gatewayServerFromBase(gatewayBase(c), slug)
}

func gatewayBase(c *app.Context) string {
	base := ""
	if c != nil && c.Settings != nil && len(c.Settings.Master.PublicBaseURLs) > 0 {
		base = c.Settings.Master.PublicBaseURLs[0]
	}
	if base == "" && c != nil && c.Request != nil {
		scheme := c.Request.URL.Scheme
		if scheme == "" {
			if c.Request.TLS != nil {
				scheme = "https"
			} else {
				scheme = "http"
			}
		}
		base = scheme + "://" + c.Request.Host
	}
	return strings.TrimRight(base, "/")
}

func gatewayServerFromBase(base, slug string) string {
	return strings.TrimRight(base, "/") + "/v1/api/" + slug
}
