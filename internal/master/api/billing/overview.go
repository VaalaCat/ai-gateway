package billing

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Overview(c *app.Context, req OverviewRequest) (*dao.BillingOverview, error) {
	scope := middleware.GetScope(c.Context)

	reqUserID, err := parseOptionalUint(req.UserID)
	if err != nil {
		return nil, api.BadRequestError("invalid user_id", err)
	}
	startDate, endDate, err := normalizeDateRange(req.StartDate, req.EndDate)
	if err != nil {
		return nil, api.BadRequestError("invalid date range", err)
	}

	filterUserID := middleware.ScopedUserID(scope, reqUserID)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	overview, err := q.Billing().GetBillingOverview(dao.TokenBillingListFilter{
		UserID:    filterUserID,
		StartDate: startDate,
		EndDate:   endDate,
	})
	if err != nil {
		return nil, api.InternalError("billing overview query failed", err)
	}
	return overview, nil
}
