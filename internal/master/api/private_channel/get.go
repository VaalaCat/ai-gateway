package private_channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) PortalGet(c *app.Context, req api.IDPathRequest) (DetailResponse, error) {
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
	// 404 (not 403) for cross-owner access — don't leak existence
	if err != nil || pc == nil || pc.OwnerID != c.UserInfo.UserID {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}
	return toDetailResponse(pc), nil
}
