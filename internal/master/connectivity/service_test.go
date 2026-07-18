package connectivity

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type controlSourceStub struct {
	facts map[string]ControlSessionFact
}

func (s *controlSourceStub) GetControlSession(agentID string) (ControlSessionFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

type relaySourceStub struct {
	facts map[string]RelayRuntimeFact
}

func (s *relaySourceStub) GetRelayRuntime(agentID string) (RelayRuntimeFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

type healthSourceStub struct {
	reasons map[string][]string
}

func (s *healthSourceStub) ReasonCodes(agentID string) []string {
	return s.reasons[agentID]
}

type countingControlSource struct{ calls int }

func (s *countingControlSource) GetControlSession(string) (ControlSessionFact, bool) {
	s.calls++
	return ControlSessionFact{}, false
}

type countingRelaySource struct{ calls int }

func (s *countingRelaySource) GetRelayRuntime(string) (RelayRuntimeFact, bool) {
	s.calls++
	return RelayRuntimeFact{}, false
}

func TestConnectionSnapshotControlStateUsesCurrentSessionNotLastSeen(t *testing.T) {
	now := time.Unix(1_000, 0)
	control := &controlSourceStub{facts: map[string]ControlSessionFact{}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return now },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled, LastSeen: 999}

	disconnected := svc.Build(agent)
	require.Equal(t, "disconnected", disconnected.Control.State)
	require.Equal(t, "unknown", disconnected.Control.Health)
	require.Equal(t, int64(999), disconnected.Control.LastSeen)
	require.Equal(t, []string{ControlReasonHeartbeatMissing}, disconnected.Control.ReasonCodes)
	require.Zero(t, disconnected.Control.SessionGeneration)

	control.facts[agent.AgentID] = ControlSessionFact{
		Generation:        7,
		ConnectedAt:       700,
		HeartbeatAt:       800,
		RuntimeReportedAt: 801,
	}
	connected := svc.Build(agent)
	require.Equal(t, "connected", connected.Control.State, "stale heartbeat must not disconnect a live session")
	require.Equal(t, "degraded", connected.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatStale}, connected.Control.ReasonCodes)
	require.Equal(t, uint64(7), connected.Control.SessionGeneration)
	require.Equal(t, int64(700), connected.Control.ConnectedAt)
	require.Equal(t, int64(800), connected.Control.HeartbeatAt)
	require.Equal(t, int64(801), connected.Control.RuntimeReportedAt)
}

func TestConnectionSnapshotHeartbeatMissingUsesConnectedAtAge(t *testing.T) {
	now := time.Unix(1_000, 0)
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"new":  {Generation: 1, ConnectedAt: 911},
		"old":  {Generation: 2, ConnectedAt: 910},
		"zero": {Generation: 3},
	}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return now },
	})

	freshlyConnected := svc.Build(models.Agent{AgentID: "new", Status: consts.StatusEnabled})
	require.Equal(t, "connected", freshlyConnected.Control.State)
	require.Equal(t, "unknown", freshlyConnected.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatMissing}, freshlyConnected.Control.ReasonCodes)

	boundary := svc.Build(models.Agent{AgentID: "old", Status: consts.StatusEnabled})
	require.Equal(t, "connected", boundary.Control.State)
	require.Equal(t, "degraded", boundary.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatMissing}, boundary.Control.ReasonCodes)

	unknownAge := svc.Build(models.Agent{AgentID: "zero", Status: consts.StatusEnabled})
	require.Equal(t, "unknown", unknownAge.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatMissing}, unknownAge.Control.ReasonCodes)
}

func TestConnectionSnapshotHealthReasonsAreStableDeduplicatedAndIsolated(t *testing.T) {
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
	}}
	health := &healthSourceStub{reasons: map[string][]string{
		"agent-a": {ControlReasonHeartbeatStale, "clock_skew", "clock_skew"},
	}}
	svc := NewService("instance-a", Sources{Control: control, Health: health}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return time.Unix(1_000, 0) },
	})

	first := svc.Build(models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled})
	require.Equal(t, []string{ControlReasonHeartbeatStale, "clock_skew"}, first.Control.ReasonCodes)
	first.Control.ReasonCodes[0] = "mutated"
	second := svc.Build(models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled})
	require.Equal(t, []string{ControlReasonHeartbeatStale, "clock_skew"}, second.Control.ReasonCodes)
	health.reasons["agent-a"][0] = "source-mutated"
	require.Equal(t, []string{ControlReasonHeartbeatStale, "clock_skew"}, second.Control.ReasonCodes)
}

func TestHealthHysteresisRequiresDistinctFreshHeartbeatSamples(t *testing.T) {
	now := time.Unix(1_000, 0)
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
	}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return now },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	stale := svc.Build(agent)
	require.Equal(t, "degraded", stale.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatStale}, stale.Control.ReasonCodes)

	control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 950}
	recovering := svc.Build(agent)
	require.Equal(t, "degraded", recovering.Control.Health)
	require.Equal(t, []string{ControlReasonHeartbeatRecovering}, recovering.Control.ReasonCodes)

	repeated := svc.Build(agent)
	require.Equal(t, "degraded", repeated.Control.Health, "rebuilding one sample must not advance recovery")
	require.Equal(t, []string{ControlReasonHeartbeatRecovering}, repeated.Control.ReasonCodes)

	control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 960}
	recovered := svc.Build(agent)
	require.Equal(t, "healthy", recovered.Control.Health)
	require.Empty(t, recovered.Control.ReasonCodes)
}

func TestHealthHysteresisResetsWhenSessionChangesOrDisappears(t *testing.T) {
	now := time.Unix(1_000, 0)
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
	}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        3,
		Now:                    func() time.Time { return now },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	require.Equal(t, "degraded", svc.Build(agent).Control.Health)
	control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 950}
	require.Equal(t, "degraded", svc.Build(agent).Control.Health)

	control.facts[agent.AgentID] = ControlSessionFact{Generation: 2, ConnectedAt: 940, HeartbeatAt: 960}
	require.Equal(t, "healthy", svc.Build(agent).Control.Health, "a new session's first fresh heartbeat is healthy")

	control.facts[agent.AgentID] = ControlSessionFact{Generation: 2, ConnectedAt: 700, HeartbeatAt: 800}
	require.Equal(t, "degraded", svc.Build(agent).Control.Health)
	delete(control.facts, agent.AgentID)
	require.Equal(t, "disconnected", svc.Build(agent).Control.State)
	control.facts[agent.AgentID] = ControlSessionFact{Generation: 3, ConnectedAt: 940, HeartbeatAt: 960}
	require.Equal(t, "healthy", svc.Build(agent).Control.Health)
}

func TestHealthHysteresisUsesStrictlyIncreasingHeartbeatHighWater(t *testing.T) {
	t.Run("clock rollback repeated and out of order samples", func(t *testing.T) {
		now := time.Unix(1_000, 0)
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return now },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Equal(t, int64(800), svc.health[agent.AgentID].heartbeatHighWater)
		require.Zero(t, svc.health[agent.AgentID].recoveryCount)

		now = time.Unix(850, 0)
		for _, heartbeatAt := range []int64{800, 790, 800, 799} {
			control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: heartbeatAt}
			require.Equal(t, "degraded", svc.Build(agent).Control.Health, heartbeatAt)
			require.Zero(t, svc.health[agent.AgentID].recoveryCount, heartbeatAt)
			require.Equal(t, int64(800), svc.health[agent.AgentID].heartbeatHighWater, heartbeatAt)
		}

		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 801}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Equal(t, 1, svc.health[agent.AgentID].recoveryCount)
		require.Equal(t, int64(801), svc.health[agent.AgentID].heartbeatHighWater)
		require.Equal(t, "degraded", svc.Build(agent).Control.Health, "rebuilding heartbeat 801 must not count twice")
		require.Equal(t, 1, svc.health[agent.AgentID].recoveryCount)

		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 802}
		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
	})

	t.Run("future samples remain monotonic", func(t *testing.T) {
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 900},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return time.Unix(1_000, 0) },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 1_100}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Equal(t, 1, svc.health[agent.AgentID].recoveryCount)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 1_099}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Equal(t, 1, svc.health[agent.AgentID].recoveryCount)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 1_101}
		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
	})

	t.Run("becoming stale again resets recovery without lowering high water", func(t *testing.T) {
		now := time.Unix(1_000, 0)
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return now },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		now = time.Unix(850, 0)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 801}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Equal(t, 1, svc.health[agent.AgentID].recoveryCount)

		now = time.Unix(1_000, 0)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 850}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Zero(t, svc.health[agent.AgentID].recoveryCount)
		require.Equal(t, int64(850), svc.health[agent.AgentID].heartbeatHighWater)

		now = time.Unix(900, 0)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 851}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 852}
		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
	})
}

func TestHealthHysteresisStateLifecycle(t *testing.T) {
	t.Run("healthy recovery disconnect and generation change clear state", func(t *testing.T) {
		now := time.Unix(1_000, 0)
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 1, ConnectedAt: 940, HeartbeatAt: 950},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return now },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
		require.Empty(t, svc.health, "healthy sessions must not retain hysteresis state")

		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 800}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		require.Len(t, svc.health, 1)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 950}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 960}
		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
		require.Empty(t, svc.health, "successful recovery must delete hysteresis state")

		control.facts[agent.AgentID] = ControlSessionFact{Generation: 2, ConnectedAt: 700, HeartbeatAt: 800}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 3, ConnectedAt: 940, HeartbeatAt: 950}
		require.Equal(t, "healthy", svc.Build(agent).Control.Health)
		require.Empty(t, svc.health, "generation reset to healthy must delete old state")

		control.facts[agent.AgentID] = ControlSessionFact{Generation: 3, ConnectedAt: 700, HeartbeatAt: 800}
		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		delete(control.facts, agent.AgentID)
		require.Equal(t, "disconnected", svc.Build(agent).Control.State)
		require.Empty(t, svc.health, "disconnect must delete hysteresis state")
	})

	t.Run("forget preserves newer and clears matching generation", func(t *testing.T) {
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 4, ConnectedAt: 700, HeartbeatAt: 800},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return time.Unix(1_000, 0) },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		svc.Forget(agent.AgentID, 3)
		require.Len(t, svc.health, 1, "stale session removal must preserve current hysteresis state")
		svc.Forget(agent.AgentID, 4)
		require.Empty(t, svc.health, "matching session removal must delete hysteresis state")
	})

	t.Run("forget current unobserved generation clears older state", func(t *testing.T) {
		control := &controlSourceStub{facts: map[string]ControlSessionFact{
			"agent-a": {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
		}}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return time.Unix(1_000, 0) },
		})
		agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

		require.Equal(t, "degraded", svc.Build(agent).Control.Health)
		control.facts[agent.AgentID] = ControlSessionFact{Generation: 2, ConnectedAt: 900}
		svc.Forget(agent.AgentID, 2)
		require.Empty(t, svc.health, "current session removal must clear an older unobserved state")
	})

	t.Run("active degraded sessions retain recovery state until lifecycle cleanup", func(t *testing.T) {
		const activeSessions = 4097
		control := &controlSourceStub{facts: make(map[string]ControlSessionFact, activeSessions)}
		svc := NewService("instance-a", Sources{Control: control}, Options{
			HeartbeatDegradedAfter: 90 * time.Second,
			RecoverySamples:        2,
			Now:                    func() time.Time { return time.Unix(1_000, 0) },
		})
		for i := 0; i < activeSessions; i++ {
			agentID := fmt.Sprintf("agent-%d", i)
			control.facts[agentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 800}
			svc.Build(models.Agent{AgentID: agentID, Status: consts.StatusEnabled})
		}
		if got := len(svc.health); got != activeSessions {
			t.Errorf("active hysteresis states = %d, want %d", got, activeSessions)
		}

		agentZero := models.Agent{AgentID: "agent-0", Status: consts.StatusEnabled}
		control.facts[agentZero.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 950}
		if got := svc.Build(agentZero).Control.Health; got != "degraded" {
			t.Errorf("first recovery sample health = %q, want degraded", got)
		}
		control.facts[agentZero.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 960}
		require.Equal(t, "healthy", svc.Build(agentZero).Control.Health)

		for i := 1; i < activeSessions; i++ {
			agentID := fmt.Sprintf("agent-%d", i)
			if i%2 == 0 {
				svc.Forget(agentID, 1)
				continue
			}
			delete(control.facts, agentID)
			require.Equal(t, "disconnected", svc.Build(models.Agent{AgentID: agentID}).Control.State)
		}
		require.Empty(t, svc.health, "matching removal and disconnect must clear all historical state")
	})
}

func TestConnectionSnapshotBuildManySharesBatchIdentityAndSequenceIsMonotonic(t *testing.T) {
	nowCalls := 0
	svc := NewService("instance-a", Sources{}, Options{
		Now: func() time.Time {
			nowCalls++
			return time.Unix(int64(100+nowCalls), 0)
		},
	})
	agents := []models.Agent{
		{AgentID: "agent-a", Status: consts.StatusEnabled},
		{AgentID: "agent-b", Status: consts.StatusEnabled},
	}

	first := svc.Build(agents[0])
	batch := svc.BuildMany(agents)
	last := svc.Build(agents[1])

	require.Equal(t, uint64(1), first.SnapshotSeq)
	require.Equal(t, uint64(2), batch.SnapshotSeq)
	require.Equal(t, uint64(3), last.SnapshotSeq)
	require.Equal(t, 3, nowCalls, "one Build/BuildMany operation reads its clock exactly once")
	require.Equal(t, "instance-a", batch.SnapshotEpoch)
	require.Equal(t, int64(102), batch.ObservedAt)
	require.Len(t, batch.Items, 2)
	for _, agent := range agents {
		item := batch.Items[agent.AgentID]
		require.Equal(t, batch.SnapshotEpoch, item.SnapshotEpoch)
		require.Equal(t, batch.SnapshotSeq, item.SnapshotSeq)
		require.Equal(t, batch.ObservedAt, item.ObservedAt)
	}
}

func TestConnectionSnapshotDefaultsAreCompleteAndCopyIsolated(t *testing.T) {
	relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{}}
	svc := NewService("instance-a", Sources{Relay: relay}, Options{
		Now: func() time.Time { return time.Unix(100, 0) },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	snapshot := svc.Build(agent)
	require.Equal(t, "v1", snapshot.Version)
	require.Equal(t, "instance-a", snapshot.SnapshotEpoch)
	require.Equal(t, "unsupported", snapshot.Relay.Support)
	require.Equal(t, "not_configured", snapshot.Relay.Config)
	require.Equal(t, "unavailable", snapshot.Relay.Availability)
	require.False(t, snapshot.Relay.AcceptingNewStreams)
	require.Equal(t, "converged", snapshot.Relay.Convergence)
	require.NotNil(t, snapshot.Relay.RecentErrors)
	require.Empty(t, snapshot.Relay.RecentErrors)
	require.Equal(t, "unknown", snapshot.Direct.Summary.State)
	require.Zero(t, snapshot.Direct.Summary.Total)
	require.Nil(t, snapshot.Direct.Targets)
	require.NotNil(t, snapshot.Control.ReasonCodes)
	require.Equal(t, []Operation{
		OperationFullSync,
		OperationProbe,
		OperationRelayReconnect,
		OperationRelayDrain,
		OperationRelayDisconnect,
		OperationDirectCircuitReset,
		OperationInterrupt,
	}, operationOrder(snapshot.AllowedOperations))

	relay.facts[agent.AgentID] = RelayRuntimeFact{
		Support:             "supported",
		Config:              "configured",
		Availability:        "available",
		AcceptingNewStreams: true,
		Convergence:         "converging",
		RecentErrors: []RecentError{{
			Code: "dial_failed", Stage: "dial", Message: "failed", OccurredAt: 90, Count: 2,
		}},
	}
	projected := svc.Build(agent)
	require.Equal(t, "ready", projected.Relay.Availability)
	require.Equal(t, "applying", projected.Relay.Convergence)
	require.Equal(t, "dial_failed", projected.Relay.RecentErrors[0].Code)
	projected.Relay.RecentErrors[0].Code = "mutated"
	again := svc.Build(agent)
	require.Equal(t, "dial_failed", again.Relay.RecentErrors[0].Code)
}

func TestConnectionSnapshotNormalizesRelayWireStates(t *testing.T) {
	tests := []struct {
		name          string
		availability  string
		accepting     bool
		convergence   string
		wantAvailable string
		wantConverged string
	}{
		{name: "available and accepting is ready", availability: "available", accepting: true, convergence: "converged", wantAvailable: "ready", wantConverged: "converged"},
		{name: "available and not accepting is draining", availability: "available", convergence: "converging", wantAvailable: "draining", wantConverged: "applying"},
		{name: "unavailable remains unavailable", availability: "unavailable", convergence: "converged", wantAvailable: "unavailable", wantConverged: "converged"},
		{name: "explicit degraded convergence remains degraded", availability: "draining", convergence: "degraded", wantAvailable: "unavailable", wantConverged: "degraded"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{"agent-a": {
				Support: "supported", Config: "configured", Availability: test.availability,
				AcceptingNewStreams: test.accepting, Convergence: test.convergence,
			}}}
			svc := NewService("instance-a", Sources{Relay: relay}, Options{})

			snapshot := svc.Build(models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled})

			require.Equal(t, test.wantAvailable, snapshot.Relay.Availability)
			require.Equal(t, test.wantConverged, snapshot.Relay.Convergence)
			encoded, err := json.Marshal(snapshot)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), `"availability":"available"`)
			require.NotContains(t, string(encoded), `"convergence":"converging"`)
		})
	}
}

func TestConnectionSnapshotInvalidOptionsUseDefaults(t *testing.T) {
	now := time.Unix(1_000, 0)
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"agent-a": {Generation: 1, ConnectedAt: 800, HeartbeatAt: 909},
	}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		HeartbeatDegradedAfter: -time.Second,
		RecoverySamples:        -1,
		Now:                    func() time.Time { return now },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	require.Equal(t, "degraded", svc.Build(agent).Control.Health, "invalid threshold must fall back to 90s")
	control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 800, HeartbeatAt: 950}
	require.Equal(t, "degraded", svc.Build(agent).Control.Health, "default recovery requires two samples")
	control.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 800, HeartbeatAt: 960}
	require.Equal(t, "healthy", svc.Build(agent).Control.Health)
}

func TestConnectionSnapshotOutOfRangeOptionsUseDefaults(t *testing.T) {
	now := time.Unix(1_000, 0)
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	shortThresholdControl := &controlSourceStub{facts: map[string]ControlSessionFact{
		agent.AgentID: {Generation: 1, ConnectedAt: 900, HeartbeatAt: 950},
	}}
	shortThreshold := NewService("instance-a", Sources{Control: shortThresholdControl}, Options{
		HeartbeatDegradedAfter: 5 * time.Second,
		RecoverySamples:        2,
		Now:                    func() time.Time { return now },
	})
	require.Equal(t, "healthy", shortThreshold.Build(agent).Control.Health, "threshold below the settings range must use 90s")

	largeRecoveryControl := &controlSourceStub{facts: map[string]ControlSessionFact{
		agent.AgentID: {Generation: 1, ConnectedAt: 700, HeartbeatAt: 800},
	}}
	largeRecovery := NewService("instance-a", Sources{Control: largeRecoveryControl}, Options{
		HeartbeatDegradedAfter: 90 * time.Second,
		RecoverySamples:        11,
		Now:                    func() time.Time { return now },
	})
	require.Equal(t, "degraded", largeRecovery.Build(agent).Control.Health)
	largeRecoveryControl.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 950}
	require.Equal(t, "degraded", largeRecovery.Build(agent).Control.Health)
	largeRecoveryControl.facts[agent.AgentID] = ControlSessionFact{Generation: 1, ConnectedAt: 700, HeartbeatAt: 960}
	require.Equal(t, "healthy", largeRecovery.Build(agent).Control.Health, "recovery count above the settings range must use two samples")
}

func TestConnectionSnapshotAuthorizationMatrixAndLiveLease(t *testing.T) {
	control := &controlSourceStub{facts: map[string]ControlSessionFact{}}
	relay := &relaySourceStub{facts: map[string]RelayRuntimeFact{}}
	svc := NewService("instance-a", Sources{Control: control, Relay: relay}, Options{
		Now: func() time.Time { return time.Unix(100, 0) },
	})
	enabled := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}
	disabled := models.Agent{AgentID: "agent-a", Status: consts.StatusDisabled}
	allOps := []Operation{
		OperationFullSync, OperationProbe, OperationRelayReconnect, OperationRelayDrain,
		OperationRelayDisconnect, OperationDirectCircuitReset, OperationInterrupt,
	}

	for _, op := range allOps {
		assertDenied(t, svc, disabled, op, DenialAgentDisabled)
	}
	for _, op := range []Operation{OperationFullSync, OperationProbe, OperationRelayReconnect, OperationDirectCircuitReset, OperationInterrupt} {
		assertDenied(t, svc, enabled, op, DenialControlDisconnected)
	}
	for _, op := range []Operation{OperationRelayDrain, OperationRelayDisconnect} {
		assertDenied(t, svc, enabled, op, DenialRelaySessionUnavailable)
	}

	control.facts[enabled.AgentID] = ControlSessionFact{Generation: 11, ConnectedAt: 90, HeartbeatAt: 95}
	for _, op := range []Operation{OperationFullSync, OperationProbe, OperationDirectCircuitReset, OperationInterrupt} {
		lease, err := svc.Authorize(enabled, op)
		require.NoError(t, err)
		require.Equal(t, "instance-a", lease.SnapshotEpoch)
		require.Equal(t, uint64(11), lease.ControlGeneration)
	}
	assertDenied(t, svc, enabled, OperationRelayReconnect, DenialRelayUnsupported)
	for _, op := range []Operation{OperationRelayDrain, OperationRelayDisconnect} {
		assertDenied(t, svc, enabled, op, DenialRelaySessionUnavailable)
	}

	relay.facts[enabled.AgentID] = RelayRuntimeFact{Support: "supported", Config: "disabled"}
	assertDenied(t, svc, enabled, OperationRelayReconnect, DenialRelayDisabled)
	relay.facts[enabled.AgentID] = RelayRuntimeFact{Support: "supported", Config: "not_configured"}
	assertDenied(t, svc, enabled, OperationRelayReconnect, DenialRelayNotConfigured)
	relay.facts[enabled.AgentID] = RelayRuntimeFact{Support: "supported", Config: "configured"}
	lease, err := svc.Authorize(enabled, OperationRelayReconnect)
	require.NoError(t, err)
	require.Equal(t, uint64(11), lease.ControlGeneration)

	relay.facts[enabled.AgentID] = RelayRuntimeFact{
		Support: "supported",
		Config:  "disabled",
		Active: RelayActiveSnapshot{
			ActiveGeneration:  999,
			SessionGeneration: 23,
		},
	}
	delete(control.facts, enabled.AgentID)
	for _, op := range []Operation{OperationRelayDrain, OperationRelayDisconnect} {
		lease, err := svc.Authorize(enabled, op)
		require.NoError(t, err)
		require.Zero(t, lease.ControlGeneration)
		require.Equal(t, uint64(23), lease.RelayGeneration, "lease must use relay session generation")
	}

	control.facts[enabled.AgentID] = ControlSessionFact{Generation: 31}
	_ = svc.Build(enabled)
	delete(control.facts, enabled.AgentID)
	assertDenied(t, svc, enabled, OperationFullSync, DenialControlDisconnected)
}

func TestConnectionSnapshotUnknownOperationIsValidatedBeforeState(t *testing.T) {
	control := &countingControlSource{}
	relay := &countingRelaySource{}
	svc := NewService("instance-a", Sources{Control: control, Relay: relay}, Options{
		Now: func() time.Time { return time.Unix(100, 0) },
	})
	unknown := Operation("unknown")

	_, disabledErr := svc.Authorize(models.Agent{AgentID: "agent-a", Status: consts.StatusDisabled}, unknown)
	_, disconnectedErr := svc.Authorize(models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}, unknown)
	require.EqualError(t, disabledErr, `unknown operation "unknown"`)
	require.EqualError(t, disconnectedErr, `unknown operation "unknown"`)
	var denied *OperationDeniedError
	require.False(t, errors.As(disabledErr, &denied))
	require.False(t, errors.As(disconnectedErr, &denied))
	require.Zero(t, control.calls, "unknown operation must be rejected before reading control state")
	require.Zero(t, relay.calls, "unknown operation must be rejected before reading relay state")
}

func TestConnectionSnapshotAllowedOperationsMatchAuthorization(t *testing.T) {
	control := &controlSourceStub{facts: map[string]ControlSessionFact{
		"agent-a": {Generation: 7, ConnectedAt: 90, HeartbeatAt: 95},
	}}
	svc := NewService("instance-a", Sources{Control: control}, Options{
		Now: func() time.Time { return time.Unix(100, 0) },
	})
	agent := models.Agent{AgentID: "agent-a", Status: consts.StatusEnabled}

	snapshot := svc.Build(agent)
	want := map[Operation]struct {
		allowed bool
		denial  string
	}{
		OperationFullSync:           {allowed: true},
		OperationProbe:              {allowed: true},
		OperationRelayReconnect:     {denial: DenialRelayUnsupported},
		OperationRelayDrain:         {denial: DenialRelaySessionUnavailable},
		OperationRelayDisconnect:    {denial: DenialRelaySessionUnavailable},
		OperationDirectCircuitReset: {allowed: true},
		OperationInterrupt:          {allowed: true},
	}
	for _, status := range snapshot.AllowedOperations {
		require.Equal(t, want[status.Operation].allowed, status.Allowed, status.Operation)
		require.Equal(t, want[status.Operation].denial, status.DenialCode, status.Operation)
	}
}

func TestConnectionSnapshotRuntimeTypeRetainsProtocolCacheStats(t *testing.T) {
	fact := AgentRuntimeFact{CacheStats: map[string]protocol.CacheEntityStats{
		"token": {Hits: 2, Extra: map[string]int64{"hot": 1}},
	}}
	require.Equal(t, int64(2), fact.CacheStats["token"].Hits)
	require.Equal(t, int64(1), fact.CacheStats["token"].Extra["hot"])
}

func TestConnectionSnapshotLockedJSONTags(t *testing.T) {
	typeOf := reflect.TypeOf(ConnectionSnapshot{})
	want := map[string]string{
		"Version": "version", "SnapshotEpoch": "snapshot_epoch", "SnapshotSeq": "snapshot_seq",
		"ObservedAt": "observed_at", "AgentID": "agent_id", "AdminStatus": "admin_status",
		"Control": "control", "Relay": "relay", "Direct": "direct", "AllowedOperations": "allowed_operations",
	}
	for fieldName, jsonName := range want {
		field, ok := typeOf.FieldByName(fieldName)
		require.True(t, ok, fieldName)
		require.Equal(t, jsonName, field.Tag.Get("json"), fieldName)
	}
}

func operationOrder(statuses []OperationStatus) []Operation {
	operations := make([]Operation, len(statuses))
	for i := range statuses {
		operations[i] = statuses[i].Operation
	}
	return operations
}

func assertDenied(t *testing.T, svc *Service, agent models.Agent, op Operation, code string) {
	t.Helper()
	lease, err := svc.Authorize(agent, op)
	require.Error(t, err)
	require.Equal(t, OperationLease{}, lease)
	var denied *OperationDeniedError
	require.True(t, errors.As(err, &denied), "error must expose a typed denial")
	require.Equal(t, code, denied.DenialCode)
}
