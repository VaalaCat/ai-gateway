package cache

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
)

// RouteIndex 封装 Agent 路由规则的存储和索引。
type RouteIndex struct {
	routes    utils.SyncMap[uint, *models.AgentRoute]
	byToken   utils.SyncMap[uint, []*models.AgentRoute]
	byChannel utils.SyncMap[uint, []*models.AgentRoute]
}

// NewRouteIndex 创建空的 RouteIndex。
func NewRouteIndex() *RouteIndex {
	return &RouteIndex{}
}

// Match 按优先级匹配路由规则：
// 1. token + model 精确匹配 (priority 100)
// 2. token 默认 (priority 90)
// 3. channel + model 精确匹配 (priority 80)
// 4. channel 默认 (priority 70)
func (ri *RouteIndex) Match(tokenID uint, model string, channelIDs []uint) *models.AgentRoute {
	// 1. Token + model
	if routes, ok := ri.byToken.Load(tokenID); ok {
		for _, r := range routes {
			if r.Model != "" && r.Model == model {
				return r
			}
		}
		// 2. Token default
		for _, r := range routes {
			if r.Model == "" {
				return r
			}
		}
	}

	// 3. Channel + model
	for _, chID := range channelIDs {
		if routes, ok := ri.byChannel.Load(chID); ok {
			for _, r := range routes {
				if r.Model != "" && r.Model == model {
					return r
				}
			}
		}
	}

	// 4. Channel default
	for _, chID := range channelIDs {
		if routes, ok := ri.byChannel.Load(chID); ok {
			for _, r := range routes {
				if r.Model == "" {
					return r
				}
			}
		}
	}

	return nil
}

// Load 全量加载：清空旧数据后重建索引。
func (ri *RouteIndex) Load(routes []*models.AgentRoute) {
	// 清空
	ri.routes.Range(func(k uint, _ *models.AgentRoute) bool {
		ri.routes.Delete(k)
		return true
	})
	ri.byToken.Range(func(k uint, _ []*models.AgentRoute) bool {
		ri.byToken.Delete(k)
		return true
	})
	ri.byChannel.Range(func(k uint, _ []*models.AgentRoute) bool {
		ri.byChannel.Delete(k)
		return true
	})

	// 重建
	for _, r := range routes {
		ri.routes.Store(r.ID, r)
	}
	ri.rebuildIndex()
}

// Put 单条增改。
func (ri *RouteIndex) Put(route *models.AgentRoute) {
	ri.routes.Store(route.ID, route)
	ri.rebuildIndex()
}

// Delete 单条删除。
func (ri *RouteIndex) Delete(routeID uint) {
	ri.routes.Delete(routeID)
	ri.rebuildIndex()
}

// rebuildIndex 从 routes 重建 byToken 和 byChannel 索引。
func (ri *RouteIndex) rebuildIndex() {
	tokenIdx := make(map[uint][]*models.AgentRoute)
	channelIdx := make(map[uint][]*models.AgentRoute)

	ri.routes.Range(func(_ uint, r *models.AgentRoute) bool {
		switch r.SourceType {
		case "token":
			tokenIdx[r.SourceID] = append(tokenIdx[r.SourceID], r)
		case "channel":
			channelIdx[r.SourceID] = append(channelIdx[r.SourceID], r)
		}
		return true
	})

	// 清空旧索引
	ri.byToken.Range(func(k uint, _ []*models.AgentRoute) bool {
		ri.byToken.Delete(k)
		return true
	})
	ri.byChannel.Range(func(k uint, _ []*models.AgentRoute) bool {
		ri.byChannel.Delete(k)
		return true
	})

	// 写入新索引（按 priority DESC 排序）
	for id, routes := range tokenIdx {
		sortRoutesByPriority(routes)
		ri.byToken.Store(id, routes)
	}
	for id, routes := range channelIdx {
		sortRoutesByPriority(routes)
		ri.byChannel.Store(id, routes)
	}
}

func sortRoutesByPriority(routes []*models.AgentRoute) {
	for i := 0; i < len(routes); i++ {
		for j := i + 1; j < len(routes); j++ {
			if routes[j].Priority > routes[i].Priority {
				routes[i], routes[j] = routes[j], routes[i]
			}
		}
	}
}
