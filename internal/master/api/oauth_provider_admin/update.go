package oauth_provider_admin

import (
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

var allowedUpdateKeys = map[string]bool{
	"display_name":           true,
	"issuer":                 true,
	"authorization_endpoint": true,
	"token_endpoint":         true,
	"userinfo_endpoint":      true,
	"jwks_uri":               true,
	"client_id":              true,
	"client_secret":          true,
	"scopes":                 true,
	"icon_url":               true,
	"enabled":                true,
	"default_group_id":       true,
}

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.OAuthProvider, error) {
	id64, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return models.OAuthProvider{}, api.BadRequestError("invalid id", err)
	}
	id := uint(id64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.OAuthProvider().GetByID(id); err != nil {
		return models.OAuthProvider{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := make(map[string]any, len(req.Fields))
	for k, v := range req.Fields {
		if !allowedUpdateKeys[k] {
			return models.OAuthProvider{}, api.BadRequestError("unknown field: "+k, nil)
		}
		updates[k] = v
	}
	if raw, ok := updates["default_group_id"]; ok {
		number, valid := raw.(float64)
		if !valid || number <= 0 || number > 1<<53-1 || number != float64(uint(number)) {
			return models.OAuthProvider{}, api.BadRequestError("default_group_id must be a positive integer", nil)
		}
		updates["default_group_id"] = uint(number)
	}
	if len(updates) == 0 {
		return models.OAuthProvider{}, api.BadRequestError("no updatable fields", nil)
	}

	if err := m.OAuthProvider().Update(id, updates); err != nil {
		if errors.Is(err, dao.ErrInvalidRegistrationGroup) {
			return models.OAuthProvider{}, api.BadRequestError(err.Error(), err)
		}
		return models.OAuthProvider{}, api.InternalError("update oauth provider failed", err)
	}

	p, err := q.OAuthProvider().GetByID(id)
	if err != nil {
		return models.OAuthProvider{}, api.InternalError("reload oauth provider failed", err)
	}
	p.ClientSecret = SecretMask
	return *p, nil
}
