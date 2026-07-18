package user_group

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[Item], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	groups, total, err := q.UserGroup().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.UserGroupListFilter{Search: req.Search, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[Item]{}, api.InternalError("list user groups failed", err)
	}

	items := make([]Item, len(groups))
	for i, g := range groups {
		n, _ := q.UserGroup().CountUsers(g.ID)
		items[i] = Item{UserGroup: g, UserCount: n}
	}

	return api.PaginatedResponse[Item]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}
