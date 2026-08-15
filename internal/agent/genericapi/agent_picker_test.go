package genericapi

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

type agentPickerLookup struct {
	agents map[string]*models.Agent
	tags   map[string][]*models.Agent
}

func (l agentPickerLookup) GetAgent(id string) *models.Agent          { return l.agents[id] }
func (l agentPickerLookup) GetAgentsByTag(tag string) []*models.Agent { return l.tags[tag] }

func TestAgentPickerUsesTokenThenRouteThenServiceThenLocal(t *testing.T) {
	routes := cache.NewRouteIndex()
	lookup := agentPickerLookup{agents: map[string]*models.Agent{
		"token":   {AgentID: "token", Status: consts.StatusEnabled},
		"route":   {AgentID: "route", Status: consts.StatusEnabled},
		"service": {AgentID: "service", Status: consts.StatusEnabled},
	}}
	picker := NewAgentPicker(routes, lookup, "local")
	routes.Replace([]*models.AgentRoute{
		{ID: 1, SourceType: models.AgentRouteSourceToken, SourceID: 3, Model: "llm-only", AgentID: "service", Priority: 999},
		{ID: 2, SourceType: models.AgentRouteSourceToken, SourceID: 3, AgentID: "token"},
		{ID: 3, SourceType: models.AgentRouteSourceAPIRoute, SourceID: 9, AgentID: "route"},
		{ID: 4, SourceType: models.AgentRouteSourceAPIService, SourceID: 7, AgentID: "service"},
	})

	pick, err := picker.Pick(3, 9, 7, "request-a")
	require.NoError(t, err)
	require.Equal(t, "token", pick.ExecutionAgentID, "API token routing must ignore model-specific token routes")
	require.Equal(t, uint(2), pick.AgentRouteID)

	routes.Delete(2)
	pick, err = picker.Pick(3, 9, 7, "request-a")
	require.NoError(t, err)
	require.Equal(t, "route", pick.ExecutionAgentID)
	routes.Delete(3)
	pick, err = picker.Pick(3, 9, 7, "request-a")
	require.NoError(t, err)
	require.Equal(t, "service", pick.ExecutionAgentID)
	routes.Delete(4)
	pick, err = picker.Pick(3, 9, 7, "request-a")
	require.NoError(t, err)
	require.Equal(t, AgentPick{ExecutionAgentID: "local", AgentRoutePath: "local"}, pick)
}

func TestAgentPickerTagSelectionIsStableAndDoesNotTryAnotherAgent(t *testing.T) {
	routes := cache.NewRouteIndex()
	routes.Replace([]*models.AgentRoute{{ID: 42, SourceType: models.AgentRouteSourceAPIRoute, SourceID: 9, AgentTag: "edge"}})
	lookup := agentPickerLookup{tags: map[string][]*models.Agent{"edge": {
		{AgentID: "agent-b", Tags: "edge", Status: consts.StatusEnabled},
		{AgentID: "agent-a", Tags: "edge", Status: consts.StatusEnabled},
	}}}
	picker := NewAgentPicker(routes, lookup, "local")

	first, err := picker.Pick(3, 9, 7, "stable-request")
	require.NoError(t, err)
	for range 50 {
		again, pickErr := picker.Pick(3, 9, 7, "stable-request")
		require.NoError(t, pickErr)
		require.Equal(t, first, again)
	}
}

func TestAgentPickerRejectsInvalidInputAndUnusableExplicitRoute(t *testing.T) {
	routes := cache.NewRouteIndex()
	picker := NewAgentPicker(routes, agentPickerLookup{}, "local")
	_, err := picker.Pick(0, 0, 0, "")
	require.Error(t, err)

	routes.Replace([]*models.AgentRoute{{ID: 1, SourceType: models.AgentRouteSourceAPIService, SourceID: 7, AgentID: "missing"}})
	_, err = picker.Pick(3, 9, 7, "request")
	require.ErrorIs(t, err, ErrExecutionUnavailable)
}

func TestAgentPickerExplicitSourceAgentRouteUsesLocalPath(t *testing.T) {
	routes := cache.NewRouteIndex()
	routes.Replace([]*models.AgentRoute{{
		ID: 17, SourceType: models.AgentRouteSourceAPIRoute, SourceID: 9, AgentID: "local",
	}})
	lookup := agentPickerLookup{agents: map[string]*models.Agent{
		"local": {AgentID: "local", Status: consts.StatusEnabled},
	}}
	pick, err := NewAgentPicker(routes, lookup, "local").Pick(3, 9, 7, "request-local")
	require.NoError(t, err)
	require.Equal(t, "local", pick.ExecutionAgentID)
	require.Equal(t, uint(17), pick.AgentRouteID)
	require.Equal(t, app.RoutePathLocal, pick.AgentRoutePath)
}
