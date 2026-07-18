package connectivity

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestRouteEdgesReplaceGenerationRejectLateOldAndBoundDigest(t *testing.T) {
	now := time.Unix(30_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	edges := make([]protocol.RouteEdgeSnapshot, 1024)
	for i := range edges {
		edges[i] = protocol.RouteEdgeSnapshot{TargetAgentID: fmt.Sprintf("target-%04d", i), LastUsedAt: now.Unix()}
	}
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 101, Edges: edges}))
	require.Len(t, svc.RouteEdges("source-a"), 1024)

	require.Error(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 102, Edges: append(edges, protocol.RouteEdgeSnapshot{TargetAgentID: "forged"}),
	}))
	require.Len(t, svc.RouteEdges("source-a"), 1024)

	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 202, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "new", LastUsedAt: now.Unix()}}}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 101, Events: []protocol.RouteEvent{{TargetAgentID: "late", ObservedAt: now.Unix()}}}))
	require.Equal(t, "new", svc.RouteEdges("source-a")[0].TargetAgentID)
}

func TestRouteEdgesApplyEventsIdempotentlyAndExpireAtOneHour(t *testing.T) {
	now := time.Unix(40_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	event := protocol.RouteEvent{TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", Result: "success", DurationMS: 7, ObservedAt: now.Unix()}
	changed := event
	changed.Result = "error"
	changed.ReasonCode = "connection_reset"
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 303}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 303, Events: []protocol.RouteEvent{event, event, changed}}))
	edges := svc.RouteEdges("source-a")
	require.Len(t, edges, 1)
	require.Equal(t, uint64(1), edges[0].SuccessCount)
	require.Equal(t, uint64(7), edges[0].LatencyTotalMS)
	require.Equal(t, "connection_reset", edges[0].LastDirectResult, "a distinct state change in the same second is not a duplicate")

	now = now.Add(time.Hour)
	require.Empty(t, svc.RouteEdges("source-a"))
}

func TestRouteEdgeDigestAllowsFirstSameSecondStateEvent(t *testing.T) {
	now := time.Unix(45_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 313, Edges: []protocol.RouteEdgeSnapshot{{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", LastUsedAt: now.Unix(),
		LastDirectResult: "success", AddressFingerprint: "fp-old", SuccessCount: 7, LatencyTotalMS: 70,
	}}}))
	event := protocol.RouteEvent{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", Result: "error",
		ReasonCode: "connection_reset", AddressFingerprint: "fp-new", ObservedAt: now.Unix(),
	}
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 313, Events: []protocol.RouteEvent{event, event}}))
	edges := svc.RouteEdges("source-a")
	require.Len(t, edges, 1)
	require.Equal(t, "connection_reset", edges[0].LastDirectResult)
	require.Equal(t, "fp-new", edges[0].AddressFingerprint)
	require.Equal(t, uint64(7), edges[0].SuccessCount, "the duplicate error event must be idempotent")
}

func TestRouteEdgeDigestBoundaryCoversQueuedSameSecondSuccesses(t *testing.T) {
	now := time.Unix(46_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 314, CoveredThrough: 2, Edges: []protocol.RouteEdgeSnapshot{{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", LastUsedAt: now.Unix(),
		LastDirectResult: "success", AddressFingerprint: "fp-new", SuccessCount: 2, LatencyTotalMS: 22,
	}}}))
	oldFingerprint := protocol.RouteEvent{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", PathKind: "direct",
		Result: "success", AddressFingerprint: "fp-old", DurationMS: 10, ObservedAt: now.Unix(), Sequence: 1,
	}
	newFingerprint := oldFingerprint
	newFingerprint.AddressFingerprint = "fp-new"
	newFingerprint.DurationMS = 12
	newFingerprint.Sequence = 2
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 314, Events: []protocol.RouteEvent{oldFingerprint, newFingerprint},
	}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, "fp-new", edge.AddressFingerprint)
	require.Equal(t, uint64(2), edge.SuccessCount)
	require.Equal(t, uint64(22), edge.LatencyTotalMS)
}

func TestRouteEdgeDigestBoundaryAllowsImmediateSameSecondSuccess(t *testing.T) {
	now := time.Unix(47_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 315, CoveredThrough: 2, Edges: []protocol.RouteEdgeSnapshot{{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", LastUsedAt: now.Unix(),
		LastDirectResult: "success", AddressFingerprint: "fp-new", SuccessCount: 2, LatencyTotalMS: 22,
	}}}))
	newSuccess := protocol.RouteEvent{
		TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", PathKind: "direct",
		Result: "success", AddressFingerprint: "fp-new", DurationMS: 13, ObservedAt: now.Unix(), Sequence: 3,
	}
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 315, Events: []protocol.RouteEvent{newSuccess},
	}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, uint64(3), edge.SuccessCount)
	require.Equal(t, uint64(35), edge.LatencyTotalMS)
}

func TestRouteEdgeAppliesMoreThanSixteenSameSecondStateChanges(t *testing.T) {
	now := time.Unix(48_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 316}))
	events := make([]protocol.RouteEvent, 0, 17)
	for i := 1; i <= 17; i++ {
		events = append(events, protocol.RouteEvent{
			TargetAgentID: "target-a", RouteID: 5, SelectorKind: "agent_id", Result: "error",
			ReasonCode: fmt.Sprintf("failure-%d", i), AddressFingerprint: fmt.Sprintf("fp-%d", i),
			ObservedAt: now.Unix(), Sequence: uint64(i),
		})
	}
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 316, Events: events}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, "failure-17", edge.LastDirectResult)
	require.Equal(t, "fp-17", edge.AddressFingerprint)
}

func TestRouteEdgeSequenceRejectsDuplicatesAndOutOfOrderEvents(t *testing.T) {
	now := time.Unix(49_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 317, CoveredThrough: 10}))
	event := protocol.RouteEvent{
		TargetAgentID: "target-a", PathKind: "direct", Result: "success", DurationMS: 7,
		ObservedAt: now.Unix(), Sequence: 12,
	}
	outOfOrder := event
	outOfOrder.Sequence = 11
	newer := event
	newer.Sequence = 13
	newer.DurationMS = 9
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 317, Events: []protocol.RouteEvent{event, outOfOrder, event, newer},
	}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, uint64(2), edge.SuccessCount)
	require.Equal(t, uint64(16), edge.LatencyTotalMS)
}

func TestRouteEdgeSequenceBoundaryResetsWithProcessGeneration(t *testing.T) {
	now := time.Unix(49_500, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 318, CoveredThrough: 100}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 318, Events: []protocol.RouteEvent{{
		TargetAgentID: "old-process", Result: "error", ObservedAt: now.Unix(), Sequence: 101,
	}}}))

	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 319, CoveredThrough: 1,
		Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "new-process", LastUsedAt: now.Unix(), SuccessCount: 3}},
	}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 319, Events: []protocol.RouteEvent{{
		TargetAgentID: "new-process", PathKind: "direct", Result: "success", DurationMS: 4,
		ObservedAt: now.Unix(), Sequence: 2,
	}}}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, "new-process", edge.TargetAgentID)
	require.Equal(t, uint64(4), edge.SuccessCount)
}

func TestRouteEdgeZeroSequenceHasFiniteLegacyCompatibility(t *testing.T) {
	now := time.Unix(49_750, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 320}))
	legacy := protocol.RouteEvent{
		TargetAgentID: "target-a", PathKind: "direct", Result: "success", DurationMS: 5, ObservedAt: now.Unix(),
	}
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 320, Events: []protocol.RouteEvent{legacy, legacy},
	}))
	require.Equal(t, uint64(1), svc.RouteEdges("source-a")[0].SuccessCount, "legacy duplicate memory is bounded to the last event per edge")

	sequenced := legacy
	sequenced.Result = "error"
	sequenced.ReasonCode = "dial_failed"
	sequenced.Sequence = 1
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 320, Events: []protocol.RouteEvent{sequenced}}))
	legacy.ObservedAt++
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 320, Events: []protocol.RouteEvent{legacy}}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, "dial_failed", edge.LastDirectResult, "zero sequence cannot cross a nonzero sequence boundary")
	require.Equal(t, uint64(1), edge.SuccessCount)
}

func TestRouteEdgeDigestCannotRegressSequenceBoundary(t *testing.T) {
	now := time.Unix(49_875, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 321, CoveredThrough: 5,
		Edges: []protocol.RouteEdgeSnapshot{{
			TargetAgentID: "target-a", LastUsedAt: now.Unix(), LastDirectResult: "current",
		}},
	}))

	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 321, CoveredThrough: 4,
		Edges: []protocol.RouteEdgeSnapshot{{
			TargetAgentID: "target-a", LastUsedAt: now.Unix(), LastDirectResult: "stale",
		}},
	}))
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 321,
		Edges: []protocol.RouteEdgeSnapshot{{
			TargetAgentID: "target-a", LastUsedAt: now.Unix(), LastDirectResult: "legacy-downgrade",
		}},
	}))

	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, "current", edge.LastDirectResult)
	legacy := protocol.RouteEvent{
		TargetAgentID: "target-a", Result: "error", ReasonCode: "legacy-event", ObservedAt: now.Unix(),
	}
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 321, Events: []protocol.RouteEvent{legacy}}))
	require.Equal(t, "current", svc.RouteEdges("source-a")[0].LastDirectResult)
}

func TestRouteEdgeLegacyDigestCoversQueuedSuccessesButNotNewStateChanges(t *testing.T) {
	now := time.Unix(49_900, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{
		Generation: 322,
		Edges: []protocol.RouteEdgeSnapshot{{
			TargetAgentID: "target-a", LastUsedAt: now.Unix(), LastDirectResult: "success",
			AddressFingerprint: "fp-new", SuccessCount: 2, LatencyTotalMS: 22,
		}},
	}))
	coveredOld := protocol.RouteEvent{
		TargetAgentID: "target-a", PathKind: "direct", Result: "success",
		AddressFingerprint: "fp-old", DurationMS: 10, ObservedAt: now.Unix(),
	}
	coveredNew := coveredOld
	coveredNew.AddressFingerprint = "fp-new"
	coveredNew.DurationMS = 12
	stateChange := coveredNew
	stateChange.Result = "error"
	stateChange.ReasonCode = "dial_failed"
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 322, Events: []protocol.RouteEvent{coveredOld, coveredNew, stateChange},
	}))
	edge := svc.RouteEdges("source-a")[0]
	require.Equal(t, uint64(2), edge.SuccessCount)
	require.Equal(t, uint64(22), edge.LatencyTotalMS)
	require.Equal(t, "dial_failed", edge.LastDirectResult)

	now = now.Add(time.Second)
	freshSuccess := coveredNew
	freshSuccess.ObservedAt = now.Unix()
	freshSuccess.DurationMS = 13
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{
		Generation: 322, Events: []protocol.RouteEvent{freshSuccess},
	}))
	edge = svc.RouteEdges("source-a")[0]
	require.Equal(t, uint64(3), edge.SuccessCount)
	require.Equal(t, uint64(35), edge.LatencyTotalMS)
}

func TestRouteEdgesDigestRepairsDroppedEventAndValidatesInput(t *testing.T) {
	now := time.Unix(50_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.Error(t, svc.ReplaceDigest("", protocol.RouteEdgeDigest{Generation: 1}))
	require.Error(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{}))
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 404}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 404, Events: []protocol.RouteEvent{{TargetAgentID: "received", ObservedAt: now.Unix()}}}))

	digest := protocol.RouteEdgeDigest{Generation: 404, Edges: []protocol.RouteEdgeSnapshot{
		{TargetAgentID: "received", LastUsedAt: now.Unix()},
		{TargetAgentID: "dropped", LastUsedAt: now.Unix()},
	}}
	require.NoError(t, svc.ReplaceDigest("source-a", digest))
	require.Len(t, svc.RouteEdges("source-a"), 2)
}

func TestRouteEdgeFutureTimestampsAreClampedToMasterReceiptTime(t *testing.T) {
	now := time.Unix(80_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 601, Edges: []protocol.RouteEdgeSnapshot{{
		TargetAgentID: "future-digest", LastUsedAt: now.Add(24 * time.Hour).Unix(),
	}}}))
	require.NoError(t, svc.ApplyEvents("source-a", protocol.RouteTelemetryBatch{Generation: 601, Events: []protocol.RouteEvent{{
		TargetAgentID: "future-event", ObservedAt: now.Add(24 * time.Hour).Unix(),
	}}}))
	for _, edge := range svc.RouteEdges("source-a") {
		require.Equal(t, now.Unix(), edge.LastUsedAt)
	}
	now = now.Add(time.Hour)
	require.Empty(t, svc.RouteEdges("source-a"))
}

func TestRouteEdgeSweeperExpiresInactiveSourcesAndStopsWithContext(t *testing.T) {
	now := time.Unix(90_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	require.NoError(t, svc.ReplaceDigest("source-a", protocol.RouteEdgeDigest{Generation: 701, Edges: []protocol.RouteEdgeSnapshot{{
		TargetAgentID: "target-a", LastUsedAt: now.Unix(),
	}}}))
	now = now.Add(time.Hour)
	svc.sweepExpiredRouteEdges()
	svc.edgesMu.Lock()
	_, exists := svc.edges["source-a"]
	svc.edgesMu.Unlock()
	require.False(t, exists)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		svc.Run(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("route edge sweeper did not stop with its owner context")
	}
}

func TestRouteEdgeSessionOwnershipRejectsOldControlAndUnknownProcessGenerations(t *testing.T) {
	now := time.Unix(100_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	const sourceID = "source-a"

	for controlGeneration := uint64(1); controlGeneration <= 20; controlGeneration++ {
		processGeneration := 1_000 + controlGeneration
		svc.AdvanceRouteObservationSession(sourceID, controlGeneration)
		require.NoError(t, svc.ReplaceSessionDigest(sourceID, controlGeneration, protocol.RouteEdgeDigest{
			Generation: processGeneration,
			Edges:      []protocol.RouteEdgeSnapshot{{TargetAgentID: fmt.Sprintf("target-%d", controlGeneration), LastUsedAt: now.Unix()}},
		}))
		require.Equal(t, fmt.Sprintf("target-%d", controlGeneration), svc.RouteEdges(sourceID)[0].TargetAgentID)
	}

	ancient := protocol.RouteEdgeDigest{Generation: 1_001, Edges: []protocol.RouteEdgeSnapshot{{TargetAgentID: "ancient", LastUsedAt: now.Unix()}}}
	require.NoError(t, svc.ReplaceSessionDigest(sourceID, 20, ancient))
	require.Equal(t, "target-20", svc.RouteEdges(sourceID)[0].TargetAgentID, "unknown process generation cannot replace within one control session")

	late := protocol.RouteTelemetryBatch{Generation: 1_019, Events: []protocol.RouteEvent{{TargetAgentID: "late-control", ObservedAt: now.Unix() + 1}}}
	require.NoError(t, svc.ApplySessionEvents(sourceID, 19, late))
	require.Equal(t, "target-20", svc.RouteEdges(sourceID)[0].TargetAgentID, "old control generation remains rejected after more than 16 replacements")
}

func TestRouteEdgeSessionRejectsEventsUntilDigestEstablishesProcess(t *testing.T) {
	now := time.Unix(110_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	svc.AdvanceRouteObservationSession("source-a", 7)
	require.NoError(t, svc.ApplySessionEvents("source-a", 7, protocol.RouteTelemetryBatch{
		Generation: 707, Events: []protocol.RouteEvent{{TargetAgentID: "before-digest", ObservedAt: now.Unix()}},
	}))
	require.Empty(t, svc.RouteEdges("source-a"))
	require.NoError(t, svc.ReplaceSessionDigest("source-a", 7, protocol.RouteEdgeDigest{Generation: 707}))
	require.NoError(t, svc.ApplySessionEvents("source-a", 7, protocol.RouteTelemetryBatch{
		Generation: 707, Events: []protocol.RouteEvent{{TargetAgentID: "after-digest", ObservedAt: now.Unix()}},
	}))
	require.Equal(t, "after-digest", svc.RouteEdges("source-a")[0].TargetAgentID)
}

func TestRouteFailuresAreCorrelatedBoundedAndResetWithControlOwnership(t *testing.T) {
	now := time.Unix(120_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	const sourceID = "source-a"
	require.NoError(t, svc.ReplaceDigest(sourceID, protocol.RouteEdgeDigest{Generation: 808}))

	success := protocol.RouteEvent{
		RequestID: "success", TargetAgentID: "target-a", RouteID: 7, PathKind: "direct",
		Result: "success", CommitState: "committed", ObservedAt: now.Unix(), Sequence: 1,
	}
	require.NoError(t, svc.ApplyEvents(sourceID, protocol.RouteTelemetryBatch{Generation: 808, Events: []protocol.RouteEvent{success}}))
	for sequence := uint64(2); sequence <= 26; sequence++ {
		event := protocol.RouteEvent{
			RequestID: fmt.Sprintf("request-%02d", sequence), TargetAgentID: "target-a", RouteID: 7,
			PathKind: "relay", Result: "error", Stage: "commit", CommitState: "commit_uncertain",
			ReasonCode: "relay_commit_uncertain", ObservedAt: now.Unix() + int64(sequence), Sequence: sequence,
		}
		require.NoError(t, svc.ApplyEvents(sourceID, protocol.RouteTelemetryBatch{Generation: 808, Events: []protocol.RouteEvent{event}}))
	}

	failures := svc.RouteFailures(sourceID)
	require.Len(t, failures, 20)
	require.Equal(t, "request-07", failures[0].RequestID)
	require.Equal(t, "request-26", failures[19].RequestID)
	require.Equal(t, sourceID, failures[19].SourceAgentID)
	require.Equal(t, "target-a", failures[19].TargetAgentID)
	require.Equal(t, uint(7), failures[19].RouteID)
	require.Equal(t, "relay", failures[19].PathKind)
	require.Equal(t, "commit", failures[19].Stage)
	require.Equal(t, "commit_uncertain", failures[19].CommitState)
	require.Equal(t, "relay_commit_uncertain", failures[19].ReasonCode)
	failures[0].RequestID = "mutated"
	require.Equal(t, "request-07", svc.RouteFailures(sourceID)[0].RequestID)

	svc.AdvanceRouteObservationSession(sourceID, 9)
	require.Empty(t, svc.RouteFailures(sourceID))
}

func TestRouteFailuresKeepAttemptFieldsIndependentFromEdgeSummary(t *testing.T) {
	now := time.Unix(121_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	const sourceID = "source-a"
	require.NoError(t, svc.ReplaceDigest(sourceID, protocol.RouteEdgeDigest{Generation: 809}))

	event := protocol.RouteEvent{
		RequestID: "request-1", TargetAgentID: "target-a", RouteID: 9,
		PathKind: "relay", Result: "error", Stage: "dial", CommitState: "commit_uncertain",
		ReasonCode: "direct_dns", ObservedAt: now.Unix(), Sequence: 1,
		Failures: []protocol.RouteFailureObservation{
			{PathKind: "direct", Stage: "dial", CommitState: "pre_commit", ReasonCode: "direct_dns"},
			{PathKind: "relay", Stage: "commit", CommitState: "commit_uncertain", ReasonCode: "relay_commit_uncertain"},
		},
	}
	require.NoError(t, svc.ApplyEvents(sourceID, protocol.RouteTelemetryBatch{Generation: 809, Events: []protocol.RouteEvent{event}}))

	failures := svc.RouteFailures(sourceID)
	require.Len(t, failures, 2)
	require.Equal(t, RouteFailureDiagnostic{
		RequestID: "request-1", SourceAgentID: sourceID, TargetAgentID: "target-a", RouteID: 9,
		PathKind: "direct", Stage: "dial", CommitState: "pre_commit", ReasonCode: "direct_dns", OccurredAt: now.Unix(),
	}, failures[0])
	require.Equal(t, RouteFailureDiagnostic{
		RequestID: "request-1", SourceAgentID: sourceID, TargetAgentID: "target-a", RouteID: 9,
		PathKind: "relay", Stage: "commit", CommitState: "commit_uncertain", ReasonCode: "relay_commit_uncertain", OccurredAt: now.Unix(),
	}, failures[1])
}

func TestLegacyRouteFailureEventDedupesAttemptObservations(t *testing.T) {
	now := time.Unix(122_000, 0)
	svc := NewService("instance-a", Sources{}, Options{Now: func() time.Time { return now }})
	const sourceID = "source-a"
	require.NoError(t, svc.ReplaceDigest(sourceID, protocol.RouteEdgeDigest{Generation: 810}))
	event := protocol.RouteEvent{
		RequestID: "legacy-request", TargetAgentID: "target-a", PathKind: "relay", Result: "success",
		ObservedAt: now.Unix(), Failures: []protocol.RouteFailureObservation{{
			PathKind: "direct", Stage: "dial", CommitState: "pre_commit", ReasonCode: "direct_dns",
		}},
	}

	for range 2 {
		require.NoError(t, svc.ApplyEvents(sourceID, protocol.RouteTelemetryBatch{Generation: 810, Events: []protocol.RouteEvent{event}}))
	}
	require.Len(t, svc.RouteFailures(sourceID), 1)
}
