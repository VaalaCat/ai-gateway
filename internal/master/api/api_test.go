package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func setupTestMaster(t *testing.T) *master.Server {
	t.Helper()
	logger, _ := zap.NewDevelopment()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
			DBPath:    ":memory:",
			JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{
			CredentialsFile: filepath.Join(t.TempDir(), "embedded-agent.json"),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := master.New(cfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	var sqlDB *sql.DB
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown test master: %v", err)
		}
		if srv.Bus != nil {
			if err := srv.Bus.Close(); err != nil {
				t.Errorf("close test master event bus: %v", err)
			}
		}
		if sqlDB != nil {
			if err := sqlDB.Close(); err != nil {
				t.Errorf("close test master database: %v", err)
			}
		}
	})
	sqlDB, err = srv.DB.DB()
	if err != nil {
		t.Fatalf("get test master database: %v", err)
	}
	return srv
}

func loginAsAdmin(t *testing.T, srv *master.Server, user, pwd string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": user, "password": pwd})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return resp["token"]
}

// behavior change: generic API management is an admin control plane. The new
// path must reject anonymous/non-admin callers and the old portal path must be
// gone instead of remaining as a compatibility alias.
func TestAPIServiceRouteUsesAdminPathAndJSONBinder(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))

	legacy := reqHelper(srv, "", http.MethodPost, "/api/api-services", map[string]any{
		"slug": "weather", "name": "Weather",
	})
	require.Equal(t, http.StatusNotFound, legacy.Code, legacy.Body.String())

	unauthenticated := reqHelper(srv, "", http.MethodPost, "/api/admin/api-services", map[string]any{
		"slug": "weather", "name": "Weather",
	})
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())

	user := models.User{Username: "generic-api-control-plane-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	nonAdmin := reqHelper(srv, userJWT, http.MethodPost, "/api/admin/api-services", map[string]any{
		"slug": "weather", "name": "Weather",
	})
	require.Equal(t, http.StatusForbidden, nonAdmin.Code, nonAdmin.Body.String())

	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	invalid := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-services", map[string]any{
		"slug": "weather",
	})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-services", map[string]any{
		"slug": "weather", "name": "Weather",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
}

// Break caught: registering any Generic API management surface outside the
// admin group can leave one resource anonymously reachable, invoke-authorized,
// or accidentally available on its former portal path.
func TestGenericAPIManagementRouteIsolationMatrix(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)

	service := models.APIService{Slug: "route-isolation", Name: "Route isolation", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "route-isolation"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "route-isolation", APIServiceID: service.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: "route-isolation"}).Error)

	user := models.User{Username: "route-isolation-invoker", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "route-isolation-invoker", Name: "Route isolation invoker", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	tests := []struct {
		name             string
		legacyPath       string
		adminPath        string
		legacyStatusCode int
	}{
		{name: "service", legacyPath: "/api/api-services", adminPath: "/api/admin/api-services", legacyStatusCode: http.StatusNotFound},
		{name: "backend", legacyPath: fmt.Sprintf("/api/api-backends?api_service_id=%d", service.ID), adminPath: fmt.Sprintf("/api/admin/api-backends?api_service_id=%d", service.ID), legacyStatusCode: http.StatusNotFound},
		{name: "route", legacyPath: fmt.Sprintf("/api/api-routes?api_service_id=%d", service.ID), adminPath: fmt.Sprintf("/api/admin/api-routes?api_service_id=%d", service.ID), legacyStatusCode: http.StatusNotFound},
		{name: "upstream", legacyPath: fmt.Sprintf("/api/api-upstreams?backend_id=%d", backend.ID), adminPath: fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d", backend.ID), legacyStatusCode: http.StatusNotFound},
		// behavior change: request logs now have a separate authenticated, current-user scoped portal route.
		{name: "request log", legacyPath: "/api/api-request-logs", adminPath: "/api/admin/api-request-logs", legacyStatusCode: http.StatusOK},
		{name: "trace", legacyPath: "/api/api-request-traces?request_id=route-isolation", adminPath: "/api/admin/api-request-traces?request_id=route-isolation", legacyStatusCode: http.StatusNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			legacy := reqHelper(srv, adminJWT, http.MethodGet, test.legacyPath, nil)
			require.Equal(t, test.legacyStatusCode, legacy.Code, legacy.Body.String())
			anonymous := reqHelper(srv, "", http.MethodGet, test.adminPath, nil)
			require.Equal(t, http.StatusUnauthorized, anonymous.Code, anonymous.Body.String())
			invokeUser := reqHelper(srv, userJWT, http.MethodGet, test.adminPath, nil)
			require.Equal(t, http.StatusForbidden, invokeUser.Code, invokeUser.Body.String())
			admin := reqHelper(srv, adminJWT, http.MethodGet, test.adminPath, nil)
			require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
		})
	}
}

// behavior change: an invoke grant does not expose the admin service list.
func TestAPIServiceListIsAdminOnly(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	user := models.User{Username: "api-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "api-reader", Name: "API Reader", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)

	jwt, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, consts.RoleUser, user.Username, "", "")
	require.NoError(t, err)
	require.NoError(t, srv.DB.Migrator().DropTable(&models.RoleBinding{}))
	unauthenticated := reqHelper(srv, "", http.MethodGet, "/api/admin/api-services", nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
	forbidden := reqHelper(srv, jwt, http.MethodGet, "/api/admin/api-services", nil)
	require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())
	response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-services", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data []models.APIService `json:"data"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, []models.APIService{service}, body.Data)
}

// Break caught: bypassing the model normalization in the management handler
// would persist lower-case methods or a missing HTTP protocol; passing an
// unknown protocol through would create a route Agents cannot execute.
func TestAPIRouteCreateDefaultsProtocolsAndNormalizesMethods(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
		"api_service_id": service.ID, "slug": "forecast", "allowed_methods": []string{"post", "get"},
		"target": map[string]any{
			"mode": "create", "backend": map[string]any{"name": "primary"},
			"first_upstream": map[string]any{"name": "origin", "base_url": "https://upstream.example"},
		},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var route models.APIRoute
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &route))
	require.Equal(t, []models.APIProtocol{models.APIProtocolHTTP}, []models.APIProtocol(route.Protocols))
	require.Equal(t, []string{"GET", "POST"}, []string(route.AllowedMethods))

	invalid := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
		"api_service_id": service.ID, "slug": "broken", "protocols": []string{"smtp"},
		"target": map[string]any{
			"mode": "create", "backend": map[string]any{"name": "broken"},
			"first_upstream": map[string]any{"name": "origin", "base_url": "https://upstream.example"},
		},
	})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
}

// Break caught: an empty route slug represents the service root, so the HTTP
// binder must admit it for route create and preview without weakening service
// slug validation or the one-root-route-per-service database constraint.
func TestAPIRootRouteControlPlaneCreateAndPreview(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "root-control-plane", Name: "Root Control Plane", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	upstream := models.APIUpstream{
		BackendID: backend.ID, Name: "origin", BaseURL: "https://upstream.example",
		AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled, Priority: 1, Weight: 1,
	}
	require.NoError(t, srv.DB.Create(&upstream).Error)
	target := map[string]any{"mode": "existing", "backend_id": backend.ID}

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
		"api_service_id": service.ID, "slug": "", "target": target,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var root models.APIRoute
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &root))
	require.Empty(t, root.Slug)

	duplicate := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
		"api_service_id": service.ID, "slug": "", "target": target,
	})
	require.Equal(t, http.StatusBadRequest, duplicate.Code, duplicate.Body.String())

	preview := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes/preview", map[string]any{
		"api_service_id": service.ID, "slug": "", "target": target,
		"sample": map[string]any{"method": http.MethodGet, "subpath": "/accounts"},
	})
	require.Equal(t, http.StatusOK, preview.Code, preview.Body.String())

	invalidService := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-services", map[string]any{
		"slug": "", "name": "Invalid Empty Service",
	})
	require.Equal(t, http.StatusBadRequest, invalidService.Code, invalidService.Body.String())
}

func TestRouteTargetCreateAndSwitchAreAtomic(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "routing", Name: "Routing", Status: consts.StatusEnabled}
	otherService := models.APIService{Slug: "private", Name: "Private", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	require.NoError(t, srv.DB.Create(&otherService).Error)
	foreign := models.APIBackend{APIServiceID: otherService.ID, Name: "foreign"}
	require.NoError(t, srv.DB.Create(&foreign).Error)

	create := func(slug, backendName, upstreamName string) *httptest.ResponseRecorder {
		return reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
			"api_service_id": service.ID, "slug": slug,
			"target": map[string]any{
				"mode": "create", "backend": map[string]any{"name": backendName},
				"first_upstream": map[string]any{"name": upstreamName, "base_url": "https://origin.example"},
			},
		})
	}

	created := create("forecast", "primary", "origin-a")
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var route models.APIRoute
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &route))
	var backend models.APIBackend
	require.NoError(t, srv.DB.First(&backend, route.BackendID).Error)
	require.Equal(t, service.ID, backend.APIServiceID)
	var upstream models.APIUpstream
	require.NoError(t, srv.DB.Where("backend_id = ?", backend.ID).First(&upstream).Error)
	require.Equal(t, "origin-a", upstream.Name)

	crossService := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", map[string]any{
		"api_service_id": service.ID, "slug": "denied", "target": map[string]any{"mode": "existing", "backend_id": foreign.ID},
	})
	require.Equal(t, http.StatusBadRequest, crossService.Code, crossService.Body.String())

	duplicate := create("duplicate", "primary", "origin-b")
	// behavior change: target.create reports the same stable conflict as the
	// standalone backend create endpoint.
	require.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())
	require.Equal(t, "backend_name_conflict", jsonBody(t, duplicate)["code"])
	var duplicateRoutes, duplicateUpstreams int64
	require.NoError(t, srv.DB.Model(&models.APIRoute{}).Where("api_service_id = ? AND slug = ?", service.ID, "duplicate").Count(&duplicateRoutes).Error)
	require.Zero(t, duplicateRoutes)
	require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("name = ?", "origin-b").Count(&duplicateUpstreams).Error)
	require.Zero(t, duplicateUpstreams)

	switched := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), map[string]any{
		"target": map[string]any{
			"mode": "create", "backend": map[string]any{"name": "secondary"},
			"first_upstream": map[string]any{"name": "origin-b", "base_url": "https://secondary.example"},
		},
	})
	require.Equal(t, http.StatusOK, switched.Code, switched.Body.String())
	var switchedRoute models.APIRoute
	require.NoError(t, srv.DB.First(&switchedRoute, route.ID).Error)
	require.NotEqual(t, backend.ID, switchedRoute.BackendID)
	var switchedUpstream models.APIUpstream
	require.NoError(t, srv.DB.Where("backend_id = ?", switchedRoute.BackendID).First(&switchedUpstream).Error)
	require.Equal(t, "origin-b", switchedUpstream.Name)
}

// Break caught: flattening route write errors into a generic 400 hides stable
// validation codes and turns target conflicts or missing backends into the
// wrong HTTP status for management clients.
func TestAPIRouteWriteErrorsPreserveStatusAndCode(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "write-errors", Name: "Write Errors", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "existing", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&route).Error)

	create := func(slug string, target map[string]any, example map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{"api_service_id": service.ID, "slug": slug, "target": target}
		if example != nil {
			body["example_request"] = example
		}
		return reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-routes", body)
	}
	update := func(target map[string]any, example map[string]any) *httptest.ResponseRecorder {
		body := make(map[string]any)
		if target != nil {
			body["target"] = target
		}
		if example != nil {
			body["example_request"] = example
		}
		return reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), body)
	}
	duplicateTarget := map[string]any{
		"mode": "create", "backend": map[string]any{"name": "primary"},
		"first_upstream": map[string]any{"name": "origin", "base_url": "https://upstream.example"},
	}
	missingTarget := map[string]any{"mode": "existing", "backend_id": 999_999}
	invalidExample := map[string]any{"method": "GET", "subpath": "/../admin"}

	for _, test := range []struct {
		name       string
		request    func() *httptest.ResponseRecorder
		wantStatus int
		wantCode   string
	}{
		{name: "create invalid example", request: func() *httptest.ResponseRecorder {
			return create("invalid-example", map[string]any{"mode": "existing", "backend_id": backend.ID}, invalidExample)
		}, wantStatus: http.StatusBadRequest, wantCode: "invalid_example_subpath"},
		{name: "update invalid example", request: func() *httptest.ResponseRecorder {
			return update(nil, invalidExample)
		}, wantStatus: http.StatusBadRequest, wantCode: "invalid_example_subpath"},
		{name: "create duplicate backend", request: func() *httptest.ResponseRecorder {
			return create("duplicate-backend", duplicateTarget, nil)
		}, wantStatus: http.StatusConflict, wantCode: "backend_name_conflict"},
		{name: "update duplicate backend", request: func() *httptest.ResponseRecorder {
			return update(duplicateTarget, nil)
		}, wantStatus: http.StatusConflict, wantCode: "backend_name_conflict"},
		{name: "create missing backend", request: func() *httptest.ResponseRecorder {
			return create("missing-backend", missingTarget, nil)
		}, wantStatus: http.StatusNotFound},
		{name: "update missing backend", request: func() *httptest.ResponseRecorder {
			return update(missingTarget, nil)
		}, wantStatus: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := test.request()
			require.Equal(t, test.wantStatus, response.Code, response.Body.String())
			if test.wantCode != "" {
				require.Equal(t, test.wantCode, jsonBody(t, response)["code"])
			}
		})
	}
}

// Break caught: ordinary encoding/json replaces raw invalid UTF-8 with U+FFFD,
// so route examples could otherwise be mutated and persisted instead of being
// rejected at the HTTP boundary.
func TestAPIRouteHTTPRejectsInvalidUTF8BeforePersistence(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "strict-route-json", Name: "Strict Route JSON", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "existing", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&route).Error)

	doRawJSON := func(method, path string, payload []byte) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adminJWT)
		srv.Router.ServeHTTP(response, request)
		return response
	}
	invalidString := func(prefix, suffix string) []byte {
		payload := append([]byte(prefix), 0xff)
		return append(payload, []byte(suffix)...)
	}

	createPayload := invalidString(
		fmt.Sprintf(`{"api_service_id":%d,"slug":"invalid-utf8","target":{"mode":"existing","backend_id":%d},"example_request":{"method":"GET","body":"`, service.ID, backend.ID),
		`"}}`,
	)
	created := doRawJSON(http.MethodPost, "/api/admin/api-routes", createPayload)
	require.Equal(t, http.StatusBadRequest, created.Code, created.Body.String())
	var createdCount int64
	require.NoError(t, srv.DB.Model(&models.APIRoute{}).Where("slug = ?", "invalid-utf8").Count(&createdCount).Error)
	require.Zero(t, createdCount)

	updatePayload := invalidString(`{"example_request":{"method":"GET","body":"`, `"}}`)
	updated := doRawJSON(http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), updatePayload)
	require.Equal(t, http.StatusBadRequest, updated.Code, updated.Body.String())
	var persisted models.APIRoute
	require.NoError(t, srv.DB.First(&persisted, route.ID).Error)
	require.Equal(t, models.APIRequestExample{}, persisted.ExampleRequest.Data())
}

// Break caught: serializing model.APIUpstream directly would return ciphertext
// or plaintext credentials. Omitted credentials on update must not erase the
// existing encrypted value.
func TestAPIUpstreamManagementNeverLeaksOrErasesOmittedCredentials(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	secret := "bearer-secret"
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
		"backend_id": backend.ID, "name": "primary", "base_url": "https://upstream.example",
		"auth_type": "bearer", "credential": map[string]any{"bearer_token": secret},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.NotContains(t, created.Body.String(), secret)
	var response struct {
		ID                   uint `json:"id"`
		CredentialConfigured bool `json:"credential_configured"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	require.True(t, response.CredentialConfigured)

	updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{"name": "renamed"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var stored models.APIUpstream
	require.NoError(t, srv.DB.First(&stored, response.ID).Error)
	require.NotEmpty(t, stored.CredentialCiphertext)
	got := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), nil)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	require.NotContains(t, got.Body.String(), secret)
	require.NotContains(t, got.Body.String(), stored.CredentialCiphertext)
}

// Break caught: splitting ordinary fields and secrets across writes leaves an
// orphan/create or a partial update when credentials are invalid, and permits
// auth_type changes whose resulting credential cannot be projected to Agents.
func TestAPIUpstreamWriteIsAtomicAcrossSecretsAndAuthTransitions(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)

	invalidCreate := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
		"backend_id": backend.ID, "name": "broken", "base_url": "https://upstream.example", "auth_type": "bearer",
		"credential": map[string]any{"header_name": "X-Wrong", "header_value": "not-bearer"},
	})
	require.Equal(t, http.StatusBadRequest, invalidCreate.Code, invalidCreate.Body.String())
	var count int64
	require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("name = ?", "broken").Count(&count).Error)
	require.Zero(t, count)

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
		"backend_id": backend.ID, "name": "primary", "base_url": "https://upstream.example", "auth_type": "bearer",
		"credential": map[string]any{"bearer_token": "valid-secret"},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		ID uint `json:"id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))

	invalidUpdate := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{
		"name": "must-not-persist", "credential": map[string]any{"header_name": "X-Wrong", "header_value": "not-bearer"},
	})
	require.Equal(t, http.StatusBadRequest, invalidUpdate.Code, invalidUpdate.Body.String())
	var stored models.APIUpstream
	require.NoError(t, srv.DB.First(&stored, response.ID).Error)
	require.Equal(t, "primary", stored.Name)
	require.Equal(t, models.APIUpstreamAuthBearer, stored.AuthType)
	require.NotEmpty(t, stored.CredentialCiphertext)

	transition := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{"auth_type": "none"})
	require.Equal(t, http.StatusBadRequest, transition.Code, transition.Body.String())
	require.NoError(t, srv.DB.First(&stored, response.ID).Error)
	require.Equal(t, models.APIUpstreamAuthBearer, stored.AuthType)

	cleared := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{
		"auth_type": "none", "credential": map[string]any{},
	})
	require.Equal(t, http.StatusOK, cleared.Code, cleared.Body.String())
	require.NoError(t, srv.DB.First(&stored, response.ID).Error)
	require.Equal(t, models.APIUpstreamAuthNone, stored.AuthType)
	require.Empty(t, stored.CredentialCiphertext)

	transition = reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{"auth_type": "bearer"})
	require.Equal(t, http.StatusBadRequest, transition.Code, transition.Body.String())
	require.NoError(t, srv.DB.First(&stored, response.ID).Error)
	require.Equal(t, models.APIUpstreamAuthNone, stored.AuthType)
}

func TestAPIUpstreamUpdateRejectsUnsafeTransportMetadataWithoutMutation(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "upstream-update-safety", Name: "Upstream Update Safety", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
		"backend_id": backend.ID, "name": "primary", "base_url": "https://upstream.example",
		"header_override": map[string]any{"X-Tenant": "original"}, "proxy_url": "http://proxy.example:3128",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		ID uint `json:"id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	var before models.APIUpstream
	require.NoError(t, srv.DB.First(&before, response.ID).Error)

	for _, test := range []struct {
		name  string
		patch map[string]any
	}{
		{name: "hop by hop header", patch: map[string]any{"header_override": map[string]any{"Connection": "close"}}},
		{name: "forwarded header", patch: map[string]any{"header_override": map[string]any{"X-Forwarded-For": "127.0.0.1"}}},
		{name: "gateway internal header", patch: map[string]any{"header_override": map[string]any{"X-Vaala-Trace": "internal"}}},
		{name: "canonical duplicate header", patch: map[string]any{"header_override": map[string]any{"X-Tenant": "one", "x-tenant": "two"}}},
		{name: "malformed proxy", patch: map[string]any{"proxy_url": "://bad"}},
		{name: "proxy userinfo", patch: map[string]any{"proxy_url": "http://user:secret@proxy.example:3128"}},
		{name: "non HTTP proxy", patch: map[string]any{"proxy_url": "socks5://proxy.example:1080"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), test.patch)
			require.Equal(t, http.StatusBadRequest, updated.Code, updated.Body.String())
			var after models.APIUpstream
			require.NoError(t, srv.DB.First(&after, response.ID).Error)
			require.Equal(t, before.Name, after.Name)
			require.Equal(t, before.HeaderOverride.Data(), after.HeaderOverride.Data())
			require.Equal(t, before.ProxyURLCiphertext, after.ProxyURLCiphertext)
		})
	}
	cleared := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{
		"header_override": map[string]any{}, "proxy_url": "",
	})
	require.Equal(t, http.StatusOK, cleared.Code, cleared.Body.String())
	var afterClear models.APIUpstream
	require.NoError(t, srv.DB.First(&afterClear, response.ID).Error)
	require.Empty(t, afterClear.HeaderOverride.Data())
	require.Empty(t, afterClear.ProxyURLCiphertext)
}

func TestAPIUpstreamPostWriteFailuresRollback(t *testing.T) {
	setup := func(t *testing.T) (*master.Server, string, models.APIBackend) {
		t.Helper()
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		service := models.APIService{Slug: "rollback", Name: "Rollback", Status: consts.StatusEnabled}
		require.NoError(t, srv.DB.Create(&service).Error)
		backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
		require.NoError(t, srv.DB.Create(&backend).Error)
		return srv, loginAsAdmin(t, srv, "admin", "admin123"), backend
	}

	t.Run("create rolls back row when secret update fails", func(t *testing.T) {
		srv, jwt, backend := setup(t)
		failure := errors.New("force secret update failure")
		fired := false
		const callbackName = "test:fail_api_upstream_create_secret_update"
		processor := srv.DB.Callback().Update()
		require.NoError(t, processor.Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			if fired || testCallbackTableName(tx) != "api_upstreams" {
				return
			}
			fired = true
			_ = tx.AddError(failure)
		}))
		t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

		response := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
			"backend_id": backend.ID, "name": "must-rollback", "base_url": "https://rollback.example",
			"auth_type": "bearer", "credential": map[string]any{"bearer_token": "new-secret"},
		})

		require.True(t, fired)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		var count int64
		require.NoError(t, srv.DB.Model(&models.APIUpstream{}).
			Where("backend_id = ? AND name = ?", backend.ID, "must-rollback").Count(&count).Error)
		require.Zero(t, count)
	})

	t.Run("update rolls back fields when secret update fails", func(t *testing.T) {
		srv, jwt, backend := setup(t)
		created := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
			"backend_id": backend.ID, "name": "original", "base_url": "https://rollback.example",
			"auth_type": "bearer", "credential": map[string]any{"bearer_token": "old-secret"},
		})
		require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
		var createdBody struct {
			ID uint `json:"id"`
		}
		require.NoError(t, json.Unmarshal(created.Body.Bytes(), &createdBody))
		var before models.APIUpstream
		require.NoError(t, srv.DB.First(&before, createdBody.ID).Error)

		failure := errors.New("force second upstream update failure")
		updates := 0
		const callbackName = "test:fail_api_upstream_second_update"
		processor := srv.DB.Callback().Update()
		require.NoError(t, processor.Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			if testCallbackTableName(tx) != "api_upstreams" {
				return
			}
			updates++
			if updates == 2 {
				_ = tx.AddError(failure)
			}
		}))
		t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

		response := reqHelper(srv, jwt, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", before.ID), map[string]any{
			"name": "must-not-persist", "credential": map[string]any{"bearer_token": "new-secret"},
		})

		require.Equal(t, 2, updates)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		var after models.APIUpstream
		require.NoError(t, srv.DB.First(&after, before.ID).Error)
		require.Equal(t, before.Name, after.Name)
		require.Equal(t, before.CredentialCiphertext, after.CredentialCiphertext)
	})
}

// behavior change: backend CRUD is guarded by the admin route group rather
// than service-scoped read/manage permissions inside the Handler.
func TestAPIBackendCRUDUsesAdminControlPlane(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	owned := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	foreign := models.APIService{Slug: "calendar", Name: "Calendar", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&owned).Error)
	require.NoError(t, srv.DB.Create(&foreign).Error)
	foreignBackend := models.APIBackend{APIServiceID: foreign.ID, Name: "private"}
	require.NoError(t, srv.DB.Create(&foreignBackend).Error)

	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-backends", map[string]any{"api_service_id": owned.ID, "name": "primary"})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var primary struct {
		ID                   uint     `json:"id"`
		APIServiceID         uint     `json:"api_service_id"`
		RouteCount           int64    `json:"route_count"`
		UpstreamCount        int64    `json:"upstream_count"`
		EnabledUpstreamCount int64    `json:"enabled_upstream_count"`
		EndpointHosts        []string `json:"endpoint_hosts"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &primary))
	require.Equal(t, owned.ID, primary.APIServiceID)
	require.Zero(t, primary.RouteCount)
	require.Zero(t, primary.UpstreamCount)
	require.Zero(t, primary.EnabledUpstreamCount)
	require.Empty(t, primary.EndpointHosts)

	duplicate := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-backends", map[string]any{"api_service_id": owned.ID, "name": "primary"})
	require.Equal(t, http.StatusConflict, duplicate.Code, duplicate.Body.String())
	require.Equal(t, "backend_name_conflict", jsonBody(t, duplicate)["code"])
	updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-backends/%d", primary.ID), map[string]any{"name": "renamed"})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var persistedPrimary models.APIBackend
	require.NoError(t, srv.DB.First(&persistedPrimary, primary.ID).Error)
	require.Equal(t, "renamed", persistedPrimary.Name)

	for _, path := range []string{
		fmt.Sprintf("/api/admin/api-backends?api_service_id=%d", foreign.ID),
		fmt.Sprintf("/api/admin/api-backends/%d", foreignBackend.ID),
	} {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	}
	for _, body := range []map[string]any{
		{"api_service_id": foreign.ID, "name": "foreign-one"},
		{"api_service_id": foreign.ID, "name": "foreign-two"},
	} {
		response := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-backends", body)
		require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
	}

	read := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-backends/%d", primary.ID), nil)
	require.Equal(t, http.StatusOK, read.Code, read.Body.String())
	additional := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-backends", map[string]any{"api_service_id": owned.ID, "name": "additional"})
	require.Equal(t, http.StatusCreated, additional.Code, additional.Body.String())

	linkedRoute := models.APIRoute{APIServiceID: owned.ID, BackendID: primary.ID, Slug: "forecast", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&linkedRoute).Error)
	inUse := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", primary.ID), nil)
	require.Equal(t, http.StatusConflict, inUse.Code, inUse.Body.String())
	inUseBody := jsonBody(t, inUse)
	require.Equal(t, "backend_in_use", inUseBody["code"])
	require.Equal(t, float64(1), inUseBody["details"].(map[string]any)["route_count"])

	removable := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-backends", map[string]any{"api_service_id": owned.ID, "name": "removable"})
	require.Equal(t, http.StatusCreated, removable.Code, removable.Body.String())
	var removableBody struct {
		ID uint `json:"id"`
	}
	require.NoError(t, json.Unmarshal(removable.Body.Bytes(), &removableBody))
	upstream := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": removableBody.ID, "name": "endpoint", "base_url": "https://api.example.com"})
	require.Equal(t, http.StatusCreated, upstream.Code, upstream.Body.String())
	deleted := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", removableBody.ID), nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	var count int64
	require.NoError(t, srv.DB.Model(&models.APIBackend{}).Where("id = ?", removableBody.ID).Count(&count).Error)
	require.Zero(t, count)
	require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("backend_id = ?", removableBody.ID).Count(&count).Error)
	require.Zero(t, count)
}

func TestAPIBackendDeletePublishesEveryCommittedUpstream(t *testing.T) {
	setup := func(t *testing.T) (*master.Server, string, models.APIBackend, []models.APIUpstream) {
		t.Helper()
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		service := models.APIService{Slug: "delete", Name: "Delete", Status: consts.StatusEnabled}
		require.NoError(t, srv.DB.Create(&service).Error)
		backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
		require.NoError(t, srv.DB.Create(&backend).Error)
		upstreams := []models.APIUpstream{
			{BackendID: backend.ID, Name: "one", BaseURL: "https://one.example", Weight: 1, AuthType: models.APIUpstreamAuthNone},
			{BackendID: backend.ID, Name: "two", BaseURL: "https://two.example", Weight: 1, AuthType: models.APIUpstreamAuthNone},
			{BackendID: backend.ID, Name: "three", BaseURL: "https://three.example", Weight: 1, AuthType: models.APIUpstreamAuthNone},
		}
		for i := range upstreams {
			require.NoError(t, srv.DB.Create(&upstreams[i]).Error)
		}
		return srv, loginAsAdmin(t, srv, "admin", "admin123"), backend, upstreams
	}

	t.Run("continues after a middle publish failure", func(t *testing.T) {
		srv, jwt, backend, upstreams := setup(t)
		published := make([]uint, 0, len(upstreams))
		subscription, err := events.Subscribe(srv.Bus, events.APIUpstreamDeleteTopic, func(_ context.Context, payload protocol.SyncedAPIUpstream) error {
			published = append(published, payload.ID)
			if len(published) == 2 {
				return errors.New("force middle publish failure")
			}
			return nil
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = subscription.Unsubscribe() })

		response := reqHelper(srv, jwt, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", backend.ID), nil)

		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		require.Equal(t, "publish API upstream failed", jsonBody(t, response)["error"])
		require.ElementsMatch(t, []uint{upstreams[0].ID, upstreams[1].ID, upstreams[2].ID}, published)
	})

	t.Run("publishes every upstream after success", func(t *testing.T) {
		srv, jwt, backend, upstreams := setup(t)
		published := make([]uint, 0, len(upstreams))
		subscription, err := events.Subscribe(srv.Bus, events.APIUpstreamDeleteTopic, func(_ context.Context, payload protocol.SyncedAPIUpstream) error {
			published = append(published, payload.ID)
			return nil
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = subscription.Unsubscribe() })

		response := reqHelper(srv, jwt, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", backend.ID), nil)

		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		require.ElementsMatch(t, []uint{upstreams[0].ID, upstreams[1].ID, upstreams[2].ID}, published)
	})

	t.Run("publishes nothing when delete transaction rolls back", func(t *testing.T) {
		srv, jwt, backend, upstreams := setup(t)
		published := make([]uint, 0, len(upstreams))
		subscription, err := events.Subscribe(srv.Bus, events.APIUpstreamDeleteTopic, func(_ context.Context, payload protocol.SyncedAPIUpstream) error {
			published = append(published, payload.ID)
			return nil
		})
		require.NoError(t, err)
		t.Cleanup(func() { _ = subscription.Unsubscribe() })
		const callbackName = "test:fail_api_backend_delete"
		processor := srv.DB.Callback().Delete()
		require.NoError(t, processor.Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
			if testCallbackTableName(tx) == "api_backends" {
				_ = tx.AddError(errors.New("force API backend delete rollback"))
			}
		}))
		t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

		response := reqHelper(srv, jwt, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", backend.ID), nil)

		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
		require.Empty(t, published)
		var backendCount, upstreamCount int64
		require.NoError(t, srv.DB.Model(&models.APIBackend{}).Where("id = ?", backend.ID).Count(&backendCount).Error)
		require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("backend_id = ?", backend.ID).Count(&upstreamCount).Error)
		require.EqualValues(t, 1, backendCount)
		require.EqualValues(t, len(upstreams), upstreamCount)
	})
}

func TestAPIBackendDeleteDoesNotMisclassifyUnrelatedDAOErrors(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	jwt := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "delete-error", Name: "Delete error", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	require.NoError(t, srv.DB.Create(&models.APIRoute{
		APIServiceID: service.ID, BackendID: backend.ID, Slug: "attached", Status: consts.StatusEnabled,
	}).Error)

	fired := false
	const callbackName = "test:fail_first_api_backend_route_count"
	processor := srv.DB.Callback().Query()
	require.NoError(t, processor.Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if fired || testCallbackTableName(tx) != "api_routes" {
			return
		}
		fired = true
		_ = tx.AddError(errors.New("force unrelated route count failure"))
	}))
	t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

	response := reqHelper(srv, jwt, http.MethodDelete, fmt.Sprintf("/api/admin/api-backends/%d", backend.ID), nil)

	require.True(t, fired)
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Equal(t, "delete API backend failed", jsonBody(t, response)["error"])
}

// Break caught: accepting the former api_service_id ownership input can create
// an upstream detached from its actual target pool; backend reassignment would
// silently change route behavior across a live configuration.
func TestAPIUpstreamUsesBackendOwnershipContract(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	foreignService := models.APIService{Slug: "calendar", Name: "Calendar", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	require.NoError(t, srv.DB.Create(&foreignService).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	foreignBackend := models.APIBackend{APIServiceID: foreignService.ID, Name: "private"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	require.NoError(t, srv.DB.Create(&foreignBackend).Error)

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{
		"backend_id": backend.ID, "name": "primary", "base_url": "https://api.example.com",
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var response struct {
		ID        uint `json:"id"`
		BackendID uint `json:"backend_id"`
	}
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &response))
	require.Equal(t, backend.ID, response.BackendID)

	zero := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": 0, "name": "zero", "base_url": "https://api.example.com"})
	require.Equal(t, http.StatusBadRequest, zero.Code, zero.Body.String())
	missing := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": 999999, "name": "missing", "base_url": "https://api.example.com"})
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())

	crossService := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": foreignBackend.ID, "name": "foreign", "base_url": "https://api.example.com"})
	require.Equal(t, http.StatusCreated, crossService.Code, crossService.Body.String())

	update := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-upstreams/%d", response.ID), map[string]any{"backend_id": foreignBackend.ID})
	require.Equal(t, http.StatusBadRequest, update.Code, update.Body.String())
	require.Equal(t, "backend_id_immutable", jsonBody(t, update)["code"])

	list := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d", backend.ID), nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var listBody struct {
		Data []struct {
			ID        uint `json:"id"`
			BackendID uint `json:"backend_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &listBody))
	require.Equal(t, []uint{response.ID}, []uint{listBody.Data[0].ID})
	require.Equal(t, backend.ID, listBody.Data[0].BackendID)
}

// Break caught: scoping the Upstream list by API Service instead of its
// concrete Backend can mix independent target pools and prevents the client
// from loading the exact Backend selected by a Route.
func TestAPIUpstreamListUsesBackendScope(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	ownedService := models.APIService{Slug: "owned-list", Name: "Owned List", Status: consts.StatusEnabled}
	foreignService := models.APIService{Slug: "foreign-list", Name: "Foreign List", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&ownedService).Error)
	require.NoError(t, srv.DB.Create(&foreignService).Error)
	ownedBackend := models.APIBackend{APIServiceID: ownedService.ID, Name: "owned"}
	siblingBackend := models.APIBackend{APIServiceID: ownedService.ID, Name: "sibling"}
	foreignBackend := models.APIBackend{APIServiceID: foreignService.ID, Name: "foreign"}
	require.NoError(t, srv.DB.Create(&ownedBackend).Error)
	require.NoError(t, srv.DB.Create(&siblingBackend).Error)
	require.NoError(t, srv.DB.Create(&foreignBackend).Error)
	ownedUpstreams := []models.APIUpstream{
		{BackendID: ownedBackend.ID, Name: "owned-a", BaseURL: "https://owned-a.example", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
		{BackendID: ownedBackend.ID, Name: "owned-b", BaseURL: "https://owned-b.example", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
	}
	for i := range ownedUpstreams {
		require.NoError(t, srv.DB.Create(&ownedUpstreams[i]).Error)
	}
	require.NoError(t, srv.DB.Create(&models.APIUpstream{
		BackendID: siblingBackend.ID, Name: "sibling", BaseURL: "https://sibling.example", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled,
	}).Error)

	for _, path := range []string{
		"/api/admin/api-upstreams",
		"/api/admin/api-upstreams?backend_id=0",
		"/api/admin/api-upstreams?api_service_id=0",
	} {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}

	missing := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/api-upstreams?backend_id=999999", nil)
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
	foreignList := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d", foreignBackend.ID), nil)
	require.Equal(t, http.StatusOK, foreignList.Code, foreignList.Body.String())
	bothMismatch := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?api_service_id=%d&backend_id=%d", foreignService.ID, ownedBackend.ID), nil)
	require.Equal(t, http.StatusBadRequest, bothMismatch.Code, bothMismatch.Body.String())

	serviceList := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?api_service_id=%d", ownedService.ID), nil)
	require.Equal(t, http.StatusOK, serviceList.Code, serviceList.Body.String())
	var serviceBody struct {
		Data  []models.APIUpstream `json:"data"`
		Total int64                `json:"total"`
	}
	require.NoError(t, json.Unmarshal(serviceList.Body.Bytes(), &serviceBody))
	require.Equal(t, int64(3), serviceBody.Total)
	require.ElementsMatch(t, []uint{ownedBackend.ID, ownedBackend.ID, siblingBackend.ID}, []uint{serviceBody.Data[0].BackendID, serviceBody.Data[1].BackendID, serviceBody.Data[2].BackendID})

	listed := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d", ownedBackend.ID), nil)
	require.Equal(t, http.StatusOK, listed.Code, listed.Body.String())
	var body struct {
		Data  []models.APIUpstream `json:"data"`
		Total int64                `json:"total"`
	}
	require.NoError(t, json.Unmarshal(listed.Body.Bytes(), &body))
	require.Equal(t, int64(2), body.Total)
	require.Equal(t, []uint{ownedUpstreams[1].ID, ownedUpstreams[0].ID}, []uint{body.Data[0].ID, body.Data[1].ID})
	for _, upstream := range body.Data {
		require.Equal(t, ownedBackend.ID, upstream.BackendID)
	}
	matchingBoth := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?api_service_id=%d&backend_id=%d", ownedService.ID, ownedBackend.ID), nil)
	require.Equal(t, http.StatusOK, matchingBoth.Code, matchingBoth.Body.String())
	var matchingBody struct {
		Data  []models.APIUpstream `json:"data"`
		Total int64                `json:"total"`
	}
	require.NoError(t, json.Unmarshal(matchingBoth.Body.Bytes(), &matchingBody))
	require.Equal(t, int64(2), matchingBody.Total)
	for _, upstream := range matchingBody.Data {
		require.Equal(t, ownedBackend.ID, upstream.BackendID)
	}
}

func TestAPIRouteUpdateStrictArraysAndChildCreateRequiresParent(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	jwt := loginAsAdmin(t, srv, "admin", "admin123")
	missingRoute := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-routes", map[string]any{"api_service_id": 999, "slug": "orphan"})
	require.Equal(t, http.StatusBadRequest, missingRoute.Code, missingRoute.Body.String())
	missingUpstream := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": 999, "name": "orphan", "base_url": "https://upstream.example"})
	require.Equal(t, http.StatusNotFound, missingUpstream.Code, missingUpstream.Body.String())
	var routes, upstreams int64
	require.NoError(t, srv.DB.Model(&models.APIRoute{}).Where("api_service_id = 999").Count(&routes).Error)
	require.Zero(t, routes)
	require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("backend_id = 999").Count(&upstreams).Error)
	require.Zero(t, upstreams)
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&route).Error)
	success := reqHelper(srv, jwt, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), map[string]any{"websocket_subprotocols": []string{"chat", "events"}})
	require.Equal(t, http.StatusOK, success.Code, success.Body.String())
	invalid := reqHelper(srv, jwt, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), map[string]any{"protocols": []any{"http", 7}})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	invalid = reqHelper(srv, jwt, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), map[string]any{"allowed_methods": []any{"get", false}})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	invalid = reqHelper(srv, jwt, http.MethodPut, fmt.Sprintf("/api/admin/api-routes/%d", route.ID), map[string]any{"websocket_subprotocols": []any{"chat", 3}})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
}

// SQLite cannot model a production FOR UPDATE race, but these serial boundary
// cases pin the externally visible outcomes around the child-create/delete
// interleaving: a child committed before delete is cleaned up; a child started
// after delete cannot be created.
func TestAPIServiceDeleteChildCreateSerialBoundaries(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	jwt := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, srv.DB.Create(&backend).Error)

	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "before-delete", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&route).Error)
	createdUpstream := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-upstreams", map[string]any{"backend_id": backend.ID, "name": "before-delete", "base_url": "https://upstream.example"})
	require.Equal(t, http.StatusCreated, createdUpstream.Code, createdUpstream.Body.String())
	deleted := reqHelper(srv, jwt, http.MethodDelete, fmt.Sprintf("/api/admin/api-services/%d", service.ID), nil)
	require.Equal(t, http.StatusOK, deleted.Code, deleted.Body.String())
	var children int64
	require.NoError(t, srv.DB.Model(&models.APIRoute{}).Where("api_service_id = ?", service.ID).Count(&children).Error)
	require.Zero(t, children)

	for _, child := range []struct {
		path     string
		body     map[string]any
		wantHTTP int
	}{
		{path: "/api/admin/api-routes", body: map[string]any{"api_service_id": service.ID, "slug": "after-delete"}, wantHTTP: http.StatusBadRequest},
		{path: "/api/admin/api-upstreams", body: map[string]any{"backend_id": backend.ID, "name": "after-delete", "base_url": "https://upstream.example"}, wantHTTP: http.StatusNotFound},
	} {
		response := reqHelper(srv, jwt, http.MethodPost, child.path, child.body)
		require.Equal(t, child.wantHTTP, response.Code, response.Body.String())
	}
	require.NoError(t, srv.DB.Model(&models.APIRoute{}).Where("api_service_id = ?", service.ID).Count(&children).Error)
	require.Zero(t, children)
	require.NoError(t, srv.DB.Model(&models.APIUpstream{}).Where("backend_id = ?", backend.ID).Count(&children).Error)
	require.Zero(t, children)
}

func TestAPIServiceListUsesAdminPagination(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	visible := models.APIService{Slug: "visible", Name: "Visible", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&visible).Error)
	for _, slug := range []string{"newer-one", "newer-two"} {
		require.NoError(t, srv.DB.Create(&models.APIService{Slug: slug, Name: slug, Status: consts.StatusEnabled}).Error)
	}
	user := models.User{Username: "paged-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "paged-reader", Name: "Paged Reader", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: visible.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	jwt, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, consts.RoleUser, user.Username, "", "")
	require.NoError(t, err)
	forbidden := reqHelper(srv, jwt, http.MethodGet, "/api/admin/api-services?page=1&page_size=2", nil)
	require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())
	response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-services?page=1&page_size=2", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		Data  []models.APIService `json:"data"`
		Total int64               `json:"total"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Len(t, body.Data, 2)
	require.EqualValues(t, 3, body.Total)
}

// Break caught: publishing a delete before its database transaction commits
// would make Agents drop a service that was rolled back locally. Conversely, a
// successful commit must produce precisely one delete projection.
func TestAPIServiceDeletePublishesOnlyAfterSuccessfulTransaction(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	var published []uint
	subscription, err := events.Subscribe(srv.Bus, events.APIServiceDeleteTopic, func(_ context.Context, payload protocol.SyncedAPIService) error {
		published = append(published, payload.ID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	callbackName := "test:fail_api_service_delete"
	require.NoError(t, srv.DB.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "api_services" {
			tx.AddError(errors.New("force API service delete rollback"))
		}
	}))
	failed := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-services/%d", service.ID), nil)
	require.GreaterOrEqual(t, failed.Code, http.StatusBadRequest, failed.Body.String())
	require.Empty(t, published)
	var retained models.APIService
	require.NoError(t, srv.DB.First(&retained, service.ID).Error)
	require.NoError(t, srv.DB.Callback().Delete().Remove(callbackName))

	success := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-services/%d", service.ID), nil)
	require.Equal(t, http.StatusOK, success.Code, success.Body.String())
	require.Equal(t, []uint{service.ID}, published)
}

func TestRequestLimiterHTTPRejectsInvalidJSONTextBeforeNameReplacement(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminToken := loginAsAdmin(t, srv, "admin", "admin123")

	doRawJSON := func(method, path string, payload []byte) *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		request := httptest.NewRequest(method, path, bytes.NewReader(payload))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+adminToken)
		srv.Router.ServeHTTP(response, request)
		return response
	}
	createPayload := func(nameJSON []byte) []byte {
		payload := append([]byte(`{"name":"`), nameJSON...)
		return append(payload, []byte(`","enabled":true,"metric":"rate","capacity":10,"window_ms":60000,"key_by":"shared","action":"reject"}`)...)
	}

	invalidNames := []struct {
		name     string
		nameJSON []byte
	}{
		{name: "raw invalid UTF-8", nameJSON: []byte{0xff}},
		{name: "lone high surrogate", nameJSON: []byte(`\uD800`)},
		{name: "low then high surrogate", nameJSON: []byte(`\uDC00\uD800`)},
	}
	for _, test := range invalidNames {
		t.Run("create rejects "+test.name, func(t *testing.T) {
			var before int64
			require.NoError(t, srv.DB.Model(&models.RequestLimiter{}).Count(&before).Error)

			response := doRawJSON(http.MethodPost, "/api/admin/rate-limiters", createPayload(test.nameJSON))

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			var after int64
			require.NoError(t, srv.DB.Model(&models.RequestLimiter{}).Count(&after).Error)
			require.Equal(t, before, after, "strict JSON rejection must happen before persistence")
		})
	}

	for _, test := range invalidNames {
		t.Run("update rejects "+test.name, func(t *testing.T) {
			limiter := models.RequestLimiter{
				Name: "before", Enabled: true, Metric: models.LimiterMetricRate,
				Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
			}
			require.NoError(t, srv.DB.Create(&limiter).Error)

			payload := append([]byte(`{"name":"`), test.nameJSON...)
			payload = append(payload, []byte(`"}`)...)
			response := doRawJSON(http.MethodPut, fmt.Sprintf("/api/admin/rate-limiters/%d", limiter.ID), payload)

			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
			var reloaded models.RequestLimiter
			require.NoError(t, srv.DB.First(&reloaded, limiter.ID).Error)
			require.Equal(t, "before", reloaded.Name)
		})
	}

	validNames := []struct {
		name     string
		nameJSON []byte
		want     string
	}{
		{name: "raw Unicode", nameJSON: []byte("速率限制"), want: "速率限制"},
		{name: "ordinary escapes", nameJSON: []byte(`quote:\" slash:\\ solidus:\/ unicode:\u0061`), want: `quote:" slash:\ solidus:/ unicode:a`},
		{name: "surrogate pair", nameJSON: []byte(`emoji-\uD83D\uDE00`), want: "emoji-😀"},
	}
	for _, test := range validNames {
		t.Run("create accepts "+test.name, func(t *testing.T) {
			response := doRawJSON(http.MethodPost, "/api/admin/rate-limiters", createPayload(test.nameJSON))
			require.Equal(t, http.StatusCreated, response.Code, response.Body.String())
			var created models.RequestLimiter
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
			require.Equal(t, test.want, created.Name)
		})
	}

	for _, test := range validNames {
		t.Run("update accepts "+test.name, func(t *testing.T) {
			limiter := models.RequestLimiter{
				Name: "before", Enabled: true, Metric: models.LimiterMetricRate,
				Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
			}
			require.NoError(t, srv.DB.Create(&limiter).Error)
			payload := append([]byte(`{"name":"`), test.nameJSON...)
			payload = append(payload, []byte(`"}`)...)

			response := doRawJSON(http.MethodPut, fmt.Sprintf("/api/admin/rate-limiters/%d", limiter.ID), payload)

			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var updated models.RequestLimiter
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &updated))
			require.Equal(t, test.want, updated.Name)
		})
	}
}

func createChannelE2E(t *testing.T, srv *master.Server, jwt, name, baseURL, modelsCSV string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"name":     name,
		"type":     1,
		"key":      "sk-fake",
		"base_url": baseURL,
		"models":   modelsCSV,
		"status":   1,
	})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/admin/channels", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	srv.Router.ServeHTTP(w, req)
	if w.Code != http.StatusOK && w.Code != http.StatusCreated {
		t.Fatalf("create channel: %d %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return fmt.Sprintf("%v", resp["id"])
}

func TestFullAPIFlow(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Login
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwtToken := loginResp["token"]
	if jwtToken == "" {
		t.Fatal("no token in login response")
	}

	// Helper to make authenticated requests
	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		req, _ := http.NewRequest(method, path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+jwtToken)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	// Create user
	w2 := doReq("POST", "/api/admin/users", map[string]any{"username": "user1", "password": "pass", "role": 1})
	if w2.Code != 201 {
		t.Fatalf("create user: %d %s", w2.Code, w2.Body.String())
	}

	// List users
	w3 := doReq("GET", "/api/admin/users", nil)
	if w3.Code != 200 {
		t.Fatalf("list users: %d", w3.Code)
	}

	// Create token (via new /api/tokens path)
	w4 := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "test-token"})
	if w4.Code != 201 {
		t.Fatalf("create token: %d %s", w4.Code, w4.Body.String())
	}
	var createdToken map[string]any
	json.Unmarshal(w4.Body.Bytes(), &createdToken)
	generatedKey, _ := createdToken["key"].(string)
	if generatedKey == "" {
		t.Fatal("expected generated token key")
	}

	// Create token with custom key
	const customTokenKey = "sk-custom-fixed-key"
	w4c := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "custom-token", "key": customTokenKey})
	if w4c.Code != 201 {
		t.Fatalf("create token with custom key: %d %s", w4c.Code, w4c.Body.String())
	}
	var createdCustomToken map[string]any
	json.Unmarshal(w4c.Body.Bytes(), &createdCustomToken)
	if got, _ := createdCustomToken["key"].(string); got != customTokenKey {
		t.Fatalf("expected custom key %q, got %q", customTokenKey, got)
	}

	// Duplicate custom key should conflict
	w4d := doReq("POST", "/api/tokens", map[string]any{"user_id": 1, "name": "dup-token", "key": customTokenKey})
	if w4d.Code != 409 {
		t.Fatalf("duplicate custom key: expected 409, got %d %s", w4d.Code, w4d.Body.String())
	}

	// Create channel
	w5 := doReq("POST", "/api/admin/channels", map[string]any{"name": "openai-1", "type": 1, "key": "sk-xxx", "base_url": "https://api.openai.com", "models": "gpt-4o"})
	if w5.Code != 201 {
		t.Fatalf("create channel: %d %s", w5.Code, w5.Body.String())
	}

	// Create model config
	w6 := doReq("POST", "/api/admin/models", map[string]any{"model_name": "gpt-4o", "input_price": 2.5, "output_price": 10.0})
	if w6.Code != 201 {
		t.Fatalf("create model: %d %s", w6.Code, w6.Body.String())
	}

	// === Full CRUD: Users ===
	// Get user
	w_ug := doReq("GET", "/api/admin/users/2", nil) // user1 is ID 2 (admin is 1)
	if w_ug.Code != 200 {
		t.Fatalf("get user: %d %s", w_ug.Code, w_ug.Body.String())
	}

	// Update user
	w_uu := doReq("PUT", "/api/admin/users/2", map[string]any{"status": 0})
	if w_uu.Code != 200 {
		t.Fatalf("update user: %d %s", w_uu.Code, w_uu.Body.String())
	}

	// User update with illegal status=2 must be rejected
	w_usi := doReq("PUT", "/api/admin/users/2", map[string]any{"status": 2})
	if w_usi.Code != 400 {
		t.Fatalf("expected 400 for invalid user status=2, got %d: %s", w_usi.Code, w_usi.Body.String())
	}

	// Update quota
	w_uq := doReq("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 5000})
	if w_uq.Code != 200 {
		t.Fatalf("update quota: %d %s", w_uq.Code, w_uq.Body.String())
	}

	// === Full CRUD: Tokens ===
	// Get token (via new path)
	w_tg := doReq("GET", "/api/tokens/1", nil)
	if w_tg.Code != 200 {
		t.Fatalf("get token: %d %s", w_tg.Code, w_tg.Body.String())
	}

	// List tokens (via new path)
	w_tl := doReq("GET", "/api/tokens", nil)
	if w_tl.Code != 200 {
		t.Fatalf("list tokens: %d", w_tl.Code)
	}

	// Update token (via new path)
	w_tu := doReq("PUT", "/api/tokens/1", map[string]any{"name": "updated-token"})
	if w_tu.Code != 200 {
		t.Fatalf("update token: %d %s", w_tu.Code, w_tu.Body.String())
	}

	// Update token should ignore key changes (immutable)
	w_tuk := doReq("PUT", "/api/tokens/1", map[string]any{"key": "sk-should-not-change"})
	if w_tuk.Code != 200 {
		t.Fatalf("update token with key: %d %s", w_tuk.Code, w_tuk.Body.String())
	}
	w_tg2 := doReq("GET", "/api/tokens/1", nil)
	if w_tg2.Code != 200 {
		t.Fatalf("get token after key update attempt: %d %s", w_tg2.Code, w_tg2.Body.String())
	}
	var updatedToken map[string]any
	json.Unmarshal(w_tg2.Body.Bytes(), &updatedToken)
	if got, _ := updatedToken["key"].(string); got != generatedKey {
		t.Fatalf("token key should remain immutable, want %q, got %q", generatedKey, got)
	}

	// === Full CRUD: Channels ===
	// Get channel
	w_cg := doReq("GET", "/api/admin/channels/1", nil)
	if w_cg.Code != 200 {
		t.Fatalf("get channel: %d %s", w_cg.Code, w_cg.Body.String())
	}

	// List channels
	w_cl := doReq("GET", "/api/admin/channels", nil)
	if w_cl.Code != 200 {
		t.Fatalf("list channels: %d", w_cl.Code)
	}

	// Update channel
	w_cu := doReq("PUT", "/api/admin/channels/1", map[string]any{"tag": "updated-channel"})
	if w_cu.Code != 200 {
		t.Fatalf("update channel: %d %s", w_cu.Code, w_cu.Body.String())
	}

	// Channel names are identity fields and cannot be changed by a patch.
	w_cni := doReq("PUT", "/api/admin/channels/1", map[string]any{"name": "renamed-channel"})
	if w_cni.Code != 400 {
		t.Fatalf("expected 400 for read-only channel name, got %d: %s", w_cni.Code, w_cni.Body.String())
	}

	// Channel update with illegal status=2 must be rejected
	w_csi := doReq("PUT", "/api/admin/channels/1", map[string]any{"status": 2})
	if w_csi.Code != 400 {
		t.Fatalf("expected 400 for invalid channel status=2, got %d: %s", w_csi.Code, w_csi.Body.String())
	}

	// === Full CRUD: Models ===
	// Get model
	w_mg := doReq("GET", "/api/admin/models/1", nil)
	if w_mg.Code != 200 {
		t.Fatalf("get model: %d %s", w_mg.Code, w_mg.Body.String())
	}

	// List models
	w_ml := doReq("GET", "/api/admin/models", nil)
	if w_ml.Code != 200 {
		t.Fatalf("list models: %d", w_ml.Code)
	}

	// Update model
	w_mu := doReq("PUT", "/api/admin/models/1", map[string]any{"input_price": 3.0})
	if w_mu.Code != 200 {
		t.Fatalf("update model: %d %s", w_mu.Code, w_mu.Body.String())
	}

	// === Agents CRUD ===
	// Create agent
	w_ac := doReq("POST", "/api/admin/agents", map[string]any{"name": "manual-agent"})
	if w_ac.Code != 201 {
		t.Fatalf("create agent: %d %s", w_ac.Code, w_ac.Body.String())
	}

	// List agents
	w_al := doReq("GET", "/api/admin/agents", nil)
	if w_al.Code != 200 {
		t.Fatalf("list agents: %d", w_al.Code)
	}

	// Get agent
	w_ag := doReq("GET", "/api/admin/agents/1", nil)
	if w_ag.Code != 200 {
		t.Fatalf("get agent: %d %s", w_ag.Code, w_ag.Body.String())
	}

	// Update agent
	w_au := doReq("PUT", "/api/admin/agents/1", map[string]any{"name": "renamed-agent"})
	if w_au.Code != 200 {
		t.Fatalf("update agent: %d %s", w_au.Code, w_au.Body.String())
	}

	// Agent update with illegal status=2 must be rejected
	w_asi := doReq("PUT", "/api/admin/agents/1", map[string]any{"status": 2})
	if w_asi.Code != 400 {
		t.Fatalf("expected 400 for invalid agent status=2, got %d: %s", w_asi.Code, w_asi.Body.String())
	}

	// === Stats & Logs (new paths) ===
	w_stats := doReq("GET", "/api/stats/overview", nil)
	if w_stats.Code != 200 {
		t.Fatalf("stats: %d", w_stats.Code)
	}
	var statsResp map[string]any
	json.Unmarshal(w_stats.Body.Bytes(), &statsResp)
	if _, ok := statsResp["users"]; !ok {
		t.Error("stats missing users field")
	}

	w_logs := doReq("GET", "/api/logs", nil)
	if w_logs.Code != 200 {
		t.Fatalf("logs: %d", w_logs.Code)
	}

	// Logs with filters
	w_logs2 := doReq("GET", "/api/logs?user_id=1&model_name=gpt-4o", nil)
	if w_logs2.Code != 200 {
		t.Fatalf("logs with filters: %d", w_logs2.Code)
	}

	// Trend endpoint
	w_trend := doReq("GET", "/api/stats/trend", nil)
	if w_trend.Code != 200 {
		t.Fatalf("trend: %d %s", w_trend.Code, w_trend.Body.String())
	}

	// === Backward compatible aliases (deprecated admin paths) ===
	w_compat_tokens := doReq("GET", "/api/admin/tokens", nil)
	if w_compat_tokens.Code != 200 {
		t.Fatalf("backward compat tokens: %d", w_compat_tokens.Code)
	}

	w_compat_logs := doReq("GET", "/api/admin/logs", nil)
	if w_compat_logs.Code != 200 {
		t.Fatalf("backward compat logs: %d", w_compat_logs.Code)
	}

	w_compat_stats := doReq("GET", "/api/admin/stats", nil)
	if w_compat_stats.Code != 200 {
		t.Fatalf("backward compat stats: %d", w_compat_stats.Code)
	}

	// === Delete operations (do these last) ===
	// Delete model
	w_md := doReq("DELETE", "/api/admin/models/1", nil)
	if w_md.Code != 200 {
		t.Fatalf("delete model: %d %s", w_md.Code, w_md.Body.String())
	}

	// Delete channel
	w_cd := doReq("DELETE", "/api/admin/channels/1", nil)
	if w_cd.Code != 200 {
		t.Fatalf("delete channel: %d %s", w_cd.Code, w_cd.Body.String())
	}

	// Delete token (via new path)
	w_td := doReq("DELETE", "/api/tokens/1", nil)
	if w_td.Code != 200 {
		t.Fatalf("delete token: %d %s", w_td.Code, w_td.Body.String())
	}

	// Delete custom token (via backward-compat path)
	w_tdc := doReq("DELETE", "/api/admin/tokens/2", nil)
	if w_tdc.Code != 200 {
		t.Fatalf("delete custom token: %d %s", w_tdc.Code, w_tdc.Body.String())
	}

	// Delete user
	w_ud := doReq("DELETE", "/api/admin/users/2", nil)
	if w_ud.Code != 200 {
		t.Fatalf("delete user: %d %s", w_ud.Code, w_ud.Body.String())
	}

	// Delete agent
	w_ad := doReq("DELETE", "/api/admin/agents/1", nil)
	if w_ad.Code != 200 {
		t.Fatalf("delete agent: %d %s", w_ad.Code, w_ad.Body.String())
	}

	// === Get non-existent resources (404) ===
	if w := doReq("GET", "/api/admin/users/999", nil); w.Code != 404 {
		t.Errorf("get missing user: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/tokens/999", nil); w.Code != 404 {
		t.Errorf("get missing token: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/channels/999", nil); w.Code != 404 {
		t.Errorf("get missing channel: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/models/999", nil); w.Code != 404 {
		t.Errorf("get missing model: expected 404, got %d", w.Code)
	}
	if w := doReq("GET", "/api/admin/agents/999", nil); w.Code != 404 {
		t.Errorf("get missing agent: expected 404, got %d", w.Code)
	}

	// Test unauthorized access (no token)
	w7 := httptest.NewRecorder()
	req7, _ := http.NewRequest("GET", "/api/admin/users", nil)
	srv.Router.ServeHTTP(w7, req7)
	if w7.Code != 401 {
		t.Fatalf("expected 401 for no token, got %d", w7.Code)
	}

	// Test wrong login password
	wrongLogin, _ := json.Marshal(map[string]any{"username": "admin", "password": "wrong"})
	w_wl := httptest.NewRecorder()
	req_wl, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(wrongLogin))
	req_wl.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w_wl, req_wl)
	if w_wl.Code != 401 {
		t.Errorf("wrong password: expected 401, got %d", w_wl.Code)
	}

	// Generate enrollment token (admin endpoint)
	w8a := doReq("POST", "/api/admin/agents/enrollment-token", map[string]any{"ttl": 300})
	if w8a.Code != 200 {
		t.Fatalf("generate enrollment token: %d %s", w8a.Code, w8a.Body.String())
	}
	var enrollTokenResp map[string]any
	json.Unmarshal(w8a.Body.Bytes(), &enrollTokenResp)
	enrollToken := enrollTokenResp["enrollment_token"].(string)

	// Test agent enrollment (public endpoint)
	enrollBody, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "test-agent"})
	w8 := httptest.NewRecorder()
	req8, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody))
	req8.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8, req8)
	if w8.Code != 201 {
		t.Fatalf("enroll agent: %d %s", w8.Code, w8.Body.String())
	}

	// Test enrollment with invalid token (should fail)
	enrollBody2, _ := json.Marshal(map[string]any{"enrollment_token": "invalid-token"})
	w8b := httptest.NewRecorder()
	req8b, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody2))
	req8b.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8b, req8b)
	if w8b.Code != 401 {
		t.Fatalf("expected 401 for invalid token, got %d %s", w8b.Code, w8b.Body.String())
	}

	// Test enrollment with same unexpired token (should succeed)
	enrollBody3, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "test-agent-2"})
	w8c := httptest.NewRecorder()
	req8c, _ := http.NewRequest("POST", "/api/agents/enroll", bytes.NewReader(enrollBody3))
	req8c.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w8c, req8c)
	if w8c.Code != 201 {
		t.Fatalf("expected 201 for reused unexpired token, got %d %s", w8c.Code, w8c.Body.String())
	}
}

func TestChannelTypesCatalog(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Login
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	req.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("login failed: %d %s", w.Code, w.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &loginResp)
	jwtToken := loginResp["token"]
	if jwtToken == "" {
		t.Fatal("no token in login response")
	}

	w2 := httptest.NewRecorder()
	req2, _ := http.NewRequest("GET", "/api/admin/channels/types", nil)
	req2.Header.Set("Authorization", "Bearer "+jwtToken)
	srv.Router.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("list channel types: %d %s", w2.Code, w2.Body.String())
	}

	var items []map[string]any
	if err := json.Unmarshal(w2.Body.Bytes(), &items); err != nil {
		t.Fatalf("parse channel types response: %v", err)
	}
	if len(items) == 0 {
		t.Fatal("channel types should not be empty")
	}

	lastID := -1
	for _, item := range items {
		id, ok := item["id"].(float64)
		if !ok {
			t.Fatalf("channel type id is missing or invalid: %#v", item)
		}
		if int(id) < lastID {
			t.Fatalf("channel types should be sorted by id ascending, got %d after %d", int(id), lastID)
		}
		lastID = int(id)
		if name, _ := item["name"].(string); name == "" {
			t.Fatalf("channel type name is required: %#v", item)
		}
		if key, _ := item["i18n_key"].(string); key == "" {
			t.Fatalf("channel type i18n_key is required: %#v", item)
		}
	}
}

func TestUserPermissions(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Helper for login
	login := func(username, password string) string {
		body, _ := json.Marshal(map[string]any{"username": username, "password": password})
		w := httptest.NewRecorder()
		r, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
		}
		var resp map[string]string
		json.Unmarshal(w.Body.Bytes(), &resp)
		return resp["token"]
	}

	// Helper for authenticated requests with a specific token
	doReqWith := func(jwtToken, method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+jwtToken)
		srv.Router.ServeHTTP(w, r)
		return w
	}

	adminToken := login("admin", "admin123")

	// Create a normal user
	w := doReqWith(adminToken, "POST", "/api/admin/users", map[string]any{
		"username": "normaluser", "password": "pass123", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create normal user: %d %s", w.Code, w.Body.String())
	}
	var normalUserResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &normalUserResp)
	normalUserID := uint(normalUserResp["id"].(float64))

	// Set quota for normal user
	w = doReqWith(adminToken, "PUT", "/api/admin/users/2/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	// Re-enable user (was set to status 2 in other test? no, fresh DB)
	userToken := login("normaluser", "pass123")

	// Create a token template (admin only)
	w = doReqWith(adminToken, "POST", "/api/admin/token-templates", map[string]any{
		"name": "default-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	var tplResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &tplResp)
	templateID := uint(tplResp["id"].(float64))

	// Admin creates token freely (no template needed)
	w = doReqWith(adminToken, "POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-token",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token: %d %s", w.Code, w.Body.String())
	}
	var adminTokenResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &adminTokenResp)
	adminCreatedTokenID := int(adminTokenResp["id"].(float64))

	// Normal user creates token without template (should fail)
	w = doReqWith(userToken, "POST", "/api/tokens", map[string]any{"name": "my-token"})
	if w.Code != 400 {
		t.Fatalf("user create token without template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with template (should succeed)
	w = doReqWith(userToken, "POST", "/api/tokens", map[string]any{
		"name": "my-token", "template_id": templateID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with template: %d %s", w.Code, w.Body.String())
	}
	var userTokenResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &userTokenResp)
	userCreatedTokenID := int(userTokenResp["id"].(float64))
	// Verify template-inherited fields
	if uid := uint(userTokenResp["user_id"].(float64)); uid != normalUserID {
		t.Fatalf("expected user_id %d, got %d", normalUserID, uid)
	}
	if models, _ := userTokenResp["models"].(string); models != `["gpt-4o"]` {
		t.Fatalf("expected models from template, got %q", models)
	}

	// Normal user listing only shows own tokens
	w = doReqWith(userToken, "GET", "/api/tokens", nil)
	if w.Code != 200 {
		t.Fatalf("user list tokens: %d", w.Code)
	}
	var listResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &listResp)
	tokenList, _ := listResp["data"].([]any)
	for _, tok := range tokenList {
		tokMap := tok.(map[string]any)
		if uid := uint(tokMap["user_id"].(float64)); uid != normalUserID {
			t.Fatalf("user listing shows token with user_id=%d, expected %d", uid, normalUserID)
		}
	}

	// Normal user PUT on admin's token returns 404
	w = doReqWith(userToken, "PUT", "/api/tokens/"+itoa(adminCreatedTokenID), map[string]any{"name": "hacked"})
	if w.Code != 404 {
		t.Fatalf("user update other's token: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Normal user DELETE on admin's token returns 404
	w = doReqWith(userToken, "DELETE", "/api/tokens/"+itoa(adminCreatedTokenID), nil)
	if w.Code != 404 {
		t.Fatalf("user delete other's token: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Normal user can update own token (name only)
	w = doReqWith(userToken, "PUT", "/api/tokens/"+itoa(userCreatedTokenID), map[string]any{"name": "renamed"})
	if w.Code != 200 {
		t.Fatalf("user update own token: %d %s", w.Code, w.Body.String())
	}

	// Normal user gets 403 on admin-only routes
	w = doReqWith(userToken, "GET", "/api/admin/users", nil)
	if w.Code != 403 {
		t.Fatalf("user access admin route: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Stats/overview for normal user returns quota fields
	w = doReqWith(userToken, "GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("user stats overview: %d %s", w.Code, w.Body.String())
	}
	var userStats map[string]any
	json.Unmarshal(w.Body.Bytes(), &userStats)
	if _, ok := userStats["quota"]; !ok {
		t.Error("user stats missing quota field")
	}
	if _, ok := userStats["used_quota"]; !ok {
		t.Error("user stats missing used_quota field")
	}
	// Admin-only fields should be absent
	if _, ok := userStats["users"]; ok {
		t.Error("user stats should not have users field")
	}

	// Normal user can delete own token
	w = doReqWith(userToken, "DELETE", "/api/tokens/"+itoa(userCreatedTokenID), nil)
	if w.Code != 200 {
		t.Fatalf("user delete own token: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistration(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")

	// Helper for unauthenticated requests
	doPublic := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		srv.Router.ServeHTTP(w, r)
		return w
	}

	// 1. public-config reports registration disabled by default
	w := doPublic("GET", "/api/system/public-config", nil)
	if w.Code != 200 {
		t.Fatalf("registration status: %d %s", w.Code, w.Body.String())
	}
	var statusResp map[string]any
	json.Unmarshal(w.Body.Bytes(), &statusResp)
	if statusResp["registration_enabled"] != false {
		t.Fatalf("expected registration_enabled=false, got %v", statusResp["registration_enabled"])
	}

	// 2. Register returns 403 when disabled
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 403 {
		t.Fatalf("register when disabled: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// 3. Admin enables registration
	loginBody, _ := json.Marshal(map[string]any{"username": "admin", "password": "admin123"})
	wl := httptest.NewRecorder()
	rl, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(loginBody))
	rl.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(wl, rl)
	if wl.Code != 200 {
		t.Fatalf("admin login: %d %s", wl.Code, wl.Body.String())
	}
	var loginResp map[string]string
	json.Unmarshal(wl.Body.Bytes(), &loginResp)
	adminToken := loginResp["token"]

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		var b []byte
		if body != nil {
			b, _ = json.Marshal(body)
		}
		w := httptest.NewRecorder()
		r, _ := http.NewRequest(method, path, bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+adminToken)
		srv.Router.ServeHTTP(w, r)
		return w
	}

	w = doAdmin("PUT", "/api/admin/system/settings", map[string]any{"settings": map[string]any{"registration_enabled": "true"}})
	if w.Code != 200 {
		t.Fatalf("enable registration: %d %s", w.Code, w.Body.String())
	}

	// 4. Register succeeds with 201
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 201 {
		t.Fatalf("register: expected 201, got %d %s", w.Code, w.Body.String())
	}

	// 5. Duplicate username returns 409
	w = doPublic("POST", "/api/register", map[string]any{"username": "newuser", "email": "newuser@test.example.com", "password": "password123"})
	if w.Code != 409 {
		t.Fatalf("duplicate register: expected 409, got %d %s", w.Code, w.Body.String())
	}

	// 6. Invalid username returns 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "bad user!", "email": "baduser@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("invalid username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// 7. New user can login
	w = doPublic("POST", "/api/login", map[string]any{"username": "newuser", "password": "password123"})
	if w.Code != 200 {
		t.Fatalf("new user login: %d %s", w.Code, w.Body.String())
	}
	var newLoginResp map[string]string
	json.Unmarshal(w.Body.Bytes(), &newLoginResp)
	if newLoginResp["token"] == "" {
		t.Fatal("new user login: no token returned")
	}
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}

// loginHelper logs in and returns the JWT token.
func loginHelper(t *testing.T, srv *master.Server, username, password string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"username": username, "password": password})
	w := httptest.NewRecorder()
	r, _ := http.NewRequest("POST", "/api/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	srv.Router.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login %s failed: %d %s", username, w.Code, w.Body.String())
	}
	var resp map[string]string
	json.Unmarshal(w.Body.Bytes(), &resp)
	tok := resp["token"]
	if tok == "" {
		t.Fatalf("login %s: no token in response", username)
	}
	return tok
}

// reqHelper makes an authenticated HTTP request and returns the recorder.
func reqHelper(srv *master.Server, jwtToken, method, path string, body any) *httptest.ResponseRecorder {
	var b []byte
	if body != nil {
		b, _ = json.Marshal(body)
	}
	w := httptest.NewRecorder()
	r, _ := http.NewRequest(method, path, bytes.NewReader(b))
	r.Header.Set("Content-Type", "application/json")
	if jwtToken != "" {
		r.Header.Set("Authorization", "Bearer "+jwtToken)
	}
	srv.Router.ServeHTTP(w, r)
	return w
}

// jsonBody parses response body into map.
func jsonBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("parse response body: %v\nbody: %s", err, w.Body.String())
	}
	return m
}

func testCallbackTableName(tx *gorm.DB) string {
	if tx.Statement == nil {
		return ""
	}
	if tx.Statement.Schema != nil {
		return tx.Statement.Schema.Table
	}
	return tx.Statement.Table
}

func TestAdminUpdateQuotaValidatesExactInt64JSON(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminToken := loginHelper(t, srv, "admin", "admin123")

	user := models.User{Username: "quota-input-user", Password: "password", Role: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	path := fmt.Sprintf("/api/admin/users/%d/quota", user.ID)

	doRawRequest := func(body string) *httptest.ResponseRecorder {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+adminToken)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	w := doRawRequest(`{"delta":0}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	for _, body := range []string{
		`{}`,
		`{"delta":null}`,
		`{"delta":1.5}`,
		`{"delta":"1"}`,
		`{"delta":9223372036854775808}`,
		`{"delta":-9223372036854775809}`,
	} {
		t.Run(body, func(t *testing.T) {
			w := doRawRequest(body)
			require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		})
	}
}

func TestAdminUpdateQuotaOverflowDoesNotBreakLogin(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminToken := loginHelper(t, srv, "admin", "admin123")

	w := reqHelper(srv, adminToken, http.MethodPost, "/api/admin/users", map[string]any{
		"username": "quota-login",
		"password": "pass1234",
		"role":     1,
	})
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())
	var user models.User
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &user))
	require.NotZero(t, user.ID)

	require.NoError(t, srv.DB.Model(&models.User{}).Where("id = ?", user.ID).
		Update("quota", int64(math.MaxInt64-1)).Error)

	w = reqHelper(srv, adminToken, http.MethodPut,
		fmt.Sprintf("/api/admin/users/%d/quota", user.ID), map[string]any{"delta": 2})
	assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
	assert.Equal(t, "quota out of range", jsonBody(t, w)["error"])

	var quota int64
	var storageType string
	row := srv.DB.Raw("SELECT quota, typeof(quota) FROM users WHERE id = ?", user.ID).Row()
	require.NoError(t, row.Scan(&quota, &storageType))
	require.Equal(t, int64(math.MaxInt64-1), quota)
	require.Equal(t, "integer", storageType)

	require.NotEmpty(t, loginHelper(t, srv, "quota-login", "pass1234"))
}

func TestAdminUpdateQuotaClassifiesDatabaseErrors(t *testing.T) {
	setup := func(t *testing.T) (*master.Server, string) {
		t.Helper()
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		return srv, loginHelper(t, srv, "admin", "admin123")
	}

	t.Run("not found remains 404", func(t *testing.T) {
		srv, adminToken := setup(t)
		w := reqHelper(srv, adminToken, http.MethodPut,
			"/api/admin/users/999999/quota", map[string]any{"delta": 0})

		require.Equal(t, http.StatusNotFound, w.Code, w.Body.String())
		require.Equal(t, consts.ErrNotFound, jsonBody(t, w)["error"])
	})

	t.Run("lookup failure is 500", func(t *testing.T) {
		srv, adminToken := setup(t)
		user := models.User{Username: "quota-lookup-failure", Password: "hash", Role: 1}
		require.NoError(t, srv.DB.Create(&user).Error)

		failure := errors.New("query failed secret-database-detail")
		processor := srv.DB.Callback().Query()
		fired := false
		const callbackName = "test:quota_lookup_failure"
		require.NoError(t, processor.Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if fired || testCallbackTableName(tx) != "users" {
				return
			}
			fired = true
			_ = tx.AddError(failure)
		}))
		t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

		w := reqHelper(srv, adminToken, http.MethodPut,
			fmt.Sprintf("/api/admin/users/%d/quota", user.ID), map[string]any{"delta": 0})

		require.True(t, fired)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Equal(t, "get user failed", jsonBody(t, w)["error"])
		require.NotContains(t, w.Body.String(), "secret-database-detail")
	})

	t.Run("mutation failure is 500", func(t *testing.T) {
		srv, adminToken := setup(t)
		user := models.User{Username: "quota-mutation-failure", Password: "hash", Role: 1, Quota: 7}
		require.NoError(t, srv.DB.Create(&user).Error)

		failure := errors.New("update failed secret-database-detail")
		processor := srv.DB.Callback().Update()
		fired := false
		const callbackName = "test:quota_mutation_failure"
		require.NoError(t, processor.Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
			if fired || testCallbackTableName(tx) != "users" {
				return
			}
			fired = true
			_ = tx.AddError(failure)
		}))
		t.Cleanup(func() { require.NoError(t, processor.Remove(callbackName)) })

		w := reqHelper(srv, adminToken, http.MethodPut,
			fmt.Sprintf("/api/admin/users/%d/quota", user.ID), map[string]any{"delta": 1})

		require.True(t, fired)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		require.Equal(t, "update quota failed", jsonBody(t, w)["error"])
		require.NotContains(t, w.Body.String(), "secret-database-detail")

		var quota int64
		require.NoError(t, srv.DB.Model(&models.User{}).Select("quota").Where("id = ?", user.ID).Scan(&quota).Error)
		require.Equal(t, int64(7), quota)
	})
}

func TestAdminUserUpdateRejectsQuotaFields(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminToken := loginHelper(t, srv, "admin", "admin123")

	for i, field := range []string{
		"quota",
		"Quota",
		"QUOTA",
		"used_quota",
		"UsedQuota",
		"usedQuota",
		"USED_QUOTA",
	} {
		t.Run(field, func(t *testing.T) {
			user := models.User{
				Username:  fmt.Sprintf("protected-quota-%d", i),
				Password:  "password",
				Role:      1,
				Quota:     7,
				UsedQuota: 3,
			}
			require.NoError(t, srv.DB.Create(&user).Error)

			path := fmt.Sprintf("/api/admin/users/%d", user.ID)
			w := reqHelper(srv, adminToken, http.MethodPut, path, map[string]any{field: 99})

			var saved models.User
			require.NoError(t, srv.DB.First(&saved, user.ID).Error)
			assert.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
			assert.Equal(t, int64(7), saved.Quota, w.Body.String())
			assert.Equal(t, int64(3), saved.UsedQuota, w.Body.String())
		})
	}
}

func TestTokenTemplateCRUD(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doReq := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create template with valid models JSON
	w := doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "test-tpl", "models": `["gpt-4o","claude-.*"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: expected 201, got %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	if tpl["id"] == nil || tpl["name"] != "test-tpl" || tpl["models"] != `["gpt-4o","claude-.*"]` {
		t.Fatalf("create template response unexpected: %v", tpl)
	}
	if int(tpl["expiry_days"].(float64)) != 30 {
		t.Fatalf("expected expiry_days=30, got %v", tpl["expiry_days"])
	}
	if int(tpl["status"].(float64)) != 1 {
		t.Fatalf("expected status=1, got %v", tpl["status"])
	}
	tplID := int(tpl["id"].(float64))

	// List templates (admin sees all)
	w = doReq("GET", "/api/admin/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("list templates: %d %s", w.Code, w.Body.String())
	}
	listResp := jsonBody(t, w)
	if total := int(listResp["total"].(float64)); total < 1 {
		t.Fatalf("expected total >= 1, got %d", total)
	}

	// Update template
	w = doReq("PUT", "/api/admin/token-templates/"+itoa(tplID), map[string]any{
		"name": "updated-tpl", "expiry_days": 60,
	})
	if w.Code != 200 {
		t.Fatalf("update template: %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if updated["name"] != "updated-tpl" {
		t.Fatalf("expected name=updated-tpl, got %v", updated["name"])
	}

	// Create template with invalid regex
	w = doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "bad", "models": `["gpt-[invalid"]`,
	})
	if w.Code != 400 {
		t.Fatalf("invalid regex: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Create a second template, then disable it via update (status=0 in create defaults to 1)
	w = doReq("POST", "/api/admin/token-templates", map[string]any{
		"name": "disabled-tpl", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create disabled template: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))

	// Disable the template via update
	w = doReq("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Token template update with illegal status=2 must be rejected
	wTplInvalid := doReq("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 2})
	if wTplInvalid.Code != 400 {
		t.Fatalf("expected 400 for invalid template status=2, got %d: %s", wTplInvalid.Code, wTplInvalid.Body.String())
	}

	// Delete the disabled template
	w = doReq("DELETE", "/api/admin/token-templates/"+itoa(disabledTplID), nil)
	if w.Code != 200 {
		t.Fatalf("delete template: %d %s", w.Code, w.Body.String())
	}

	// Delete non-existent template
	w = doReq("DELETE", "/api/admin/token-templates/999", nil)
	if w.Code != 404 {
		t.Fatalf("delete non-existent: expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestTokenTemplateAccess(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "normie", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userToken := loginHelper(t, srv, "normie", "pass1234")

	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Admin creates 2 templates: one enabled, one to be disabled
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "enabled-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create enabled template: %d %s", w.Code, w.Body.String())
	}
	enabledTpl := jsonBody(t, w)
	enabledTplID := int(enabledTpl["id"].(float64))

	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "disabled-tpl", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create second template: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))

	// Disable the second template
	w = doAdmin("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/token-templates (only enabled)
	w = doUser("GET", "/api/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("user list templates: %d %s", w.Code, w.Body.String())
	}
	userList := jsonBody(t, w)
	if total := int(userList["total"].(float64)); total != 1 {
		t.Fatalf("user should see 1 enabled template, got %d", total)
	}

	// Normal user: POST /api/admin/token-templates (create) -> 403
	w = doUser("POST", "/api/admin/token-templates", map[string]any{
		"name": "hacker-tpl", "models": `["test"]`,
	})
	if w.Code != 403 {
		t.Fatalf("user create template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Normal user: PUT /api/admin/token-templates/:id -> 403
	w = doUser("PUT", "/api/admin/token-templates/"+itoa(enabledTplID), map[string]any{"name": "hacked"})
	if w.Code != 403 {
		t.Fatalf("user update template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Normal user: DELETE /api/admin/token-templates/:id -> 403
	w = doUser("DELETE", "/api/admin/token-templates/"+itoa(enabledTplID), nil)
	if w.Code != 403 {
		t.Fatalf("user delete template: expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Admin: GET /api/admin/token-templates sees both
	w = doAdmin("GET", "/api/admin/token-templates", nil)
	if w.Code != 200 {
		t.Fatalf("admin list templates: %d %s", w.Code, w.Body.String())
	}
	adminList := jsonBody(t, w)
	if total := int(adminList["total"].(float64)); total != 2 {
		t.Fatalf("admin should see 2 templates, got %d", total)
	}
}

func TestTokenCreationWithTemplate(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user with quota
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "creator", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := int(userResp["id"].(float64))

	w = doAdmin("PUT", "/api/admin/users/"+itoa(normalUserID)+"/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "creator", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Create enabled template (expiry_days=30, models=["gpt-4o"])
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "30day-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	tplID := int(tpl["id"].(float64))

	// Normal user creates token with template
	beforeCreate := time.Now().Unix()
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "my-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with template: %d %s", w.Code, w.Body.String())
	}
	tok := jsonBody(t, w)

	// Verify models == template.models
	if tok["models"] != `["gpt-4o"]` {
		t.Fatalf("expected models=[\"gpt-4o\"], got %v", tok["models"])
	}

	// Verify expired_at is approximately now + 30*86400 (within 10 seconds)
	expectedExpiry := beforeCreate + 30*86400
	actualExpiry := int64(tok["expired_at"].(float64))
	if math.Abs(float64(actualExpiry-expectedExpiry)) > 10 {
		t.Fatalf("expired_at %d not within 10s of expected %d", actualExpiry, expectedExpiry)
	}

	// Verify template_id is set
	if tok["template_id"] == nil || int(tok["template_id"].(float64)) != tplID {
		t.Fatalf("expected template_id=%d, got %v", tplID, tok["template_id"])
	}

	// Verify user_id == normal user's ID
	if int(tok["user_id"].(float64)) != normalUserID {
		t.Fatalf("expected user_id=%d, got %v", normalUserID, tok["user_id"])
	}

	// Create a disabled template
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "will-disable", "models": `["test"]`, "expiry_days": 7,
	})
	if w.Code != 201 {
		t.Fatalf("create template to disable: %d %s", w.Code, w.Body.String())
	}
	disabledTpl := jsonBody(t, w)
	disabledTplID := int(disabledTpl["id"].(float64))
	w = doAdmin("PUT", "/api/admin/token-templates/"+itoa(disabledTplID), map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("disable template: %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with disabled template -> 400
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "bad-token", "template_id": disabledTplID,
	})
	if w.Code != 400 {
		t.Fatalf("disabled template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Normal user creates token with non-existent template_id=999 -> 400
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "bad-token2", "template_id": 999,
	})
	if w.Code != 400 {
		t.Fatalf("non-existent template: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Admin creates token with template (should inherit template fields)
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-tpl-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("admin create token with template: %d %s", w.Code, w.Body.String())
	}

	// Admin creates token without template for another user
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": normalUserID, "name": "admin-token-for-user",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token without template: %d %s", w.Code, w.Body.String())
	}

	// Create template with expiry_days=-1 (never expires)
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "never-expire-tpl", "models": `["gpt-4o"]`, "expiry_days": -1,
	})
	if w.Code != 201 {
		t.Fatalf("create never-expire template: %d %s", w.Code, w.Body.String())
	}
	neverExpireTpl := jsonBody(t, w)
	neverExpireTplID := int(neverExpireTpl["id"].(float64))

	// Normal user creates token with never-expire template
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "forever-token", "template_id": neverExpireTplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token with never-expire template: %d %s", w.Code, w.Body.String())
	}
	foreverTok := jsonBody(t, w)
	if int64(foreverTok["expired_at"].(float64)) != -1 {
		t.Fatalf("expected expired_at=-1, got %v", foreverTok["expired_at"])
	}
}

func TestTokenEditRestrictions(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "editor", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	w = doAdmin("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 100000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "editor", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Create template
	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "edit-tpl", "models": `["gpt-4o"]`, "expiry_days": 30,
	})
	if w.Code != 201 {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	tpl := jsonBody(t, w)
	tplID := int(tpl["id"].(float64))

	// User creates token via template
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "edit-test-token", "template_id": tplID,
	})
	if w.Code != 201 {
		t.Fatalf("user create token: %d %s", w.Code, w.Body.String())
	}
	tok := jsonBody(t, w)
	tokID := int(tok["id"].(float64))
	origModels := tok["models"].(string)
	origExpiredAt := int64(tok["expired_at"].(float64))
	origStatus := int(tok["status"].(float64))
	tokPath := "/api/tokens/" + itoa(tokID)

	// Normal user updates name (allowed)
	w = doUser("PUT", tokPath, map[string]any{"name": "new-name"})
	if w.Code != 200 {
		t.Fatalf("user update name: %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if updated["name"] != "new-name" {
		t.Fatalf("expected name=new-name, got %v", updated["name"])
	}

	// Normal user updates trace_enabled (allowed)
	w = doUser("PUT", tokPath, map[string]any{"trace_enabled": true})
	if w.Code != 200 {
		t.Fatalf("user update trace_enabled: %d %s", w.Code, w.Body.String())
	}
	updated = jsonBody(t, w)
	if updated["trace_enabled"] != true {
		t.Fatalf("expected trace_enabled=true, got %v", updated["trace_enabled"])
	}

	// behavior change: disabled self-service now rejects model whitelist edits
	// explicitly instead of silently ignoring them.
	w = doUser("PUT", tokPath, map[string]any{"models": `["hacked"]`})
	if w.Code != http.StatusForbidden {
		t.Fatalf("user update models with self-service disabled: %d %s", w.Code, w.Body.String())
	}
	denied := jsonBody(t, w)
	if denied["code"] != "model_whitelist_edit_forbidden" {
		t.Fatalf("expected model_whitelist_edit_forbidden, got %v", denied["code"])
	}
	w = doUser("GET", tokPath, nil)
	if w.Code != 200 {
		t.Fatalf("get token: %d %s", w.Code, w.Body.String())
	}
	got := jsonBody(t, w)
	if got["models"].(string) != origModels {
		t.Fatalf("models should be unchanged: expected %q, got %q", origModels, got["models"])
	}

	if err := srv.DB.Create(&models.Setting{
		Key:   consts.SettingKeyTokenModelWhitelistSelfService,
		Value: "true",
	}).Error; err != nil {
		t.Fatalf("enable token model whitelist self-service: %v", err)
	}
	w = doUser("PUT", tokPath, map[string]any{"models": `["user-managed"]`})
	if w.Code != http.StatusOK {
		t.Fatalf("user update models with self-service enabled: %d %s", w.Code, w.Body.String())
	}
	updated = jsonBody(t, w)
	if updated["models"] != `["user-managed"]` {
		t.Fatalf("expected user-managed models, got %v", updated["models"])
	}

	// Normal user can self-disable their token (status -> 0 is always allowed)
	if origStatus != 1 {
		t.Fatalf("precondition: expected origStatus=1 to exercise self-disable, got %d", origStatus)
	}
	w = doUser("PUT", tokPath, map[string]any{"status": 0})
	if w.Code != 200 {
		t.Fatalf("user disable token: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	got = jsonBody(t, w)
	if int(got["status"].(float64)) != 0 {
		t.Fatalf("user disable should persist status=0, got %v", got["status"])
	}

	// Normal user can self-enable when balance > 0
	w = doUser("PUT", tokPath, map[string]any{"status": 1})
	if w.Code != 200 {
		t.Fatalf("user enable token with balance: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	got = jsonBody(t, w)
	if int(got["status"].(float64)) != 1 {
		t.Fatalf("user enable should persist status=1, got %v", got["status"])
	}

	// Normal user attempts to update expired_at (should be ignored)
	w = doUser("PUT", tokPath, map[string]any{"expired_at": 9999999999})
	if w.Code != 200 {
		t.Fatalf("user update expired_at: %d %s", w.Code, w.Body.String())
	}
	w = doUser("GET", tokPath, nil)
	got = jsonBody(t, w)
	if int64(got["expired_at"].(float64)) != origExpiredAt {
		t.Fatalf("expired_at should be unchanged: expected %d, got %v", origExpiredAt, got["expired_at"])
	}

	// Admin can update all fields on their own token
	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": 1, "name": "admin-edit-token",
	})
	if w.Code != 201 {
		t.Fatalf("admin create token: %d %s", w.Code, w.Body.String())
	}
	adminTok := jsonBody(t, w)
	adminTokID := int(adminTok["id"].(float64))
	adminTokPath := "/api/tokens/" + itoa(adminTokID)

	w = doAdmin("PUT", adminTokPath, map[string]any{
		"models": `["new-model"]`, "status": 0, "expired_at": 1234567890,
	})
	if w.Code != 200 {
		t.Fatalf("admin update all fields: %d %s", w.Code, w.Body.String())
	}
	adminUpdated := jsonBody(t, w)
	if adminUpdated["models"] != `["new-model"]` {
		t.Fatalf("admin models not updated: got %v", adminUpdated["models"])
	}
	if int(adminUpdated["status"].(float64)) != 0 {
		t.Fatalf("admin status not updated: got %v", adminUpdated["status"])
	}
	if int64(adminUpdated["expired_at"].(float64)) != 1234567890 {
		t.Fatalf("admin expired_at not updated: got %v", adminUpdated["expired_at"])
	}

	// Admin updating with illegal status value should be rejected
	w = doAdmin("PUT", adminTokPath, map[string]any{"status": 2})
	if w.Code != 400 {
		t.Fatalf("expected 400 for invalid status=2, got %d: %s", w.Code, w.Body.String())
	}
}

func TestTokenTraceModeAPI(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")
	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	for _, tc := range []struct {
		name     string
		body     map[string]any
		wantCode int
		wantMode string
	}{
		{name: "omitted defaults full", body: map[string]any{"user_id": 1, "name": "trace-default"}, wantCode: http.StatusCreated, wantMode: "full"},
		{name: "empty defaults full", body: map[string]any{"user_id": 1, "name": "trace-empty", "trace_mode": ""}, wantCode: http.StatusCreated, wantMode: "full"},
		{name: "explicit full", body: map[string]any{"user_id": 1, "name": "trace-full", "trace_mode": "full"}, wantCode: http.StatusCreated, wantMode: "full"},
		{name: "explicit headers", body: map[string]any{"user_id": 1, "name": "trace-headers", "trace_mode": "headers"}, wantCode: http.StatusCreated, wantMode: "headers"},
		{name: "unknown rejected", body: map[string]any{"user_id": 1, "name": "trace-unknown", "trace_mode": "body"}, wantCode: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := doAdmin("POST", "/api/tokens", tc.body)
			if w.Code != tc.wantCode {
				t.Fatalf("create token: got %d want %d: %s", w.Code, tc.wantCode, w.Body.String())
			}
			if tc.wantMode != "" {
				got := jsonBody(t, w)
				if got["trace_mode"] != tc.wantMode {
					t.Fatalf("trace_mode=%v want=%q", got["trace_mode"], tc.wantMode)
				}
			}
		})
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "trace-editor", "password": "pass1234", "role": 1,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userToken := loginHelper(t, srv, "trace-editor", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	w = doAdmin("POST", "/api/admin/token-templates", map[string]any{
		"name": "trace-template", "models": `[]`, "expiry_days": 30,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create template: %d %s", w.Code, w.Body.String())
	}
	templateID := int(jsonBody(t, w)["id"].(float64))
	w = doUser("POST", "/api/tokens", map[string]any{
		"name": "trace-user-token", "template_id": templateID,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create user token: %d %s", w.Code, w.Body.String())
	}
	userTokenID := int(jsonBody(t, w)["id"].(float64))
	path := "/api/tokens/" + itoa(userTokenID)

	// The new user has zero balance. Re-sending status=1 while changing only
	// trace mode is not an enable transition and must remain allowed.
	w = doUser("PUT", path, map[string]any{"status": 1, "trace_mode": "headers", "expired_at": 123})
	if w.Code != http.StatusOK {
		t.Fatalf("user update trace mode: %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if updated["trace_mode"] != "headers" {
		t.Fatalf("trace_mode=%v want=headers", updated["trace_mode"])
	}
	if int64(updated["expired_at"].(float64)) == 123 {
		t.Fatal("ordinary user changed restricted expired_at field")
	}

	w = doUser("PUT", path, map[string]any{"trace_mode": "body"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid trace mode: got %d want 400: %s", w.Code, w.Body.String())
	}
}

func TestAdminCanReassignRegularTokenToAnotherUser(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}
	createUser := func(username string) int {
		t.Helper()
		w := doAdmin("POST", "/api/admin/users", map[string]any{
			"username": username, "password": "pass1234", "role": 1,
		})
		if w.Code != 201 {
			t.Fatalf("create user %s: %d %s", username, w.Code, w.Body.String())
		}
		return int(jsonBody(t, w)["id"].(float64))
	}

	sourceUserID := createUser("token-owner-src")
	targetUserID := createUser("token-owner-dst")

	w := doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": sourceUserID, "name": "reassign-regular-token",
	})
	if w.Code != 201 {
		t.Fatalf("create regular token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": targetUserID})
	if w.Code != 200 {
		t.Fatalf("admin reassign regular token: expected 200, got %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if int(updated["user_id"].(float64)) != targetUserID {
		t.Fatalf("expected reassigned user_id=%d, got %v", targetUserID, updated["user_id"])
	}
}

func TestAdminCannotSetRegularTokenOwnerToZero(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "regular-owner", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create regular owner user: %d %s", w.Code, w.Body.String())
	}
	ownerUserID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": ownerUserID, "name": "regular-zero-owner-token",
	})
	if w.Code != 201 {
		t.Fatalf("create regular token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": 0})
	if w.Code != 400 {
		t.Fatalf("admin set regular token owner to zero: expected 400, got %d %s", w.Code, w.Body.String())
	}
}

func TestAdminCanSetSystemTestTokenOwnerToZero(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "system-test-owner", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create system test owner user: %d %s", w.Code, w.Body.String())
	}
	ownerUserID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("POST", "/api/tokens", map[string]any{
		"user_id": ownerUserID, "name": "__system_test__",
	})
	if w.Code != 201 {
		t.Fatalf("create __system_test__ token: %d %s", w.Code, w.Body.String())
	}
	tokID := int(jsonBody(t, w)["id"].(float64))

	w = doAdmin("PUT", "/api/tokens/"+itoa(tokID), map[string]any{"user_id": 0})
	if w.Code != 200 {
		t.Fatalf("admin set __system_test__ owner to zero: expected 200, got %d %s", w.Code, w.Body.String())
	}
	updated := jsonBody(t, w)
	if int(updated["user_id"].(float64)) != 0 {
		t.Fatalf("expected __system_test__ user_id=0, got %v", updated["user_id"])
	}
}

func TestLogAccessControl(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "loguser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := uint(userResp["id"].(float64))

	userToken := loginHelper(t, srv, "loguser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Insert logs directly into DB
	logDB := srv.App.GetLogDB()
	require.NotNil(t, logDB)
	adminLog := models.RequestLog(models.UsageLog{
		UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-4o",
		RequestID: "admin-req-1", Status: 1, PromptTokens: 100,
		CompletionTokens: 50, InputCost: 10, OutputCost: 5, TotalCost: 15,
	})
	require.NoError(t, logDB.Table(models.RequestLog{}.TableName()).Create(&adminLog).Error)
	userLog := models.RequestLog(models.UsageLog{
		UserID: normalUserID, TokenID: 2, ChannelID: 1, ModelName: "gpt-4o",
		RequestID: "user-req-1", Status: 1, PromptTokens: 200,
		CompletionTokens: 100, InputCost: 20, OutputCost: 10, TotalCost: 30,
	})
	require.NoError(t, logDB.Table(models.RequestLog{}.TableName()).Create(&userLog).Error)

	// Admin: GET /api/logs - sees all logs
	w = doAdmin("GET", "/api/logs", nil)
	if w.Code != 200 {
		t.Fatalf("admin list logs: %d %s", w.Code, w.Body.String())
	}
	adminLogs := jsonBody(t, w)
	if total := int(adminLogs["total"].(float64)); total < 2 {
		t.Fatalf("admin should see >= 2 logs, got %d", total)
	}

	// Normal user: GET /api/logs - sees only own logs
	w = doUser("GET", "/api/logs", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs: %d %s", w.Code, w.Body.String())
	}
	userLogs := jsonBody(t, w)
	data, _ := userLogs["data"].([]any)
	for _, item := range data {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("user sees log with user_id=%v, expected %d", logItem["user_id"], normalUserID)
		}
		// channel_id should be hidden (0)
		if int(logItem["channel_id"].(float64)) != 0 {
			t.Fatalf("channel_id should be 0 for normal user, got %v", logItem["channel_id"])
		}
	}

	// Normal user: GET /api/logs?user_id=1 - user_id param ignored
	w = doUser("GET", "/api/logs?user_id=1", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs with user_id filter: %d %s", w.Code, w.Body.String())
	}
	filteredLogs := jsonBody(t, w)
	filteredData, _ := filteredLogs["data"].([]any)
	for _, item := range filteredData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("user_id filter should be ignored for normal user, got user_id=%v", logItem["user_id"])
		}
	}

	// Admin: GET /api/logs?user_id=2 - can filter by user
	w = doAdmin("GET", "/api/logs?user_id="+itoa(int(normalUserID)), nil)
	if w.Code != 200 {
		t.Fatalf("admin filter by user: %d %s", w.Code, w.Body.String())
	}
	adminFiltered := jsonBody(t, w)
	adminFilteredData, _ := adminFiltered["data"].([]any)
	for _, item := range adminFilteredData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("admin filter: expected user_id=%d, got %v", normalUserID, logItem["user_id"])
		}
	}

	// Normal user: GET /api/logs?channel_id=1 - channel_id ignored
	w = doUser("GET", "/api/logs?channel_id=1", nil)
	if w.Code != 200 {
		t.Fatalf("user list logs with channel_id filter: %d %s", w.Code, w.Body.String())
	}
	// Should still return the user's own logs without error
	channelFilterLogs := jsonBody(t, w)
	channelFilterData, _ := channelFilterLogs["data"].([]any)
	for _, item := range channelFilterData {
		logItem := item.(map[string]any)
		if uint(logItem["user_id"].(float64)) != normalUserID {
			t.Fatalf("channel_id filter should not leak other user logs, got user_id=%v", logItem["user_id"])
		}
	}
}

func TestTraceAccessControl(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "traceuser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	userResp := jsonBody(t, w)
	normalUserID := uint(userResp["id"].(float64))

	userToken := loginHelper(t, srv, "traceuser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Insert usage logs and traces
	logDB := srv.App.GetLogDB()
	require.NotNil(t, logDB)
	adminLog := models.RequestLog(models.UsageLog{
		UserID: 1, TokenID: 1, ChannelID: 1,
		RequestID: "admin-trace-req", Status: 1, HasTrace: true,
	})
	require.NoError(t, logDB.Table(models.RequestLog{}.TableName()).Create(&adminLog).Error)
	adminTrace := models.RequestTrace(models.UsageLogTrace{
		RequestID: "admin-trace-req", InboundPath: "/v1/chat/completions",
	})
	require.NoError(t, logDB.Table(models.RequestTrace{}.TableName()).Create(&adminTrace).Error)

	userLog := models.RequestLog(models.UsageLog{
		UserID: normalUserID, TokenID: 2, ChannelID: 1,
		RequestID: "user-trace-req", Status: 1, HasTrace: true,
	})
	require.NoError(t, logDB.Table(models.RequestLog{}.TableName()).Create(&userLog).Error)
	userTrace := models.RequestTrace(models.UsageLogTrace{
		RequestID: "user-trace-req", InboundPath: "/v1/chat/completions",
	})
	require.NoError(t, logDB.Table(models.RequestTrace{}.TableName()).Create(&userTrace).Error)

	// Admin: GET /api/logs/admin-trace-req/trace - succeeds
	w = doAdmin("GET", "/api/logs/admin-trace-req/trace", nil)
	if w.Code != 200 {
		t.Fatalf("admin get own trace: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/logs/user-trace-req/trace - own trace, succeeds
	w = doUser("GET", "/api/logs/user-trace-req/trace", nil)
	if w.Code != 200 {
		t.Fatalf("user get own trace: %d %s", w.Code, w.Body.String())
	}

	// Normal user: GET /api/logs/admin-trace-req/trace - other's trace, denied
	w = doUser("GET", "/api/logs/admin-trace-req/trace", nil)
	if w.Code != 404 {
		t.Fatalf("user get other's trace: expected 404, got %d %s", w.Code, w.Body.String())
	}

	// Non-existent trace
	w = doAdmin("GET", "/api/logs/nonexistent/trace", nil)
	if w.Code != 404 {
		t.Fatalf("non-existent trace: expected 404, got %d %s", w.Code, w.Body.String())
	}
}

func TestStatsScope(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	doAdmin := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, adminToken, method, path, body)
	}

	// Create normal user with quota
	w := doAdmin("POST", "/api/admin/users", map[string]any{
		"username": "statsuser", "password": "pass1234", "role": 1,
	})
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body.String())
	}
	w = doAdmin("PUT", "/api/admin/users/2/quota", map[string]any{"delta": 50000})
	if w.Code != 200 {
		t.Fatalf("set quota: %d %s", w.Code, w.Body.String())
	}

	userToken := loginHelper(t, srv, "statsuser", "pass1234")
	doUser := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, userToken, method, path, body)
	}

	// Admin: GET /api/stats/overview
	w = doAdmin("GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("admin stats overview: %d %s", w.Code, w.Body.String())
	}
	adminStats := jsonBody(t, w)
	// Should have admin-only fields
	for _, field := range []string{"users", "channels", "tokens", "usage_logs", "total_cost"} {
		if _, ok := adminStats[field]; !ok {
			t.Errorf("admin stats missing field: %s", field)
		}
	}
	// Should NOT have user-only fields
	if _, ok := adminStats["quota"]; ok {
		t.Error("admin stats should not have quota field")
	}
	if _, ok := adminStats["used_quota"]; ok {
		t.Error("admin stats should not have used_quota field")
	}

	// Normal user: GET /api/stats/overview
	w = doUser("GET", "/api/stats/overview", nil)
	if w.Code != 200 {
		t.Fatalf("user stats overview: %d %s", w.Code, w.Body.String())
	}
	userStats := jsonBody(t, w)
	// Should have user fields
	for _, field := range []string{"tokens", "usage_logs", "total_cost", "quota", "used_quota"} {
		if _, ok := userStats[field]; !ok {
			t.Errorf("user stats missing field: %s", field)
		}
	}
	// Should NOT have admin-only fields
	for _, field := range []string{"users", "channels", "agents", "connected_agents"} {
		if _, ok := userStats[field]; ok {
			t.Errorf("user stats should not have field: %s", field)
		}
	}

	// Admin: GET /api/stats/trend
	w = doAdmin("GET", "/api/stats/trend", nil)
	if w.Code != 200 {
		t.Fatalf("admin trend: %d %s", w.Code, w.Body.String())
	}
	trendResp := jsonBody(t, w)
	if _, ok := trendResp["items"]; !ok {
		t.Error("trend response missing items")
	}

	// Normal user: GET /api/stats/trend
	w = doUser("GET", "/api/stats/trend", nil)
	if w.Code != 200 {
		t.Fatalf("user trend: %d %s", w.Code, w.Body.String())
	}
	userTrend := jsonBody(t, w)
	if _, ok := userTrend["items"]; !ok {
		t.Error("user trend response missing items")
	}

	// Trend with custom days
	w = doAdmin("GET", "/api/stats/trend?days=7", nil)
	if w.Code != 200 {
		t.Fatalf("trend with days=7: %d %s", w.Code, w.Body.String())
	}
}

func TestRegistrationValidation(t *testing.T) {
	srv := setupTestMaster(t)
	srv.InitAdminUser("admin", "admin123")
	adminToken := loginHelper(t, srv, "admin", "admin123")

	// Enable registration
	w := reqHelper(srv, adminToken, "PUT", "/api/admin/system/settings", map[string]any{
		"settings": map[string]any{"registration_enabled": "true"},
	})
	if w.Code != 200 {
		t.Fatalf("enable registration: %d %s", w.Code, w.Body.String())
	}

	doPublic := func(method, path string, body any) *httptest.ResponseRecorder {
		return reqHelper(srv, "", method, path, body)
	}

	// Short username (< 3 chars) -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "ab", "email": "ab@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("short username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Short password (< 8 chars) -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "validuser", "email": "validuser@test.example.com", "password": "short"})
	if w.Code != 400 {
		t.Fatalf("short password: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Username with special chars -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "user@name", "email": "username@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("special chars username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Empty username -> 400
	w = doPublic("POST", "/api/register", map[string]any{"username": "", "email": "empty@test.example.com", "password": "password123"})
	if w.Code != 400 {
		t.Fatalf("empty username: expected 400, got %d %s", w.Code, w.Body.String())
	}

	// Valid registration -> 201
	w = doPublic("POST", "/api/register", map[string]any{"username": "valid_user_123", "email": "valid_user_123@test.example.com", "password": "longpassword"})
	if w.Code != 201 {
		t.Fatalf("valid registration: expected 201, got %d %s", w.Code, w.Body.String())
	}

	// New user role should be 1 (normal user) - verify via admin API
	w = reqHelper(srv, adminToken, "GET", "/api/admin/users", nil)
	if w.Code != 200 {
		t.Fatalf("admin list users: %d %s", w.Code, w.Body.String())
	}
	usersResp := jsonBody(t, w)
	usersData, _ := usersResp["data"].([]any)
	found := false
	for _, u := range usersData {
		user := u.(map[string]any)
		if user["username"] == "valid_user_123" {
			found = true
			if int(user["role"].(float64)) != 1 {
				t.Fatalf("new user role should be 1, got %v", user["role"])
			}
			break
		}
	}
	if !found {
		t.Fatal("registered user not found in admin user list")
	}
}

func TestChannelTest_E2EHitsRelayNotSPA(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"chatcmpl-e2e","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer upstream.Close()

	srv := setupTestMaster(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown test master: %v", err)
		}
	})
	srv.InitAdminUser("admin", "admin123")

	// Start the master router on a real loopback listener and update the channel
	// handler's MasterListen to match. Without this the handler would have
	// MasterListen=":0" from setupTestMaster which is meaningless after the OS
	// picks a real port.
	ts := httptest.NewServer(srv.Router)
	defer ts.Close()
	tsURL, _ := url.Parse(ts.URL)
	srv.SetChannelMasterListen(":" + tsURL.Port())

	// Mount relay routes so the channel test API can reach /v1/chat/completions.
	// In production this happens inside Run() via setupEmbeddedAgent; in tests we
	// call the exported shim to avoid spinning up a real net.Listener.
	if err := srv.SetupEmbeddedAgentForTest(tsURL.Host); err != nil {
		t.Fatalf("setup embedded agent: %v", err)
	}

	jwt := loginAsAdmin(t, srv, "admin", "admin123")

	chID := createChannelE2E(t, srv, jwt, "e2e-stub", upstream.URL, "gpt-4")

	testBody, _ := json.Marshal(map[string]any{
		"model":         "gpt-4",
		"endpoint_type": "chat-completion",
		"stream":        false,
	})

	// The relay's auth middleware reads the __system_test__ token from the
	// embedded agent's cache, which is populated asynchronously via WS sync.
	// On a fast machine the first request can race ahead of cache propagation
	// and receive a 401 "invalid api key". Retry for up to 5 s, but only on
	// that specific symptom — any other failure mode aborts immediately.
	var testResp struct {
		Success    bool   `json:"success"`
		StatusCode int    `json:"status_code"`
		Response   string `json:"response"`
		Error      string `json:"error"`
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		httpReq, _ := http.NewRequest("POST", ts.URL+"/api/admin/channels/"+chID+"/test", bytes.NewReader(testBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+jwt)
		httpResp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			t.Fatalf("test request failed: %v", err)
		}
		body, _ := io.ReadAll(httpResp.Body)
		httpResp.Body.Close()

		if httpResp.StatusCode != http.StatusOK {
			t.Fatalf("API call failed: %d %s", httpResp.StatusCode, string(body))
		}
		if err := json.Unmarshal(body, &testResp); err != nil {
			t.Fatalf("decode response: %v body=%s", err, string(body))
		}

		// Retry only while the embedded agent cache is still syncing the
		// __system_test__ token (relay returns 401 "invalid api key").
		if !testResp.Success && strings.Contains(testResp.Response, "invalid api key") && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			// Re-encode body for next iteration
			testBody, _ = json.Marshal(map[string]any{
				"model":         "gpt-4",
				"endpoint_type": "chat-completion",
				"stream":        false,
			})
			continue
		}
		break
	}

	if !testResp.Success {
		t.Fatalf("expected Success=true, got %+v", testResp)
	}
	// The relay rewrites the upstream response ID, so we check for structural
	// markers that confirm we got a real chat completion JSON payload, not SPA HTML.
	if !strings.Contains(testResp.Response, `"object":"chat.completion"`) &&
		!strings.Contains(testResp.Response, `"choices"`) {
		t.Errorf("expected chat completion JSON in Response, got %q", testResp.Response)
	}
	if strings.Contains(testResp.Response, "<!DOCTYPE html>") || strings.Contains(testResp.Response, "__next_f") {
		t.Errorf("Response looks like SPA fallback HTML: %q", testResp.Response)
	}
}
