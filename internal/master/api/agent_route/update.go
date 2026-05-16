package agent_route

import (
	"context"
	"strconv"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Update(c *app.Context, req UpdateRequest) (models.AgentRoute, error) {
	id, _ := strconv.ParseUint(req.ID, 10, 64)

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	existing, err := q.AgentRoute().GetByID(uint(id))
	if err != nil {
		return models.AgentRoute{}, api.NotFoundError(consts.ErrNotFound)
	}

	updates := req.Fields
	if updates == nil {
		updates = map[string]any{}
	}
	delete(updates, "id")
	delete(updates, "created_at")
	delete(updates, "priority")

	sourceType := existing.SourceType
	model := existing.Model
	if v, ok := updates["source_type"]; ok {
		if s, ok := v.(string); ok {
			sourceType = s
		}
	}
	if v, ok := updates["model"]; ok {
		if s, ok := v.(string); ok {
			model = s
		}
	}
	temp := models.AgentRoute{SourceType: sourceType, Model: model}
	updates["priority"] = temp.CalcPriority()

	if err := m.AgentRoute().Update(uint(id), updates); err != nil {
		return models.AgentRoute{}, api.InternalError("update agent route failed", err)
	}

	route, err := q.AgentRoute().GetByID(uint(id))
	if err != nil {
		return models.AgentRoute{}, api.InternalError("get updated route failed", err)
	}

	_ = events.PublishAgentRouteUpdate(context.Background(), c.GetBus(), *route)
	return *route, nil
}
