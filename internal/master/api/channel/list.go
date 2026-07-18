package channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.Channel], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var typeFilter *int
	if req.Type != "" {
		t, _ := strconv.Atoi(req.Type)
		typeFilter = &t
	}
	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	channels, total, err := q.Channel().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.ChannelListFilter{Search: req.Search, Type: typeFilter, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[models.Channel]{}, api.InternalError("list channels failed", err)
	}
	return api.PaginatedResponse[models.Channel]{Data: channels, Total: total, Page: page, PageSize: pageSize}, nil
}
