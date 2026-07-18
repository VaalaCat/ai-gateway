package user

import (
	"errors"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"gorm.io/gorm"
)

func (h *Handler) UpdateQuota(c *app.Context, req UpdateQuotaRequest) (api.StatusResponse, error) {
	if req.Delta == nil {
		return api.StatusResponse{}, api.BadRequestError("delta is required", nil)
	}
	delta := *req.Delta

	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.User().GetByID(uint(id)); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
		}
		return api.StatusResponse{}, api.InternalError("get user failed", err)
	}
	if err := m.User().UpdateQuota(uint(id), delta); err != nil {
		if errors.Is(err, dao.ErrQuotaOutOfRange) {
			return api.StatusResponse{}, api.BadRequestError(dao.ErrQuotaOutOfRange.Error(), err)
		}
		return api.StatusResponse{}, api.InternalError("update quota failed", err)
	}
	return api.StatusResponse{Status: "ok"}, nil
}
