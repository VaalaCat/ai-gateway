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

func (c *requestedFullSyncClient) inlineHandler(method string) app.NotificationHandler {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inlineHandlers[method]
}

func TestRequestedFullSyncCoalesces100RequestsIntoRunningAndPendingPass(t *testing.T) {
	client := &requestedFullSyncClient{
		handlers:       make(map[string]app.NotificationHandler),
		inlineHandlers: make(map[string]app.NotificationHandler),
		passStarted:    make(chan int, 2),
		releaseFirst:   make(chan struct{}),
		secondFinished: make(chan struct{}),
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
	bridge := cache.NewWSBridge(client, store, bus, zap.NewNop())
	bridge.Syncer = syncer
	bridge.Start()
	handler := client.inlineHandler(consts.RPCSyncRequestFullSync)
	require.NotNil(t, handler)

	connectionCtx, cancelConnection := context.WithCancel(serverCtx)
	_, err := handler(connectionCtx, nil)
	require.NoError(t, err)
	require.Equal(t, 1, <-client.passStarted)

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
	requests.Wait()
	require.Zero(t, handlerErrors.Load())
	cancelConnection()
	<-connectionCtx.Done()

	close(client.releaseFirst)
	require.Equal(t, 2, <-client.passStarted)
	<-client.secondFinished
	cancelServer()
	select {
	case <-workerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("requested full-sync worker did not stop after cancellation")
	}
	require.Equal(t, 2, client.passes())
}
