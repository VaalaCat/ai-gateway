package private_channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) PortalList(c *app.Context, req ListRequest) (api.PaginatedResponse[DetailResponse], error) {
	if c.UserInfo == nil {
		return api.PaginatedResponse[DetailResponse]{}, api.UnauthorizedError("not authenticated")
	}
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	filter := dao.PrivateChannelFilter{Search: req.Search}
	if req.Type != "" {
		if t, err := strconv.Atoi(req.Type); err == nil {
			filter.Type = &t
		}
	}
	if req.Status != "" {
		if s, err := strconv.Atoi(req.Status); err == nil {
			filter.Status = &s
		}
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	rows, total, err := q.PrivateChannel().ListOwnedBy(
		c.UserInfo.UserID,
		dao.ListOptions{Page: page, PageSize: pageSize},
		filter,
	)
	if err != nil {
		return api.PaginatedResponse[DetailResponse]{}, api.InternalError("list private channels", err)
	}

	data := make([]DetailResponse, 0, len(rows))
	for i := range rows {
		data = append(data, toDetailResponse(&rows[i]))
	}
	return api.PaginatedResponse[DetailResponse]{Data: data, Total: total, Page: page, PageSize: pageSize}, nil
}
