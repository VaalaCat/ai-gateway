package agent_route

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req api.IDPathRequest) (models.AgentRoute, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	route, err := q.AgentRoute().GetByID(uint(id))
	if err != nil {
		return models.AgentRoute{}, api.NotFoundError(consts.ErrNotFound)
	}
	return *route, nil
}
