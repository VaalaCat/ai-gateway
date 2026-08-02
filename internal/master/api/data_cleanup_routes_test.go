package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestSystemCleanupRoutes(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminToken := loginHelper(t, srv, "admin", "admin123")
	userToken, err := middleware.GenerateToken(
		srv.Cfg.Master.JWTSecret, 42, consts.RoleUser, "user", "", "",
	)
	require.NoError(t, err)

	t.Run("preview requires table", func(t *testing.T) {
		response := reqHelper(srv, adminToken, http.MethodGet,
			"/api/admin/system/cleanup/preview?database=core&cutoff_date=2026-07-20", nil)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	})

	t.Run("preview rejects business table", func(t *testing.T) {
		response := reqHelper(srv, adminToken, http.MethodGet,
			"/api/admin/system/cleanup/preview?database=core&table=users&cutoff_date=2026-07-20", nil)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Equal(t, "CleanupTableNotAllowed", jsonBody(t, response)["code"])
	})

	t.Run("preview rejects retired billing hourly table", func(t *testing.T) {
		response := reqHelper(srv, adminToken, http.MethodGet,
			"/api/admin/system/cleanup/preview?database=core&table=billing_hourly_buckets&cutoff_date=2026-07-20", nil)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		require.Equal(t, "CleanupTableNotAllowed", jsonBody(t, response)["code"])
	})

	t.Run("old cleanup endpoint is gone", func(t *testing.T) {
		response := reqHelper(srv, adminToken, http.MethodPost, "/api/admin/system/cleanup", map[string]any{})
		require.Equal(t, http.StatusNotFound, response.Code, response.Body.String())
	})

	t.Run("batch deletes preview snapshot", func(t *testing.T) {
		require.NoError(t, srv.App.GetCoreDB().Create(&models.BillingLog{
			RequestID: "cleanup-route-test", CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix(),
		}).Error)
		previewResponse := reqHelper(srv, adminToken, http.MethodGet,
			"/api/admin/system/cleanup/preview?database=core&table=billing_logs&cutoff_date=2026-07-20", nil)
		require.Equal(t, http.StatusOK, previewResponse.Code, previewResponse.Body.String())
		preview := jsonBody(t, previewResponse)
		snapshot, ok := preview["snapshot_max_key"].(string)
		require.True(t, ok)
		require.NotEmpty(t, snapshot)

		batchResponse := reqHelper(srv, adminToken, http.MethodPost, "/api/admin/system/cleanup/batch", map[string]any{
			"database": "core", "table": "billing_logs",
			"cutoff_date": "2026-07-20", "snapshot_max_key": snapshot,
		})
		require.Equal(t, http.StatusOK, batchResponse.Code, batchResponse.Body.String())
		batch := jsonBody(t, batchResponse)
		require.Equal(t, float64(1), batch["deleted"])
		require.Equal(t, false, batch["has_more"])
	})

	protected := &models.BillingLog{RequestID: "cleanup-auth-test", CreatedAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()}
	require.NoError(t, srv.App.GetCoreDB().Create(protected).Error)
	authEndpoints := []struct {
		name, method, path string
		body               any
	}{
		{
			name: "preview", method: http.MethodGet,
			path: "/api/admin/system/cleanup/preview?database=core&table=billing_logs&cutoff_date=2026-07-20",
		},
		{
			name: "batch", method: http.MethodPost, path: "/api/admin/system/cleanup/batch",
			body: map[string]any{
				"database": "core", "table": "billing_logs",
				"cutoff_date": "2026-07-20", "snapshot_max_key": fmt.Sprintf("%d", protected.ID),
			},
		},
	}
	authCases := []struct {
		name, token string
		want        int
	}{
		{name: "unauthenticated", want: http.StatusUnauthorized},
		{name: "non-admin", token: userToken, want: http.StatusForbidden},
	}
	for _, endpoint := range authEndpoints {
		for _, authCase := range authCases {
			t.Run(fmt.Sprintf("%s is blocked for %s", endpoint.name, authCase.name), func(t *testing.T) {
				response := reqHelper(srv, authCase.token, endpoint.method, endpoint.path, endpoint.body)
				require.Equal(t, authCase.want, response.Code, response.Body.String())
			})
		}
	}
	var protectedCount int64
	require.NoError(t, srv.App.GetCoreDB().Model(&models.BillingLog{}).
		Where("id = ?", protected.ID).Count(&protectedCount).Error)
	require.Equal(t, int64(1), protectedCount)
}
