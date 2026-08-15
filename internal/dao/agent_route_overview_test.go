package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type agentRouteOverviewFixture struct {
	token   models.Token
	channel models.Channel
	routes  map[string]models.AgentRoute
}

func setupAgentRouteOverviewTest(t *testing.T) (Context, *gorm.DB) {
	t.Helper()
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.APIService{}, &models.APIBackend{}, &models.APIRoute{}))
	return ctx, db
}

func seedAgentRouteOverviewFixture(t *testing.T, db *gorm.DB) agentRouteOverviewFixture {
	t.Helper()
	token := models.Token{UserID: 1, Key: "overview-token-key", Name: "Finance Token", Status: 1}
	channel := models.Channel{ChannelCore: models.ChannelCore{Name: "Backup Channel", Status: 1}}
	agents := []models.Agent{
		{AgentID: "agent-east", Name: "East Cluster", Status: 1},
		{AgentID: "agent-west", Name: "West Cluster", Status: 1},
	}
	require.NoError(t, db.Create(&token).Error)
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&agents).Error)

	routes := map[string]models.AgentRoute{
		"token_model": {SourceType: "token", SourceID: token.ID, Model: "gpt-4o", AgentID: "agent-east", Priority: 100},
		"token_tag":   {SourceType: "token", SourceID: token.ID, Model: "claude-3", AgentTag: "canary-blue", Priority: 100},
		"channel":     {SourceType: "channel", SourceID: channel.ID, Model: "deepseek-v3", AgentID: "agent-west", Priority: 80},
	}
	for _, name := range []string{"token_model", "token_tag", "channel"} {
		route := routes[name]
		require.NoError(t, db.Create(&route).Error, name)
		routes[name] = route
	}
	return agentRouteOverviewFixture{token: token, channel: channel, routes: routes}
}

func TestAgentRouteOverviewFiltersBeforePagination(t *testing.T) {
	ctx, db := setupAgentRouteOverviewTest(t)
	fixture := seedAgentRouteOverviewFixture(t, db)
	query := NewAdminQuery(ctx).AgentRoute()

	tests := []struct {
		name   string
		filter AgentRouteOverviewFilter
		want   []uint
	}{
		{name: "q source name", filter: AgentRouteOverviewFilter{Query: "Finance"}, want: []uint{fixture.routes["token_tag"].ID, fixture.routes["token_model"].ID}},
		{name: "q model", filter: AgentRouteOverviewFilter{Query: "deepseek"}, want: []uint{fixture.routes["channel"].ID}},
		{name: "q agent name", filter: AgentRouteOverviewFilter{Query: "East Cluster"}, want: []uint{fixture.routes["token_model"].ID}},
		{name: "q agent id", filter: AgentRouteOverviewFilter{Query: "agent-west"}, want: []uint{fixture.routes["channel"].ID}},
		{name: "q agent tag", filter: AgentRouteOverviewFilter{Query: "canary"}, want: []uint{fixture.routes["token_tag"].ID}},
		{name: "token source", filter: AgentRouteOverviewFilter{SourceType: "token", SourceID: &fixture.token.ID}, want: []uint{fixture.routes["token_tag"].ID, fixture.routes["token_model"].ID}},
		{name: "channel source", filter: AgentRouteOverviewFilter{SourceType: "channel", SourceID: &fixture.channel.ID}, want: []uint{fixture.routes["channel"].ID}},
		{name: "exact model", filter: AgentRouteOverviewFilter{Model: "gpt-4o"}, want: []uint{fixture.routes["token_model"].ID}},
		{name: "exact agent", filter: AgentRouteOverviewFilter{AgentID: "agent-east"}, want: []uint{fixture.routes["token_model"].ID}},
		{name: "empty", filter: AgentRouteOverviewFilter{Model: "missing"}, want: []uint{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := query.ListOverview(ListOptions{Page: 1, PageSize: 20}, tt.filter)
			require.NoError(t, err)
			require.Equal(t, int64(len(tt.want)), total)
			got := make([]uint, len(rows))
			for i := range rows {
				got[i] = rows[i].ID
			}
			require.Equal(t, tt.want, got)
		})
	}

	t.Run("filtered total is computed before pagination", func(t *testing.T) {
		rows, total, err := query.ListOverview(
			ListOptions{Page: 2, PageSize: 1},
			AgentRouteOverviewFilter{SourceType: "token"},
		)
		require.NoError(t, err)
		require.Equal(t, int64(2), total)
		require.Len(t, rows, 1)
		require.Equal(t, fixture.routes["token_model"].ID, rows[0].ID)
	})
}

func TestAgentRouteOverviewProjectsJoinedNames(t *testing.T) {
	ctx, db := setupAgentRouteOverviewTest(t)
	fixture := seedAgentRouteOverviewFixture(t, db)

	rows, total, err := NewAdminQuery(ctx).AgentRoute().ListOverview(
		ListOptions{Page: 1, PageSize: 20},
		AgentRouteOverviewFilter{},
	)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, rows, 3)

	byID := make(map[uint]AgentRouteOverview, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	require.Equal(t, "Finance Token", byID[fixture.routes["token_model"].ID].SourceName)
	require.Equal(t, "East Cluster", byID[fixture.routes["token_model"].ID].AgentName)
	require.Equal(t, "Backup Channel", byID[fixture.routes["channel"].ID].SourceName)
	require.Equal(t, "West Cluster", byID[fixture.routes["channel"].ID].AgentName)
	require.Empty(t, byID[fixture.routes["token_tag"].ID].AgentName)
}

func TestAgentRouteOverviewFiltersProjectsAndSearchesAPISources(t *testing.T) {
	ctx, db := setupAgentRouteOverviewTest(t)
	service := models.APIService{Slug: "weather", Name: "Weather Service", Status: 1}
	require.NoError(t, db.Create(&service).Error)
	backend := models.APIBackend{APIServiceID: service.ID, Name: "primary"}
	require.NoError(t, db.Create(&backend).Error)
	route := models.APIRoute{APIServiceID: service.ID, BackendID: backend.ID, Slug: "forecast", Status: 1}
	require.NoError(t, db.Create(&route).Error)
	serviceRule := models.AgentRoute{
		SourceType: models.AgentRouteSourceAPIService,
		SourceID:   service.ID,
		AgentTag:   "api-pool",
		Priority:   50,
	}
	routeRule := models.AgentRoute{
		SourceType: models.AgentRouteSourceAPIRoute,
		SourceID:   route.ID,
		AgentTag:   "api-pool",
		Priority:   60,
	}
	require.NoError(t, db.Create(&serviceRule).Error)
	require.NoError(t, db.Create(&routeRule).Error)
	query := NewAdminQuery(ctx).AgentRoute()

	tests := []struct {
		name   string
		filter AgentRouteOverviewFilter
		wantID uint
		nameOf string
	}{
		{name: "service type filter and name", filter: AgentRouteOverviewFilter{SourceType: models.AgentRouteSourceAPIService}, wantID: serviceRule.ID, nameOf: "Weather Service"},
		{name: "route type filter and slug", filter: AgentRouteOverviewFilter{SourceType: models.AgentRouteSourceAPIRoute}, wantID: routeRule.ID, nameOf: "forecast"},
		{name: "service name query", filter: AgentRouteOverviewFilter{Query: "Weather"}, wantID: serviceRule.ID, nameOf: "Weather Service"},
		{name: "route slug query", filter: AgentRouteOverviewFilter{Query: "forecast"}, wantID: routeRule.ID, nameOf: "forecast"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, total, err := query.ListOverview(ListOptions{Page: 1, PageSize: 20}, tt.filter)
			require.NoError(t, err)
			require.Equal(t, int64(1), total)
			require.Len(t, rows, 1)
			require.Equal(t, tt.wantID, rows[0].ID)
			require.Equal(t, tt.nameOf, rows[0].SourceName)
		})
	}
}

func TestAgentRouteOverviewPreservesOrphanRows(t *testing.T) {
	for _, sourceType := range []string{"token", "channel"} {
		t.Run(sourceType, func(t *testing.T) {
			ctx, db := setupAgentRouteOverviewTest(t)
			route := models.AgentRoute{
				SourceType: sourceType,
				SourceID:   999_999,
				Model:      "orphan-model",
				AgentID:    "missing-agent",
			}
			route.Priority = route.CalcPriority()
			require.NoError(t, db.Create(&route).Error)

			rows, total, err := NewAdminQuery(ctx).AgentRoute().ListOverview(
				ListOptions{Page: 1, PageSize: 20},
				AgentRouteOverviewFilter{},
			)
			require.NoError(t, err)
			require.Equal(t, int64(1), total)
			require.Len(t, rows, 1)
			require.Equal(t, route.ID, rows[0].ID)
			require.Empty(t, rows[0].SourceName)
			require.Empty(t, rows[0].AgentName)
		})
	}
}

func TestAgentRouteOverviewDatabaseErrorsArePreserved(t *testing.T) {
	sentinel := errors.New("forced overview failure")

	t.Run("count", func(t *testing.T) {
		ctx, db := setupAgentRouteOverviewTest(t)
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_route_overview_count", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*int64); ok {
				tx.AddError(sentinel)
			}
		}))
		_, _, err := NewAdminQuery(ctx).AgentRoute().ListOverview(ListOptions{Page: 1, PageSize: 10}, AgentRouteOverviewFilter{})
		require.ErrorIs(t, err, sentinel)
	})

	t.Run("find", func(t *testing.T) {
		ctx, db := setupAgentRouteOverviewTest(t)
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_route_overview_find", func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*int64); !ok {
				tx.AddError(sentinel)
			}
		}))
		_, _, err := NewAdminQuery(ctx).AgentRoute().ListOverview(ListOptions{Page: 1, PageSize: 10}, AgentRouteOverviewFilter{})
		require.ErrorIs(t, err, sentinel)
	})
}
