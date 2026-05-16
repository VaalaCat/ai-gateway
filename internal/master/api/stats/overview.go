package stats

import (
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Overview(c *app.Context, _ api.EmptyRequest) (OverviewResponse, error) {
	scope := middleware.GetScope(c.Context)
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	if scope != nil && !scope.IsAdmin {
		// Normal user: personal stats
		uid := scope.UserID
		uidPtr := &uid

		_, userTokenCount, err := q.Token().List(dao.ListOptions{Page: 1, PageSize: 1}, dao.TokenListFilter{UserID: uidPtr})
		if err != nil {
			return OverviewResponse{}, api.InternalError("stats query failed", err)
		}

		_, userLogCount, err := q.UsageLog().List(dao.ListOptions{Page: 1, PageSize: 1}, dao.UsageLogListFilter{UserID: uidPtr})
		if err != nil {
			return OverviewResponse{}, api.InternalError("stats query failed", err)
		}

		totalCost, err := q.Stats().GetTotalCost(dao.UsageLogListFilter{UserID: uidPtr})
		if err != nil {
			return OverviewResponse{}, api.InternalError("stats query failed", err)
		}

		user, err := q.User().GetByID(uid)
		if err != nil {
			return OverviewResponse{}, api.InternalError("stats query failed", err)
		}

		return OverviewResponse{
			Tokens:    userTokenCount,
			UsageLogs: userLogCount,
			TotalCost: totalCost,
			Quota:     &user.Quota,
			UsedQuota: &user.UsedQuota,
		}, nil
	}

	// Admin: global stats
	overview, err := q.Stats().GetOverview()
	if err != nil {
		return OverviewResponse{}, api.InternalError("stats query failed", err)
	}

	connected := 0
	if h.ConnectedCount != nil {
		connected = h.ConnectedCount()
	}

	userCount := overview.UserCount
	channelCount := overview.ChannelCount
	modelCount := overview.ModelConfigCount
	agentCount := overview.AgentCount

	return OverviewResponse{
		Users:           &userCount,
		Channels:        &channelCount,
		Models:          &modelCount,
		Agents:          &agentCount,
		ConnectedAgents: &connected,
		Tokens:          overview.TokenCount,
		UsageLogs:       overview.UsageLogCount,
		TotalCost:       overview.TotalCost,
	}, nil
}
