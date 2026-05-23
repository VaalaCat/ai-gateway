package private_channel

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"go.uber.org/zap"
)

func (h *Handler) PortalDelete(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	if c.UserInfo == nil {
		return api.StatusResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError("private channel not found")
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil || pc.OwnerID != c.UserInfo.UserID {
		return api.StatusResponse{}, api.NotFoundError("private channel not found")
	}

	// Expand audience BEFORE deleting — share rows go away after delete.
	affected, expandErr := sync.ExpandPrivateChannelAudience(q, uint(id), pc.OwnerID)
	if expandErr != nil && c.Logger != nil {
		c.Logger.Warn("expand audience for delete failed",
			zap.Uint("channel_id", uint(id)), zap.Error(expandErr))
	}

	m := dao.NewAdminMutation(daoCtx)
	if err := m.PrivateChannel().Delete(uint(id), c.UserInfo.UserID); err != nil {
		return api.StatusResponse{}, api.InternalError("delete private channel", err)
	}

	if len(affected) > 0 {
		if err := events.PublishPrivateChannelInvalidate(context.Background(), c.GetBus(), affected); err != nil {
			if c.Logger != nil {
				c.Logger.Warn("publish private_channel invalidate failed",
					zap.Uint("channel_id", uint(id)), zap.Error(err))
			}
		}
	}
	return api.StatusResponse{Status: "ok"}, nil
}
