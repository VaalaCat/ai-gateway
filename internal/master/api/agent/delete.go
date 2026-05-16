package agent

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

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	agent, err := q.Agent().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := m.Agent().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete agent failed", err)
	}
	events.PublishAgentRevoked(context.Background(), c.GetBus(), *agent)
	events.PublishAgentDelete(context.Background(), c.GetBus(), *agent)
	return api.StatusResponse{Status: "deleted"}, nil
}
