package cache

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type roleSetResponseClient struct {
	mu        sync.Mutex
	responses []roleSetResponse
	calls     int
}

type roleSetResponse struct {
	result protocol.APIRoleSetFetchResult
	err    error
}

func (c *roleSetResponseClient) Call(_ context.Context, _ string, _ any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	response := c.responses[c.calls]
	c.calls++
	if response.err != nil {
		return nil, response.err
	}
	return json.Marshal(response.result)
}
func (*roleSetResponseClient) OnNotification(string, app.NotificationHandler) {}
func (*roleSetResponseClient) Notify(string, any) error                       { return nil }
func (*roleSetResponseClient) Close() error                                   { return nil }
func (*roleSetResponseClient) ReadLoop()                                      {}

func (c *roleSetResponseClient) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func TestAPIRoleSetCacheKeepsPositiveEmptyAndConfirmedMissing(t *testing.T) {
	tests := []struct {
		name     string
		response protocol.APIRoleSetFetchResult
		wantErr  error
	}{
		{name: "positive empty", response: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{}}}},
		{name: "confirmed missing", response: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: false}, wantErr: entitycache.ErrNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &roleSetResponseClient{responses: []roleSetResponse{{result: test.response}}}
			store := NewStore(client, config.AgentCacheConfig{NegativeTTLSeconds: 60})
			defer store.Close()
			for attempt := 0; attempt < 2; attempt++ {
				got, found, err := store.FindTokenAPIRoleSet(context.Background(), 7)
				if test.wantErr != nil {
					require.ErrorIs(t, err, test.wantErr)
					require.False(t, found)
					continue
				}
				require.NoError(t, err)
				require.True(t, found)
				require.NotNil(t, got.RoleIDs)
				require.Empty(t, got.RoleIDs)
			}
			require.Equal(t, 1, client.callCount())
		})
	}
}

func TestAPIRoleSetCacheRetriesUnavailableLoad(t *testing.T) {
	client := &roleSetResponseClient{responses: []roleSetResponse{
		{err: errors.New("rpc unavailable")},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{3}}}},
	}}
	store := NewStore(client, config.AgentCacheConfig{NegativeTTLSeconds: 60})
	defer store.Close()

	_, found, err := store.FindUserAPIRoleSet(context.Background(), 7)
	require.ErrorContains(t, err, "rpc unavailable")
	require.False(t, found)
	got, found, err := store.FindUserAPIRoleSet(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{3}, got.RoleIDs)
	require.Equal(t, 2, client.callCount())
}

func TestAPIRoleSetInvalidationClearsPositiveAndNegativeEntries(t *testing.T) {
	client := &roleSetResponseClient{responses: []roleSetResponse{
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{1}}}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 8, Exists: false}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{2}}}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 8, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{3}}}},
	}}
	store := NewStore(client, config.AgentCacheConfig{NegativeTTLSeconds: 60})
	defer store.Close()

	_, _, _ = store.FindTokenAPIRoleSet(context.Background(), 7)
	_, _, _ = store.FindTokenAPIRoleSet(context.Background(), 8)
	store.DeleteTokenAPIRoleSet(7)
	store.DeleteTokenAPIRoleSet(8)
	positive, found, err := store.FindTokenAPIRoleSet(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{2}, positive.RoleIDs)
	formerlyMissing, found, err := store.FindTokenAPIRoleSet(context.Background(), 8)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{3}, formerlyMissing.RoleIDs)
}

func TestControlSessionChangesClearOnlyAPIRoleSetCaches(t *testing.T) {
	client := &roleSetResponseClient{responses: []roleSetResponse{
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{1}}}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 8, Exists: false}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 7, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{2}}}},
		{result: protocol.APIRoleSetFetchResult{PrincipalID: 8, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{3}}}},
	}}
	store := NewStore(client, config.AgentCacheConfig{NegativeTTLSeconds: 60})
	defer store.Close()
	store.SetUser(&protocol.SyncedUser{ID: 70, GroupID: 1})
	store.SetToken(&models.Token{ID: 80, Key: "sk-session-stable", UserID: 70, Status: 1})
	syncer := NewSyncer(store, client, eventbus.NewMemoryBus(), zap.NewNop(), time.Hour)
	oldSession := syncer.CurrentControlSession()

	userBefore, found, err := store.FindUserAPIRoleSet(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{1}, userBefore.RoleIDs)
	_, found, err = store.FindTokenAPIRoleSet(context.Background(), 8)
	require.ErrorIs(t, err, entitycache.ErrNotFound)
	require.False(t, found)

	newSession := syncer.BeginControlSession(client)
	require.NotNil(t, newSession)
	require.NotSame(t, oldSession, newSession)
	require.Equal(t, uint(70), store.GetToken(context.Background(), "sk-session-stable").UserID)
	user, found, err := store.FindUser(context.Background(), 70)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint(1), user.GroupID)

	userAfter, found, err := store.FindUserAPIRoleSet(context.Background(), 7)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{2}, userAfter.RoleIDs)
	tokenAfter, found, err := store.FindTokenAPIRoleSet(context.Background(), 8)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, []uint{3}, tokenAfter.RoleIDs)
	require.Equal(t, 4, client.callCount())

	store.SetUserAPIRoleSet(9, &protocol.APIRoleSet{RoleIDs: []uint{9}})
	require.False(t, syncer.EndControlSession(oldSession), "stale End must not clear current-session caches")
	_, cached := store.userAPIRoleSets.Peek(9)
	require.True(t, cached)
	require.True(t, syncer.EndControlSession(newSession))
	_, cached = store.userAPIRoleSets.Peek(9)
	require.False(t, cached)
}

func TestSyncerSubscribeEventsRegistersGenericAPIPatterns(t *testing.T) {
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetUserAPIRoleSet(7, &protocol.APIRoleSet{RoleIDs: []uint{1}})
	store.SetTokenAPIRoleSet(8, &protocol.APIRoleSet{RoleIDs: []uint{2}})
	bus := eventbus.NewMemoryBus()
	syncer := NewSyncer(store, nil, bus, zap.NewNop(), 0)
	require.NoError(t, syncer.SubscribeEvents())

	pushes := []protocol.SyncPushParams{
		apiPush(t, events.EntityUserAPIRoleSet, events.ActionInvalidate, protocol.APIRoleSetInvalidate{PrincipalID: 7}, 1),
		apiPush(t, events.EntityTokenAPIRoleSet, events.ActionInvalidate, protocol.APIRoleSetInvalidate{PrincipalID: 8}, 2),
		apiPush(t, events.EntityUserGroupAPIRoleSet, events.ActionUpdate,
			protocol.APIRoleSetFetchResult{PrincipalID: 9, Exists: true, RoleSet: protocol.APIRoleSet{RoleIDs: []uint{}}}, 3),
		apiPush(t, events.EntityAPIService, events.ActionCreate, protocol.SyncedAPIService{ID: 10, Slug: "weather", Status: 1}, 4),
	}
	for _, push := range pushes {
		require.NoError(t, events.PublishSyncEvent(context.Background(), bus, push.Entity, push.Action, push))
	}

	_, userFound, userErr := store.FindUserAPIRoleSet(context.Background(), 7)
	require.NoError(t, userErr)
	require.False(t, userFound)
	_, tokenFound, tokenErr := store.FindTokenAPIRoleSet(context.Background(), 8)
	require.NoError(t, tokenErr)
	require.False(t, tokenFound)
	group := store.APIIndex.UserGroupRoleSet(9)
	require.True(t, group.Exists)
	require.NotNil(t, group.RoleSet.RoleIDs)
	require.Empty(t, group.RoleSet.RoleIDs)
	require.Equal(t, "weather", store.APIIndex.load().servicesByID[10].Slug)
}
