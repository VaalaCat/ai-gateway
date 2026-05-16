package user_group

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
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

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	g, err := q.UserGroup().GetByID(id)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	snapshot := *g

	affected, err := m.UserGroup().DeleteAndReassign(id)
	if err != nil {
		return api.StatusResponse{}, api.InternalError("delete user group failed", err)
	}

	if h.Bus != nil {
		ctx := context.Background()
		for _, uid := range affected {
			_ = events.PublishEntity(ctx, h.Bus, events.EntityUser, events.ActionUpdate,
				protocol.SyncedUser{ID: uid, GroupID: 1})
		}
		_ = events.PublishEntity(ctx, h.Bus, events.EntityUserGroup, events.ActionDelete, snapshot)
	}

	return api.StatusResponse{Status: "ok"}, nil
}
