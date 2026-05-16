package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

type RebuildRequest struct {
	StartDate string `json:"start_date"`
	EndDate   string `json:"end_date"`
}

type RebuildResponse struct {
	Status       string `json:"status"`
	ReplayedLogs int64  `json:"replayed_logs"`
}

func (h *Handler) Rebuild(c *app.Context, req RebuildRequest) (RebuildResponse, error) {
	if req.StartDate == "" && req.EndDate == "" {
		return RebuildResponse{}, api.BadRequestError("at least one of start_date or end_date is required", nil)
	}

	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return RebuildResponse{}, api.BadRequestError("invalid date range", err)
	}

	m := dao.NewAdminMutation(dao.NewContext(c.App))
	result, err := m.Billing().RebuildDailyRollups(dao.BillingRebuildFilter{
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return RebuildResponse{}, api.InternalError("rebuild billing rollups failed", err)
	}

	return RebuildResponse{
		Status:       "ok",
		ReplayedLogs: result.ReplayedLogs,
	}, nil
}
