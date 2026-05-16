package agent

import (
	"context"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
)

func (h *Handler) Enroll(c *app.Context, req EnrollRequest) (api.Created[EnrollResponse], error) {
	daoCtx := dao.NewContext(c.App)
	q := dao.NewAdminQuery(daoCtx)
	m := dao.NewAdminMutation(daoCtx)

	if _, err := q.EnrollmentToken().GetValidByToken(req.EnrollmentToken); err != nil {
		return api.Created[EnrollResponse]{}, api.UnauthorizedError("invalid or expired enrollment token")
	}

	agentID := GenerateRandomID("agent-")
	secret := GenerateRandomID("sec-")
	if req.Name == "" {
		req.Name = agentID
	}
	agent := models.Agent{AgentID: agentID, Secret: secret, Name: req.Name, Status: 1}
	if err := m.Agent().Create(&agent); err != nil {
		return api.Created[EnrollResponse]{}, api.ConflictError(err.Error(), err)
	}

	if err := events.PublishAgentRegistered(context.Background(), c.GetBus(), agent); err != nil {
		return api.Created[EnrollResponse]{}, api.InternalError("publish agent.registered failed", err)
	}
	return api.Created[EnrollResponse]{Value: EnrollResponse{AgentID: agentID, Secret: secret}}, nil
}
