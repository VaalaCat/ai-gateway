package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// behavior change: log detail is part of the admin control plane; invoke
// permissions never authorize this endpoint.
func TestAPIRequestLogDetailIsAdminOnly(t *testing.T) {
	srv, adminToken := newAPIRequestLogDetailServer(t)
	service := models.APIService{Slug: "detail-service", Name: "Detail service", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	entry := models.APIRequestLog{RequestID: "detail-visible", APIServiceID: service.ID, StatusCode: http.StatusCreated}
	require.NoError(t, srv.DB.Create(&entry).Error)

	readerToken := createAPIRequestLogViewer(t, srv, "detail-reader", service.ID)
	t.Run("invoke-only viewer is forbidden", func(t *testing.T) {
		response := reqHelper(srv, readerToken, http.MethodGet, "/api/admin/api-request-logs/detail-visible", nil)
		require.Equal(t, http.StatusForbidden, response.Code, response.Body.String())
	})

	t.Run("administrator reads detail", func(t *testing.T) {
		response := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/detail-visible", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var got models.APIRequestLog
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &got))
		require.Equal(t, entry.RequestID, got.RequestID)
		require.Equal(t, entry.APIServiceID, got.APIServiceID)
		require.Equal(t, entry.StatusCode, got.StatusCode)
	})
}

// behavior change: detail reads preserve the list/trace distinction between
// missing rows, an unavailable Log DB, and unexpected query failures.
func TestAPIRequestLogDetailDistinguishesReadFailures(t *testing.T) {
	t.Run("missing log returns not found", func(t *testing.T) {
		srv, adminToken := newAPIRequestLogDetailServer(t)
		response := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/missing", nil)
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	})

	t.Run("unavailable log database returns service unavailable", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
		srv.App.SetLogDB(nil)

		response := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/unavailable", nil)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "LogDatabaseUnavailable")
	})

	t.Run("unexpected log query failure returns internal server error", func(t *testing.T) {
		srv, adminToken := newAPIRequestLogDetailServer(t)
		require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: "detail-query-failure"}).Error)

		queryFailure := errors.New("forced API request log detail query failure")
		const callbackName = "test:fail_api_request_log_detail_query"
		require.NoError(t, srv.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "api_request_logs" {
				tx.AddError(queryFailure)
			}
		}))
		t.Cleanup(func() { require.NoError(t, srv.DB.Callback().Query().Remove(callbackName)) })

		response := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/detail-query-failure", nil)
		require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	})
}

func newAPIRequestLogDetailServer(t *testing.T) (*master.Server, string) {
	t.Helper()
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)
	return srv, loginAsAdmin(t, srv, "admin", "admin123")
}

func createAPIRequestLogViewer(t *testing.T, srv *master.Server, username string, serviceID uint) string {
	t.Helper()
	user := models.User{Username: username, Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	if serviceID != 0 {
		role := models.Role{Key: username, Name: username, Status: consts.StatusEnabled}
		require.NoError(t, srv.DB.Create(&role).Error)
		permission := models.Permission{Resource: models.APIResourceService, ResourceID: serviceID, Action: models.APIPermissionInvoke}
		require.NoError(t, srv.DB.Create(&permission).Error)
		require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
		require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	}
	token, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	return token
}
