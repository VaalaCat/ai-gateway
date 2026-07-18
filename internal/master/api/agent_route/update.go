package agent_route

import (
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/gorm"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.AgentRoute, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.AgentRoute().GetByID(uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AgentRoute{}, api.NotFoundError(consts.ErrNotFound)
		}
		return models.AgentRoute{}, api.InternalError("get agent route failed", err)
	}

	merged := req.Merge(*existing)
	if err := validateAgentRoute(q, merged); err != nil {
		return models.AgentRoute{}, err
	}
	if err := m.AgentRoute().Update(&merged); err != nil {
		switch {
		case errors.Is(err, dao.ErrAgentRouteNotFound):
			return models.AgentRoute{}, api.NotFoundError(consts.ErrNotFound)
		case dao.IsAgentRouteUniqueConflict(err):
			return models.AgentRoute{}, api.ConflictError("agent route already exists", err)
		default:
			return models.AgentRoute{}, api.InternalError("update agent route failed", err)
		}
	}

	route, err := q.AgentRoute().GetByID(uint(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return models.AgentRoute{}, api.NotFoundError(consts.ErrNotFound)
		}
		return models.AgentRoute{}, api.InternalError("get updated route failed", err)
	}

	_ = events.PublishAgentRouteUpdate(c.RequestContext(), c.GetBus(), *route)
	return *route, nil
}
