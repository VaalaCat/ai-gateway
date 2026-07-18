package connectivity

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type schedulerControlSource map[string]uint64

func (s schedulerControlSource) GetControlSession(agentID string) (ControlSessionFact, bool) {
	generation, ok := s[agentID]
	return ControlSessionFact{Generation: generation}, ok
}

type lockedSchedulerControlSource struct {
	mu          sync.Mutex
	generations map[string]uint64
}

func (s *lockedSchedulerControlSource) GetControlSession(agentID string) (ControlSessionFact, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	generation, ok := s.generations[agentID]
	return ControlSessionFact{Generation: generation}, ok
}

func (s *lockedSchedulerControlSource) set(agentID string, generation uint64) {
	s.mu.Lock()
	s.generations[agentID] = generation
	s.mu.Unlock()
}

type schedulerFinder struct{ targets []ProbeTarget }

func (f schedulerFinder) FindEnabledProbeTargets(context.Context, []string) ([]ProbeTarget, error) {
	return append([]ProbeTarget(nil), f.targets...), nil
}

type failingSchedulerFinder struct {
	err   error
	calls int
}

func (f *failingSchedulerFinder) FindEnabledProbeTargets(context.Context, []string) ([]ProbeTarget, error) {
	f.calls++
	return nil, f.err
}

type recordingSchedulerFinder struct {
	targets []ProbeTarget
	calls   [][]string
}

func (f *recordingSchedulerFinder) FindEnabledProbeTargets(_ context.Context, targetAgentIDs []string) ([]ProbeTarget, error) {
	f.calls = append(f.calls, append([]string(nil), targetAgentIDs...))
	if targetAgentIDs == nil {
		return append([]ProbeTarget(nil), f.targets...), nil
	}
	wanted := make(map[string]struct{}, len(targetAgentIDs))
	for _, agentID := range targetAgentIDs {
		wanted[agentID] = struct{}{}
	}
	result := make([]ProbeTarget, 0, len(targetAgentIDs))
	for _, target := range f.targets {
		if _, ok := wanted[target.AgentID]; ok {
			result = append(result, target)
		}
	}
	return result, nil
}

type blockingSchedulerFinder struct {
	mu      sync.Mutex
	calls   int
	entered chan struct{}
	release chan struct{}
	targets []ProbeTarget
}

func (f *blockingSchedulerFinder) FindEnabledProbeTargets(context.Context, []string) ([]ProbeTarget, error) {
	f.mu.Lock()
	f.calls++
	entered := f.entered
	f.mu.Unlock()
	if entered != nil {
		select {
		case <-entered:
		default:
			close(entered)
		}
	}
	if f.release != nil {
		<-f.release
	}
	return append([]ProbeTarget(nil), f.targets...), nil
}

func (f *blockingSchedulerFinder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type schedulerCaller struct {
	mu          sync.Mutex
	calls       []schedulerCall
	active      int
	maxActive   int
	bySource    map[string]int
	maxBySource map[string]int
	block       <-chan struct{}
	result      protocol.DirectProbeResult
	err         error
}

type generationCheckingSchedulerCaller struct {
	mu      sync.Mutex
	control *lockedSchedulerControlSource
	calls   []uint64
}

func (c *generationCheckingSchedulerCaller) CallDirectProbe(_ context.Context, sourceID string, generation uint64, _ protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, generation)
	c.mu.Unlock()
	current, ok := c.control.GetControlSession(sourceID)
	if !ok || current.Generation != generation {
		return protocol.DirectProbeResult{}, ErrConnectionGenerationChanged
	}
	return protocol.DirectProbeResult{Eligible: true}, nil
}

func (c *generationCheckingSchedulerCaller) generations() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint64(nil), c.calls...)
}

type failingProbeCaller struct{ err error }

func (c failingProbeCaller) CallDirectProbe(context.Context, string, uint64, protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	return protocol.DirectProbeResult{}, c.err
}

type typedCancelledProbeCaller struct{}

func (typedCancelledProbeCaller) CallDirectProbe(context.Context, string, uint64, protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	return protocol.DirectProbeResult{Network: "reachable", Identity: "unknown", ReasonCode: "cancelled"}, nil
}

type schedulerCall struct {
	sourceID   string
	generation uint64
	target     protocol.DirectProbeTarget
}

func (c *schedulerCaller) CallDirectProbe(ctx context.Context, sourceID string, generation uint64, target protocol.DirectProbeTarget) (protocol.DirectProbeResult, error) {
	c.mu.Lock()
	c.calls = append(c.calls, schedulerCall{sourceID: sourceID, generation: generation, target: target})
	c.active++
	if c.active > c.maxActive {
		c.maxActive = c.active
	}
	if c.bySource == nil {
		c.bySource = make(map[string]int)
		c.maxBySource = make(map[string]int)
	}
	c.bySource[sourceID]++
	if c.bySource[sourceID] > c.maxBySource[sourceID] {
		c.maxBySource[sourceID] = c.bySource[sourceID]
	}
	c.mu.Unlock()

	if c.block != nil {
		select {
		case <-ctx.Done():
			c.finish(sourceID)
			return protocol.DirectProbeResult{}, context.Cause(ctx)
		case <-c.block:
		}
	}
	c.finish(sourceID)
	result := c.result
	result.TargetAgentID = target.TargetAgentID
	result.AddressFingerprint = target.AddressFingerprint
	if result.Network == "" {
		result.Network, result.Identity, result.Eligible = "reachable", "verified", true
	}
	return result, c.err
}

func (c *schedulerCaller) finish(sourceID string) {
	c.mu.Lock()
	c.active--
	c.bySource[sourceID]--
	c.mu.Unlock()
}

func (c *schedulerCaller) snapshot() ([]schedulerCall, int, map[string]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]schedulerCall(nil), c.calls...), c.maxActive, cloneIntMap(c.maxBySource)
}

func TestSchedulerManualScopesExcludeSourceAndKeepProbeIDsIndependent(t *testing.T) {
	caller := &schedulerCaller{}
	service := NewService("master-a", Sources{Control: schedulerControlSource{"source": 11}}, Options{})
	directCapability := []string{protocol.AgentCapabilityDirectIngressV1}
	targets := []ProbeTarget{
		{AgentID: "source", Tags: []string{"gpu"}, ControlGeneration: 11, Addresses: []protocol.Address{{URL: "http://source"}}, Capabilities: directCapability},
		{AgentID: "a", Tags: []string{"gpu"}, ControlGeneration: 21, Addresses: []protocol.Address{{URL: "http://a"}}, Capabilities: directCapability},
		{AgentID: "b", Tags: []string{"cpu"}, ControlGeneration: 22, Addresses: []protocol.Address{{URL: "http://b"}}, Capabilities: directCapability},
		{AgentID: "c", Tags: []string{"gpu"}, ControlGeneration: 23, Addresses: []protocol.Address{{URL: "http://c"}}, Capabilities: directCapability},
	}
	scheduler := NewScheduler(caller, service, SchedulerOptions{ProbeTargetFinder: schedulerFinder{targets: targets}})

	all, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	require.Equal(t, 3, all.TargetTotal)
	tag, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "tag", Tag: "gpu"})
	require.NoError(t, err)
	require.Equal(t, 2, tag.TargetTotal)
	explicit, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "targets", TargetAgentIDs: []string{"b", "missing", "source"}})
	require.NoError(t, err)
	require.Equal(t, 1, explicit.TargetTotal)
	require.NotEqual(t, all.ProbeID, tag.ProbeID)
	require.NotEqual(t, tag.ProbeID, explicit.ProbeID)

	runScheduler(t, scheduler)
	waitProgressState(t, scheduler, all.ProbeID, "completed")
	waitProgressState(t, scheduler, tag.ProbeID, "completed")
	waitProgressState(t, scheduler, explicit.ProbeID, "completed")
	calls, _, _ := caller.snapshot()
	require.Len(t, calls, 3, "source/target/fingerprint dedupe must share physical probes")
}

func TestSchedulerManualJoiningRunningDedupeCompletesBothProgressRecords(t *testing.T) {
	release := make(chan struct{})
	caller := &schedulerCaller{block: release}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		GlobalConcurrency: 1, PerSourceConcurrency: 1,
		ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}},
	})
	first, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	runScheduler(t, scheduler)
	require.Eventually(t, func() bool { return scheduler.ResourceCount() == 1 }, time.Second, 10*time.Millisecond)
	second, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	close(release)
	waitProgressState(t, scheduler, first.ProbeID, "completed")
	waitProgressState(t, scheduler, second.ProbeID, "completed")
	calls, _, _ := caller.snapshot()
	require.Len(t, calls, 1)
}

func TestSchedulerHonorsGlobalAndPerSourceConcurrency(t *testing.T) {
	release := make(chan struct{})
	caller := &schedulerCaller{block: release}
	service := NewService("master-a", Sources{Control: schedulerControlSource{"source-a": 1, "source-b": 2}}, Options{})
	targets := make([]ProbeTarget, 0, 8)
	for _, id := range []string{"t1", "t2", "t3", "t4"} {
		targets = append(targets, ProbeTarget{
			AgentID: id, ControlGeneration: 10, Addresses: []protocol.Address{{URL: "http://" + id}},
			Capabilities: []string{protocol.AgentCapabilityDirectIngressV1},
		})
	}
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		GlobalConcurrency: 3, PerSourceConcurrency: 2, ProbeTargetFinder: schedulerFinder{targets: targets},
	})
	_, err := scheduler.EnqueueManual(t.Context(), "source-a", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	_, err = scheduler.EnqueueManual(t.Context(), "source-b", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	runScheduler(t, scheduler)
	require.Eventually(t, func() bool {
		_, max, perSource := caller.snapshot()
		return max == 3 && perSource["source-a"] <= 2 && perSource["source-b"] <= 2
	}, time.Second, 10*time.Millisecond)
	close(release)
	require.Eventually(t, func() bool { return scheduler.ResourceCount() == 0 }, time.Second, 10*time.Millisecond)
	_, max, perSource := caller.snapshot()
	require.Equal(t, 3, max)
	require.LessOrEqual(t, perSource["source-a"], 2)
	require.LessOrEqual(t, perSource["source-b"], 2)
}

func TestSchedulerManualPriorityBeatsRecoveryAndStale(t *testing.T) {
	service := NewService("master-a", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{GlobalConcurrency: 1, PerSourceConcurrency: 1})
	enqueueProbeForTest(scheduler, probePriorityStale, "source", probeTargetForScheduler("stale"), "")
	enqueueProbeForTest(scheduler, probePriorityRecovery, "source", probeTargetForScheduler("recovery"), "")
	enqueueProbeForTest(scheduler, probePriorityManual, "source", probeTargetForScheduler("manual"), "probe-manual")

	first, ok := scheduler.nextJob()
	require.True(t, ok)
	require.Equal(t, "manual", first.target.AgentID)
	completeProbeForTest(scheduler, first)
	second, ok := scheduler.nextJob()
	require.True(t, ok)
	require.Equal(t, "recovery", second.target.AgentID)
	completeProbeForTest(scheduler, second)
	third, ok := scheduler.nextJob()
	require.True(t, ok)
	require.Equal(t, "stale", third.target.AgentID)
}

func TestSchedulerAutomaticCandidatesComeOnlyFromActualRouteEdges(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	service := NewService("master-a", Sources{Control: schedulerControlSource{"source": 7}}, Options{Now: func() time.Time { return now }})
	require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{Generation: 1, Events: []protocol.RouteEvent{{
		TargetAgentID: "actual", RouteID: 9, SelectorKind: "agent_id", PathKind: "relay",
		Result: "error", ReasonCode: "direct_connect", AddressFingerprint: "old", ObservedAt: now.Unix(), Sequence: 1,
	}}}))
	caller := &schedulerCaller{}
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		Now: func() time.Time { return now },
		ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{
			probeTargetForScheduler("actual"), probeTargetForScheduler("unrelated"),
		}},
	})
	require.NoError(t, scheduler.enqueueAutomatic(t.Context()))
	runScheduler(t, scheduler)
	require.Eventually(t, func() bool {
		calls, _, _ := caller.snapshot()
		return len(calls) == 1
	}, time.Second, 10*time.Millisecond)
	calls, _, _ := caller.snapshot()
	require.Equal(t, "actual", calls[0].target.TargetAgentID)
}

func TestSchedulerAutomaticFinderUsesOnlyStableUniqueRouteTargets(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	service := NewService("master", Sources{Control: schedulerControlSource{"source-a": 7, "source-b": 8}}, Options{
		Now: func() time.Time { return now },
	})
	require.NoError(t, service.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 1, Events: []protocol.RouteEvent{
		{TargetAgentID: "target-b", RouteID: 1, SelectorKind: "agent_id", PathKind: "direct", Result: "success", ObservedAt: now.Unix(), Sequence: 1},
		{TargetAgentID: "target-a", RouteID: 2, SelectorKind: "agent_id", PathKind: "direct", Result: "success", ObservedAt: now.Unix(), Sequence: 2},
	}}))
	require.NoError(t, service.ApplyEvents("source-b", protocol.RouteTelemetryBatch{Generation: 1, Events: []protocol.RouteEvent{
		{TargetAgentID: "target-b", RouteID: 3, SelectorKind: "agent_id", PathKind: "relay", Result: "error", ObservedAt: now.Unix(), Sequence: 1},
	}}))
	finder := &recordingSchedulerFinder{targets: []ProbeTarget{
		probeTargetForScheduler("target-a"), probeTargetForScheduler("target-b"), probeTargetForScheduler("unrelated"),
	}}
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})

	require.NoError(t, scheduler.enqueueAutomatic(t.Context()))
	require.Equal(t, [][]string{{"target-a", "target-b"}}, finder.calls)
}

func TestSchedulerAutomaticSkipsFinderWithoutRouteEdges(t *testing.T) {
	finder := &recordingSchedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("unrelated")}}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})

	require.NoError(t, scheduler.enqueueAutomatic(t.Context()))
	require.Empty(t, finder.calls)
}

func TestSchedulerAutomaticFinderFailuresAreSuppressedAndCancellationIsSilent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	core, observed := observer.New(zap.DebugLevel)
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{
		Now: func() time.Time { return now }, Logger: zap.New(core),
	})
	finder := &failingSchedulerFinder{err: errors.New("Authorization: Bearer secret-token")}
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{
		Now: func() time.Time { return now }, ProbeTargetFinder: finder,
	})

	require.NoError(t, scheduler.enqueueAutomaticObserved(t.Context()))
	require.Zero(t, finder.calls)
	require.Zero(t, observed.Len())

	require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{Generation: 1, Events: []protocol.RouteEvent{{
		TargetAgentID: "target", RouteID: 7, PathKind: "direct", Result: "error",
		ReasonCode: "direct_connect", ObservedAt: now.Unix(), Sequence: 1,
	}}}))
	require.Error(t, scheduler.enqueueAutomaticObserved(t.Context()))
	require.Error(t, scheduler.enqueueAutomaticObserved(t.Context()))
	require.Equal(t, 2, finder.calls)
	require.Equal(t, 1, observed.Len())
	fields := observed.All()[0].ContextMap()
	require.Equal(t, zap.WarnLevel, observed.All()[0].Level)
	require.Equal(t, "connectivity", fields["path_kind"])
	require.Equal(t, "target_discovery", fields["stage"])
	require.Equal(t, "automatic_probe_targets_failed", fields["reason_code"])
	require.Equal(t, "redacted", fields["error"])

	now = now.Add(time.Minute)
	require.Error(t, scheduler.enqueueAutomaticObserved(t.Context()))
	require.Equal(t, 2, observed.Len())
	require.EqualValues(t, 1, observed.All()[1].ContextMap()["suppressed_count"])

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	finder.err = context.Canceled
	require.ErrorIs(t, scheduler.enqueueAutomaticObserved(canceled), context.Canceled)
	require.Equal(t, 2, observed.Len())
}

func TestSchedulerManualFinderQueryMatchesScope(t *testing.T) {
	finder := &recordingSchedulerFinder{targets: []ProbeTarget{
		probeTargetForScheduler("source"), probeTargetForScheduler("target-a"), probeTargetForScheduler("target-b"),
	}}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})

	_, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	_, err = scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "tag", Tag: "gpu"})
	require.NoError(t, err)
	_, err = scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{
		Kind: "targets", TargetAgentIDs: []string{"target-b", "target-a", "target-b"},
	})
	require.NoError(t, err)
	require.Equal(t, [][]string{nil, nil, {"target-a", "target-b"}}, finder.calls)
}

func TestSchedulerAutomaticPriorityUsesCurrentProbeFingerprintAndFailures(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name         string
		edgeFailure  bool
		directResult *protocol.DirectProbeResult
		wantPriority probePriority
	}{
		{name: "recent successful edge without probe result is stale", wantPriority: probePriorityStale},
		{name: "successful edge with matching successful probe is stale", directResult: &protocol.DirectProbeResult{Network: "reachable", Identity: "verified", Eligible: true}, wantPriority: probePriorityStale},
		{name: "failed edge is recovery", edgeFailure: true, wantPriority: probePriorityRecovery},
		{name: "matching failed probe is recovery", directResult: &protocol.DirectProbeResult{Network: "unreachable", Identity: "unknown", ReasonCode: "direct_connect"}, wantPriority: probePriorityRecovery},
		{name: "changed probe fingerprint is recovery", directResult: &protocol.DirectProbeResult{AddressFingerprint: "old-probe-fingerprint", Network: "reachable", Identity: "verified", Eligible: true}, wantPriority: probePriorityRecovery},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService("master", Sources{Control: schedulerControlSource{"source": 7}}, Options{Now: func() time.Time { return now }})
			event := protocol.RouteEvent{
				TargetAgentID: "target", RouteID: 1, SelectorKind: "agent_id", PathKind: "direct",
				Result: "success", AddressFingerprint: "address-only-fingerprint", ObservedAt: now.Unix(), Sequence: 1,
			}
			if test.edgeFailure {
				event.PathKind, event.Result, event.ReasonCode = "relay", "error", "direct_connect"
			}
			require.NoError(t, service.ApplyEvents("source", protocol.RouteTelemetryBatch{Generation: 1, Events: []protocol.RouteEvent{event}}))
			target := probeTargetForScheduler("target")
			fingerprint := CanonicalProbeFingerprint("source", 7, target)
			if test.directResult != nil {
				result := *test.directResult
				result.TargetAgentID = target.AgentID
				if result.AddressFingerprint == "" {
					result.AddressFingerprint = fingerprint
				}
				result.CheckedAt = now.Add(-time.Minute).Unix()
				service.MarkDirectProbeChecking("source", 7, target, result.AddressFingerprint, 1)
				service.ApplyDirectProbeResult("source", 7, target, result, 1)
			}
			scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{
				Now: func() time.Time { return now }, ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{target}},
			})
			require.NoError(t, scheduler.enqueueAutomatic(t.Context()))
			key := probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: fingerprint}
			scheduler.mu.Lock()
			job := scheduler.jobs[key]
			scheduler.mu.Unlock()
			require.NotNil(t, job)
			require.Equal(t, test.wantPriority, job.priority)
		})
	}
}

func TestSchedulerRetryAndTTLBoundaries(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	scheduler := NewScheduler(&schedulerCaller{}, NewService("master", Sources{}, Options{}), SchedulerOptions{
		SuccessTTL: 5 * time.Minute, FailureRetryMin: 30 * time.Second,
		FailureRetryMax: 5 * time.Minute, Now: func() time.Time { return now },
	})
	key := probeJobKey{sourceID: "source", targetAgentID: "target", fingerprint: "fp"}
	require.Equal(t, now.Add(5*time.Minute), scheduler.nextSuccessAt(now))
	for attempt := 1; attempt <= 20; attempt++ {
		delay := scheduler.failureDelay(key, attempt)
		require.GreaterOrEqual(t, delay, 24*time.Second)
		require.LessOrEqual(t, delay, 5*time.Minute)
		if attempt > 6 {
			require.GreaterOrEqual(t, delay, 4*time.Minute)
		}
	}
}

func TestSchedulerSweepIntervalBalancesFailureRetryAndSuccessTTL(t *testing.T) {
	defaultScheduler := NewScheduler(nil, nil, SchedulerOptions{})
	require.Equal(t, 30*time.Second, defaultScheduler.currentSweepInterval())

	slowerRetry := NewScheduler(nil, nil, SchedulerOptions{FailureRetryMin: 45 * time.Second})
	require.Equal(t, 45*time.Second, slowerRetry.currentSweepInterval())

	shorterSuccessTTL := NewScheduler(nil, nil, SchedulerOptions{
		SuccessTTL: 30 * time.Second, FailureRetryMin: 5 * time.Minute, FailureRetryMax: 10 * time.Minute,
	})
	require.Equal(t, 30*time.Second, shorterSuccessTTL.currentSweepInterval())
}

func TestSchedulerTimingsHotUpdateChangesTTLRetryAndSweepAtomically(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	scheduler := NewScheduler(nil, nil, SchedulerOptions{})
	scheduler.SetTimings(45*time.Second, 10*time.Second, 40*time.Second)

	require.Equal(t, ProbeTimings{
		SuccessTTL: 45 * time.Second, FailureRetryMin: 10 * time.Second, FailureRetryMax: 40 * time.Second,
	}, scheduler.currentTimings())
	require.Equal(t, now.Add(45*time.Second), scheduler.nextSuccessAt(now))
	require.Equal(t, 30*time.Second, scheduler.currentSweepInterval())
	for attempt := 1; attempt <= 10; attempt++ {
		delay := scheduler.failureDelay(probeJobKey{sourceID: "source", targetAgentID: "target"}, attempt)
		require.GreaterOrEqual(t, delay, 8*time.Second)
		require.LessOrEqual(t, delay, 40*time.Second)
	}

	// Repeated UI saves coalesce into one timer wake instead of blocking the caller.
	for range 10 {
		scheduler.SetTimings(60*time.Second, 45*time.Second, 90*time.Second)
	}
	require.Len(t, scheduler.timingsChanged, 1)
	require.Equal(t, 45*time.Second, scheduler.currentSweepInterval())
}

func TestSchedulerTimingsRejectInvalidBoundsAsOneTypedSnapshot(t *testing.T) {
	scheduler := NewScheduler(nil, nil, SchedulerOptions{})
	scheduler.SetTimings(time.Second, time.Second, time.Second)
	require.Equal(t, ProbeTimings{
		SuccessTTL:      defaultProbeSuccessTTL,
		FailureRetryMin: defaultProbeFailureRetryMin,
		FailureRetryMax: defaultProbeFailureRetryMax,
	}, scheduler.currentTimings())

	scheduler.SetTimings(time.Hour, 5*time.Minute, 4*time.Minute)
	require.Equal(t, time.Hour, scheduler.currentTimings().SuccessTTL)
	require.Equal(t, 5*time.Minute, scheduler.currentTimings().FailureRetryMin)
	require.Equal(t, defaultProbeFailureRetryMax, scheduler.currentTimings().FailureRetryMax)
}

func TestSchedulerCancelledCallPreservesPreviousDirectSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	service := NewService("master", Sources{}, Options{Now: func() time.Time { return now }})
	target := probeTargetForScheduler("target")
	fingerprint := CanonicalProbeFingerprint("source", 1, target)
	previous := protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: fingerprint, Network: "unreachable",
		Identity: "unknown", CheckedAt: now.Add(-time.Minute).Unix(), LatencyMS: 9, ReasonCode: "direct_connect",
	}
	service.MarkDirectProbeChecking("source", 1, target, fingerprint, 1)
	service.ApplyDirectProbeResult("source", 1, target, previous, 1)
	scheduler := NewScheduler(failingProbeCaller{err: context.Canceled}, service, SchedulerOptions{Now: func() time.Time { return now }})
	job := probeJob{
		key:    probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: fingerprint},
		target: target, sourceGeneration: 1, probeGeneration: 2, manualIDs: map[string]struct{}{},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scheduler.runJob(ctx, job)

	snapshot := service.directSnapshot("source").Targets[target.AgentID]
	require.False(t, snapshot.Checking)
	require.Equal(t, previous.Network, snapshot.Network)
	require.Equal(t, previous.Identity, snapshot.Identity)
	require.Equal(t, previous.CheckedAt, snapshot.CheckedAt)
	require.Equal(t, previous.LatencyMS, snapshot.LatencyMS)
	require.Equal(t, previous.ReasonCode, snapshot.LastError.Code)
}

func TestSchedulerFirstCancelledCallLeavesUnknownSnapshot(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := probeTargetForScheduler("target")
	fingerprint := CanonicalProbeFingerprint("source", 1, target)
	scheduler := NewScheduler(failingProbeCaller{err: errors.New("control disconnected")}, service, SchedulerOptions{})
	scheduler.runJob(context.Background(), probeJob{
		key:    probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: fingerprint},
		target: target, sourceGeneration: 1, probeGeneration: 1, manualIDs: map[string]struct{}{},
	})

	snapshot := service.directSnapshot("source").Targets[target.AgentID]
	require.False(t, snapshot.Checking)
	require.Equal(t, "unknown", snapshot.Network)
	require.Equal(t, "unknown", snapshot.Identity)
	require.Zero(t, snapshot.CheckedAt)
	require.Nil(t, snapshot.LastError)
}

func TestSchedulerTypedCancelledResultPreservesPreviousDirectSnapshot(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	service := NewService("master", Sources{}, Options{Now: func() time.Time { return now }})
	target := probeTargetForScheduler("target")
	fingerprint := CanonicalProbeFingerprint("source", 1, target)
	previous := protocol.DirectProbeResult{
		TargetAgentID: target.AgentID, AddressFingerprint: fingerprint, Network: "reachable",
		Identity: "verified", Eligible: true, CheckedAt: now.Add(-time.Minute).Unix(), LatencyMS: 7,
	}
	service.MarkDirectProbeChecking("source", 1, target, fingerprint, 1)
	service.ApplyDirectProbeResult("source", 1, target, previous, 1)
	scheduler := NewScheduler(typedCancelledProbeCaller{}, service, SchedulerOptions{Now: func() time.Time { return now }})
	scheduler.runJob(context.Background(), probeJob{
		key:    probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: fingerprint},
		target: target, sourceGeneration: 1, probeGeneration: 2, manualIDs: map[string]struct{}{},
	})

	snapshot := service.directSnapshot("source").Targets[target.AgentID]
	require.False(t, snapshot.Checking)
	require.Equal(t, previous.Network, snapshot.Network)
	require.Equal(t, previous.Identity, snapshot.Identity)
	require.Equal(t, previous.Eligible, snapshot.Eligible)
	require.Equal(t, previous.CheckedAt, snapshot.CheckedAt)
	require.Equal(t, previous.LatencyMS, snapshot.LatencyMS)
}

func TestSchedulerFirstTypedCancelledResultLeavesUnknownSnapshot(t *testing.T) {
	service := NewService("master", Sources{}, Options{})
	target := probeTargetForScheduler("target")
	fingerprint := CanonicalProbeFingerprint("source", 1, target)
	scheduler := NewScheduler(typedCancelledProbeCaller{}, service, SchedulerOptions{})
	scheduler.runJob(context.Background(), probeJob{
		key:    probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: fingerprint},
		target: target, sourceGeneration: 1, probeGeneration: 1, manualIDs: map[string]struct{}{},
	})

	snapshot := service.directSnapshot("source").Targets[target.AgentID]
	require.False(t, snapshot.Checking)
	require.Equal(t, "unknown", snapshot.Network)
	require.Equal(t, "unknown", snapshot.Identity)
	require.Zero(t, snapshot.CheckedAt)
	require.Nil(t, snapshot.LastError)
}

func TestSchedulerProgressBoundRetentionAndCancellationJoin(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	release := make(chan struct{})
	caller := &schedulerCaller{block: release, err: errors.New("unused")}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		GlobalConcurrency: 2, PerSourceConcurrency: 2, ManualRetention: 15 * time.Minute,
		MaxProgressEntries: 2, Now: func() time.Time { return now },
		ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("a"), probeTargetForScheduler("b")}},
	})
	ack, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	runScheduler(t, scheduler)
	require.Eventually(t, func() bool { return scheduler.ResourceCount() == 2 }, time.Second, 10*time.Millisecond)
	closeScheduler(t, scheduler)
	require.Zero(t, scheduler.ResourceCount())
	select {
	case <-scheduler.Done():
	default:
		t.Fatal("scheduler Done remained open after Close")
	}
	progress, ok := scheduler.Progress(ack.ProbeID)
	require.True(t, ok)
	require.Equal(t, "cancelled", progress.State)

	now = now.Add(15*time.Minute + time.Second)
	scheduler.cleanupProgress()
	_, ok = scheduler.Progress(ack.ProbeID)
	require.False(t, ok)
}

func TestSchedulerFindProgressForSourceRejectsCrossAgentProbeIDs(t *testing.T) {
	service := NewService("master", Sources{Control: schedulerControlSource{"source-a": 1, "source-b": 2}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: schedulerFinder{}})
	first, err := scheduler.EnqueueManual(t.Context(), "source-a", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	second, err := scheduler.EnqueueManual(t.Context(), "source-b", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)

	firstProgress, ok := scheduler.FindProgressForSource("source-a", first.ProbeID)
	require.True(t, ok)
	require.Equal(t, first.ProbeID, firstProgress.ProbeID)
	_, ok = scheduler.FindProgressForSource("source-a", second.ProbeID)
	require.False(t, ok)
	secondProgress, ok := scheduler.FindProgressForSource("source-b", second.ProbeID)
	require.True(t, ok)
	require.Equal(t, second.ProbeID, secondProgress.ProbeID)
}

func TestSchedulerProgressOwnerExpiresWithProgressEntry(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	service := NewService("master", Sources{Control: schedulerControlSource{"source-a": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{
		ManualRetention: time.Minute, Now: func() time.Time { return now }, ProbeTargetFinder: schedulerFinder{},
	})
	ack, err := scheduler.EnqueueManual(t.Context(), "source-a", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	_, ok := scheduler.FindProgressForSource("source-a", ack.ProbeID)
	require.True(t, ok)

	now = now.Add(time.Minute + time.Second)
	scheduler.cleanupProgress()
	_, ok = scheduler.FindProgressForSource("source-a", ack.ProbeID)
	require.False(t, ok)
	_, ok = scheduler.Progress(ack.ProbeID)
	require.False(t, ok)
}

func TestSchedulerProgressOwnerConcurrentReadsRemainIsolated(t *testing.T) {
	service := NewService("master", Sources{Control: schedulerControlSource{"source-a": 1, "source-b": 2}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: schedulerFinder{}})
	first, err := scheduler.EnqueueManual(t.Context(), "source-a", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	second, err := scheduler.EnqueueManual(t.Context(), "source-b", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)

	var failures atomic.Int32
	var readers conc.WaitGroup
	for range 32 {
		readers.Go(func() {
			for range 100 {
				if progress, ok := scheduler.FindProgressForSource("source-a", first.ProbeID); !ok || progress.ProbeID != first.ProbeID {
					failures.Add(1)
				}
				if _, ok := scheduler.FindProgressForSource("source-a", second.ProbeID); ok {
					failures.Add(1)
				}
			}
		})
	}
	readers.Wait()
	require.Zero(t, failures.Load())
}

func TestSchedulerCompletionRacingCloseKeepsManualProgressCancelled(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	scheduler := NewScheduler(&schedulerCaller{}, NewService("master", Sources{}, Options{}), SchedulerOptions{Now: func() time.Time { return now }})
	probeID := "probe-racing-close"
	scheduler.progress[probeID] = manualProbeProgressEntry{
		sourceID: "source",
		progress: protocol.ManualProbeProgress{
			ProbeID: probeID, State: "running", TargetTotal: 1, Remaining: 1, StartedAt: now.Add(-time.Second).Unix(),
		},
	}
	target := probeTargetForScheduler("target")
	job := probeJob{
		key:    probeJobKey{sourceID: "source", targetAgentID: target.AgentID, fingerprint: "fp"},
		target: target, manualIDs: map[string]struct{}{probeID: {}},
	}
	scheduler.mu.Lock()
	scheduler.closing = true
	scheduler.mu.Unlock()
	scheduler.completeJob(job, protocol.DirectProbeResult{}, context.Canceled)

	progress, ok := scheduler.Progress(probeID)
	require.True(t, ok)
	require.Equal(t, "cancelled", progress.State)
	require.Equal(t, 1, progress.Remaining)
	require.Equal(t, now.Unix(), progress.CompletedAt)
}

func TestSchedulerCancelBeforeRunIsTerminal(t *testing.T) {
	finder := &blockingSchedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})

	scheduler.Cancel()
	requireSchedulerDone(t, scheduler.Done())
	require.Error(t, scheduler.Run(context.Background()))
	_, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.Error(t, err)
	require.Zero(t, finder.callCount())
	require.Empty(t, scheduler.progress)
	require.Empty(t, scheduler.jobs)
}

func TestSchedulerCancelOrCloseBeforeRunTerminalizesQueuedProgress(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	tests := []struct {
		name string
		stop func(*testing.T, *Scheduler)
	}{
		{name: "cancel", stop: func(_ *testing.T, scheduler *Scheduler) { scheduler.Cancel() }},
		{name: "close", stop: func(t *testing.T, scheduler *Scheduler) { closeScheduler(t, scheduler) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
			scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{
				Now: func() time.Time { return now }, ManualRetention: 15 * time.Minute,
				ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}},
			})
			first, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
			require.NoError(t, err)
			second, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
			require.NoError(t, err)
			require.Equal(t, "queued", first.State)
			require.Equal(t, "queued", second.State)

			test.stop(t, scheduler)
			requireSchedulerDone(t, scheduler.Done())
			for _, probeID := range []string{first.ProbeID, second.ProbeID} {
				progress, ok := scheduler.Progress(probeID)
				require.True(t, ok)
				require.Equal(t, "cancelled", progress.State)
				require.Equal(t, now.Unix(), progress.CompletedAt)
				require.Equal(t, now.Add(15*time.Minute), scheduler.progressExpiry[probeID])
			}
			require.Empty(t, scheduler.jobs)
		})
	}
}

func TestSchedulerCancelWhileRunningRejectsSecondRunAndEnqueue(t *testing.T) {
	finder := &blockingSchedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})
	runResult := make(chan error, 1)
	go func() { runResult <- scheduler.Run(context.Background()) }()
	require.Eventually(t, func() bool {
		scheduler.mu.Lock()
		running := scheduler.running
		scheduler.mu.Unlock()
		return running
	}, time.Second, time.Millisecond)

	scheduler.Cancel()
	requireSchedulerDone(t, scheduler.Done())
	require.Error(t, <-runResult)
	require.Error(t, scheduler.Run(context.Background()))
	_, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.Error(t, err)
}

func TestSchedulerParentCancellationIsTerminal(t *testing.T) {
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- scheduler.Run(ctx) }()
	require.Eventually(t, func() bool {
		scheduler.mu.Lock()
		running := scheduler.running
		scheduler.mu.Unlock()
		return running
	}, time.Second, time.Millisecond)
	cancel()
	requireSchedulerDone(t, scheduler.Done())
	require.ErrorIs(t, <-runResult, context.Canceled)
	scheduler.mu.Lock()
	terminal := scheduler.closing
	scheduler.mu.Unlock()
	require.True(t, terminal, "parent cancellation must make the owner terminal")
	require.Error(t, scheduler.Run(context.Background()))
	_, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.Error(t, err)
}

func TestSchedulerCloseRacingFinderRejectsEnqueueWithoutPendingProgress(t *testing.T) {
	finder := &blockingSchedulerFinder{
		entered: make(chan struct{}), release: make(chan struct{}),
		targets: []ProbeTarget{probeTargetForScheduler("target")},
	}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})
	enqueueResult := make(chan error, 1)
	go func() {
		_, err := scheduler.EnqueueManual(context.Background(), "source", protocol.ProbeScope{Kind: "all_enabled"})
		enqueueResult <- err
	}()
	<-finder.entered

	closeResult := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		closeResult <- scheduler.Close(ctx)
	}()
	require.NoError(t, <-closeResult, "Close must not wait for finder while holding scheduler state")
	close(finder.release)
	require.Error(t, <-enqueueResult)
	require.Empty(t, scheduler.progress)
	require.Empty(t, scheduler.jobs)
}

type schedulerEnqueueResult struct {
	ack protocol.ProbeAck
	err error
}

func TestSchedulerCanceledEnqueueAfterFinderDoesNotConsumeStateOrCapacity(t *testing.T) {
	finder := &blockingSchedulerFinder{
		entered: make(chan struct{}), release: make(chan struct{}),
		targets: []ProbeTarget{probeTargetForScheduler("target")},
	}
	service := NewService("master", Sources{Control: schedulerControlSource{"source": 1}}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{MaxProgressEntries: 1, ProbeTargetFinder: finder})
	requestCtx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("request canceled during target discovery")
	workers := enqueueProbeForResult(scheduler, requestCtx)
	<-finder.entered
	cancel(cause)
	close(finder.release)
	requireCanceledEnqueueLeavesCapacity(t, scheduler, workers, cause)
}

type blockingPostFinderControlSource struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *blockingPostFinderControlSource) GetControlSession(string) (ControlSessionFact, bool) {
	if s.calls.Add(1) == 3 {
		close(s.entered)
		<-s.release
	}
	return ControlSessionFact{Generation: 1}, true
}

func TestSchedulerCanceledBeforeStateLockDoesNotConsumeStateOrCapacity(t *testing.T) {
	control := &blockingPostFinderControlSource{entered: make(chan struct{}), release: make(chan struct{})}
	service := NewService("master", Sources{Control: control}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{
		MaxProgressEntries: 1, ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}},
	})
	requestCtx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("request canceled before scheduler state lock")
	workers := enqueueProbeForResult(scheduler, requestCtx)
	<-control.entered
	cancel(cause)
	close(control.release)
	requireCanceledEnqueueLeavesCapacity(t, scheduler, workers, cause)
}

func enqueueProbeForResult(scheduler *Scheduler, ctx context.Context) *pool.ResultPool[schedulerEnqueueResult] {
	workers := pool.NewWithResults[schedulerEnqueueResult]().WithMaxGoroutines(1)
	workers.Go(func() schedulerEnqueueResult {
		ack, err := scheduler.EnqueueManual(ctx, "source", protocol.ProbeScope{Kind: "all_enabled"})
		return schedulerEnqueueResult{ack: ack, err: err}
	})
	return workers
}

func requireCanceledEnqueueLeavesCapacity(t *testing.T, scheduler *Scheduler, workers *pool.ResultPool[schedulerEnqueueResult], cause error) {
	t.Helper()
	results := workers.Wait()
	require.Len(t, results, 1)
	require.ErrorIs(t, results[0].err, cause)
	require.Empty(t, results[0].ack.ProbeID)
	require.Empty(t, scheduler.progress)
	require.Empty(t, scheduler.progressExpiry)
	require.Empty(t, scheduler.jobs)
	ack, err := scheduler.EnqueueManual(t.Context(), "source", protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err, "canceled enqueue must not consume the only progress slot")
	require.NotEmpty(t, ack.ProbeID)
	require.Len(t, scheduler.progress, 1)
}

func TestSchedulerManualSessionRejectsReplacementDuringFinder(t *testing.T) {
	control := &lockedSchedulerControlSource{generations: map[string]uint64{"source": 7}}
	finder := &blockingSchedulerFinder{
		entered: make(chan struct{}), release: make(chan struct{}),
		targets: []ProbeTarget{probeTargetForScheduler("target")},
	}
	service := NewService("master", Sources{Control: control}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})
	result := make(chan error, 1)
	go func() {
		_, err := scheduler.EnqueueManualSession(t.Context(), "source", 7, protocol.ProbeScope{Kind: "all_enabled"})
		result <- err
	}()
	<-finder.entered
	control.set("source", 8)
	close(finder.release)

	require.ErrorIs(t, <-result, ErrConnectionGenerationChanged)
	require.Empty(t, scheduler.progress)
	require.Empty(t, scheduler.jobs)
}

func TestSchedulerManualSessionRejectsReplacementBeforeFinder(t *testing.T) {
	control := &lockedSchedulerControlSource{generations: map[string]uint64{"source": 8}}
	finder := &recordingSchedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}}
	service := NewService("master", Sources{Control: control}, Options{})
	scheduler := NewScheduler(&schedulerCaller{}, service, SchedulerOptions{ProbeTargetFinder: finder})

	_, err := scheduler.EnqueueManualSession(t.Context(), "source", 7, protocol.ProbeScope{Kind: "all_enabled"})
	require.ErrorIs(t, err, ErrConnectionGenerationChanged)
	require.Empty(t, finder.calls)
	require.Empty(t, scheduler.progress)
	require.Empty(t, scheduler.jobs)
}

func TestSchedulerManualSessionKeepsExpectedGenerationAfterEnqueue(t *testing.T) {
	control := &lockedSchedulerControlSource{generations: map[string]uint64{"source": 7}}
	caller := &generationCheckingSchedulerCaller{control: control}
	service := NewService("master", Sources{Control: control}, Options{})
	scheduler := NewScheduler(caller, service, SchedulerOptions{
		ProbeTargetFinder: schedulerFinder{targets: []ProbeTarget{probeTargetForScheduler("target")}},
	})
	ack, err := scheduler.EnqueueManualSession(t.Context(), "source", 7, protocol.ProbeScope{Kind: "all_enabled"})
	require.NoError(t, err)
	control.set("source", 8)
	runScheduler(t, scheduler)
	require.Eventually(t, func() bool { return len(caller.generations()) == 1 }, time.Second, time.Millisecond)
	require.Equal(t, []uint64{7}, caller.generations())
	waitProgressState(t, scheduler, ack.ProbeID, "completed")
}

func probeTargetForScheduler(id string) ProbeTarget {
	return ProbeTarget{
		AgentID: id, Tags: []string{"gpu"}, ControlGeneration: 10,
		Addresses:    []protocol.Address{{URL: "http://" + id}},
		Capabilities: []string{protocol.AgentCapabilityDirectIngressV1},
	}
}

func enqueueProbeForTest(scheduler *Scheduler, priority probePriority, sourceID string, target ProbeTarget, probeID string) {
	scheduler.mu.Lock()
	scheduler.enqueueLocked(priority, sourceID, 1, target, CanonicalProbeFingerprint(sourceID, 1, target), probeID, 1)
	scheduler.mu.Unlock()
}

func completeProbeForTest(scheduler *Scheduler, job probeJob) {
	scheduler.completeJob(job, protocol.DirectProbeResult{Eligible: true}, nil)
}

func runScheduler(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- scheduler.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("scheduler Run did not stop")
		}
	})
}

func closeScheduler(t *testing.T, scheduler *Scheduler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, scheduler.Close(ctx))
}

func waitProgressState(t *testing.T, scheduler *Scheduler, probeID, state string) {
	t.Helper()
	require.Eventually(t, func() bool {
		progress, ok := scheduler.Progress(probeID)
		return ok && progress.State == state
	}, time.Second, 10*time.Millisecond)
}

func requireSchedulerDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduler Done did not close")
	}
}

func cloneIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
