package agent_route

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type OverviewRequest struct {
	api.PaginationQuery
	SourceType string `form:"source_type"`
}

func (h *Handler) Overview(c *app.Context, req OverviewRequest) (api.PaginatedResponse[OverviewItem], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	routes, total, err := q.AgentRoute().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.AgentRouteListFilter{SourceType: req.SourceType},
	)
	if err != nil {
		return api.PaginatedResponse[OverviewItem]{}, api.InternalError("list agent routes failed", err)
	}

	items := make([]OverviewItem, len(routes))
	for i, r := range routes {
		item := OverviewItem{
			ID:         r.ID,
			SourceType: r.SourceType,
			SourceID:   r.SourceID,
			Model:      r.Model,
			AgentID:    r.AgentID,
			AgentTag:   r.AgentTag,
			Priority:   r.Priority,
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
		}

		switch r.SourceType {
		case "token":
			if t, err := q.Token().GetByID(r.SourceID); err == nil {
				item.SourceName = t.Name
			}
		case "channel":
			if ch, err := q.Channel().GetByID(r.SourceID); err == nil {
				item.SourceName = ch.Name
			}
		}

		if r.AgentID != "" {
			if a, err := q.Agent().GetByAgentID(r.AgentID); err == nil {
				item.AgentName = a.Name
			}
		}

		items[i] = item
	}

	return api.PaginatedResponse[OverviewItem]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}
