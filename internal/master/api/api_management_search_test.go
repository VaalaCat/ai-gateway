package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

// Break caught: ignoring the management-list filters, counting before they
// apply, or paginating before a service scope is applied exposes unrelated
// entities and gives picker UIs misleading totals.
func TestAPIManagementSearch(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

	weather := models.APIService{Slug: "weather", Name: "Weather", Status: consts.StatusEnabled}
	calendar := models.APIService{Slug: "calendar", Name: "Calendar", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&weather).Error)
	require.NoError(t, srv.DB.Create(&calendar).Error)
	weatherBackend := models.APIBackend{APIServiceID: weather.ID, Name: "weather-primary"}
	calendarBackend := models.APIBackend{APIServiceID: calendar.ID, Name: "calendar-primary"}
	require.NoError(t, srv.DB.Create(&weatherBackend).Error)
	require.NoError(t, srv.DB.Create(&calendarBackend).Error)

	routes := []models.APIRoute{
		{APIServiceID: weather.ID, BackendID: weatherBackend.ID, Slug: "forecast", Status: consts.StatusEnabled},
		{APIServiceID: weather.ID, BackendID: weatherBackend.ID, Slug: "forecast-v2", Status: consts.StatusEnabled},
		{APIServiceID: calendar.ID, BackendID: calendarBackend.ID, Slug: "forecast-private", Status: consts.StatusEnabled},
	}
	for i := range routes {
		require.NoError(t, srv.DB.Create(&routes[i]).Error)
	}
	upstreams := []models.APIUpstream{
		{BackendID: weatherBackend.ID, Name: "primary", BaseURL: "https://weather.example/v1", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
		{BackendID: weatherBackend.ID, Name: "secondary", BaseURL: "https://secondary.example/v1", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
		{BackendID: weatherBackend.ID, Name: "weather-name-only", BaseURL: "https://name-only.example/v1", Weight: 1, Priority: -1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
		{BackendID: calendarBackend.ID, Name: "outside", BaseURL: "https://weather.example/calendar", Weight: 1, AuthType: models.APIUpstreamAuthNone, Status: consts.StatusEnabled},
	}
	for i := range upstreams {
		require.NoError(t, srv.DB.Create(&upstreams[i]).Error)
	}
	roles := []models.Role{
		{Key: "auditor", Name: "API Auditor", Status: consts.StatusEnabled},
		{Key: "auditor-disabled", Name: "Disabled Auditor", Status: consts.StatusDisabled},
		{Key: "operator", Name: "API Operator", Status: consts.StatusEnabled},
		{Key: "built-in-reader", Name: "Built-in reader", BuiltIn: true, Status: consts.StatusEnabled},
		{Key: "unrelated-name-key", Name: "Name-only match", Status: consts.StatusEnabled},
		{Key: "key-only-match", Name: "Unrelated key label", Status: consts.StatusEnabled},
	}
	for i := range roles {
		require.NoError(t, srv.DB.Create(&roles[i]).Error)
	}
	require.NoError(t, srv.DB.Model(&models.Role{}).Where("id = ?", roles[1].ID).UpdateColumn("status", consts.StatusDisabled).Error)
	bindingFixtures := []models.RoleBinding{
		{PrincipalType: models.APIPrincipalToken, PrincipalID: 7, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: 8, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: roles[2].ID},
	}
	for i := range bindingFixtures {
		require.NoError(t, srv.DB.Create(&bindingFixtures[i]).Error)
	}

	tests := []struct {
		name      string
		path      string
		field     string
		wantNames []string
		wantTotal int64
	}{
		{"route slug", fmt.Sprintf("/api/admin/api-routes?api_service_id=%d&search=forecast", weather.ID), "slug", []string{"forecast-v2", "forecast"}, 2},
		{"upstream name or URL", fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d&search=weather.example", weatherBackend.ID), "name", []string{"primary"}, 1},
		{"upstream name only", fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d&search=weather-name-only", weatherBackend.ID), "name", []string{"weather-name-only"}, 1},
		{"backend name or endpoint", fmt.Sprintf("/api/admin/api-backends?api_service_id=%d&search=weather.example", weather.ID), "name", []string{"weather-primary"}, 1},
		{"role name or key", "/api/admin/api-roles?search=auditor&status=1", "name", []string{"API Auditor"}, 1},
		{"role name only", "/api/admin/api-roles?search=Name-only%20match", "name", []string{"Name-only match"}, 1},
		{"role key only", "/api/admin/api-roles?search=key-only-match", "name", []string{"Unrelated key label"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response := reqHelper(srv, adminJWT, http.MethodGet, tt.path, nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var body struct {
				Data []struct {
					Name string `json:"name"`
					Slug string `json:"slug"`
				} `json:"data"`
				Total int64 `json:"total"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
			names := make([]string, 0, len(body.Data))
			for _, item := range body.Data {
				if tt.field == "slug" {
					names = append(names, item.Slug)
				} else {
					names = append(names, item.Name)
				}
			}
			require.Equal(t, tt.wantNames, names)
			require.Equal(t, tt.wantTotal, body.Total)
		})
	}

	t.Run("empty search returns complete service scope", func(t *testing.T) {
		response := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-routes?api_service_id=%d&search=", weather.ID), nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []models.APIRoute `json:"data"`
			Total int64             `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, int64(2), body.Total)
		require.Equal(t, []string{"forecast-v2", "forecast"}, []string{body.Data[0].Slug, body.Data[1].Slug})
	})

	t.Run("search total is filtered before page", func(t *testing.T) {
		response := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-routes?api_service_id=%d&search=forecast&page=2&page_size=1", weather.ID), nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []models.APIRoute `json:"data"`
			Total int64             `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, int64(2), body.Total)
		require.Len(t, body.Data, 1)
		require.Equal(t, routes[0].ID, body.Data[0].ID)
		require.Equal(t, routes[0].Slug, body.Data[0].Slug)
	})

	t.Run("upstream tie uses id descending", func(t *testing.T) {
		response := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-upstreams?backend_id=%d", weatherBackend.ID), nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data []models.APIUpstream `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, []string{"secondary", "primary", "weather-name-only"}, []string{body.Data[0].Name, body.Data[1].Name, body.Data[2].Name})
	})

	t.Run("assignable excludes disabled and built-in roles", func(t *testing.T) {
		var gatewayAdmin models.Role
		require.NoError(t, srv.DB.Where("key = ?", models.GatewayAdminRoleKey).First(&gatewayAdmin).Error)
		require.NoError(t, srv.DB.Model(&gatewayAdmin).Updates(map[string]any{
			"built_in": false, "status": consts.StatusEnabled,
		}).Error)
		response := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/api-roles?assignable=true", nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data []models.Role `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		names := make([]string, 0, len(body.Data))
		for _, role := range body.Data {
			names = append(names, role.Name)
		}
		require.Equal(t, []string{"Unrelated key label", "Name-only match", "API Operator", "API Auditor"}, names)
		require.NotContains(t, names, gatewayAdmin.Name)
	})
}

// Break caught: treating a binding tuple as optional query decoration makes
// principal pickers select the wrong rows, while accepting malformed filters
// silently turns a narrow request into a global administrative list.
func TestAPIManagementBindingFilters(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	roles := []models.Role{
		{Key: "binding-reader", Name: "Binding reader", Status: consts.StatusEnabled},
		{Key: "binding-writer", Name: "Binding writer", Status: consts.StatusEnabled},
	}
	for i := range roles {
		require.NoError(t, srv.DB.Create(&roles[i]).Error)
	}
	bindingFixtures := []models.RoleBinding{
		{PrincipalType: models.APIPrincipalToken, PrincipalID: 7, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: 7, RoleID: roles[1].ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: 7, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: 8, RoleID: roles[0].ID},
	}
	for i := range bindingFixtures {
		require.NoError(t, srv.DB.Create(&bindingFixtures[i]).Error)
	}

	listBindings := func(t *testing.T, path string) []models.RoleBinding {
		t.Helper()
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []models.RoleBinding `json:"data"`
			Total int64                `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		require.Equal(t, int64(len(body.Data)), body.Total)
		return body.Data
	}

	for _, tt := range []struct {
		name      string
		path      string
		wantTotal int
	}{
		{"principal type", "/api/admin/api-role-bindings?principal_type=token", 3},
		{"principal id", "/api/admin/api-role-bindings?principal_id=7", 3},
		{"role id", fmt.Sprintf("/api/admin/api-role-bindings?role_id=%d", roles[0].ID), 3},
		{"type and role without principal id", fmt.Sprintf("/api/admin/api-role-bindings?principal_type=token&role_id=%d", roles[0].ID), 2},
		{"exact tuple", fmt.Sprintf("/api/admin/api-role-bindings?principal_type=token&principal_id=7&role_id=%d", roles[0].ID), 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := listBindings(t, tt.path)
			require.Len(t, got, tt.wantTotal)
			if tt.name == "exact tuple" {
				require.Equal(t, []models.RoleBinding{bindingFixtures[0]}, got)
			}
		})
	}

	for _, path := range []string{
		"/api/admin/api-role-bindings?principal_type=unknown",
		"/api/admin/api-role-bindings?principal_id=0",
		"/api/admin/api-role-bindings?role_id=0",
	} {
		t.Run(path, func(t *testing.T) {
			response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
			require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		})
	}
}

// Break caught: role detail reads that return only the role omit the assigned
// permission set needed by an edit form, while a missing route hides 404s
// behind an unrelated fallback handler.
func TestAPIRoleGet(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	role := models.Role{Key: "detail-reader", Name: "Detail reader", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: 77, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	require.NoError(t, srv.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)

	response := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var body struct {
		ID          uint                `json:"id"`
		Permissions []models.Permission `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
	require.Equal(t, role.ID, body.ID)
	require.Equal(t, []models.Permission{permission}, body.Permissions)

	unauthenticated := reqHelper(srv, "", http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
	user := models.User{Username: "role-detail-reader", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	userJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, user.ID, user.Role, user.Username, "", "")
	require.NoError(t, err)
	forbidden := reqHelper(srv, userJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusForbidden, forbidden.Code, forbidden.Body.String())

	missing := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/api-roles/999999", nil)
	require.Equal(t, http.StatusNotFound, missing.Code, missing.Body.String())
}
