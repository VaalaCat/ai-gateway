package model

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.ModelConfig], error) {
	mc := models.ModelConfig{
		ModelName:   req.ModelName,
		InputPrice:  req.InputPrice,
		OutputPrice: req.OutputPrice,
		Status:      1,
	}

	daoCtx := dao.NewContext(c.App)
	m := dao.NewAdminMutation(daoCtx)

	if err := m.ModelConfig().Create(&mc); err != nil {
		return api.Created[models.ModelConfig]{}, api.ConflictError(err.Error(), err)
	}
	if err := events.PublishModelCreate(context.Background(), c.GetBus(), mc); err != nil {
		return api.Created[models.ModelConfig]{}, api.InternalError("publish model.create failed", err)
	}
	return api.Created[models.ModelConfig]{Value: mc}, nil
}
