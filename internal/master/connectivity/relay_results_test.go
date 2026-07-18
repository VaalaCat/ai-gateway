package connectivity

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestRelayResultsApplyGenerationBoundReachableResult(t *testing.T) {
	service, relay := relayResultServiceForTest()
	target := ProbeTarget{AgentID: "target", Name: "Target"}
	service.MarkRelayProbeChecking("source", 7, target, "relay-fp", 31, 11, 22)
	service.ApplyRelayProbeResult("source", 7, target, "relay-fp", 31, 11, 22, protocol.RelayProbeResult{
		TargetAgentID: "target", State: protocol.RelayProbeReachable,
		Stage: protocol.RelayProbeStageResponse, CheckedAt: 100, LatencyMS: 8,
	})

	snapshot := service.relayPathSnapshot("source")
	require.Equal(t, RelayPathSummary{
		State: string(protocol.RelayProbeReachable), Reachable: 1, Total: 1,
	}, snapshot.Summary)
	require.Equal(t, RelayTargetSnapshot{
		TargetAgentID: "target", TargetName: "Target", State: protocol.RelayProbeReachable,
		Stage: protocol.RelayProbeStageResponse, ProbeGeneration: 31,
		RelayFingerprint: "relay-fp", SourceRelayGeneration: 11, TargetRelayGeneration: 22,
		CheckedAt: 100, LatencyMS: 8,
	}, snapshot.Targets["target"])

	// Returned snapshots must not expose mutable error pointers.
	relay.facts["source"] = relayRuntimeForProbe(12)
	unchanged := service.relayPathSnapshot("source")
	require.Equal(t, protocol.RelayProbeReachable, unchanged.Targets["target"].State)
}

func TestRelayResultsRejectLateOrReplacedGenerations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*relaySourceStub)
	}{
		{name: "source relay replaced", mutate: func(relay *relaySourceStub) { relay.facts["source"] = relayRuntimeForProbe(12) }},
		{name: "target relay replaced", mutate: func(relay *relaySourceStub) { relay.facts["target"] = relayRuntimeForProbe(23) }},
		{name: "source relay removed", mutate: func(relay *relaySourceStub) { delete(relay.facts, "source") }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, relay := relayResultServiceForTest()
			target := ProbeTarget{AgentID: "target", Name: "Target"}
			service.MarkRelayProbeChecking("source", 7, target, "relay-fp", 31, 11, 22)
			test.mutate(relay)
			service.ApplyRelayProbeResult("source", 7, target, "relay-fp", 31, 11, 22, protocol.RelayProbeResult{
				TargetAgentID: "target", State: protocol.RelayProbeReachable,
				Stage: protocol.RelayProbeStageResponse, CheckedAt: 100,
			})

			stored := service.relayPathSnapshot("source").Targets["target"]
			require.Equal(t, protocol.RelayProbeUnknown, stored.State)
			require.False(t, stored.Checking)
			require.Zero(t, stored.CheckedAt)
		})
	}
}

func TestRelayResultsNormalizeInvalidPayloadAndForgetSourceSession(t *testing.T) {
	service, _ := relayResultServiceForTest()
	target := ProbeTarget{AgentID: "target"}
	service.MarkRelayProbeChecking("source", 7, target, "relay-fp", 31, 11, 22)
	service.ApplyRelayProbeResult("source", 7, target, "relay-fp", 31, 11, 22, protocol.RelayProbeResult{
		TargetAgentID: "target", State: "invented", Stage: "invented", ReasonCode: "private_error", CheckedAt: 100,
	})

	stored := service.relayPathSnapshot("source").Targets["target"]
	require.Equal(t, protocol.RelayProbeUnreachable, stored.State)
	require.Equal(t, protocol.RelayProbeStageResponse, stored.Stage)
	require.Equal(t, consts.RouteErrorRelayProbeInvalidResult, stored.LastError.Code)

	service.Forget("source", 6)
	require.Len(t, service.relayPathSnapshot("source").Targets, 1)
	service.Forget("source", 7)
	require.Empty(t, service.relayPathSnapshot("source").Targets)
}

func TestRelayResultsIgnoreZeroAndMismatchedProbeIdentity(t *testing.T) {
	service, _ := relayResultServiceForTest()
	target := ProbeTarget{AgentID: "target"}
	service.MarkRelayProbeChecking("source", 7, target, "relay-fp", 31, 11, 22)

	service.ApplyRelayProbeResult("source", 7, target, "other-fp", 31, 11, 22, protocol.RelayProbeResult{
		TargetAgentID: "target", State: protocol.RelayProbeReachable, CheckedAt: 100,
	})
	service.ApplyRelayProbeResult("source", 7, target, "relay-fp", 30, 11, 22, protocol.RelayProbeResult{
		TargetAgentID: "target", State: protocol.RelayProbeReachable, CheckedAt: 100,
	})
	service.MarkRelayProbeChecking("source", 7, ProbeTarget{}, "relay-fp", 0, 0, 0)

	stored := service.relayPathSnapshot("source").Targets["target"]
	require.Equal(t, protocol.RelayProbeUnknown, stored.State)
	require.True(t, stored.Checking)
	require.Zero(t, stored.CheckedAt)
}

func TestRelayResultsRejectMismatchedTargetIdentity(t *testing.T) {
	for _, resultTargetID := range []string{"", "other-target"} {
		t.Run(resultTargetID, func(t *testing.T) {
			service, _ := relayResultServiceForTest()
			target := ProbeTarget{AgentID: "target"}
			service.MarkRelayProbeChecking("source", 7, target, "relay-fp", 31, 11, 22)
			service.ApplyRelayProbeResult("source", 7, target, "relay-fp", 31, 11, 22, protocol.RelayProbeResult{
				TargetAgentID: resultTargetID, State: protocol.RelayProbeReachable,
				Stage: protocol.RelayProbeStageResponse, CheckedAt: 100,
			})

			stored := service.relayPathSnapshot("source").Targets["target"]
			require.Equal(t, protocol.RelayProbeUnreachable, stored.State)
			require.Equal(t, consts.RouteErrorRelayProbeInvalidResult, stored.LastError.Code)
		})
	}
}

func relayResultServiceForTest() (*Service, *relaySourceStub) {
	relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{
		"source": relayRuntimeForProbe(11),
		"target": relayRuntimeForProbe(22),
	}}
	service := NewService("master", Sources{
		Control: &controlSourceStub{facts: map[string]ControlSessionFact{
			"source": {Generation: 7}, "target": {Generation: 8},
		}},
		Relay: relay,
	}, Options{Now: func() time.Time { return time.Unix(100, 0) }})
	return service, relay
}

func relayRuntimeForProbe(generation uint64) RelayRuntimeFact {
	return RelayRuntimeFact{
		Support: "supported", Config: "configured", Availability: "available",
		AcceptingNewStreams: true, Convergence: "converged",
		Active: RelayActiveSnapshot{SessionGeneration: generation},
	}
}
