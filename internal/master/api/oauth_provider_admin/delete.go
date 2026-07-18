package oauth_provider_admin

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Delete(c *app.Context, req DeleteRequest) (api.StatusResponse, error) {
	id64, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid id", err)
	}
	id := uint(id64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.OAuthProvider().GetByID(id); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	if err := m.OAuthProvider().Delete(id); err != nil {
		return api.StatusResponse{}, api.InternalError("delete oauth provider failed", err)
	}

	return api.StatusResponse{Status: "ok"}, nil
}
