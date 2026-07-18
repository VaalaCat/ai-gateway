package sync_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestAgentFullSyncKeysetRetains501RowsAcrossDeletionAndInsert(t *testing.T) {
	srv := setupMaster(t)
	srv.Version.Store(73)
	client := connectFullSyncAgent(t, srv, "agent-keyset-recipient")

	agents := make([]models.Agent, 500)
	for i := range agents {
		agents[i] = models.Agent{
			AgentID: fmt.Sprintf("snapshot-agent-%03d", i+1),
			Secret:  "must-be-redacted",
			Name:    fmt.Sprintf("Snapshot Agent %03d", i+1),
			Status:  1,
		}
	}
	require.NoError(t, srv.DB.CreateInBatches(agents, 100).Error)

	first := callAgentFullSyncV1(t, client, protocol.FullSyncRequest{
		Entity:   events.EntityAgent,
		PageSize: protocol.FullSyncMaxPageSize,
	})
	require.True(t, first.Keyset)
	require.Zero(t, first.Page)
	require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, first.SnapshotContract)
	require.Equal(t, int64(73), first.BaseVersion)
	require.Equal(t, int64(501), first.Total)
	require.Equal(t, uint(501), first.SnapshotMaxID)
	require.Equal(t, uint(500), first.LastID)
	require.True(t, first.HasMore)

	var firstAgents []models.Agent
	require.NoError(t, json.Unmarshal(first.Items, &firstAgents))
	require.Len(t, firstAgents, 500)
	for i := range firstAgents {
		require.Equal(t, uint(i+1), firstAgents[i].ID)
		require.Empty(t, firstAgents[i].Secret)
	}

	// A deletion behind the cursor cannot shift the remaining snapshot page.
	// The insert above SnapshotMaxID is delivered by push replay, not this pull.
	require.NoError(t, srv.DB.Delete(&models.Agent{}, 100).Error)
	late := models.Agent{AgentID: "late-agent", Secret: "late-secret", Name: "Late Agent", Status: 1}
	require.NoError(t, srv.DB.Create(&late).Error)
	require.Equal(t, uint(502), late.ID)
	srv.Version.Store(79)

	second := callAgentFullSyncV1(t, client, protocol.FullSyncRequest{
		Entity:        events.EntityAgent,
		PageSize:      protocol.FullSyncMaxPageSize,
		AfterID:       first.LastID,
		SnapshotMaxID: first.SnapshotMaxID,
		BaseVersion:   first.BaseVersion,
	})
	require.True(t, second.Keyset)
	require.Zero(t, second.Page)
	require.Equal(t, protocol.AgentFullSyncSnapshotContractV1, second.SnapshotContract)
	require.Equal(t, first.BaseVersion, second.BaseVersion)
	require.Equal(t, first.SnapshotMaxID, second.SnapshotMaxID)
	require.Equal(t, uint(501), second.LastID)
	require.Equal(t, int64(79), second.Version)
	require.False(t, second.HasMore)

	var secondAgents []models.Agent
	require.NoError(t, json.Unmarshal(second.Items, &secondAgents))
	require.Len(t, secondAgents, 1)
	require.Equal(t, uint(501), secondAgents[0].ID)
	require.Empty(t, secondAgents[0].Secret)
	require.NotEqual(t, late.ID, secondAgents[0].ID)
}

func TestAgentFullSyncDatabaseErrorsReturnJSONRPCInternal(t *testing.T) {
	tests := []struct {
		name          string
		request       protocol.FullSyncRequest
		registerError func(*testing.T, *gorm.DB, error)
	}{
		{
			name:    "keyset max id",
			request: protocol.FullSyncRequest{Entity: events.EntityAgent, PageSize: 500},
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_agent_max", func(tx *gorm.DB) {
					tx.AddError(sentinel)
				}))
			},
		},
		{
			name:    "keyset list",
			request: protocol.FullSyncRequest{Entity: events.EntityAgent, PageSize: 500},
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, registerNthAgentQueryError(db, 1, sentinel))
			},
		},
		{
			name:    "keyset count",
			request: protocol.FullSyncRequest{Entity: events.EntityAgent, PageSize: 500},
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, registerNthAgentQueryError(db, 2, sentinel))
			},
		},
		{
			name:    "legacy offset list",
			request: protocol.FullSyncRequest{Entity: events.EntityAgent, Page: 1, PageSize: 500},
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, registerNthAgentQueryError(db, 1, sentinel))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupMaster(t)
			client := connectFullSyncAgent(t, srv, "agent-error-"+strings.ReplaceAll(tc.name, " ", "-"))
			sentinel := errors.New("forced " + tc.name + " failure")
			tc.registerError(t, srv.DB, sentinel)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := client.Call(ctx, consts.RPCSyncFullSync, tc.request)
			require.ErrorContains(t, err, fmt.Sprintf("rpc error %d", -32603))
			require.ErrorContains(t, err, sentinel.Error())
			require.Nil(t, raw, "database error must not also return a successful snapshot contract")
		})
	}
}

func registerNthAgentQueryError(db *gorm.DB, nth int64, sentinel error) error {
	var calls atomic.Int64
	return db.Callback().Query().Before("gorm:query").Register(fmt.Sprintf("test:fail_agent_query_%d", nth), func(tx *gorm.DB) {
		if calls.Add(1) == nth {
			tx.AddError(sentinel)
		}
	})
}

func callAgentFullSyncV1(t *testing.T, client *ws.Client, req protocol.FullSyncRequest) protocol.FullSyncResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := client.Call(ctx, consts.RPCSyncFullSync, req)
	require.NoError(t, err)
	var resp protocol.FullSyncResponse
	require.NoError(t, json.Unmarshal(raw, &resp))
	return resp
}
