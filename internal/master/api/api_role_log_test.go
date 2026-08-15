package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/master/apirbac"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

// Break caught: placing API role administration on the user-level API group
// would let an ordinary JWT mutate global API authorization state. Omitting
// the route entirely would also hide that bad placement behind a 404.
func TestAPIRoleAndBindingEndpointsAreAdminOnly(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	brokenRole := models.Role{
		Key: "compiler-failure", Name: "Compiler failure", Status: consts.StatusEnabled,
	}
	require.NoError(t, srv.DB.Create(&brokenRole).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{
		RoleID: brokenRole.ID, PermissionID: 999999,
	}).Error)
	_, compileErr := apirbac.NewRoleCompiler(
		dao.NewAdminQuery(dao.NewContext(srv.App)).APIRBAC(),
	).CompileAPIRoles(context.Background())
	require.ErrorContains(t, compileErr, "permission is missing")
	user := models.User{Username: "role-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	for _, path := range []string{"/api/admin/api-roles", "/api/admin/api-role-bindings"} {
		t.Run(path, func(t *testing.T) {
			unauthenticated := reqHelper(srv, "", http.MethodGet, path, nil)
			require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
			nonAdmin := reqHelper(srv, userJWT, http.MethodGet, path, nil)
			require.Equal(t, http.StatusForbidden, nonAdmin.Code, nonAdmin.Body.String())
			admin := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
			require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
		})
	}
}

// Break caught: returning an empty log page when the split log database is
// unavailable masks an operational outage as a legitimate absence of data.
func TestAPIRequestLogsReturn503WhenLogDatabaseUnavailable(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(nil)

	response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs", nil)
	require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "LogDatabaseUnavailable")
}

// behavior change: request logs are a control-plane view. Invoke grants do not
// admit ordinary users, while administrators see the unscoped log page.
func TestAPIRequestLogsAreAdminOnlyAndUnscoped(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)
	visible := models.APIService{Slug: "visible", Name: "Visible", Status: consts.StatusEnabled}
	hidden := models.APIService{Slug: "hidden", Name: "Hidden", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&visible).Error)
	require.NoError(t, srv.DB.Create(&hidden).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "visible-request", APIServiceID: visible.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "hidden-request", APIServiceID: hidden.ID}).Error)
	user := models.User{Username: "scoped-log-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)

	viewer := reqHelper(srv, userJWT, http.MethodGet, "/api/admin/api-request-logs?page=1&page_size=1", nil)
	require.Equal(t, http.StatusForbidden, viewer.Code, viewer.Body.String())

	admin := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs", nil)
	require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	var adminBody struct {
		Total int64 `json:"total"`
	}
	require.NoError(t, json.Unmarshal(admin.Body.Bytes(), &adminBody))
	require.Equal(t, int64(2), adminBody.Total)
}

// Break caught: recreating a trace from raw traffic, rather than returning the
// persisted redacted capture, could disclose an Authorization secret that the
// capture policy intentionally removed.
func TestAPIRequestTraceReturnsRedactedCaptureAndFlags(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)
	service := models.APIService{Slug: "trace-service", Name: "Trace service", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "trace-request", APIServiceID: service.ID}).Error)
	trace := models.APIRequestTrace{
		RequestID: "trace-request",
		SourceRequestHeaders: datatypes.NewJSONType(map[string][]string{
			"Authorization": {"[REDACTED]"},
		}),
		SourceRequestBody: datatypes.NewJSONType(models.APIBodyCapture{
			Captured: true, Data: "[REDACTED]", Truncated: true, CapturedBytes: 64, TotalBytes: 128,
		}),
		ResponseBody: datatypes.NewJSONType(models.APIBodyCapture{Captured: false, SkipReason: "capture_disabled"}),
	}
	require.NoError(t, srv.DB.Create(&trace).Error)
	response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/trace-request/trace", nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Contains(t, response.Body.String(), "[REDACTED]")
	require.Contains(t, response.Body.String(), "\"truncated\":true")
	require.Contains(t, response.Body.String(), "capture_disabled")
	require.NotContains(t, response.Body.String(), "Bearer raw-secret")
}

// Break caught: personal logs are independent from invoke grants, while all
// Generic API management capabilities remain administrator-only.
func TestCapabilitiesExposeGenericAPIFeaturesByViewer(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	service := models.APIService{Slug: "capability-service", Name: "Capability service", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	viewer := models.User{Username: "capability-viewer", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	globalManager := models.User{Username: "capability-global", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	denied := models.User{Username: "capability-denied", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&viewer).Error)
	require.NoError(t, srv.DB.Create(&globalManager).Error)
	require.NoError(t, srv.DB.Create(&denied).Error)
	role := models.Role{Key: "capability-reader", Name: "Capability reader", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: service.ID, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: viewer.ID, RoleID: role.ID}).Error)
	globalRole := models.Role{Key: "capability-global", Name: "Capability global", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&globalRole).Error)
	globalPermission := models.Permission{Resource: models.APIResourceService, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Where(
		"resource = ? AND resource_id = ? AND action = ?",
		globalPermission.Resource, globalPermission.ResourceID, globalPermission.Action,
	).First(&globalPermission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: globalRole.ID, PermissionID: globalPermission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: globalManager.ID, RoleID: globalRole.ID}).Error)

	readCapabilities := func(t *testing.T, token string) struct {
		GenericAPI struct {
			Services  bool `json:"services"`
			Access    bool `json:"access"`
			Logs      bool `json:"logs"`
			WebSocket bool `json:"websocket"`
		} `json:"generic_api"`
	} {
		t.Helper()
		response := reqHelper(srv, token, http.MethodGet, "/api/capabilities", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			GenericAPI struct {
				Services  bool `json:"services"`
				Access    bool `json:"access"`
				Logs      bool `json:"logs"`
				WebSocket bool `json:"websocket"`
			} `json:"generic_api"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return body
	}
	viewerJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, viewer.ID, viewer.Role, viewer.Username, "", "")
	require.NoError(t, err)
	viewerCapabilities := readCapabilities(t, viewerJWT)
	require.False(t, viewerCapabilities.GenericAPI.Services)
	require.True(t, viewerCapabilities.GenericAPI.Logs)
	require.False(t, viewerCapabilities.GenericAPI.Access)
	require.False(t, viewerCapabilities.GenericAPI.WebSocket)
	globalJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, globalManager.ID, globalManager.Role, globalManager.Username, "", "")
	require.NoError(t, err)
	globalCapabilities := readCapabilities(t, globalJWT)
	require.False(t, globalCapabilities.GenericAPI.Services)
	require.True(t, globalCapabilities.GenericAPI.Logs)
	// API Access administers global roles/bindings and remains platform-admin-only,
	// even when an ordinary user can manage every API service.
	require.False(t, globalCapabilities.GenericAPI.Access)
	require.False(t, globalCapabilities.GenericAPI.WebSocket)
	deniedJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, denied.ID, denied.Role, denied.Username, "", "")
	require.NoError(t, err)
	deniedCapabilities := readCapabilities(t, deniedJWT)
	require.False(t, deniedCapabilities.GenericAPI.Services)
	require.True(t, deniedCapabilities.GenericAPI.Logs)
	adminCapabilities := readCapabilities(t, loginAsAdmin(t, srv, "admin", "admin123"))
	require.True(t, adminCapabilities.GenericAPI.Services)
	require.True(t, adminCapabilities.GenericAPI.Access)
	require.True(t, adminCapabilities.GenericAPI.Logs)
	require.True(t, adminCapabilities.GenericAPI.WebSocket)
}

// Break caught: writing a binding without checking the principal or notifying
// the targeted RoleSet cache leaves either dangling authorization or a stale
// Agent-side authorization decision.
func TestAPIRoleBindingValidatesPrincipalUniquenessAndInvalidatesUser(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "bound-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "bound-reader", Name: "Bound reader", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)

	invalidZero := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": "user", "principal_id": 0, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, invalidZero.Code, invalidZero.Body.String())
	missingUser := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": "user", "principal_id": 99999, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, missingUser.Code, missingUser.Body.String())

	var invalidated []uint
	subscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": "user", "principal_id": user.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	require.Equal(t, []uint{user.ID}, invalidated)

	duplicate := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": "user", "principal_id": user.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, duplicate.Code, duplicate.Body.String())
	var gatewayAdmin models.Role
	require.NoError(t, srv.DB.Where("key = ?", models.GatewayAdminRoleKey).First(&gatewayAdmin).Error)
	builtin := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": "user", "principal_id": user.ID, "role_id": gatewayAdmin.ID,
	})
	require.Equal(t, http.StatusBadRequest, builtin.Code, builtin.Body.String())
}

// Break caught: accepting an invalid grant, duplicate role key, or deletion
// of gateway_admin would either produce an unusable role set or let an admin
// destroy the role the compiler derives for every administrator.
func TestAPIRoleMutationValidatesActionsScopesAndBuiltinProtection(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	invalid := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "reader", "name": "Reader", "permissions": []map[string]any{{
			"resource": "api_service", "action": "erase",
		}},
	})
	require.Equal(t, http.StatusBadRequest, invalid.Code, invalid.Body.String())
	invalidScope := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "missing-scope", "name": "Missing scope", "permissions": []map[string]any{{
			"resource": "api_service", "resource_id": 99999, "action": "invoke",
		}},
	})
	require.Equal(t, http.StatusBadRequest, invalidScope.Code, invalidScope.Body.String())

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "reader", "name": "Reader", "permissions": []map[string]any{{
			"resource": "api_service", "action": "invoke",
		}},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var role models.Role
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &role))
	require.NotZero(t, role.ID)

	duplicate := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "reader", "name": "Duplicate",
	})
	require.Equal(t, http.StatusBadRequest, duplicate.Code, duplicate.Body.String())

	var gatewayAdmin models.Role
	require.NoError(t, srv.DB.Where("key = ?", models.GatewayAdminRoleKey).First(&gatewayAdmin).Error)
	builtinDelete := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-roles/%d", gatewayAdmin.ID), nil)
	require.Equal(t, http.StatusBadRequest, builtinDelete.Code, builtinDelete.Body.String())
}

// Break caught: omitting the role update route leaves administrators unable to
// atomically replace a role and its permission set after creation.
func TestAPIRoleUpdateReplacesRoleAndPermissions(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "role-update", Name: "Role update", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	role := models.Role{Key: "before-update", Name: "Before update", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	oldPermission := models.Permission{Resource: models.APIResourceService, ResourceID: 99, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&oldPermission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: oldPermission.ID}).Error)

	invalidRouteWildcard := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": "invalid-route-wildcard", "name": "Invalid route wildcard", "status": consts.StatusEnabled,
		"permissions": []map[string]any{{
			"resource": models.APIResourceRoute, "resource_id": 0, "action": models.APIPermissionInvoke,
		}},
	})
	require.Equal(t, http.StatusBadRequest, invalidRouteWildcard.Code, invalidRouteWildcard.Body.String())

	response := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": "after-update", "name": "After update", "description": "updated", "status": consts.StatusEnabled,
		"permissions": []map[string]any{{
			"resource": models.APIResourceService, "resource_id": service.ID, "action": models.APIPermissionInvoke,
		}},
	})
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())

	require.NoError(t, srv.DB.First(&role, role.ID).Error)
	require.Equal(t, "after-update", role.Key)
	var links []models.RolePermission
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Find(&links).Error)
	require.Len(t, links, 1)
	var permission models.Permission
	require.NoError(t, srv.DB.First(&permission, links[0].PermissionID).Error)
	require.Equal(t, models.APIPermissionInvoke, permission.Action)
	require.Equal(t, service.ID, permission.ResourceID)
}

// Break caught: deleting a binding without its pre-delete principal snapshot
// cannot target the RoleSet invalidation after the row has been removed.
func TestAPIRoleBindingDeleteInvalidatesSnapshotPrincipal(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "binding-delete", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "binding-delete", Name: "Binding delete", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}
	require.NoError(t, srv.DB.Create(&binding).Error)

	var invalidated []uint
	subscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	response := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var count int64
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Where("id = ?", binding.ID).Count(&count).Error)
	require.Zero(t, count)
	require.Equal(t, []uint{user.ID}, invalidated)
}

// Break caught: applying role fields and permission replacement outside one
// Core transaction leaves a partially updated role when a later scope is invalid.
func TestAPIRoleUpdateRollsBackRoleAndPermissions(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "rollback-role", Name: "Rollback role", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	role := models.Role{Key: "rollback-before", Name: "Rollback before", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	oldPermission := models.Permission{Resource: models.APIResourceService, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Where(
		"resource = ? AND resource_id = ? AND action = ?",
		oldPermission.Resource, oldPermission.ResourceID, oldPermission.Action,
	).First(&oldPermission).Error)
	oldLink := models.RolePermission{RoleID: role.ID, PermissionID: oldPermission.ID}
	require.NoError(t, srv.DB.Create(&oldLink).Error)
	published := 0
	subscription, err := events.Subscribe(srv.Bus, events.APIRoleUpdateTopic, func(_ context.Context, _ protocol.SyncedAPIRole) error {
		published++
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	response := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": "rollback-after", "name": "Rollback after", "status": consts.StatusEnabled,
		"permissions": []map[string]any{
			{"resource": models.APIResourceService, "resource_id": service.ID, "action": models.APIPermissionInvoke},
			{"resource": models.APIResourceRoute, "resource_id": 99999, "action": models.APIPermissionInvoke},
		},
	})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	require.Zero(t, published)

	var stored models.Role
	require.NoError(t, srv.DB.First(&stored, role.ID).Error)
	require.Equal(t, "rollback-before", stored.Key)
	var links []models.RolePermission
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Find(&links).Error)
	require.Equal(t, []models.RolePermission{oldLink}, links)
	var newPermissionCount int64
	require.NoError(t, srv.DB.Model(&models.Permission{}).Where(
		"resource = ? AND resource_id = ? AND action = ?",
		models.APIResourceService, service.ID, models.APIPermissionInvoke,
	).Count(&newPermissionCount).Error)
	require.Zero(t, newPermissionCount)
}

// Break caught: decoding an omitted status as zero disables a role by accident,
// while publishing an update for an explicitly disabled role leaves Agents with
// the old compiled grant instead of sending the required role-delete event.
func TestAPIRoleUpdatePreservesOmittedStatusAndPublishesDisableAsDelete(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	role := models.Role{Key: "status-transition", Name: "Status transition", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Where(
		"resource = ? AND resource_id = ? AND action = ?",
		permission.Resource, permission.ResourceID, permission.Action,
	).First(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)

	var updated, deleted []uint
	updateSubscription, err := events.Subscribe(srv.Bus, events.APIRoleUpdateTopic, func(_ context.Context, event protocol.SyncedAPIRole) error {
		updated = append(updated, event.ID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = updateSubscription.Unsubscribe() })
	deleteSubscription, err := events.Subscribe(srv.Bus, events.APIRoleDeleteTopic, func(_ context.Context, event protocol.SyncedAPIRole) error {
		deleted = append(deleted, event.ID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = deleteSubscription.Unsubscribe() })

	updateBody := map[string]any{
		"key": role.Key, "name": role.Name,
		"permissions": []map[string]any{{"resource": permission.Resource, "action": permission.Action}},
	}
	omitted := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), updateBody)
	require.Equal(t, http.StatusOK, omitted.Code, omitted.Body.String())
	require.NoError(t, srv.DB.First(&role, role.ID).Error)
	require.Equal(t, consts.StatusEnabled, role.Status)
	require.Equal(t, []uint{role.ID}, updated)
	require.Empty(t, deleted)

	updateBody["status"] = consts.StatusDisabled
	disabled := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), updateBody)
	require.Equal(t, http.StatusOK, disabled.Code, disabled.Body.String())
	require.NoError(t, srv.DB.First(&role, role.ID).Error)
	require.Equal(t, consts.StatusDisabled, role.Status)
	require.Equal(t, []uint{role.ID}, updated)
	require.Equal(t, []uint{role.ID}, deleted)

	updateBody["status"] = consts.StatusEnabled
	reenabled := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), updateBody)
	require.Equal(t, http.StatusOK, reenabled.Code, reenabled.Body.String())
	require.Equal(t, []uint{role.ID, role.ID}, updated)
	require.Equal(t, []uint{role.ID}, deleted)
}
