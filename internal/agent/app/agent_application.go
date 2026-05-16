package app

import (
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// defaultAgentApplication 是 app.AgentApplication 的默认实现，
// 持有 agent 端具体服务实例的接口引用。
//
// 通过此结构体，relay pipeline 只依赖 internal/pkg/app 抽象，
// server.go 装配阶段把具体类型注入；测试可替换任一字段。
type defaultAgentApplication struct {
	cache     appkg.AgentCache
	forwarder appkg.RouteForwarder
	logger    *zap.Logger
	cfg       *config.AgentRuntimeConfig
	transport appkg.TransportPool
}

// NewDefaultAgentApplication 装配 agent 服务到 app.AgentApplication 接口。
//
// 参数：
//   - store: agent 缓存（内部包装成 agentCache 暴露 MatchRoute）
//   - rf:    路由转发器；nil 时 GetRouteForwarder 返回真 nil（防 typed-nil 接口陷阱）
//   - logger / cfg / pool: 直接持有
//
// 所有依赖均允许为 nil，构造本身不 panic——具体调用各 Getter 后用其方法可能 panic，
// 由调用方负责装配完整性。
func NewDefaultAgentApplication(
	store *cache.Store,
	rf *agentproxy.RouteForwarder,
	logger *zap.Logger,
	cfg *config.AgentRuntimeConfig,
	pool appkg.TransportPool,
) appkg.AgentApplication {
	// typed-nil 接口陷阱：var x app.RouteForwarder = (*agentproxy.RouteForwarder)(nil)
	// 得到的 x != nil（因为有 type info）。这里显式判 nil 让 Getter 行为可预期。
	var fwd appkg.RouteForwarder
	if rf != nil {
		fwd = rf
	}
	return &defaultAgentApplication{
		cache:     newAgentCache(store),
		forwarder: fwd,
		logger:    logger,
		cfg:       cfg,
		transport: pool,
	}
}

func (a *defaultAgentApplication) GetCache() appkg.AgentCache              { return a.cache }
func (a *defaultAgentApplication) GetRouteForwarder() appkg.RouteForwarder { return a.forwarder }
func (a *defaultAgentApplication) GetLogger() *zap.Logger                  { return a.logger }
func (a *defaultAgentApplication) GetConfig() *config.AgentRuntimeConfig   { return a.cfg }
func (a *defaultAgentApplication) GetTransportPool() appkg.TransportPool   { return a.transport }
