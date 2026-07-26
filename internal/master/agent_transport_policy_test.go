package master

import (
	"slices"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentTransportPolicyFinderBatchesUniqueIDsAndSkipsEmptyInput(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.Agent{}))
	require.NoError(t, db.Create([]models.Agent{
		{AgentID: "agent-a", Name: "A"},
		{AgentID: "agent-b", Name: "B"},
	}).Error)
	application := app.NewApplication()
	application.SetCoreDB(db)
	finder := masterAgentTransportPolicyFinder{application: application}
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:count_agent_policy_queries", func(tx *gorm.DB) {
		if tx.Statement.Table == "agents" {
			queries++
		}
	}))

	empty, err := finder.FindAgentTransportPolicies(t.Context(), []string{"", ""})
	require.NoError(t, err)
	require.Empty(t, empty)
	require.Zero(t, queries)

	policies, err := finder.FindAgentTransportPolicies(t.Context(), []string{"agent-b", "agent-a", "agent-b", ""})
	require.NoError(t, err)
	require.Equal(t, 1, queries)
	require.Equal(t, []string{"agent-a", "agent-b"}, sortedAgentPolicyIDs(policies))
}

func TestAgentTransportPolicyFinderReturnsDatabaseErrors(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	application := app.NewApplication()
	application.SetCoreDB(db)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	_, err = (masterAgentTransportPolicyFinder{application: application}).
		FindAgentTransportPolicies(t.Context(), []string{"agent-a"})
	require.Error(t, err)
}

func sortedAgentPolicyIDs(policies map[string]models.Agent) []string {
	ids := make([]string, 0, len(policies))
	for id := range policies {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}
