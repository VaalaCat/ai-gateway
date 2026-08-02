package channel

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.Channel, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.Channel().GetByID(uint(id))
	if err != nil {
		return models.Channel{}, api.NotFoundError(consts.ErrNotFound)
	}

	patch, err := ParseChannelPatch(req.Fields)
	if err != nil {
		return models.Channel{}, api.BadRequestError(err.Error(), err)
	}
	candidate := *existing
	if err := patch.Apply(&candidate); err != nil {
		return models.Channel{}, api.BadRequestError(err.Error(), err)
	}
	if err := m.Channel().Update(existing.ID, patch.Assignments()); err != nil {
		return models.Channel{}, api.InternalError("update channel failed", err)
	}

	channel, err := q.Channel().GetByID(existing.ID)
	if err != nil {
		return models.Channel{}, api.InternalError("update channel failed", err)
	}

	if err := events.PublishChannelUpdate(context.Background(), c.GetBus(), *channel); err != nil {
		// behavior change: a committed update remains successful when cache notification is delayed.
		if c.Logger != nil {
			c.Logger.Warn("publish channel.update failed after commit", zap.Uint("channel_id", channel.ID), zap.Error(err))
		}
	}
	return *channel, nil
}
