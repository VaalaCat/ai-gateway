package token

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	token, err := q.Token().GetByID(uint(id))
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if scope != nil && !scope.IsAdmin && scope.UserID != token.UserID {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if err := m.Token().Delete(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("delete token failed", err)
	}

	if err := events.PublishTokenDelete(context.Background(), c.GetBus(), *token); err != nil {
		return api.StatusResponse{}, api.InternalError("publish token.delete failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
