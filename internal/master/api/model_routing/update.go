package model_routing

import (
	"encoding/json"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.ModelRouting, error) {
	id, err := strconv.ParseUint(req.ID, 10, 64)
	if err != nil {
		return models.ModelRouting{}, api.BadRequestError("invalid id", err)
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	q := dao.NewAdminQuery(daoCtx)
	existing, loadErr := q.ModelRouting().GetByID(uint(id))
	if loadErr != nil || existing == nil || existing.Scope == models.RoutingScopeToken {
		return models.ModelRouting{}, api.NotFoundError("model routing not found")
	}
	return h.updateRouting(c, uint(id), req.Fields)
}

func (h *Handler) updateRouting(c *app.Context, id uint, fields map[string]any) (models.ModelRouting, error) {
	// 如果 fields 含 members 是数组类型，序列化成 JSON 字符串
	if v, ok := fields["members"]; ok {
		if arr, isArr := v.([]any); isArr {
			raw, _ := json.Marshal(arr)
			fields["members"] = string(raw)
		}
	}

	daoCtx := dao.NewContextWithContext(c.App, c.RequestContext())
	m := dao.NewAdminMutation(daoCtx)
	if ve := m.ModelRouting().Update(id, fields); ve != nil {
		return models.ModelRouting{}, validateErrorToAPI(ve)
	}

	// 重新读出最新记录用于 event payload
	q := dao.NewAdminQuery(daoCtx)
	r, _ := q.ModelRouting().GetByID(id)
	if r == nil {
		return models.ModelRouting{}, api.NotFoundError("routing not found after update")
	}

	h.publishEvent(c, events.ActionUpdate, r)
	return *r, nil
}
