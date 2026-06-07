package cache

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

// captureWSClient 捕获 Start() 注册的 OnNotification 回调，供测试直接触发。
type captureWSClient struct {
	handlers map[string]app.NotificationHandler
}

func newCaptureWSClient() *captureWSClient {
	return &captureWSClient{handlers: map[string]app.NotificationHandler{}}
}

func (c *captureWSClient) OnNotification(method string, handler app.NotificationHandler) {
	c.handlers[method] = handler
}
func (c *captureWSClient) Call(_ context.Context, _ string, _ any) (json.RawMessage, error) {
	return nil, nil
}
func (c *captureWSClient) Notify(_ string, _ any) error { return nil }
func (c *captureWSClient) Close() error                 { return nil }
func (c *captureWSClient) ReadLoop()                    {}

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
	s.SetAgent(&models.Agent{
		AgentID:       "agent-c",
		HTTPAddresses: `[{"url":"http://10.0.0.1:8139","tag":"auto-detected"}]`,
	})

	logger, _ := zap.NewDevelopment()
	b := &WSBridge{Store: s, Logger: logger}

	err := b.handleAutoAddrUpdate([]byte(`{"agent_id":"agent-c","http_addresses":[{"url":"http://10.0.0.9:8139","tag":"auto-detected"}]}`))
	if err != nil {
		t.Fatalf("handleAutoAddrUpdate failed: %v", err)
	}

	agent := s.GetAgent("agent-c")
	if agent == nil {
		t.Fatal("expected agent to exist")
	}
	if agent.HTTPAddresses != `[{"url":"http://10.0.0.9:8139","tag":"auto-detected"}]` {
		t.Fatalf("unexpected updated addresses: %s", agent.HTTPAddresses)
	}
}
