package plan

import (
	"net/http"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// stubRoutingStore 让 chain 测试不依赖真实 *cache.Store。
type stubRoutingStore struct {
	user   map[string]*protocol.SyncedRouting // by name
	global map[string]*protocol.SyncedRouting
}

func (s *stubRoutingStore) ResolveRouting(name string, userID uint) *protocol.SyncedRouting {
	if userID > 0 && s.user != nil {
		if r, ok := s.user[name]; ok {
			return r
		}
	}
	return s.GetGlobalRouting(name)
}

func (s *stubRoutingStore) GetGlobalRouting(name string) *protocol.SyncedRouting {
	if s.global == nil {
		return nil
	}
	return s.global[name]
}

// stubAgentCache 用 embedded interface 技巧满足 app.AgentCache，
// 只覆盖 ResolveRouting / GetGlobalRouting / GetChannelsForModel /
// GetVisiblePrivateChannelsForUser 四个测试关心的方法。
// 其它方法访问会 nil 反射 panic——测试只触发上述方法故安全。
type stubAgentCache struct {
	app.AgentCache // embedded nil interface — promoted methods 不会被本测试调用
	rs             RoutingStore
	channels       []*models.Channel
	// privChannels: model → private channels，供 BYOK pool 测试使用。
	privChannels map[string][]*protocol.SyncedPrivateChannel
}

func (c *stubAgentCache) ResolveRouting(name string, userID uint) *protocol.SyncedRouting {
	if c.rs == nil {
		return nil
	}
	return c.rs.ResolveRouting(name, userID)
}

func (c *stubAgentCache) GetGlobalRouting(name string) *protocol.SyncedRouting {
	if c.rs == nil {
		return nil
	}
	return c.rs.GetGlobalRouting(name)
}

func (c *stubAgentCache) GetChannelsForModel(model string) []*models.Channel {
	return c.channels
}

func (c *stubAgentCache) GetVisiblePrivateChannelsForUser(userID uint, model string) []*protocol.SyncedPrivateChannel {
	if c.privChannels == nil {
		return nil
	}
	return c.privChannels[model]
}

// stubAgentApp 实现 app.AgentApplication，只暴露我们装配的 stubAgentCache。
// cfg 可选——非 nil 时 GetConfig 返回它，用于 Planner 测试注入 RetryMax。
// logger 可选——非 nil 时 GetLogger 返回它（log emit 用例注入 observer.New core）。
type stubAgentApp struct {
	cache  app.AgentCache
	cfg    *config.AgentRuntimeConfig
	logger *zap.Logger
}

func (s *stubAgentApp) GetCache() app.AgentCache              { return s.cache }
func (s *stubAgentApp) GetRouteForwarder() app.RouteForwarder { return nil }
func (s *stubAgentApp) GetLogger() *zap.Logger {
	if s.logger != nil {
		return s.logger
	}
	return zap.NewNop()
}
func (s *stubAgentApp) GetConfig() *config.AgentRuntimeConfig { return s.cfg }
func (s *stubAgentApp) GetTransportPool() app.TransportPool   { return stubTransportPool{} }

// stubTransportPool 只为满足 TransportPool 接口；测试不调用 Get / Invalidate。
type stubTransportPool struct{}

func (stubTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubTransportPool) Invalidate(uint, string)             {}

// 编译期断言：embedded interface 写法不会破坏接口实现。
var (
	_ app.AgentCache       = (*stubAgentCache)(nil)
	_ app.AgentApplication = (*stubAgentApp)(nil)
	_ RoutingStore         = (*stubRoutingStore)(nil)
	_ app.RouteForwarder   = (*agentproxy.RouteForwarder)(nil) // 仅引入 agentproxy 让 go.sum 链路完整
	_ app.TransportPool    = stubTransportPool{}
)

// newTestRelayContext 构造一个最小可用的 state.RelayContext，喂给 chain / pool 测试。
func newTestRelayContext(cache app.AgentCache, userModel string, ui *app.UserInfo, forcedID uint) *state.RelayContext {
	return &state.RelayContext{
		Context: &gin.Context{},
		Agent:   &stubAgentApp{cache: cache},
		Input: state.RelayInput{
			Model:           userModel,
			UserInfo:        ui,
			ForcedChannelID: forcedID,
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
}
