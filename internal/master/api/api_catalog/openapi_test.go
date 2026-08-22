package api_catalog

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Break caught: passing a full service document to a route-scoped Token would
// disclose operations that the Token cannot invoke.
func TestOpenAPIHTTPFiltersDocumentsToTheRequestScope(t *testing.T) {
	for _, tc := range []struct {
		name       string
		admin      bool
		grant      func(*openAPICatalogFixture)
		queryToken bool
		wantPaths  []string
	}{
		{
			name:      "administrator receives every enabled route",
			admin:     true,
			wantPaths: []string{"/", "/alpha/one", "/beta/two", "/disabled/hidden"},
		},
		{
			name: "service-wide Token receives every enabled route",
			grant: func(fx *openAPICatalogFixture) {
				fx.bindInvoke(t, models.APIResourceService, fx.service.ID)
			},
			queryToken: true,
			wantPaths:  []string{"/", "/alpha/one", "/beta/two", "/disabled/hidden"},
		},
		{
			name: "route-only Token receives only its granted route",
			grant: func(fx *openAPICatalogFixture) {
				fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID)
			},
			queryToken: true,
			wantPaths:  []string{"/alpha/one"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newOpenAPICatalogFixture(t)
			if tc.grant != nil {
				tc.grant(fx)
			}
			response := fx.get(t, tc.admin, fx.user.ID, fx.service.ID, tc.queryToken)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			document := responseDocument(t, response)
			require.Equal(t, tc.wantPaths, sortedDocumentPaths(document))
			require.NotContains(t, response.Body.String(), "backend_id")
			require.NotContains(t, response.Body.String(), "upstream_path")
			require.NotContains(t, response.Body.String(), "price_per_call")
			require.NotContains(t, response.Body.String(), "credential")
		})
	}
}

// Break caught: filtering paths without filtering the Service component graph
// discloses schemas and reusable Path Items owned only by an ungranted Route.
func TestOpenAPIHTTPPrunesRouteScopedComponentsToReachableClosure(t *testing.T) {
	for _, tc := range []struct {
		name          string
		version       string
		pathItemRefs  bool
		admin         bool
		grant         func(*openAPICatalogFixture)
		queryToken    bool
		wantSchemas   []string
		wantPathItems []string
		wantSecurity  []string
		wantExamples  []string
		wantLinks     []string
	}{
		{
			name:    "OpenAPI 3.1 Route scope keeps a ref-only Path Item and nested schema closure",
			version: "3.1.0", pathItemRefs: true,
			grant:         func(fx *openAPICatalogFixture) { fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID) },
			queryToken:    true,
			wantSchemas:   []string{"AllowedEnvelope", "AllowedLeaf", "MappedLeaf"},
			wantPathItems: []string{"AllowedPath"},
			wantSecurity:  []string{"AllowedAuth"},
			wantExamples:  []string{"AllowedExample"},
			wantLinks:     []string{"AllowedLink"},
		},
		{
			name:          "OpenAPI 3.0 Route scope keeps nested schema refs without Path Item components",
			version:       "3.0.3",
			grant:         func(fx *openAPICatalogFixture) { fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID) },
			queryToken:    true,
			wantSchemas:   []string{"AllowedEnvelope", "AllowedLeaf", "MappedLeaf"},
			wantPathItems: []string{},
			wantSecurity:  []string{"AllowedAuth"},
			wantExamples:  []string{"AllowedExample"},
			wantLinks:     []string{"AllowedLink"},
		},
		{
			name:    "service scope preserves the complete component document",
			version: "3.1.0", pathItemRefs: true,
			grant:         func(fx *openAPICatalogFixture) { fx.bindInvoke(t, models.APIResourceService, fx.service.ID) },
			queryToken:    true,
			wantSchemas:   []string{"AllowedEnvelope", "AllowedLeaf", "DeniedOnly", "MappedLeaf", "Unused"},
			wantPathItems: []string{"AllowedPath", "DeniedPath", "UnusedPath"},
			wantSecurity:  []string{"AllowedAuth", "DeniedAuth"},
			wantExamples:  []string{"AllowedExample", "UnusedExample"},
			wantLinks:     []string{"AllowedLink", "UnusedLink"},
		},
		{
			name:    "administrator preserves the complete component document",
			version: "3.1.0", pathItemRefs: true, admin: true,
			wantSchemas:   []string{"AllowedEnvelope", "AllowedLeaf", "DeniedOnly", "MappedLeaf", "Unused"},
			wantPathItems: []string{"AllowedPath", "DeniedPath", "UnusedPath"},
			wantSecurity:  []string{"AllowedAuth", "DeniedAuth"},
			wantExamples:  []string{"AllowedExample", "UnusedExample"},
			wantLinks:     []string{"AllowedLink", "UnusedLink"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newOpenAPICatalogFixture(t)
			fx.seedComponentClosure(t, tc.version, tc.pathItemRefs)
			if tc.grant != nil {
				tc.grant(fx)
			}

			response := fx.get(t, tc.admin, fx.user.ID, fx.service.ID, tc.queryToken)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			document := responseDocument(t, response)
			require.Equal(t, tc.wantSchemas, sortedComponentNames(document, "schemas"))
			require.Equal(t, tc.wantPathItems, sortedComponentNames(document, "pathItems"))
			require.Equal(t, tc.wantSecurity, sortedComponentNames(document, "securitySchemes"))
			require.Equal(t, tc.wantExamples, sortedComponentNames(document, "examples"))
			require.Equal(t, tc.wantLinks, sortedComponentNames(document, "links"))
		})
	}
}

// Break caught: anchor references require JSON Schema resource scope, so a
// global anchor map can either drop a live target or retain a same-name target
// from a denied resource.
func TestOpenAPIHTTPPrunesAnchoredSchemasWithinTheirResourceScope(t *testing.T) {
	fx := newOpenAPICatalogFixture(t)
	fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID)
	document := models.OpenAPIServiceDocument{
		Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"},
		Components: models.OpenAPIComponents{Schemas: map[string]json.RawMessage{
			"AnchorUse":     json.RawMessage(`{"allOf":[{"$ref":"#plain"},{"$dynamicRef":"#dynamic"}]}`),
			"AnchorTarget":  json.RawMessage(`{"$anchor":"plain","type":"string"}`),
			"DynamicTarget": json.RawMessage(`{"$dynamicAnchor":"dynamic","type":"number"}`),
			"AllowedPet":    json.RawMessage(`{"type":"object","discriminator":{"propertyName":"kind","mapping":{"dog":"Dog"}}}`),
			"Dog":           json.RawMessage(`{"type":"object"}`),
			"AllowedResource": json.RawMessage(`{
				"$id":"urn:allowed","$defs":{"Target":{"$anchor":"same","type":"string"}},
				"properties":{"value":{"$ref":"#same"}}
			}`),
			"DeniedResource": json.RawMessage(`{
				"$id":"urn:denied","$defs":{"Target":{"$anchor":"same","type":"boolean"}},
				"properties":{"value":{"$ref":"#same"}}
			}`),
			"DeniedOnly": json.RawMessage(`{"type":"null"}`),
		}},
	}
	require.NoError(t, fx.db.Model(&models.APIService{}).Where("id = ?", fx.service.ID).
		Update("openapi_document", datatypes.NewJSONType(document)).Error)
	responseSchemas := []string{"AnchorUse", "AllowedPet", "AllowedResource"}
	responses := make(map[string]models.OpenAPIResponse, len(responseSchemas))
	for index, schema := range responseSchemas {
		responses[strconv.Itoa(200+index)] = models.OpenAPIResponse{
			Description: "ok",
			Content: map[string]models.OpenAPIMediaType{
				"application/json": {Schema: json.RawMessage(`{"$ref":"#/components/schemas/` + schema + `"}`)},
			},
		}
	}
	require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", fx.alpha.ID).
		Update("openapi_paths", datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
			"/private/alpha/one": {Operations: map[string]models.OpenAPIOperation{"GET": {Responses: responses}}},
		})).Error)

	response := fx.get(t, false, fx.user.ID, fx.service.ID, true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []string{
		"AllowedPet", "AllowedResource", "AnchorTarget", "AnchorUse", "Dog", "DynamicTarget",
	}, sortedComponentNames(responseDocument(t, response), "schemas"))
}

// Break caught: discriminator mappings are URI references and may target a
// nested schema. The closure must retain the owning top-level component, not
// require the mapping target itself to be a component root.
func TestOpenAPIHTTPRetainsComponentOwningNestedDiscriminatorTarget(t *testing.T) {
	for _, version := range []string{"3.1.0", "3.0.3"} {
		t.Run(version, func(t *testing.T) {
			fx := newOpenAPICatalogFixture(t)
			fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID)
			document := models.OpenAPIServiceDocument{
				Version: version, Info: models.OpenAPIInfo{Version: "1"},
				Components: models.OpenAPIComponents{Schemas: map[string]json.RawMessage{
					"AllowedPet": json.RawMessage(`{
						"type":"object","discriminator":{"propertyName":"kind","mapping":{
							"dog":"#%2Fcomponents%2Fschemas%2FPet~1Variants%2F$defs%2FDog"
						}}
					}`),
					"Pet/Variants": json.RawMessage(`{"$defs":{"Dog":{"type":"object"}}}`),
					"DeniedOnly":   json.RawMessage(`{"type":"null"}`),
				}},
			}
			require.NoError(t, fx.db.Model(&models.APIService{}).Where("id = ?", fx.service.ID).
				Update("openapi_document", datatypes.NewJSONType(document)).Error)
			require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", fx.alpha.ID).
				Update("openapi_paths", datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
					"/private/alpha/one": {Operations: map[string]models.OpenAPIOperation{"GET": {
						Responses: map[string]models.OpenAPIResponse{"200": {
							Description: "ok", Content: map[string]models.OpenAPIMediaType{
								"application/json": {Schema: json.RawMessage(`{"$ref":"#/components/schemas/AllowedPet"}`)},
							},
						}},
					}}},
				})).Error)

			response := fx.get(t, false, fx.user.ID, fx.service.ID, true)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Equal(t, []string{"AllowedPet", "Pet/Variants"},
				sortedComponentNames(responseDocument(t, response), "schemas"))
		})
	}
}

// Break caught: a service-level permission must not make disabled or root
// routes visible outside their own Route ID authorization decision.
func TestOpenAPIHTTPDoesNotLeakDisabledOrUngrantableRoutes(t *testing.T) {
	fx := newOpenAPICatalogFixture(t)
	fx.bindInvoke(t, models.APIResourceService, fx.service.ID)
	require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", fx.disabled.ID).Update("status", consts.StatusDisabled).Error)
	require.NoError(t, fx.db.Exec("UPDATE api_routes SET openapi_paths = ? WHERE id = ?", "{", fx.disabled.ID).Error)

	response := fx.get(t, false, fx.user.ID, fx.service.ID, true)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	serviceWide := responseDocument(t, response)
	require.Equal(t, []string{"/", "/alpha/one", "/beta/two"}, sortedDocumentPaths(serviceWide))

	rootOnly := newOpenAPICatalogFixture(t)
	rootOnly.bindInvoke(t, models.APIResourceRoute, rootOnly.root.ID)
	rootDocument := responseDocument(t, rootOnly.get(t, false, rootOnly.user.ID, rootOnly.service.ID, true))
	require.Equal(t, []string{"/"}, sortedDocumentPaths(rootDocument))
}

// Break caught: treating an ungranted service as an empty document leaks the
// service's existence and turns a permission denial into a successful read.
func TestOpenAPIHTTPReturnsNotFoundForAnUnpermittedService(t *testing.T) {
	fx := newOpenAPICatalogFixture(t)
	fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID)

	response := fx.get(t, false, fx.user.ID, fx.otherService.ID, true)
	require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
}

// Break caught: once catalog authorization has been read, a concurrent commit
// must not mix that old scope with a newer Service state or Route document.
// The endpoint linearizes at its first scope read and returns that full snapshot.
func TestOpenAPIHTTPReadsAuthorizationAndDocumentsFromOneSQLiteSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name            string
		admin           bool
		queryToken      bool
		syncPoint       func(*gorm.Statement) bool
		mutate          func(*gorm.DB, *openAPICatalogFixture) error
		assertCommitted func(*testing.T, *gorm.DB, *openAPICatalogFixture)
	}{
		{
			name:       "route grant revocation cannot expose the post-revocation document",
			queryToken: true,
			syncPoint:  catalogScopeRouteRead,
			mutate: func(writer *gorm.DB, fx *openAPICatalogFixture) error {
				return writer.Transaction(func(tx *gorm.DB) error {
					if err := tx.Where("principal_type = ? AND principal_id = ?", models.APIPrincipalToken, fx.token.ID).Delete(&models.RoleBinding{}).Error; err != nil {
						return err
					}
					paths := datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
						"/private/alpha/one": {Operations: map[string]models.OpenAPIOperation{"GET": {
							Summary: "snapshot-after-revocation", Responses: map[string]models.OpenAPIResponse{"200": {Description: "ok"}},
						}}},
					})
					return tx.Model(&models.APIRoute{}).Where("id = ?", fx.alpha.ID).UpdateColumn("openapi_paths", paths).Error
				})
			},
			assertCommitted: assertRouteRevocationCommitted,
		},
		{
			name:       "Token service disable cannot replace the authorized snapshot with a 404",
			queryToken: true,
			syncPoint:  catalogScopeRouteRead,
			mutate: func(writer *gorm.DB, fx *openAPICatalogFixture) error {
				return writer.Model(&models.APIService{}).Where("id = ?", fx.service.ID).UpdateColumn("status", consts.StatusDisabled).Error
			},
			assertCommitted: assertServiceDisabledCommitted,
		},
		{
			name:      "administrator service disable after first Service read preserves the complete snapshot",
			admin:     true,
			syncPoint: catalogServiceRead,
			mutate: func(writer *gorm.DB, fx *openAPICatalogFixture) error {
				return writer.Transaction(func(tx *gorm.DB) error {
					if err := tx.Model(&models.APIService{}).Where("id = ?", fx.service.ID).UpdateColumn("status", consts.StatusDisabled).Error; err != nil {
						return err
					}
					paths := datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
						"/private/alpha/one": {Operations: map[string]models.OpenAPIOperation{"GET": {
							Summary: "snapshot-after-admin-disable", Responses: map[string]models.OpenAPIResponse{"200": {Description: "ok"}},
						}}},
					})
					return tx.Model(&models.APIRoute{}).Where("id = ?", fx.alpha.ID).UpdateColumn("openapi_paths", paths).Error
				})
			},
			assertCommitted: assertAdminDisableCommitted,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx, writer := newOpenAPICatalogFileFixture(t)
			if tc.queryToken {
				fx.bindInvoke(t, models.APIResourceRoute, fx.alpha.ID)
			}
			callbackName := "test:catalog_openapi_snapshot_" + strings.ReplaceAll(tc.name, " ", "_")
			var mutateOnce sync.Once
			var mutationRan atomic.Int32
			var mutateErr error
			require.NoError(t, fx.db.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement == nil || !tc.syncPoint(tx.Statement) {
					return
				}
				mutateOnce.Do(func() {
					mutationRan.Add(1)
					mutateErr = tc.mutate(writer, fx)
				})
				if mutateErr != nil {
					tx.AddError(mutateErr)
				}
			}))
			t.Cleanup(func() { require.NoError(t, fx.db.Callback().Query().Remove(callbackName)) })

			response := fx.get(t, tc.admin, fx.user.ID, fx.service.ID, tc.queryToken)
			require.Equal(t, int32(1), mutationRan.Load(), "snapshot synchronization point must run exactly once")
			require.NoError(t, mutateErr)
			tc.assertCommitted(t, writer, fx)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			require.Contains(t, response.Body.String(), "snapshot-before-commit")
			require.NotContains(t, response.Body.String(), "snapshot-after-revocation")
			require.NotContains(t, response.Body.String(), "snapshot-after-admin-disable")
		})
	}
}

func catalogScopeRouteRead(statement *gorm.Statement) bool {
	if statement.Table != "api_routes" {
		return false
	}
	_, ok := statement.Dest.(*[]models.APIRoute)
	return ok
}

func catalogServiceRead(statement *gorm.Statement) bool {
	if statement.Table != "api_services" {
		return false
	}
	_, ok := statement.Dest.(*models.APIService)
	return ok
}

func assertRouteRevocationCommitted(t *testing.T, writer *gorm.DB, fx *openAPICatalogFixture) {
	t.Helper()
	var bindingCount int64
	require.NoError(t, writer.Model(&models.RoleBinding{}).
		Where("principal_type = ? AND principal_id = ?", models.APIPrincipalToken, fx.token.ID).
		Count(&bindingCount).Error)
	require.Zero(t, bindingCount, "independent writer must observe the committed Route grant revocation")
	var route models.APIRoute
	require.NoError(t, writer.Select("id", "openapi_paths").First(&route, fx.alpha.ID).Error)
	require.Equal(t, "snapshot-after-revocation", route.OpenAPIPaths.Data()["/private/alpha/one"].Operations["GET"].Summary)
}

func assertServiceDisabledCommitted(t *testing.T, writer *gorm.DB, fx *openAPICatalogFixture) {
	t.Helper()
	var service models.APIService
	require.NoError(t, writer.Select("id", "status").First(&service, fx.service.ID).Error)
	require.Equal(t, consts.StatusDisabled, service.Status, "independent writer must observe the committed Service disable")
}

func assertAdminDisableCommitted(t *testing.T, writer *gorm.DB, fx *openAPICatalogFixture) {
	t.Helper()
	assertServiceDisabledCommitted(t, writer, fx)
	var route models.APIRoute
	require.NoError(t, writer.Select("id", "openapi_paths").First(&route, fx.alpha.ID).Error)
	require.Equal(t, "snapshot-after-admin-disable", route.OpenAPIPaths.Data()["/private/alpha/one"].Operations["GET"].Summary)
}

func TestOpenAPIHTTPValidatesServiceAndTokenScope(t *testing.T) {
	fx := newOpenAPICatalogFixture(t)
	for _, target := range []string{
		"/api-catalog/openapi",
		"/api-catalog/openapi?service_id=0",
		"/api-catalog/openapi?service_id=not-a-number",
	} {
		response := fx.request(t, true, fx.user.ID, target)
		require.Equal(t, http.StatusBadRequest, response.Code, target+": "+response.Body.String())
	}

	missingToken := fx.request(t, false, fx.user.ID, "/api-catalog/openapi?service_id="+strconv.FormatUint(uint64(fx.service.ID), 10))
	require.Equal(t, http.StatusBadRequest, missingToken.Code, missingToken.Body.String())
	require.Equal(t, "token_required", openAPIErrorCode(t, missingToken))

	foreignUser := models.User{Username: "catalog-openapi-foreign", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, fx.db.Create(&foreignUser).Error)
	foreignToken := models.Token{UserID: foreignUser.ID, Key: "catalog-openapi-foreign", Name: "foreign", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, fx.db.Create(&foreignToken).Error)
	foreignTarget := "/api-catalog/openapi?service_id=" + strconv.FormatUint(uint64(fx.service.ID), 10) + "&token_id=" + strconv.FormatUint(uint64(foreignToken.ID), 10)
	foreign := fx.request(t, false, fx.user.ID, foreignTarget)
	require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	require.Equal(t, "token_not_available", openAPIErrorCode(t, foreign))
}

type openAPICatalogFixture struct {
	db           *gorm.DB
	application  app.Application
	user         models.User
	token        models.Token
	service      models.APIService
	otherService models.APIService
	alpha        models.APIRoute
	beta         models.APIRoute
	root         models.APIRoute
	disabled     models.APIRoute
}

func newOpenAPICatalogFixture(t *testing.T) *openAPICatalogFixture {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+strings.ReplaceAll(t.Name(), "/", "-")+strconv.FormatInt(time.Now().UnixNano(), 10)+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	return seedOpenAPICatalogFixture(t, db)
}

func newOpenAPICatalogFileFixture(t *testing.T) (*openAPICatalogFixture, *gorm.DB) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "catalog-openapi.db") + "?_pragma=busy_timeout(5000)"
	reader, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	readerSQL, err := reader.DB()
	require.NoError(t, err)
	readerSQL.SetMaxOpenConns(4)
	t.Cleanup(func() { require.NoError(t, readerSQL.Close()) })
	require.NoError(t, reader.Exec("PRAGMA journal_mode=WAL").Error)
	require.NoError(t, models.MigrateCoreDB(reader))

	writer, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	writerSQL, err := writer.DB()
	require.NoError(t, err)
	writerSQL.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, writerSQL.Close()) })
	writeCtx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	t.Cleanup(cancel)
	writer = writer.WithContext(writeCtx)
	return seedOpenAPICatalogFixture(t, reader), writer
}

func seedOpenAPICatalogFixture(t *testing.T, db *gorm.DB) *openAPICatalogFixture {
	t.Helper()
	application := app.NewApplication()
	application.SetCoreDB(db)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	fx := &openAPICatalogFixture{db: db, application: application}
	fx.user = models.User{Username: "catalog-openapi-user", Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, db.Create(&fx.user).Error)
	fx.token = models.Token{UserID: fx.user.ID, Key: "catalog-openapi-token", Name: "catalog OpenAPI", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, db.Create(&fx.token).Error)
	fx.service, fx.alpha, fx.beta, fx.root, fx.disabled = fx.seedService(t, "catalog-openapi", true)
	fx.otherService, _, _, _, _ = fx.seedService(t, "catalog-other", false)
	return fx
}

func (fx *openAPICatalogFixture) seedService(t *testing.T, slug string, documented bool) (models.APIService, models.APIRoute, models.APIRoute, models.APIRoute, models.APIRoute) {
	t.Helper()
	service := models.APIService{Slug: slug, Name: "Catalog " + slug, Description: "safe catalog document", PricePerCall: 99, Status: consts.StatusEnabled}
	require.NoError(t, fx.db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: slug + " backend"}
	require.NoError(t, fx.db.Create(&backend).Error)
	routes := []models.APIRoute{
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "alpha", UpstreamPath: "/private/alpha", Status: consts.StatusEnabled},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "beta", UpstreamPath: "/private/beta", Status: consts.StatusEnabled},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "", UpstreamPath: "/", Status: consts.StatusEnabled},
		{APIServiceID: service.ID, BackendID: backend.ID, Slug: "disabled", Status: consts.StatusEnabled},
	}
	for index := range routes {
		require.NoError(t, fx.db.Create(&routes[index]).Error)
	}
	if documented {
		require.NoError(t, fx.db.Model(&models.APIService{}).Where("id = ?", service.ID).Update("openapi_document", datatypes.NewJSONType(models.OpenAPIServiceDocument{Version: "3.1.0", Info: models.OpenAPIInfo{Version: "1"}})).Error)
		for index, route := range routes {
			path := []string{"/private/alpha/one", "/private/beta/two", "/", "/disabled/hidden"}[index]
			require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", route.ID).Update("openapi_paths", datatypes.NewJSONType(map[string]models.OpenAPIPathItem{
				path: {Operations: map[string]models.OpenAPIOperation{"GET": {Summary: "snapshot-before-commit", Responses: map[string]models.OpenAPIResponse{"200": {Description: "ok"}}}}},
			})).Error)
		}
	}
	return service, routes[0], routes[1], routes[2], routes[3]
}

func (fx *openAPICatalogFixture) bindInvoke(t *testing.T, resource models.APIResource, resourceID uint) {
	t.Helper()
	role := models.Role{Key: "catalog-openapi-role-" + strconv.FormatUint(uint64(resourceID), 10) + string(resource), Name: "catalog OpenAPI", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: models.APIPermissionInvoke}
	require.NoError(t, fx.db.Create(&role).Error)
	require.NoError(t, fx.db.Create(&permission).Error)
	require.NoError(t, fx.db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, fx.db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: fx.token.ID, RoleID: role.ID}).Error)
}

func (fx *openAPICatalogFixture) seedComponentClosure(t *testing.T, version string, pathItemRefs bool) {
	t.Helper()
	responseFor := func(schema string) models.OpenAPIOperation {
		return models.OpenAPIOperation{Extensions: map[string]json.RawMessage{
			"x-reference-like-data": json.RawMessage(`{"$ref":"#/components/schemas/DeniedOnly"}`),
		}, Responses: map[string]models.OpenAPIResponse{
			"200": {
				Description: "ok",
				Content: map[string]models.OpenAPIMediaType{
					"application/json": {
						Schema: json.RawMessage(`{"$ref":"#/components/schemas/` + schema + `"}`),
						Examples: map[string]json.RawMessage{
							"allowed": json.RawMessage(`{"$ref":"#/components/examples/AllowedExample"}`),
						},
					},
				},
				Links: map[string]json.RawMessage{
					"allowed": json.RawMessage(`{"$ref":"#/components/links/AllowedLink"}`),
				},
			},
		}}
	}
	document := models.OpenAPIServiceDocument{
		Version:  version,
		Info:     models.OpenAPIInfo{Version: "1"},
		Security: []models.OpenAPISecurityRequirement{{"AllowedAuth": {}}},
		Components: models.OpenAPIComponents{Schemas: map[string]json.RawMessage{
			"AllowedEnvelope": json.RawMessage(`{"type":"object","properties":{"data":{"$ref":"#/components/schemas/AllowedLeaf"}},"discriminator":{"propertyName":"kind","mapping":{"leaf":"#/components/schemas/MappedLeaf"}},"examples":[{"$ref":"#/components/schemas/DeniedOnly"}],"default":{"$ref":"#/components/schemas/DeniedOnly"}}`),
			"AllowedLeaf":     json.RawMessage(`{"type":"string"}`),
			"DeniedOnly":      json.RawMessage(`{"type":"object"}`),
			"MappedLeaf":      json.RawMessage(`{"type":"object"}`),
			"Unused":          json.RawMessage(`{"type":"boolean"}`),
		}, SecuritySchemes: map[string]json.RawMessage{
			"AllowedAuth": json.RawMessage(`{"type":"apiKey","in":"header","name":"X-Allowed-Key"}`),
			"DeniedAuth":  json.RawMessage(`{"type":"apiKey","in":"header","name":"X-Denied-Key"}`),
		}, Examples: map[string]json.RawMessage{
			"AllowedExample": json.RawMessage(`{"value":{"$ref":"#/components/schemas/DeniedOnly"}}`),
			"UnusedExample":  json.RawMessage(`{"value":"unused"}`),
		}, Links: map[string]json.RawMessage{
			"AllowedLink": json.RawMessage(`{
				"operationId":"getAllowed","parameters":{"$ref":"#/components/schemas/DeniedOnly"}
			}`),
			"UnusedLink": json.RawMessage(`{"operationId":"getUnused"}`),
		}},
	}
	alphaPath := models.OpenAPIPathItem{Operations: map[string]models.OpenAPIOperation{"GET": responseFor("AllowedEnvelope")}}
	betaPath := models.OpenAPIPathItem{Operations: map[string]models.OpenAPIOperation{"GET": responseFor("DeniedOnly")}}
	if pathItemRefs {
		document.Components.PathItems = map[string]models.OpenAPIPathItem{
			"AllowedPath": {Operations: map[string]models.OpenAPIOperation{"GET": responseFor("AllowedEnvelope")}},
			"DeniedPath":  {Operations: map[string]models.OpenAPIOperation{"GET": responseFor("DeniedOnly")}},
			"UnusedPath":  {Operations: map[string]models.OpenAPIOperation{"GET": {Responses: map[string]models.OpenAPIResponse{"204": {Description: "unused"}}}}},
		}
		alphaPath = models.OpenAPIPathItem{Ref: "#/components/pathItems/AllowedPath"}
		betaPath = models.OpenAPIPathItem{Ref: "#/components/pathItems/DeniedPath"}
	}
	require.NoError(t, fx.db.Model(&models.APIService{}).Where("id = ?", fx.service.ID).
		Update("openapi_document", datatypes.NewJSONType(document)).Error)
	require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", fx.alpha.ID).
		Update("openapi_paths", datatypes.NewJSONType(map[string]models.OpenAPIPathItem{"/private/alpha/one": alphaPath})).Error)
	require.NoError(t, fx.db.Model(&models.APIRoute{}).Where("id = ?", fx.beta.ID).
		Update("openapi_paths", datatypes.NewJSONType(map[string]models.OpenAPIPathItem{"/private/beta/two": betaPath})).Error)
}

func (fx *openAPICatalogFixture) get(t *testing.T, admin bool, userID, serviceID uint, includeToken bool) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api-catalog/openapi?service_id=" + strconv.FormatUint(uint64(serviceID), 10)
	if includeToken {
		target += "&token_id=" + strconv.FormatUint(uint64(fx.token.ID), 10)
	}
	return fx.request(t, admin, userID, target)
}

func (fx *openAPICatalogFixture) request(t *testing.T, admin bool, userID uint, target string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &Handler{App: fx.application}
	router.GET("/api-catalog/openapi", func(c *gin.Context) {
		c.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: admin, UserID: userID})
	}, api.Adapt(api.NewAdapter(&config.MasterRuntimeConfig{}, zap.NewNop(), fx.application), api.BindQuery, handler.OpenAPI))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	return response
}

func openAPIErrorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	return body.Code
}

func responseDocument(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body struct {
		Document map[string]any `json:"document"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.NotEmpty(t, body.Document)
	return body.Document
}

func sortedDocumentPaths(document map[string]any) []string {
	paths, _ := document["paths"].(map[string]any)
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func sortedComponentNames(document map[string]any, collection string) []string {
	components, _ := document["components"].(map[string]any)
	items, _ := components[collection].(map[string]any)
	result := make([]string, 0, len(items))
	for name := range items {
		result = append(result, name)
	}
	sort.Strings(result)
	return result
}
