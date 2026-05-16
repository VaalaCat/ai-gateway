package model_routing

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid id", err)
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	r, qErr := q.ModelRouting().GetByID(uint(id))
	if qErr != nil || r == nil {
		return api.StatusResponse{}, api.NotFoundError("routing not found")
	}

	m := dao.NewAdminMutation(daoCtx)
	if ve := m.ModelRouting().Delete(uint(id)); ve != nil {
		return api.StatusResponse{}, validateErrorToAPI(ve)
	}

	h.publishEvent(events.ActionDelete, r)
	return api.StatusResponse{Status: "deleted"}, nil
}
