// Package app 装配 agent 端服务到 app.AgentApplication 接口。
//
// 设计目标：让 relay pipeline 仅依赖 internal/pkg/app 抽象接口，
// 而非具体的 *cache.Store / *agentproxy.RouteForwarder / *relay.transportPool。
// 本包就是把这些具体类型适配/封装成接口实现，供 server 装配使用。
package app

import (
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// agentCache 把 *cache.Store 适配到 app.AgentCache 接口。
//
// AgentCache = app.Store + MatchRoute；*cache.Store 已实现 app.Store，
// 但 MatchRoute 委托给嵌入的 *cache.RouteIndex.Match——这层薄适配补齐该方法。
// 通过 struct 嵌入复用 Store 的全部方法，零运行时开销。
type agentCache struct {
	*cache.Store
}

// newAgentCache 构造 agentCache 适配器。s 可为 nil（仅用于装配占位），
// 但调用 MatchRoute / Store.* 时会 panic——由调用方负责保证非 nil。
func newAgentCache(s *cache.Store) appkg.AgentCache {
	return &agentCache{Store: s}
}

// MatchRoute 委托到 Store.RouteIndex.Match。
// 用接口形式暴露给 relay，避免 relay 直接持有 *cache.Store 的具体字段。
func (a *agentCache) MatchRoute(tokenID uint, model string, channelIDs []uint) *models.AgentRoute {
	return a.Store.RouteIndex.Match(tokenID, model, channelIDs)
}
