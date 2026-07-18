package model

import (
	"context"
	"errors"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"gorm.io/gorm"
)

func (h *Handler) Sync(c *app.Context, _ api.EmptyRequest) (SyncResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	channels, err := q.Channel().ListAll()
	if err != nil {
		return SyncResponse{}, api.InternalError("sync models failed", err)
	}

	modelSet := make(map[string]bool)
	for _, ch := range channels {
		if ch.Models == "" {
			continue
		}
		for _, mn := range strings.Split(ch.Models, ",") {
			mn = strings.TrimSpace(mn)
			if mn != "" {
				modelSet[mn] = true
			}
		}
	}

	created := 0
	for modelName := range modelSet {
		_, err := q.ModelConfig().GetByModelName(modelName)
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return SyncResponse{}, api.InternalError("sync models failed", err)
		}
		mc := models.ModelConfig{ModelName: modelName, InputPrice: 0, OutputPrice: 0, Status: 1}
		if err := m.ModelConfig().Create(&mc); err == nil {
			created++
			if err := events.PublishModelCreate(context.Background(), c.GetBus(), mc); err != nil {
				return SyncResponse{}, api.InternalError("publish model.create failed", err)
			}
		}
	}

	removed := 0
	allModels, err := q.ModelConfig().ListAll()
	if err != nil {
		return SyncResponse{}, api.InternalError("sync models failed", err)
	}
	for _, mc := range allModels {
		if !modelSet[mc.ModelName] {
			if err := m.ModelConfig().Delete(mc.ID); err == nil {
				removed++
				events.PublishModelDelete(context.Background(), c.GetBus(), mc)
			}
		}
	}

	return SyncResponse{Created: created, Removed: removed}, nil
}
