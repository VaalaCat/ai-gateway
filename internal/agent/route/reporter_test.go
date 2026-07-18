package route

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type capturedNotification struct {
	method string
	params any
}

type captureNotifier struct {
	mu    sync.Mutex
	calls []capturedNotification
	wake  chan struct{}
}

type blockingNotifier struct {
	*captureNotifier
	blockMethod string
	entered     chan struct{}
	release     chan struct{}
	err         error
	once        sync.Once
}

func (n *blockingNotifier) Notify(method string, params any) error {
	_ = n.captureNotifier.Notify(method, params)
	if method == n.blockMethod {
		n.once.Do(func() { close(n.entered) })
		<-n.release
	}
	return n.err
}

func newCaptureNotifier() *captureNotifier { return &captureNotifier{wake: make(chan struct{}, 16)} }

func (n *captureNotifier) Notify(method string, params any) error {
	n.mu.Lock()
	n.calls = append(n.calls, capturedNotification{method: method, params: params})
	n.mu.Unlock()
	select {
	case n.wake <- struct{}{}:
	default:
	}
	return nil
}

func (n *captureNotifier) snapshot() []capturedNotification {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]capturedNotification(nil), n.calls...)
}

func TestRouteDigestReporterSendsOnReconnectAndPeriodically(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 41, MaxEdges: 8, QueueSize: 8})
	require.NoError(t, err)
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "success"})
	<-observer.Events()

	notifier := newCaptureNotifier()
	reporter := NewReporter(observer, ReporterOptions{DigestInterval: 20 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()
	reporter.SetClient(notifier)

	select {
	case <-notifier.wake:
	case <-time.After(time.Second):
		t.Fatal("reconnect digest was not sent")
	}
	require.Equal(t, consts.RPCAgentRouteDigest, notifier.snapshot()[0].method)

	select {
	case <-notifier.wake:
	case <-time.After(time.Second):
		t.Fatal("periodic digest was not sent")
	}
	require.GreaterOrEqual(t, len(notifier.snapshot()), 2)
	cancel()
	require.NoError(t, <-done)
}

func TestRouteTelemetryReporterSendsQueuedEventsAfterReconnectDigest(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 43, MaxEdges: 8, QueueSize: 8})
	require.NoError(t, err)
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "error", ReasonCode: "dial_failed"})

	notifier := newCaptureNotifier()
	reporter := NewReporter(observer, ReporterOptions{DigestInterval: time.Hour})
	reporter.SetClient(notifier)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()

	require.Eventually(t, func() bool { return len(notifier.snapshot()) >= 2 }, time.Second, time.Millisecond)
	calls := notifier.snapshot()
	require.Equal(t, consts.RPCAgentRouteDigest, calls[0].method)
	require.Equal(t, consts.RPCAgentRouteTelemetry, calls[1].method)
	digest, ok := calls[0].params.(protocol.RouteEdgeDigest)
	require.True(t, ok)
	batch, ok := calls[1].params.(protocol.RouteTelemetryBatch)
	require.True(t, ok)
	require.Equal(t, uint64(43), batch.Generation)
	require.Len(t, batch.Events, 1)
	require.NotZero(t, batch.Events[0].Sequence)
	require.GreaterOrEqual(t, digest.CoveredThrough, batch.Events[0].Sequence)
	cancel()
	require.NoError(t, <-done)
}

func TestRouteDigestReporterRepairsDroppedTelemetry(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 47, MaxEdges: 8, QueueSize: 1})
	require.NoError(t, err)
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "error"})
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-b", Result: "error"})
	require.Equal(t, uint64(1), observer.Dropped())
	require.Len(t, observer.Digest().Edges, 2)
}

func TestRouteDigestReporterOrdersReplacementBeforeAlreadySelectedEvent(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 53, MaxEdges: 8, QueueSize: 8})
	require.NoError(t, err)
	oldClient := newCaptureNotifier()
	newClient := newCaptureNotifier()
	reporter := NewReporter(observer, ReporterOptions{DigestInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()
	reporter.SetClient(oldClient)
	select {
	case <-oldClient.wake:
	case <-time.After(time.Second):
		t.Fatal("old client did not receive initial digest")
	}

	selected := make(chan struct{})
	release := make(chan struct{})
	reporter.beforeEventSend = func() {
		close(selected)
		<-release
	}
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "error"})
	select {
	case <-selected:
	case <-time.After(time.Second):
		t.Fatal("reporter did not select the route event")
	}
	reporter.SetClient(newClient)
	close(release)
	require.Eventually(t, func() bool { return len(newClient.snapshot()) >= 1 }, time.Second, time.Millisecond)
	require.Equal(t, consts.RPCAgentRouteDigest, newClient.snapshot()[0].method)
	cancel()
	require.NoError(t, <-done)
}

func TestRouteDigestReporterReplacementSupersedesBlockedOldNotify(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 59, MaxEdges: 8, QueueSize: 8})
	require.NoError(t, err)
	oldClient := &blockingNotifier{
		captureNotifier: newCaptureNotifier(), blockMethod: consts.RPCAgentRouteTelemetry,
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	newClient := newCaptureNotifier()
	reporter := NewReporter(observer, ReporterOptions{DigestInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()
	reporter.SetClient(oldClient)
	select {
	case <-oldClient.wake:
	case <-time.After(time.Second):
		t.Fatal("old client did not receive initial digest")
	}
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "error"})
	select {
	case <-oldClient.entered:
	case <-time.After(time.Second):
		t.Fatal("old client telemetry did not block")
	}
	reporter.SetClient(newClient)
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-b", Result: "error"})
	close(oldClient.release)
	require.Eventually(t, func() bool { return len(newClient.snapshot()) >= 1 }, time.Second, time.Millisecond)
	require.Equal(t, consts.RPCAgentRouteDigest, newClient.snapshot()[0].method)
	cancel()
	require.NoError(t, <-done)
}

func TestRouteTelemetryReporterCountsNotifyFailures(t *testing.T) {
	observer, err := NewObserver(ObserverOptions{Generation: 61, MaxEdges: 8, QueueSize: 8})
	require.NoError(t, err)
	failing := &blockingNotifier{
		captureNotifier: newCaptureNotifier(), entered: make(chan struct{}), release: make(chan struct{}),
		err: errors.New("send failed"),
	}
	close(failing.release)
	reporter := NewReporter(observer, ReporterOptions{DigestInterval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- reporter.Run(ctx) }()
	reporter.SetClient(failing)
	require.Eventually(t, func() bool { return reporter.SendFailures() == 1 }, time.Second, time.Millisecond)
	observer.Record(protocol.RouteEvent{TargetAgentID: "target-a", Result: "error"})
	require.Eventually(t, func() bool { return reporter.SendFailures() == 2 }, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, <-done)
}
