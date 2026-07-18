package agent_route

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.AgentRoute], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	var sourceIDFilter *uint
	if req.SourceID != "" {
		id, _ := strconv.ParseUint(req.SourceID, 10, 64)
		uid := uint(id)
		sourceIDFilter = &uid
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	routes, total, err := q.AgentRoute().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.AgentRouteListFilter{SourceType: req.SourceType, SourceID: sourceIDFilter},
	)
	if err != nil {
		return api.PaginatedResponse[models.AgentRoute]{}, api.InternalError("list agent routes failed", err)
	}
	return api.PaginatedResponse[models.AgentRoute]{Data: routes, Total: total, Page: page, PageSize: pageSize}, nil
}
