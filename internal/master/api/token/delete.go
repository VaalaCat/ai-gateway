package token

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	token, err := q.Token().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if scope != nil && !scope.IsAdmin && scope.UserID != token.UserID {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	deletedRoutings, err := m.Token().DeleteWithRoutings(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete token failed", err)
	}

	publishFailures := 0
	var lastPublishErr error
	for _, routing := range deletedRoutings {
		if err := events.Publish(c.RequestContext(), c.GetBus(), events.ModelRoutingDeleteTopic, routing); err != nil {
			publishFailures++
			lastPublishErr = err
		}
	}
	if err := events.PublishTokenDelete(c.RequestContext(), c.GetBus(), *token); err != nil {
		publishFailures++
		lastPublishErr = err
	}
	if publishFailures > 0 && c.Logger != nil {
		// behavior change: token and routing rows are already deleted atomically.
		c.Logger.Warn("publish token deletion invalidation failed after commit",
			zap.Uint("token_id", token.ID),
			zap.Int("failed_events", publishFailures),
			zap.Error(lastPublishErr))
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
