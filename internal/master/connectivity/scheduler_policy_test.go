package connectivity

import (
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestPrepareProbePathsRejectsLegacyDirectCapability(t *testing.T) {
	scheduler, source, target := policySchedulerFixture(t)
	legacyCapability := strings.Join([]string{"agent", "direct", "ingress", "v1"}, "_")
	source.Capabilities = []string{legacyCapability}
	target.Capabilities = []string{legacyCapability}

	paths := scheduler.prepareProbePaths("source", 7, source, target, protocol.ProbeBypassBusinessPolicy)
	require.Empty(t, paths)
}

func TestPrepareProbePathsSeparatesAutomaticAndManualPolicy(t *testing.T) {
	tests := []struct {
		name       string
		policy     protocol.ProbePolicy
		mutate     func(*ProbeTarget, *ProbeTarget)
		wantDirect string
		wantRelay  string
	}{
		{name: "automatic all enabled", policy: protocol.ProbeRespectBusinessPolicy, wantDirect: "", wantRelay: ""},
		{
			name: "automatic skips source direct outbound disabled", policy: protocol.ProbeRespectBusinessPolicy,
			mutate: func(source, _ *ProbeTarget) { source.DirectOutboundEnabled = false }, wantRelay: "",
		},
		{
			name: "automatic skips target direct inbound disabled", policy: protocol.ProbeRespectBusinessPolicy,
			mutate: func(_, target *ProbeTarget) { target.DirectInboundEnabled = false }, wantRelay: "",
		},
		{
			name: "automatic skips source relay outbound disabled", policy: protocol.ProbeRespectBusinessPolicy,
			mutate: func(source, _ *ProbeTarget) { source.RelayOutboundEnabled = false }, wantDirect: "",
		},
		{
			name: "automatic skips target relay inbound disabled", policy: protocol.ProbeRespectBusinessPolicy,
			mutate: func(_, target *ProbeTarget) { target.RelayInboundEnabled = false }, wantDirect: "",
		},
		{
			name: "manual bypass keeps both paths and prefers source reasons", policy: protocol.ProbeBypassBusinessPolicy,
			mutate: func(source, target *ProbeTarget) {
				source.DirectOutboundEnabled = false
				target.DirectInboundEnabled = false
				source.RelayOutboundEnabled = false
				target.RelayInboundEnabled = false
			},
			wantDirect: consts.RouteErrorSourceDirectOutboundDisabled,
			wantRelay:  consts.RouteErrorSourceRelayOutboundDisabled,
		},
		{
			name: "manual bypass records target reasons", policy: protocol.ProbeBypassBusinessPolicy,
			mutate: func(_ *ProbeTarget, target *ProbeTarget) {
				target.DirectInboundEnabled = false
				target.RelayInboundEnabled = false
			},
			wantDirect: consts.RouteErrorTargetDirectInboundDisabled,
			wantRelay:  consts.RouteErrorTargetRelayInboundDisabled,
		},
		{
			name: "manual cannot create a disabled physical relay", policy: protocol.ProbeBypassBusinessPolicy,
			mutate: func(source, _ *ProbeTarget) { source.RelayMode = consts.RelayModeDisabled }, wantDirect: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler, source, target := policySchedulerFixture(t)
			if test.mutate != nil {
				test.mutate(&source, &target)
			}
			paths := scheduler.prepareProbePaths("source", 7, source, target, test.policy)
			got := make(map[probePath]preparedProbePath, len(paths))
			for _, path := range paths {
				got[path.kind] = path
				require.Equal(t, test.policy, path.policy)
			}

			direct, hasDirect := got[probePathDirect]
			relay, hasRelay := got[probePathRelay]
			wantDirect := test.wantDirect != "" || test.name == "automatic all enabled" ||
				test.name == "automatic skips source relay outbound disabled" ||
				test.name == "automatic skips target relay inbound disabled" ||
				test.name == "manual cannot create a disabled physical relay"
			wantRelay := test.wantRelay != "" || test.name == "automatic all enabled" ||
				test.name == "automatic skips source direct outbound disabled" ||
				test.name == "automatic skips target direct inbound disabled"
			require.Equal(t, wantDirect, hasDirect)
			require.Equal(t, wantRelay, hasRelay)
			if hasDirect {
				require.Equal(t, test.wantDirect, direct.policyReason)
			}
			if hasRelay {
				require.Equal(t, test.wantRelay, relay.policyReason)
			}
		})
	}
}

func TestProbeJobKeyKeepsRespectAndBypassWorkSeparate(t *testing.T) {
	base := probeJobKey{sourceID: "source", targetAgentID: "target", fingerprint: "fp", path: probePathDirect}
	respect := base
	respect.policy = protocol.ProbeRespectBusinessPolicy
	bypass := base
	bypass.policy = protocol.ProbeBypassBusinessPolicy
	require.NotEqual(t, respect, bypass)
}

func TestProbeRunnersProjectManualPolicyWithoutHidingNetworkDiagnostics(t *testing.T) {
	caller := &relaySchedulerCaller{}
	scheduler, source, target := policySchedulerFixture(t)
	service := scheduler.service

	directJob := probeJob{
		key: probeJobKey{
			sourceID: "source", targetAgentID: "target", fingerprint: "direct-fp",
			path: probePathDirect, policy: protocol.ProbeBypassBusinessPolicy,
		},
		target: target, source: source, sourceGeneration: 7, probeGeneration: 1,
		policyReason: consts.RouteErrorSourceDirectOutboundDisabled,
		startedAt:    time.Unix(100, 0),
	}
	direct := (&DirectProbeRunner{caller: caller, service: service}).Run(t.Context(), directJob)
	require.False(t, direct.succeeded)
	directSnapshot := service.directSnapshot("source").Targets["target"]
	require.Equal(t, "reachable", directSnapshot.Network)
	require.Equal(t, "verified", directSnapshot.Identity)
	require.False(t, directSnapshot.Eligible)
	require.Equal(t, protocol.ProbeBypassBusinessPolicy, caller.directTargets[0].Policy)

	relayJob := probeJob{
		key: probeJobKey{
			sourceID: "source", targetAgentID: "target", fingerprint: "relay-fp",
			path: probePathRelay, policy: protocol.ProbeBypassBusinessPolicy,
		},
		target: target, source: source, sourceGeneration: 7,
		sourceRelayGeneration: 11, targetRelayGeneration: 22, probeGeneration: 2,
		policyReason: consts.RouteErrorTargetRelayInboundDisabled,
		startedAt:    time.Unix(100, 0),
	}
	relay := (&RelayProbeRunner{caller: caller, service: service}).Run(t.Context(), relayJob)
	require.False(t, relay.succeeded)
	require.Equal(t, protocol.RelayProbeReachable, service.relayPathSnapshot("source").Targets["target"].State)
	require.Equal(t, protocol.ProbeBypassBusinessPolicy, caller.relayCalls[0].Policy)
}

func policySchedulerFixture(t *testing.T) (*Scheduler, ProbeTarget, ProbeTarget) {
	t.Helper()
	capabilities := []string{
		protocol.AgentCapabilityTunnelV2,
		protocol.AgentCapabilityDirectTunnelV1,
		protocol.AgentCapabilityRelayHTTPPingV1,
	}
	source := ProbeTarget{
		AgentID: "source", ControlGeneration: 7, Capabilities: capabilities,
		DirectInboundEnabled: true, DirectOutboundEnabled: true,
		RelayInboundEnabled: true, RelayOutboundEnabled: true, RelayMode: consts.RelayModeInherit,
	}
	target := ProbeTarget{
		AgentID: "target", ControlGeneration: 8, Capabilities: capabilities,
		Addresses:            []protocol.Address{{URL: "http://target"}},
		DirectInboundEnabled: true, DirectOutboundEnabled: true,
		RelayInboundEnabled: true, RelayOutboundEnabled: true, RelayMode: consts.RelayModeInherit,
	}
	service := NewService("master", Sources{
		Control: &controlSourceStub{facts: map[string]ControlSessionFact{
			"source": {Generation: 7}, "target": {Generation: 8},
		}},
		Relay: &relaySourceStub{facts: map[string]RelayRuntimeFact{
			"source": relayRuntimeForProbe(11), "target": relayRuntimeForProbe(22),
		}},
	}, Options{Now: func() time.Time { return time.Unix(100, 0) }})
	scheduler := NewScheduler(&relaySchedulerCaller{}, service, SchedulerOptions{})
	return scheduler, source, target
}
