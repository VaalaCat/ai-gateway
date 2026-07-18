package oauth

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) ListPublicProviders(c *app.Context, _ api.EmptyRequest) ([]PublicProvider, error) {
	q := dao.NewAdminQuery(dao.NewContextWithContext(h.App, c.RequestContext())).OAuthProvider()
	list, err := q.ListEnabled()
	if err != nil {
		return nil, api.InternalError("list providers failed", err)
	}
	out := make([]PublicProvider, 0, len(list))
	for _, p := range list {
		out = append(out, PublicProvider{
			Name:        p.Name,
			DisplayName: p.DisplayName,
			IconURL:     p.IconURL,
		})
	}
	return out, nil
}
