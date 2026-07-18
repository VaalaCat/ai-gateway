package sync_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestAgentRouteFullSyncKeysetRetains501RowsAcrossDeletionAndInsert(t *testing.T) {
	srv := setupMaster(t)
	srv.Version.Store(73)
	client := connectFullSyncAgent(t, srv, "route-keyset-agent")

	routes := make([]models.AgentRoute, 501)
	for i := range routes {
		id := uint(i + 1)
		routes[i] = models.AgentRoute{
			ID:         id,
			SourceType: "token",
			SourceID:   id,
			Model:      "model",
			AgentID:    "snapshot-agent",
			Priority:   100,
		}
	}
	require.NoError(t, srv.DB.CreateInBatches(routes, 100).Error)

	first := callAgentRouteFullSync(t, client, protocol.FullSyncRequest{
		Entity:   "agent_route",
		PageSize: 500,
	})
	require.True(t, first.Keyset)
	require.Zero(t, first.Page)
	require.Equal(t, int64(73), first.BaseVersion)
	require.Equal(t, uint(501), first.SnapshotMaxID)
	require.Equal(t, uint(500), first.LastID)
	require.Equal(t, int64(501), first.Total)
	require.True(t, first.HasMore)

	var firstRoutes []models.AgentRoute
	require.NoError(t, json.Unmarshal(first.Items, &firstRoutes))
	require.Len(t, firstRoutes, 500)
	for i := range firstRoutes {
		require.Equal(t, uint(i+1), firstRoutes[i].ID)
	}

	// Deleting a row from page one cannot shift the cursor. A new row above the
	// captured maximum must wait for push replay instead of entering page two.
	require.NoError(t, srv.DB.Delete(&models.AgentRoute{}, 100).Error)
	require.NoError(t, srv.DB.Create(&models.AgentRoute{
		ID:         502,
		SourceType: "token",
		SourceID:   502,
		Model:      "model",
		AgentID:    "late-agent",
		Priority:   100,
	}).Error)
	srv.Version.Store(79)

	second := callAgentRouteFullSync(t, client, protocol.FullSyncRequest{
		Entity:        "agent_route",
		PageSize:      500,
		AfterID:       first.LastID,
		SnapshotMaxID: first.SnapshotMaxID,
		BaseVersion:   first.BaseVersion,
	})
	require.True(t, second.Keyset)
	require.Zero(t, second.Page)
	require.Equal(t, first.BaseVersion, second.BaseVersion)
	require.Equal(t, first.SnapshotMaxID, second.SnapshotMaxID)
	require.Equal(t, uint(501), second.LastID)
	require.Equal(t, int64(79), second.Version)
	require.False(t, second.HasMore)

	var secondRoutes []models.AgentRoute
	require.NoError(t, json.Unmarshal(second.Items, &secondRoutes))
	require.Equal(t, []uint{501}, masterAgentRouteIDs(secondRoutes))
}

func TestAgentRouteFullSyncKeysetCapsPageSizeAt500(t *testing.T) {
	srv := setupMaster(t)
	client := connectFullSyncAgent(t, srv, "route-page-size-agent")

	routes := make([]models.AgentRoute, 501)
	for i := range routes {
		id := uint(i + 1)
		routes[i] = models.AgentRoute{
			ID:         id,
			SourceType: "token",
			SourceID:   id,
			Model:      "model",
			AgentID:    "snapshot-agent",
			Priority:   100,
		}
	}
	require.NoError(t, srv.DB.CreateInBatches(routes, 100).Error)

	for _, pageSize := range []int{500, 501} {
		t.Run(fmt.Sprintf("page_size_%d", pageSize), func(t *testing.T) {
			resp := callAgentRouteFullSync(t, client, protocol.FullSyncRequest{
				Entity:   "agent_route",
				PageSize: pageSize,
			})
			var got []models.AgentRoute
			require.NoError(t, json.Unmarshal(resp.Items, &got))
			require.Equal(t, 500, len(got))
			require.Equal(t, uint(1), got[0].ID)
			require.Equal(t, uint(500), got[len(got)-1].ID)
			require.Equal(t, int64(501), resp.Total)
			require.Equal(t, uint(500), resp.LastID)
			require.Equal(t, uint(501), resp.SnapshotMaxID)
			require.True(t, resp.HasMore)
		})
	}
}

func TestAgentRouteFullSyncKeysetHandshakeKeepsLegacyOffsetPath(t *testing.T) {
	srv := setupMaster(t)
	client := connectFullSyncAgent(t, srv, "route-legacy-agent")

	routes := []models.AgentRoute{
		{ID: 1, SourceType: "token", SourceID: 1, Model: "model", AgentID: "low", Priority: 70},
		{ID: 2, SourceType: "token", SourceID: 2, Model: "model", AgentID: "high", Priority: 100},
		{ID: 3, SourceType: "token", SourceID: 3, Model: "model", AgentID: "mid", Priority: 90},
	}
	require.NoError(t, srv.DB.Create(&routes).Error)

	// Page>=1 identifies an old Agent and must retain the existing offset order.
	first := callAgentRouteFullSync(t, client, protocol.FullSyncRequest{
		Entity:   "agent_route",
		Page:     1,
		PageSize: 2,
	})
	require.False(t, first.Keyset)
	require.Equal(t, 1, first.Page)
	require.True(t, first.HasMore)
	var firstRoutes []models.AgentRoute
	require.NoError(t, json.Unmarshal(first.Items, &firstRoutes))
	require.Equal(t, []uint{2, 3}, masterAgentRouteIDs(firstRoutes))

	second := callAgentRouteFullSync(t, client, protocol.FullSyncRequest{
		Entity:   "agent_route",
		Page:     2,
		PageSize: 2,
	})
	require.False(t, second.Keyset)
	require.Equal(t, 2, second.Page)
	require.False(t, second.HasMore)
	var secondRoutes []models.AgentRoute
	require.NoError(t, json.Unmarshal(second.Items, &secondRoutes))
	require.Equal(t, []uint{1}, masterAgentRouteIDs(secondRoutes))
}

func TestAgentRouteFullSyncKeysetDatabaseErrorsReturnJSONRPCInternal(t *testing.T) {
	tests := []struct {
		name          string
		registerError func(*testing.T, *gorm.DB, error)
	}{
		{
			name: "max id",
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, db.Callback().Row().Before("gorm:row").Register("test:fail_agent_route_max", func(tx *gorm.DB) {
					tx.AddError(sentinel)
				}))
			},
		},
		{
			name: "list keyset",
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, registerNthAgentRouteQueryError(db, 1, sentinel))
			},
		},
		{
			name: "count through id",
			registerError: func(t *testing.T, db *gorm.DB, sentinel error) {
				t.Helper()
				require.NoError(t, registerNthAgentRouteQueryError(db, 2, sentinel))
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := setupMaster(t)
			require.NoError(t, srv.DB.Create(testMasterAgentRoute(1)).Error)
			client := connectFullSyncAgent(t, srv, "route-error-"+strings.ReplaceAll(tc.name, " ", "-"))
			sentinel := errors.New("forced " + tc.name + " failure")
			tc.registerError(t, srv.DB, sentinel)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := client.Call(ctx, consts.RPCSyncFullSync, protocol.FullSyncRequest{
				Entity:   "agent_route",
				PageSize: 500,
			})
			require.ErrorContains(t, err, fmt.Sprintf("rpc error %d", -32603))
			require.ErrorContains(t, err, sentinel.Error())
			require.Nil(t, raw, "internal error must not also return a success result")
		})
	}
}

func registerNthAgentRouteQueryError(db *gorm.DB, nth int64, sentinel error) error {
	var calls atomic.Int64
	return db.Callback().Query().Before("gorm:query").Register(fmt.Sprintf("test:fail_agent_route_query_%d", nth), func(tx *gorm.DB) {
		if calls.Add(1) == nth {
			tx.AddError(sentinel)
		}
	})
}

func testMasterAgentRoute(id uint) *models.AgentRoute {
	return &models.AgentRoute{
		ID:         id,
		SourceType: "token",
		SourceID:   id,
		Model:      "model",
		AgentID:    "snapshot-agent",
		Priority:   100,
	}
}

func connectFullSyncAgent(t *testing.T, srv *master.Server, agentID string) *ws.Client {
	t.Helper()
	secret := "test-secret"
	require.NoError(t, srv.DB.Create(&models.Agent{
		AgentID: agentID,
		Secret:  secret,
		Name:    agentID,
		Status:  1,
	}).Error)

	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	headers := http.Header{}
	headers.Set(consts.HeaderXAgentID, agentID)
	headers.Set(consts.HeaderXAgentSecret, secret)
	client, err := ws.Dial(
		context.Background(),
		"ws"+strings.TrimPrefix(ts.URL, "http")+"/ws/agent",
		zap.NewNop(),
		headers,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	require.Eventually(t, func() bool { return srv.Hub.IsOnline(agentID) }, time.Second, time.Millisecond)
	return client
}

func callAgentRouteFullSync(t *testing.T, client *ws.Client, req protocol.FullSyncRequest) protocol.FullSyncResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, err := client.Call(ctx, consts.RPCSyncFullSync, req)
	require.NoError(t, err)
	var resp protocol.FullSyncResponse
	require.NoError(t, json.Unmarshal(raw, &resp))
	return resp
}

func masterAgentRouteIDs(routes []models.AgentRoute) []uint {
	ids := make([]uint, len(routes))
	for i := range routes {
		ids[i] = routes[i].ID
	}
	return ids
}
