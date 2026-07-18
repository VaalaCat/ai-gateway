package app

import (
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	agentlegacy "github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/config"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// defaultAgentApplication 是 app.AgentApplication 的默认实现，
// 持有 agent 端具体服务实例的接口引用。
//
// 通过此结构体，relay pipeline 只依赖 internal/pkg/app 抽象，
// server.go 装配阶段把具体类型注入；测试可替换任一字段。
type defaultAgentApplication struct {
	cache           appkg.AgentCache
	bodyStore       appkg.BodyStore
	logger          *zap.Logger
	cfg             *config.AgentRuntimeConfig
	transport       appkg.TransportPool
	legacyTransport *agentlegacy.TransportOwner
}

// NewDefaultAgentApplication 装配 agent 服务到 app.AgentApplication 接口。
//
// 参数：
//   - store: agent 缓存（内部包装成 agentCache 暴露 per-attempt route finders）
//   - logger / cfg / pool: 直接持有
//
// 所有依赖均允许为 nil，构造本身不 panic——具体调用各 Getter 后用其方法可能 panic，
// 由调用方负责装配完整性。
func NewDefaultAgentApplication(
	store *cache.Store,
	bodyStore appkg.BodyStore,
	logger *zap.Logger,
	cfg *config.AgentRuntimeConfig,
	pool appkg.TransportPool,
	legacyOwners ...*agentlegacy.TransportOwner,
) appkg.AgentApplication {
	var legacyOwner *agentlegacy.TransportOwner
	if len(legacyOwners) > 0 {
		legacyOwner = legacyOwners[0]
	}
	return &defaultAgentApplication{
		cache:           newAgentCache(store),
		bodyStore:       bodyStore,
		logger:          logger,
		cfg:             cfg,
		transport:       pool,
		legacyTransport: legacyOwner,
	}
}

func (a *defaultAgentApplication) GetCache() appkg.AgentCache            { return a.cache }
func (a *defaultAgentApplication) GetBodyStore() appkg.BodyStore         { return a.bodyStore }
func (a *defaultAgentApplication) GetLogger() *zap.Logger                { return a.logger }
func (a *defaultAgentApplication) GetConfig() *config.AgentRuntimeConfig { return a.cfg }
func (a *defaultAgentApplication) GetTransportPool() appkg.TransportPool { return a.transport }
func (a *defaultAgentApplication) GetLegacyTransportOwner() *agentlegacy.TransportOwner {
	return a.legacyTransport
}
func (a *defaultAgentApplication) RelayTimeout() time.Duration {
	return time.Duration(a.cfg.Runtime.RelayTimeout) * time.Second
}
