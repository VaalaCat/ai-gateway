package api_openapi

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Import(c *app.Context, req ImportCommand) (api.Created[ImportResult], error) {
	result, err := h.Importer.Import(dao.NewContextWithContext(c.App, c.RequestContext()), req)
	if err != nil {
		return api.Created[ImportResult]{}, err
	}
	return api.Created[ImportResult]{Value: result}, nil
}
