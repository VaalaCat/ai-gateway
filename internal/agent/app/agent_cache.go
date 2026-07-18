// Package app 装配 agent 端服务到 app.AgentApplication 接口。
//
// 设计目标：让 relay pipeline 仅依赖 internal/pkg/app 抽象接口，
// 而非具体的 *cache.Store / *relay transport pool。
// 本包就是把这些具体类型适配/封装成接口实现，供 server 装配使用。
package app

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// agentCache 把 *cache.Store 适配到 app.AgentCache 接口。
//
// AgentCache = app.Store + route finder；*cache.Store 已实现 app.Store，
// route finder 委托给嵌入的 *cache.RouteIndex——这层薄适配补齐这些方法。
// 通过 struct 嵌入复用 Store 的全部方法，零运行时开销。
type agentCache struct {
	*cache.Store
}

// newAgentCache 构造 agentCache 适配器。s 可为 nil（仅用于装配占位），
// 但调用 route finder / Store.* 时会 panic——由调用方负责保证非 nil。
func newAgentCache(s *cache.Store) appkg.AgentCache {
	return &agentCache{Store: s}
}

// FindTokenRoute 委托到 Store.RouteIndex.FindTokenRoute。
func (a *agentCache) FindTokenRoute(tokenID uint, realModel string) *models.AgentRoute {
	return a.Store.RouteIndex.FindTokenRoute(tokenID, realModel)
}

// FindAdminChannelRoute 委托到 Store.RouteIndex.FindAdminChannelRoute。
func (a *agentCache) FindAdminChannelRoute(channelID uint, realModel string) *models.AgentRoute {
	return a.Store.RouteIndex.FindAdminChannelRoute(channelID, realModel)
}
