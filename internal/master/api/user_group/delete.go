package user_group

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mastersync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func (h *Handler) Delete(c *app.Context, req DeleteRequest) (api.StatusResponse, error) {
	id64, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.BadRequestError("invalid id", err)
	}
	id := uint(id64)
	if id == 1 {
		return api.StatusResponse{}, api.BadRequestError("cannot delete default user group", nil)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	g, err := q.UserGroup().GetByID(id)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	snapshot := *g

	affected, deletedManagedRoleID, err := m.UserGroup().DeleteAndReassign(id)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete user group failed", err)
	}

	if h.Bus != nil {
		ctx, cancel := api.NewPostCommitPublishContext(c.RequestContext())
		defer cancel()
		actions := mastersync.NewAPISyncActions(h.Bus, nil)
		if deletedManagedRoleID != 0 {
			query := dao.NewAdminQuery(dao.NewContextWithContext(c.App, ctx))
			if err := actions.PublishRole(ctx, query, events.ActionDelete, deletedManagedRoleID); err != nil {
				return api.StatusResponse{}, api.InternalError("publish deleted managed API role failed", err)
			}
		}
		for _, uid := range affected {
			if err := events.PublishEntity(ctx, h.Bus, events.EntityUser, events.ActionUpdate,
				protocol.SyncedUser{ID: uid, GroupID: 1}); err != nil {
				return api.StatusResponse{}, api.InternalError("publish reassigned user failed", err)
			}
			// behavior change: group membership changes alter the effective User RoleSet.
			if err := actions.InvalidateUserRoleSet(ctx, uid); err != nil {
				return api.StatusResponse{}, api.InternalError("invalidate reassigned user API roles failed", err)
			}
		}
		if err := actions.DeleteUserGroupRoleSet(ctx, id); err != nil {
			return api.StatusResponse{}, api.InternalError("delete user group API roles failed", err)
		}
		if err := events.PublishEntity(ctx, h.Bus, events.EntityUserGroup, events.ActionDelete, snapshot); err != nil {
			return api.StatusResponse{}, api.InternalError("publish user group deletion failed", err)
		}
	}

	return api.StatusResponse{Status: "ok"}, nil
}
