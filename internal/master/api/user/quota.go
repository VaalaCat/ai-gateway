package user

import (
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func (h *Handler) UpdateQuota(c *app.Context, req UpdateQuotaRequest) (api.StatusResponse, error) {
	deltaRaw, ok := req.Fields["delta"]
	if !ok {
		return api.StatusResponse{}, api.BadRequestError("delta is required", nil)
	}

	delta, ok := toInt64(deltaRaw)
	if !ok {
		return api.StatusResponse{}, api.BadRequestError("delta must be number", nil)
	}

	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.User().GetByID(uint(id)); err != nil {
		return api.StatusResponse{}, api.NotFoundError(consts.ErrNotFound)
	}
	if err := m.User().UpdateQuota(uint(id), delta); err != nil {
		return api.StatusResponse{}, api.InternalError("update quota failed", err)
	}
	return api.StatusResponse{Status: "ok"}, nil
}

func toInt64(v any) (int64, bool) {
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int8:
		return int64(val), true
	case int16:
		return int64(val), true
	case int32:
		return int64(val), true
	case int64:
		return val, true
	case uint:
		return int64(val), true
	case uint8:
		return int64(val), true
	case uint16:
		return int64(val), true
	case uint32:
		return int64(val), true
	case uint64:
		return int64(val), true
	case float32:
		return int64(val), true
	case float64:
		return int64(val), true
	default:
		return 0, false
	}
}
