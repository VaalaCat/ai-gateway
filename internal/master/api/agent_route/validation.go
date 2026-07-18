package agent_route

import (
	"errors"
	"fmt"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"gorm.io/gorm"
)

func normalizeAgentRouteSelectors(route *models.AgentRoute) {
	route.AgentID = strings.TrimSpace(route.AgentID)
	route.AgentTag = strings.TrimSpace(route.AgentTag)
}

func validateAgentRoute(q dao.AdminQuery, route models.AgentRoute) error {
	normalizeAgentRouteSelectors(&route)
	if (route.AgentID == "") == (route.AgentTag == "") {
		return api.BadRequestError("agent_id and agent_tag must be set exactly one", nil)
	}

	switch route.SourceType {
	case "token":
		if _, err := q.Token().GetByID(route.SourceID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return api.BadRequestError(fmt.Sprintf("token %d not found", route.SourceID), err)
			}
			return api.InternalError("validate agent route source failed", err)
		}
	case "channel":
		if _, err := q.Channel().GetByID(route.SourceID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return api.BadRequestError(fmt.Sprintf("channel %d not found", route.SourceID), err)
			}
			return api.InternalError("validate agent route source failed", err)
		}
	default:
		return api.BadRequestError("source_type must be 'token' or 'channel'", nil)
	}

	if route.AgentID != "" {
		if _, err := q.Agent().GetByAgentID(route.AgentID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return api.BadRequestError(fmt.Sprintf("agent %s not found", route.AgentID), err)
			}
			return api.InternalError("validate agent route agent failed", err)
		}
	}
	return nil
}
