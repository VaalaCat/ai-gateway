package relay

import (
	"testing"
	"time"

	"go.uber.org/zap"

	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
)

// TestNewHandler_WiresStageFields 验证规范构造器装配了 Agent + 4 个 stage 字段。
// Handler 只暴露 App / Agent / 4 个 stage 实例，依赖由 backend / stage 通过这两个容器接口间接取，
// 测试只验顶层字段是否到位。
func TestNewHandler_WiresStageFields(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	logger := zap.NewNop()
	pool := upstream.NewTransportPool(8, 4, 5*time.Second, upstream.KeepaliveConfig{Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3})
	cfg := &config.AgentRuntimeConfig{
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
		Relay:   config.RelayConfig{Timeout: 30},
	}
	agentApp := agentappkg.NewDefaultAgentApplication(store, nil, logger, cfg, pool)
	bus := eventbus.NewMemoryBus()

	h := NewHandler(bus, agentApp, TestDispatcherFactory(agentApp), nil)

	if h == nil {
		t.Fatal("NewHandler returned nil")
	}
	if h.Agent == nil {
		t.Error("Agent field not wired")
	}
	if h.planner == nil {
		t.Error("planner not constructed")
	}
	if h.executor == nil {
		t.Error("executor not constructed")
	}
	if h.publisher == nil {
		t.Error("publisher not constructed")
	}
	// 间接验证：Agent 的 config / transport pool 仍能从 GetXxx 接口拿到，
	// 且构造器把传入的 Runtime 配置原样接上。
	if h.Agent.GetConfig() == nil || h.Agent.GetConfig().Runtime.RelayTimeout != 30 {
		t.Errorf("Agent.GetConfig().Runtime.RelayTimeout = %v, want 30",
			h.Agent.GetConfig())
	}
	if h.Agent.GetLogger() == nil {
		t.Error("Agent.GetLogger() returned nil")
	}
	if h.Agent.GetTransportPool() == nil {
		t.Error("Agent.GetTransportPool() returned nil")
	}
	// EventBus 由 publish.Publisher 持有；通过 trace_integration_test.go 里实际发布
	// usage event 的用例间接覆盖。
}

// TestNewHandler_NilArgsDoesNotPanic 验证两边都 nil 时构造不 panic（只是用时 panic）。
func TestNewHandler_NilArgsDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("NewHandler(nil, nil, nil) panicked: %v", r)
		}
	}()
	h := NewHandler(nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewHandler(nil, nil, nil) returned nil; should still construct")
	}
	if h.planner == nil || h.executor == nil || h.publisher == nil {
		t.Error("stage fields not initialised under nil args")
	}
}
