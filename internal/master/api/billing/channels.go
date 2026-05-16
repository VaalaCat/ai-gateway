package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type ListChannelsRequest struct {
	api.PaginationQuery
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
	ChannelID string `form:"channel_id"`
}

type ChannelDailyRequest struct {
	ChannelID string `uri:"channel_id" binding:"required"`
	StartDate string `form:"start_date"`
	EndDate   string `form:"end_date"`
}

type ChannelDailyResponse struct {
	Items []dao.ChannelBillingDailyItem `json:"items"`
}

func (h *Handler) ListChannels(c *app.Context, req ListChannelsRequest) (api.PaginatedResponse[dao.ChannelBillingListItem], error) {
	channelID, err := parseOptionalUint(req.ChannelID)
	if err != nil {
		return api.PaginatedResponse[dao.ChannelBillingListItem]{}, api.BadRequestError("invalid channel_id", err)
	}
	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return api.PaginatedResponse[dao.ChannelBillingListItem]{}, api.BadRequestError("invalid date range", err)
	}

	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	q := dao.NewAdminQuery(dao.NewContext(c.App))
	items, total, err := q.Billing().ListChannelBilling(dao.ListOptions{Page: page, PageSize: pageSize}, dao.ChannelBillingListFilter{
		ChannelID: channelID,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return api.PaginatedResponse[dao.ChannelBillingListItem]{}, api.InternalError("list channel billing failed", err)
	}

	return api.PaginatedResponse[dao.ChannelBillingListItem]{
		Data:     items,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
	}, nil
}

func (h *Handler) ChannelDaily(c *app.Context, req ChannelDailyRequest) (ChannelDailyResponse, error) {
	channelID, err := parseRequiredUint(req.ChannelID)
	if err != nil {
		return ChannelDailyResponse{}, api.BadRequestError("invalid channel_id", err)
	}
	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return ChannelDailyResponse{}, api.BadRequestError("invalid date range", err)
	}

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	items, err := q.Billing().GetChannelDaily(channelID, dao.ChannelBillingListFilter{
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return ChannelDailyResponse{}, api.InternalError("get channel billing trend failed", err)
	}
	if items == nil {
		items = []dao.ChannelBillingDailyItem{}
	}
	return ChannelDailyResponse{Items: items}, nil
}
