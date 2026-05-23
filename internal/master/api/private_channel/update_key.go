package private_channel

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"go.uber.org/zap"
)

func (h *Handler) PortalUpdateKey(c *app.Context, req UpdateKeyRequest) (DetailResponse, error) {
	if c.UserInfo == nil {
		return DetailResponse{}, api.UnauthorizedError("not authenticated")
	}
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil || pc.OwnerID != c.UserInfo.UserID {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}

	// Re-check BYOK enablement — admin/group may have flipped the switch since
	// the channel was created. The runner skips field-specific validators when
	// only "key" is dirty.
	if err := RunValidators(ValidatorCtx{
		Query:   q,
		Req:     map[string]any{"key": req.Key},
		Dirty:   map[string]bool{"key": true},
		OwnerID: c.UserInfo.UserID,
		GroupID: c.UserInfo.GroupID,
	}); err != nil {
		return DetailResponse{}, err
	}

	cipher := h.Provider.GetCipher()
	if cipher == nil {
		return DetailResponse{}, api.InternalError("byok cipher not configured", nil)
	}
	ct, err := cipher.Seal(req.Key, c.UserInfo.UserID)
	if err != nil {
		return DetailResponse{}, api.InternalError("seal key", err)
	}

	m := dao.NewAdminMutation(daoCtx)
	if err := m.PrivateChannel().UpdateKey(uint(id), c.UserInfo.UserID, ct, byokcrypto.Last4(req.Key)); err != nil {
		return DetailResponse{}, api.InternalError("update key", err)
	}

	if err := sync.PublishPrivateChannelMutation(context.Background(), q, c.GetBus(), uint(id), pc.OwnerID); err != nil {
		if c.Logger != nil {
			c.Logger.Warn("publish private_channel invalidate failed",
				zap.Uint("channel_id", uint(id)), zap.Error(err))
		}
	}

	updated, _ := q.PrivateChannel().GetByID(uint(id))
	if updated == nil {
		return DetailResponse{}, api.InternalError("re-read after key update failed", nil)
	}
	return toDetailResponse(updated), nil
}
