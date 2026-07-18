package model

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

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.ModelConfig, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.ModelConfig().GetByID(uint(id)); err != nil {
		return models.ModelConfig{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")

	if err := m.ModelConfig().Update(uint(id), updates); err != nil {
		return models.ModelConfig{}, api.InternalError("update model failed", err)
	}

	mc, err := q.ModelConfig().GetByID(uint(id))
	if err != nil {
		return models.ModelConfig{}, api.InternalError("update model failed", err)
	}

	if err := events.PublishModelUpdate(context.Background(), c.GetBus(), *mc); err != nil {
		return models.ModelConfig{}, api.InternalError("publish model.update failed", err)
	}
	return *mc, nil
}
