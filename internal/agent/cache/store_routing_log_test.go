package cache

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

// newObserverLogger 构造 observer logger，捕获 Debug 及以上日志。
func newObserverLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	return zap.New(core), logs
}

// newGlobalRoutingsLRU 用指定 LoaderFunc 构造 globalRoutings LRU（供测试注入错误；
// 生产为 FullCache，不会返回连接错误）。
func newGlobalRoutingsLRU(loaderFn entitycache.LoaderFunc[string, *protocol.SyncedRouting]) entitycache.EntityCache[string, *protocol.SyncedRouting] {
	c, err := entitycache.NewLRUCache(entitycache.Config[string, *protocol.SyncedRouting]{
		Capacity:    100,
		Loader:      loaderFn,
		NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return c
}

// newUserRoutingsLRU 用指定 LoaderFunc 构造 userRoutings LRU（供测试注入错误）。
func newUserRoutingsLRU(loaderFn entitycache.LoaderFunc[uint, *protocol.UserRoutingMap]) entitycache.EntityCache[uint, *protocol.UserRoutingMap] {
	c, err := entitycache.NewLRUCache(entitycache.Config[uint, *protocol.UserRoutingMap]{
		Capacity:    100,
		Loader:      loaderFn,
		NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return c
}

// TestGetGlobalRouting_LogsWarnOnConnClosed 验证 ws.ErrConnClosed 时触发 Warn 日志，返回仍为 nil。
func TestGetGlobalRouting_LogsWarnOnConnClosed(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.globalRoutings = newGlobalRoutingsLRU(func(_ context.Context, key string) (*protocol.SyncedRouting, error) {
		return nil, ws.ErrConnClosed
	})

	result := s.GetGlobalRouting("gpt-4o")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("want 1 Warn log, got %d", len(warns))
	}
	fields := warns[0].ContextMap()
	if fields["entity"] != "global_routing" {
		t.Errorf("entity field want 'global_routing', got %q", fields["entity"])
	}
	if fields["reason"] != "master_unreachable" {
		t.Errorf("reason field want 'master_unreachable', got %q", fields["reason"])
	}
	if fields["name"] != "gpt-4o" {
		t.Errorf("name field want 'gpt-4o', got %q", fields["name"])
	}
}

// TestGetGlobalRouting_LogsWarnOnTimeout 验证内部超时（DeadlineExceeded）归类为 control_timeout。
func TestGetGlobalRouting_LogsWarnOnTimeout(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.globalRoutings = newGlobalRoutingsLRU(func(_ context.Context, key string) (*protocol.SyncedRouting, error) {
		return nil, context.DeadlineExceeded
	})

	result := s.GetGlobalRouting("gpt-4o")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("want 1 Warn log, got %d", len(warns))
	}
	if warns[0].ContextMap()["reason"] != "control_timeout" {
		t.Errorf("reason want 'control_timeout', got %q", warns[0].ContextMap()["reason"])
	}
}

// TestGetGlobalRouting_NotFoundNoWarn 验证 ErrNotFound 时只记 Debug，不产生 Warn。
func TestGetGlobalRouting_NotFoundNoWarn(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.globalRoutings = newGlobalRoutingsLRU(func(_ context.Context, key string) (*protocol.SyncedRouting, error) {
		return nil, entitycache.ErrNotFound
	})

	result := s.GetGlobalRouting("gpt-4o")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(warns) != 0 {
		t.Errorf("not_found should NOT produce Warn, got %d warn entries", len(warns))
	}
	debugs := logs.FilterLevelExact(zapcore.DebugLevel).FilterMessage("relay entity resolve degraded").All()
	if len(debugs) != 1 {
		t.Fatalf("not_found should produce exactly 1 Debug log, got %d", len(debugs))
	}
}

// TestListUserRoutingNames_LogsWarnOnConnClosed 验证 ListUserRoutingNames 在 ErrConnClosed 时记 Warn。
func TestListUserRoutingNames_LogsWarnOnConnClosed(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.userRoutings = newUserRoutingsLRU(func(_ context.Context, userID uint) (*protocol.UserRoutingMap, error) {
		return nil, ws.ErrConnClosed
	})

	result := s.ListUserRoutingNames(42)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("want 1 Warn log, got %d", len(warns))
	}
	fields := warns[0].ContextMap()
	if fields["entity"] != "user_routing" {
		t.Errorf("entity want 'user_routing', got %q", fields["entity"])
	}
	if fields["reason"] != "master_unreachable" {
		t.Errorf("reason want 'master_unreachable', got %q", fields["reason"])
	}
}

// TestListUserRoutingNames_NotFoundNoWarn 验证 ErrNotFound 时不产生 Warn。
func TestListUserRoutingNames_NotFoundNoWarn(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.userRoutings = newUserRoutingsLRU(func(_ context.Context, userID uint) (*protocol.UserRoutingMap, error) {
		return nil, entitycache.ErrNotFound
	})

	s.ListUserRoutingNames(42)

	warns := logs.FilterLevelExact(zapcore.WarnLevel).All()
	if len(warns) != 0 {
		t.Errorf("not_found should NOT produce Warn, got %d warn entries", len(warns))
	}
	debugs := logs.FilterLevelExact(zapcore.DebugLevel).FilterMessage("relay entity resolve degraded").All()
	if len(debugs) != 1 {
		t.Fatalf("not_found should produce exactly 1 Debug log, got %d", len(debugs))
	}
}

// TestResolveRouting_LogsWarnOnUserRoutingConnClosed 验证最高频调用方 ResolveRouting 在
// userRoutings loader 失败时也记 Warn（entity="user_routing" + user_id）。
func TestResolveRouting_LogsWarnOnUserRoutingConnClosed(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.userRoutings = newUserRoutingsLRU(func(_ context.Context, userID uint) (*protocol.UserRoutingMap, error) {
		return nil, ws.ErrConnClosed
	})

	if got := s.ResolveRouting("smart", 42); got != nil {
		t.Errorf("expected nil, got %+v", got)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	var userWarn map[string]any
	for _, w := range warns {
		if w.ContextMap()["entity"] == "user_routing" {
			userWarn = w.ContextMap()
			break
		}
	}
	if userWarn == nil {
		t.Fatalf("want a user_routing Warn entry, got entries %+v", warns)
	}
	if userWarn["reason"] != "master_unreachable" {
		t.Errorf("reason want 'master_unreachable', got %q", userWarn["reason"])
	}
	if userWarn["user_id"] != uint64(42) {
		t.Errorf("user_id want 42, got %v", userWarn["user_id"])
	}
}
