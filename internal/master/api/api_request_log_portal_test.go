package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
)

func TestAPIRequestLogPortalScopesAndProjectsCurrentUserData(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	require.NoError(t, srv.DB.AutoMigrate(&models.APIRequestLog{}, &models.APIRequestTrace{}))
	srv.App.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)
	srv.App.SetLogDB(srv.DB)

	viewer := models.User{Username: "portal-log-viewer", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	other := models.User{Username: "portal-log-other", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&viewer).Error)
	require.NoError(t, srv.DB.Create(&other).Error)
	viewerJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, viewer.ID, viewer.Role, viewer.Username, "", "")
	require.NoError(t, err)

	mine := models.APIRequestLog{
		RequestID: "portal-mine", UserID: viewer.ID, TokenID: 31, TokenName: "my-token",
		APIServiceID: 7, APIServiceName: "Weather", APIRouteID: 8, APIRouteName: "Forecast",
		APIUpstreamID: 9, APIUpstreamName: "secret-upstream", SourceAgentID: "source-agent",
		ExecutionAgentID: "execution-agent", AgentRouteID: 10, AgentRoutePath: "/internal/route",
		Protocol: models.APIProtocolHTTP, Method: http.MethodGet, StatusCode: http.StatusOK,
		DurationMs: 15, FirstByteMs: 4, RequestBytes: 12, ResponseBytes: 34, UnitPrice: 2, TotalCost: 2,
	}
	theirs := models.APIRequestLog{RequestID: "portal-theirs", UserID: other.ID, APIUpstreamName: "other-secret"}
	require.NoError(t, srv.DB.Create(&mine).Error)
	require.NoError(t, srv.DB.Create(&theirs).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestTrace{
		RequestID:            mine.RequestID,
		SourceRequestHeaders: datatypes.NewJSONType(map[string][]string{"Authorization": {"[REDACTED]"}}),
	}).Error)
	require.NoError(t, srv.DB.Create(&models.APIRequestTrace{RequestID: theirs.RequestID}).Error)

	t.Run("list is forced to the authenticated user and omits internal fields", func(t *testing.T) {
		response := reqHelper(srv, viewerJWT, http.MethodGet, "/api/api-request-logs?api_upstream_id=9&page=1&page_size=20", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []map[string]any `json:"data"`
			Total int64            `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, int64(1), body.Total)
		require.Len(t, body.Data, 1)
		require.Equal(t, mine.RequestID, body.Data[0]["request_id"])
		for _, key := range []string{
			"user_id", "client_ip", "api_upstream_id", "api_upstream_name", "source_agent_id",
			"execution_agent_id", "agent_route_id", "agent_route_path", "provider_dispatch_known",
			"provider_dispatched", "service_missing_at_settlement", "rate_limit_reason", "rate_limit_hits",
		} {
			require.NotContains(t, body.Data[0], key)
		}
	})

	t.Run("trace returns the persisted redacted capture only for its owner", func(t *testing.T) {
		own := reqHelper(srv, viewerJWT, http.MethodGet, "/api/api-request-traces?request_id=portal-mine", nil)
		require.Equal(t, http.StatusOK, own.Code, own.Body.String())
		require.Contains(t, own.Body.String(), "[REDACTED]")

		foreign := reqHelper(srv, viewerJWT, http.MethodGet, "/api/api-request-traces?request_id=portal-theirs", nil)
		require.Equal(t, http.StatusNotFound, foreign.Code, foreign.Body.String())
	})

	t.Run("admin control plane remains complete and admin-only", func(t *testing.T) {
		forbidden := reqHelper(srv, viewerJWT, http.MethodGet, "/api/admin/api-request-logs", nil)
		require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())

		admin := reqHelper(srv, loginAsAdmin(t, srv, "admin", "admin123"), http.MethodGet, "/api/admin/api-request-logs/portal-mine", nil)
		require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
		require.Contains(t, admin.Body.String(), "secret-upstream")
		require.Contains(t, admin.Body.String(), "execution-agent")
	})
}
