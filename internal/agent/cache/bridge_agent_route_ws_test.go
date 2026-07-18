package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/jsonrpc"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const agentRouteWSBarrierMethod = "test.agentRoutePushBarrier"

type recordingEventBus struct {
	*eventbus.MemoryBus
	mu     sync.Mutex
	topics []string
}

func newRecordingEventBus() *recordingEventBus {
	return &recordingEventBus{MemoryBus: eventbus.NewMemoryBus()}
}

func (b *recordingEventBus) Publish(ctx context.Context, event eventbus.Event) error {
	b.mu.Lock()
	b.topics = append(b.topics, event.Topic)
	b.mu.Unlock()
	return b.MemoryBus.Publish(ctx, event)
}

func (b *recordingEventBus) countTopicPrefix(prefix string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	count := 0
	for _, topic := range b.topics {
		if strings.HasPrefix(topic, prefix) {
			count++
		}
	}
	return count
}

func TestWSBridgeAgentRoutePushIsRegisteredInline(t *testing.T) {
	client := newCaptureWSClient()
	store := NewStore(nil, config.AgentCacheConfig{})
	syncer := NewSyncer(store, client, nil, zap.NewNop(), time.Hour)
	bridge := NewWSBridge(client, store, nil, zap.NewNop())
	bridge.Syncer = syncer
	bridge.Start()

	require.NotContains(t, client.handlers, consts.RPCSyncPush)
	require.NotNil(t, client.inlineHandlers[consts.RPCSyncPush])
}

func TestWSBridgeAgentRoutePushErrorsMarkRoutesDirty(t *testing.T) {
	newHandler := func(t *testing.T) (*Syncer, func(context.Context, json.RawMessage) (any, error)) {
		t.Helper()
		client := newCaptureWSClient()
		store := NewStore(nil, config.AgentCacheConfig{})
		syncer := NewSyncer(store, client, nil, zap.NewNop(), time.Hour)
		bridge := NewWSBridge(client, store, nil, zap.NewNop())
		bridge.Syncer = syncer
		bridge.Start()
		handler := client.inlineHandlers[consts.RPCSyncPush]
		require.NotNil(t, handler)
		return syncer, handler
	}

	t.Run("malformed envelope", func(t *testing.T) {
		syncer, handler := newHandler(t)
		pullCtx, pullCancel := context.WithCancel(context.Background())
		defer pullCancel()
		builder := &agentRouteSyncBuilder{
			cancel: pullCancel, session: syncer.CurrentControlSession(),
		}
		syncer.agentRouteStateMu.Lock()
		syncer.agentRouteBuilder = builder
		syncer.agentRouteStateMu.Unlock()
		defer syncer.clearAgentRouteBuilder(builder)

		_, err := handler(context.Background(), json.RawMessage(`{not-json`))
		require.Error(t, err)
		require.True(t, syncer.agentRouteDirty.Load())
		require.Error(t, syncer.agentRouteBuilderError(builder))
		select {
		case <-pullCtx.Done():
		default:
			t.Fatal("malformed envelope did not cancel the active route transaction")
		}
	})

	t.Run("unknown route action", func(t *testing.T) {
		syncer, handler := newHandler(t)
		route := agentRouteWSTestRoute(7, "must-not-apply")
		data, err := json.Marshal(route)
		require.NoError(t, err)
		envelope, err := json.Marshal(protocol.SyncPushParams{
			Entity:  events.EntityAgentRoute,
			Action:  "replace",
			Data:    data,
			Version: 21,
		})
		require.NoError(t, err)

		_, err = handler(context.Background(), envelope)
		require.Error(t, err)
		require.True(t, syncer.agentRouteDirty.Load())
		require.Empty(t, matchedAgentID(syncer.Store.RouteIndex, route.SourceID))
	})
}

func TestWSBridgeAgentRoutePushesAreCapturedInlineBeforeFullSyncResponse(t *testing.T) {
	logger := zap.NewNop()
	serverErrors := make(chan error, 1)
	requestSeen := make(chan protocol.FullSyncRequest, 1)
	barrierReached := make(chan struct{})
	releaseBarrier := make(chan struct{})
	serverStop := make(chan struct{})
	var releaseBarrierOnce sync.Once
	releasePushBarrier := func() {
		releaseBarrierOnce.Do(func() { close(releaseBarrier) })
	}

	baseRoute := agentRouteWSTestRoute(1, "snapshot")
	updateRoute := agentRouteWSTestRoute(1, "update-v12")
	createdRoute := agentRouteWSTestRoute(2, "create-v17")
	pushes := []protocol.SyncPushParams{
		agentRouteWSTestPush(events.ActionDelete, baseRoute, 14),
		agentRouteWSTestPush(events.ActionUpdate, updateRoute, 12),
		agentRouteWSTestPush(events.ActionCreate, createdRoute, 17),
		{
			Entity:  events.EntitySetting,
			Action:  events.ActionUpdate,
			Data:    []byte(`{"key":"test","value":"seen"}`),
			Version: 18,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := ws.Upgrade(w, r, logger)
		if err != nil {
			serverErrors <- fmt.Errorf("upgrade: %w", err)
			return
		}
		defer conn.Close()

		req, _, err := conn.ReadMessage()
		if err != nil {
			serverErrors <- fmt.Errorf("read full-sync request: %w", err)
			return
		}
		if req == nil || req.ID == nil || req.Method != consts.RPCSyncFullSync {
			serverErrors <- fmt.Errorf("unexpected full-sync request: %+v", req)
			return
		}
		var fullSyncReq protocol.FullSyncRequest
		if err := json.Unmarshal(req.Params, &fullSyncReq); err != nil {
			serverErrors <- fmt.Errorf("decode full-sync request: %w", err)
			return
		}
		requestSeen <- fullSyncReq

		for _, push := range pushes {
			if err := conn.SendNotification(consts.RPCSyncPush, push); err != nil {
				serverErrors <- fmt.Errorf("send sync push: %w", err)
				return
			}
		}
		if err := conn.SendNotification(agentRouteWSBarrierMethod, nil); err != nil {
			serverErrors <- fmt.Errorf("send barrier: %w", err)
			return
		}

		select {
		case <-barrierReached:
		case <-serverStop:
			return
		}
		items, err := json.Marshal([]models.AgentRoute{baseRoute})
		if err != nil {
			serverErrors <- fmt.Errorf("marshal full-sync items: %w", err)
			return
		}
		resp, err := jsonrpc.NewResponse(req.ID, protocol.FullSyncResponse{
			Items:         items,
			Total:         1,
			Version:       10,
			Keyset:        true,
			LastID:        1,
			SnapshotMaxID: 1,
			BaseVersion:   10,
		})
		if err != nil {
			serverErrors <- fmt.Errorf("build full-sync response: %w", err)
			return
		}
		if err := conn.SendResponse(resp); err != nil {
			serverErrors <- fmt.Errorf("send full-sync response: %w", err)
			return
		}
		<-serverStop
	})

	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := ws.Dial(context.Background(), "ws"+strings.TrimPrefix(server.URL, "http")+"/ws", logger)
	require.NoError(t, err)
	defer client.Close()
	defer close(serverStop)
	defer releasePushBarrier()

	bus := newRecordingEventBus()
	defer bus.Close()
	var routeBusEvents atomic.Int64
	_, err = events.SubscribeSyncPushPattern(bus, events.SyncAgentRouteAllPattern, func(context.Context, protocol.SyncPushParams) error {
		routeBusEvents.Add(1)
		return nil
	})
	require.NoError(t, err)
	nonRouteEvent := make(chan protocol.SyncPushParams, 1)
	_, err = events.SubscribeSyncPushPattern(bus, events.SyncSettingAllPattern, func(_ context.Context, push protocol.SyncPushParams) error {
		nonRouteEvent <- push
		return nil
	})
	require.NoError(t, err)

	store := NewStore(nil, config.AgentCacheConfig{})
	syncer := NewSyncer(store, client, bus, logger, time.Hour)
	bridge := NewWSBridge(client, store, bus, logger)
	bridge.Syncer = syncer
	bridge.Start()

	type builderCapture struct {
		active   bool
		versions []int64
	}
	builderCaptured := make(chan builderCapture, 1)
	client.OnNotificationInline(agentRouteWSBarrierMethod, func(context.Context, json.RawMessage) (any, error) {
		syncer.agentRouteStateMu.Lock()
		capture := builderCapture{active: syncer.agentRouteBuilder != nil}
		if syncer.agentRouteBuilder != nil {
			for _, push := range syncer.agentRouteBuilder.pushes {
				capture.versions = append(capture.versions, push.version)
			}
		}
		syncer.agentRouteStateMu.Unlock()
		builderCaptured <- capture
		close(barrierReached)
		<-releaseBarrier
		return nil, nil
	})

	syncDone := make(chan error, 1)
	go func() { syncDone <- syncer.fullSyncAgentRoutes(context.Background()) }()

	select {
	case req := <-requestSeen:
		require.Equal(t, events.EntityAgentRoute, req.Entity)
		require.Zero(t, req.Page)
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("fake master did not receive the AgentRoute full-sync request")
	}

	select {
	case capture := <-builderCaptured:
		require.True(t, capture.active)
		require.Equal(t, []int64{14, 12, 17}, capture.versions)
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("inline push barrier was not reached")
	}

	select {
	case push := <-nonRouteEvent:
		require.Equal(t, events.EntitySetting, push.Entity)
		require.Equal(t, int64(18), push.Version)
	case <-time.After(5 * time.Second):
		t.Fatal("non-AgentRoute sync.push did not reach EventBus")
	}

	releasePushBarrier()
	select {
	case err := <-syncDone:
		require.NoError(t, err)
	case err := <-serverErrors:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("AgentRoute full sync did not finish")
	}

	require.Empty(t, matchedAgentID(store.RouteIndex, 1), "version 14 delete must replay after version 12 update")
	require.Equal(t, "create-v17", matchedAgentID(store.RouteIndex, 2))
	require.Equal(t, 1, store.RouteIndex.CacheStat().Size)
	require.Equal(t, int64(17), store.Version())
	require.Zero(t, bus.countTopicPrefix("sync.agent_route."))
	require.Zero(t, routeBusEvents.Load())
}

func agentRouteWSTestRoute(id uint, agentID string) models.AgentRoute {
	return models.AgentRoute{
		ID:         id,
		SourceType: "token",
		SourceID:   id,
		Model:      "model",
		AgentID:    agentID,
		Priority:   100,
	}
}

func agentRouteWSTestPush(action string, route models.AgentRoute, version int64) protocol.SyncPushParams {
	data, err := json.Marshal(route)
	if err != nil {
		panic(err)
	}
	return protocol.SyncPushParams{
		Entity:  events.EntityAgentRoute,
		Action:  action,
		Data:    data,
		Version: version,
	}
}
