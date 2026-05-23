package user

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
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

	// Deleting a user must purge every artifact that lets them — or a stale
	// share — touch the system again. In particular the BYOK private_channels
	// hold encrypted key material that must not survive the user row
	// (GDPR / SOC2 right-to-erasure), and any share row whose target is this
	// user becomes orphan grant data.
	err := dao.RunInTx[dao.Context](dao.NewContext(c.App), func(ctx dao.Context) error {
		m := dao.NewAdminMutation(ctx)
		if err := m.OAuthIdentity().DeleteByUserID(uid); err != nil {
			return err
		}
		if err := m.PrivateChannel().DeleteByOwner(uid); err != nil {
			return err
		}
		if err := m.PrivateChannelShare().DeleteSharesByTarget(models.PrivateShareTargetUser, uid); err != nil {
			return err
		}
		return m.User().Delete(uid)
	})
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete user failed", err)
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
