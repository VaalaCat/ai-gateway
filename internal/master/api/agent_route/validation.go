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
	case models.AgentRouteSourceToken:
		if _, err := q.Token().GetByID(route.SourceID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return api.BadRequestError(fmt.Sprintf("token %d not found", route.SourceID), err)
			}
			return api.InternalError("validate agent route source failed", err)
		}
	case models.AgentRouteSourceChannel:
		if _, err := q.Channel().GetByID(route.SourceID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return api.BadRequestError(fmt.Sprintf("channel %d not found", route.SourceID), err)
			}
			return api.InternalError("validate agent route source failed", err)
		}
	case models.AgentRouteSourceAPIService:
		if err := validateAPIAgentRouteSource(route, "api service", q.APIService().GetByID); err != nil {
			return err
		}
	case models.AgentRouteSourceAPIRoute:
		if err := validateAPIAgentRouteSource(route, "api route", q.APIRoute().GetByID); err != nil {
			return err
		}
	default:
		return api.BadRequestError("source_type must be token, channel, api_service, or api_route", nil)
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

func validateAPIAgentRouteSource[T any](route models.AgentRoute, name string, getByID func(uint) (*T, error)) error {
	if route.SourceID == 0 {
		return api.BadRequestError(name+" source_id must be positive", nil)
	}
	if route.Model != "" {
		return api.BadRequestError(name+" agent route model must be empty", nil)
	}
	if _, err := getByID(route.SourceID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return api.BadRequestError(fmt.Sprintf("%s %d not found", name, route.SourceID), err)
		}
		return api.InternalError("validate agent route source failed", err)
	}
	return nil
}
