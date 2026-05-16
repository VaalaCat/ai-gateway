package user_group

import (
	"context"
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"gorm.io/datatypes"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.UserGroup], error) {
	if req.Models != "" {
		var patterns []string
		if err := json.Unmarshal([]byte(req.Models), &patterns); err != nil {
			return api.Created[models.UserGroup]{}, api.BadRequestError("invalid models JSON: "+err.Error(), err)
		}
		if err := utils.ValidateModelPatterns(patterns); err != nil {
			return api.Created[models.UserGroup]{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
		}
	}

	if req.AllowedChannelIDs != nil {
		if err := api.ValidateAllowedChannelIDs(*req.AllowedChannelIDs); err != nil {
			return api.Created[models.UserGroup]{}, api.BadRequestError(err.Error(), err)
		}
	}

	g := models.UserGroup{
		Name:        req.Name,
		Description: req.Description,
		Status:      req.Status,
		Models:      req.Models,
	}
	if g.Status == 0 {
		g.Status = consts.StatusEnabled
	}
	if req.AllowedChannelIDs != nil {
		g.AllowedChannelIDs = datatypes.JSONSlice[uint](*req.AllowedChannelIDs)
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.UserGroup().GetByName(req.Name); err == nil {
		return api.Created[models.UserGroup]{}, api.ConflictError("user group name already exists", nil)
	}

	if err := m.UserGroup().Create(&g); err != nil {
		return api.Created[models.UserGroup]{}, api.InternalError("create user group failed", err)
	}

	if h.Bus != nil {
		_ = events.PublishEntity(context.Background(), h.Bus, events.EntityUserGroup, events.ActionCreate, g)
	}

	return api.Created[models.UserGroup]{Value: g}, nil
}
