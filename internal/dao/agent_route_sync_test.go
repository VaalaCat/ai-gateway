package dao

import (
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentRouteFullSyncKeysetReadsAreAscendingAndBounded(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).AgentRoute()

	maxID, err := q.MaxID()
	require.NoError(t, err)
	require.Zero(t, maxID)

	for id := uint(1); id <= 6; id++ {
		route := &models.AgentRoute{
			ID:         id,
			SourceType: "token",
			SourceID:   id,
			Model:      "model",
			AgentID:    "agent",
			Priority:   100,
		}
		require.NoError(t, db.Create(route).Error)
	}

	maxID, err = q.MaxID()
	require.NoError(t, err)
	require.Equal(t, uint(6), maxID)

	first, err := q.ListKeyset(0, maxID, 3)
	require.NoError(t, err)
	require.Equal(t, []uint{1, 2, 3}, agentRouteIDs(first))

	// Deleting an already-returned row must not shift the second keyset page.
	require.NoError(t, db.Delete(&models.AgentRoute{}, 2).Error)
	// Rows created beyond the frozen maximum are excluded from this pull.
	require.NoError(t, db.Create(&models.AgentRoute{
		ID:         7,
		SourceType: "token",
		SourceID:   7,
		Model:      "model",
		AgentID:    "late",
		Priority:   100,
	}).Error)

	second, err := q.ListKeyset(first[len(first)-1].ID, maxID, 3)
	require.NoError(t, err)
	require.Equal(t, []uint{4, 5, 6}, agentRouteIDs(second))

	total, err := q.CountThroughID(maxID)
	require.NoError(t, err)
	require.Equal(t, int64(5), total)
}

func TestAgentRouteFullSyncKeysetCapsPageSizeAt500(t *testing.T) {
	ctx, db := setupAdminContext(t)
	routes := make([]models.AgentRoute, 501)
	for i := range routes {
		id := uint(i + 1)
		routes[i] = models.AgentRoute{
			ID:         id,
			SourceType: "token",
			SourceID:   id,
			Model:      "model",
			AgentID:    "agent",
			Priority:   100,
		}
	}
	require.NoError(t, db.CreateInBatches(routes, 100).Error)

	q := NewAdminQuery(ctx).AgentRoute()
	for _, limit := range []int{500, 501} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			got, err := q.ListKeyset(0, 501, limit)
			require.NoError(t, err)
			require.Equal(t, 500, len(got))
			require.Equal(t, uint(1), got[0].ID)
			require.Equal(t, uint(500), got[len(got)-1].ID)
		})
	}
}

func TestAgentRouteFullSyncKeysetQueriesPropagateDatabaseErrors(t *testing.T) {
	sentinel := errors.New("forced agent route query failure")

	t.Run("max id", func(t *testing.T) {
		ctx, db := setupAdminContext(t)
		require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_agent_route_max", func(tx *gorm.DB) {
			tx.AddError(sentinel)
		}))
		_, err := NewAdminQuery(ctx).AgentRoute().MaxID()
		require.ErrorIs(t, err, sentinel)
	})

	t.Run("list keyset", func(t *testing.T) {
		ctx, db := setupAdminContext(t)
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_route_list", func(tx *gorm.DB) {
			tx.AddError(sentinel)
		}))
		_, err := NewAdminQuery(ctx).AgentRoute().ListKeyset(0, 10, 5)
		require.ErrorIs(t, err, sentinel)
	})

	t.Run("count through id", func(t *testing.T) {
		ctx, db := setupAdminContext(t)
		require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:fail_agent_route_count", func(tx *gorm.DB) {
			tx.AddError(sentinel)
		}))
		_, err := NewAdminQuery(ctx).AgentRoute().CountThroughID(10)
		require.ErrorIs(t, err, sentinel)
	})
}

func agentRouteIDs(routes []models.AgentRoute) []uint {
	ids := make([]uint, len(routes))
	for i := range routes {
		ids[i] = routes[i].ID
	}
	return ids
}
