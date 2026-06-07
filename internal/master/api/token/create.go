package token

import (
	"context"
	"encoding/json"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/utils"
	"gorm.io/datatypes"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.Token], error) {
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	key := req.Key
	if key == "" {
		key = GenerateKey()
	}

	token := models.Token{
		Key:          key,
		Name:         req.Name,
		Status:       consts.StatusEnabled,
		TraceEnabled: req.TraceEnabled,
	}

	if scope != nil && !scope.IsAdmin {
		// Normal user: require template_id, inherit models/expiry from template
		if req.TemplateID == nil {
			return api.Created[models.Token]{}, api.BadRequestError("template_id is required", nil)
		}
		tpl, err := q.TokenTemplate().GetByID(*req.TemplateID)
		if err != nil {
			return api.Created[models.Token]{}, api.BadRequestError("invalid template_id", err)
		}
		if tpl.Status != consts.StatusEnabled {
			return api.Created[models.Token]{}, api.BadRequestError("template is disabled", nil)
		}
		groupID, gErr := api.ResolveGroupID(q, scope.UserID)
		if gErr != nil {
			return api.Created[models.Token]{}, api.InternalError("load user failed", gErr)
		}
		if !tpl.AllowsGroup(groupID) {
			return api.Created[models.Token]{}, api.ForbiddenError("template not available for your group")
		}

		token.UserID = scope.UserID
		token.TemplateID = req.TemplateID
		token.Models = tpl.Models
		token.AllowedChannelIDs = tpl.AllowedChannelIDs // snapshot whitelist from template
		if tpl.ExpiryDays > 0 {
			token.ExpiredAt = time.Now().AddDate(0, 0, tpl.ExpiryDays).Unix()
		} else {
			token.ExpiredAt = -1
		}
	} else {
		// Admin: use existing behavior with models validation
		if req.Models != "" {
			var patterns []string
			if err := json.Unmarshal([]byte(req.Models), &patterns); err != nil {
				return api.Created[models.Token]{}, api.BadRequestError("invalid models JSON: must be a JSON array of strings", err)
			}
			if err := utils.ValidateModelPatterns(patterns); err != nil {
				return api.Created[models.Token]{}, api.BadRequestError("invalid model pattern: "+err.Error(), err)
			}
		}
		if req.AllowedChannelIDs != nil {
			if err := validateAllowedChannelIDsForToken(*req.AllowedChannelIDs); err != nil {
				return api.Created[models.Token]{}, api.BadRequestError(err.Error(), err)
			}
		}
		token.UserID = req.UserID
		token.TemplateID = req.TemplateID
		token.ExpiredAt = req.ExpiredAt
		token.Models = req.Models
		if req.AllowedChannelIDs != nil {
			token.AllowedChannelIDs = datatypes.JSONSlice[uint](*req.AllowedChannelIDs)
		}
		if token.ExpiredAt == 0 {
			token.ExpiredAt = -1
		}
	}

	if err := m.Token().Create(&token); err != nil {
		return api.Created[models.Token]{}, api.ConflictError(err.Error(), err)
	}
	if err := events.PublishTokenCreate(context.Background(), c.GetBus(), token); err != nil {
		return api.Created[models.Token]{}, api.InternalError("publish token.create failed", err)
	}
	return api.Created[models.Token]{Value: token}, nil
}

func validateAllowedChannelIDsForToken(ids []uint) error {
	return api.ValidateAllowedChannelIDs(ids)
}
