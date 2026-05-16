package oauth_provider_admin

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, _ api.EmptyRequest) ([]models.OAuthProvider, error) {
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	providers, err := q.OAuthProvider().List()
	if err != nil {
		return nil, api.InternalError("list oauth providers failed", err)
	}
	for i := range providers {
		providers[i].ClientSecret = SecretMask
	}
	return providers, nil
}
