package cache

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestControlSessionReplacementRejectsStaleFullSyncBuilders(t *testing.T) {
	t.Run("agent snapshot", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		client := &agentRouteSyncClient{}
		client.respond = func(context.Context, agentRouteSyncCall, int) (json.RawMessage, error) {
			close(started)
			<-release
			return marshalAgentFullSync(
				[]models.Agent{testSyncedAgent("new-agent", "stale-snapshot")},
				protocol.FullSyncResponse{Version: 20, BaseVersion: 10},
			), nil
		}
		syncer := newAgentRouteTestSyncer(client)
		syncer.Store.SetAgent(&models.Agent{AgentID: "live-agent", Name: "keep-live", Status: 1})
		oldSession := syncer.CurrentControlSession()

		done := make(chan error, 1)
		go func() { done <- syncer.fullSyncEntity(context.Background(), events.EntityAgent) }()
		<-started
		newSession := syncer.BeginControlSession(&agentRouteSyncClient{})
		close(release)

		require.ErrorIs(t, <-done, ErrControlSessionChanged)
		require.Same(t, newSession, syncer.CurrentControlSession())
		require.ErrorIs(t, context.Cause(oldSession.ctx), ErrControlSessionChanged)
		require.Equal(t, "keep-live", syncer.Store.GetAgent("live-agent").Name)
		require.Nil(t, syncer.Store.GetAgent("new-agent"))
		require.True(t, syncer.agentsDirty.Load())
		requireAgentBuilderCleared(t, syncer)
	})

	t.Run("agent route snapshot", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		client := &agentRouteSyncClient{}
		client.respond = func(context.Context, agentRouteSyncCall, int) (json.RawMessage, error) {
			close(started)
			<-release
			return marshalAgentRouteFullSync(
				[]*models.AgentRoute{testAgentRoute(2, "stale-route")},
				protocol.FullSyncResponse{
					Version: 20, Keyset: true, LastID: 2, SnapshotMaxID: 2, BaseVersion: 10,
				},
			), nil
		}
		syncer := newAgentRouteTestSyncer(client)
		syncer.Store.RouteIndex.Replace([]*models.AgentRoute{testAgentRoute(1, "keep-route")})
		oldSession := syncer.CurrentControlSession()

		done := make(chan error, 1)
		go func() { done <- syncer.fullSyncEntity(context.Background(), events.EntityAgentRoute) }()
		<-started
		newSession := syncer.BeginControlSession(&agentRouteSyncClient{})
		close(release)

		require.ErrorIs(t, <-done, ErrControlSessionChanged)
		require.Same(t, newSession, syncer.CurrentControlSession())
		require.ErrorIs(t, context.Cause(oldSession.ctx), ErrControlSessionChanged)
		require.Equal(t, "keep-route", matchedAgentID(syncer.Store.RouteIndex, 1))
		require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, 2))
		require.True(t, syncer.agentRouteDirty.Load())
		requireAgentRouteBuilderCleared(t, syncer)
	})
}

func TestWSBridgeControlSessionFenceRejectsStalePushes(t *testing.T) {
	tests := []struct {
		name        string
		prepare     func(*Store)
		push        protocol.SyncPushParams
		assertStale func(*testing.T, *Store)
		assertFresh func(*testing.T, *Store)
	}{
		{
			name: "agent",
			prepare: func(store *Store) {
				store.SetAgent(&models.Agent{AgentID: "target", Name: "before", Status: 1})
			},
			push: sessionTestPush(t, events.EntityAgent, events.ActionUpdate,
				models.Agent{AgentID: "target", Name: "after", Status: 1}),
			assertStale: func(t *testing.T, store *Store) { require.Equal(t, "before", store.GetAgent("target").Name) },
			assertFresh: func(t *testing.T, store *Store) { require.Equal(t, "after", store.GetAgent("target").Name) },
		},
		{
			name: "agent route",
			push: sessionTestPush(t, events.EntityAgentRoute, events.ActionCreate, models.AgentRoute{
				ID: 7, SourceType: "token", SourceID: 77, AgentID: "route-target", Priority: 90,
			}),
			assertStale: func(t *testing.T, store *Store) { require.Empty(t, matchedAgentID(store.RouteIndex, 77)) },
			assertFresh: func(t *testing.T, store *Store) {
				require.Equal(t, "route-target", matchedAgentID(store.RouteIndex, 77))
			},
		},
		{
			name: "event bus entity",
			push: sessionTestPush(t, events.EntityChannel, events.ActionCreate, models.Channel{
				ChannelCore: models.ChannelCore{ID: 99, Name: "fresh-channel", Status: 1},
			}),
			assertStale: func(t *testing.T, store *Store) { require.Nil(t, store.GetChannel(99)) },
			assertFresh: func(t *testing.T, store *Store) { require.Equal(t, "fresh-channel", store.GetChannel(99).Name) },
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			bus := eventbus.NewMemoryBus()
			t.Cleanup(func() { require.NoError(t, bus.Close()) })
			store := NewStore(nil, config.AgentCacheConfig{})
			if test.prepare != nil {
				test.prepare(store)
			}
			oldClient := newCaptureWSClient()
			syncer := NewSyncer(store, oldClient, bus, zap.NewNop(), time.Hour)
			require.NoError(t, syncer.SubscribeEvents())
			oldBridge := NewWSBridge(oldClient, store, bus, zap.NewNop())
			oldBridge.Syncer = syncer
			oldBridge.ControlSession = syncer.CurrentControlSession()
			oldBridge.Start()
			oldHandler := oldClient.inlineHandlers[consts.RPCSyncPush]

			newClient := newCaptureWSClient()
			newSession := syncer.BeginControlSession(newClient)
			newBridge := NewWSBridge(newClient, store, bus, zap.NewNop())
			newBridge.Syncer = syncer
			newBridge.ControlSession = newSession
			newBridge.Start()
			newHandler := newClient.inlineHandlers[consts.RPCSyncPush]
			raw, err := json.Marshal(test.push)
			require.NoError(t, err)

			_, err = oldHandler(context.Background(), raw)
			require.NoError(t, err)
			test.assertStale(t, store)
			require.Zero(t, store.Version())

			_, err = newHandler(context.Background(), raw)
			require.NoError(t, err)
			test.assertFresh(t, store)
			require.Equal(t, int64(10), store.Version())
		})
	}
}

func TestRequestedFullSyncCarriesOriginatingControlSession(t *testing.T) {
	syncer := newAgentRouteTestSyncer(&agentRouteSyncClient{})
	oldSession := syncer.CurrentControlSession()
	require.True(t, syncer.RequestFullSyncForSession(oldSession))
	queued := <-syncer.requestedFullSyncChannel()
	require.Same(t, oldSession, queued)

	syncer.BeginControlSession(&agentRouteSyncClient{})
	require.ErrorIs(t, syncer.FullSyncForSession(context.Background(), queued), ErrControlSessionChanged)
}

func sessionTestPush(t *testing.T, entity, action string, value any) protocol.SyncPushParams {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return protocol.SyncPushParams{Entity: entity, Action: action, Data: data, Version: 10}
}
