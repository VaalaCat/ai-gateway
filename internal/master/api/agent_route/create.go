package agent_route

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.AgentRoute], error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	route := models.AgentRoute{
		SourceType: req.SourceType,
		SourceID:   req.SourceID,
		Model:      req.Model,
		AgentID:    req.AgentID,
		AgentTag:   req.AgentTag,
	}
	normalizeAgentRouteSelectors(&route)
	if err := validateAgentRoute(q, route); err != nil {
		return api.Created[models.AgentRoute]{}, err
	}
	route.Priority = route.CalcPriority()

	if err := m.AgentRoute().Create(&route); err != nil {
		if dao.IsAgentRouteUniqueConflict(err) {
			return api.Created[models.AgentRoute]{}, api.ConflictError("agent route already exists", err)
		}
		return api.Created[models.AgentRoute]{}, api.InternalError("create agent route failed", err)
	}

	_ = events.PublishAgentRouteCreate(c.RequestContext(), c.GetBus(), route)
	return api.Created[models.AgentRoute]{Value: route}, nil
}
