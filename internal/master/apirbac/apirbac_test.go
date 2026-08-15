package apirbac

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type testApplication struct{ db *gorm.DB }

func (a testApplication) GetCoreDB() *gorm.DB { return a.db }
func (a testApplication) GetLogDB() *gorm.DB  { return nil }
func (a testApplication) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutLegacySingle
}

func setupAPIRBACTest(t *testing.T) (*gorm.DB, dao.AdminQuery) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(
		&models.User{}, &models.Token{}, &models.APIService{}, &models.APIBackend{},
		&models.APIRoute{}, &models.Role{}, &models.Permission{},
		&models.RolePermission{}, &models.RoleBinding{},
	))
	q := dao.NewAdminQuery(dao.NewContext(testApplication{db: db}))
	return db, q
}

func TestCompileAPIRolesPreservesInvokeOnlyPermissions(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	role := models.Role{Key: "service_manager", Name: "Service manager", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	permissions := []models.Permission{
		{Resource: models.APIResourceService, ResourceID: 9, Action: models.APIPermissionInvoke},
		{Resource: models.APIResourceRoute, ResourceID: 3, Action: models.APIPermissionInvoke},
	}
	for i := range permissions {
		require.NoError(t, db.Create(&permissions[i]).Error)
		require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permissions[i].ID}).Error)
	}
	disabled := models.Role{Key: "disabled", Name: "Disabled", Status: consts.StatusDisabled}
	require.NoError(t, db.Create(&disabled).Error)
	require.NoError(t, db.Model(&models.Role{}).Where("id = ?", disabled.ID).Update("status", consts.StatusDisabled).Error)

	got, err := NewRoleCompiler(q.APIRBAC()).CompileAPIRoles(context.Background())
	require.NoError(t, err)
	require.Equal(t, []protocol.SyncedAPIRole{{
		ID: role.ID, Name: role.Name,
		Permissions: []protocol.APIPermissionGrant{
			{Resource: "api_route", ResourceID: 3, Action: "invoke"},
			{Resource: "api_service", ResourceID: 9, Action: "invoke"},
		},
	}}, got)
}

func TestCompileAPIRolesRejectsDanglingPermission(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	role := models.Role{Key: "dangling", Name: "Dangling", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: 999}).Error)

	_, err := NewRoleCompiler(q.APIRBAC()).CompileAPIRoles(context.Background())
	require.Error(t, err)
}

func TestCompileAPIRolesRejectsRouteWildcardPermission(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	role := models.Role{Key: "route-wildcard", Name: "Route wildcard", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	permission := models.Permission{Resource: models.APIResourceRoute, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)

	_, err := NewRoleCompiler(q.APIRBAC()).CompileAPIRoles(context.Background())
	require.Error(t, err)
}

func TestCompileAPIRolesUsesConstantQueryCount(t *testing.T) {
	for _, roleCount := range []int{1, 8} {
		t.Run(fmt.Sprintf("roles_%d", roleCount), func(t *testing.T) {
			db, q := setupAPIRBACTest(t)
			for i := 1; i <= roleCount; i++ {
				role := models.Role{Key: fmt.Sprintf("role-%d", i), Name: fmt.Sprintf("Role %d", i), Status: consts.StatusEnabled}
				require.NoError(t, db.Create(&role).Error)
				permission := models.Permission{Resource: models.APIResourceService, ResourceID: uint(i), Action: models.APIPermissionInvoke}
				require.NoError(t, db.Create(&permission).Error)
				require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
			}
			queries := 0
			callbackName := fmt.Sprintf("test:count_api_role_compile_queries_%d", roleCount)
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) { queries++ }))
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

			_, err := NewRoleCompiler(q.APIRBAC()).CompileAPIRoles(context.Background())
			require.NoError(t, err)
			require.Equal(t, 3, queries)
		})
	}
}

func TestFindUserAPIRoleSetMergesDirectGroupAndAdminRole(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	roles := seedRoles(t, db, "direct", "group", GatewayAdminRoleKey)
	user := models.User{Username: "admin", Role: consts.RoleAdmin, GroupID: 7}
	require.NoError(t, db.Create(&user).Error)
	seedBinding(t, db, models.APIPrincipalUser, user.ID, roles[0].ID)
	seedBinding(t, db, models.APIPrincipalUserGroup, user.GroupID, roles[0].ID)
	seedBinding(t, db, models.APIPrincipalUserGroup, user.GroupID, roles[1].ID)

	got, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.True(t, got.Exists)
	require.Equal(t, []uint{roles[0].ID, roles[1].ID, roles[2].ID}, got.RoleIDs)
}

func TestFindUserAPIRoleSetUsesDefaultGroupForZeroGroupID(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	role := seedRoles(t, db, "default_group")[0]
	user := models.User{Username: "zero-group", GroupID: models.DefaultUserGroupID}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Model(&models.User{}).Where("id = ?", user.ID).Update("group_id", 0).Error)
	seedBinding(t, db, models.APIPrincipalUserGroup, models.DefaultUserGroupID, role.ID)

	got, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, RoleSetResult{Exists: true, RoleIDs: []uint{role.ID}}, got)
}

func TestFindTokenAPIRoleSetInheritUnionsUserAndGroup(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	roles := seedRoles(t, db, "direct", "group")
	user := models.User{Username: "owner", GroupID: 8}
	require.NoError(t, db.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "sk-inherit", Name: "inherit", APIRoleMode: models.APIRoleModeInherit}
	require.NoError(t, db.Create(&token).Error)
	seedBinding(t, db, models.APIPrincipalUser, user.ID, roles[1].ID)
	seedBinding(t, db, models.APIPrincipalUserGroup, user.GroupID, roles[0].ID)
	seedBinding(t, db, models.APIPrincipalUserGroup, user.GroupID, roles[1].ID)
	seedBinding(t, db, models.APIPrincipalToken, token.ID, roles[0].ID)

	got, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindToken(context.Background(), token.ID)
	require.NoError(t, err)
	require.Equal(t, RoleSetResult{Exists: true, RoleIDs: []uint{roles[0].ID, roles[1].ID}}, got)
}

func TestFindTokenAPIRoleSetExplicitEmptyIsPositiveEmpty(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	user := models.User{Username: "owner"}
	require.NoError(t, db.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "sk-explicit", Name: "explicit", APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, db.Create(&token).Error)

	got, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindToken(context.Background(), token.ID)
	require.NoError(t, err)
	require.True(t, got.Exists)
	require.Empty(t, got.RoleIDs)
}

func TestFindRoleSetDistinguishesMissingPrincipalFromDatabaseError(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	finder := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC())

	user, err := finder.FindUser(context.Background(), 404)
	require.NoError(t, err)
	require.False(t, user.Exists)
	token, err := finder.FindToken(context.Background(), 404)
	require.NoError(t, err)
	require.False(t, token.Exists)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	_, err = finder.FindUser(context.Background(), 1)
	require.Error(t, err)
	_, err = finder.FindToken(context.Background(), 1)
	require.Error(t, err)
}

func TestFindTokenAPIRoleSetRejectsMissingOwner(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	token := models.Token{UserID: 999, Key: "sk-orphan", Name: "orphan", APIRoleMode: models.APIRoleModeInherit}
	require.NoError(t, db.Create(&token).Error)

	_, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindToken(context.Background(), token.ID)
	require.Error(t, err)
}

func TestFindTokenAPIRoleSetRejectsInvalidMode(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	user := models.User{Username: "invalid-mode-owner"}
	require.NoError(t, db.Create(&user).Error)
	token := models.Token{UserID: user.ID, Key: "sk-invalid-mode", Name: "invalid-mode"}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Exec("PRAGMA ignore_check_constraints = ON").Error)
	require.NoError(t, db.Model(&models.Token{}).Where("id = ?", token.ID).Update("api_role_mode", "invalid").Error)

	_, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindToken(context.Background(), token.ID)
	require.Error(t, err)
}

func TestGatewayAdminRoleIsDerivedWithoutPerAdminBinding(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	adminRole := seedRoles(t, db, GatewayAdminRoleKey)[0]
	admin := models.User{Username: "admin", Role: consts.RoleAdmin}
	user := models.User{Username: "user", Role: consts.RoleUser}
	require.NoError(t, db.Create(&admin).Error)
	require.NoError(t, db.Create(&user).Error)
	finder := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC())

	adminSet, err := finder.FindUser(context.Background(), admin.ID)
	require.NoError(t, err)
	require.Equal(t, []uint{adminRole.ID}, adminSet.RoleIDs)
	userSet, err := finder.FindUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Empty(t, userSet.RoleIDs)
	var count int64
	require.NoError(t, db.Model(&models.RoleBinding{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestGatewayAdminRoleDerivationRejectsInvalidSeed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		seed   bool
	}{
		{name: "missing", seed: false},
		{name: "disabled", seed: true, status: consts.StatusDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, q := setupAPIRBACTest(t)
			if tc.seed {
				role := models.Role{Key: GatewayAdminRoleKey, Name: "Gateway Admin", BuiltIn: true, Status: tc.status}
				require.NoError(t, db.Create(&role).Error)
				require.NoError(t, db.Model(&models.Role{}).Where("id = ?", role.ID).Update("status", tc.status).Error)
			}
			admin := models.User{Username: "admin", Role: consts.RoleAdmin}
			require.NoError(t, db.Create(&admin).Error)
			_, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(context.Background(), admin.ID)
			require.Error(t, err)
		})
	}
}

func TestGatewayAdminRoleDerivationRejectsDuplicateSeed(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	require.NoError(t, db.Migrator().DropIndex(&models.Role{}, "Key"))
	for _, name := range []string{"Gateway Admin A", "Gateway Admin B"} {
		require.NoError(t, db.Create(&models.Role{
			Key: GatewayAdminRoleKey, Name: name, BuiltIn: true, Status: consts.StatusEnabled,
		}).Error)
	}
	appender := NewGatewayAdminRoleAppender(q.APIRBAC())

	_, err := appender.AppendForUser(context.Background(), models.User{Role: consts.RoleAdmin}, nil)
	require.Error(t, err)
}

func TestGatewayAdminRoleDerivationPreservesQueryError(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	sentinel := errors.New("gateway role query failed")
	callbackName := "test:fail_gateway_admin_role_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "roles" {
			tx.AddError(sentinel)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })
	appender := NewGatewayAdminRoleAppender(q.APIRBAC())

	_, err := appender.AppendForUser(context.Background(), models.User{Role: consts.RoleAdmin}, nil)
	require.ErrorIs(t, err, sentinel)
}

func TestRoleSetFiltersHistoricalGatewayAdminBindings(t *testing.T) {
	for _, principalType := range []models.APIPrincipalType{
		models.APIPrincipalUser, models.APIPrincipalUserGroup, models.APIPrincipalToken,
	} {
		t.Run(string(principalType), func(t *testing.T) {
			db, q := setupAPIRBACTest(t)
			gatewayRole := seedRoles(t, db, GatewayAdminRoleKey)[0]
			user := models.User{Username: "ordinary", Role: consts.RoleUser, GroupID: 7}
			require.NoError(t, db.Create(&user).Error)
			finder := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC())
			var got RoleSetResult
			var err error
			switch principalType {
			case models.APIPrincipalUser:
				seedBinding(t, db, principalType, user.ID, gatewayRole.ID)
				got, err = finder.FindUser(context.Background(), user.ID)
			case models.APIPrincipalUserGroup:
				seedBinding(t, db, principalType, user.GroupID, gatewayRole.ID)
				got, err = finder.FindUser(context.Background(), user.ID)
			case models.APIPrincipalToken:
				token := models.Token{UserID: user.ID, Key: "sk-bad-admin-binding", Name: "bad", APIRoleMode: models.APIRoleModeExplicit}
				require.NoError(t, db.Create(&token).Error)
				seedBinding(t, db, principalType, token.ID, gatewayRole.ID)
				got, err = finder.FindToken(context.Background(), token.ID)
			}
			require.NoError(t, err)
			require.True(t, got.Exists)
			require.NotContains(t, got.RoleIDs, gatewayRole.ID)
		})
	}
}

func TestRoleSetRetainsDisabledAndMissingRoleIDsAsNonEffectiveReferences(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	disabled := models.Role{Key: "disabled-reference", Name: "Disabled", Status: consts.StatusDisabled}
	require.NoError(t, db.Create(&disabled).Error)
	require.NoError(t, db.Model(&models.Role{}).Where("id = ?", disabled.ID).Update("status", consts.StatusDisabled).Error)
	user := models.User{Username: "stale-references", Role: consts.RoleUser}
	require.NoError(t, db.Create(&user).Error)
	seedBinding(t, db, models.APIPrincipalUser, user.ID, disabled.ID)
	seedBinding(t, db, models.APIPrincipalUser, user.ID, 999)

	got, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(context.Background(), user.ID)
	require.NoError(t, err)
	require.Equal(t, []uint{disabled.ID, 999}, got.RoleIDs)
}

func TestFindRoleSetReturnsBindingDatabaseError(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	user := models.User{Username: "binding-error"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Migrator().DropTable(&models.RoleBinding{}))

	_, err := NewRoleSetFinder(q.User(), q.Token(), q.APIRBAC()).FindUser(context.Background(), user.ID)
	require.Error(t, err)
	require.False(t, errors.Is(err, gorm.ErrRecordNotFound))
}

func TestTokenRoleAssignmentDoesNotTurnDatabaseFailureIntoInvalidRole(t *testing.T) {
	db, q := setupAPIRBACTest(t)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	validator := NewTokenRoleAssignmentValidator(q.User(), q.Token(), q.APIRBAC())

	err = validator.Validate(context.Background(), 1, []uint{1}, true)
	require.Error(t, err)
	require.False(t, errors.Is(err, ErrRoleNotAssignable))
}

func seedRoles(t *testing.T, db *gorm.DB, keys ...string) []models.Role {
	t.Helper()
	roles := make([]models.Role, len(keys))
	for i, key := range keys {
		roles[i] = models.Role{Key: key, Name: key, Status: consts.StatusEnabled, BuiltIn: key == GatewayAdminRoleKey}
		require.NoError(t, db.Create(&roles[i]).Error)
	}
	return roles
}

func seedBinding(t *testing.T, db *gorm.DB, principalType models.APIPrincipalType, principalID, roleID uint) {
	t.Helper()
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: principalType, PrincipalID: principalID, RoleID: roleID}).Error)
}
