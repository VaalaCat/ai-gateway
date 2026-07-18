package oauth_provider_admin

import (
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
	if len(updates) == 0 {
		return models.OAuthProvider{}, api.BadRequestError("no updatable fields", nil)
	}

	if err := m.OAuthProvider().Update(id, updates); err != nil {
		return models.OAuthProvider{}, api.InternalError("update oauth provider failed", err)
	}

	p, err := q.OAuthProvider().GetByID(id)
	if err != nil {
		return models.OAuthProvider{}, api.InternalError("reload oauth provider failed", err)
	}
	p.ClientSecret = SecretMask
	return *p, nil
}
