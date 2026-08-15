package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	apiaccessgrant "github.com/VaalaCat/ai-gateway/internal/master/api/api_access_grant"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Break caught: registering these handlers outside Server.setupRoutes or on a
// non-admin group would respectively return 404 or expose grant projections to
// ordinary users. The same fixture proves distinct pagination at the real URL.
func TestAPIAccessGrantRoutesAreAdminOnlyAndPaginateDistinctKeys(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	ordinary := models.User{Username: "grant-route-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID}
	require.NoError(t, srv.DB.Create(&ordinary).Error)
	ordinaryJWT, err := middleware.GenerateToken(srv.Cfg.Master.JWTSecret, ordinary.ID, ordinary.Role, ordinary.Username, "", "")
	require.NoError(t, err)
	serviceA := models.APIService{Slug: "route-weather", Name: "Route Weather", Status: consts.StatusEnabled}
	serviceB := models.APIService{Slug: "route-maps", Name: "Route Maps", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&serviceA).Error)
	require.NoError(t, srv.DB.Create(&serviceB).Error)
	users := make([]models.User, 3)
	for index := range users {
		users[index] = models.User{
			Username: fmt.Sprintf("grant-page-%d", index+1), Role: consts.RoleUser,
			Status: consts.StatusEnabled, GroupID: models.DefaultUserGroupID,
		}
		require.NoError(t, srv.DB.Create(&users[index]).Error)
	}
	seedServerAccessRole(t, srv.DB, "grant-page-1-a", users[0].ID, serviceA.ID)
	seedServerAccessRole(t, srv.DB, "grant-page-1-b", users[0].ID, serviceA.ID)
	seedServerAccessRole(t, srv.DB, "grant-page-2", users[1].ID, serviceA.ID)
	seedServerAccessRole(t, srv.DB, "grant-page-3", users[2].ID, serviceB.ID)

	effectivePath := fmt.Sprintf(
		"/api/admin/api-access-grants/effective?principal_type=user&principal_id=%d&api_service_id=%d",
		users[0].ID, serviceA.ID,
	)
	for _, path := range []string{"/api/admin/api-access-grants", effectivePath} {
		unauthenticated := reqHelper(srv, "", http.MethodGet, path, nil)
		require.Equal(t, http.StatusUnauthorized, unauthenticated.Code, unauthenticated.Body.String())
		nonAdmin := reqHelper(srv, ordinaryJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusForbidden, nonAdmin.Code, nonAdmin.Body.String())
		admin := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, admin.Code, admin.Body.String())
	}

	page1 := requestAccessGrantPage(t, srv, adminJWT, "/api/admin/api-access-grants?page=1&page_size=2")
	require.EqualValues(t, 3, page1.Total)
	require.Equal(t, []uint{users[0].ID, users[1].ID}, accessGrantPrincipalIDs(page1.Data))
	page2 := requestAccessGrantPage(t, srv, adminJWT, "/api/admin/api-access-grants?page=2&page_size=2")
	require.EqualValues(t, 3, page2.Total)
	require.Equal(t, []uint{users[2].ID}, accessGrantPrincipalIDs(page2.Data))
	page3 := requestAccessGrantPage(t, srv, adminJWT, "/api/admin/api-access-grants?page=3&page_size=2")
	require.EqualValues(t, 3, page3.Total)
	require.Empty(t, page3.Data)
	missing := requestAccessGrantPage(t, srv, adminJWT, "/api/admin/api-access-grants?search=no-such-principal-or-service")
	require.Zero(t, missing.Total)
	require.Empty(t, missing.Data)
}

func seedServerAccessRole(t *testing.T, db *gorm.DB, key string, principalID, serviceID uint) {
	t.Helper()
	role := models.Role{Key: key, Name: key, Kind: models.APIRoleKindCustom, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: serviceID, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Where("resource = ? AND resource_id = ? AND action = ?", permission.Resource, permission.ResourceID, permission.Action).
		FirstOrCreate(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	require.NoError(t, db.Create(&models.RoleBinding{
		PrincipalType: models.APIPrincipalUser, PrincipalID: principalID, RoleID: role.ID,
	}).Error)
}

func requestAccessGrantPage(
	t *testing.T,
	srv *master.Server,
	jwt, path string,
) masterapi.PaginatedResponse[apiaccessgrant.AccessGrantResponse] {
	t.Helper()
	response := reqHelper(srv, jwt, http.MethodGet, path, nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	var page masterapi.PaginatedResponse[apiaccessgrant.AccessGrantResponse]
	require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
	return page
}

func accessGrantPrincipalIDs(rows []apiaccessgrant.AccessGrantResponse) []uint {
	ids := make([]uint, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.PrincipalID)
	}
	return ids
}
