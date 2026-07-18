package user

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[dao.UserListRow], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var roleFilter *int
	if req.Role != "" {
		r, _ := strconv.Atoi(req.Role)
		roleFilter = &r
	}

	var groupIDFilter *uint
	if req.GroupID != "" {
		if v, err := strconv.ParseUint(req.GroupID, 10, 64); err == nil {
			u := uint(v)
			groupIDFilter = &u
		}
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	users, total, err := q.User().ListWithGroup(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.UserListFilter{Search: req.Search, Role: roleFilter, GroupID: groupIDFilter},
	)
	if err != nil {
		return api.PaginatedResponse[dao.UserListRow]{}, api.InternalError("list users failed", err)
	}
	for i := range users {
		users[i].Password = ""
	}
	return api.PaginatedResponse[dao.UserListRow]{Data: users, Total: total, Page: page, PageSize: pageSize}, nil
}
