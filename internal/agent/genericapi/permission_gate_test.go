package genericapi

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type permissionFacts struct {
	tokens                     map[uint]*models.Token
	userRoles                  map[uint]*protocol.APIRoleSet
	tokenRoles                 map[uint]*protocol.APIRoleSet
	userErr                    error
	tokenErr                   error
	missingWithoutConfirmation bool
	userCalls                  int
	tokenCalls                 int
}

type roleSequenceClient struct {
	results []protocol.APIRoleSetFetchResult
	calls   int
}

func (c *roleSequenceClient) Call(context.Context, string, any) (json.RawMessage, error) {
	result := c.results[c.calls]
	c.calls++
	return json.Marshal(result)
}
func (*roleSequenceClient) OnNotification(string, app.NotificationHandler) {}
func (*roleSequenceClient) Notify(string, any) error                       { return nil }
func (*roleSequenceClient) Close() error                                   { return nil }
func (*roleSequenceClient) ReadLoop()                                      {}

type roleLoadClient struct {
	result protocol.APIRoleSetFetchResult
	err    error
	method string
	calls  int
}

func (c *roleLoadClient) Call(_ context.Context, method string, _ any) (json.RawMessage, error) {
	c.calls++
	c.method = method
	if c.err != nil {
		return nil, c.err
	}
	return json.Marshal(c.result)
}

func (*roleLoadClient) OnNotification(string, app.NotificationHandler) {}
func (*roleLoadClient) Notify(string, any) error                       { return nil }
func (*roleLoadClient) Close() error                                   { return nil }
func (*roleLoadClient) ReadLoop()                                      {}

func (f *permissionFacts) FindTokenByID(_ context.Context, id uint) (*models.Token, bool, error) {
	token, ok := f.tokens[id]
	return token, ok, nil
}
func (f *permissionFacts) FindUserAPIRoleSet(_ context.Context, id uint) (*protocol.APIRoleSet, bool, error) {
	f.userCalls++
	if f.userErr != nil {
		return nil, false, f.userErr
	}
	roleSet, ok := f.userRoles[id]
	if !ok {
		if f.missingWithoutConfirmation {
			return nil, false, nil
		}
		return nil, false, entitycache.ErrNotFound
	}
	return roleSet, true, nil
}
func (f *permissionFacts) FindTokenAPIRoleSet(_ context.Context, id uint) (*protocol.APIRoleSet, bool, error) {
	f.tokenCalls++
	if f.tokenErr != nil {
		return nil, false, f.tokenErr
	}
	roleSet, ok := f.tokenRoles[id]
	if !ok {
		if f.missingWithoutConfirmation {
			return nil, false, nil
		}
		return nil, false, entitycache.ErrNotFound
	}
	return roleSet, true, nil
}

func TestPermissionGateAllowsInheritedAndExplicitInvoke(t *testing.T) {
	index := permissionTestIndex(t)
	tests := []struct {
		name       string
		mode       models.APIRoleMode
		userRoles  []uint
		tokenRoles []uint
		serviceID  uint
		routeID    uint
	}{
		{name: "inherit service exact", mode: models.APIRoleModeInherit, userRoles: []uint{1}, serviceID: 9, routeID: 100},
		{name: "explicit route exact", mode: models.APIRoleModeExplicit, tokenRoles: []uint{2}, serviceID: 100, routeID: 8},
		{name: "inherit service wildcard", mode: models.APIRoleModeInherit, userRoles: []uint{3}, serviceID: 100, routeID: 100},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := &permissionFacts{
				tokens:     map[uint]*models.Token{5: {ID: 5, UserID: 7, APIRoleMode: test.mode}},
				userRoles:  map[uint]*protocol.APIRoleSet{7: {RoleIDs: test.userRoles}},
				tokenRoles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: test.tokenRoles}},
			}
			gate := NewPermissionGate(facts, facts, index)
			require.NoError(t, gate.AllowInvoke(context.Background(), 5, 7, test.serviceID, test.routeID))
			if test.mode == models.APIRoleModeInherit {
				require.Equal(t, 1, facts.userCalls)
				require.Zero(t, facts.tokenCalls)
			} else {
				require.Zero(t, facts.userCalls)
				require.Equal(t, 1, facts.tokenCalls)
			}
		})
	}
}

func TestPermissionGateSystemTokenStillRequiresExplicitRBAC(t *testing.T) {
	index := permissionTestIndex(t)
	t.Run("inherit denied without owner lookup", func(t *testing.T) {
		facts := &permissionFacts{
			tokens:    map[uint]*models.Token{5: {ID: 5, UserID: 0, APIRoleMode: models.APIRoleModeInherit}},
			userRoles: map[uint]*protocol.APIRoleSet{},
		}
		gate := NewPermissionGate(facts, facts, index)
		require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 0, 9, 8), ErrAPIForbidden)
		require.Zero(t, facts.userCalls)
		require.Zero(t, facts.tokenCalls)
	})
	t.Run("explicit grant allowed", func(t *testing.T) {
		facts := &permissionFacts{
			tokens:     map[uint]*models.Token{5: {ID: 5, UserID: 0, APIRoleMode: models.APIRoleModeExplicit}},
			tokenRoles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{1}}},
		}
		gate := NewPermissionGate(facts, facts, index)
		require.NoError(t, gate.AllowInvoke(context.Background(), 5, 0, 9, 8))
		require.Zero(t, facts.userCalls)
		require.Equal(t, 1, facts.tokenCalls)
	})
}

func TestPermissionGateRefetchesUserRoleSetAfterGroupInvalidation(t *testing.T) {
	tests := []struct {
		name       string
		firstRoles []uint
		firstErr   error
		nextRoles  []uint
		nextErr    error
	}{
		{name: "revoked allow becomes deny", firstRoles: []uint{1}, nextRoles: []uint{}, nextErr: ErrAPIForbidden},
		{name: "new grant changes deny to allow", firstRoles: []uint{}, firstErr: ErrAPIForbidden, nextRoles: []uint{1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &roleSequenceClient{results: []protocol.APIRoleSetFetchResult{
				{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: test.firstRoles}},
				{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: test.nextRoles}},
			}}
			store := cache.NewStore(client, config.AgentCacheConfig{})
			defer store.Close()
			store.SetToken(&models.Token{ID: 5, Key: "sk-group-role", UserID: 7, APIRoleMode: models.APIRoleModeInherit})
			gate := NewPermissionGate(store, store, permissionTestIndex(t))

			require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), test.firstErr)
			store.DeleteUserAPIRoleSet(7)
			require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), test.nextErr)
			require.Equal(t, 2, client.calls)
		})
	}
}

func TestPermissionGateDeniesMissingPositiveEmptyAndNonInvoke(t *testing.T) {
	index := permissionTestIndex(t)
	for _, test := range []struct {
		name  string
		roles map[uint]*protocol.APIRoleSet
	}{
		{name: "missing", roles: map[uint]*protocol.APIRoleSet{}},
		{name: "positive empty", roles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{}}}},
		{name: "manage and read", roles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{5}}}},
		{name: "route wildcard", roles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{4}}}},
		{name: "missing role id", roles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{404}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			facts := &permissionFacts{
				tokens:     map[uint]*models.Token{5: {ID: 5, UserID: 7, APIRoleMode: models.APIRoleModeExplicit}},
				tokenRoles: test.roles,
			}
			gate := NewPermissionGate(facts, facts, index)
			require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), ErrAPIForbidden)
		})
	}
}

func TestPermissionGateSeparatesUnavailableFactsFromForbidden(t *testing.T) {
	index := permissionTestIndex(t)
	tests := []struct {
		name   string
		tokens map[uint]*models.Token
		err    error
	}{
		{name: "missing token", tokens: map[uint]*models.Token{}},
		{name: "invalid mode", tokens: map[uint]*models.Token{5: {ID: 5, UserID: 7, APIRoleMode: "future"}}},
		{name: "user mismatch", tokens: map[uint]*models.Token{5: {ID: 5, UserID: 99, APIRoleMode: models.APIRoleModeExplicit}}},
		{name: "role rpc error", tokens: map[uint]*models.Token{5: {ID: 5, UserID: 7, APIRoleMode: models.APIRoleModeExplicit}}, err: errors.New("rpc unavailable")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			facts := &permissionFacts{tokens: test.tokens, tokenErr: test.err}
			gate := NewPermissionGate(facts, facts, index)
			require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), ErrPermissionFactsUnavailable)
		})
	}
}

// Break caught: a PermissionGate cache miss must invoke the existing RoleSet
// loader and fail closed. Confirmed absence is forbidden; transport failure is
// an unavailable authorization fact and neither may bypass the gate.
func TestPermissionGateLazyRoleSetLoadFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name       string
		mode       models.APIRoleMode
		result     protocol.APIRoleSetFetchResult
		loadErr    error
		wantMethod string
		wantErr    error
	}{
		{
			name: "inherit confirmed missing", mode: models.APIRoleModeInherit,
			result:     protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: false},
			wantMethod: consts.RPCSyncFetchUserAPIRoles, wantErr: ErrAPIForbidden,
		},
		{
			name: "explicit confirmed missing", mode: models.APIRoleModeExplicit,
			result:     protocol.APIRoleSetFetchResult{PrincipalID: 5, Exists: false},
			wantMethod: consts.RPCSyncFetchTokenAPIRoles, wantErr: ErrAPIForbidden,
		},
		{
			name: "inherit unavailable", mode: models.APIRoleModeInherit,
			loadErr:    errors.New("role RPC unavailable"),
			wantMethod: consts.RPCSyncFetchUserAPIRoles, wantErr: ErrPermissionFactsUnavailable,
		},
		{
			name: "explicit unavailable", mode: models.APIRoleModeExplicit,
			loadErr:    errors.New("role RPC unavailable"),
			wantMethod: consts.RPCSyncFetchTokenAPIRoles, wantErr: ErrPermissionFactsUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &roleLoadClient{result: test.result, err: test.loadErr}
			store := cache.NewStore(client, config.AgentCacheConfig{})
			defer store.Close()
			store.SetToken(&models.Token{
				ID: 5, Key: "sk-lazy-role-set", UserID: 7, APIRoleMode: test.mode,
			})
			gate := NewPermissionGate(store, store, permissionTestIndex(t))

			require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), test.wantErr)
			require.Equal(t, 1, client.calls)
			require.Equal(t, test.wantMethod, client.method)
		})
	}
}

func TestPermissionGateUnconfirmedLocalMissIsUnavailable(t *testing.T) {
	facts := &permissionFacts{
		tokens:                     map[uint]*models.Token{5: {ID: 5, UserID: 7, APIRoleMode: models.APIRoleModeExplicit}},
		missingWithoutConfirmation: true,
	}
	gate := NewPermissionGate(facts, facts, permissionTestIndex(t))
	require.ErrorIs(t, gate.AllowInvoke(context.Background(), 5, 7, 9, 8), ErrPermissionFactsUnavailable)
}

type blockingPermissionFacts struct {
	permissionFacts
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *blockingPermissionFacts) FindTokenAPIRoleSet(
	ctx context.Context,
	id uint,
) (*protocol.APIRoleSet, bool, error) {
	f.once.Do(func() { close(f.entered) })
	select {
	case <-ctx.Done():
		return nil, false, context.Cause(ctx)
	case <-f.release:
	}
	return f.permissionFacts.FindTokenAPIRoleSet(ctx, id)
}

func TestPermissionGateRechecksReadinessWithFinalGrantSnapshot(t *testing.T) {
	index := permissionTestIndex(t)
	facts := &blockingPermissionFacts{
		permissionFacts: permissionFacts{
			tokens: map[uint]*models.Token{5: {
				ID: 5, UserID: 7, APIRoleMode: models.APIRoleModeExplicit,
			}},
			tokenRoles: map[uint]*protocol.APIRoleSet{5: {RoleIDs: []uint{1}}},
		},
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	gate := NewPermissionGate(facts, facts, index)
	result := make(chan error, 1)
	go func() {
		result <- gate.AllowInvoke(context.Background(), 5, 7, 9, 8)
	}()
	<-facts.entered
	index.MarkDirty(events.EntityAPIRole)
	close(facts.release)

	require.ErrorIs(t, <-result, cache.ErrAPICacheNotReady)
}

func permissionTestIndex(t *testing.T) *cache.APIIndex {
	t.Helper()
	index := cache.NewAPIIndex()
	require.NoError(t, index.ReplaceServices(nil))
	require.NoError(t, index.ReplaceRoutes(nil))
	require.NoError(t, index.ReplaceUpstreams(nil))
	require.NoError(t, index.ReplaceRoles([]protocol.SyncedAPIRole{
		{ID: 1, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 9, Action: "invoke"}}},
		{ID: 2, Permissions: []protocol.APIPermissionGrant{{Resource: "api_route", ResourceID: 8, Action: "invoke"}}},
		{ID: 3, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 0, Action: "invoke"}}},
		{ID: 4, Permissions: []protocol.APIPermissionGrant{{Resource: "api_route", ResourceID: 0, Action: "invoke"}}},
		{ID: 5, Permissions: []protocol.APIPermissionGrant{{Resource: "api_service", ResourceID: 9, Action: "manage"}, {Resource: "api_route", ResourceID: 8, Action: "read"}}},
	}))
	require.NoError(t, index.ReplaceUserGroupRoleSets(nil))
	return index
}
