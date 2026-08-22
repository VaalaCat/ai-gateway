package cache

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestAPIIndexRequiresEveryFullCacheBeforeLookup(t *testing.T) {
	index := NewAPIIndex()
	_, err := index.FindServiceRoute("weather", "forecast")
	require.ErrorIs(t, err, ErrAPICacheNotReady)

	require.NoError(t, index.ReplaceServices(nil))
	require.NoError(t, index.ReplaceRoutes(nil))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles(nil))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))

	_, err = index.FindServiceRoute("weather", "forecast")
	require.ErrorIs(t, err, ErrAPIServiceNotFound)
}

func TestAPIIndexFindServiceRouteFailsClosedAfterServiceDelete(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 1, Slug: "forecast", Status: 1}},
		nil,
		nil,
		nil,
	)

	got, err := index.FindServiceRoute("weather", "forecast")
	require.NoError(t, err)
	require.Equal(t, uint(1), got.Service.ID)
	require.Equal(t, uint(2), got.Route.ID)

	require.NoError(t, index.ApplyService("delete", protocol.SyncedAPIService{ID: 1, Slug: "weather"}))
	_, err = index.FindServiceRoute("weather", "forecast")
	require.ErrorIs(t, err, ErrAPIServiceNotFound)
}

func TestAPIIndexFindsEmptySlugRootRouteAndRejectsDuplicate(t *testing.T) {
	services := []protocol.SyncedAPIService{
		{ID: 1, Slug: "users", Status: 1},
		{ID: 2, Slug: "orders", Status: 1},
	}
	rootRoutes := []protocol.SyncedAPIRoute{
		{ID: 10, ServiceID: 1, BackendID: 11, Slug: "", Status: 1},
		{ID: 20, ServiceID: 2, BackendID: 21, Slug: "", Status: 1},
	}
	index := readyAPIIndex(t, services, rootRoutes, nil, nil, nil)

	got, err := index.FindServiceRoute("users", "")
	require.NoError(t, err)
	require.Equal(t, uint(10), got.Route.ID)
	require.Empty(t, got.Route.Slug)

	err = index.ReplaceRoutes([]protocol.SyncedAPIRoute{
		rootRoutes[0],
		{ID: 11, ServiceID: 1, BackendID: 12, Slug: "", Status: 1},
	})
	require.Error(t, err)
	got, findErr := index.FindServiceRoute("users", "")
	require.NoError(t, findErr)
	require.Equal(t, uint(10), got.Route.ID, "a rejected snapshot must not replace the prior root route")
}

func TestAPIIndexFindServiceRouteByIDReturnsOnlyMatchingFrozenProjection(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}, {ID: 4, Slug: "maps", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 1, Slug: "forecast", Protocols: []string{"http"}, Status: 1}},
		nil, nil, nil,
	)

	got, err := index.FindServiceRouteByID(1, 2)
	require.NoError(t, err)
	require.Equal(t, uint(1), got.Service.ID)
	require.Equal(t, uint(2), got.Route.ID)

	_, err = index.FindServiceRouteByID(4, 2)
	require.ErrorIs(t, err, ErrAPIRouteNotFound)
	_, err = index.FindServiceRouteByID(0, 2)
	require.ErrorIs(t, err, ErrAPIServiceNotFound)
	_, err = index.FindServiceRouteByID(1, 0)
	require.ErrorIs(t, err, ErrAPIRouteNotFound)
}

func TestAPIIndexUpstreamReadFailsClosedWhileCacheNotReady(t *testing.T) {
	index := NewAPIIndex()
	require.NoError(t, index.ReplaceServices([]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}}))
	require.NoError(t, index.ReplaceUpstreams([]protocol.SyncedAPIUpstream{{ID: 2, BackendID: 1, Name: "primary", Status: 1}}))

	require.Empty(t, index.UpstreamsForBackend(1))
}

func TestAPIIndexRejectsBrokenSnapshotReferencesWithoutPublishing(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 1, Slug: "forecast", Status: 1}},
		nil,
		nil,
		nil,
	)

	err := index.ReplaceRoutes([]protocol.SyncedAPIRoute{{ID: 3, ServiceID: 999, Slug: "broken", Status: 1}})
	require.Error(t, err)
	got, findErr := index.FindServiceRoute("weather", "forecast")
	require.NoError(t, findErr)
	require.Equal(t, uint(2), got.Route.ID)

	err = index.ReplaceUpstreams([]protocol.SyncedAPIUpstream{{ID: 4, BackendID: 0, Name: "broken", Status: 1}})
	require.Error(t, err)
	got, findErr = index.FindServiceRoute("weather", "forecast")
	require.NoError(t, findErr)
	require.Equal(t, uint(2), got.Route.ID)
}

func TestAPIIndexAllowsOnlyExplicitInvokeGrants(t *testing.T) {
	index := readyAPIIndex(t, nil, nil, nil, []protocol.SyncedAPIRole{
		{ID: 1, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 9, Action: "invoke"}}},
		{ID: 2, Permissions: []protocol.APIPermissionGrant{{Resource: "api_route", ResourceID: 8, Action: "invoke"}}},
		{ID: 3, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 0, Action: "invoke"}}},
		{ID: 4, Permissions: []protocol.APIPermissionGrant{{Resource: "api_route", ResourceID: 0, Action: "invoke"}}},
		{ID: 5, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 9, Action: "manage"}, {Resource: "api_route", ResourceID: 8, Action: "read"}}},
	}, nil)

	require.True(t, index.AllowsInvoke([]uint{1}, 9, 100))
	require.True(t, index.AllowsInvoke([]uint{2}, 100, 8))
	require.True(t, index.AllowsInvoke([]uint{3}, 100, 100))
	require.False(t, index.AllowsInvoke([]uint{4}, 100, 100))
	require.False(t, index.AllowsInvoke([]uint{5}, 9, 8))
	require.False(t, index.AllowsInvoke([]uint{404}, 9, 8))
	require.False(t, index.AllowsInvoke(nil, 9, 8))
}

func TestAPIIndexCopiesNestedProtocolData(t *testing.T) {
	route := protocol.SyncedAPIRoute{ID: 2, ServiceID: 1, BackendID: 9, Slug: "forecast", Protocols: []string{"http"}, AllowedMethods: []string{"GET"}, Status: 1}
	upstream := protocol.SyncedAPIUpstream{ID: 3, BackendID: 9, HeaderOverride: map[string]string{"X-Test": "before"}, Status: 1}
	role := protocol.SyncedAPIRole{ID: 4, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 1, Action: "invoke"}}}
	group := protocol.APIRoleSetFetchResult{PrincipalID: 5, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{4}}}
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{route},
		[]protocol.SyncedAPIUpstream{upstream},
		[]protocol.SyncedAPIRole{role},
		[]protocol.APIRoleSetFetchResult{group},
	)

	route.Protocols[0] = "mutated"
	route.AllowedMethods[0] = "DELETE"
	upstream.HeaderOverride["X-Test"] = "mutated"
	role.Permissions[0].Action = "manage"
	group.RoleSet.RoleIDs[0] = 999

	got, err := index.FindServiceRoute("weather", "forecast")
	require.NoError(t, err)
	require.Equal(t, []string{"http"}, got.Route.Protocols)
	require.Equal(t, []string{"GET"}, got.Route.AllowedMethods)
	require.Equal(t, "before", index.UpstreamsForBackend(9)[0].HeaderOverride["X-Test"])
	require.True(t, index.AllowsInvoke([]uint{4}, 1, 2))
	require.Equal(t, []uint{4}, index.UserGroupRoleSet(5).RoleSet.RoleIDs)
}

func TestAPIIndexScopesUpstreamsByBackendAndClonesReadResults(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{
			{ID: 2, ServiceID: 1, BackendID: 10, Slug: "current", Status: 1},
			{ID: 3, ServiceID: 1, BackendID: 20, Slug: "archive", Status: 1},
		},
		[]protocol.SyncedAPIUpstream{
			{ID: 101, BackendID: 10, HeaderOverride: map[string]string{"X-Pool": "current"}, Status: 1},
			{ID: 201, BackendID: 20, HeaderOverride: map[string]string{"X-Pool": "archive"}, Status: 1},
		}, nil, nil,
	)

	current := index.UpstreamsForBackend(10)
	require.Equal(t, []uint{101}, []uint{current[0].ID})
	require.Empty(t, index.UpstreamsForBackend(999))
	current[0].HeaderOverride["X-Pool"] = "mutated"
	require.Equal(t, "current", index.UpstreamsForBackend(10)[0].HeaderOverride["X-Pool"])
}

func TestAPIIndexRejectsInvalidBackendUpstreamSnapshotWithoutPublishing(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 10, Slug: "forecast", Status: 1}},
		[]protocol.SyncedAPIUpstream{{ID: 101, BackendID: 10, Status: 1}}, nil, nil,
	)

	err := index.ReplaceUpstreams([]protocol.SyncedAPIUpstream{{ID: 201, BackendID: 0, Status: 1}})
	require.Error(t, err)
	err = index.ReplaceUpstreams([]protocol.SyncedAPIUpstream{{ID: 201, BackendID: 20, Status: 1}, {ID: 201, BackendID: 20, Status: 1}})
	require.Error(t, err)
	require.Equal(t, []uint{101}, []uint{index.UpstreamsForBackend(10)[0].ID})
}

func TestAPIIndexApplyUpstreamClonesCallerOwnedNestedData(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 10, Slug: "forecast", Status: 1}},
		[]protocol.SyncedAPIUpstream{{ID: 101, BackendID: 10, HeaderOverride: map[string]string{"X-Pool": "before"}, Status: 1}}, nil, nil,
	)

	updated := protocol.SyncedAPIUpstream{ID: 101, BackendID: 10, HeaderOverride: map[string]string{"X-Pool": "after"}, Status: 1}
	require.NoError(t, index.ApplyUpstream(events.ActionUpdate, updated))
	updated.HeaderOverride["X-Pool"] = "mutated"
	require.Equal(t, "after", index.UpstreamsForBackend(10)[0].HeaderOverride["X-Pool"])
}

func TestAPIIndexRejectsZeroBackendIncrementalUpstreamWithoutPublishing(t *testing.T) {
	for _, test := range []struct {
		name   string
		action string
		id     uint
	}{
		{name: "create", action: events.ActionCreate, id: 201},
		{name: "update", action: events.ActionUpdate, id: 101},
	} {
		t.Run(test.name, func(t *testing.T) {
			index := readyAPIIndex(t,
				[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
				[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 10, Slug: "forecast", Status: 1}},
				[]protocol.SyncedAPIUpstream{{ID: 101, BackendID: 10, Name: "primary", Status: 1}}, nil, nil,
			)

			err := index.ApplyUpstream(test.action, protocol.SyncedAPIUpstream{ID: test.id, BackendID: 0, Name: "invalid", Status: 1})
			require.Error(t, err)
			values := index.UpstreamsForBackend(10)
			require.Len(t, values, 1)
			require.Equal(t, "primary", values[0].Name)
			require.Empty(t, index.UpstreamsForBackend(0))
		})
	}
}

func TestAPIIndexApplyUpstreamDeleteDoesNotRequireBackend(t *testing.T) {
	index := readyAPIIndex(t,
		[]protocol.SyncedAPIService{{ID: 1, Slug: "weather", Status: 1}},
		[]protocol.SyncedAPIRoute{{ID: 2, ServiceID: 1, BackendID: 10, Slug: "forecast", Status: 1}},
		[]protocol.SyncedAPIUpstream{{ID: 101, BackendID: 10, Status: 1}}, nil, nil,
	)

	require.NoError(t, index.ApplyUpstream(events.ActionDelete, protocol.SyncedAPIUpstream{ID: 101}))
	require.Empty(t, index.UpstreamsForBackend(10))
}

func TestAPIIndexUserGroupRoleSetDeleteRemovesOldGrant(t *testing.T) {
	index := readyAPIIndex(t, nil, nil, nil, nil, []protocol.APIRoleSetFetchResult{{
		PrincipalID: 5, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{4}},
	}})
	require.True(t, index.UserGroupRoleSet(5).Exists)
	require.NoError(t, index.ApplyUserGroupRoleSet(events.ActionDelete, protocol.APIRoleSetFetchResult{PrincipalID: 5}))
	require.False(t, index.UserGroupRoleSet(5).Exists)
}

func readyAPIIndex(
	t *testing.T,
	services []protocol.SyncedAPIService,
	routes []protocol.SyncedAPIRoute,
	upstreams []protocol.SyncedAPIUpstream,
	roles []protocol.SyncedAPIRole,
	groups []protocol.APIRoleSetFetchResult,
) *APIIndex {
	t.Helper()
	index := NewAPIIndex()
	require.NoError(t, index.ReplaceServices(services))
	require.NoError(t, index.ReplaceRoutes(routes))
	require.NoError(t, index.ReplaceUpstreams(upstreams))
	require.NoError(t, index.ReplaceRoles(roles))
	require.NoError(t, index.ReplaceUserGroupRoleSets(groups))
	require.False(t, errors.Is(index.RequireReady(), ErrAPICacheNotReady))
	return index
}
