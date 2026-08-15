package apirbac

import (
	"context"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func TestTokenInvokeFinderFiltersByEffectiveRoleAndExactScope(t *testing.T) {
	db, query := setupAPIRBACTest(t)
	service, route := seedInvokeScope(t, db)

	serviceRole := seedInvokeRole(t, db, "service-invoker", models.APIResourceService, service.ID)
	routeRole := seedInvokeRole(t, db, "route-invoker", models.APIResourceRoute, route.ID)
	otherRouteRole := seedInvokeRole(t, db, "other-route", models.APIResourceRoute, route.ID+100)
	disabledRole := seedInvokeRole(t, db, "disabled-role", models.APIResourceRoute, route.ID+200)
	require.NoError(t, db.Model(&disabledRole).Update("status", consts.StatusDisabled).Error)
	adminRole := seedInvokeRole(t, db, GatewayAdminRoleKey, models.APIResourceService, 0)
	adminRole.BuiltIn = true
	require.NoError(t, db.Model(&adminRole).Update("built_in", true).Error)

	groupOwner := seedInvokeUser(t, db, "group-owner", consts.RoleUser, 7)
	userOwner := seedInvokeUser(t, db, "user-owner", consts.RoleUser, 8)
	adminOwner := seedInvokeUser(t, db, "admin-owner", consts.RoleAdmin, 9)
	defaultGroupOwner := seedInvokeUser(t, db, "default-group-owner", consts.RoleUser, 0)
	seedBinding(t, db, models.APIPrincipalUserGroup, groupOwner.GroupID, routeRole.ID)
	seedBinding(t, db, models.APIPrincipalUserGroup, models.DefaultUserGroupID, routeRole.ID)
	seedBinding(t, db, models.APIPrincipalUser, userOwner.ID, serviceRole.ID)

	explicitService := seedInvokeToken(t, db, "explicit-service", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	explicitRoute := seedInvokeToken(t, db, "explicit-route", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	inheritGroup := seedInvokeToken(t, db, "inherit-group", groupOwner.ID, models.APIRoleModeInherit, consts.StatusEnabled, -1)
	inheritUser := seedInvokeToken(t, db, "inherit-user", userOwner.ID, models.APIRoleModeInherit, consts.StatusEnabled, -1)
	admin := seedInvokeToken(t, db, "admin", adminOwner.ID, models.APIRoleModeInherit, consts.StatusEnabled, -1)
	inheritDefaultGroup := seedInvokeToken(t, db, "inherit-default-group", defaultGroupOwner.ID, models.APIRoleModeInherit, consts.StatusEnabled, -1)
	zeroPermission := seedInvokeToken(t, db, "zero-permission", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	disabledRoleToken := seedInvokeToken(t, db, "disabled-role", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	disabled := seedInvokeToken(t, db, "disabled", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusDisabled, -1)
	expired := seedInvokeToken(t, db, "expired", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, 1)
	otherRoute := seedInvokeToken(t, db, "other-route", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	missingRole := seedInvokeToken(t, db, "missing-role", groupOwner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)

	for _, binding := range []struct {
		tokenID uint
		roleID  uint
	}{
		{explicitService.ID, serviceRole.ID},
		{explicitRoute.ID, routeRole.ID},
		{disabled.ID, serviceRole.ID},
		{expired.ID, serviceRole.ID},
		{otherRoute.ID, otherRouteRole.ID},
		{missingRole.ID, 999_999},
		{disabledRoleToken.ID, disabledRole.ID},
	} {
		seedBinding(t, db, models.APIPrincipalToken, binding.tokenID, binding.roleID)
	}

	candidates := []models.Token{
		explicitService, explicitRoute, inheritGroup, inheritUser, admin, inheritDefaultGroup,
		zeroPermission, disabledRoleToken, disabled, expired, otherRoute, missingRole,
	}
	got, err := NewTokenInvokeFinder(query).Filter(context.Background(), candidates, service.ID, route.ID)
	require.NoError(t, err)
	require.Equal(t, []uint{
		explicitService.ID, explicitRoute.ID, inheritGroup.ID, inheritUser.ID, admin.ID, inheritDefaultGroup.ID,
	}, tokenIDs(got))
}

func TestTokenInvokeFinderFailsClosedForMissingPermission(t *testing.T) {
	db, query := setupAPIRBACTest(t)
	service, route := seedInvokeScope(t, db)
	role := models.Role{Key: "missing-permission", Name: "Missing permission", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: 999_999}).Error)
	owner := seedInvokeUser(t, db, "missing-permission-owner", consts.RoleUser, 7)
	token := seedInvokeToken(t, db, "missing-permission", owner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	seedBinding(t, db, models.APIPrincipalToken, token.ID, role.ID)

	_, err := NewTokenInvokeFinder(query).Filter(context.Background(), []models.Token{token}, service.ID, route.ID)
	require.Error(t, err)
}

func TestTokenInvokeFinderFailsClosedForRouteWildcardPermission(t *testing.T) {
	db, query := setupAPIRBACTest(t)
	service, route := seedInvokeScope(t, db)
	role := models.Role{Key: "route-wildcard", Name: "Route wildcard", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	permission := models.Permission{Resource: models.APIResourceRoute, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	owner := seedInvokeUser(t, db, "route-wildcard-owner", consts.RoleUser, 7)
	token := seedInvokeToken(t, db, "route-wildcard", owner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
	seedBinding(t, db, models.APIPrincipalToken, token.ID, role.ID)

	got, err := NewTokenInvokeFinder(query).Filter(context.Background(), []models.Token{token}, service.ID, route.ID)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTokenInvokeFinderRejectsMismatchedRouteAndInvalidRoleMode(t *testing.T) {
	db, query := setupAPIRBACTest(t)
	service, _ := seedInvokeScope(t, db)
	foreignService, foreignRoute := seedInvokeScope(t, db)
	owner := seedInvokeUser(t, db, "invalid-owner", consts.RoleUser, 10)
	token := seedInvokeToken(t, db, "invalid-mode", owner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)

	_, err := NewTokenInvokeFinder(query).Filter(context.Background(), []models.Token{token}, service.ID, foreignRoute.ID)
	require.Error(t, err)

	require.NoError(t, db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	token.APIRoleMode = models.APIRoleMode("invalid")
	_, err = NewTokenInvokeFinder(query).Filter(context.Background(), []models.Token{token}, foreignService.ID, foreignRoute.ID)
	require.Error(t, err)
}

func TestTokenInvokeFinderUsesConstantAuthorizationQueryCount(t *testing.T) {
	counts := make(map[int]int)
	for _, candidateCount := range []int{1, 100, 401} {
		t.Run(fmt.Sprintf("tokens_%d", candidateCount), func(t *testing.T) {
			db, query := setupAPIRBACTest(t)
			service, route := seedInvokeScope(t, db)
			role := seedInvokeRole(t, db, "constant-query", models.APIResourceRoute, route.ID)
			owner := seedInvokeUser(t, db, "constant-owner", consts.RoleUser, 11)
			candidates := make([]models.Token, 0, candidateCount)
			for i := 0; i < candidateCount; i++ {
				token := seedInvokeToken(t, db, fmt.Sprintf("constant-%03d", i), owner.ID, models.APIRoleModeExplicit, consts.StatusEnabled, -1)
				seedBinding(t, db, models.APIPrincipalToken, token.ID, role.ID)
				candidates = append(candidates, token)
			}

			queries := 0
			callbackName := fmt.Sprintf("test:count_token_invoke_queries_%d", candidateCount)
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) { queries++ }))
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

			got, err := NewTokenInvokeFinder(query).Filter(context.Background(), candidates, service.ID, route.ID)
			require.NoError(t, err)
			require.Len(t, got, candidateCount)
			counts[candidateCount] = queries
		})
	}
	require.Equal(t, counts[1], counts[100], "authorization reads must be batched instead of growing per Token")
	require.Equal(t, counts[100]+1, counts[401], "crossing one 400-ID chunk adds one query, not one query per Token")
}

func seedInvokeScope(t *testing.T, db *gorm.DB) (models.APIService, models.APIRoute) {
	t.Helper()
	service := models.APIService{Slug: fmt.Sprintf("service-%d", nextInvokeFixtureID(db)), Name: "Service", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	route := models.APIRoute{
		APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast",
		Protocols: datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP}, Status: consts.StatusEnabled,
	}
	require.NoError(t, db.Create(&route).Error)
	return service, route
}

func nextInvokeFixtureID(db *gorm.DB) int64 {
	var count int64
	_ = db.Model(&models.APIService{}).Count(&count).Error
	return count + 1
}

func seedInvokeRole(t *testing.T, db *gorm.DB, key string, resource models.APIResource, resourceID uint) models.Role {
	return seedTokenPermissionRole(t, db, key, resource, resourceID, models.APIPermissionInvoke)
}

func seedTokenPermissionRole(t *testing.T, db *gorm.DB, key string, resource models.APIResource, resourceID uint, action models.APIPermissionAction) models.Role {
	t.Helper()
	role := models.Role{Key: key, Name: key, Status: consts.StatusEnabled, BuiltIn: key == GatewayAdminRoleKey}
	require.NoError(t, db.Create(&role).Error)
	permission := models.Permission{Resource: resource, ResourceID: resourceID, Action: action}
	require.NoError(t, db.Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	return role
}

func seedInvokeUser(t *testing.T, db *gorm.DB, username string, role int, groupID uint) models.User {
	t.Helper()
	user := models.User{Username: username, Role: role, GroupID: groupID, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	return user
}

func seedInvokeToken(t *testing.T, db *gorm.DB, name string, userID uint, mode models.APIRoleMode, status int, expiredAt int64) models.Token {
	t.Helper()
	token := models.Token{UserID: userID, Key: "sk-" + name, Name: name, APIRoleMode: mode, Status: status, ExpiredAt: expiredAt}
	require.NoError(t, db.Create(&token).Error)
	if status == consts.StatusDisabled {
		require.NoError(t, db.Model(&token).Update("status", status).Error)
	}
	return token
}

func tokenIDs(tokens []models.Token) []uint {
	ids := make([]uint, 0, len(tokens))
	for _, token := range tokens {
		ids = append(ids, token.ID)
	}
	return ids
}
