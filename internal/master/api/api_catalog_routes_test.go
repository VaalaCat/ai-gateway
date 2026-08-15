package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Break caught: exposing the management models on the user catalog would leak
// upstream routing internals, while coupling the service list to access lookup
// would make a safe read fail when the independent effective lookup fails.
func TestAPICatalogIsAuthenticatedSafeAndIndependentFromEffectiveAccess(t *testing.T) {
	srv := setupTestMaster(t)
	service := models.APIService{
		Slug: "catalog-weather", Name: "Catalog weather", Description: "Public weather API",
		Status: consts.StatusEnabled,
	}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "catalog-weather-origin"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{
		APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast",
		Protocols:             datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP, models.APIProtocolWebSocket},
		AllowedMethods:        datatypes.JSONSlice[string]{http.MethodPost},
		WebSocketSubprotocols: datatypes.JSONSlice[string]{"forecast.v1"},
		ExampleRequest: datatypes.NewJSONType(models.APIRequestExample{
			Method: http.MethodPost, Subpath: "today", Query: "unit=c",
			Headers: map[string]string{"X-Client": "catalog"}, Body: `{"city":"Paris"}`,
		}),
		UpstreamPath: "/private/forecast", Status: consts.StatusEnabled,
	}
	require.NoError(t, srv.DB.Create(&route).Error)
	user := models.User{Username: "catalog-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "catalog-safe-token", Name: "catalog-safe", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&token).Error)
	role := models.Role{Key: "catalog-safe-invoke", Name: "Catalog safe invoke", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error)
	jwt, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)

	unauthenticated := reqHelper(srv, "", http.MethodGet, "/api/api-catalog/services", nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())

	services := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, services.Code, services.Body.String())
	var servicePage map[string]any
	require.NoError(t, json.Unmarshal(services.Body.Bytes(), &servicePage))
	rows := servicePage["data"].([]any)
	require.Len(t, rows, 1)
	publicService := rows[0].(map[string]any)
	require.Equal(t, "catalog-weather", publicService["slug"])
	require.NotContains(t, publicService, "price_per_call")
	require.NotContains(t, publicService, "backend_id")
	detail := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services/detail?id="+jsonNumber(service.ID)+"&token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var publicDetail map[string]any
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &publicDetail))
	require.Equal(t, "Public weather API", publicDetail["description"])
	require.NotContains(t, publicDetail, "price_per_call")

	routes := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(service.ID)+"&token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, routes.Code, routes.Body.String())
	var routePage map[string]any
	require.NoError(t, json.Unmarshal(routes.Body.Bytes(), &routePage))
	publicRoute := routePage["data"].([]any)[0].(map[string]any)
	require.Equal(t, "forecast", publicRoute["slug"])
	require.Equal(t, map[string]any{
		"method": http.MethodPost, "subpath": "today", "query": "unit=c",
		"headers": map[string]any{"X-Client": "catalog"}, "body": `{"city":"Paris"}`,
	}, publicRoute["example_request"])
	for _, forbidden := range []string{"backend_id", "upstream_path", "forward_subpath", "credential_ciphertext", "proxy_url_ciphertext", "header_override"} {
		require.NotContains(t, publicRoute, forbidden)
	}

	// The selected Token's invoke scope governs every catalog endpoint.
	effective := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/effective?service_id="+jsonNumber(service.ID)+"&token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, effective.Code, effective.Body.String())
	missingEffective := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/effective?service_id=999999&token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusNotFound, missingEffective.Code, missingEffective.Body.String())
	stillListed := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, stillListed.Code, stillListed.Body.String())
}

func TestAPICatalogFiltersDisabledResourcesPaginatesAndUsesStrictSafeDTOs(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("catalog-pagination-admin", "admin123"))
	jwt := loginAsAdmin(t, srv, "catalog-pagination-admin", "admin123")

	enabled := models.APIService{Slug: "enabled-catalog", Name: "Enabled catalog", Description: "safe", Status: consts.StatusEnabled}
	disabled := models.APIService{Slug: "disabled-catalog", Name: "Disabled catalog", Status: consts.StatusDisabled}
	require.NoError(t, srv.DB.Create(&enabled).Error)
	require.NoError(t, srv.DB.Create(&disabled).Error)
	require.NoError(t, srv.DB.Model(&disabled).UpdateColumn("status", consts.StatusDisabled).Error)
	backend := models.APIBackend{APIServiceID: enabled.ID, Name: "catalog-origin"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	enabledRoute := models.APIRoute{APIServiceID: enabled.ID, BackendID: backend.ID, Slug: "enabled-route", Status: consts.StatusEnabled}
	disabledRoute := models.APIRoute{APIServiceID: enabled.ID, BackendID: backend.ID, Slug: "disabled-route", Status: consts.StatusDisabled}
	require.NoError(t, srv.DB.Create(&enabledRoute).Error)
	require.NoError(t, srv.DB.Create(&disabledRoute).Error)
	require.NoError(t, srv.DB.Model(&disabledRoute).UpdateColumn("status", consts.StatusDisabled).Error)
	for index := 0; index < 100; index++ {
		service := models.APIService{Slug: "paged-catalog-" + strconv.Itoa(index), Name: "Paged catalog", Status: consts.StatusEnabled}
		require.NoError(t, srv.DB.Create(&service).Error)
	}

	services := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?page=3&page_size=50", nil)
	require.Equal(t, http.StatusOK, services.Code, services.Body.String())
	var servicePage map[string]any
	require.NoError(t, json.Unmarshal(services.Body.Bytes(), &servicePage))
	serviceRows := servicePage["data"].([]any)
	require.NotEmpty(t, serviceRows)
	for _, item := range serviceRows {
		row := item.(map[string]any)
		require.Equal(t, []string{"description", "id", "name", "slug"}, sortedKeys(row))
		require.NotEqual(t, disabled.Slug, row["slug"])
	}

	detail := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services/detail?id="+jsonNumber(enabled.ID), nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var detailRow map[string]any
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &detailRow))
	require.Equal(t, []string{"description", "id", "name", "slug"}, sortedKeys(detailRow))
	missingDisabled := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services/detail?id="+jsonNumber(disabled.ID), nil)
	require.Equal(t, http.StatusNotFound, missingDisabled.Code, missingDisabled.Body.String())

	routes := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(enabled.ID), nil)
	require.Equal(t, http.StatusOK, routes.Code, routes.Body.String())
	var routePage map[string]any
	require.NoError(t, json.Unmarshal(routes.Body.Bytes(), &routePage))
	routeRows := routePage["data"].([]any)
	require.Len(t, routeRows, 1)
	routeRow := routeRows[0].(map[string]any)
	require.Equal(t, enabledRoute.Slug, routeRow["slug"])
	require.Equal(t, []string{"allowed_methods", "api_service_id", "example_request", "id", "protocols", "slug", "websocket_subprotocols"}, sortedKeys(routeRow))
	require.Equal(t, map[string]any{"method": "", "subpath": "", "query": "", "headers": map[string]any{}, "body": ""}, routeRow["example_request"])
}

// behavior change: ordinary catalog visibility is selected-token scoped.
func TestAPICatalogRequiresAndAuthorizesSelectedToken(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("catalog-admin", "admin123"))

	service := models.APIService{Slug: "scope-visible", Name: "Scope visible", Status: consts.StatusEnabled}
	hidden := models.APIService{Slug: "scope-hidden", Name: "Scope hidden", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	require.NoError(t, srv.DB.Create(&hidden).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "scope-visible-origin"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "scope-visible-route", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&route).Error)

	user := models.User{Username: "catalog-scope-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	foreignUser := models.User{Username: "catalog-scope-foreign", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&user).Error)
	require.NoError(t, srv.DB.Create(&foreignUser).Error)
	owned := models.Token{ID: 11, UserID: user.ID, Key: "catalog-scope-owned", Name: "owned", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	foreign := models.Token{ID: 12, UserID: foreignUser.ID, Key: "catalog-scope-foreign", Name: "foreign", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&owned).Error)
	require.NoError(t, srv.DB.Create(&foreign).Error)
	role := models.Role{Key: "catalog-scope-invoke", Name: "Catalog scope invoke", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: owned.ID, RoleID: role.ID}).Error)

	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	adminJWT := loginAsAdmin(t, srv, "catalog-admin", "admin123")

	cases := []struct {
		name       string
		viewer     string
		tokenQuery string
		wantStatus int
		wantCode   string
	}{
		{name: "ordinary missing token", viewer: "user", wantStatus: http.StatusBadRequest, wantCode: "token_required"},
		{name: "ordinary own token", viewer: "user", tokenQuery: "?token_id=11", wantStatus: http.StatusOK},
		{name: "ordinary foreign token", viewer: "user", tokenQuery: "?token_id=12", wantStatus: http.StatusNotFound, wantCode: "token_not_available"},
		{name: "admin missing token", viewer: "admin", wantStatus: http.StatusOK},
		{name: "admin foreign token", viewer: "admin", tokenQuery: "?token_id=11", wantStatus: http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jwt := userJWT
			if tc.viewer == "admin" {
				jwt = adminJWT
			}
			response := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services"+tc.tokenQuery, nil)
			require.Equal(t, tc.wantStatus, response.Code, response.Body.String())
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, jsonBody(t, response)["code"])
			}
		})
	}

	for _, path := range []string{
		"/api/api-catalog/services/detail?id=" + jsonNumber(service.ID) + "&token_id=11",
		"/api/api-catalog/routes?service_id=" + jsonNumber(service.ID) + "&token_id=11",
	} {
		response := reqHelper(srv, userJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	effective := reqHelper(srv, userJWT, http.MethodGet, "/api/api-catalog/effective?service_id="+jsonNumber(service.ID)+"&token_id=11", nil)
	require.Equal(t, http.StatusOK, effective.Code, effective.Body.String())
	var effectiveAccess struct {
		Scope    string `json:"scope"`
		RouteIDs []uint `json:"route_ids"`
	}
	require.NoError(t, json.Unmarshal(effective.Body.Bytes(), &effectiveAccess))
	require.Equal(t, "service", effectiveAccess.Scope)
	require.Equal(t, []uint{}, effectiveAccess.RouteIDs)
	for _, path := range []string{
		"/api/api-catalog/services/detail?id=" + jsonNumber(hidden.ID) + "&token_id=11",
		"/api/api-catalog/routes?service_id=" + jsonNumber(hidden.ID) + "&token_id=11",
		"/api/api-catalog/effective?service_id=" + jsonNumber(hidden.ID) + "&token_id=11",
	} {
		response := reqHelper(srv, userJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.Equal(t, "resource_not_found", jsonBody(t, response)["code"])
	}
}

// Break caught: an ordinary user could not be allowed to use a valid deep link
// to skip the selected-Token requirement on any catalog entry point.
func TestAPICatalogRequiresTokenAcrossEveryOrdinaryUserEntryPoint(t *testing.T) {
	srv, jwt, service, _ := newCatalogScopeHTTPFixture(t)

	for _, test := range []struct {
		name string
		path string
	}{
		{name: "services", path: "/api/api-catalog/services"},
		{name: "detail", path: "/api/api-catalog/services/detail?id=" + jsonNumber(service.ID)},
		{name: "routes", path: "/api/api-catalog/routes?service_id=" + jsonNumber(service.ID)},
		{name: "effective", path: "/api/api-catalog/effective?service_id=" + jsonNumber(service.ID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := reqHelper(srv, jwt, http.MethodGet, test.path, nil)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			require.Equal(t, "token_required", jsonBody(t, response)["code"])
		})
	}
}

// Break caught: choosing another user's Token as an administrator must project
// that Token's grants, not the administrator's unrestricted catalog view.
func TestAPICatalogAdministratorExternalTokenUsesExternalRouteOnlyScope(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("catalog-external-admin", "admin123"))

	allowed := models.APIService{Slug: "external-allowed", Name: "External allowed", Status: consts.StatusEnabled}
	hidden := models.APIService{Slug: "external-hidden", Name: "External hidden", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&allowed).Error)
	require.NoError(t, srv.DB.Create(&hidden).Error)
	allowedBackend := models.APIBackend{APIServiceID: allowed.ID, Name: "external-allowed-origin"}
	hiddenBackend := models.APIBackend{APIServiceID: hidden.ID, Name: "external-hidden-origin"}
	require.NoError(t, srv.DB.Create(&allowedBackend).Error)
	require.NoError(t, srv.DB.Create(&hiddenBackend).Error)
	grantedRoute := models.APIRoute{APIServiceID: allowed.ID, BackendID: allowedBackend.ID, Slug: "granted", Status: consts.StatusEnabled}
	siblingRoute := models.APIRoute{APIServiceID: allowed.ID, BackendID: allowedBackend.ID, Slug: "sibling", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&grantedRoute).Error)
	require.NoError(t, srv.DB.Create(&siblingRoute).Error)

	owner := models.User{Username: "catalog-external-owner", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&owner).Error)
	external := models.Token{UserID: owner.ID, Key: "catalog-external-token", Name: "external", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&external).Error)
	role := models.Role{Key: "catalog-external-route", Name: "Catalog external route", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceRoute, ResourceID: grantedRoute.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: external.ID, RoleID: role.ID}).Error)
	adminJWT := loginAsAdmin(t, srv, "catalog-external-admin", "admin123")
	tokenQuery := "&token_id=" + jsonNumber(external.ID)

	services := reqHelper(srv, adminJWT, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(external.ID), nil)
	require.Equal(t, http.StatusOK, services.Code, services.Body.String())
	serviceRows := jsonBody(t, services)["data"].([]any)
	require.Len(t, serviceRows, 1)
	require.Equal(t, allowed.ID, uint(serviceRows[0].(map[string]any)["id"].(float64)))

	for _, path := range []string{
		"/api/api-catalog/services/detail?id=" + jsonNumber(allowed.ID) + tokenQuery,
		"/api/api-catalog/routes?service_id=" + jsonNumber(allowed.ID) + tokenQuery,
	} {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	// Break caught: Effective must reuse the CatalogRequestScope it already
	// authorized, instead of invoking its old second access finder after the gate.
	callbackName := "test:catalog_effective_second_route_lookup"
	unexpectedRouteLookup := false
	sentinel := errors.New("unexpected second catalog effective route lookup")
	require.NoError(t, srv.DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		query := tx.Statement.SQL.String()
		if tx.Statement.Table != "api_routes" || !strings.Contains(query, "api_service_id") || strings.Contains(query, "status") {
			return
		}
		unexpectedRouteLookup = true
		tx.AddError(sentinel)
	}))
	t.Cleanup(func() { _ = srv.DB.Callback().Query().Remove(callbackName) })
	effective := reqHelper(srv, adminJWT, http.MethodGet, "/api/api-catalog/effective?service_id="+jsonNumber(allowed.ID)+tokenQuery, nil)
	require.Equal(t, http.StatusOK, effective.Code, effective.Body.String())
	require.False(t, unexpectedRouteLookup)
	require.Equal(t, map[string]any{
		"scope":     "routes",
		"route_ids": []any{float64(grantedRoute.ID)},
	}, jsonBody(t, effective))
	routes := reqHelper(srv, adminJWT, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(allowed.ID)+tokenQuery, nil)
	routeRows := jsonBody(t, routes)["data"].([]any)
	require.Len(t, routeRows, 1)
	require.Equal(t, grantedRoute.ID, uint(routeRows[0].(map[string]any)["id"].(float64)))

	for _, path := range []string{
		"/api/api-catalog/services/detail?id=" + jsonNumber(hidden.ID) + tokenQuery,
		"/api/api-catalog/routes?service_id=" + jsonNumber(hidden.ID) + tokenQuery,
		"/api/api-catalog/effective?service_id=" + jsonNumber(hidden.ID) + tokenQuery,
	} {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
		require.Equal(t, "resource_not_found", jsonBody(t, response)["code"])
	}
}

// Break caught: a failure in any authorization fact must not yield a partial
// catalog response from one endpoint while another correctly fails closed.
func TestAPICatalogAuthorizationFactFailuresAreUnavailableAcrossEndpoints(t *testing.T) {
	endpoints := []struct {
		name      string
		pathForID func(serviceID, tokenID uint) string
	}{
		{name: "services", pathForID: func(_, tokenID uint) string {
			return "/api/api-catalog/services?token_id=" + jsonNumber(tokenID)
		}},
		{name: "detail", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/services/detail?id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
		{name: "routes", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/routes?service_id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
		{name: "effective", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/effective?service_id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
	}
	facts := []struct {
		name  string
		table string
	}{
		{name: "token", table: "tokens"},
		{name: "role binding", table: "role_bindings"},
		{name: "permission", table: "permissions"},
	}
	for _, fact := range facts {
		for _, endpoint := range endpoints {
			t.Run(fact.name+"/"+endpoint.name, func(t *testing.T) {
				srv, jwt, service, token := newCatalogScopeHTTPFixture(t)
				sentinel := errors.New("catalog " + fact.name + " read unavailable")
				callbackName := "test:catalog_authorization_failure_" + strings.ReplaceAll(fact.name, " ", "_") + "_" + endpoint.name
				fired := false
				require.NoError(t, srv.DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
					if tx.Statement.Table != fact.table {
						return
					}
					fired = true
					tx.AddError(sentinel)
				}))
				t.Cleanup(func() { _ = srv.DB.Callback().Query().Remove(callbackName) })

				response := reqHelper(srv, jwt, http.MethodGet, endpoint.pathForID(service.ID, token.ID), nil)
				require.True(t, fired, "failure injection did not reach %s", fact.table)
				require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
				require.Equal(t, "catalog_access_unavailable", jsonBody(t, response)["code"])
				require.NotContains(t, response.Body.String(), sentinel.Error())
			})
		}
	}
}

// Break caught: treating a catalog Service lookup outage as a missing resource
// hides a control-plane failure and makes callers retry the wrong condition.
func TestAPICatalogServiceLookupFailureReturnsStableUnavailable(t *testing.T) {
	for _, test := range []struct {
		name      string
		pathForID func(uint, uint) string
	}{
		{name: "detail", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/services/detail?id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
		{name: "routes", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/routes?service_id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
		{name: "effective", pathForID: func(serviceID, tokenID uint) string {
			return "/api/api-catalog/effective?service_id=" + jsonNumber(serviceID) + "&token_id=" + jsonNumber(tokenID)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv, jwt, service, token := newCatalogScopeHTTPFixture(t)
			sentinel := errors.New("catalog service lookup unavailable")
			callbackName := "test:catalog_service_lookup_failure_" + test.name
			fired := false
			require.NoError(t, srv.DB.Callback().Query().After("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table != "api_services" || !strings.Contains(tx.Statement.SQL.String(), "`api_services`.`id`") {
					return
				}
				fired = true
				tx.AddError(sentinel)
			}))
			t.Cleanup(func() { _ = srv.DB.Callback().Query().Remove(callbackName) })

			response := reqHelper(srv, jwt, http.MethodGet, test.pathForID(service.ID, token.ID), nil)
			require.True(t, fired, "failure injection did not reach APIService.GetByID")
			require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
			require.Equal(t, "catalog_access_unavailable", jsonBody(t, response)["code"])
			require.NotContains(t, response.Body.String(), sentinel.Error())
		})
	}
}

// behavior change: catalog list totals and pages are limited by the selected
// Token's invoke scope before search and pagination are applied.
func TestAPICatalogScopeFiltersServiceAndRoutePaginationBeforeSearch(t *testing.T) {
	srv := setupTestMaster(t)
	serviceWide := models.APIService{Slug: "scope-service-wide", Name: "Scope service wide", Status: consts.StatusEnabled}
	routeOnly := models.APIService{Slug: "scope-route-only", Name: "Scope route only", Status: consts.StatusEnabled}
	hidden := models.APIService{Slug: "scope-hidden-search", Name: "Scope hidden search", Status: consts.StatusEnabled}
	disabled := models.APIService{Slug: "scope-disabled", Name: "Scope disabled", Status: consts.StatusDisabled}
	services := []*models.APIService{&serviceWide, &routeOnly, &hidden, &disabled}
	for _, service := range services {
		require.NoError(t, srv.DB.Create(service).Error)
	}
	require.NoError(t, srv.DB.Model(&disabled).UpdateColumn("status", consts.StatusDisabled).Error)

	wideBackend := models.APIBackend{APIServiceID: serviceWide.ID, Name: "scope-wide-backend"}
	routeBackend := models.APIBackend{APIServiceID: routeOnly.ID, Name: "scope-route-backend"}
	require.NoError(t, srv.DB.Create(&wideBackend).Error)
	require.NoError(t, srv.DB.Create(&routeBackend).Error)
	wideFirst := models.APIRoute{APIServiceID: serviceWide.ID, BackendID: wideBackend.ID, Slug: "scope-wide-first", Status: consts.StatusEnabled}
	wideSecond := models.APIRoute{APIServiceID: serviceWide.ID, BackendID: wideBackend.ID, Slug: "scope-wide-second", Status: consts.StatusEnabled}
	wideDisabled := models.APIRoute{APIServiceID: serviceWide.ID, BackendID: wideBackend.ID, Slug: "scope-wide-disabled", Status: consts.StatusDisabled}
	routeGranted := models.APIRoute{APIServiceID: routeOnly.ID, BackendID: routeBackend.ID, Slug: "scope-route-granted", Status: consts.StatusEnabled}
	routeHidden := models.APIRoute{APIServiceID: routeOnly.ID, BackendID: routeBackend.ID, Slug: "scope-route-hidden-search", Status: consts.StatusEnabled}
	routes := []*models.APIRoute{&wideFirst, &wideSecond, &wideDisabled, &routeGranted, &routeHidden}
	for _, route := range routes {
		require.NoError(t, srv.DB.Create(route).Error)
	}
	require.NoError(t, srv.DB.Model(&wideDisabled).UpdateColumn("status", consts.StatusDisabled).Error)

	user := models.User{Username: "catalog-pagination-scope", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "catalog-pagination-scope", Name: "scope", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	emptyToken := models.Token{UserID: user.ID, Key: "catalog-pagination-empty", Name: "empty", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&token).Error)
	require.NoError(t, srv.DB.Create(&emptyToken).Error)
	role := models.Role{Key: "catalog-pagination-invoke", Name: "Catalog pagination invoke", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	for _, permission := range []models.Permission{
		{Resource: models.APIResourceService, ResourceID: serviceWide.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: routeGranted.ID, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceService, ResourceID: disabled.ID, Action: models.APIPermissionInvoke},
	} {
		permission := permission
		require.NoError(t, srv.DB.Create(&permission).Error)
		require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	}
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error)
	jwt, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)

	serviceIDs := map[uint]struct{}{}
	for _, page := range []string{"1", "2"} {
		response := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(token.ID)+"&page="+page+"&page_size=1", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := jsonBody(t, response)
		require.EqualValues(t, 2, body["total"], "scope must be applied before service pagination")
		rows := body["data"].([]any)
		require.Len(t, rows, 1)
		serviceIDs[uint(rows[0].(map[string]any)["id"].(float64))] = struct{}{}
	}
	require.Equal(t, map[uint]struct{}{serviceWide.ID: {}, routeOnly.ID: {}}, serviceIDs)

	hiddenServiceSearch := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(token.ID)+"&search=hidden", nil)
	require.Equal(t, http.StatusOK, hiddenServiceSearch.Code, hiddenServiceSearch.Body.String())
	require.Zero(t, jsonBody(t, hiddenServiceSearch)["total"], "an unauthorized search match must not affect service total")

	for _, page := range []string{"1", "2"} {
		response := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(serviceWide.ID)+"&token_id="+jsonNumber(token.ID)+"&page="+page+"&page_size=1", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		body := jsonBody(t, response)
		require.EqualValues(t, 2, body["total"], "a service grant must include every enabled route before pagination")
		require.Len(t, body["data"].([]any), 1)
	}

	routePage := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(routeOnly.ID)+"&token_id="+jsonNumber(token.ID), nil)
	require.Equal(t, http.StatusOK, routePage.Code, routePage.Body.String())
	routePageBody := jsonBody(t, routePage)
	require.EqualValues(t, 1, routePageBody["total"], "a route-only grant must exclude sibling routes")
	require.Equal(t, routeGranted.ID, uint(routePageBody["data"].([]any)[0].(map[string]any)["id"].(float64)))

	hiddenRouteSearch := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/routes?service_id="+jsonNumber(routeOnly.ID)+"&token_id="+jsonNumber(token.ID)+"&search=hidden", nil)
	require.Equal(t, http.StatusOK, hiddenRouteSearch.Code, hiddenRouteSearch.Body.String())
	require.Zero(t, jsonBody(t, hiddenRouteSearch)["total"], "an unauthorized route search match must not affect route total")

	emptyPage := reqHelper(srv, jwt, http.MethodGet, "/api/api-catalog/services?token_id="+jsonNumber(emptyToken.ID), nil)
	require.Equal(t, http.StatusOK, emptyPage.Code, emptyPage.Body.String())
	emptyBody := jsonBody(t, emptyPage)
	require.Empty(t, emptyBody["data"].([]any))
	require.Zero(t, emptyBody["total"])
}

func newCatalogScopeHTTPFixture(t *testing.T) (*master.Server, string, models.APIService, models.Token) {
	t.Helper()
	srv := setupTestMaster(t)
	service := models.APIService{Slug: "lookup-failure-service", Name: "Lookup failure service", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "lookup-failure-origin"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	require.NoError(t, srv.DB.Create(&models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "lookup-failure-route", Status: consts.StatusEnabled}).Error)
	user := models.User{Username: "lookup-failure-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "lookup-failure-token", Name: "lookup-failure", Status: consts.StatusEnabled, ExpiredAt: -1, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&token).Error)
	role := models.Role{Key: "lookup-failure-invoke", Name: "Lookup failure invoke", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID}).Error)
	jwt, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	return srv, jwt, service, token
}

func sortedKeys(row map[string]any) []string {
	keys := make([]string, 0, len(row))
	for key := range row {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func jsonNumber(value uint) string {
	return strconv.FormatUint(uint64(value), 10)
}
