package models

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSeedDefaultUserGroup_CreatesAndBackfills(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	// Old user: force group_id=0 via raw SQL to simulate pre-migration data
	u := User{Username: "old", Password: "x"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("UPDATE users SET group_id = 0 WHERE id = ?", u.ID).Error; err != nil {
		t.Fatal(err)
	}

	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatal(err)
	}

	var g UserGroup
	if err := db.First(&g, 1).Error; err != nil {
		t.Fatalf("default group missing: %v", err)
	}
	if g.Name != "default" {
		t.Fatalf("default name = %q", g.Name)
	}

	var got User
	db.First(&got, u.ID)
	if got.GroupID != 1 {
		t.Fatalf("backfill failed, GroupID = %d", got.GroupID)
	}
}

func TestSeedDefaultUserGroup_Idempotent(t *testing.T) {
	db, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	AutoMigrate(db)
	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatal(err)
	}
	if err := SeedDefaultUserGroup(db); err != nil {
		t.Fatalf("second call: %v", err)
	}

	var n int64
	db.Model(&UserGroup{}).Where("id = 1").Count(&n)
	if n != 1 {
		t.Fatalf("default group count = %d (want 1)", n)
	}
}

func TestSeedBYOKSettings_FirstStartInserts(t *testing.T) {
	db := setupTestDB(t)

	if err := SeedBYOKSettings(db); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Model(&Setting{}).Where("key LIKE ?", "byok_%").Count(&count)
	if count != 5 {
		t.Fatalf("expected 5 byok_* settings, got %d", count)
	}
}

func TestSeedBYOKSettings_DoesNotOverrideExisting(t *testing.T) {
	db := setupTestDB(t)
	db.Create(&Setting{Key: "byok_enabled", Value: "false"}) // admin 改过

	if err := SeedBYOKSettings(db); err != nil {
		t.Fatal(err)
	}

	var s Setting
	db.Where("key = ?", "byok_enabled").First(&s)
	if s.Value != "false" {
		t.Fatalf("byok_enabled overwritten: %q", s.Value)
	}
}

func TestSeedGatewayAdminRoleIsIdempotentAndRepairsGrants(t *testing.T) {
	// This catches a seed that creates a role without its invoke grant, leaks
	// duplicate rows on repeated startup, or cannot repair a manually deleted grant.
	db := openSplitTestDB(t)
	require.NoError(t, MigrateCoreDB(db))

	require.NoError(t, SeedGatewayAdminRole(db))
	require.NoError(t, SeedGatewayAdminRole(db))

	var role Role
	require.NoError(t, db.Where("key = ?", "gateway_admin").First(&role).Error)
	require.True(t, role.BuiltIn)
	require.Equal(t, consts.StatusEnabled, role.Status)
	require.Equal(t, "Gateway Admin", role.Name)
	require.Equal(t, "Built-in administrator for generic API gateway resources", role.Description)
	var grants []Permission
	require.NoError(t, db.Model(&Permission{}).
		Joins("JOIN role_permissions ON role_permissions.permission_id = permissions.id").
		Where("role_permissions.role_id = ?", role.ID).
		Order("permissions.resource, permissions.action").
		Find(&grants).Error)
	require.Equal(t, []Permission{
		{Resource: APIResourceService, ResourceID: 0, Action: APIPermissionInvoke},
	}, withoutPermissionIDs(grants))

	var deleted Permission
	require.NoError(t, db.Where("resource = ? AND resource_id = ? AND action = ?", APIResourceService, 0, APIPermissionInvoke).First(&deleted).Error)
	require.NoError(t, db.Where("role_id = ? AND permission_id = ?", role.ID, deleted.ID).Delete(&RolePermission{}).Error)
	extra := Permission{Resource: APIResourceService, ResourceID: 99, Action: APIPermissionInvoke}
	require.NoError(t, db.Create(&extra).Error)
	require.NoError(t, db.Create(&RolePermission{RoleID: role.ID, PermissionID: extra.ID}).Error)
	require.NoError(t, db.Model(&Role{}).Where("id = ?", role.ID).Updates(map[string]any{
		"name": "Drifted", "description": "drifted", "built_in": false, "status": consts.StatusDisabled,
	}).Error)
	require.NoError(t, SeedGatewayAdminRole(db))

	var repaired int64
	require.NoError(t, db.Model(&RolePermission{}).Where("role_id = ? AND permission_id = ?", role.ID, deleted.ID).Count(&repaired).Error)
	require.Equal(t, int64(1), repaired)
	var extraCount int64
	require.NoError(t, db.Model(&RolePermission{}).Where("role_id = ? AND permission_id = ?", role.ID, extra.ID).Count(&extraCount).Error)
	require.Zero(t, extraCount, "seed must remove grants outside the built-in contract")
	role = Role{}
	require.NoError(t, db.Where("key = ?", "gateway_admin").First(&role).Error)
	require.True(t, role.BuiltIn)
	require.Equal(t, consts.StatusEnabled, role.Status)
	require.Equal(t, "Gateway Admin", role.Name)
	require.Equal(t, "Built-in administrator for generic API gateway resources", role.Description)
	var bindingCount int64
	require.NoError(t, db.Model(&RoleBinding{}).Count(&bindingCount).Error)
	require.Zero(t, bindingCount, "the built-in seed must never grant the role to a principal")
}

func withoutPermissionIDs(in []Permission) []Permission {
	out := make([]Permission, len(in))
	for i, permission := range in {
		out[i] = Permission{Resource: permission.Resource, ResourceID: permission.ResourceID, Action: permission.Action}
	}
	return out
}
