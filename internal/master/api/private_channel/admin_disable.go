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

// AdminDisable is the admin kill switch: forcibly sets status=0 on any user's
// private channel. After disable, expand the affected user set and publish an
// invalidate event so agents drop their cache.
func (h *Handler) AdminDisable(c *app.Context, req api.IDPathRequest) (api.StatusResponse, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return api.StatusResponse{}, api.NotFoundError("private channel not found")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil {
		return api.StatusResponse{}, api.NotFoundError("private channel not found")
	}

	m := dao.NewAdminMutation(daoCtx)
	if err := m.PrivateChannel().AdminDisable(uint(id)); err != nil {
		return api.StatusResponse{}, api.InternalError("disable private channel", err)
	}

	affected, expandErr := sync.ExpandPrivateChannelAudience(q, uint(id), pc.OwnerID)
	if expandErr != nil && c.Logger != nil {
		c.Logger.Warn("expand audience after admin disable failed",
			zap.Uint("channel_id", uint(id)), zap.Error(expandErr))
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
