package log

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) List(c *app.Context, req ListRequest) (api.PaginatedResponse[models.UsageLog], error) {
	page, pageSize := api.NormalizePagination(req.Page, req.PageSize)
	scope := middleware.GetScope(c.Context)

	tw := req.TimeWindowQuery.ToTimeWindow()
	if err := tw.Validate(MaxLogsListDays); err != nil {
		return api.PaginatedResponse[models.UsageLog]{}, api.BadRequestError("range out of bounds", err)
	}

	var reqUserID *uint
	if req.UserID != "" {
		u, _ := strconv.ParseUint(req.UserID, 10, 64)
		uid := uint(u)
		reqUserID = &uid
	}

	userIDFilter := middleware.ScopedUserID(scope, reqUserID)

	var tokenIDFilter *uint
	if req.TokenID != "" {
		t, _ := strconv.ParseUint(req.TokenID, 10, 64)
		tid := uint(t)
		tokenIDFilter = &tid
	}

	// Normal users cannot filter by channel_id
	var channelIDFilter *uint
	if req.ChannelID != "" && scope != nil && scope.IsAdmin {
		ch, _ := strconv.ParseUint(req.ChannelID, 10, 64)
		cid := uint(ch)
		channelIDFilter = &cid
	}

	var statusFilter *int
	if req.Status != "" {
		s, _ := strconv.Atoi(req.Status)
		statusFilter = &s
	}

	var pcIDFilter *uint
	if req.PrivateChannelID != "" {
		pcid, err := strconv.ParseUint(req.PrivateChannelID, 10, 64)
		if err != nil {
			return api.PaginatedResponse[models.UsageLog]{}, api.BadRequestError("invalid private_channel_id", err)
		}
		pc := uint(pcid)
		pcIDFilter = &pc
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	// 非 admin 用户传 private_channel_id 必须是自己拥有的；
	// 不存在或不属于自己一律 403（不区分，避免存在性探测）。
	if pcIDFilter != nil && scope != nil && !scope.IsAdmin {
		pc, err := q.PrivateChannel().GetByID(*pcIDFilter)
		if err != nil || pc == nil || pc.OwnerID != scope.UserID {
			return api.PaginatedResponse[models.UsageLog]{}, api.ForbiddenError("private channel not owned")
		}
	}

	logs, total, err := q.UsageLog().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.UsageLogListFilter{
			TimeWindow:       tw,
			UserID:           userIDFilter,
			TokenID:          tokenIDFilter,
			ChannelID:        channelIDFilter,
			ModelName:        req.ModelName,
			Status:           statusFilter,
			PrivateChannelID: pcIDFilter,
		},
	)
	if err != nil {
		return api.PaginatedResponse[models.UsageLog]{}, api.InternalError("list logs failed", err)
	}

	// Hide channel_id for normal users
	if scope != nil && !scope.IsAdmin {
		for i := range logs {
			logs[i].ChannelID = 0
		}
	}

	return api.PaginatedResponse[models.UsageLog]{Data: logs, Total: total, Page: page, PageSize: pageSize}, nil
}
