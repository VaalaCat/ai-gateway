package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	masterapi "github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestConnectivityAndDiagnosticsHTTPContracts(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
		agent.AgentID: {Generation: 7, ConnectedAt: 80, HeartbeatAt: 90},
	}}
	connections := connectivity.NewService("epoch-http", connectivity.Sources{Control: control}, connectivity.Options{})
	probes := &apiProbeOperator{ack: protocol.ProbeAck{
		ProbeID: "probe-http", State: "queued", TargetTotal: 2, SnapshotSeq: 11,
	}}
	ctx := newTestContext(t, db)
	h := &Handler{
		Connections:     connections,
		ControlSessions: control,
		GetProbeProgress: func(sourceID, probeID string) (protocol.ManualProbeProgress, bool) {
			if sourceID != agent.AgentID {
				return protocol.ManualProbeProgress{}, false
			}
			if probeID != "probe-http" {
				return protocol.ManualProbeProgress{}, false
			}
			return protocol.ManualProbeProgress{ProbeID: probeID, State: "running", TargetTotal: 2, Remaining: 1}, true
		},
	}
	h.Operations = masteroperations.NewService(t.Context(), apiDatabaseOperationFinder{application: ctx.App}, masteroperations.Sources{
		Connections: connections, Probes: probes,
	})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	adapter := masterapi.NewAdapter(nil, zap.NewNop(), ctx.App)
	router.POST("/agents/:id/connectivity", masterapi.Adapt(adapter, masterapi.BindURIAndOptionalJSON, h.CheckConnectivity))
	router.GET("/agents/:id/connectivity", masterapi.Adapt(adapter, masterapi.BindURIAndQuery, h.GetConnectivity))
	router.GET("/agents/:id/connections/diagnostics", masterapi.Adapt(adapter, masterapi.BindURI, h.ConnectionDiagnostics))
	id := strconv.Itoa(int(agent.ID))

	post := httptest.NewRecorder()
	router.ServeHTTP(post, httptest.NewRequest(http.MethodPost, "/agents/"+id+"/connectivity", nil))
	require.Equal(t, http.StatusAccepted, post.Code, post.Body.String())
	var ack ProbeAck
	require.NoError(t, json.Unmarshal(post.Body.Bytes(), &ack))
	require.Equal(t, probes.ack, ack)

	get := httptest.NewRecorder()
	router.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/agents/"+id+"/connectivity?probe_id=probe-http", nil))
	require.Equal(t, http.StatusOK, get.Code, get.Body.String())
	var progress ManualProbeProgress
	require.NoError(t, json.Unmarshal(get.Body.Bytes(), &progress))
	require.Equal(t, "probe-http", progress.ProbeID)
	require.Equal(t, 1, progress.Remaining)

	diagnostics := httptest.NewRecorder()
	router.ServeHTTP(diagnostics, httptest.NewRequest(http.MethodGet, "/agents/"+id+"/connections/diagnostics", nil))
	require.Equal(t, http.StatusOK, diagnostics.Code, diagnostics.Body.String())
	var snapshot ConnectionDiagnosticsResponse
	require.NoError(t, json.Unmarshal(diagnostics.Body.Bytes(), &snapshot))
	require.Equal(t, "epoch-http", snapshot.SnapshotEpoch)
}

func TestConnectivityProgressUsesProbeIDAndKeepsConcurrentProbesIndependent(t *testing.T) {
	db := setupTestDB(t)
	agentA := models.Agent{AgentID: "source-a", Name: "source-a", Status: consts.StatusEnabled}
	agentB := models.Agent{AgentID: "source-b", Name: "source-b", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agentA).Error)
	require.NoError(t, db.Create(&agentB).Error)
	progressBySource := map[string]map[string]ManualProbeProgress{
		agentA.AgentID: {"probe-a": {ProbeID: "probe-a", State: "running", TargetTotal: 3, Remaining: 2}},
		agentB.AgentID: {"probe-b": {ProbeID: "probe-b", State: "completed", TargetTotal: 1, CompletedAt: 100}},
	}
	h := &Handler{
		Connections: connectivity.NewService("epoch", connectivity.Sources{}, connectivity.Options{}),
		GetProbeProgress: func(sourceID, probeID string) (protocol.ManualProbeProgress, bool) {
			progress, ok := progressBySource[sourceID][probeID]
			return progress, ok
		},
	}
	ctx := newTestContext(t, db)

	first, err := h.GetConnectivity(ctx, ConnectivityProgressRequest{ID: strconv.Itoa(int(agentA.ID)), ProbeID: "probe-a"})
	require.NoError(t, err)
	_, err = h.GetConnectivity(ctx, ConnectivityProgressRequest{ID: strconv.Itoa(int(agentA.ID)), ProbeID: "probe-b"})
	require.Equal(t, "probe_not_found", requireAPIError(t, err).Code)
	second, err := h.GetConnectivity(ctx, ConnectivityProgressRequest{ID: strconv.Itoa(int(agentB.ID)), ProbeID: "probe-b"})
	require.NoError(t, err)
	require.Equal(t, progressBySource[agentA.AgentID]["probe-a"], first)
	require.Equal(t, progressBySource[agentB.AgentID]["probe-b"], second)
}

func TestConnectivityProgressRejectsMissingOrUnknownProbeID(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	h := &Handler{
		Connections: connectivity.NewService("epoch", connectivity.Sources{}, connectivity.Options{}),
		GetProbeProgress: func(string, string) (protocol.ManualProbeProgress, bool) {
			return protocol.ManualProbeProgress{}, false
		},
	}
	ctx := newTestContext(t, db)

	_, err := h.GetConnectivity(ctx, ConnectivityProgressRequest{ID: strconv.Itoa(int(agent.ID))})
	require.Equal(t, "probe_id_required", requireAPIError(t, err).Code)
	_, err = h.GetConnectivity(ctx, ConnectivityProgressRequest{ID: strconv.Itoa(int(agent.ID)), ProbeID: "missing"})
	require.Equal(t, "probe_not_found", requireAPIError(t, err).Code)
}

type contextWaitingProbeOperator struct {
	onStart func()
}

func (o contextWaitingProbeOperator) EnqueueManualSession(ctx context.Context, _ string, _ uint64, _ protocol.ProbeScope) (protocol.ProbeAck, error) {
	if o.onStart != nil {
		o.onStart()
	}
	<-ctx.Done()
	return protocol.ProbeAck{}, context.Cause(ctx)
}

func TestCheckConnectivityMapsCancellationDuringEnqueue(t *testing.T) {
	tests := []struct {
		name       string
		requestCtx func(context.Context) (context.Context, context.CancelFunc)
		wantStatus int
		wantCode   string
	}{
		{
			name: "cancelled",
			requestCtx: func(parent context.Context) (context.Context, context.CancelFunc) {
				return context.WithCancel(parent)
			},
			wantStatus: http.StatusRequestTimeout,
			wantCode:   "request_canceled",
		},
		{
			name: "deadline",
			requestCtx: func(parent context.Context) (context.Context, context.CancelFunc) {
				return context.WithTimeout(parent, 10*time.Millisecond)
			},
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   "request_deadline_exceeded",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupTestDB(t)
			agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
			require.NoError(t, db.Create(&agent).Error)
			ctx := newTestContext(t, db)
			requestCtx, cancel := test.requestCtx(ctx.Request.Context())
			t.Cleanup(cancel)
			ctx.Request = ctx.Request.WithContext(requestCtx)
			control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{agent.AgentID: {Generation: 7}}}
			connections := connectivity.NewService("epoch", connectivity.Sources{Control: control}, connectivity.Options{})
			operator := contextWaitingProbeOperator{}
			if test.name == "cancelled" {
				operator.onStart = cancel
			}
			h := &Handler{Connections: connections}
			h.Operations = masteroperations.NewService(t.Context(), apiDatabaseOperationFinder{application: ctx.App}, masteroperations.Sources{
				Connections: connections, Probes: operator,
			})

			_, err := h.CheckConnectivity(ctx, ConnectivityRequest{ID: strconv.Itoa(int(agent.ID))})
			apiErr := requireAPIError(t, err)
			require.Equal(t, test.wantStatus, apiErr.Status)
			require.Equal(t, test.wantCode, apiErr.Code)
		})
	}
}

func TestConnectivityWireTypesRemainProtocolAliases(t *testing.T) {
	var ack ProbeAck = protocol.ProbeAck{ProbeID: "probe-a"}
	var progress ManualProbeProgress = protocol.ManualProbeProgress{ProbeID: "probe-a"}
	require.Equal(t, "probe-a", ack.ProbeID)
	require.Equal(t, "probe-a", progress.ProbeID)
}
