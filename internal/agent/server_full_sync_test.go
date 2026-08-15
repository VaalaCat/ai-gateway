package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type requestedFullSyncClient struct {
	mu             sync.Mutex
	handlers       map[string]app.NotificationHandler
	inlineHandlers map[string]app.NotificationHandler
	pass           int
	currentPass    int
	passStarted    chan int
	releaseFirst   chan struct{}
	secondFinished chan struct{}
	finishOnce     sync.Once
	apiRequests    map[int][]protocol.FullSyncRequest
}

var requestedFullSyncAPIEntities = []string{
	events.EntityAPIService,
	events.EntityAPIRoute,
	events.EntityAPIUpstream,
	events.EntityAPIRole,
	events.EntityUserGroupAPIRoleSet,
}

func (c *requestedFullSyncClient) OnNotification(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	c.handlers[method] = handler
	c.mu.Unlock()
}

func (c *requestedFullSyncClient) OnNotificationInline(method string, handler app.NotificationHandler) {
	c.mu.Lock()
	c.inlineHandlers[method] = handler
	c.mu.Unlock()
}

func (c *requestedFullSyncClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	if method != consts.RPCSyncFullSync {
		return nil, fmt.Errorf("method = %q, want %q", method, consts.RPCSyncFullSync)
	}
	req, ok := params.(protocol.FullSyncRequest)
	if !ok {
		return nil, fmt.Errorf("params type = %T, want protocol.FullSyncRequest", params)
	}

	if req.Entity == "user_group" {
		c.mu.Lock()
		c.pass++
		c.currentPass = c.pass
		pass := c.pass
		c.mu.Unlock()
		c.passStarted <- pass
		if pass == 1 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-c.releaseFirst:
			}
		}
	}

	c.mu.Lock()
	pass := c.currentPass
	if isRequestedFullSyncAPIEntity(req.Entity) {
		c.apiRequests[pass] = append(c.apiRequests[pass], req)
	}
	c.mu.Unlock()
	resp := protocol.FullSyncResponse{
		Items:   []byte("[]"),
		Page:    1,
		Version: int64(pass),
	}
	if req.Entity == "agent_route" {
		resp.Page = 0
		resp.Keyset = true
		resp.BaseVersion = int64(pass)
	}
	if req.Entity == "agent" {
		resp.Page = 0
		resp.Keyset = true
		resp.BaseVersion = int64(pass)
		resp.SnapshotContract = protocol.AgentFullSyncSnapshotContractV1
	}
	if isRequestedFullSyncAPIEntity(req.Entity) {
		resp.Page = 0
		resp.Keyset = true
		resp.BaseVersion = int64(pass)
		resp.SnapshotContract = protocol.APIFullSyncSnapshotContractV1
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		return nil, err
	}
	if req.Entity == "script" && pass == 2 {
		c.finishOnce.Do(func() { close(c.secondFinished) })
	}
	return raw, nil
}

func (c *requestedFullSyncClient) Notify(string, any) error { return nil }
func (c *requestedFullSyncClient) Close() error             { return nil }
func (c *requestedFullSyncClient) ReadLoop()                {}

func (c *requestedFullSyncClient) passes() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pass
}

func (c *requestedFullSyncClient) apiRequestsForPass(pass int) []protocol.FullSyncRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]protocol.FullSyncRequest(nil), c.apiRequests[pass]...)
}

func (c *requestedFullSyncClient) inlineHandler(method string) app.NotificationHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inlineHandlers[method]
}

func isRequestedFullSyncAPIEntity(entity string) bool {
	for _, candidate := range requestedFullSyncAPIEntities {
		if entity == candidate {
			return true
		}
	}
	return false
}

func requireRequestedFullSyncPass(t *testing.T, started <-chan int, want int) {
	t.Helper()
	select {
	case got := <-started:
		require.Equal(t, want, got)
	case <-time.After(5 * time.Second):
		t.Fatalf("requested full-sync pass %d did not start", want)
	}
}

func TestRequestedFullSyncCoalesces100RequestsIntoRunningAndPendingPass(t *testing.T) {
	client := &requestedFullSyncClient{
		handlers:       make(map[string]app.NotificationHandler),
		inlineHandlers: make(map[string]app.NotificationHandler),
		passStarted:    make(chan int, 2),
		releaseFirst:   make(chan struct{}),
		secondFinished: make(chan struct{}),
		apiRequests:    make(map[int][]protocol.FullSyncRequest),
	}
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	bus := eventbus.NewMemoryBus()
	syncer := cache.NewSyncer(
		store,
		client,
		bus,
		zap.NewNop(),
		time.Hour,
	)
	serverCtx, cancelServer := context.WithCancel(context.Background())
	server := &Server{Syncer: syncer}
	workerDone := server.startRequestedFullSyncWorker(serverCtx)
	var releaseFirstOnce sync.Once
	releaseFirst := func() {
		releaseFirstOnce.Do(func() { close(client.releaseFirst) })
	}
	stopWorker := func() bool {
		cancelServer()
		releaseFirst()
		select {
		case <-workerDone:
			return true
		case <-time.After(5 * time.Second):
			return false
		}
	}
	t.Cleanup(func() {
		if !stopWorker() {
			t.Error("requested full-sync worker did not stop during test cleanup")
		}
	})
	bridge := cache.NewWSBridge(client, store, bus, zap.NewNop())
	bridge.Syncer = syncer
	bridge.Start()
	handler := client.inlineHandler(consts.RPCSyncRequestFullSync)
	require.NotNil(t, handler)

	connectionCtx, cancelConnection := context.WithCancel(serverCtx)
	_, err := handler(connectionCtx, nil)
	require.NoError(t, err)
	requireRequestedFullSyncPass(t, client.passStarted, 1)

	start := make(chan struct{})
	var handlerErrors atomic.Int64
	var requests sync.WaitGroup
	requests.Add(99)
	for i := 0; i < 99; i++ {
		go func() {
			defer requests.Done()
			<-start
			if _, err := handler(connectionCtx, nil); err != nil {
				handlerErrors.Add(1)
			}
		}()
	}
	close(start)
	requestsDone := make(chan struct{})
	go func() {
		requests.Wait()
		close(requestsDone)
	}()
	select {
	case <-requestsDone:
	case <-time.After(5 * time.Second):
		t.Fatal("99 concurrent requested full-sync handlers did not finish")
	}
	require.Zero(t, handlerErrors.Load())
	cancelConnection()
	<-connectionCtx.Done()

	releaseFirst()
	requireRequestedFullSyncPass(t, client.passStarted, 2)
	select {
	case <-client.secondFinished:
	case <-time.After(5 * time.Second):
		t.Fatal("requested full-sync pass 2 did not reach the script entity")
	}
	require.NoError(t, store.APIIndex.RequireReady())
	for pass := 1; pass <= 2; pass++ {
		requests := client.apiRequestsForPass(pass)
		require.Len(t, requests, len(requestedFullSyncAPIEntities))
		entities := make([]string, 0, len(requests))
		for _, request := range requests {
			entities = append(entities, request.Entity)
			require.Zero(t, request.Page)
			require.Equal(t, protocol.FullSyncMaxPageSize, request.PageSize)
			require.Zero(t, request.AfterID)
			require.Zero(t, request.SnapshotMaxID)
			require.Zero(t, request.BaseVersion)
		}
		require.ElementsMatch(t, requestedFullSyncAPIEntities, entities)
	}
	require.True(t, stopWorker(), "requested full-sync worker did not stop after cancellation")
	require.Equal(t, 2, client.passes())
}
