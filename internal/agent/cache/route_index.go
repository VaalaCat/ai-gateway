package cache

import (
	"sort"
	"sync"
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type routeIndexState struct {
	routes    map[uint]models.AgentRoute
	byToken   map[uint][]uint
	byChannel map[uint][]uint
}

// RouteIndex 封装 Agent 路由规则的存储和索引。
type RouteIndex struct {
	writeMu sync.Mutex
	state   atomic.Pointer[routeIndexState]
}

// NewRouteIndex 创建空的 RouteIndex。
func NewRouteIndex() *RouteIndex {
	ri := &RouteIndex{}
	ri.state.Store(newRouteIndexState())
	return ri
}

// FindTokenRoute 查找当前 token scope 下的精确模型或默认路由。
func (ri *RouteIndex) FindTokenRoute(tokenID uint, realModel string) *models.AgentRoute {
	return ri.find(ri.state.Load(), "token", tokenID, realModel)
}

// FindAdminChannelRoute 查找当前 admin channel scope 下的精确模型或默认路由。
func (ri *RouteIndex) FindAdminChannelRoute(channelID uint, realModel string) *models.AgentRoute {
	return ri.find(ri.state.Load(), "channel", channelID, realModel)
}

func (ri *RouteIndex) find(state *routeIndexState, sourceType string, sourceID uint, realModel string) *models.AgentRoute {
	if state == nil {
		return nil
	}

	var routeIDs []uint
	switch sourceType {
	case "token":
		routeIDs = state.byToken[sourceID]
	case "channel":
		routeIDs = state.byChannel[sourceID]
	default:
		return nil
	}

	if route := matchRoute(state, routeIDs, realModel, true); route != nil {
		return route
	}
	return matchRoute(state, routeIDs, realModel, false)
}

// Replace 全量替换路由索引，并在完整构建后一次性发布。
func (ri *RouteIndex) Replace(routes []*models.AgentRoute) {
	next := buildRouteIndexState(routes)

	ri.writeMu.Lock()
	ri.state.Store(next)
	ri.writeMu.Unlock()
}

// Load 保留全量加载的兼容入口。
func (ri *RouteIndex) Load(routes []*models.AgentRoute) {
	ri.Replace(routes)
}

// Put 单条增改。
func (ri *RouteIndex) Put(route *models.AgentRoute) {
	if route == nil {
		return
	}

	ri.writeMu.Lock()
	defer ri.writeMu.Unlock()

	ri.putLocked(route)
}

// Delete 单条删除。
func (ri *RouteIndex) Delete(routeID uint) {
	ri.writeMu.Lock()
	defer ri.writeMu.Unlock()

	ri.deleteLocked(routeID)
}

// Apply 应用 create/update/delete 路由变更，未知动作不修改索引。
func (ri *RouteIndex) Apply(action string, route *models.AgentRoute) {
	if route == nil {
		return
	}
	switch action {
	case events.ActionCreate, events.ActionUpdate, events.ActionDelete:
	default:
		return
	}

	ri.writeMu.Lock()
	defer ri.writeMu.Unlock()

	if action == events.ActionDelete {
		ri.deleteLocked(route.ID)
		return
	}
	ri.putLocked(route)
}

func (ri *RouteIndex) putLocked(route *models.AgentRoute) {
	next := cloneRouteIndexState(ri.state.Load())
	if old, ok := next.routes[route.ID]; ok {
		next.removeFromSourceIndex(old)
	}

	next.routes[route.ID] = *route
	next.addToSourceIndex(route.ID)
	ri.state.Store(next)
}

func (ri *RouteIndex) deleteLocked(routeID uint) {
	current := ri.state.Load()
	if current == nil {
		return
	}
	route, ok := current.routes[routeID]
	if !ok {
		return
	}

	next := cloneRouteIndexState(current)
	next.removeFromSourceIndex(route)
	delete(next.routes, routeID)
	ri.state.Store(next)
}

func newRouteIndexState() *routeIndexState {
	return &routeIndexState{
		routes:    make(map[uint]models.AgentRoute),
		byToken:   make(map[uint][]uint),
		byChannel: make(map[uint][]uint),
	}
}

func buildRouteIndexState(routes []*models.AgentRoute) *routeIndexState {
	state := newRouteIndexState()
	for _, route := range routes {
		if route == nil {
			continue
		}
		state.routes[route.ID] = *route
	}
	for routeID, route := range state.routes {
		switch route.SourceType {
		case "token":
			state.byToken[route.SourceID] = append(state.byToken[route.SourceID], routeID)
		case "channel":
			state.byChannel[route.SourceID] = append(state.byChannel[route.SourceID], routeID)
		}
	}
	for _, routeIDs := range state.byToken {
		sortRouteIDsByPriority(routeIDs, state.routes)
	}
	for _, routeIDs := range state.byChannel {
		sortRouteIDsByPriority(routeIDs, state.routes)
	}
	return state
}

func cloneRouteIndexState(current *routeIndexState) *routeIndexState {
	next := newRouteIndexState()
	if current == nil {
		return next
	}

	for routeID, route := range current.routes {
		next.routes[routeID] = route
	}
	for sourceID, routeIDs := range current.byToken {
		next.byToken[sourceID] = append([]uint(nil), routeIDs...)
	}
	for sourceID, routeIDs := range current.byChannel {
		next.byChannel[sourceID] = append([]uint(nil), routeIDs...)
	}
	return next
}

func (state *routeIndexState) addToSourceIndex(routeID uint) {
	route := state.routes[routeID]
	switch route.SourceType {
	case "token":
		state.byToken[route.SourceID] = append(state.byToken[route.SourceID], routeID)
		sortRouteIDsByPriority(state.byToken[route.SourceID], state.routes)
	case "channel":
		state.byChannel[route.SourceID] = append(state.byChannel[route.SourceID], routeID)
		sortRouteIDsByPriority(state.byChannel[route.SourceID], state.routes)
	}
}

func (state *routeIndexState) removeFromSourceIndex(route models.AgentRoute) {
	switch route.SourceType {
	case "token":
		removeRouteID(state.byToken, route.SourceID, route.ID)
	case "channel":
		removeRouteID(state.byChannel, route.SourceID, route.ID)
	}
}

func removeRouteID(index map[uint][]uint, sourceID, routeID uint) {
	routeIDs := index[sourceID]
	for i, candidateID := range routeIDs {
		if candidateID != routeID {
			continue
		}
		routeIDs = append(routeIDs[:i], routeIDs[i+1:]...)
		if len(routeIDs) == 0 {
			delete(index, sourceID)
		} else {
			index[sourceID] = routeIDs
		}
		return
	}
}

func matchRoute(state *routeIndexState, routeIDs []uint, model string, exact bool) *models.AgentRoute {
	for _, routeID := range routeIDs {
		route := state.routes[routeID]
		if exact {
			if route.Model == "" || route.Model != model {
				continue
			}
		} else if route.Model != "" {
			continue
		}

		matched := route
		return &matched
	}
	return nil
}

func sortRouteIDsByPriority(routeIDs []uint, routes map[uint]models.AgentRoute) {
	sort.Slice(routeIDs, func(i, j int) bool {
		left := routes[routeIDs[i]]
		right := routes[routeIDs[j]]
		if left.Priority != right.Priority {
			return left.Priority > right.Priority
		}
		return routeIDs[i] < routeIDs[j]
	})
}

// CacheName / CacheStat 实现 NamedCacheStat：索引类只报条目数（无 LRU 命中语义）。
func (ri *RouteIndex) CacheName() string { return "route_index" }

func (ri *RouteIndex) CacheStat() protocol.CacheEntityStats {
	state := ri.state.Load()
	size := 0
	if state != nil {
		size = len(state.routes)
	}
	return protocol.CacheEntityStats{Kind: "index", Size: size}
}
