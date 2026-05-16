package stats

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Trend(c *app.Context, req TrendRequest) (TrendResponse, error) {
	scope := middleware.GetScope(c.Context)

	days := 30
	if req.Days != "" {
		if d, err := strconv.Atoi(req.Days); err == nil && d > 0 {
			days = d
		}
	}
	if days > 90 {
		days = 90
	}

	var userID *uint
	userID = middleware.ScopedUserID(scope, nil)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	items, err := q.Stats().GetTrend(days, userID)
	if err != nil {
		return TrendResponse{}, api.InternalError("trend query failed", err)
	}
	if items == nil {
		items = []dao.TrendItem{}
	}
	return TrendResponse{Items: items}, nil
}
