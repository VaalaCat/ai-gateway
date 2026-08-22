package api_catalog

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apiopenapi "github.com/VaalaCat/ai-gateway/internal/master/api/api_openapi"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type OpenAPIRequest struct {
	ServiceID uint `form:"service_id" binding:"required"`
	TokenID   uint `form:"token_id"`
}

// OpenAPIResponse deliberately contains only the standard exported document.
// The management document snapshot includes route and persistence details that
// do not belong to the authenticated catalog boundary.
type OpenAPIResponse struct {
	Document json.RawMessage `json:"document"`
}

func (h *Handler) OpenAPI(c *app.Context, req OpenAPIRequest) (OpenAPIResponse, error) {
	viewerScope := middleware.GetScope(c.Context)
	if viewerScope == nil {
		return OpenAPIResponse{}, catalogScopeAPIError(ErrCatalogAccessUnavailable)
	}
	viewer := CatalogViewer{UserID: viewerScope.UserID, IsAdmin: viewerScope.IsAdmin}
	snapshot, routeScoped, err := findCatalogOpenAPIDocument(c, viewer, req)
	if err != nil {
		return OpenAPIResponse{}, err
	}
	document, err := apiopenapi.BuildExport(snapshot, apiopenapi.GatewayServer(c, snapshot.Service.Slug))
	if err != nil {
		return OpenAPIResponse{}, api.InternalError("export API catalog OpenAPI document failed", err)
	}
	if routeScoped {
		document, err = retainReachableOpenAPIComponents(document)
		if err != nil {
			return OpenAPIResponse{}, api.InternalError("scope API catalog OpenAPI components failed", err)
		}
	}
	return OpenAPIResponse{Document: document}, nil
}

func findCatalogOpenAPIDocument(c *app.Context, viewer CatalogViewer, req OpenAPIRequest) (apiopenapi.DocumentSnapshot, bool, error) {
	var snapshot apiopenapi.DocumentSnapshot
	var routeScoped bool
	err := dao.RunInCoreTx[dao.Context](catalogDAOContext(c), func(tx dao.Context) error {
		scope, err := NewCatalogRequestScopeFinder(tx).findInTx(c.RequestContext(), viewer, req.TokenID)
		if err != nil {
			return catalogScopeAPIError(err)
		}
		if _, err := requireCatalogServiceInDAO(tx, scope, req.ServiceID); err != nil {
			return err
		}
		routeIDs, _ := scope.RouteIDsFor(req.ServiceID)
		routeScoped = routeIDs != nil
		found, err := (apiopenapi.OpenAPIDocumentFinder{}).FindVisibleInTx(tx, req.ServiceID, routeIDs)
		if err != nil {
			return api.InternalError("read API catalog OpenAPI document failed", err)
		}
		snapshot = found
		return nil
	})
	return snapshot, routeScoped, err
}
