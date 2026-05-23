package private_channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AdminListRequest is the admin cross-user listing query.
// Includes the same filters as portal ListRequest plus an OwnerID filter
// (e.g., from /admin/byok?owner_id=42).
type AdminListRequest struct {
	api.PaginationQuery
	Search  string `form:"search"`
	Type    string `form:"type"`
	Status  string `form:"status"`
	OwnerID string `form:"owner_id"`
}

// AdminList returns private channels across all users (admin-only).
// DetailResponse never contains plaintext Key — only KeyLast4.
func (h *Handler) AdminList(c *app.Context, req AdminListRequest) (api.PaginatedResponse[DetailResponse], error) {
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
	if req.OwnerID != "" {
		if o, err := strconv.ParseUint(req.OwnerID, 10, 64); err == nil {
			u := uint(o)
			filter.OwnerID = &u
		}
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	rows, total, err := q.PrivateChannel().ListAcrossOwners(
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
