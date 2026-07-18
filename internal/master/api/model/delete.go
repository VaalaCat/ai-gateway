package model

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	mc, err := q.ModelConfig().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := m.ModelConfig().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete model failed", err)
	}
	if err := events.PublishModelDelete(context.Background(), c.GetBus(), *mc); err != nil {
		return api.StatusResponse{}, api.InternalError("publish model.delete failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
