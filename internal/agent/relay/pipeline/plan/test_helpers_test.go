package plan

import (
	"context"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// stubRoutingStore 让 chain 测试不依赖真实 *cache.Store。
type stubRoutingStore struct {
	user       map[string]*protocol.SyncedRouting // by name
	token      map[string]*protocol.SyncedRouting
	global     map[string]*protocol.SyncedRouting
	realModels map[string]bool // name → 是否真实模型
}

func (s *stubRoutingStore) ResolveRouting(ctx context.Context, name string, owner protocol.RoutingOwner) *protocol.SyncedRouting {
	if owner.TokenID > 0 && s.token != nil {
		if r, ok := s.token[name]; ok {
			return r
		}
	}
	if owner.UserID > 0 && s.user != nil {
		if r, ok := s.user[name]; ok {
			return r
		}
	}
	return s.GetGlobalRouting(ctx, name)
}

func (s *stubRoutingStore) GetGlobalRouting(_ context.Context, name string) *protocol.SyncedRouting {
	if s.global == nil {
		return nil
	}
	return s.global[name]
}

func (s *stubRoutingStore) HasRealModel(name string) bool {
	return s.realModels[name]
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
	settings     settings.AgentSettings
	// modelConfigs / users 供 quotaFilter 测试注入可控定价与余额。
	modelConfigs map[string]*models.ModelConfig
	users        map[uint]*protocol.SyncedUser
	// getUserCalls 记录 GetUser 被调用次数，供 quotaFilter 测试断言"未定价/全免费不读余额"。
	getUserCalls int
	getUser      func(context.Context, uint) *protocol.SyncedUser
}

func (c *stubAgentCache) Settings() settings.AgentSettings { return c.settings }

func (c *stubAgentCache) GetModelConfig(name string) *models.ModelConfig {
	if c.modelConfigs == nil {
		return nil
	}
	return c.modelConfigs[name]
}

func (c *stubAgentCache) GetUser(ctx context.Context, id uint) *protocol.SyncedUser {
	c.getUserCalls++
	if c.getUser != nil {
		return c.getUser(ctx, id)
	}
	if c.users == nil {
		return nil
	}
	return c.users[id]
}

func (c *stubAgentCache) ResolveRouting(ctx context.Context, name string, owner protocol.RoutingOwner) *protocol.SyncedRouting {
	if c.rs == nil {
		return nil
	}
	return c.rs.ResolveRouting(ctx, name, owner)
}

func (c *stubAgentCache) GetGlobalRouting(ctx context.Context, name string) *protocol.SyncedRouting {
	if c.rs == nil {
		return nil
	}
	return c.rs.GetGlobalRouting(ctx, name)
}

func (c *stubAgentCache) GetChannelsForModel(model string) []*models.Channel {
	return c.channels
}

// HasRealModel: stub 简化——只看 c.channels 是否非空，不区分 name。
// 现有用例 c.channels 为 nil 故恒 false；若将来需要 name-aware 行为，改为代理 c.rs.HasRealModel(name)。
func (c *stubAgentCache) HasRealModel(name string) bool {
	return len(c.channels) > 0
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

func (s *stubAgentApp) GetCache() app.AgentCache    { return s.cache }
func (s *stubAgentApp) GetBodyStore() app.BodyStore { return nil }
func (s *stubAgentApp) GetLogger() *zap.Logger {
	if s.logger != nil {
		return s.logger
	}
	return zap.NewNop()
}
func (s *stubAgentApp) GetConfig() *config.AgentRuntimeConfig { return s.cfg }
func (s *stubAgentApp) GetTransportPool() app.TransportPool   { return stubTransportPool{} }
func (s *stubAgentApp) RelayTimeout() time.Duration           { return 0 }

// stubTransportPool 只为满足 TransportPool 接口；测试不调用 Get / Invalidate。
type stubTransportPool struct{}

func (stubTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubTransportPool) Invalidate(uint, string)             {}
func (stubTransportPool) CloseIdleConnections()               {}

// 编译期断言：embedded interface 写法不会破坏接口实现。
var (
	_ app.AgentCache       = (*stubAgentCache)(nil)
	_ app.AgentApplication = (*stubAgentApp)(nil)
	_ RoutingStore         = (*stubRoutingStore)(nil)
	_ app.TransportPool    = stubTransportPool{}
)

// newTestRelayContext 构造一个最小可用的 state.RelayContext，喂给 chain / pool 测试。
func newTestRelayContext(cache app.AgentCache, userModel string, ui *app.UserInfo, forcedID uint) *state.RelayContext {
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	return &state.RelayContext{
		Context: &gin.Context{Request: request},
		Agent:   &stubAgentApp{cache: cache},
		Input: state.RelayInput{
			Model:           userModel,
			UserInfo:        ui,
			ForcedChannelID: forcedID,
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
}

func resolveToRealModel(store RoutingStore, ref string, userID uint, walk *ResolveCtx) string {
	return ResolveToRealModel(context.Background(), store, ref, protocol.RoutingOwner{UserID: userID}, walk)
}
