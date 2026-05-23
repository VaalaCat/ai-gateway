package private_channel

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// AdminGet returns one private channel by ID (admin-only, no owner filter).
// Never returns plaintext Key.
func (h *Handler) AdminGet(c *app.Context, req api.IDPathRequest) (DetailResponse, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	pc, err := q.PrivateChannel().GetByID(uint(id))
	if err != nil || pc == nil {
		return DetailResponse{}, api.NotFoundError("private channel not found")
	}
	return toDetailResponse(pc), nil
}
