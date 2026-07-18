package app

import (
	"context"
	"io"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/config"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// TestDefaultAgentApplicationImplementsInterface 编译期断言 *defaultAgentApplication
// 满足 app.AgentApplication。
func TestDefaultAgentApplicationImplementsInterface(t *testing.T) {
	var _ appkg.AgentApplication = (*defaultAgentApplication)(nil)
}

// TestDefaultAgentApplicationGetters happy path：完整装配后所有 Getter 都非 nil，
// 且返回的对象与传入的实例一致（指针等价）。
func TestDefaultAgentApplicationGetters(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	logger := zap.NewNop()
	cfg := &config.AgentRuntimeConfig{}
	pool := upstream.NewTransportPool(8, 4, 30*time.Second, upstream.KeepaliveConfig{Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3})
	bodyStore := &applicationTestBodyStore{}

	aa := NewDefaultAgentApplication(store, bodyStore, logger, cfg, pool)
	if aa.GetCache() == nil {
		t.Error("GetCache nil")
	}
	if aa.GetLogger() != logger {
		t.Error("GetLogger should return injected logger")
	}
	if aa.GetConfig() != cfg {
		t.Error("GetConfig should return injected cfg")
	}
	if aa.GetTransportPool() != pool {
		t.Error("GetTransportPool should return injected pool")
	}
	if aa.GetBodyStore() != bodyStore {
		t.Error("GetBodyStore should return injected body store")
	}
}

// TestDefaultAgentApplicationNilArgs 边界：全 nil 装配不应 panic；
// nil 字段保持透传——下游访问时再 panic 是预期行为。
func TestDefaultAgentApplicationNilArgs(t *testing.T) {
	aa := NewDefaultAgentApplication(nil, nil, nil, nil, nil)
	if aa == nil {
		t.Fatal("NewDefaultAgentApplication returned nil with nil args")
	}
	// agentCache 适配器是个 struct{Store: nil}——本身非 nil，仅访问其方法时才会 panic
	if aa.GetCache() == nil {
		t.Error("GetCache should return non-nil adapter even with nil store")
	}
	if aa.GetLogger() != nil {
		t.Error("GetLogger should be nil when not set")
	}
	if aa.GetConfig() != nil {
		t.Error("GetConfig should be nil when not set")
	}
	if aa.GetTransportPool() != nil {
		t.Error("GetTransportPool should be nil when not set")
	}
	if aa.GetBodyStore() != nil {
		t.Error("GetBodyStore should be nil when not set")
	}
}

type applicationTestBodyStore struct{}

func (*applicationTestBodyStore) Capture(context.Context, io.Reader, appkg.BodyLimits) (appkg.ReplayBody, error) {
	return nil, nil
}
