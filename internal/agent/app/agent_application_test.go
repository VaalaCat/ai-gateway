package app

import (
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
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
	rf := &agentproxy.RouteForwarder{}
	logger := zap.NewNop()
	cfg := &config.AgentRuntimeConfig{}
	pool := upstream.NewTransportPool(8, 4, 30*time.Second)

	aa := NewDefaultAgentApplication(store, rf, logger, cfg, pool)
	if aa.GetCache() == nil {
		t.Error("GetCache nil")
	}
	if aa.GetRouteForwarder() == nil {
		t.Error("GetRouteForwarder nil")
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
}

// TestDefaultAgentApplicationNilArgs 边界：全 nil 装配不应 panic；
// GetRouteForwarder 必须返回真 nil（typed-nil 接口陷阱防护）。
// 其余 nil 字段保持透传——下游访问时再 panic 是预期行为。
func TestDefaultAgentApplicationNilArgs(t *testing.T) {
	aa := NewDefaultAgentApplication(nil, nil, nil, nil, nil)
	if aa == nil {
		t.Fatal("NewDefaultAgentApplication returned nil with nil args")
	}
	// agentCache 适配器是个 struct{Store: nil}——本身非 nil，仅访问其方法时才会 panic
	if aa.GetCache() == nil {
		t.Error("GetCache should return non-nil adapter even with nil store")
	}
	// 关键防护：rf 为 nil 时接口字段必须是 untyped nil
	if aa.GetRouteForwarder() != nil {
		t.Error("GetRouteForwarder should return untyped nil when rf is nil (typed-nil interface guard)")
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
}

// TestDefaultAgentApplicationTypedNilForwarderGuard 显式验证 typed-nil guard：
// 直接传入 (*agentproxy.RouteForwarder)(nil)，GetRouteForwarder 必须返回 untyped nil。
// 没有 guard 时，Go 接口持有的 (T, nil) 是 non-nil 接口，会导致下游 ==nil 判断失效。
func TestDefaultAgentApplicationTypedNilForwarderGuard(t *testing.T) {
	var rf *agentproxy.RouteForwarder // typed nil
	aa := NewDefaultAgentApplication(nil, rf, nil, nil, nil)
	if aa.GetRouteForwarder() != nil {
		t.Error("typed-nil *RouteForwarder must become untyped nil through interface")
	}
}
