package api_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

// behavior change: invoke permissions authorize relay traffic, not access to
// the Generic API management UI; personal logs remain available independently.
func TestGenericAPIControlPlaneCapabilityIgnoresInvokeRoles(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	user := models.User{Username: "invoke-only-capability", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "invoke-only-capability", Name: "Invoke only", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	var permission models.Permission
	require.NoError(t, srv.DB.Where("resource = ? AND resource_id = 0 AND action = ?", models.APIResourceService, models.APIPermissionInvoke).First(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	userToken, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)

	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{name: "invoke role remains hidden", token: userToken},
		{name: "admin sees control plane", token: loginAsAdmin(t, srv, "admin", "admin123"), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := reqHelper(srv, tc.token, http.MethodGet, "/api/capabilities", nil)
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
			require.Equal(t, tc.want, body.GenericAPI.Services)
			require.Equal(t, tc.want, body.GenericAPI.Access)
			require.True(t, body.GenericAPI.Logs)
			require.Equal(t, tc.want, body.GenericAPI.WebSocket)
		})
	}
}
