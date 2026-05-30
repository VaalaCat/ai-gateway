package script

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req api.IDPathRequest) (models.AdminScript, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	daoCtx := dao.NewContext(c.App)
	s, err := dao.NewAdminQuery(daoCtx).AdminScript().GetByID(uint(id))
	if err != nil {
		return models.AdminScript{}, api.NotFoundError(consts.ErrNotFound)
	}
	return *s, nil
}
