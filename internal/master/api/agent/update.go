package agent

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Agent, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.Agent().GetByID(uint(id)); err != nil {
		return models.Agent{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	if v, ok := updates["status"]; ok {
		if err := api.ValidateStatusValue(v); err != nil {
			return models.Agent{}, api.BadRequestError(err.Error(), err)
		}
	}

	if err := m.Agent().Update(uint(id), updates); err != nil {
		return models.Agent{}, api.InternalError("update agent failed", err)
	}

	agent, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return models.Agent{}, api.InternalError("update agent failed", err)
	}
	events.PublishAgentUpdate(context.Background(), c.GetBus(), *agent)
	return *agent, nil
}
