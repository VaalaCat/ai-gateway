package user

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	uid := uint(id)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	if _, err := q.User().GetByID(uid); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	err := dao.RunInTx[dao.Context](dao.NewContext(c.App), func(ctx dao.Context) error {
		m := dao.NewAdminMutation(ctx)
		if err := m.OAuthIdentity().DeleteByUserID(uid); err != nil {
			return err
		}
		return m.User().Delete(uid)
	})
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete user failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
