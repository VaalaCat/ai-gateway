package apiusage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type retryingServiceFinder struct {
	failures int32
	calls    atomic.Int32
}

func (f *retryingServiceFinder) FindByID(context.Context, uint) (*models.APIService, error) {
	call := f.calls.Add(1)
	if call <= f.failures {
		return nil, errors.New("temporary core database failure")
	}
	return &models.APIService{PricePerCall: 5}, nil
}

// Production break caught: a transient service lookup failure must retry and
// recover, while a persistent failure must stop precisely at the bounded limit
// and Stop must finish without waiting for a leaked retry goroutine.
func TestAPIUsageWorkerRetriesTemporarySettlementErrorWithinBound(t *testing.T) {
	for _, tc := range []struct {
		name                string
		failures, wantCalls int32
	}{
		{name: "recovers", failures: 1, wantCalls: 2},
		{name: "bounded persistent failure", failures: 9, wantCalls: maxSettlementAttempts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			finder := &retryingServiceFinder{failures: tc.failures}
			metrics := &recordingUsageMetrics{}
			core, observed := observer.New(zap.ErrorLevel)
			queue := NewQueue(QueueOptions{Capacity: 4, DedupCapacity: 4, Metrics: metrics})
			require.NoError(t, queue.Accept(t.Context(), "source", []protocol.APIUsageEntry{{RequestID: tc.name, APIServiceID: 1, ProviderDispatchKnown: true, ProviderDispatched: true}}))
			worker := NewWorker(WorkerOptions{Queue: queue, Settler: NewAPIUsageSettler(finder), Poll: time.Millisecond, RetryBase: time.Millisecond, Metrics: metrics, Logger: zap.New(core)})
			worker.Start(t.Context())
			require.Eventually(t, func() bool { return finder.calls.Load() == tc.wantCalls && queue.Len() == 0 }, time.Second, time.Millisecond)
			require.NoError(t, worker.Stop(t.Context()))
			if tc.failures >= maxSettlementAttempts {
				require.Equal(t, 1, metrics.retryExhausted)
				require.Equal(t, 1, observed.Len())
				require.Equal(t, "api usage settlement retries exhausted", observed.All()[0].Message)
				require.EqualValues(t, maxSettlementAttempts, observed.All()[0].ContextMap()["attempts"])
			} else {
				require.Zero(t, metrics.retryExhausted)
				require.Zero(t, observed.Len())
			}
		})
	}
}

type blockingRetryFinder struct {
	calls        atomic.Int32
	firstStarted chan struct{}
	releaseFirst chan struct{}
	failures     int32
}

func (f *blockingRetryFinder) FindByID(ctx context.Context, _ uint) (*models.APIService, error) {
	call := f.calls.Add(1)
	if call == 1 {
		close(f.firstStarted)
		select {
		case <-f.releaseFirst:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	if call <= f.failures {
		return nil, errors.New("temporary")
	}
	return &models.APIService{}, nil
}

// Production break caught: an acknowledged item owns all of its retries even
// when fresh HTTP admission fills the queue slot released by Take.
func TestAPIUsageWorkerKeepsRetryOwnershipWhenAdmissionRefillsQueue(t *testing.T) {
	metrics := &recordingUsageMetrics{}
	finder := &blockingRetryFinder{firstStarted: make(chan struct{}), releaseFirst: make(chan struct{}), failures: maxSettlementAttempts}
	queue := NewQueue(QueueOptions{Capacity: 1, DedupCapacity: 4, Metrics: metrics})
	require.NoError(t, queue.Accept(t.Context(), "source", []protocol.APIUsageEntry{{RequestID: "owned", APIServiceID: 1}}))
	worker := NewWorker(WorkerOptions{Queue: queue, Settler: NewAPIUsageSettler(finder), Metrics: metrics, RetryBase: time.Millisecond, Poll: time.Millisecond})
	worker.Start(t.Context())
	<-finder.firstStarted
	require.NoError(t, queue.Accept(t.Context(), "source", []protocol.APIUsageEntry{{RequestID: "new", APIServiceID: 1}}))
	close(finder.releaseFirst)
	require.Eventually(t, func() bool { return finder.calls.Load() >= maxSettlementAttempts }, time.Second, time.Millisecond)
	require.NoError(t, worker.Stop(t.Context()))
	require.Equal(t, 1, metrics.retryExhausted)
}

type contextIgnoringUsageSettler struct {
	started chan struct{}
	release chan struct{}
}

func (s *contextIgnoringUsageSettler) Settle(context.Context, protocol.APIUsageEntry) (APISettlement, error) {
	close(s.started)
	<-s.release
	return APISettlement{}, nil
}

func TestAPIUsageWorkerStopReturnsAtContextDeadline(t *testing.T) {
	settler := &contextIgnoringUsageSettler{started: make(chan struct{}), release: make(chan struct{})}
	queue := NewQueue(QueueOptions{Capacity: 1, DedupCapacity: 1})
	require.NoError(t, queue.Accept(t.Context(), "source", []protocol.APIUsageEntry{{RequestID: "blocked"}}))
	worker := NewWorker(WorkerOptions{Queue: queue, Settler: settler, Poll: time.Millisecond})
	worker.Start(t.Context())
	<-settler.started

	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, worker.Stop(stopCtx), context.DeadlineExceeded)
	waitCtx, cancelWait := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancelWait()
	require.ErrorIs(t, worker.Wait(waitCtx), context.DeadlineExceeded, "worker must remain owned while settlement is still running")
	close(settler.release)
	require.NoError(t, worker.Wait(t.Context()))
}
