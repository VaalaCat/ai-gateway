package connectivity

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type relaySchedulerFinder struct {
	source  ProbeTarget
	targets []ProbeTarget
}

func (f relaySchedulerFinder) FindEnabledProbeTargets(_ context.Context, targetIDs []string) ([]ProbeTarget, error) {
	if targetIDs == nil {
		return append([]ProbeTarget(nil), f.targets...), nil
	}
	wanted := make(map[string]struct{}, len(targetIDs))
	for _, targetID := range targetIDs {
		wanted[targetID] = struct{}{}
	}
	result := make([]ProbeTarget, 0, len(targetIDs))
	for _, target := range f.targets {
		if _, ok := wanted[target.AgentID]; ok {
			result = append(result, target)
		}
	}
	return result, nil
}

func (f relaySchedulerFinder) FindEnabledProbeSource(context.Context, string) (ProbeTarget, error) {
	return cloneProbeTarget(f.source), nil
}

type relaySchedulerCaller struct {
	mu          sync.Mutex
	directCalls int
	relayCalls  []protocol.RelayProbeTarget
	relayStart  chan struct{}
	relayBlock  <-chan struct{}
}

func (c *relaySchedulerCaller) CallDirectProbe(_ context.Context, _ string, _ uint64, target protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	c.mu.Lock()
	c.directCalls++
	c.mu.Unlock()
	return protocol.DirectProbeResult{
		TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
		Network: "reachable", Identity: "verified", Eligible: true, CheckedAt: 100,
	}, nil
}

func (c *relaySchedulerCaller) CallRelayProbe(ctx context.Context, _ string, _ uint64, target protocol.RelayProbeTarget) (protocol.RelayProbeResult, error) {
	c.mu.Lock()
	c.relayCalls = append(c.relayCalls, target)
	started := c.relayStart
	block := c.relayBlock
	c.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if block != nil {
		select {
		case <-ctx.Done():
			return protocol.RelayProbeResult{}, context.Cause(ctx)
		case <-block:
		}
	}
	return protocol.RelayProbeResult{
		TargetAgentID: target.TargetAgentID, State: protocol.RelayProbeReachable,
		Stage: protocol.RelayProbeStageResponse, CheckedAt: 100, LatencyMS: 9,
	}, nil
}

func (c *relaySchedulerCaller) snapshot() (int, []protocol.RelayProbeTarget) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.directCalls, append([]protocol.RelayProbeTarget(nil), c.relayCalls...)
}

func TestSchedulerRunsDirectAndRelayPathsButTracksManualProgressPerTarget(t *testing.T) {
	release := make(chan struct{})
	caller := &relaySchedulerCaller{relayStart: make(chan struct{}), relayBlock: release}
	scheduler, service := relaySchedulerForTest(caller, consts.PeerRouteModeDirectFirst, true, true)
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- scheduler.Run(runCtx) }()
	t.Cleanup(func() {
		cancelRun()
		<-runDone
	})

	ack, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{
		Kind: "targets", TargetAgentIDs: []string{"target"},
	})
	require.NoError(t, err)
	select {
	case <-caller.relayStart:
	case <-time.After(time.Second):
		t.Fatal("relay probe did not start")
	}
	require.Eventually(t, func() bool {
		directCalls, _ := caller.snapshot()
		return directCalls == 1
	}, time.Second, time.Millisecond)

	progress, ok := scheduler.FindProgressForSource("source", ack.ProbeID)
	require.True(t, ok)
	require.Equal(t, "running", progress.State)
	require.Equal(t, 1, progress.TargetTotal)
	require.Equal(t, 1, progress.Remaining, "relay path is still pending for the same target")

	close(release)
	require.Eventually(t, func() bool {
		progress, found := scheduler.FindProgressForSource("source", ack.ProbeID)
		return found && progress.State == "completed" && progress.Remaining == 0
	}, time.Second, time.Millisecond)
	directCalls, relayCalls := caller.snapshot()
	require.Equal(t, 1, directCalls)
	require.Equal(t, []protocol.RelayProbeTarget{{
		TargetAgentID: "target", SourceRelayGeneration: 11, TargetRelayGeneration: 22,
	}}, relayCalls)
	require.True(t, service.directSnapshot("source").Targets["target"].Eligible)
	require.Equal(t, protocol.RelayProbeReachable, service.relayPathSnapshot("source").Targets["target"].State)
}

func TestSchedulerRelayOnlyNeverQueuesDirectProbe(t *testing.T) {
	caller := &relaySchedulerCaller{}
	scheduler, service := relaySchedulerForTest(caller, consts.PeerRouteModeRelayOnly, true, true)
	runCtx, cancelRun := context.WithCancel(t.Context())
	runDone := make(chan error, 1)
	go func() { runDone <- scheduler.Run(runCtx) }()

	ack, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{
		Kind: "targets", TargetAgentIDs: []string{"target"},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		progress, found := scheduler.FindProgressForSource("source", ack.ProbeID)
		return found && progress.State == "completed"
	}, time.Second, time.Millisecond)
	directCalls, relayCalls := caller.snapshot()
	require.Zero(t, directCalls)
	require.Len(t, relayCalls, 1)
	require.Empty(t, service.directSnapshot("source").Targets)
	require.Equal(t, protocol.RelayProbeReachable, service.relayPathSnapshot("source").Targets["target"].State)

	cancelRun()
	require.ErrorIs(t, <-runDone, context.Canceled)
}

func TestSchedulerOnlyRunsPathsWhoseContractsAndInputsAreAvailable(t *testing.T) {
	tests := []struct {
		name             string
		sourceCapability bool
		targetCapability bool
		targetDirect     bool
		targetAddresses  bool
		wantDirect       int
		wantRelay        int
	}{
		{name: "both supported", sourceCapability: true, targetCapability: true, targetDirect: true, targetAddresses: true, wantDirect: 1, wantRelay: 1},
		{name: "source relay unsupported", targetCapability: true, targetDirect: true, targetAddresses: true, wantDirect: 1},
		{name: "target relay unsupported", sourceCapability: true, targetDirect: true, targetAddresses: true, wantDirect: 1},
		{name: "relay does not require direct address", sourceCapability: true, targetCapability: true, targetDirect: true, wantRelay: 1},
		{name: "direct ingress unsupported falls back to relay probe", sourceCapability: true, targetCapability: true, targetAddresses: true, wantRelay: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			caller := &relaySchedulerCaller{}
			scheduler, _ := relaySchedulerForTestWithInputs(
				caller, consts.PeerRouteModeDirectFirst,
				test.sourceCapability, test.targetCapability, test.targetDirect, test.targetAddresses,
			)
			runCtx, cancelRun := context.WithCancel(t.Context())
			runDone := make(chan error, 1)
			go func() { runDone <- scheduler.Run(runCtx) }()

			ack, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{
				Kind: "targets", TargetAgentIDs: []string{"target"},
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				progress, found := scheduler.FindProgressForSource("source", ack.ProbeID)
				return found && progress.State == "completed"
			}, time.Second, time.Millisecond)
			directCalls, relayCalls := caller.snapshot()
			require.Equal(t, test.wantDirect, directCalls)
			require.Len(t, relayCalls, test.wantRelay)

			cancelRun()
			require.ErrorIs(t, <-runDone, context.Canceled)
		})
	}
}

func relaySchedulerForTest(
	caller *relaySchedulerCaller,
	mode string,
	sourceCapability bool,
	targetCapability bool,
) (*Scheduler, *Service) {
	return relaySchedulerForTestWithInputs(caller, mode, sourceCapability, targetCapability, true, true)
}

func relaySchedulerForTestWithInputs(
	caller *relaySchedulerCaller,
	mode string,
	sourceCapability bool,
	targetCapability bool,
	targetDirect bool,
	targetAddresses bool,
) (*Scheduler, *Service) {
	capability := func(enabled bool) []string {
		if !enabled {
			return nil
		}
		return []string{protocol.AgentCapabilityRelayHTTPPingV1}
	}
	source := ProbeTarget{
		AgentID: "source", ControlGeneration: 7, PeerRouteMode: mode,
		Capabilities: capability(sourceCapability),
	}
	target := ProbeTarget{
		AgentID: "target", Name: "Target", ControlGeneration: 8,
		Capabilities: capability(targetCapability),
	}
	if targetDirect {
		target.Capabilities = append(target.Capabilities, protocol.AgentCapabilityDirectIngressV1)
	}
	if targetAddresses {
		target.Addresses = []protocol.Address{{URL: "http://target"}}
	}
	relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{
		"source": relayRuntimeForProbe(11), "target": relayRuntimeForProbe(22),
	}}
	service := NewService("master", Sources{
		Control: &controlSourceStub{facts: map[string]ControlSessionFact{
			"source": {Generation: 7}, "target": {Generation: 8},
		}},
		Relay: relay,
	}, Options{Now: func() time.Time { return time.Unix(100, 0) }})
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		ProbeTargetFinder: relaySchedulerFinder{source: source, targets: []ProbeTarget{target}},
		GlobalConcurrency: 2, PerSourceConcurrency: 2,
	})
	return scheduler, service
}
