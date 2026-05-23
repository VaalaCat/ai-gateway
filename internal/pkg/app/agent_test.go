package app

import (
	"context"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// Compile-time check that the interface set exists and has expected methods.
func TestAgentApplicationInterfaceShape(t *testing.T) {
	var _ AgentApplication = (*stubAgentApp)(nil)
	var _ AgentCache = (*stubAgentCache)(nil)
	var _ RouteForwarder = (*stubRouteForwarder)(nil)
	var _ TransportPool = (*stubTransportPool)(nil)
}

// TestAgentApplicationNilReturns: AgentApplication 是纯依赖容器接口，
// stub 返回 nil 必须可被直接调用（不应 panic）。
func TestAgentApplicationNilReturns(t *testing.T) {
	var app AgentApplication = stubAgentApp{}
	if app.GetCache() != nil {
		t.Fatal("stub GetCache should be nil")
	}
	if app.GetRouteForwarder() != nil {
		t.Fatal("stub GetRouteForwarder should be nil")
	}
	if app.GetLogger() != nil {
		t.Fatal("stub GetLogger should be nil")
	}
	if app.GetConfig() != nil {
		t.Fatal("stub GetConfig should be nil")
	}
	if app.GetTransportPool() != nil {
		t.Fatal("stub GetTransportPool should be nil")
	}
}

// TestAgentCacheEmbedsStore: 边界 — AgentCache 必须 *组合* Store，
// 否则 master 端的 Store-only 方法（GetToken 等）就拿不到。
func TestAgentCacheEmbedsStore(t *testing.T) {
	var c AgentCache = stubAgentCache{}
	// MatchRoute 是 AgentCache 自带的
	if got := c.MatchRoute(1, "gpt-4", nil); got != nil {
		t.Fatal("stub MatchRoute should be nil")
	}
	// GetToken 来自嵌入的 Store
	if got := c.GetToken(context.Background(), "k"); got != nil {
		t.Fatal("stub Store.GetToken should be nil")
	}
}

// --- stubs ---

type stubAgentApp struct{}

func (stubAgentApp) GetCache() AgentCache                  { return nil }
func (stubAgentApp) GetRouteForwarder() RouteForwarder     { return nil }
func (stubAgentApp) GetLogger() *zap.Logger                { return nil }
func (stubAgentApp) GetConfig() *config.AgentRuntimeConfig { return nil }
func (stubAgentApp) GetTransportPool() TransportPool       { return nil }

type stubAgentCache struct {
	stubStore
}

func (stubAgentCache) MatchRoute(uint, string, []uint) *models.AgentRoute { return nil }

type stubRouteForwarder struct{}

func (stubRouteForwarder) ForwardByRoute(*gin.Context, *models.AgentRoute) (bool, error) {
	return false, nil
}

type stubTransportPool struct{}

func (stubTransportPool) Get(*models.Channel) *http.Transport { return nil }
func (stubTransportPool) Invalidate(uint, string)             {}

// stubStore 满足 Store 接口的最小实现（全部返回零值）。
type stubStore struct{}

func (stubStore) GetToken(context.Context, string) *models.Token        { return nil }
func (stubStore) SetToken(*models.Token)                                {}
func (stubStore) DeleteToken(string)                                    {}
func (stubStore) GetTokenByID(context.Context, uint) *models.Token      { return nil }
func (stubStore) DeleteTokenByID(uint)                                  {}
func (stubStore) LoadTokens([]models.Token)                             {}
func (stubStore) TokenCount() int                                       { return 0 }
func (stubStore) GetChannel(uint) *models.Channel                       { return nil }
func (stubStore) SetChannel(*models.Channel)                            {}
func (stubStore) DeleteChannel(uint)                                    {}
func (stubStore) LoadChannels([]models.Channel)                         {}
func (stubStore) ChannelCount() int                                     { return 0 }
func (stubStore) GetModelConfig(string) *models.ModelConfig             { return nil }
func (stubStore) SetModelConfig(*models.ModelConfig)                    {}
func (stubStore) DeleteModelConfig(string)                              {}
func (stubStore) LoadModelConfigs([]models.ModelConfig)                 {}
func (stubStore) ModelConfigCount() int                                 { return 0 }
func (stubStore) GetChannelsForModel(string) []*models.Channel                          { return nil }
func (stubStore) RebuildModelIndex()                                                    {}
func (stubStore) GetAllModelNames() []string                                            { return nil }
func (stubStore) GetVisiblePrivateChannelsForUser(uint, string) []*protocol.SyncedPrivateChannel {
	return nil
}
func (stubStore) ListVisibleBYOKModelNamesForUser(uint) []string        { return nil }
func (stubStore) GetAgent(string) *models.Agent                         { return nil }
func (stubStore) SetAgent(*models.Agent)                                {}
func (stubStore) UpdateAgentAutoAddresses(string, []agentproxy.Address) {}
func (stubStore) DeleteAgent(string)                                    {}
func (stubStore) GetAgentsByTag(string) []*models.Agent                 { return nil }
func (stubStore) GetAllAgents() []*models.Agent                         { return nil }
func (stubStore) LoadAgents([]models.Agent)                             {}
func (stubStore) AgentCount() int                                       { return 0 }
func (stubStore) Version() int64                                        { return 0 }
func (stubStore) SetVersion(int64)                                      {}
func (stubStore) LoadSettings([]models.Setting)                         {}
func (stubStore) Settings() settings.AgentSettings                     { return settings.AgentSettings{} }
func (stubStore) TraceMaxBodySize() int                                 { return 0 }
func (stubStore) FallbackSleepMs() int                                  { return 0 }
func (stubStore) GetSystemTestToken() *models.Token                     { return nil }
func (stubStore) HandleSyncEvent(string, string, []byte)                {}
