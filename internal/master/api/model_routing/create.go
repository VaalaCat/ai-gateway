package model_routing

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (models.ModelRouting, error) {
	membersJSON, err := json.Marshal(req.Members)
	if err != nil {
		return models.ModelRouting{}, api.BadRequestError("invalid members", err)
	}

	r := models.ModelRouting{
		Name:    req.Name,
		Scope:   req.Scope,
		UserID:  req.UserID,
		Members: string(membersJSON),
		Enabled: req.Enabled,
		Remark:  req.Remark,
	}
	return h.createRouting(c, &r)
}

func (h *Handler) createRouting(c *app.Context, r *models.ModelRouting) (models.ModelRouting, error) {
	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)

	if ve := m.ModelRouting().Create(r); ve != nil {
		return models.ModelRouting{}, validateErrorToAPI(ve)
	}

	h.publishEvent(c, events.ActionCreate, r)
	return *r, nil
}
