package genericapi

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type APIAgentRouteFinder interface {
	FindTokenRoute(tokenID uint, model string) *models.AgentRoute
	FindAPIRouteRoute(routeID uint) *models.AgentRoute
	FindAPIServiceRoute(serviceID uint) *models.AgentRoute
}

type AgentPicker struct {
	routes       APIAgentRouteFinder
	agents       agentproxy.AgentLookup
	localAgentID string
}

func NewAgentPicker(routes APIAgentRouteFinder, agents agentproxy.AgentLookup, localAgentID string) *AgentPicker {
	return &AgentPicker{routes: routes, agents: agents, localAgentID: localAgentID}
}

func (p *AgentPicker) Pick(tokenID, routeID, serviceID uint, requestID string) (AgentPick, error) {
	if p == nil || p.routes == nil || p.agents == nil || p.localAgentID == "" || tokenID == 0 || routeID == 0 || serviceID == 0 || requestID == "" {
		return AgentPick{}, fmt.Errorf("%w: invalid agent picker input", ErrExecutionUnavailable)
	}
	route := p.firstRoute(tokenID, routeID, serviceID)
	if route == nil {
		return AgentPick{ExecutionAgentID: p.localAgentID, AgentRoutePath: app.RoutePathLocal}, nil
	}
	if route.Model != "" {
		return AgentPick{}, fmt.Errorf("%w: API agent route has a model", ErrExecutionUnavailable)
	}
	target, err := agentproxy.SelectTarget(app.AgentSelector{AgentID: route.AgentID, AgentTag: route.AgentTag}, requestID, route.ID, p.agents)
	if err != nil {
		return AgentPick{}, fmt.Errorf("%w: %v", ErrExecutionUnavailable, err)
	}
	path := app.RoutePath("")
	if target.AgentID == p.localAgentID {
		path = app.RoutePathLocal
	}
	return AgentPick{ExecutionAgentID: target.AgentID, AgentRouteID: route.ID, AgentRoutePath: path, Target: target}, nil
}

func (p *AgentPicker) firstRoute(tokenID, routeID, serviceID uint) *models.AgentRoute {
	if route := p.routes.FindTokenRoute(tokenID, ""); route != nil && route.Model == "" {
		return route
	}
	if route := p.routes.FindAPIRouteRoute(routeID); route != nil {
		return route
	}
	return p.routes.FindAPIServiceRoute(serviceID)
}
