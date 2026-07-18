package oauth_provider_admin

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req GetRequest) (models.OAuthProvider, error) {
	id64, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return models.OAuthProvider{}, api.BadRequestError("invalid id", err)
	}

	q := dao.NewAdminQuery(dao.NewContextWithContext(c.App, c.RequestContext()))
	p, err := q.OAuthProvider().GetByID(uint(id64))
	if err != nil {
		return models.OAuthProvider{}, api.NotFoundError(consts.ErrNotFound)
	}
	p.ClientSecret = SecretMask
	return *p, nil
}
