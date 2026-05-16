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

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	logs, total, err := q.UsageLog().List(
		dao.ListOptions{Page: page, PageSize: pageSize},
		dao.UsageLogListFilter{
			UserID:    userIDFilter,
			TokenID:   tokenIDFilter,
			ChannelID: channelIDFilter,
			ModelName: req.ModelName,
			Status:    statusFilter,
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
