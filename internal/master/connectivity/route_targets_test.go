package connectivity

import (
	"context"
	"errors"
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

type recordingAgentTransportPolicyFinder struct {
	calls [][]string
	items map[string]models.Agent
	err   error
}

func (f *recordingAgentTransportPolicyFinder) FindAgentTransportPolicies(_ context.Context, ids []string) (map[string]models.Agent, error) {
	f.calls = append(f.calls, append([]string(nil), ids...))
	if f.err != nil {
		return nil, f.err
	}
	result := make(map[string]models.Agent, len(f.items))
	for id, agent := range f.items {
		result[id] = agent
	}
	return result, nil
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
	source := models.Agent{AgentID: "source"}

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

func TestBuildManyContextOverlaysDirectedPoliciesWithOneBatchLookup(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	finder := &recordingAgentTransportPolicyFinder{items: map[string]models.Agent{
		"target-a": {AgentID: "target-a", DirectInboundEnabled: false, RelayInboundEnabled: true},
		"target-b": {AgentID: "target-b", DirectInboundEnabled: true, RelayInboundEnabled: false},
	}}
	service.options.AgentTransportPolicyFinder = finder
	source := models.Agent{
		AgentID: "source", DirectOutboundEnabled: true, RelayOutboundEnabled: true,
	}

	batch, err := service.BuildManyContext(t.Context(), []models.Agent{source, source})
	require.NoError(t, err)
	require.Equal(t, [][]string{{"target-a", "target-b"}}, finder.calls)
	snapshot := batch.Items["source"].RouteTargets
	require.Equal(t, consts.RouteErrorTargetDirectInboundDisabled, snapshot.Targets["target-a"].Direct.PolicyReason)
	require.Equal(t, routeTargetStateDisabled, snapshot.Targets["target-a"].Direct.State)
	require.Equal(t, "reachable", snapshot.Targets["target-a"].Direct.Network, "policy overlay must retain diagnostics")
	require.False(t, snapshot.Targets["target-a"].Direct.Eligible)
	require.Equal(t, consts.RouteErrorTargetRelayInboundDisabled, snapshot.Targets["target-b"].Relay.PolicyReason)
	require.Equal(t, protocol.RelayProbeState(routeTargetStateDisabled), snapshot.Targets["target-b"].Relay.State)
	require.Equal(t, 1, snapshot.Summaries.Direct.Disabled)
	require.Equal(t, 1, snapshot.Summaries.Relay.Disabled)
	require.Equal(t, routeTargetStateReachable, snapshot.Summaries.Relay.State, "disabled targets must not degrade enabled targets")
}

func TestBuildContextSourceOutboundPolicyHasPriorityAndAllDisabledSummary(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	finder := &recordingAgentTransportPolicyFinder{items: map[string]models.Agent{
		"target-a": {AgentID: "target-a", DirectInboundEnabled: false, RelayInboundEnabled: false},
		"target-b": {AgentID: "target-b", DirectInboundEnabled: false, RelayInboundEnabled: false},
	}}
	service.options.AgentTransportPolicyFinder = finder
	snapshot, err := service.BuildContext(t.Context(), models.Agent{
		AgentID: "source", DirectOutboundEnabled: false, RelayOutboundEnabled: false,
	})
	require.NoError(t, err)
	for _, target := range snapshot.RouteTargets.Targets {
		require.Equal(t, consts.RouteErrorSourceDirectOutboundDisabled, target.Direct.PolicyReason)
		require.Equal(t, consts.RouteErrorSourceRelayOutboundDisabled, target.Relay.PolicyReason)
	}
	require.Equal(t, DirectSummary{State: routeTargetStateDisabled, Disabled: 2, Total: 2}, snapshot.RouteTargets.Summaries.Direct)
	require.Equal(t, routeTargetStateDisabled, snapshot.RouteTargets.Summaries.Relay.State)
	require.Equal(t, 2, snapshot.RouteTargets.Summaries.Relay.Disabled)
}

func TestBuildManyContextFinderBoundaries(t *testing.T) {
	t.Run("empty target set performs no lookup", func(t *testing.T) {
		finder := &recordingAgentTransportPolicyFinder{}
		service := NewService("master", Sources{}, Options{AgentTransportPolicyFinder: finder})
		batch, err := service.BuildManyContext(t.Context(), nil)
		require.NoError(t, err)
		require.Empty(t, batch.Items)
		require.Empty(t, finder.calls)
	})

	t.Run("lookup error is returned", func(t *testing.T) {
		service, _, _ := routeTargetsServiceForTest(t)
		finder := &recordingAgentTransportPolicyFinder{err: errors.New("database unavailable")}
		service.options.AgentTransportPolicyFinder = finder
		_, err := service.BuildContext(t.Context(), models.Agent{AgentID: "source", DirectOutboundEnabled: true, RelayOutboundEnabled: true})
		require.ErrorContains(t, err, "database unavailable")
		require.Len(t, finder.calls, 1)
	})

	t.Run("source outbound policy does not require a target finder", func(t *testing.T) {
		service, _, _ := routeTargetsServiceForTest(t)
		snapshot, err := service.BuildContext(t.Context(), models.Agent{
			AgentID: "source", DirectOutboundEnabled: false, RelayOutboundEnabled: false,
		})
		require.NoError(t, err)
		for _, target := range snapshot.RouteTargets.Targets {
			require.Equal(t, consts.RouteErrorSourceDirectOutboundDisabled, target.Direct.PolicyReason)
			require.Equal(t, consts.RouteErrorSourceRelayOutboundDisabled, target.Relay.PolicyReason)
		}
	})

	t.Run("missing target policy row stays unknown while source policy still overlays", func(t *testing.T) {
		service, _, _ := routeTargetsServiceForTest(t)
		finder := &recordingAgentTransportPolicyFinder{items: map[string]models.Agent{
			"target-a": {AgentID: "target-a", DirectInboundEnabled: true, RelayInboundEnabled: true},
		}}
		service.options.AgentTransportPolicyFinder = finder

		snapshot, err := service.BuildContext(t.Context(), models.Agent{
			AgentID: "source", DirectOutboundEnabled: false, RelayOutboundEnabled: true,
		})

		require.NoError(t, err)
		require.Equal(t, [][]string{{"target-a", "target-b"}}, finder.calls)
		missing := snapshot.RouteTargets.Targets["target-b"]
		require.Equal(t, consts.RouteErrorSourceDirectOutboundDisabled, missing.Direct.PolicyReason)
		require.Equal(t, routeTargetStateDisabled, missing.Direct.State)
		require.Empty(t, missing.Relay.PolicyReason, "an absent finder row is not a disabled target")
		require.Equal(t, protocol.RelayProbeState(routeTargetStateUnsupported), missing.Relay.State)
	})
}

func TestRouteTargetsGenerationIgnoresRouteUseTimestamps(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	source := models.Agent{AgentID: "source"}
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

func TestRouteTargetsSourceDirectPolicyDisablesDirectWithoutChangingDiagnostics(t *testing.T) {
	service, _, _ := routeTargetsServiceForTest(t)
	enabled, err := service.BuildContext(t.Context(), models.Agent{
		AgentID: "source", DirectOutboundEnabled: true, RelayOutboundEnabled: true,
	})
	require.NoError(t, err)
	disabled, err := service.BuildContext(t.Context(), models.Agent{
		AgentID: "source", DirectOutboundEnabled: false, RelayOutboundEnabled: true,
	})
	require.NoError(t, err)
	enabledTarget := enabled.RouteTargets.Targets["target-a"]
	disabledTarget := disabled.RouteTargets.Targets["target-a"]

	require.Equal(t, routeTargetStateReachable, enabledTarget.Direct.State)
	require.Equal(t, routeTargetStateDisabled, disabledTarget.Direct.State)
	require.Equal(t, consts.RouteErrorSourceDirectOutboundDisabled, disabledTarget.Direct.PolicyReason)
	require.Equal(t, enabledTarget.Direct.Network, disabledTarget.Direct.Network)
	require.Equal(t, enabledTarget.Direct.Identity, disabledTarget.Direct.Identity)
	require.Equal(t, enabledTarget.Direct.AddressFingerprint, disabledTarget.Direct.AddressFingerprint)
	require.Equal(t, enabledTarget.Relay.State, disabledTarget.Relay.State)
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
		service.ApplyRelayProbeResult("source", 7, target, "manual-relay-fp", 100, 11, 44, protocol.RelayProbeResult{Policy: protocol.ProbeRespectBusinessPolicy,
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
			"target-a": {protocol.AgentCapabilityDirectTunnelV1, protocol.AgentCapabilityRelayHTTPPingV1},
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
	service.ApplyDirectProbeResult("source", 7, directTarget, protocol.DirectProbeResult{Policy: protocol.ProbeRespectBusinessPolicy,
		TargetAgentID: "target-a", AddressFingerprint: "direct-fp", Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: 1_000, LatencyMS: 5,
	}, 1)
	relayTarget := ProbeTarget{AgentID: "target-a", Name: "Target A", ControlGeneration: 8}
	service.MarkRelayProbeChecking("source", 7, relayTarget, "relay-fp", 2, 11, 22)
	service.ApplyRelayProbeResult("source", 7, relayTarget, "relay-fp", 2, 11, 22, protocol.RelayProbeResult{Policy: protocol.ProbeRespectBusinessPolicy,
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
