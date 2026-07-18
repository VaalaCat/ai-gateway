package dao

import (
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type agentKeysetQuery interface {
	MaxID() (uint, error)
	ListKeyset(afterID, snapshotMaxID uint, limit int) ([]models.Agent, error)
	CountThroughID(snapshotMaxID uint) (int64, error)
}

func requireAgentKeysetQuery(t *testing.T, query AdminAgentQuery) agentKeysetQuery {
	t.Helper()
	keyset, ok := query.(agentKeysetQuery)
	require.True(t, ok, "Agent query must support consistent keyset full-sync snapshots")
	return keyset
}

func TestAgentFullSyncKeysetReadsAreAscendingAndStableAcrossMutations(t *testing.T) {
	ctx, db := setupAdminContext(t)
	query := requireAgentKeysetQuery(t, NewAdminQuery(ctx).Agent())

	maxID, err := query.MaxID()
	require.NoError(t, err)
	require.Zero(t, maxID)

	agents := make([]models.Agent, 6)
	for i := range agents {
		agents[i] = models.Agent{ID: uint(i + 1), AgentID: fmt.Sprintf("agent-%d", i+1)}
	}
	require.NoError(t, db.Create(&agents).Error)

	maxID, err = query.MaxID()
	require.NoError(t, err)
	require.Equal(t, uint(6), maxID)
	first, err := query.ListKeyset(0, maxID, 3)
	require.NoError(t, err)
	require.Equal(t, []uint{1, 2, 3}, agentIDs(first))

	require.NoError(t, db.Delete(&models.Agent{}, 2).Error)
	require.NoError(t, db.Create(&models.Agent{ID: 7, AgentID: "agent-7"}).Error)
	second, err := query.ListKeyset(first[len(first)-1].ID, maxID, 3)
	require.NoError(t, err)
	require.Equal(t, []uint{4, 5, 6}, agentIDs(second))

	total, err := query.CountThroughID(maxID)
	require.NoError(t, err)
	require.Equal(t, int64(5), total)
}

func TestAgentFullSyncKeysetBoundsPageSizeAndEmptyRanges(t *testing.T) {
	ctx, db := setupAdminContext(t)
	agents := make([]models.Agent, 501)
	for i := range agents {
		agents[i] = models.Agent{ID: uint(i + 1), AgentID: fmt.Sprintf("agent-%03d", i+1)}
	}
	require.NoError(t, db.CreateInBatches(agents, 100).Error)
	query := requireAgentKeysetQuery(t, NewAdminQuery(ctx).Agent())

	for _, limit := range []int{500, 501} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			got, err := query.ListKeyset(0, 501, limit)
			require.NoError(t, err)
			require.Len(t, got, 500)
			require.Equal(t, uint(1), got[0].ID)
			require.Equal(t, uint(500), got[len(got)-1].ID)
		})
	}
	for _, input := range []struct {
		after, max uint
		limit      int
	}{{max: 501}, {after: 501, max: 501, limit: 1}, {after: 2, max: 1, limit: 1}} {
		got, err := query.ListKeyset(input.after, input.max, input.limit)
		require.NoError(t, err)
		require.Empty(t, got)
	}
}

func TestAgentFullSyncKeysetQueriesPropagateDatabaseErrors(t *testing.T) {
	sentinel := errors.New("forced agent query failure")
	tests := []struct {
		name     string
		register func(*testing.T, *gorm.DB)
		call     func(agentKeysetQuery) error
	}{
		{
			name: "max id",
			register: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_agent_max", func(tx *gorm.DB) { tx.AddError(sentinel) }))
			},
			call: func(query agentKeysetQuery) error { _, err := query.MaxID(); return err },
		},
		{
			name: "list keyset",
			register: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_list", func(tx *gorm.DB) { tx.AddError(sentinel) }))
			},
			call: func(query agentKeysetQuery) error { _, err := query.ListKeyset(0, 10, 5); return err },
		},
		{
			name: "count through id",
			register: func(t *testing.T, db *gorm.DB) {
				require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_count", func(tx *gorm.DB) { tx.AddError(sentinel) }))
			},
			call: func(query agentKeysetQuery) error { _, err := query.CountThroughID(10); return err },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, db := setupAdminContext(t)
			test.register(t, db)
			err := test.call(requireAgentKeysetQuery(t, NewAdminQuery(ctx).Agent()))
			require.ErrorIs(t, err, sentinel)
		})
	}
}

func agentIDs(agents []models.Agent) []uint {
	ids := make([]uint, len(agents))
	for i := range agents {
		ids[i] = agents[i].ID
	}
	return ids
}
