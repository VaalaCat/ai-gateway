package oauth_provider_admin

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.OAuthProvider], error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.OAuthProvider().GetByName(req.Name); err == nil {
		return api.Created[models.OAuthProvider]{}, api.ConflictError("oauth provider name already exists", nil)
	}

	proto := req.Protocol
	if proto == "" {
		proto = oauth.ProtocolOIDC
	}
	if proto != oauth.ProtocolOIDC && proto != oauth.ProtocolFeishu {
		return api.Created[models.OAuthProvider]{}, api.BadRequestError("invalid protocol: "+proto, nil)
	}

	p := models.OAuthProvider{
		Name:                  req.Name,
		Protocol:              proto,
		DisplayName:           req.DisplayName,
		Issuer:                req.Issuer,
		AuthorizationEndpoint: req.AuthorizationEndpoint,
		TokenEndpoint:         req.TokenEndpoint,
		UserinfoEndpoint:      req.UserinfoEndpoint,
		JWKSURI:               req.JWKSURI,
		ClientID:              req.ClientID,
		ClientSecret:          req.ClientSecret,
		Scopes:                req.Scopes,
		IconURL:               req.IconURL,
		Enabled:               req.Enabled,
	}

	if err := m.OAuthProvider().Create(&p); err != nil {
		return api.Created[models.OAuthProvider]{}, api.InternalError("create oauth provider failed", err)
	}

	p.ClientSecret = SecretMask
	return api.Created[models.OAuthProvider]{Value: p}, nil
}
