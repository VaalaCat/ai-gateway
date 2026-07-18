package agent

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type apiRelaySource struct{ fact connectivity.RelayRuntimeFact }

func (s apiRelaySource) GetRelayRuntime(string) (connectivity.RelayRuntimeFact, bool) {
	return s.fact, true
}

func TestConnectionDiagnosticsAreBoundedAndSanitized(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	controlErrors := sensitiveErrors("control", 25)
	relayErrors := sensitiveErrors("relay", 25)
	control := &apiControlSource{facts: map[string]connectivity.ControlSessionFact{
		agent.AgentID: {Generation: 7, ConnectedAt: 80, HeartbeatAt: 90, RuntimeReportedAt: 95, RecentErrors: controlErrors},
	}}
	relay := apiRelaySource{fact: connectivity.RelayRuntimeFact{
		Support: "supported", Config: "configured", Availability: "available", Convergence: "converged",
		Desired: connectivity.RelayDesiredSnapshot{
			Mode: "auto", ConfiguredURI: "wss://user:pass@relay.example/ws?token=secret", EffectiveURI: "wss://relay.example/ws?ticket=secret", DesiredGeneration: 8,
		},
		Active:       connectivity.RelayActiveSnapshot{URI: "wss://user:pass@active.example/ws?token=secret", ActiveGeneration: 8, SessionGeneration: 9, ConnectedAt: 96},
		RecentErrors: relayErrors,
	}}
	service := connectivity.NewService("epoch-a", connectivity.Sources{Control: control, Relay: relay}, connectivity.Options{
		Now: func() time.Time { return time.Unix(100, 0) },
	})
	require.NoError(t, service.ReplaceDigest(agent.AgentID, protocol.RouteEdgeDigest{Generation: 31}))
	require.NoError(t, service.ApplyEvents(agent.AgentID, protocol.RouteTelemetryBatch{Generation: 31, Events: []protocol.RouteEvent{{
		RequestID: "request-24", TargetAgentID: "target-24", RouteID: 42, PathKind: "relay",
		Result: "error", Stage: "commit", CommitState: "commit_uncertain", ReasonCode: "relay_commit_uncertain",
		ObservedAt: 24, Sequence: 1,
	}}}))
	for generation := 1; generation <= 25; generation++ {
		targetID := fmt.Sprintf("target-%02d", generation)
		target := connectivity.ProbeTarget{AgentID: targetID, Name: targetID, Addresses: []protocol.Address{{URL: "https://user:pass@target.example?token=secret"}}}
		service.MarkDirectProbeChecking(agent.AgentID, 7, target, "fp-"+targetID, uint64(generation))
		service.ApplyDirectProbeResult(agent.AgentID, 7, target, protocol.DirectProbeResult{
			TargetAgentID: targetID, AddressFingerprint: "fp-" + targetID, Network: "unreachable", Identity: "unknown",
			CheckedAt: int64(generation), ReasonCode: "direct_connect",
		}, uint64(generation))
	}
	h := &Handler{Connections: service, ControlSessions: control}

	got, err := h.ConnectionDiagnostics(newTestContext(t, db), DiagnosticsRequest{ID: strconv.Itoa(int(agent.ID))})
	require.NoError(t, err)
	require.Equal(t, "epoch-a", got.SnapshotEpoch)
	require.Equal(t, uint64(7), got.Control.SessionGeneration)
	require.Len(t, got.Control.RecentErrors, 20)
	require.Len(t, got.Relay.RecentErrors, 20)
	require.Len(t, got.Direct.RecentErrors, 20)
	require.Len(t, got.RouteFailures, 1)
	require.Equal(t, "request-24", got.RouteFailures[0].RequestID)
	require.Equal(t, agent.AgentID, got.RouteFailures[0].SourceAgentID)
	require.Equal(t, "target-24", got.RouteFailures[0].TargetAgentID)
	require.Equal(t, uint(42), got.RouteFailures[0].RouteID)
	require.Equal(t, "relay", got.RouteFailures[0].PathKind)
	require.Equal(t, "commit", got.RouteFailures[0].Stage)
	require.Equal(t, "commit_uncertain", got.RouteFailures[0].CommitState)
	require.Equal(t, "relay_commit_uncertain", got.RouteFailures[0].ReasonCode)
	require.Equal(t, "wss://relay.example/ws", got.Relay.Desired.ConfiguredURI)
	require.Equal(t, "wss://relay.example/ws", got.Relay.Desired.EffectiveURI)
	require.Equal(t, "wss://active.example/ws", got.Relay.Active.URI)
	for _, group := range [][]connectivity.RecentError{got.Control.RecentErrors, got.Relay.RecentErrors, got.Direct.RecentErrors} {
		for _, recent := range group {
			require.NotContains(t, recent.Message, "secret")
			require.NotContains(t, recent.Message, "token")
		}
	}
}

func TestConnectionDiagnosticsRejectsMissingService(t *testing.T) {
	_, err := (&Handler{}).ConnectionDiagnostics(newTestContext(t, setupTestDB(t)), DiagnosticsRequest{ID: "1"})
	require.Equal(t, 500, requireAPIError(t, err).Status)
}

func TestConnectionDiagnosticsHonorsCanceledRequest(t *testing.T) {
	db := setupTestDB(t)
	agent := models.Agent{AgentID: "source", Name: "source", Status: consts.StatusEnabled}
	require.NoError(t, db.Create(&agent).Error)
	ctx := newTestContext(t, db)
	canceled, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(canceled)
	h := &Handler{Connections: connectivity.NewService("epoch", connectivity.Sources{}, connectivity.Options{})}

	_, err := h.ConnectionDiagnostics(ctx, DiagnosticsRequest{ID: strconv.Itoa(int(agent.ID))})
	require.Equal(t, "request_canceled", requireAPIError(t, err).Code)
}

func sensitiveErrors(prefix string, count int) []connectivity.RecentError {
	result := make([]connectivity.RecentError, 0, count)
	for i := 0; i < count; i++ {
		result = append(result, connectivity.RecentError{
			Code: fmt.Sprintf("%s-%02d", prefix, i), Stage: prefix,
			Message: "Authorization: Bearer secret-token", OccurredAt: int64(i), Count: 1,
		})
	}
	return result
}
