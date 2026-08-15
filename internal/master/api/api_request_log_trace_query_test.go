package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestAPIRequestTraceQueryTransportPreservesOpaqueRequestID(t *testing.T) {
	srv, adminToken := newAPIRequestLogDetailServer(t)
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestTrace{}))
	service := models.APIService{Slug: "trace-query", Name: "Trace query", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)

	requestIDs := []string{
		"ordinary",
		"trace",
		"\u00a0edge\u00a0",
		"question?mark",
		"hash#mark",
		"slash/mark",
		"percent%mark",
	}
	for _, requestID := range requestIDs {
		require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: requestID, APIServiceID: service.ID}).Error)
		require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: requestID}).Error)
	}

	for _, requestID := range requestIDs {
		t.Run(requestID, func(t *testing.T) {
			response := reqHelper(srv, adminToken, http.MethodGet, traceQueryPath(requestID), nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var trace models.APIRequestTrace
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &trace))
			require.Equal(t, requestID, trace.RequestID)
		})
	}

	legacy := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/ordinary/trace", nil)
	require.Equal(t, http.StatusOK, legacy.Code, legacy.Body.String())

	detail := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/trace", nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var detailLog models.APIRequestLog
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &detailLog))
	require.Equal(t, "trace", detailLog.RequestID)

	legacyCollision := reqHelper(srv, adminToken, http.MethodGet, "/api/admin/api-request-logs/trace/trace", nil)
	require.Equal(t, http.StatusOK, legacyCollision.Code, legacyCollision.Body.String())
}

func TestAPIRequestTraceQueryTransportValidatesAndPreservesReadSemantics(t *testing.T) {
	t.Run("missing and empty request IDs are bad requests", func(t *testing.T) {
		srv, adminToken := newAPIRequestLogDetailServer(t)
		for _, path := range []string{
			"/api/admin/api-request-traces",
			"/api/admin/api-request-traces?request_id=",
		} {
			response := reqHelper(srv, adminToken, http.MethodGet, path, nil)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		}
	})

	t.Run("invoke role is forbidden while admin keeps not found semantics", func(t *testing.T) {
		srv, adminToken := newAPIRequestLogDetailServer(t)
		require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestTrace{}))
		allowedService := models.APIService{Slug: "trace-allowed", Name: "Trace allowed", Status: consts.StatusEnabled}
		deniedService := models.APIService{Slug: "trace-denied", Name: "Trace denied", Status: consts.StatusEnabled}
		require.NoError(t, srv.DB.Create(&allowedService).Error)
		require.NoError(t, srv.DB.Create(&deniedService).Error)
		for requestID, serviceID := range map[string]uint{
			"allowed": allowedService.ID,
			"denied":  deniedService.ID,
		} {
			require.NoError(t, srv.DB.Create(&models.APIRequestLog{RequestID: requestID, APIServiceID: serviceID}).Error)
			require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: requestID}).Error)
		}
		readerToken := createAPIRequestLogViewer(t, srv, "trace-query-reader", allowedService.ID)

		require.Equal(t, http.StatusForbidden, reqHelper(srv, readerToken, http.MethodGet, traceQueryPath("allowed"), nil).Code)
		require.Equal(t, http.StatusForbidden, reqHelper(srv, readerToken, http.MethodGet, traceQueryPath("denied"), nil).Code)
		require.Equal(t, http.StatusOK, reqHelper(srv, adminToken, http.MethodGet, traceQueryPath("allowed"), nil).Code)
		require.Equal(t, http.StatusNotFound, reqHelper(srv, adminToken, http.MethodGet, traceQueryPath("missing"), nil).Code)
	})

	t.Run("unavailable log database remains service unavailable", func(t *testing.T) {
		srv := setupTestMaster(t)
		require.NoError(t, srv.InitAdminUser("admin", "admin123"))
		srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
		srv.App.SetLogDB(nil)

		response := reqHelper(
			srv,
			loginAsAdmin(t, srv, "admin", "admin123"),
			http.MethodGet,
			traceQueryPath("unavailable"),
			nil,
		)
		require.Equal(t, http.StatusServiceUnavailable, response.Code, response.Body.String())
		require.Contains(t, response.Body.String(), "LogDatabaseUnavailable")
	})
}

func traceQueryPath(requestID string) string {
	return "/api/admin/api-request-traces?" + url.Values{"request_id": {requestID}}.Encode()
}
