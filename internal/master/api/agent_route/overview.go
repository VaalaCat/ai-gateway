package agent_route

import (
	"fmt"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type OverviewRequest struct {
	api.PaginationQuery
	Query      string `form:"q"`
	SourceType string `form:"source_type"`
	SourceID   string `form:"source_id"`
	Model      string `form:"model"`
	AgentID    string `form:"agent_id"`
}

func (h *Handler) Overview(c *app.Context, req OverviewRequest) (api.PaginatedResponse[OverviewItem], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	filter, err := req.Filter()
	if err != nil {
		return api.PaginatedResponse[OverviewItem]{}, err
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	routes, total, err := q.AgentRoute().ListOverview(
		dao.ListOptions{Page: page, PageSize: pageSize},
		filter,
	)
	if err != nil {
		return api.PaginatedResponse[OverviewItem]{}, api.InternalError("list agent routes failed", err)
	}

	items := make([]OverviewItem, len(routes))
	for i, r := range routes {
		items[i] = OverviewItem{
			ID:         r.ID,
			SourceType: r.SourceType,
			SourceID:   r.SourceID,
			SourceName: r.SourceName,
			Model:      r.Model,
			AgentID:    r.AgentID,
			AgentName:  r.AgentName,
			AgentTag:   r.AgentTag,
			Priority:   r.Priority,
			CreatedAt:  r.CreatedAt,
			UpdatedAt:  r.UpdatedAt,
		}
	}

	return api.PaginatedResponse[OverviewItem]{Data: items, Total: total, Page: page, PageSize: pageSize}, nil
}

func (r OverviewRequest) Filter() (dao.AgentRouteOverviewFilter, error) {
	filter := dao.AgentRouteOverviewFilter{
		Query: r.Query, SourceType: r.SourceType, Model: r.Model, AgentID: r.AgentID,
	}
	if r.SourceType != "" && !validAgentRouteSourceType(r.SourceType) {
		return filter, api.BadRequestError("source_type must be token, channel, api_service, or api_route", nil)
	}
	if r.SourceID == "" {
		return filter, nil
	}
	if r.SourceType == "" {
		return filter, api.BadRequestError("source_id requires source_type", nil)
	}
	id, err := strconv.ParseUint(r.SourceID, 10, 64)
	if err != nil || id == 0 || uint64(uint(id)) != id {
		return filter, api.BadRequestError("source_id must be a positive integer", fmt.Errorf("invalid source_id %q", r.SourceID))
	}
	sourceID := uint(id)
	filter.SourceID = &sourceID
	return filter, nil
}

func validAgentRouteSourceType(sourceType string) bool {
	switch sourceType {
	case models.AgentRouteSourceToken, models.AgentRouteSourceChannel,
		models.AgentRouteSourceAPIService, models.AgentRouteSourceAPIRoute:
		return true
	default:
		return false
	}
}
