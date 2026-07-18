package agent

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

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Agent, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	agent, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return models.Agent{}, api.NotFoundError(consts.ErrNotFound)
	}

	_, updates, err := mergeAgentPatch(*agent, req.AgentPatch)
	if err != nil {
		return models.Agent{}, api.BadRequestError(err.Error(), nil)
	}

	if req.RelayMode != nil || req.RelayURI != nil {
		updated, err := m.Agent().UpdateIfRelayConfigMatches(
			uint(id),
			agent.RelayMode,
			agent.RelayURI,
			updates,
		)
		if err != nil {
			return models.Agent{}, api.InternalError("update agent failed", err)
		}
		if !updated {
			if _, err := q.Agent().GetByID(uint(id)); err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return models.Agent{}, api.NotFoundError(consts.ErrNotFound)
				}
				return models.Agent{}, api.InternalError("update agent failed", err)
			}
			return models.Agent{}, api.ConflictError("agent was modified concurrently", nil)
		}
	} else if err := m.Agent().Update(uint(id), updates); err != nil {
		return models.Agent{}, api.InternalError("update agent failed", err)
	}

	agent, err = q.Agent().GetByID(uint(id))
	if err != nil {
		return models.Agent{}, api.InternalError("update agent failed", err)
	}
	events.PublishAgentUpdate(c.RequestContext(), c.GetBus(), *agent)
	return *agent, nil
}
