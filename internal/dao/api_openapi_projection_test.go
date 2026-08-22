package dao

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestAPIServiceOpenAPIProjectionSeparatesRuntimeAndDocumentQueries(t *testing.T) {
	// Loading this document through normal list/sync paths would add roughly
	// 1 MiB per row to hot-path allocations and wire preparation.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	document := largeOpenAPIServiceDocument(t)
	service := models.APIService{
		Slug:            "projection-service",
		Name:            "Projection Service",
		OpenAPIDocument: datatypes.NewJSONType(document),
	}
	require.NoError(t, core.Create(&service).Error)
	require.NoError(t, core.Model(&models.APIService{}).Where("id = ?", service.ID).UpdateColumn("updated_at", int64(1_777_777_777)).Error)

	query := NewAdminQuery(ctx).APIService()
	getByID, err := query.GetByID(service.ID)
	require.NoError(t, err)
	require.Equal(t, models.OpenAPIServiceDocument{}, getByID.OpenAPIDocument.Data())
	getBySlug, err := query.GetBySlug(service.Slug)
	require.NoError(t, err)
	require.Equal(t, models.OpenAPIServiceDocument{}, getBySlug.OpenAPIDocument.Data())
	locked, err := query.LockByID(service.ID)
	require.NoError(t, err)
	require.Equal(t, models.OpenAPIServiceDocument{}, locked.OpenAPIDocument.Data())
	listed, total, err := query.List(ListOptions{Page: 1, PageSize: 10}, APIServiceFilter{})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, listed, 1)
	require.Equal(t, models.OpenAPIServiceDocument{}, listed[0].OpenAPIDocument.Data())
	keyset, err := query.ListKeyset(0, service.ID, 10)
	require.NoError(t, err)
	require.Len(t, keyset, 1)
	require.Equal(t, models.OpenAPIServiceDocument{}, keyset[0].OpenAPIDocument.Data())

	loadedDocument, updatedAt, err := query.GetOpenAPIDocument(service.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1_777_777_777), updatedAt)
	require.Equal(t, document.Version, loadedDocument.Version)
	require.Equal(t, document.Components.Schemas["LargePayload"], loadedDocument.Components.Schemas["LargePayload"])

	err = NewAdminMutation(ctx).APIService().Update(service.ID, map[string]any{"name": "Renamed"})
	require.NoError(t, err)
	loadedDocument, _, err = query.GetOpenAPIDocument(service.ID)
	require.NoError(t, err)
	require.Equal(t, document.Components.Schemas["LargePayload"], loadedDocument.Components.Schemas["LargePayload"])
}

func TestAPIRouteOpenAPIProjectionScopesFinderAndPreservesDocuments(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "projection-routes", Name: "Projection Routes"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	paths := largeOpenAPIPaths(t)
	route := models.APIRoute{
		APIServiceID: service.ID,
		BackendID:    backend.ID,
		Slug:         "users",
		OpenAPIPaths: datatypes.NewJSONType(paths),
	}
	require.NoError(t, core.Create(&route).Error)

	otherService := models.APIService{Slug: "other-projection-routes", Name: "Other Projection Routes"}
	require.NoError(t, core.Create(&otherService).Error)
	otherBackend := models.APIBackend{APIServiceID: otherService.ID, Name: "primary"}
	require.NoError(t, core.Create(&otherBackend).Error)
	otherRoute := models.APIRoute{APIServiceID: otherService.ID, BackendID: otherBackend.ID, Slug: "other", OpenAPIPaths: datatypes.NewJSONType(paths)}
	require.NoError(t, core.Create(&otherRoute).Error)

	query := NewAdminQuery(ctx).APIRoute()
	getByID, err := query.GetByID(route.ID)
	require.NoError(t, err)
	require.Empty(t, getByID.OpenAPIPaths.Data())
	getBySlug, err := query.GetByServiceAndSlug(service.ID, route.Slug)
	require.NoError(t, err)
	require.Empty(t, getBySlug.OpenAPIPaths.Data())
	locked, err := query.LockByID(route.ID)
	require.NoError(t, err)
	require.Empty(t, locked.OpenAPIPaths.Data())
	listed, total, err := query.List(ListOptions{Page: 1, PageSize: 10}, APIRouteFilter{APIServiceID: &service.ID})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, listed, 1)
	require.Empty(t, listed[0].OpenAPIPaths.Data())
	keyset, err := query.ListKeyset(0, otherRoute.ID, 10)
	require.NoError(t, err)
	require.Len(t, keyset, 2)
	for _, row := range keyset {
		require.Empty(t, row.OpenAPIPaths.Data())
	}

	allForService, err := query.ListOpenAPIPaths(service.ID, nil)
	require.NoError(t, err)
	require.Len(t, allForService, 1)
	require.Equal(t, route.ID, allForService[0].ID)
	require.Equal(t, "getUser", allForService[0].OpenAPIPaths.Data()["/users/{id}"].Operations["GET"].OperationID)

	IDs := []uint{route.ID, otherRoute.ID}
	scoped, err := query.ListOpenAPIPaths(service.ID, &IDs)
	require.NoError(t, err)
	require.Len(t, scoped, 1, "service ownership must be enforced even when another route ID is supplied")
	require.Equal(t, route.ID, scoped[0].ID)
	emptyIDs := []uint{}
	empty, err := query.ListOpenAPIPaths(service.ID, &emptyIDs)
	require.NoError(t, err)
	require.Empty(t, empty, "an explicit empty scope must not fall back to all routes")
	allVisible, err := query.ListVisibleOpenAPIPaths(service.ID, nil)
	require.NoError(t, err)
	require.Len(t, allVisible, 1, "nil visible scope must retain every enabled route")
	emptyVisible, err := query.ListVisibleOpenAPIPaths(service.ID, &emptyIDs)
	require.NoError(t, err)
	require.Empty(t, emptyVisible, "an explicit empty visible scope must retain zero routes")

	require.NoError(t, NewAdminMutation(ctx).APIRoute().Update(route.ID, map[string]any{"upstream_path": "/v2/users"}))
	preserved, err := query.ListOpenAPIPaths(service.ID, &IDs)
	require.NoError(t, err)
	require.Len(t, preserved, 1)
	require.Equal(t, "getUser", preserved[0].OpenAPIPaths.Data()["/users/{id}"].Operations["GET"].OperationID)
}

func TestAPIServiceOpenAPIDocumentFinderReportsMissingAndMalformedRows(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	query := NewAdminQuery(ctx).APIService()

	_, _, err := query.GetOpenAPIDocument(999_999)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)

	service := models.APIService{Slug: "malformed-document", Name: "Malformed Document"}
	require.NoError(t, core.Create(&service).Error)
	require.NoError(t, core.Exec("UPDATE api_services SET openapi_document = ? WHERE id = ?", "{", service.ID).Error)
	_, _, err = query.GetOpenAPIDocument(service.ID)
	require.Error(t, err)
	require.False(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestAPIServiceOpenAPIDocumentFinderRejectsZeroID(t *testing.T) {
	// A zero ID is outside the finder contract. The explicit guard keeps that
	// boundary independent of ORM inline-primary-key behavior changes.
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{
		Slug: "zero-id-boundary",
		Name: "Zero ID Boundary",
		OpenAPIDocument: datatypes.NewJSONType(models.OpenAPIServiceDocument{
			Version: "3.1.0",
		}),
	}
	require.NoError(t, core.Create(&service).Error)

	document, updatedAt, err := NewAdminQuery(ctx).APIService().GetOpenAPIDocument(0)
	require.ErrorIs(t, err, errInvalidAPIServiceID)
	require.Equal(t, models.OpenAPIServiceDocument{}, document)
	require.Zero(t, updatedAt)
}

func TestAPIRouteOpenAPIPathFinderReportsMalformedRows(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	ctx := NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})
	service := models.APIService{Slug: "malformed-paths", Name: "Malformed Paths"}
	require.NoError(t, core.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, core.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "malformed"}
	require.NoError(t, core.Create(&route).Error)
	require.NoError(t, core.Exec("UPDATE api_routes SET openapi_paths = ? WHERE id = ?", "{", route.ID).Error)

	_, err := NewAdminQuery(ctx).APIRoute().ListOpenAPIPaths(service.ID, nil)
	require.Error(t, err)
}

func largeOpenAPIServiceDocument(t *testing.T) models.OpenAPIServiceDocument {
	t.Helper()
	schema, err := json.Marshal(map[string]any{
		"type":        "string",
		"description": strings.Repeat("s", 1<<20),
	})
	require.NoError(t, err)
	return models.OpenAPIServiceDocument{
		Version: "3.1.0",
		Info:    models.OpenAPIInfo{Version: "1.0.0"},
		Components: models.OpenAPIComponents{
			Schemas: map[string]json.RawMessage{"LargePayload": schema},
		},
	}
}

func largeOpenAPIPaths(t *testing.T) map[string]models.OpenAPIPathItem {
	t.Helper()
	schema, err := json.Marshal(map[string]any{
		"type":        "string",
		"description": strings.Repeat("r", 1<<20),
	})
	require.NoError(t, err)
	return map[string]models.OpenAPIPathItem{
		"/users/{id}": {
			Operations: map[string]models.OpenAPIOperation{
				"GET": {
					OperationID: "getUser",
					Responses: map[string]models.OpenAPIResponse{
						"200": {Description: "ok", Content: map[string]models.OpenAPIMediaType{"application/json": {Schema: schema}}},
					},
				},
			},
		},
	}
}
