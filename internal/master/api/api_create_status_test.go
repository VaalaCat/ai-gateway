package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

type createStatusCase struct {
	name          string
	includeStatus bool
	status        int
	wantHTTP      int
	wantStatus    int
}

var createStatusCases = []createStatusCase{
	{name: "omitted defaults enabled", wantHTTP: http.StatusCreated, wantStatus: consts.StatusEnabled},
	{name: "explicit disabled", includeStatus: true, status: consts.StatusDisabled, wantHTTP: http.StatusCreated, wantStatus: consts.StatusDisabled},
	{name: "explicit enabled boundary", includeStatus: true, status: consts.StatusEnabled, wantHTTP: http.StatusCreated, wantStatus: consts.StatusEnabled},
	{name: "invalid above boundary", includeStatus: true, status: 2, wantHTTP: http.StatusBadRequest},
}

func withStatus(body map[string]any, tc createStatusCase) map[string]any {
	if tc.includeStatus {
		body["status"] = tc.status
	}
	return body
}

func TestGenericAPICreateStatusOmittedAndExplicitDisabled(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	jwt := loginAsAdmin(t, srv, "admin", "admin123")
	parent := models.APIService{Slug: "status-parent", Name: "Status parent", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&parent).Error)
	backend := models.APIBackend{APIServiceID: parent.ID, Name: "status-backend"}
	require.NoError(t, srv.DB.Create(&backend).Error)

	t.Run("service", func(t *testing.T) {
		for index, tc := range createStatusCases {
			t.Run(tc.name, func(t *testing.T) {
				response := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-services", withStatus(map[string]any{
					"slug": fmt.Sprintf("status-service-%d", index), "name": "Status service",
				}, tc))
				require.Equal(t, tc.wantHTTP, response.Code, response.Body.String())
				if tc.wantHTTP != http.StatusCreated {
					return
				}
				var created models.APIService
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
				require.Equal(t, tc.wantStatus, created.Status)
				require.NoError(t, srv.DB.First(&created, created.ID).Error)
				require.Equal(t, tc.wantStatus, created.Status)
			})
		}
	})

	t.Run("route", func(t *testing.T) {
		for index, tc := range createStatusCases {
			t.Run(tc.name, func(t *testing.T) {
				response := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-routes", withStatus(map[string]any{
					"api_service_id": parent.ID, "slug": fmt.Sprintf("status-route-%d", index),
					"target": map[string]any{"mode": "existing", "backend_id": backend.ID},
				}, tc))
				require.Equal(t, tc.wantHTTP, response.Code, response.Body.String())
				if tc.wantHTTP != http.StatusCreated {
					return
				}
				var created models.APIRoute
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
				require.Equal(t, tc.wantStatus, created.Status)
				require.NoError(t, srv.DB.First(&created, created.ID).Error)
				require.Equal(t, tc.wantStatus, created.Status)
			})
		}
	})

	t.Run("upstream", func(t *testing.T) {
		for index, tc := range createStatusCases {
			t.Run(tc.name, func(t *testing.T) {
				response := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-upstreams", withStatus(map[string]any{
					"backend_id": backend.ID, "name": fmt.Sprintf("status-upstream-%d", index), "base_url": "https://upstream.example",
				}, tc))
				require.Equal(t, tc.wantHTTP, response.Code, response.Body.String())
				if tc.wantHTTP != http.StatusCreated {
					return
				}
				var created models.APIUpstream
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
				require.Equal(t, tc.wantStatus, created.Status)
				require.NoError(t, srv.DB.First(&created, created.ID).Error)
				require.Equal(t, tc.wantStatus, created.Status)
			})
		}
	})

	t.Run("role", func(t *testing.T) {
		for index, tc := range createStatusCases {
			t.Run(tc.name, func(t *testing.T) {
				response := reqHelper(srv, jwt, http.MethodPost, "/api/admin/api-roles", withStatus(map[string]any{
					"key": fmt.Sprintf("status-role-%d", index), "name": "Status role",
				}, tc))
				require.Equal(t, tc.wantHTTP, response.Code, response.Body.String())
				if tc.wantHTTP != http.StatusCreated {
					return
				}
				var created models.Role
				require.NoError(t, json.Unmarshal(response.Body.Bytes(), &created))
				require.Equal(t, tc.wantStatus, created.Status)
				require.NoError(t, srv.DB.First(&created, created.ID).Error)
				require.Equal(t, tc.wantStatus, created.Status)
			})
		}
	})
}
