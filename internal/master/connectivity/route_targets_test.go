package connectivity

import (
	"sort"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type routeTargetControlSource struct {
	facts        map[string]ControlSessionFact
	capabilities map[string][]string
}

func (s *routeTargetControlSource) GetControlSession(agentID string) (ControlSessionFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

func (s *routeTargetControlSource) Capabilities(agentID string) []string {
	return append([]string(nil), s.capabilities[agentID]...)
}

func TestRouteTargetsUseDirectedRouteEdgesAsMembersAndKeepFullPathSummaries(t *testing.T) {
	service, control, relay := routeTargetsServiceForTest(t)
	source := models.Agent{AgentID: "source", PeerRouteMode: consts.PeerRouteModeDirectFirst}

	first := service.Build(source).RouteTargets
	require.Equal(t, []string{"target-a", "target-b"}, sortedRouteTargetIDs(first.Targets))
	require.Equal(t, routeTargetStateReachable, first.Targets["target-a"].Direct.State)
	require.Equal(t, protocol.RelayProbeReachable, first.Targets["target-a"].Relay.State)
	require.Equal(t, routeTargetStateUnsupported, first.Targets["target-b"].Direct.State)
	require.Equal(t, protocol.RelayProbeState(routeTargetStateUnsupported), first.Targets["target-b"].Relay.State)
	require.Equal(t, 2, first.Summaries.Direct.Total)
	require.Equal(t, 1, first.Summaries.Direct.Reachable)
	require.Equal(t, 1, first.Summaries.Direct.Stale)
	require.Equal(t, 2, first.Summaries.Relay.Total)
	require.Equal(t, 1, first.Summaries.Relay.Reachable)
	require.Equal(t, 1, first.Summaries.Relay.Unsupported)

	again := service.Build(source).RouteTargets
	require.Equal(t, first.Generation, again.Generation)
	relay.facts["target-a"] = relayRuntimeForProbe(23)
	stale := service.Build(source).RouteTargets
	require.Equal(t, protocol.RelayProbeState(routeTargetStateStale), stale.Targets["target-a"].Relay.State)
	require.NotEqual(t, first.Generation, stale.Generation)

	delete(control.facts, "source")
	disconnected := service.Build(source).RouteTargets
	require.Equal(t, protocol.RelayProbeState(routeTargetStateUnknown), disconnected.Targets["target-a"].Relay.State)
}

func TestRouteTargetsGenerationIgnoresRouteUseTimestamps(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	source := models.Agent{AgentID: "source", PeerRouteMode: consts.PeerRouteModeDirectFirst}
	before := service.Build(source).RouteTargets.Generation

	require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{
		Generation: 1,
		Events: []protocol.RouteEvent{{
			TargetAgentID: "target-a", RouteID: 1, SelectorKind: "agent", PathKind: "direct",
			Result: "success", ObservedAt: 1_001, Sequence: 3,
		}},
	}))

	after := service.Build(source).RouteTargets.Generation
	require.Equal(t, before, after, "route activity alone must not invalidate a public target snapshot")
}

func TestRouteTargetsGenerationIsSafeForJavaScriptNumbers(t *testing.T) {
	const maxJavaScriptSafeInteger = uint64(1<<53 - 1)
	tests := []struct {
		name     string
		snapshot RouteTargetsSnapshot
	}{
		{name: "empty snapshot", snapshot: RouteTargetsSnapshot{}},
		{
			name: "single target",
			snapshot: RouteTargetsSnapshot{Targets: map[string]RouteTargetSnapshot{
				"agent-a": {TargetAgentID: "agent-a", TargetName: "Agent A"},
			}},
		},
		{
			name: "changed path state",
			snapshot: RouteTargetsSnapshot{Targets: map[string]RouteTargetSnapshot{
				"agent-a": {
					TargetAgentID: "agent-a",
					TargetName:    "Agent A",
					Relay:         RelayTargetSnapshot{State: protocol.RelayProbeReachable},
				},
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			generation := routeTargetsGeneration(tt.snapshot)
			require.NotZero(t, generation)
			require.LessOrEqual(t, generation, maxJavaScriptSafeInteger)
		})
	}
}

func TestRouteTargetsRelayOnlyPolicyDisablesDirectWithoutChangingInboundData(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	directFirst := service.Build(models.Agent{
		AgentID: "source", PeerRouteMode: consts.PeerRouteModeDirectFirst,
	}).RouteTargets.Targets["target-a"]
	relayOnly := service.Build(models.Agent{
		AgentID: "source", PeerRouteMode: consts.PeerRouteModeRelayOnly,
	}).RouteTargets.Targets["target-a"]

	require.Equal(t, routeTargetStateReachable, directFirst.Direct.State)
	require.Equal(t, routeTargetStateDisabled, relayOnly.Direct.State)
	require.Equal(t, directFirst.Direct.Network, relayOnly.Direct.Network)
	require.Equal(t, directFirst.Direct.Identity, relayOnly.Direct.Identity)
	require.Equal(t, directFirst.Direct.AddressFingerprint, relayOnly.Direct.AddressFingerprint)
	require.Equal(t, directFirst.Relay.State, relayOnly.Relay.State)
}

func TestRouteTargetsIncludeRetainedManualProbeTargets(t *testing.T) {
	t.Run("direct result without route edge", func(t *testing.T) {
		service, _, _ := routeTargetsServiceForTest(t)
		target := ProbeTarget{AgentID: "manual-direct", Name: "Manual Direct", Addresses: []protocol.Address{{URL: "http://manual-direct"}}}
		service.MarkDirectProbeChecking("source", 7, target, "manual-direct-fp", 99)

		targets := service.Build(models.Agent{AgentID: "source"}).RouteTargets.Targets
		require.Contains(t, targets, "manual-direct")
		require.Equal(t, "Manual Direct", targets["manual-direct"].TargetName)
		require.True(t, targets["manual-direct"].Direct.Checking)
	})

	t.Run("relay result without route edge", func(t *testing.T) {
		service, control, relay := routeTargetsServiceForTest(t)
		control.capabilities["manual-relay"] = []string{protocol.AgentCapabilityRelayHTTPPingV1}
		relay.facts["manual-relay"] = relayRuntimeForProbe(44)
		target := ProbeTarget{AgentID: "manual-relay", Name: "Manual Relay", ControlGeneration: 10}
		service.MarkRelayProbeChecking("source", 7, target, "manual-relay-fp", 100, 11, 44)
		service.ApplyRelayProbeResult("source", 7, target, "manual-relay-fp", 100, 11, 44, protocol.RelayProbeResult{
			TargetAgentID: "manual-relay", State: protocol.RelayProbeReachable,
			Stage: protocol.RelayProbeStageResponse, CheckedAt: 1_000, LatencyMS: 7,
		})

		targets := service.Build(models.Agent{AgentID: "source"}).RouteTargets.Targets
		require.Contains(t, targets, "manual-relay")
		require.Equal(t, "Manual Relay", targets["manual-relay"].TargetName)
		require.Equal(t, protocol.RelayProbeReachable, targets["manual-relay"].Relay.State)
	})

	t.Run("route edge and probe result are deduplicated while self loop is excluded", func(t *testing.T) {
		service, _, _ := routeTargetsServiceForTest(t)
		service.MarkDirectProbeChecking("source", 7, ProbeTarget{
			AgentID: "target-a", Addresses: []protocol.Address{{URL: "http://target-a"}},
		}, "direct-fp", 2)
		require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{
			Generation: 1,
			Events: []protocol.RouteEvent{{
				TargetAgentID: "source", RouteID: 99, SelectorKind: "agent", Result: "success",
				ObservedAt: 1_000, Sequence: 3,
			}},
		}))

		targets := service.Build(models.Agent{AgentID: "source"}).RouteTargets.Targets
		require.NotContains(t, targets, "source")
		require.Equal(t, []string{"target-a", "target-b"}, sortedRouteTargetIDs(targets))
	})
}

func routeTargetsServiceForTest(t *testing.T) (*Service, *routeTargetControlSource, *relaySourceStub) {
	t.Helper()
	now := time.Unix(1_000, 0)
	relayCapability := []string{protocol.AgentCapabilityRelayHTTPPingV1}
	control := &routeTargetControlSource{
		facts: map[string]ControlSessionFact{
			"source": {Generation: 7}, "target-a": {Generation: 8}, "target-b": {Generation: 9},
		},
		capabilities: map[string][]string{
			"source":   relayCapability,
			"target-a": {protocol.AgentCapabilityDirectIngressV1, protocol.AgentCapabilityRelayHTTPPingV1},
		},
	}
	relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{
		"source": relayRuntimeForProbe(11), "target-a": relayRuntimeForProbe(22),
		"target-b": relayRuntimeForProbe(33),
	}}
	service := NewService("master", Sources{Control: control, Relay: relay}, Options{Now: func() time.Time { return now }})
	require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{
		Generation: 1,
		Events: []protocol.RouteEvent{
			{TargetAgentID: "target-a", RouteID: 1, SelectorKind: "agent", PathKind: "direct", Result: "success", ObservedAt: 1_000, Sequence: 1},
			{TargetAgentID: "target-b", RouteID: 2, SelectorKind: "agent", PathKind: "direct", Result: "error", ReasonCode: "direct_connect", ObservedAt: 1_000, Sequence: 2},
		},
	}))
	directTarget := ProbeTarget{
		AgentID: "target-a", Name: "Target A", ControlGeneration: 8,
		Addresses: []protocol.Address{{URL: "http://target-a"}},
	}
	service.MarkDirectProbeChecking("source", 7, directTarget, "direct-fp", 1)
	service.ApplyDirectProbeResult("source", 7, directTarget, protocol.DirectProbeResult{
		TargetAgentID: "target-a", AddressFingerprint: "direct-fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 1_000, LatencyMS: 5,
	}, 1)
	relayTarget := ProbeTarget{AgentID: "target-a", Name: "Target A", ControlGeneration: 8}
	service.MarkRelayProbeChecking("source", 7, relayTarget, "relay-fp", 2, 11, 22)
	service.ApplyRelayProbeResult("source", 7, relayTarget, "relay-fp", 2, 11, 22, protocol.RelayProbeResult{
		TargetAgentID: "target-a", State: protocol.RelayProbeReachable,
		Stage: protocol.RelayProbeStageResponse, CheckedAt: 1_000, LatencyMS: 6,
	})
	return service, control, relay
}

func sortedRouteTargetIDs(targets map[string]RouteTargetSnapshot) []string {
	result := make([]string, 0, len(targets))
	for targetID := range targets {
		result = append(result, targetID)
	}
	sort.Strings(result)
	return result
}
