package user_group

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req GetRequest) (Item, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return Item{}, api.BadRequestError("invalid id", err)
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)

	g, err := q.UserGroup().GetByID(uint(id))
	if err != nil {
		return Item{}, api.NotFoundError(consts.ErrNotFound)
	}
	n, _ := q.UserGroup().CountUsers(g.ID)
	return Item{UserGroup: *g, UserCount: n}, nil
}
