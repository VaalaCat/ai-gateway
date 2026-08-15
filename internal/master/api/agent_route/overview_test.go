package agent_route

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestAgentRouteOverviewRejectsInvalidSourceFilters(t *testing.T) {
	tests := []struct {
		name string
		req  OverviewRequest
	}{
		{name: "unknown source type", req: OverviewRequest{SourceType: "workspace"}},
		{name: "source id without type", req: OverviewRequest{SourceID: "1"}},
		{name: "zero source id", req: OverviewRequest{SourceType: "token", SourceID: "0"}},
		{name: "negative source id", req: OverviewRequest{SourceType: "channel", SourceID: "-1"}},
		{name: "non numeric source id", req: OverviewRequest{SourceType: "token", SourceID: "abc"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, ctx, _ := setupAgentRouteTest(t)
			_, err := h.Overview(ctx, tt.req)
			var apiErr *api.APIError
			require.ErrorAs(t, err, &apiErr)
			require.Equal(t, http.StatusBadRequest, apiErr.Status)
		})
	}
}

func TestAgentRouteOverviewReturnsFilteredJoinedProjection(t *testing.T) {
	h, ctx, db := setupAgentRouteTest(t)
	token, _, agent := seedAgentRouteDependencies(t, db)
	wanted := seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, Model: "gpt-4o", AgentID: agent.AgentID})
	seedAgentRoute(t, db, models.AgentRoute{SourceType: "token", SourceID: token.ID, Model: "claude", AgentTag: "blue"})

	resp, err := h.Overview(ctx, OverviewRequest{
		SourceType: "token",
		SourceID:   strconv.FormatUint(uint64(token.ID), 10),
		Model:      "gpt-4o",
		AgentID:    agent.AgentID,
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), resp.Total)
	require.Len(t, resp.Data, 1)
	require.Equal(t, wanted.ID, resp.Data[0].ID)
	require.Equal(t, token.Name, resp.Data[0].SourceName)
	require.Equal(t, agent.Name, resp.Data[0].AgentName)
}

func TestAgentRouteOverviewAcceptsAPISourceFilters(t *testing.T) {
	for _, sourceType := range []string{
		models.AgentRouteSourceAPIService,
		models.AgentRouteSourceAPIRoute,
	} {
		t.Run(sourceType, func(t *testing.T) {
			filter, err := (OverviewRequest{SourceType: sourceType, SourceID: "7"}).Filter()
			require.NoError(t, err)
			require.Equal(t, sourceType, filter.SourceType)
			require.NotNil(t, filter.SourceID)
			require.Equal(t, uint(7), *filter.SourceID)
		})
	}
}
