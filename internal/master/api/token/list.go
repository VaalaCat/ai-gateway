package token

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.Token], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	scope := middleware.GetScope(c.Context)

	var reqUserID *uint
	if req.UserID != "" {
		u, _ := strconv.ParseUint(req.UserID, 10, 64)
		uid := uint(u)
		reqUserID = &uid
	}

	userIDFilter := middleware.ScopedUserID(scope, reqUserID)

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	tokens, total, err := q.Token().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.TokenListFilter{Search: req.Search, UserID: userIDFilter, Status: statusFilter},
	)
	if err != nil {
		return api.PaginatedResponse[models.Token]{}, api.InternalError("list tokens failed", err)
	}
	return api.PaginatedResponse[models.Token]{Data: tokens, Total: total, Page: page, PageSize: pageSize}, nil
}
