package cache

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// captureWSClient 捕获 Start() 注册的 OnNotification 回调，供测试直接触发。
type captureWSClient struct {
	handlers       map[string]app.NotificationHandler
	inlineHandlers map[string]app.NotificationHandler
}

func newCaptureWSClient() *captureWSClient {
	return &captureWSClient{
		handlers:       map[string]app.NotificationHandler{},
		inlineHandlers: map[string]app.NotificationHandler{},
	}
}

func (c *captureWSClient) OnNotification(method string, handler app.NotificationHandler) {
	c.handlers[method] = handler
}
func (c *captureWSClient) OnNotificationInline(method string, handler app.NotificationHandler) {
	c.inlineHandlers[method] = handler
}
func (c *captureWSClient) Call(_ context.Context, _ string, _ any) (json.RawMessage, error) {
	return nil, nil
}
func (c *captureWSClient) Notify(_ string, _ any) error { return nil }
func (c *captureWSClient) Close() error                 { return nil }
func (c *captureWSClient) ReadLoop()                    {}

func TestWSBridgeAgentCapabilitiesApplyBoundedInlineUpdates(t *testing.T) {
	client := newCaptureWSClient()
	store := NewStore(nil, config.AgentCacheConfig{})
	bridge := NewWSBridge(client, store, nil, zap.NewNop())
	bridge.Start()

	if _, ok := client.handlers[consts.RPCSyncAgentCapabilities]; ok {
		t.Fatal("bounded capability updates must not be dispatched asynchronously")
	}
	handler := client.inlineHandlers[consts.RPCSyncAgentCapabilities]
	if handler == nil {
		t.Fatal("capability inline handler not registered")
	}
	if client.inlineHandlers[consts.RPCSyncPush] == nil {
		t.Fatal("registering capabilities displaced ordered sync.push handling")
	}

	raw, err := json.Marshal(protocol.AgentCapabilitiesUpdate{
		AgentID: "agent-a",
		Capabilities: []string{
			" future.short ",
			protocol.AgentCapabilityTunnelV1,
			protocol.AgentCapabilityTunnelV1,
			"",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), raw); err != nil {
		t.Fatalf("valid capability update: %v", err)
	}
	want := []string{protocol.AgentCapabilityTunnelV1, "future.short"}
	if got := store.GetAgentCapabilities("agent-a"); !slices.Equal(got, want) {
		t.Fatalf("stored capabilities = %#v, want %#v", got, want)
	}

	raw, err = json.Marshal(protocol.AgentCapabilitiesUpdate{AgentID: "agent-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), raw); err != nil {
		t.Fatalf("empty capability update: %v", err)
	}
	if got := store.GetAgentCapabilities("agent-a"); got != nil {
		t.Fatalf("empty update did not clear capabilities: %#v", got)
	}
}

func TestWSBridgeAgentCapabilitiesIgnoreMalformedOrEmptyAgentWithoutPanicking(t *testing.T) {
	client := newCaptureWSClient()
	store := NewStore(nil, config.AgentCacheConfig{})
	store.SetAgentCapabilities("agent-a", []string{"existing"})
	bridge := NewWSBridge(client, store, nil, zap.NewNop())
	bridge.Start()
	handler := client.inlineHandlers[consts.RPCSyncAgentCapabilities]
	if handler == nil {
		t.Fatal("capability inline handler not registered")
	}

	if _, err := handler(context.Background(), json.RawMessage(`{"agent_id":`)); err != nil {
		t.Fatalf("malformed update must be ignored, got %v", err)
	}
	if got := store.GetAgentCapabilities("agent-a"); !slices.Equal(got, []string{"existing"}) {
		t.Fatalf("malformed update changed existing state: %#v", got)
	}
	for _, agentID := range []string{"", "   "} {
		raw, err := json.Marshal(protocol.AgentCapabilitiesUpdate{
			AgentID:      agentID,
			Capabilities: []string{"must-not-exist"},
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := handler(context.Background(), raw); err != nil {
			t.Fatalf("empty agent update: %v", err)
		}
	}
	if got := store.GetAgentCapabilities(""); got != nil {
		t.Fatalf("empty agent ID polluted Store: %#v", got)
	}
	if got := store.GetAgentCapabilities("   "); got != nil {
		t.Fatalf("blank agent ID polluted Store: %#v", got)
	}

	nilStoreClient := newCaptureWSClient()
	NewWSBridge(nilStoreClient, nil, nil, zap.NewNop()).Start()
	nilStoreHandler := nilStoreClient.inlineHandlers[consts.RPCSyncAgentCapabilities]
	if nilStoreHandler == nil {
		t.Fatal("nil-Store bridge did not register capability handler")
	}
	if _, err := nilStoreHandler(context.Background(), json.RawMessage(`{"agent_id":"agent-a","capabilities":["future.short"]}`)); err != nil {
		t.Fatalf("nil-Store update must be ignored: %v", err)
	}
}

func TestWSBridgeRequestedFullSyncUsesInlineDirectAdmission(t *testing.T) {
	client := newCaptureWSClient()
	store := NewStore(nil, config.AgentCacheConfig{})
	syncer := NewSyncer(store, client, nil, zap.NewNop(), time.Hour)
	bridge := NewWSBridge(client, store, nil, zap.NewNop())
	bridge.Syncer = syncer
	bridge.Start()

	if _, ok := client.handlers[consts.RPCSyncRequestFullSync]; ok {
		t.Fatal("requested full sync must not use asynchronous notification registration")
	}
	handler := client.inlineHandlers[consts.RPCSyncRequestFullSync]
	if handler == nil {
		t.Fatal("requested full sync inline handler not registered")
	}

	start := make(chan struct{})
	var calls sync.WaitGroup
	calls.Add(100)
	for i := 0; i < 100; i++ {
		go func() {
			defer calls.Done()
			<-start
			if _, err := handler(context.Background(), nil); err != nil {
				t.Errorf("requested full sync handler: %v", err)
			}
		}()
	}
	close(start)
	calls.Wait()
	if syncer.RequestFullSync() {
		t.Fatal("100 direct admissions must leave exactly one pending signal")
	}
}

// TestWSBridge_AppliesUserQuotaSync 验证 master 回送的 UserQuotaSync 被应用到本地
// user 缓存余额。覆盖 success(已缓存 user 被更新)、boundary(未缓存 user 静默忽略)、
// failure(非法 JSON 不 panic)。
func TestWSBridge_AppliesUserQuotaSync(t *testing.T) {
	newBridge := func() (*captureWSClient, *Store, *WSBridge) {
		cli := newCaptureWSClient()
		// Store 用 nil client → user 缓存无 loader，GetUser 未命中即返回 nil(不远程拉取),
		// 让 boundary 用例确定性断言。bridge 仍用 capture client 注册/捕获 OnNotification。
		s := NewStore(nil, config.AgentCacheConfig{})
		logger, _ := zap.NewDevelopment()
		b := &WSBridge{Client: cli, Store: s, Logger: logger}
		b.Start()
		return cli, s, b
	}

	t.Run("success_updates_cached_user_quota", func(t *testing.T) {
		cli, s, _ := newBridge()
		s.SetUser(&protocol.SyncedUser{ID: 4, GroupID: 1, Quota: 100})

		handler := cli.handlers[consts.RPCSyncUserQuota]
		if handler == nil {
			t.Fatal("RPCSyncUserQuota handler not registered")
		}
		params, _ := json.Marshal([]protocol.SyncedUser{{ID: 4, Quota: 20}})
		if _, err := handler(context.Background(), params); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}

		u := s.GetUser(context.Background(), 4)
		if u == nil {
			t.Fatal("user 4 missing from store")
		}
		if u.Quota != 20 {
			t.Fatalf("Quota = %d, want 20", u.Quota)
		}
	})

	t.Run("boundary_uncached_user_is_ignored", func(t *testing.T) {
		cli, s, _ := newBridge()
		// user 4 未缓存
		handler := cli.handlers[consts.RPCSyncUserQuota]
		params, _ := json.Marshal([]protocol.SyncedUser{{ID: 4, Quota: 20}})
		if _, err := handler(context.Background(), params); err != nil {
			t.Fatalf("handler returned error: %v", err)
		}
		if u := s.GetUser(context.Background(), 4); u != nil {
			t.Fatalf("uncached user must stay absent, got %+v", u)
		}
	})

	t.Run("failure_invalid_json_does_not_panic", func(t *testing.T) {
		cli, _, _ := newBridge()
		handler := cli.handlers[consts.RPCSyncUserQuota]
		if _, err := handler(context.Background(), json.RawMessage(`{not json`)); err != nil {
			t.Fatalf("handler should swallow invalid JSON, got err: %v", err)
		}
	})
}

func TestWSBridgeHandleAutoAddrUpdate(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetAgent(&models.Agent{AgentID: "agent-c"})
	s.BeginDirectAddressSession("master-a")

	client := newCaptureWSClient()
	b := NewWSBridge(client, s, nil, zap.NewNop())
	b.Start()
	if client.handlers[consts.RPCSyncAutoAddrUpdate] != nil {
		t.Fatal("direct address updates must not be dispatched asynchronously")
	}
	handler := client.inlineHandlers[consts.RPCSyncAutoAddrUpdate]
	if handler == nil {
		t.Fatal("direct address inline handler not registered")
	}

	raw, err := json.Marshal(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "agent-c", SessionGeneration: 3, Sequence: 9,
		HTTPAddresses: []protocol.Address{{URL: "http://10.0.0.9:8139", Tag: "auto-detected"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), raw); err != nil {
		t.Fatalf("handle direct addresses update failed: %v", err)
	}

	agent := s.GetAgent("agent-c")
	if agent == nil {
		t.Fatal("expected agent to exist")
	}
	if agent.HTTPAddresses != `[{"url":"http://10.0.0.9:8139","tag":"auto-detected"}]` {
		t.Fatalf("unexpected updated addresses: %s", agent.HTTPAddresses)
	}

	if _, err := handler(context.Background(), json.RawMessage(`{"master_instance_id":`)); err != nil {
		t.Fatalf("malformed direct address update must be contained, got %v", err)
	}
	wrongEpoch, err := json.Marshal(protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-b", AgentID: "agent-c", SessionGeneration: 4, Sequence: 10,
		HTTPAddresses: []protocol.Address{},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), wrongEpoch); err != nil {
		t.Fatalf("wrong-epoch direct address update must be contained, got %v", err)
	}
	if got := s.GetAgent("agent-c").HTTPAddresses; got != agent.HTTPAddresses {
		t.Fatalf("rejected direct address update changed state: before=%s after=%s", agent.HTTPAddresses, got)
	}
}
