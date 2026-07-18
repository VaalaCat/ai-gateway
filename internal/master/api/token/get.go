package token

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) Get(c *app.Context, req api.IDPathRequest) (models.Token, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)
	scope := middleware.GetScope(c.Context)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)

	token, err := q.Token().GetByID(uint(id))
	if err != nil {
		return models.Token{}, api.NotFoundError(consts.ErrNotFound)
	}

	if scope != nil && !scope.IsAdmin && scope.UserID != token.UserID {
		return models.Token{}, api.NotFoundError(consts.ErrNotFound)
	}

	return *token, nil
}
