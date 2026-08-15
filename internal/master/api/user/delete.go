package user

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mastersync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Delete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	uid := uint(id)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	if _, err := q.User().GetByID(uid); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}

	// Deleting a user must purge every artifact that lets them — or a stale
	// share — touch the system again. In particular the BYOK private_channels
	// hold encrypted key material that must not survive the user row
	// (GDPR / SOC2 right-to-erasure), and any share row whose target is this
	// user becomes orphan grant data.
	var deletedManagedRoleID uint
	err := dao.RunInTx[dao.Context](dao.NewContextWithContext(c.App, c.RequestContext()), func(ctx dao.Context) error {
		if err := dao.LockAPIPrincipal(ctx.GetCoreDB(), models.APIPrincipalUser, uid); err != nil {
			return err
		}
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
		var err error
		deletedManagedRoleID, err = dao.DeleteAPIPrincipalAccess(ctx.GetCoreDB(), models.APIPrincipalUser, uid)
		if err != nil {
			return err
		}
		return m.User().Delete(uid)
	})
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete user failed", err)
	}
	if h.Bus != nil {
		publishCtx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
		defer cancel()
		actions := mastersync.NewAPISyncActions(h.Bus, nil)
		query := dao.NewAdminQuery(dao.NewContextWithContext(c.App, publishCtx))
		if deletedManagedRoleID != 0 {
			if err := actions.PublishRole(publishCtx, query, events.ActionDelete, deletedManagedRoleID); err != nil {
				return api.StatusResponse{}, api.InternalError("publish deleted managed API role failed", err)
			}
		}
		if err := actions.InvalidateUserRoleSet(publishCtx, uid); err != nil {
			return api.StatusResponse{}, api.InternalError("invalidate deleted user API roles failed", err)
		}
	}
	return api.StatusResponse{Status: "deleted"}, nil
}
