package api_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// behavior change: invoke grants never expose the admin log control plane;
// every ordinary user gets only the separately scoped portal log surface.
func TestAPIRequestLogInvokeGrantsDoNotExposeAdminLogs(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)
	services := []models.APIService{
		{Slug: "log-scope-a", Name: "Log scope A", Status: consts.StatusEnabled},
		{Slug: "log-scope-b", Name: "Log scope B", Status: consts.StatusEnabled},
	}
	for i := range services {
		require.NoError(t, srv.DB.Create(&services[i]).Error)
		requestID := fmt.Sprintf("log-scope-%d", i)
		require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: requestID, APIServiceID: services[i].ID}).Error)
		require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: requestID}).Error)
	}
	users := []models.User{
		{Username: "log-scope-specific", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "log-scope-global", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "log-scope-empty", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
	}
	for i := range users {
		require.NoError(t, srv.DB.Create(&users[i]).Error)
	}
	specificRole := models.Role{Key: "log-scope-specific", Name: "Log scope specific", Status: consts.StatusEnabled}
	globalRole := models.Role{Key: "log-scope-global", Name: "Log scope global", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&specificRole).Error)
	require.NoError(t, srv.DB.Create(&globalRole).Error)
	specificPermission := models.Permission{
		Resource: models.APIResourceService, ResourceID: services[0].ID, Action: models.APIPermissionInvoke,
	}
	require.NoError(t, srv.DB.Create(&specificPermission).Error)
	globalPermission := models.Permission{Resource: models.APIResourceService, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Where(
		"resource = ? AND resource_id = ? AND action = ?",
		globalPermission.Resource, globalPermission.ResourceID, globalPermission.Action,
	).First(&globalPermission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: specificRole.ID, PermissionID: specificPermission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: globalRole.ID, PermissionID: globalPermission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: users[0].ID, RoleID: specificRole.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: users[1].ID, RoleID: globalRole.ID}).Error)

	tokens := make([]string, len(users))
	for i, user := range users {
		var err error
		tokens[i], err = middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
		require.NoError(t, err)
	}
	readTotal := func(t *testing.T, token string) int64 {
		t.Helper()
		response := reqHelper(srv, token, http.MethodGet, "/api/admin/api-request-logs?page=1&page_size=1", nil)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
		return -1
	}
	readLogCapability := func(t *testing.T, token string) (servicesVisible, logsVisible bool) {
		t.Helper()
		response := reqHelper(srv, token, http.MethodGet, "/api/capabilities", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			GenericAPI struct {
				Services bool `json:"services"`
				Logs     bool `json:"logs"`
			} `json:"generic_api"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return body.GenericAPI.Services, body.GenericAPI.Logs
	}

	require.Equal(t, int64(-1), readTotal(t, tokens[0]))
	require.Equal(t, http.StatusForbidden, reqHelper(srv, tokens[0], http.MethodGet, "/api/admin/api-request-logs/log-scope-0/trace", nil).Code)
	require.Equal(t, http.StatusForbidden, reqHelper(srv, tokens[0], http.MethodGet, "/api/admin/api-request-logs/log-scope-1/trace", nil).Code)
	servicesVisible, logsVisible := readLogCapability(t, tokens[0])
	require.False(t, servicesVisible)
	require.True(t, logsVisible)

	require.Equal(t, int64(-1), readTotal(t, tokens[1]))
	require.Equal(t, http.StatusForbidden, reqHelper(srv, tokens[1], http.MethodGet, "/api/admin/api-request-logs/log-scope-1/trace", nil).Code)
	servicesVisible, logsVisible = readLogCapability(t, tokens[1])
	require.False(t, servicesVisible)
	require.True(t, logsVisible)

	require.Equal(t, int64(-1), readTotal(t, tokens[2]))
	require.Equal(t, http.StatusForbidden, reqHelper(srv, tokens[2], http.MethodGet, "/api/admin/api-request-logs/log-scope-0/trace", nil).Code)
	servicesVisible, logsVisible = readLogCapability(t, tokens[2])
	require.False(t, servicesVisible)
	require.True(t, logsVisible)
}

// Break caught: treating every trace DAO failure as a missing row hides
// operational database failures behind a misleading 404 response.
func TestAPIRequestTraceDistinguishesNotFoundUnavailableAndQueryFailure(t *testing.T) {
	t.Run("missing trace returns not found", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
		srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
		srv.App.SetLogDB(srv.DB)

		response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/missing/trace", nil)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	})

	t.Run("unavailable log database returns service unavailable", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
		srv.App.SetLogDB(nil)

		response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/unavailable/trace", nil)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
	})

	t.Run("unexpected query failure returns internal server error", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
		srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
		srv.App.SetLogDB(srv.DB)
		require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "query-failure"}).Error)
		require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: "query-failure"}).Error)

		queryFailure := errors.New("forced trace query failure")
		const callbackName = "test:fail_api_request_trace_query"
		require.NoError(t, srv.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "api_request_traces" {
				tx.AddError(queryFailure)
			}
		}))
		t.Cleanup(func() { require.NoError(t, srv.DB.Callback().Query().Remove(callbackName)) })

		response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/query-failure/trace", nil)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	})
}
