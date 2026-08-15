package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAPIFullSyncRegistryReturnsReadyEmptySnapshots(t *testing.T) {
	q, _ := setupSyncDB(t)
	registry := NewAPIFullSyncRegistry(nil, func() int64 { return 17 })
	for _, entity := range []string{
		events.EntityAPIService, events.EntityAPIRoute, events.EntityAPIUpstream,
		events.EntityAPIRole, events.EntityUserGroupAPIRoleSet,
	} {
		t.Run(entity, func(t *testing.T) {
			handler, ok := registry.Resolve(entity)
			require.True(t, ok)
			got, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{
				Entity: entity, PageSize: protocol.FullSyncMaxPageSize,
			})
			require.NoError(t, err)
			require.True(t, got.Keyset)
			require.Equal(t, int64(17), got.BaseVersion)
			require.Equal(t, protocol.APIFullSyncSnapshotContractV1, got.SnapshotContract)
			if entity == events.EntityUserGroupAPIRoleSet {
				require.JSONEq(t, `[{"principal_id":1,"exists":true,"role_set":{"role_ids":[]}}]`, string(got.Items))
				require.Equal(t, int64(1), got.Total)
			} else {
				require.JSONEq(t, `[]`, string(got.Items))
				require.Zero(t, got.Total)
			}
			require.False(t, got.HasMore)
		})
	}
	_, ok := registry.Resolve("unknown")
	require.False(t, ok)
	require.Len(t, registry.handlers, 5, "backend remains a master-only entity and is not synced")
}

func TestAPIFullSyncIsKeysetPagedAndSnapshotConsistent(t *testing.T) {
	q, m := setupSyncDB(t)
	for i := 1; i <= 501; i++ {
		require.NoError(t, m.APIService().Create(&models.APIService{
			Slug: fmt.Sprintf("service-%03d", i), Name: fmt.Sprintf("Service %d", i),
			PricePerCall: int64(i % 2), Status: consts.StatusEnabled,
		}))
	}
	version := int64(23)
	handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return version }).Resolve(events.EntityAPIService)
	require.True(t, ok)
	first, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{
		Entity: events.EntityAPIService, PageSize: 500,
	})
	require.NoError(t, err)
	require.Equal(t, int64(501), first.Total)
	require.Len(t, decodeSyncedAPIServices(t, first.Items), 500)
	require.True(t, first.HasMore)
	require.Equal(t, uint(501), first.SnapshotMaxID)
	require.Equal(t, int64(23), first.BaseVersion)

	// A deletion behind the cursor cannot shift the second page; an insertion
	// beyond SnapshotMaxID belongs to versioned push replay, not this snapshot.
	require.NoError(t, m.APIService().Delete(1))
	require.NoError(t, m.APIService().Create(&models.APIService{
		Slug: "service-late", Name: "Late", Status: consts.StatusEnabled,
	}))
	version = 24
	second, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{
		Entity: events.EntityAPIService, PageSize: 500, AfterID: first.LastID,
		SnapshotMaxID: first.SnapshotMaxID, BaseVersion: first.BaseVersion,
	})
	require.NoError(t, err)
	items := decodeSyncedAPIServices(t, second.Items)
	require.Len(t, items, 1)
	require.Equal(t, uint(501), items[0].ID)
	require.False(t, second.HasMore)
	require.Equal(t, int64(500), second.Total)
	require.Equal(t, int64(23), second.BaseVersion)
	require.Equal(t, uint(501), second.SnapshotMaxID)
	require.Equal(t, int64(24), second.Version)
	for _, item := range items {
		body, marshalErr := json.Marshal(item)
		require.NoError(t, marshalErr)
		require.NotContains(t, string(body), "price")
	}
}

func decodeSyncedAPIServices(t *testing.T, data []byte) []protocol.SyncedAPIService {
	t.Helper()
	var result []protocol.SyncedAPIService
	require.NoError(t, json.Unmarshal(data, &result))
	return result
}

func TestAPIFullSyncUpstreamProjectionFailureClosesSnapshot(t *testing.T) {
	q, m := setupSyncDB(t)
	require.NoError(t, m.APIService().Create(&models.APIService{Slug: "svc", Name: "Service", Status: 1}))
	require.NoError(t, m.APIBackend().Create(&models.APIBackend{APIServiceID: 1, Name: "primary"}))
	require.NoError(t, m.APIUpstream().Create(&models.APIUpstream{
		BackendID: 1, Name: "bad", BaseURL: "https://example.com", Weight: 1,
		AuthType: models.APIUpstreamAuthBearer, CredentialCiphertext: "bad", Status: 1,
	}))
	handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return 1 }).Resolve(events.EntityAPIUpstream)
	require.True(t, ok)
	_, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
	require.Error(t, err)
}

func TestAPIFullSyncRoleAndGroupUseCompiledAuthorizationProjection(t *testing.T) {
	q, m := setupSyncDB(t)
	role := models.Role{Key: "weather-manager", Name: "Weather manager", Status: consts.StatusEnabled}
	require.NoError(t, m.APIRBAC().CreateRole(&role))
	permission := models.Permission{
		Resource: models.APIResourceService, ResourceID: 7, Action: models.APIPermissionInvoke,
	}
	require.NoError(t, m.APIRBAC().CreatePermission(&permission))
	require.NoError(t, m.APIRBAC().CreateRolePermission(&models.RolePermission{
		RoleID: role.ID, PermissionID: permission.ID,
	}))
	require.NoError(t, m.APIRBAC().CreateRoleBinding(&models.RoleBinding{
		PrincipalType: models.APIPrincipalUserGroup, PrincipalID: 1, RoleID: role.ID,
	}))
	registry := NewAPIFullSyncRegistry(nil, func() int64 { return 5 })

	roleHandler, ok := registry.Resolve(events.EntityAPIRole)
	require.True(t, ok)
	rolePage, err := roleHandler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
	require.NoError(t, err)
	var roles []protocol.SyncedAPIRole
	require.NoError(t, json.Unmarshal(rolePage.Items, &roles))
	require.Equal(t, []protocol.SyncedAPIRole{{
		ID: role.ID, Name: role.Name,
		Permissions: []protocol.APIPermissionGrant{
			{Resource: "api_service", ResourceID: 7, Action: "invoke"},
		},
	}}, roles)

	groupHandler, ok := registry.Resolve(events.EntityUserGroupAPIRoleSet)
	require.True(t, ok)
	groupPage, err := groupHandler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
	require.NoError(t, err)
	var groups []protocol.APIRoleSetFetchResult
	require.NoError(t, json.Unmarshal(groupPage.Items, &groups))
	require.Equal(t, []protocol.APIRoleSetFetchResult{{
		PrincipalID: 1, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{role.ID}},
	}}, groups)
}

func TestUserGroupAPIRoleSetFullSyncUsesBoundedBindingQueryChunks(t *testing.T) {
	for _, tc := range []struct {
		groupCount     int
		bindingQueries int64
	}{
		{groupCount: 1, bindingQueries: 1},
		{groupCount: 400, bindingQueries: 1},
		{groupCount: 401, bindingQueries: 2},
		{groupCount: 500, bindingQueries: 2},
	} {
		t.Run(fmt.Sprintf("%d groups", tc.groupCount), func(t *testing.T) {
			q, _, db := setupSyncDBWithDatabase(t)
			if tc.groupCount > 1 {
				groups := make([]models.UserGroup, tc.groupCount-1)
				for i := range groups {
					groups[i] = models.UserGroup{Name: fmt.Sprintf("group-%03d", i+2)}
				}
				require.NoError(t, db.CreateInBatches(groups, 100).Error)
			}

			var bindingQueries atomic.Int64
			callbackName := fmt.Sprintf("test:count_role_bindings_%d", tc.groupCount)
			require.NoError(t, db.Callback().Query().Before("gorm:query").Register(callbackName, func(tx *gorm.DB) {
				if tx.Statement.Table == "role_bindings" {
					bindingQueries.Add(1)
				}
			}))
			t.Cleanup(func() { _ = db.Callback().Query().Remove(callbackName) })

			handler, ok := NewAPIFullSyncRegistry(nil, func() int64 { return 11 }).Resolve(events.EntityUserGroupAPIRoleSet)
			require.True(t, ok)
			page, err := handler.FullSync(context.Background(), q, protocol.FullSyncRequest{PageSize: 500})
			require.NoError(t, err)
			var roleSets []protocol.APIRoleSetFetchResult
			require.NoError(t, json.Unmarshal(page.Items, &roleSets))
			require.Len(t, roleSets, tc.groupCount)
			require.Equal(t, tc.bindingQueries, bindingQueries.Load())
			for _, roleSet := range roleSets {
				require.True(t, roleSet.Exists)
				require.NotNil(t, roleSet.RoleSet.RoleIDs)
				require.Empty(t, roleSet.RoleSet.RoleIDs)
			}
		})
	}
}

func TestProjectUserGroupRoleSetsSortsAndDeduplicatesBindings(t *testing.T) {
	groups := []models.UserGroup{{ID: 7}, {ID: 8}}
	bindings := []models.RoleBinding{
		{PrincipalID: 7, RoleID: 5},
		{PrincipalID: 8, RoleID: 4},
		{PrincipalID: 7, RoleID: 2},
		{PrincipalID: 7, RoleID: 5},
	}
	require.Equal(t, []protocol.APIRoleSetFetchResult{
		{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{2, 5}}},
		{PrincipalID: 8, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{4}}},
	}, projectUserGroupRoleSets(groups, bindings))
}
