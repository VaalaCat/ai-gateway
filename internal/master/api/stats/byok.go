package stats

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// BYOKOverviewResponse holds aggregated BYOK usage stats for the current user.
// Kept for backward-compat with the original /api/stats/byok-overview endpoint;
// new clients should use /api/private-channels/billing/overview which returns
// KPI + daily time series.
type BYOKOverviewResponse struct {
	Requests  int64 `json:"requests"`
	TotalCost int64 `json:"total_cost"`
}

// BYOKOverview returns private-channel usage summary for the authenticated user.
//
// Task 19+ moved the source of truth from usage_logs to channel_daily_billings.
// Reusing ListPrivateChannelDailyByOwner keeps this endpoint consistent with the
// new /private-channels/billing/* endpoints (same rows, same scoping rules) at
// the cost of one extra SUM in Go — the per-user row count is negligible.
func (h *Handler) BYOKOverview(c *app.Context, _ api.EmptyRequest) (BYOKOverviewResponse, error) {
	scope := middleware.GetScope(c.Context)
	if scope == nil {
		return BYOKOverviewResponse{}, api.UnauthorizedError("not authenticated")
	}

	q := dao.NewAdminQuery(dao.NewContext(c.App))
	rows, err := q.Billing().ListPrivateChannelDailyByOwner(scope.UserID, dao.ChannelBillingListFilter{})
	if err != nil {
		return BYOKOverviewResponse{}, api.InternalError("stats query failed", err)
	}

	var resp BYOKOverviewResponse
	for i := range rows {
		resp.Requests += rows[i].RequestCount
		resp.TotalCost += rows[i].TotalCost
	}
	return resp, nil
}
