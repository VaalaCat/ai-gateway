package api_openapi

import (
	"fmt"
	"sort"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/datatypes"
)

type OpenAPIDocumentFinder struct{}

type ServiceDocument struct {
	ID          uint                          `json:"id"`
	Slug        string                        `json:"slug"`
	Name        string                        `json:"name"`
	Description string                        `json:"description"`
	UpdatedAt   int64                         `json:"updated_at"`
	Document    models.OpenAPIServiceDocument `json:"document"`
}

type RouteDocument struct {
	ID           uint                              `json:"id"`
	Slug         string                            `json:"slug"`
	UpstreamPath string                            `json:"upstream_path"`
	UpdatedAt    int64                             `json:"updated_at"`
	Paths        map[string]models.OpenAPIPathItem `json:"paths"`
}

type DocumentSnapshot struct {
	Service ServiceDocument `json:"service"`
	Routes  []RouteDocument `json:"routes"`
}

func (OpenAPIDocumentFinder) Find(ctx dao.Context, serviceID uint) (DocumentSnapshot, error) {
	var snapshot DocumentSnapshot
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		var findErr error
		snapshot, findErr = findOpenAPIDocument(tx, serviceID)
		return findErr
	})
	return snapshot, err
}

// FindVisible returns a service document containing only enabled routes in the
// supplied authorization scope. A nil route scope retains every enabled route.
func (OpenAPIDocumentFinder) FindVisible(ctx dao.Context, serviceID uint, routeIDs *[]uint) (DocumentSnapshot, error) {
	var snapshot DocumentSnapshot
	err := dao.RunInCoreTx[dao.Context](ctx, func(tx dao.Context) error {
		var findErr error
		snapshot, findErr = (OpenAPIDocumentFinder{}).FindVisibleInTx(tx, serviceID, routeIDs)
		return findErr
	})
	return snapshot, err
}

// FindVisibleInTx composes the scoped document read into a caller-owned core
// read transaction so authorization and documents can share one snapshot.
func (OpenAPIDocumentFinder) FindVisibleInTx(tx dao.Context, serviceID uint, routeIDs *[]uint) (DocumentSnapshot, error) {
	return findVisibleOpenAPIDocument(tx, serviceID, routeIDs)
}

func findOpenAPIDocument(ctx dao.Context, serviceID uint) (DocumentSnapshot, error) {
	return findOpenAPIDocumentInScope(ctx, serviceID, nil, false)
}

func findVisibleOpenAPIDocument(ctx dao.Context, serviceID uint, routeIDs *[]uint) (DocumentSnapshot, error) {
	return findOpenAPIDocumentInScope(ctx, serviceID, routeIDs, true)
}

func findOpenAPIDocumentInScope(ctx dao.Context, serviceID uint, routeIDs *[]uint, visibleOnly bool) (DocumentSnapshot, error) {
	query := dao.NewAdminQuery(ctx)
	service, err := query.APIService().GetByID(serviceID)
	if err != nil {
		return DocumentSnapshot{}, err
	}
	document, serviceVersion, err := query.APIService().GetOpenAPIDocument(serviceID)
	if err != nil {
		return DocumentSnapshot{}, err
	}
	routeFilter := dao.APIRouteFilter{APIServiceID: &serviceID}
	if visibleOnly {
		routeFilter.Status = openAPIEnabledStatus()
		routeFilter.IDs = routeIDs
	}
	runtimeRoutes, _, err := query.APIRoute().List(dao.ListOptions{Page: 1, PageSize: int(^uint(0) >> 1)}, routeFilter)
	if err != nil {
		return DocumentSnapshot{}, err
	}
	documentRoutes, err := findDocumentRoutePaths(query.APIRoute(), serviceID, routeIDs, visibleOnly)
	if err != nil {
		return DocumentSnapshot{}, err
	}
	pathsByID := make(map[uint]map[string]models.OpenAPIPathItem, len(documentRoutes))
	for _, route := range documentRoutes {
		pathsByID[route.ID] = route.OpenAPIPaths.Data()
	}
	routes := make([]RouteDocument, 0, len(runtimeRoutes))
	for _, route := range runtimeRoutes {
		paths, exists := pathsByID[route.ID]
		if !exists {
			return DocumentSnapshot{}, fmt.Errorf("OpenAPI paths missing for route %d", route.ID)
		}
		routes = append(routes, RouteDocument{
			ID: route.ID, Slug: route.Slug, UpstreamPath: route.UpstreamPath,
			UpdatedAt: route.UpdatedAt, Paths: paths,
		})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].ID < routes[j].ID })
	return DocumentSnapshot{
		Service: ServiceDocument{
			ID: service.ID, Slug: service.Slug, Name: service.Name, Description: service.Description,
			UpdatedAt: serviceVersion, Document: document,
		},
		Routes: routes,
	}, nil
}

func findDocumentRoutePaths(query dao.APIRouteQuery, serviceID uint, routeIDs *[]uint, visibleOnly bool) ([]models.APIRoute, error) {
	if visibleOnly {
		return query.ListVisibleOpenAPIPaths(serviceID, routeIDs)
	}
	return query.ListOpenAPIPaths(serviceID, nil)
}

func openAPIEnabledStatus() *int {
	status := consts.StatusEnabled
	return &status
}

func snapshotServiceModel(snapshot DocumentSnapshot) models.APIService {
	return models.APIService{
		ID: snapshot.Service.ID, Slug: snapshot.Service.Slug, Name: snapshot.Service.Name,
		Description: snapshot.Service.Description, UpdatedAt: snapshot.Service.UpdatedAt,
		OpenAPIDocument: datatypes.NewJSONType(snapshot.Service.Document),
	}
}

func snapshotRouteModels(snapshot DocumentSnapshot) []models.APIRoute {
	routes := make([]models.APIRoute, 0, len(snapshot.Routes))
	for _, route := range snapshot.Routes {
		routes = append(routes, models.APIRoute{
			ID: route.ID, APIServiceID: snapshot.Service.ID, Slug: route.Slug,
			UpstreamPath: route.UpstreamPath, UpdatedAt: route.UpdatedAt,
			OpenAPIPaths: datatypes.NewJSONType(route.Paths),
		})
	}
	return routes
}
