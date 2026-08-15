package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Break caught: invalidating only the binding's new principal leaves the old
// principal's cached RoleSet authorized after a binding is moved.
func TestAPIRoleBindingUpdateInvalidatesOldAndNewPrincipalsWithoutDuplicates(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	users := []models.User{
		{Username: "binding-old", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "binding-new", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
	}
	for i := range users {
		require.NoError(t, srv.DB.Create(&users[i]).Error)
	}
	roles := []models.Role{
		{Key: "binding-role-a", Name: "Binding role A", Status: consts.StatusEnabled},
		{Key: "binding-role-b", Name: "Binding role B", Status: consts.StatusEnabled},
	}
	for i := range roles {
		require.NoError(t, srv.DB.Create(&roles[i]).Error)
	}
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: users[0].ID, RoleID: roles[0].ID}
	require.NoError(t, srv.DB.Create(&binding).Error)

	var invalidated []uint
	subscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	moved := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": users[1].ID, "role_id": roles[0].ID,
	})
	require.Equal(t, http.StatusOK, moved.Code, moved.Body.String())
	require.Equal(t, []uint{users[0].ID, users[1].ID}, invalidated)

	samePrincipal := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": users[1].ID, "role_id": roles[1].ID,
	})
	require.Equal(t, http.StatusOK, samePrincipal.Code, samePrincipal.Body.String())
	require.Equal(t, []uint{users[0].ID, users[1].ID, users[1].ID}, invalidated)
	require.NoError(t, srv.DB.First(&binding, binding.ID).Error)
	require.Equal(t, users[1].ID, binding.PrincipalID)
	require.Equal(t, roles[1].ID, binding.RoleID)
}

// Break caught: an update that skips the same transactional validation as
// create can bind zero principals, built-in roles, or publish after rollback.
func TestAPIRoleBindingUpdateValidatesAndRollsBackBeforePublishing(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "binding-validation", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "binding-validation", Name: "Binding validation", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}
	require.NoError(t, srv.DB.Create(&binding).Error)

	zero := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": 0, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, zero.Code, zero.Body.String())
	var gatewayAdmin models.Role
	require.NoError(t, srv.DB.Where("key = ?", models.GatewayAdminRoleKey).First(&gatewayAdmin).Error)
	builtin := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": user.ID, "role_id": gatewayAdmin.ID,
	})
	require.Equal(t, http.StatusBadRequest, builtin.Code, builtin.Body.String())

	eventsSeen := 0
	subscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, _ protocol.APIRoleSetInvalidate) error {
		eventsSeen++
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })
	callbackName := "test:fail_api_role_binding_update"
	require.NoError(t, srv.DB.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "role_bindings" {
			tx.AddError(errors.New("force binding update rollback"))
		}
	}))
	t.Cleanup(func() { _ = srv.DB.Callback().Update().Remove(callbackName) })
	failed := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": user.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, failed.Code, failed.Body.String())
	require.Zero(t, eventsSeen)
	require.NoError(t, srv.DB.First(&binding, binding.ID).Error)
	require.Equal(t, user.ID, binding.PrincipalID)
}

// Break caught: deleting the role row without snapshotting its bindings first
// removes the only information needed to invalidate user/group/token RoleSets.
func TestAPIRoleDeleteInvalidatesEveryBoundPrincipalAfterCommit(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	directUser := models.User{Username: "role-delete-direct", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&directUser).Error)
	group := models.UserGroup{Name: "role-delete-group", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&group).Error)
	groupUser := models.User{Username: "role-delete-group-member", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: group.ID}
	require.NoError(t, srv.DB.Create(&groupUser).Error)
	token := models.Token{Name: "role-delete-token", Key: "sk-role-delete", UserID: directUser.ID, Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&token).Error)
	role := models.Role{Key: "delete-bound-role", Name: "Delete bound role", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	bindings := []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: directUser.ID, RoleID: role.ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: group.ID, RoleID: role.ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID},
	}
	for i := range bindings {
		require.NoError(t, srv.DB.Create(&bindings[i]).Error)
	}
	var users, tokens, groups []uint
	_, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		users = append(users, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(srv.Bus, events.TokenAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		tokens = append(tokens, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	_, err = events.Subscribe(srv.Bus, events.UserGroupAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetFetchResult) error {
		groups = append(groups, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)

	response := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	require.Equal(t, []uint{directUser.ID, groupUser.ID}, users)
	require.Equal(t, []uint{token.ID}, tokens)
	require.Equal(t, []uint{group.ID}, groups)
}

func TestAPIRoleManagedRoleProtection(t *testing.T) {
	// This catches ordinary role CRUD or binding APIs modifying a role owned by
	// the managed-principal projection instead of the explicit grant facade.
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	managed := models.Role{
		Key:    models.ManagedAPIRoleKey(models.APIPrincipalUser, 42),
		Name:   "Managed user role",
		Kind:   models.APIRoleKindManaged,
		Status: consts.StatusEnabled,
	}
	require.NoError(t, srv.DB.Create(&managed).Error)
	user := models.User{Username: "managed-role-binding", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: managed.ID}
	require.NoError(t, srv.DB.Create(&binding).Error)

	get := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", managed.ID), nil)
	require.Equal(t, http.StatusNotFound, get.Code, get.Body.String())
	update := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", managed.ID), map[string]any{
		"key": managed.Key, "name": "Mutated", "permissions": []any{},
	})
	require.Equal(t, http.StatusBadRequest, update.Code, update.Body.String())
	deleteRole := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-roles/%d", managed.ID), nil)
	require.Equal(t, http.StatusBadRequest, deleteRole.Code, deleteRole.Body.String())
	deleteBinding := reqHelper(srv, adminJWT, http.MethodDelete, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), nil)
	require.Equal(t, http.StatusBadRequest, deleteBinding.Code, deleteBinding.Body.String())

	var stored models.Role
	require.NoError(t, srv.DB.First(&stored, managed.ID).Error)
	require.Equal(t, "Managed user role", stored.Name)
	var bindingCount int64
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Where("id = ?", binding.ID).Count(&bindingCount).Error)
	require.Equal(t, int64(1), bindingCount)
}

func TestAPIRoleBindingUpdateDoesNotMoveManagedBindingIntoCustomRole(t *testing.T) {
	// Break caught: checking only the replacement role lets the ordinary API
	// take ownership of a binding which was created by the managed-role flow.
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "managed-binding-update", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	managed := models.Role{Key: models.ManagedAPIRoleKey(models.APIPrincipalUser, 99), Name: "Managed", Kind: models.APIRoleKindManaged, Status: consts.StatusEnabled}
	custom := models.Role{Key: "managed-binding-target", Name: "Custom", Status: consts.StatusEnabled}
	for _, model := range []any{&user, &managed, &custom} {
		require.NoError(t, srv.DB.Create(model).Error)
	}
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: managed.ID}
	require.NoError(t, srv.DB.Create(&binding).Error)

	response := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalUser, "principal_id": user.ID, "role_id": custom.ID,
	})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	var stored models.RoleBinding
	require.NoError(t, srv.DB.First(&stored, binding.ID).Error)
	require.Equal(t, managed.ID, stored.RoleID)
	require.Equal(t, user.ID, stored.PrincipalID)
}

func TestAPIRoleBindingTokenModeRequiresExplicit(t *testing.T) {
	// This catches direct generic binding of an inherit Token, which would make
	// its effective role source ambiguous with inherited user/group roles.
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	owner := models.User{Username: "token-mode-owner", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&owner).Error)
	role := models.Role{Key: "token-mode-role", Name: "Token mode role", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	inherit := models.Token{Name: "inherit-token", Key: "sk-inherit-token", UserID: owner.ID, Status: consts.StatusEnabled}
	explicit := models.Token{Name: "explicit-token", Key: "sk-explicit-token", UserID: owner.ID, Status: consts.StatusEnabled, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&inherit).Error)
	require.NoError(t, srv.DB.Create(&explicit).Error)

	rejected := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": models.APIPrincipalToken, "principal_id": inherit.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-role-bindings", map[string]any{
		"principal_type": models.APIPrincipalToken, "principal_id": explicit.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	var binding models.RoleBinding
	require.NoError(t, srv.DB.Where("principal_type = ? AND principal_id = ?", models.APIPrincipalToken, explicit.ID).First(&binding).Error)
	updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalToken, "principal_id": explicit.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.NoError(t, srv.DB.Model(&models.Token{}).Where("id = ?", explicit.ID).UpdateColumn("api_role_mode", models.APIRoleModeInherit).Error)
	rejectedUpdate := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-role-bindings/%d", binding.ID), map[string]any{
		"principal_type": models.APIPrincipalToken, "principal_id": explicit.ID, "role_id": role.ID,
	})
	require.Equal(t, http.StatusBadRequest, rejectedUpdate.Code, rejectedUpdate.Body.String())
	var stored models.RoleBinding
	require.NoError(t, srv.DB.First(&stored, binding.ID).Error)
	require.Equal(t, role.ID, stored.RoleID)
}

func TestAPIRoleListsHideManagedRolesAndBindings(t *testing.T) {
	// Break caught: applying the custom-role boundary after count or pagination,
	// or not applying it to bindings, exposes managed projection rows.
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	roles := []models.Role{
		{Key: "custom-first", Name: "Custom first", Status: consts.StatusEnabled},
		{Key: "custom-second", Name: "Custom second", Status: consts.StatusEnabled},
		{Key: models.ManagedAPIRoleKey(models.APIPrincipalUser, 100), Name: "Managed searchable", Kind: models.APIRoleKindManaged, Status: consts.StatusEnabled},
	}
	for i := range roles {
		require.NoError(t, srv.DB.Create(&roles[i]).Error)
	}
	bindings := []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: 1, RoleID: roles[0].ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: 1, RoleID: roles[2].ID},
	}
	for i := range bindings {
		require.NoError(t, srv.DB.Create(&bindings[i]).Error)
	}
	customKind := models.APIRoleKindCustom
	var customTotal, assignableCustomTotal int64
	require.NoError(t, srv.DB.Model(&models.Role{}).Where("kind = ?", customKind).Count(&customTotal).Error)
	require.NoError(t, srv.DB.Model(&models.Role{}).
		Where("kind = ? AND status = ? AND built_in = ? AND `key` <> ?", customKind, consts.StatusEnabled, false, models.GatewayAdminRoleKey).
		Count(&assignableCustomTotal).Error)

	listRoles := func(path string) (int64, []models.Role) {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []models.Role `json:"data"`
			Total int64         `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return body.Total, body.Data
	}
	for _, tt := range []struct {
		path      string
		wantTotal int64
	}{
		{path: "/api/admin/api-roles", wantTotal: customTotal},
		{path: "/api/admin/api-roles?assignable=true", wantTotal: assignableCustomTotal},
		{path: "/api/admin/api-roles?search=Managed%20searchable", wantTotal: 0},
	} {
		total, rows := listRoles(tt.path)
		if tt.wantTotal == 0 {
			require.Zero(t, total)
			require.Empty(t, rows)
			continue
		}
		require.Equal(t, tt.wantTotal, total)
		for _, row := range rows {
			require.Equal(t, models.APIRoleKindCustom, row.Kind)
		}
	}
	total, page := listRoles("/api/admin/api-roles?page=2&page_size=1")
	require.Equal(t, customTotal, total)
	require.Equal(t, []uint{roles[0].ID}, []uint{page[0].ID})

	listBindings := func(path string) (int64, []models.RoleBinding) {
		response := reqHelper(srv, adminJWT, http.MethodGet, path, nil)
		require.Equal(t, http.StatusOK, response.Code, response.Body.String())
		var body struct {
			Data  []models.RoleBinding `json:"data"`
			Total int64                `json:"total"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &body))
		return body.Total, body.Data
	}
	total, rows := listBindings("/api/admin/api-role-bindings?principal_type=user&principal_id=1")
	require.Equal(t, int64(1), total)
	require.Equal(t, []models.RoleBinding{bindings[0]}, rows)
	total, rows = listBindings(fmt.Sprintf("/api/admin/api-role-bindings?role_id=%d", roles[2].ID))
	require.Zero(t, total)
	require.Empty(t, rows)
}

// Break caught: loading permissions and members inside the paginated role loop
// makes the role-list query count grow by three for every returned role.
func TestAPIRoleListBulkLoadsRelationsAfterPagination(t *testing.T) {
	for _, roleCount := range []int{1, 100} {
		t.Run(fmt.Sprintf("roles_%d", roleCount), func(t *testing.T) {
			srv := setupTestMaster(t)
			require.NoError(t, srv.InitAdminUser("admin", "admin123"))
			adminJWT := loginAsAdmin(t, srv, "admin", "admin123")

			roles := make([]models.Role, roleCount)
			permissions := make([]models.Permission, roleCount)
			for i := 0; i < roleCount; i++ {
				roles[i] = models.Role{
					Key: fmt.Sprintf("bulk-list-role-%03d", i), Name: fmt.Sprintf("Bulk list role %03d", i), Status: consts.StatusEnabled,
				}
				permissions[i] = models.Permission{
					Resource: models.APIResourceService, ResourceID: uint(10_000 + i), Action: models.APIPermissionInvoke,
				}
			}
			require.NoError(t, srv.DB.CreateInBatches(&roles, 100).Error)
			require.NoError(t, srv.DB.CreateInBatches(&permissions, 100).Error)
			links := make([]models.RolePermission, roleCount)
			bindings := make([]models.RoleBinding, roleCount)
			for i := range roles {
				links[i] = models.RolePermission{RoleID: roles[i].ID, PermissionID: permissions[i].ID}
				bindings[i] = models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: 1, RoleID: roles[i].ID}
			}
			require.NoError(t, srv.DB.CreateInBatches(&links, 100).Error)
			require.NoError(t, srv.DB.CreateInBatches(&bindings, 100).Error)

			queries := 0
			callbackName := fmt.Sprintf("test:count_api_role_list_queries_%d", roleCount)
			require.NoError(t, srv.DB.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				switch tx.Statement.Table {
				case "roles", "role_permissions", "permissions", "role_bindings":
					queries++
				}
			}))
			t.Cleanup(func() { require.NoError(t, srv.DB.Callback().Query().Remove(callbackName)) })

			response := reqHelper(srv, adminJWT, http.MethodGet,
				fmt.Sprintf("/api/admin/api-roles?search=bulk-list-role-&page=1&page_size=%d", roleCount), nil)
			require.Equal(t, http.StatusOK, response.Code, response.Body.String())
			var page struct {
				Data []struct {
					Permissions []models.Permission `json:"permissions"`
					Members     []struct {
						PrincipalType models.APIPrincipalType `json:"principal_type"`
						PrincipalID   uint                    `json:"principal_id"`
					} `json:"members"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &page))
			require.Len(t, page.Data, roleCount)
			for _, row := range page.Data {
				require.Len(t, row.Permissions, 1)
				require.Len(t, row.Members, 1)
			}
			require.Equal(t, 5, queries)
		})
	}
}

// Break caught: accepting members in the HTTP body without persisting them in
// the role transaction leaves the edit form apparently successful but the
// effective RoleSets unchanged. The duplicate user also proves request-level
// de-duplication rather than relying on a unique-index error.
func TestAPIRoleMembersCreateBindsEveryPrincipalOnceAndReturnsMembers(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "role-member-user", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	group := models.UserGroup{Name: "role-member-group", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&user).Error)
	require.NoError(t, srv.DB.Create(&group).Error)
	token := models.Token{Name: "role-member-token", Key: "sk-role-member", UserID: user.ID, Status: consts.StatusEnabled, APIRoleMode: models.APIRoleModeExplicit}
	require.NoError(t, srv.DB.Create(&token).Error)
	rolePublishes := 0
	roleSubscription, err := events.Subscribe(srv.Bus, events.APIRoleCreateTopic, func(_ context.Context, _ protocol.SyncedAPIRole) error {
		rolePublishes++
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = roleSubscription.Unsubscribe() })
	var invalidatedUsers, invalidatedGroups, invalidatedTokens []uint
	userSubscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidatedUsers = append(invalidatedUsers, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = userSubscription.Unsubscribe() })
	groupSubscription, err := events.Subscribe(srv.Bus, events.UserGroupAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetFetchResult) error {
		invalidatedGroups = append(invalidatedGroups, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = groupSubscription.Unsubscribe() })
	tokenSubscription, err := events.Subscribe(srv.Bus, events.TokenAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidatedTokens = append(invalidatedTokens, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tokenSubscription.Unsubscribe() })

	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "role-with-members", "name": "Role with members",
		"members": []map[string]any{
			{"principal_type": models.APIPrincipalUser, "principal_id": user.ID},
			{"principal_type": models.APIPrincipalUserGroup, "principal_id": group.ID},
			{"principal_type": models.APIPrincipalToken, "principal_id": token.ID},
			{"principal_type": models.APIPrincipalUser, "principal_id": user.ID},
		},
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())
	var role models.Role
	require.NoError(t, json.Unmarshal(created.Body.Bytes(), &role))
	require.Equal(t, 1, rolePublishes)
	require.Equal(t, []uint{user.ID}, invalidatedUsers)
	require.Equal(t, []uint{group.ID}, invalidatedGroups)
	require.Equal(t, []uint{token.ID}, invalidatedTokens)

	var bindings []models.RoleBinding
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Order("id ASC").Find(&bindings).Error)
	require.Equal(t, []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: group.ID, RoleID: role.ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID, RoleID: role.ID},
	}, roleBindingsWithoutIdentity(bindings))

	detail := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var body struct {
		Members []struct {
			PrincipalType models.APIPrincipalType `json:"principal_type"`
			PrincipalID   uint                    `json:"principal_id"`
		} `json:"members"`
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &body))
	require.Equal(t, []struct {
		PrincipalType models.APIPrincipalType `json:"principal_type"`
		PrincipalID   uint                    `json:"principal_id"`
	}{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID},
		{PrincipalType: models.APIPrincipalUserGroup, PrincipalID: group.ID},
		{PrincipalType: models.APIPrincipalToken, PrincipalID: token.ID},
	}, body.Members)

	list := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/api-roles?search=role-with-members", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	var page struct {
		Data []struct {
			Members []struct {
				PrincipalType models.APIPrincipalType `json:"principal_type"`
				PrincipalID   uint                    `json:"principal_id"`
			} `json:"members"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(list.Body.Bytes(), &page))
	require.Len(t, page.Data, 1)
	require.Equal(t, body.Members, page.Data[0].Members)
}

// Break caught: validating members after the role row is committed would leave
// a partial role when an inherit-mode Token makes the member set invalid.
func TestAPIRoleMembersCreateInvalidTokenRollsBackWholeRole(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "role-member-rollback", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	inherit := models.Token{Name: "inherit-role-member", Key: "sk-inherit-role-member", UserID: user.ID, Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&inherit).Error)

	response := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "rolled-back-members", "name": "Rolled back members",
		"permissions": []map[string]any{{"resource": models.APIResourceService, "action": models.APIPermissionInvoke}},
		"members": []map[string]any{
			{"principal_type": models.APIPrincipalUser, "principal_id": user.ID},
			{"principal_type": models.APIPrincipalToken, "principal_id": inherit.ID},
		},
	})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	var roles, links, bindings int64
	require.NoError(t, srv.DB.Model(&models.Role{}).Where("`key` = ?", "rolled-back-members").Count(&roles).Error)
	require.NoError(t, srv.DB.Model(&models.RolePermission{}).Joins("JOIN roles ON roles.id = role_permissions.role_id").Where("roles.`key` = ?", "rolled-back-members").Count(&links).Error)
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Joins("JOIN roles ON roles.id = role_bindings.role_id").Where("roles.`key` = ?", "rolled-back-members").Count(&bindings).Error)
	require.Zero(t, roles)
	require.Zero(t, links)
	require.Zero(t, bindings)
}

// Break caught: replacing only permissions, or publishing only the new member,
// leaves stale RoleSets for removed principals. Repeating a principal on both
// sides must still produce one invalidation for that principal.
func TestAPIRoleMembersUpdateAtomicallyReplacesAndInvalidatesOldAndNew(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	users := []models.User{
		{Username: "role-member-old", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "role-member-kept", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "role-member-new", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
	}
	for i := range users {
		require.NoError(t, srv.DB.Create(&users[i]).Error)
	}
	role := models.Role{Key: "replace-members", Name: "Replace members", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	for _, user := range users[:2] {
		require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)
	}

	var invalidated []uint
	userSubscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = userSubscription.Unsubscribe() })
	rolePublished := 0
	roleSubscription, err := events.Subscribe(srv.Bus, events.APIRoleUpdateTopic, func(_ context.Context, event protocol.SyncedAPIRole) error {
		require.Equal(t, role.ID, event.ID)
		rolePublished++
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = roleSubscription.Unsubscribe() })

	updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": role.Key, "name": role.Name,
		"permissions": []map[string]any{{"resource": models.APIResourceService, "action": models.APIPermissionInvoke}},
		"members": []map[string]any{
			{"principal_type": models.APIPrincipalUser, "principal_id": users[1].ID},
			{"principal_type": models.APIPrincipalUser, "principal_id": users[2].ID},
			{"principal_type": models.APIPrincipalUser, "principal_id": users[2].ID},
		},
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	require.Equal(t, 1, rolePublished)
	require.ElementsMatch(t, []uint{users[0].ID, users[1].ID, users[2].ID}, invalidated)
	require.Len(t, invalidated, 3)
	var bindings []models.RoleBinding
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Order("id ASC").Find(&bindings).Error)
	require.Equal(t, []models.RoleBinding{
		{PrincipalType: models.APIPrincipalUser, PrincipalID: users[1].ID, RoleID: role.ID},
		{PrincipalType: models.APIPrincipalUser, PrincipalID: users[2].ID, RoleID: role.ID},
	}, roleBindingsWithoutIdentity(bindings))
}

// Break caught: treating an empty member list like an omitted update leaves the
// final binding in storage or serializes the empty GET/LIST result as null.
func TestAPIRoleMembersUpdateToEmptyDeletesLastBindingAndReturnsEmptyArrays(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "role-empty-members", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	role := models.Role{Key: "empty-members", Name: "Empty members", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}).Error)

	updated := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": role.Key, "name": role.Name, "members": []map[string]any{},
	})
	require.Equal(t, http.StatusOK, updated.Code, updated.Body.String())
	var bindingCount int64
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Where("role_id = ?", role.ID).Count(&bindingCount).Error)
	require.Zero(t, bindingCount)

	assertEmptyMembers := func(responseBody []byte, fromList bool) {
		t.Helper()
		if fromList {
			var page struct {
				Data []struct {
					Members []map[string]any `json:"members"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(responseBody, &page))
			require.Len(t, page.Data, 1)
			require.NotNil(t, page.Data[0].Members)
			require.Empty(t, page.Data[0].Members)
			return
		}
		var detail struct {
			Members []map[string]any `json:"members"`
		}
		require.NoError(t, json.Unmarshal(responseBody, &detail))
		require.NotNil(t, detail.Members)
		require.Empty(t, detail.Members)
	}
	detail := reqHelper(srv, adminJWT, http.MethodGet, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), nil)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	assertEmptyMembers(detail.Body.Bytes(), false)
	list := reqHelper(srv, adminJWT, http.MethodGet, "/api/admin/api-roles?search=empty-members", nil)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	assertEmptyMembers(list.Body.Bytes(), true)
}

// Break caught: deleting old links before every replacement member validates
// commits a partially updated role when one requested principal is illegal.
func TestAPIRoleMembersUpdateInvalidMemberRollsBackRolePermissionsAndBindings(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "role-update-member-rollback", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)
	inherit := models.Token{Name: "update-inherit-member", Key: "sk-update-inherit-member", UserID: user.ID, Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&inherit).Error)
	role := models.Role{Key: "members-before", Name: "Members before", Status: consts.StatusEnabled}
	permission := models.Permission{Resource: models.APIResourceService, ResourceID: 99, Action: models.APIPermissionInvoke}
	require.NoError(t, srv.DB.Create(&role).Error)
	require.NoError(t, srv.DB.Create(&permission).Error)
	link := models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}
	binding := models.RoleBinding{PrincipalType: models.APIPrincipalUser, PrincipalID: user.ID, RoleID: role.ID}
	require.NoError(t, srv.DB.Create(&link).Error)
	require.NoError(t, srv.DB.Create(&binding).Error)

	response := reqHelper(srv, adminJWT, http.MethodPut, fmt.Sprintf("/api/admin/api-roles/%d", role.ID), map[string]any{
		"key": "members-after", "name": "Members after",
		"permissions": []map[string]any{{"resource": models.APIResourceService, "action": models.APIPermissionInvoke}},
		"members":     []map[string]any{{"principal_type": models.APIPrincipalToken, "principal_id": inherit.ID}},
	})
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	var stored models.Role
	require.NoError(t, srv.DB.First(&stored, role.ID).Error)
	require.Equal(t, "members-before", stored.Key)
	var storedLinks []models.RolePermission
	var storedBindings []models.RoleBinding
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Find(&storedLinks).Error)
	require.NoError(t, srv.DB.Where("role_id = ?", role.ID).Find(&storedBindings).Error)
	require.Equal(t, []models.RolePermission{link}, storedLinks)
	require.Equal(t, []models.RoleBinding{binding}, storedBindings)
}

// Break caught: exposing control-plane read/manage actions through ordinary
// Roles lets the UI create grants that Agents cannot enforce consistently.
func TestAPIRoleCapabilityPermissionsUseOnlyLegalMatrix(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	service := models.APIService{Slug: "role-capability", Name: "Role capability", Status: consts.StatusEnabled}
	require.NoError(t, srv.DB.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "role-capability"}
	require.NoError(t, srv.DB.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "invoke"}
	require.NoError(t, srv.DB.Create(&route).Error)
	capabilities := []map[string]any{
		{"resource": models.APIResourceService, "action": models.APIPermissionInvoke},
		{"resource": models.APIResourceService, "resource_id": service.ID, "action": models.APIPermissionInvoke},
		{"resource": models.APIResourceRoute, "resource_id": route.ID, "action": models.APIPermissionInvoke},
	}
	created := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "ui-capabilities", "name": "UI capabilities", "permissions": capabilities,
	})
	require.Equal(t, http.StatusCreated, created.Code, created.Body.String())

	for key, permission := range map[string]map[string]any{
		"route-global":     {"resource": models.APIResourceRoute, "resource_id": 0, "action": models.APIPermissionInvoke},
		"service-read":     {"resource": models.APIResourceService, "action": "read"},
		"service-manage":   {"resource": models.APIResourceService, "action": "manage"},
		"route-manage":     {"resource": models.APIResourceRoute, "action": "manage"},
		"request-log-read": {"resource": "api_request_log", "action": "read"},
		"upstream-manage":  {"resource": "api_upstream", "action": "manage"},
		"empty-resource":   {"resource": "", "action": models.APIPermissionInvoke},
		"empty-action":     {"resource": models.APIResourceService, "action": ""},
	} {
		response := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
			"key": key, "name": key, "permissions": []map[string]any{permission},
		})
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	}
}

// Break caught: treating publication as part of the database transaction, or
// returning after the first publisher error, can either roll back committed
// state incorrectly or skip the member RoleSet invalidation entirely.
func TestAPIRoleMembersPublisherFailureHappensAfterCommitAndStillInvalidates(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	user := models.User{Username: "member-publish-failure", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1}
	require.NoError(t, srv.DB.Create(&user).Error)

	rolePublishes := 0
	roleSubscription, err := events.Subscribe(srv.Bus, events.APIRoleCreateTopic, func(_ context.Context, _ protocol.SyncedAPIRole) error {
		rolePublishes++
		return errors.New("force role publisher failure")
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = roleSubscription.Unsubscribe() })
	var invalidated []uint
	userSubscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = userSubscription.Unsubscribe() })

	response := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "committed-publish-failure", "name": "Committed publish failure",
		"members": []map[string]any{{"principal_type": models.APIPrincipalUser, "principal_id": user.ID}},
	})
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Equal(t, 1, rolePublishes)
	require.Equal(t, []uint{user.ID}, invalidated)
	var role models.Role
	require.NoError(t, srv.DB.Where("`key` = ?", "committed-publish-failure").First(&role).Error)
	var bindings int64
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Where("role_id = ? AND principal_type = ? AND principal_id = ?", role.ID, models.APIPrincipalUser, user.ID).Count(&bindings).Error)
	require.Equal(t, int64(1), bindings)
}

// Break caught: returning from the first failed principal publication skips all
// later invalidations even though every member binding is already committed.
func TestAPIRoleMembersPublisherFailureStillAttemptsRemainingPrincipals(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("admin", "admin123"))
	adminJWT := loginAsAdmin(t, srv, "admin", "admin123")
	users := []models.User{
		{Username: "member-publish-first", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
		{Username: "member-publish-second", Role: consts.RoleUser, Status: consts.StatusEnabled, GroupID: 1},
	}
	for i := range users {
		require.NoError(t, srv.DB.Create(&users[i]).Error)
	}
	var invalidated []uint
	subscription, err := events.Subscribe(srv.Bus, events.UserAPIRolesSyncedTopic, func(_ context.Context, event protocol.APIRoleSetInvalidate) error {
		invalidated = append(invalidated, event.PrincipalID)
		if event.PrincipalID == users[0].ID {
			return errors.New("force first principal publisher failure")
		}
		return nil
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = subscription.Unsubscribe() })

	response := reqHelper(srv, adminJWT, http.MethodPost, "/api/admin/api-roles", map[string]any{
		"key": "continue-principal-publish", "name": "Continue principal publish",
		"members": []map[string]any{
			{"principal_type": models.APIPrincipalUser, "principal_id": users[0].ID},
			{"principal_type": models.APIPrincipalUser, "principal_id": users[1].ID},
		},
	})
	require.Equal(t, http.StatusInternalServerError, response.Code, response.Body.String())
	require.Equal(t, []uint{users[0].ID, users[1].ID}, invalidated)
	var role models.Role
	require.NoError(t, srv.DB.Where("`key` = ?", "continue-principal-publish").First(&role).Error)
	var bindingCount int64
	require.NoError(t, srv.DB.Model(&models.RoleBinding{}).Where("role_id = ?", role.ID).Count(&bindingCount).Error)
	require.Equal(t, int64(2), bindingCount)
}

func roleBindingsWithoutIdentity(bindings []models.RoleBinding) []models.RoleBinding {
	result := make([]models.RoleBinding, len(bindings))
	for i, binding := range bindings {
		result[i] = models.RoleBinding{PrincipalType: binding.PrincipalType, PrincipalID: binding.PrincipalID, RoleID: binding.RoleID}
	}
	return result
}
