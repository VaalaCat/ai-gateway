package cache

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap/zapcore"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache/entitycache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
)

// newVisiblePrivateChannelsLRU 用指定 LoaderFunc 构造 visiblePrivateChannels LRU（供测试注入错误）。
func newVisiblePrivateChannelsLRU(loaderFn entitycache.LoaderFunc[uint, *protocol.VisiblePrivateChannelSet]) entitycache.EntityCache[uint, *protocol.VisiblePrivateChannelSet] {
	c, err := entitycache.NewLRUCache(entitycache.Config[uint, *protocol.VisiblePrivateChannelSet]{
		Capacity:    100,
		Loader:      loaderFn,
		NegativeTTL: 30 * time.Second,
	})
	if err != nil {
		panic(err)
	}
	return c
}

// TestGetVisiblePrivateChannelsForUser_LogsWarnOnConnClosed 验证 ErrConnClosed 时触发 Warn，
// 并带 entity="private_channel_visible"、reason="master_unreachable"、user_id=42，返回仍为 nil。
func TestGetVisiblePrivateChannelsForUser_LogsWarnOnConnClosed(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.visiblePrivateChannels = newVisiblePrivateChannelsLRU(func(_ context.Context, userID uint) (*protocol.VisiblePrivateChannelSet, error) {
		return nil, ws.ErrConnClosed
	})

	result := s.GetVisiblePrivateChannelsForUser(42, "gpt-4o")
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("want 1 Warn log, got %d", len(warns))
	}
	fields := warns[0].ContextMap()
	if fields["entity"] != "private_channel_visible" {
		t.Errorf("entity want 'private_channel_visible', got %q", fields["entity"])
	}
	if fields["reason"] != "master_unreachable" {
		t.Errorf("reason want 'master_unreachable', got %q", fields["reason"])
	}
	if fields["user_id"] != uint64(42) {
		t.Errorf("user_id want 42, got %v", fields["user_id"])
	}
}

// TestGetVisiblePrivateChannelsForUser_NotFoundNoWarn 验证 ErrNotFound 时不产生 Warn，
// 而是恰好一条 Debug 级降级日志。
func TestGetVisiblePrivateChannelsForUser_NotFoundNoWarn(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.visiblePrivateChannels = newVisiblePrivateChannelsLRU(func(_ context.Context, userID uint) (*protocol.VisiblePrivateChannelSet, error) {
		return nil, entitycache.ErrNotFound
	})

	result := s.GetVisiblePrivateChannelsForUser(42, "gpt-4o")
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

// TestListVisibleBYOKModelNamesForUser_LogsWarnOnConnClosed 验证 BYOK model 名列举走同一
// visiblePrivateChannels 缓存时，ErrConnClosed 同样触发 Warn 降级日志，返回仍为 nil。
func TestListVisibleBYOKModelNamesForUser_LogsWarnOnConnClosed(t *testing.T) {
	logger, logs := newObserverLogger()

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)
	s.visiblePrivateChannels = newVisiblePrivateChannelsLRU(func(_ context.Context, userID uint) (*protocol.VisiblePrivateChannelSet, error) {
		return nil, ws.ErrConnClosed
	})

	result := s.ListVisibleBYOKModelNamesForUser(42)
	if result != nil {
		t.Errorf("expected nil, got %+v", result)
	}

	warns := logs.FilterLevelExact(zapcore.WarnLevel).FilterMessage("relay entity resolve degraded").All()
	if len(warns) != 1 {
		t.Fatalf("want 1 Warn log, got %d", len(warns))
	}
	fields := warns[0].ContextMap()
	if fields["entity"] != "private_channel_visible" {
		t.Errorf("entity want 'private_channel_visible', got %q", fields["entity"])
	}
	if fields["reason"] != "master_unreachable" {
		t.Errorf("reason want 'master_unreachable', got %q", fields["reason"])
	}
	if fields["user_id"] != uint64(42) {
		t.Errorf("user_id want 42, got %v", fields["user_id"])
	}
}
