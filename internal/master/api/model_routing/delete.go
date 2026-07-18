package model_routing

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid id", err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	r, qErr := q.ModelRouting().GetByID(uint(id))
	if qErr != nil || r == nil || r.Scope == models.RoutingScopeToken {
		return api.StatusResponse{}, api.NotFoundError("routing not found")
	}
	return h.deleteRouting(c, r)
}

func (h *Handler) deleteRouting(c *app.Context, r *models.ModelRouting) (api.StatusResponse, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)
	if ve := m.ModelRouting().Delete(r.ID); ve != nil {
		return api.StatusResponse{}, validateErrorToAPI(ve)
	}

	h.publishEvent(c, events.ActionDelete, r)
	return api.StatusResponse{Status: "deleted"}, nil
}
