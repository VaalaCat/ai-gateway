package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	apiagent "github.com/VaalaCat/ai-gateway/internal/master/api/agent"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestConnectionsTargetsRouteAndDetailSharePhaseZeroContract(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("snapshot-admin", "admin123"))
	token := loginAsAdmin(t, srv, "snapshot-admin", "admin123")

	agent := models.Agent{AgentID: "route-agent", Name: "route-agent", Status: 1, LastSeen: 123}
	require.NoError(t, srv.DB.Create(&agent).Error)

	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	detailW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/detail")
	require.Equal(t, http.StatusOK, detailW.Code, detailW.Body.String())
	var detail apiagent.AgentDetailResponse
	require.NoError(t, json.Unmarshal(detailW.Body.Bytes(), &detail))
	require.Equal(t, "unknown", detail.Connection.Direct.Summary.State)
	require.NotNil(t, detail.RouteTargets.Data)
	require.Equal(t, 20, detail.RouteTargets.Limit)
	require.Equal(t, detail.Connection.SnapshotEpoch, detail.RouteTargets.SnapshotEpoch)
	require.NotZero(t, detail.RouteTargets.SnapshotSeq)
	require.Equal(t, detail.Connection.ObservedAt, detail.RouteTargets.ObservedAt)

	directW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/connections/targets?limit=500")
	require.Equal(t, http.StatusOK, directW.Code, directW.Body.String())
	var direct connectivity.RouteTargetsPage
	require.NoError(t, json.Unmarshal(directW.Body.Bytes(), &direct))
	require.NotNil(t, direct.Data)
	require.Empty(t, direct.Data)
	require.Equal(t, 100, direct.Limit)
	require.Equal(t, detail.Connection.SnapshotEpoch, direct.SnapshotEpoch)
	require.Equal(t, detail.RouteTargets.SnapshotSeq, direct.SnapshotSeq)

	refetchedDetailW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/detail")
	require.Equal(t, http.StatusOK, refetchedDetailW.Code, refetchedDetailW.Body.String())
	var refetchedDetail apiagent.AgentDetailResponse
	require.NoError(t, json.Unmarshal(refetchedDetailW.Body.Bytes(), &refetchedDetail))
	require.Greater(t, refetchedDetail.Connection.SnapshotSeq, detail.Connection.SnapshotSeq)
	require.Equal(t, detail.RouteTargets.SnapshotSeq, refetchedDetail.RouteTargets.SnapshotSeq)

	defaultW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/connections/targets")
	require.Equal(t, http.StatusOK, defaultW.Code, defaultW.Body.String())
	var defaultPage connectivity.RouteTargetsPage
	require.NoError(t, json.Unmarshal(defaultW.Body.Bytes(), &defaultPage))
	require.NotNil(t, defaultPage.Data)
	require.Equal(t, 20, defaultPage.Limit)

	invalidCursorW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/connections/targets?cursor=phase-zero")
	require.Equal(t, http.StatusBadRequest, invalidCursorW.Code, invalidCursorW.Body.String())
	require.JSONEq(t, `{"code":"route_targets_cursor_invalid","message":"the route targets cursor is invalid"}`, invalidCursorW.Body.String())

	legacyW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/connections/direct")
	require.Equal(t, http.StatusNotFound, legacyW.Code, "the removed Direct-only endpoint must not remain as an alias")
}

func TestConnectionsTargetsRouteKeepsDetailSnapshotAcrossPages(t *testing.T) {
	srv := setupTestMaster(t)
	require.NoError(t, srv.InitAdminUser("snapshot-page-admin", "admin123"))
	token := loginAsAdmin(t, srv, "snapshot-page-admin", "admin123")
	agent := models.Agent{AgentID: "paged-agent", Name: "paged-agent", Status: 1}
	require.NoError(t, srv.DB.Create(&agent).Error)
	events := make([]protocol.RouteEvent, 0, 21)
	for index := 0; index < 21; index++ {
		targetID := fmt.Sprintf("target-%02d", index)
		srv.Connections.MarkDirectProbeChecking(agent.AgentID, 1, connectivity.ProbeTarget{
			AgentID: targetID, Name: targetID,
			Addresses: []protocol.Address{{URL: "https://" + targetID + ".example"}},
		}, "fp-"+targetID, uint64(index+1))
		events = append(events, protocol.RouteEvent{
			TargetAgentID: targetID, RouteID: uint(index + 1), SelectorKind: "agent",
			PathKind: "direct", Result: "success", ObservedAt: time.Now().Unix(), Sequence: uint64(index + 1),
		})
	}
	require.NoError(t, srv.Connections.ApplyEvents(agent.AgentID, protocol.RouteTelemetryBatch{
		Generation: 1, Events: events,
	}))
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		srv.Router.ServeHTTP(w, req)
		return w
	}

	detailW := request("/api/admin/agents/" + strconv.Itoa(int(agent.ID)) + "/detail")
	require.Equal(t, http.StatusOK, detailW.Code, detailW.Body.String())
	var detail apiagent.AgentDetailResponse
	require.NoError(t, json.Unmarshal(detailW.Body.Bytes(), &detail))
	require.Len(t, detail.RouteTargets.Data, 20)
	require.NotEmpty(t, detail.RouteTargets.NextCursor)

	nextPath := fmt.Sprintf(
		"/api/admin/agents/%d/connections/targets?cursor=%s&limit=20&expected_snapshot_epoch=%s&expected_snapshot_seq=%d",
		agent.ID,
		url.QueryEscape(detail.RouteTargets.NextCursor),
		url.QueryEscape(detail.RouteTargets.SnapshotEpoch),
		detail.RouteTargets.SnapshotSeq,
	)
	nextW := request(nextPath)
	require.Equal(t, http.StatusOK, nextW.Code, nextW.Body.String())
	var next connectivity.RouteTargetsPage
	require.NoError(t, json.Unmarshal(nextW.Body.Bytes(), &next))
	require.Equal(t, detail.RouteTargets.SnapshotEpoch, next.SnapshotEpoch)
	require.Equal(t, detail.RouteTargets.SnapshotSeq, next.SnapshotSeq)
	require.Equal(t, detail.RouteTargets.ObservedAt, next.ObservedAt)
	require.Len(t, next.Data, 1)
	require.Equal(t, "target-20", next.Data[0].TargetAgentID)
}
