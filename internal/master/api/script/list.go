package script

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.AdminScript], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	daoCtx := dao.NewContext(c.App)
	items, total, err := dao.NewAdminQuery(daoCtx).AdminScript().List(
		dao.ListOptions{Page: page, PageSize: pageSize}, req.Search,
	)
	if err != nil {
		return api.PaginatedResponse[models.AdminScript]{}, api.InternalError("list scripts failed", err)
	}
	return api.PaginatedResponse[models.AdminScript]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}
