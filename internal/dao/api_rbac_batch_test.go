package dao

import (
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Break caught: passing an unbounded caller-owned slice directly to an IN
// clause eventually exceeds database bind-variable limits. Every RBAC bulk
// reader must issue one query per fixed-size chunk instead of one per row.
func TestAPIRBACBulkReadersUseBoundedIDChunks(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.AutoMigrate(&models.Role{}, &models.Permission{}, &models.RolePermission{}, &models.RoleBinding{}))
	const rowCount = 401
	users := make([]models.User, rowCount)
	roles := make([]models.Role, rowCount)
	permissions := make([]models.Permission, rowCount)
	for i := 0; i < rowCount; i++ {
		users[i] = models.User{Username: fmt.Sprintf("batch-user-%03d", i), Status: consts.StatusEnabled}
		roles[i] = models.Role{Key: fmt.Sprintf("batch-role-%03d", i), Name: fmt.Sprintf("Batch Role %03d", i), Status: consts.StatusEnabled}
		permissions[i] = models.Permission{Resource: models.APIResourceService, ResourceID: uint(i + 1), Action: models.APIPermissionInvoke}
	}
	require.NoError(t, db.CreateInBatches(&users, 100).Error)
	require.NoError(t, db.CreateInBatches(&roles, 100).Error)
	require.NoError(t, db.CreateInBatches(&permissions, 100).Error)
	rolePermissions := make([]models.RolePermission, rowCount)
	bindings := make([]models.RoleBinding, rowCount)
	userIDs := make([]uint, rowCount)
	roleIDs := make([]uint, rowCount)
	permissionIDs := make([]uint, rowCount)
	for i := 0; i < rowCount; i++ {
		userIDs[i] = users[i].ID
		roleIDs[i] = roles[i].ID
		permissionIDs[i] = permissions[i].ID
		rolePermissions[i] = models.RolePermission{RoleID: roles[i].ID, PermissionID: permissions[i].ID}
		bindings[i] = models.RoleBinding{PrincipalType: models.APIPrincipalToken, PrincipalID: users[i].ID, RoleID: roles[i].ID}
	}
	require.NoError(t, db.CreateInBatches(&rolePermissions, 100).Error)
	require.NoError(t, db.CreateInBatches(&bindings, 100).Error)

	queries := 0
	const callbackName = "test:count_api_rbac_bulk_chunks"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(*gorm.DB) { queries++ }))
	t.Cleanup(func() { require.NoError(t, db.Callback().Query().Remove(callbackName)) })
	q := NewAdminQuery(NewContext(&testApp{db: db})).APIRBAC()

	assertTwoChunks := func(name string, run func() (int, error)) {
		t.Helper()
		queries = 0
		count, err := run()
		require.NoError(t, err, name)
		require.Equal(t, rowCount, count, name)
		require.Equal(t, 2, queries, name)
	}
	assertTwoChunks("users", func() (int, error) {
		rows, err := q.ListUsersByIDs(userIDs)
		return len(rows), err
	})
	assertTwoChunks("principal bindings", func() (int, error) {
		rows, err := q.ListRoleSetBindingsByPrincipals(models.APIPrincipalToken, userIDs)
		return len(rows), err
	})
	assertTwoChunks("role permissions", func() (int, error) {
		rows, err := q.ListRolePermissionsByRoleIDs(roleIDs)
		return len(rows), err
	})
	assertTwoChunks("role bindings", func() (int, error) {
		rows, err := q.ListRoleBindingsByRoleIDs(roleIDs)
		return len(rows), err
	})
	assertTwoChunks("permissions", func() (int, error) {
		rows, err := q.ListPermissionsByIDs(permissionIDs)
		return len(rows), err
	})
}
