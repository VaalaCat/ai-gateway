package model

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.ModelConfig], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	configs, total, err := q.ModelConfig().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.ModelConfigListFilter{Search: req.Search, PriceFilter: req.PriceFilter},
	)
	if err != nil {
		return api.PaginatedResponse[models.ModelConfig]{}, api.InternalError("list models failed", err)
	}
	return api.PaginatedResponse[models.ModelConfig]{Data: configs, Total: total, Page: page, PageSize: pageSize}, nil
}
