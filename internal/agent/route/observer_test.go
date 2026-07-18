package route

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestRouteObserverBoundsEdgesAndExpiresIdleEdges(t *testing.T) {
	now := time.Unix(10_000, 0)
	observer, err := NewObserver(ObserverOptions{
		Generation: 17, MaxEdges: 1024, QueueSize: 1024, EdgeTTL: time.Hour,
		SuccessWindow: 30 * time.Second, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	for i := 0; i < 1025; i++ {
		observer.Record(protocol.RouteEvent{TargetAgentID: fmt.Sprintf("target-%04d", i), Result: "success", DurationMS: 2})
	}
	digest := observer.Digest()
	require.Equal(t, uint64(17), digest.Generation)
	require.Len(t, digest.Edges, 1024)
	for _, edge := range digest.Edges {
		require.NotEqual(t, "target-0000", edge.TargetAgentID, "least recently used edge must be evicted")
	}

	now = now.Add(time.Hour)
	require.Empty(t, observer.Digest().Edges, "the one-hour TTL expires at the boundary")
}

func TestRouteObserverEmitsErrorsAndStateChangesImmediatelyButAggregatesSuccess(t *testing.T) {
	now := time.Unix(20_000, 0)
	observer, err := NewObserver(ObserverOptions{
		Generation: 23, MaxEdges: 8, QueueSize: 8, EdgeTTL: time.Hour,
		SuccessWindow: 30 * time.Second, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	base := protocol.RouteEvent{TargetAgentID: "target-a", RouteID: 3, SelectorKind: "agent_id", PathKind: "direct", Result: "success", AddressFingerprint: "fp-a", DurationMS: 10}
	observer.Record(base)
	require.Equal(t, "success", (<-observer.Events()).Result)

	now = now.Add(time.Second)
	observer.Record(base)
	select {
	case event := <-observer.Events():
		t.Fatalf("aggregated success emitted early: %+v", event)
	default:
	}

	errorEvent := base
	errorEvent.Result = "error"
	errorEvent.Stage = "dial"
	errorEvent.ReasonCode = "connection_refused"
	observer.Record(errorEvent)
	require.Equal(t, "connection_refused", (<-observer.Events()).ReasonCode)

	now = now.Add(time.Second)
	changed := base
	changed.AddressFingerprint = "fp-b"
	observer.Record(changed)
	require.Equal(t, "fp-b", (<-observer.Events()).AddressFingerprint)

	now = now.Add(30 * time.Second)
	observer.Record(changed)
	require.Equal(t, "success", (<-observer.Events()).Result)
	digest := observer.Digest()
	require.Len(t, digest.Edges, 1)
	require.Equal(t, uint64(4), digest.Edges[0].SuccessCount)
	require.Equal(t, uint64(40), digest.Edges[0].LatencyTotalMS)
}

func TestRouteObserverFullQueueNeverBlocksAndCountsDrops(t *testing.T) {
	metric := &telemetryDropMetric{}
	observer, err := NewObserver(ObserverOptions{Generation: 31, MaxEdges: 4, QueueSize: 1, Metrics: metric})
	require.NoError(t, err)
	observer.Record(protocol.RouteEvent{TargetAgentID: "a", Result: "error"})

	started := time.Now()
	observer.Record(protocol.RouteEvent{TargetAgentID: "b", Result: "error"})
	require.Less(t, time.Since(started), time.Millisecond)
	require.Equal(t, uint64(1), observer.Dropped())
	require.Equal(t, uint64(1), metric.count.Load())
}

func TestRouteObserverSlowDropMetricDoesNotBlockFollowingRecords(t *testing.T) {
	metricStarted := make(chan struct{})
	releaseMetric := make(chan struct{})
	metric := &telemetryDropMetric{callback: func() {
		select {
		case <-metricStarted:
		default:
			close(metricStarted)
		}
		<-releaseMetric
	}}
	observer, err := NewObserver(ObserverOptions{Generation: 32, QueueSize: 1, Metrics: metric})
	require.NoError(t, err)
	observer.Record(protocol.RouteEvent{TargetAgentID: "queue-fill", Result: "error"})

	firstDone := make(chan struct{})
	go func() {
		observer.Record(protocol.RouteEvent{TargetAgentID: "first-drop", Result: "error"})
		close(firstDone)
	}()
	<-metricStarted

	secondReachedEnqueue := make(chan struct{})
	observer.beforeEventEnqueue = func(event protocol.RouteEvent) {
		if event.TargetAgentID == "second-drop" {
			close(secondReachedEnqueue)
		}
	}
	secondDone := make(chan struct{})
	go func() {
		observer.Record(protocol.RouteEvent{TargetAgentID: "second-drop", Result: "error"})
		close(secondDone)
	}()

	reached := false
	select {
	case <-secondReachedEnqueue:
		reached = true
	case <-time.After(time.Second):
	}
	close(releaseMetric)
	<-firstDone
	<-secondDone
	require.True(t, reached, "a slow metrics callback must not hold recordMu")
	require.Equal(t, uint64(2), metric.count.Load())
}

func TestRouteObserverDropMetricCanReenterRecord(t *testing.T) {
	steady := protocol.RouteEvent{TargetAgentID: "steady", PathKind: "direct", Result: "success"}
	callbackDone := make(chan struct{})
	metric := &telemetryDropMetric{}
	observer, err := NewObserver(ObserverOptions{
		Generation: 33, QueueSize: 1, SuccessWindow: time.Hour, Metrics: metric,
	})
	require.NoError(t, err)
	observer.Record(steady)
	metric.callback = func() {
		observer.Record(steady)
		close(callbackDone)
	}

	recordDone := make(chan struct{})
	go func() {
		observer.Record(protocol.RouteEvent{TargetAgentID: "dropped", Result: "error"})
		close(recordDone)
	}()

	select {
	case <-callbackDone:
	case <-time.After(time.Second):
		t.Fatal("metrics callback deadlocked while reentering Record")
	}
	<-recordDone
	require.Equal(t, uint64(1), metric.count.Load())
}

type telemetryDropMetric struct {
	count    atomic.Uint64
	callback func()
}

func (m *telemetryDropMetric) IncRouteTelemetryDropped() {
	m.count.Add(1)
	if m.callback != nil {
		m.callback()
	}
}

func TestRouteObserverGeneratesNonzeroProcessGeneration(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{})
	require.NoError(t, err)
	require.NotZero(t, observer.Generation())
}

func TestRouteObserverSequencesEveryRecordAndDigestCoversSuppressedSuccess(t *testing.T) {
	now := time.Unix(30_000, 0)
	observer, err := NewObserver(ObserverOptions{
		Generation: 37, MaxEdges: 8, QueueSize: 8, SuccessWindow: time.Hour,
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	event := protocol.RouteEvent{
		TargetAgentID: "target-a", PathKind: "direct", Result: "success", DurationMS: 5,
	}
	observer.Record(event)
	first := <-observer.Events()
	require.Equal(t, uint64(1), first.Sequence)

	observer.Record(event)
	select {
	case emitted := <-observer.Events():
		t.Fatalf("success inside the aggregation window was emitted: %+v", emitted)
	default:
	}
	digest := observer.Digest()
	require.Equal(t, uint64(2), digest.CoveredThrough)
	require.Equal(t, uint64(2), digest.Edges[0].SuccessCount)

	errorEvent := event
	errorEvent.Result = "error"
	errorEvent.ReasonCode = "dial_failed"
	observer.Record(errorEvent)
	require.Equal(t, uint64(3), (<-observer.Events()).Sequence)
}

func TestRouteObserverRecordAndDigestShareAnAtomicSequenceBoundary(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{
		Generation: 41, MaxEdges: 128, QueueSize: 128, SuccessWindow: time.Hour,
	})
	require.NoError(t, err)

	for i := 0; i < 100; i++ {
		target := fmt.Sprintf("target-%03d", i)
		start := make(chan struct{})
		var digest protocol.RouteEdgeDigest
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			observer.Record(protocol.RouteEvent{TargetAgentID: target, Result: "error"})
		}()
		go func() {
			defer wg.Done()
			<-start
			digest = observer.Digest()
		}()
		close(start)
		wg.Wait()

		event := <-observer.Events()
		if digest.CoveredThrough < event.Sequence {
			continue
		}
		covered := false
		for _, edge := range digest.Edges {
			covered = covered || edge.TargetAgentID == target
		}
		require.True(t, covered, "a digest boundary cannot cover an event missing from its edge snapshot")
	}
}

func TestRouteObserverSequenceSaturationStillConvergesThroughDigest(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 43, QueueSize: 1})
	require.NoError(t, err)
	observer.sequence = ^uint64(0)

	observer.Record(protocol.RouteEvent{
		TargetAgentID: "target-saturated", Result: "error", ReasonCode: "dial_failed", Sequence: 7,
	})

	select {
	case event := <-observer.Events():
		t.Fatalf("saturated sequence emitted an ambiguous event: %+v", event)
	default:
	}
	digest := observer.Digest()
	require.Equal(t, ^uint64(0), digest.CoveredThrough)
	require.Len(t, digest.Edges, 1)
	require.Equal(t, "target-saturated", digest.Edges[0].TargetAgentID)
	require.Equal(t, "dial_failed", digest.Edges[0].LastDirectResult)
}

func TestRouteObserverConcurrentRecordsEnqueueMonotonicSequences(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 47, QueueSize: 2})
	require.NoError(t, err)
	firstAssigned := make(chan struct{})
	secondAssigned := make(chan struct{})
	releaseFirst := make(chan struct{})
	observer.beforeEventEnqueue = func(event protocol.RouteEvent) {
		if event.Sequence == 1 {
			close(firstAssigned)
			<-releaseFirst
		}
		if event.Sequence == 2 {
			close(secondAssigned)
		}
	}

	firstDone := make(chan struct{})
	go func() {
		observer.Record(protocol.RouteEvent{TargetAgentID: "target-1", Result: "error"})
		close(firstDone)
	}()
	<-firstAssigned
	secondDone := make(chan struct{})
	go func() {
		observer.Record(protocol.RouteEvent{TargetAgentID: "target-2", Result: "error"})
		close(secondDone)
	}()
	select {
	case <-secondAssigned:
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)
	<-firstDone
	<-secondDone

	first := <-observer.Events()
	second := <-observer.Events()
	require.Less(t, first.Sequence, second.Sequence)
}
