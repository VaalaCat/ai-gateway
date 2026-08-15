package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDeleteAPIPrincipalAccessRemovesCustomBindingsAndManagedRoleOnlyForPrincipal(t *testing.T) {
	db, _ := setupStrictSplitDBs(t)
	user := models.User{Username: "access-delete", Password: "x", GroupID: 1, Status: consts.StatusEnabled}
	other := models.User{Username: "access-keep", Password: "x", GroupID: 1, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	custom := models.Role{Key: "custom-delete-test", Name: "Custom", Kind: models.APIRoleKindCustom, Status: consts.StatusEnabled}
	managed := models.Role{Key: models.ManagedAPIRoleKey(models.APIPrincipalUser, user.ID), Name: "Managed", Kind: models.APIRoleKindManaged, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&custom).Error)
	require.NoError(t, db.Create(&managed).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: managed.ID, PermissionID: permission.ID}).Error)
	for _, binding := range []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: custom.ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: managed.ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: other.ID, RoleID: custom.ID},
	} {
		require.NoError(t, db.Create(&binding).Error)
	}

	deletedRoleID, err := DeleteAPIPrincipalAccess(db, models.APIPrincipalUser, user.ID)
	require.NoError(t, err)
	require.Equal(t, managed.ID, deletedRoleID)
	var deletedBindings, deletedRoleLinks, deletedRoles, keptBindings int64
	require.NoError(t, db.Model(&models.RoleBinding{}).Where("principal_type = ? AND principal_id = ?", models.APIPrincipalUser, user.ID).Count(&deletedBindings).Error)
	require.NoError(t, db.Model(&models.RolePermission{}).Where("role_id = ?", managed.ID).Count(&deletedRoleLinks).Error)
	require.NoError(t, db.Model(&models.Role{}).Where("id = ?", managed.ID).Count(&deletedRoles).Error)
	require.NoError(t, db.Model(&models.RoleBinding{}).Where("principal_type = ? AND principal_id = ?", models.APIPrincipalUser, other.ID).Count(&keptBindings).Error)
	require.Zero(t, deletedBindings)
	require.Zero(t, deletedRoleLinks)
	require.Zero(t, deletedRoles)
	require.Equal(t, int64(1), keptBindings)
}

func TestDeleteAPIPrincipalAccessWithoutManagedRoleStillDeletesCustomBindings(t *testing.T) {
	db, _ := setupStrictSplitDBs(t)
	role := models.Role{Key: "custom-only-delete", Name: "Custom", Kind: models.APIRoleKindCustom, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: 77, RoleID: role.ID}).Error)

	deletedRoleID, err := DeleteAPIPrincipalAccess(db, models.APIPrincipalToken, 77)
	require.NoError(t, err)
	require.Zero(t, deletedRoleID)
	var count int64
	require.NoError(t, db.Model(&models.RoleBinding{}).Where("principal_type = ? AND principal_id = ?", models.APIPrincipalToken, 77).Count(&count).Error)
	require.Zero(t, count)
}

func TestDeleteAPIPrincipalAccessRollsBackWithOwningTransaction(t *testing.T) {
	db, _ := setupStrictSplitDBs(t)
	role := models.Role{Key: models.ManagedAPIRoleKey(models.APIPrincipalUserGroup, 88), Name: "Managed", Kind: models.APIRoleKindManaged, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 88, RoleID: role.ID}).Error)
	sentinel := errors.New("force owner rollback")

	err := db.Transaction(func(tx *gorm.DB) error {
		_, deleteErr := DeleteAPIPrincipalAccess(tx, models.APIPrincipalUserGroup, 88)
		require.NoError(t, deleteErr)
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	var roles, bindings int64
	require.NoError(t, db.Model(&models.Role{}).Where("id = ?", role.ID).Count(&roles).Error)
	require.NoError(t, db.Model(&models.RoleBinding{}).Where("role_id = ?", role.ID).Count(&bindings).Error)
	require.Equal(t, int64(1), roles)
	require.Equal(t, int64(1), bindings)
}

func TestDeleteAPIPrincipalAccessRejectsCustomRoleUsingManagedKey(t *testing.T) {
	db, _ := setupStrictSplitDBs(t)
	role := models.Role{Key: models.ManagedAPIRoleKey(models.APIPrincipalUser, 42), Name: "Custom collision", Kind: models.APIRoleKindCustom, Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&role).Error)
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: 0, Action: models.APIPermissionInvoke}
	require.NoError(t, db.Create(&permission).Error)
	require.NoError(t, db.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
	for _, principalID := range []uint{42, 99} {
		require.NoError(t, db.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: principalID, RoleID: role.ID}).Error)
	}

	deletedRoleID, err := DeleteAPIPrincipalAccess(db, models.APIPrincipalUser, 42)
	require.Error(t, err)
	require.Zero(t, deletedRoleID)
	var roles, links, bindings int64
	require.NoError(t, db.Model(&models.Role{}).Where("id = ?", role.ID).Count(&roles).Error)
	require.NoError(t, db.Model(&models.RolePermission{}).Where("role_id = ?", role.ID).Count(&links).Error)
	require.NoError(t, db.Model(&models.RoleBinding{}).Where("role_id = ?", role.ID).Count(&bindings).Error)
	require.Equal(t, int64(1), roles)
	require.Equal(t, int64(1), links)
	require.Equal(t, int64(2), bindings)
}

func TestLockAPIPrincipalValidatesEveryPrincipalType(t *testing.T) {
	db, _ := setupStrictSplitDBs(t)
	user := models.User{Username: "lock-user", Password: "x", GroupID: 1, Status: consts.StatusEnabled}
	group := models.UserGroup{Name: "lock-group", Status: consts.StatusEnabled}
	token := models.Token{Key: "sk-lock-principal", Name: "lock-token", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&group).Error)
	require.NoError(t, db.Create(&token).Error)

	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		for principalType, principalID := range map[models.APIPrincipalType]uint{
			models.APIPrincipalUser: user.ID, models.APIPrincipalUserGroup: group.ID, models.APIPrincipalToken: token.ID,
		} {
			require.NoError(t, LockAPIPrincipal(tx, principalType, principalID))
		}
		return nil
	}))
	require.ErrorIs(t, LockAPIPrincipal(db, models.APIPrincipalUser, 999_999), gorm.ErrRecordNotFound)
	require.Error(t, LockAPIPrincipal(db, "invalid", 1))
}
