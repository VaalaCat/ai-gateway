package agent_route

import (
	"context"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Create(c *app.Context, req CreateRequest) (api.Created[models.AgentRoute], error) {
	if (req.AgentID == "" && req.AgentTag == "") || (req.AgentID != "" && req.AgentTag != "") {
		return api.Created[models.AgentRoute]{}, api.BadRequestError("agent_id and agent_tag must be set exactly one", nil)
	}

	if req.SourceType != "token" && req.SourceType != "channel" {
		return api.Created[models.AgentRoute]{}, api.BadRequestError("source_type must be 'token' or 'channel'", nil)
	}

	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	switch req.SourceType {
	case "token":
		if _, err := q.Token().GetByID(req.SourceID); err != nil {
			return api.Created[models.AgentRoute]{}, api.BadRequestError(fmt.Sprintf("token %d not found", req.SourceID), err)
		}
	case "channel":
		if _, err := q.Channel().GetByID(req.SourceID); err != nil {
			return api.Created[models.AgentRoute]{}, api.BadRequestError(fmt.Sprintf("channel %d not found", req.SourceID), err)
		}
	}

	if req.AgentID != "" {
		if _, err := q.Agent().GetByAgentID(req.AgentID); err != nil {
			return api.Created[models.AgentRoute]{}, api.BadRequestError(fmt.Sprintf("agent %s not found", req.AgentID), err)
		}
	}

	route := models.AgentRoute{
		SourceType: req.SourceType,
		SourceID:   req.SourceID,
		Model:      req.Model,
		AgentID:    req.AgentID,
		AgentTag:   req.AgentTag,
	}
	route.Priority = route.CalcPriority()

	if err := m.AgentRoute().Create(&route); err != nil {
		return api.Created[models.AgentRoute]{}, api.ConflictError("route already exists or creation failed: "+err.Error(), err)
	}

	_ = events.PublishAgentRouteCreate(context.Background(), c.GetBus(), route)
	return api.Created[models.AgentRoute]{Value: route}, nil
}
