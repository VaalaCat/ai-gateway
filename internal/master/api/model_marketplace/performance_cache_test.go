package model_marketplace

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

func TestGlobalModelPerformanceCacheSnapshotColdLoadUsesSingleflight(t *testing.T) {
	const readers = 24
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return performanceCacheTestSnapshot(1, observedUntil), nil
	}))

	start := make(chan struct{})
	workers := pool.NewWithResults[performanceCacheGetResult]().WithMaxGoroutines(readers)
	for range readers {
		workers.Go(func() performanceCacheGetResult {
			<-start
			snapshot, status, err := cache.Get(context.Background(), now)
			return performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
		})
	}
	close(start)
	<-entered
	require.Equal(t, int64(1), calls.Load())
	close(release)
	results := workers.Wait()

	require.Len(t, results, readers)
	for _, result := range results {
		require.NoError(t, result.err)
		require.Equal(t, PerformanceAvailable, result.status)
		requirePerformanceSnapshotMarker(t, result.snapshot, 1)
		require.Equal(t, now, result.snapshot.RefreshedAt)
		require.Equal(t, now, result.snapshot.ObservedUntil)
		require.Equal(t, now, result.snapshot.LastRefreshAttemptAt)
	}
	require.Equal(t, int64(1), calls.Load(), "all cold readers must wait for one loader call")
}

func TestGlobalModelPerformanceCacheSnapshotRefreshesAtExactHourOnce(t *testing.T) {
	const readers = 20
	startedAt := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	refreshEntered := make(chan struct{})
	refreshRelease := make(chan struct{})
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		call := calls.Add(1)
		if call == 2 {
			close(refreshEntered)
			<-refreshRelease
		}
		return performanceCacheTestSnapshot(call, observedUntil), nil
	}))

	initial, status, err := cache.Get(context.Background(), startedAt)
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, initial, 1)

	beforeBoundary, status, err := cache.Get(context.Background(), startedAt.Add(time.Hour-time.Nanosecond))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, beforeBoundary, 1)
	require.Equal(t, int64(1), calls.Load(), "less than one hour must return the same snapshot")

	start := make(chan struct{})
	workers := pool.NewWithResults[performanceCacheGetResult]().WithMaxGoroutines(readers)
	for range readers {
		workers.Go(func() performanceCacheGetResult {
			<-start
			snapshot, gotStatus, getErr := cache.Get(context.Background(), startedAt.Add(time.Hour))
			return performanceCacheGetResult{snapshot: snapshot, status: gotStatus, err: getErr}
		})
	}
	close(start)
	<-refreshEntered
	require.Equal(t, int64(2), calls.Load(), "the exact one-hour boundary must start one blocking refresh")
	close(refreshRelease)
	results := workers.Wait()
	for _, result := range results {
		require.NoError(t, result.err)
		require.Equal(t, PerformanceAvailable, result.status)
		requirePerformanceSnapshotMarker(t, result.snapshot, 2)
	}
	require.Equal(t, int64(2), calls.Load())
}

func TestGlobalModelPerformanceCacheSnapshotRechecksFreshnessInsideFlight(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		calls.Add(1)
		return performanceCacheTestSnapshot(9, observedUntil), nil
	}))
	cache.state.Store(&performanceCacheState{snapshot: performanceCacheTestSnapshot(3, now)})

	got, err := cache.refreshSnapshotInsideFlight(&performanceLoadFlight{
		ctx: context.Background(), attemptStartedAt: now.Add(59 * time.Minute),
	})

	require.NoError(t, err)
	requirePerformanceSnapshotMarker(t, got.snapshot, 3)
	require.Zero(t, calls.Load(), "a waiter entering after another refresh must not refresh again")
}

func TestGlobalModelPerformanceCacheSnapshotColdFailureBacksOffAndRetriesAtMinute(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	loadErr := errors.New("log database unavailable")
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		if calls.Add(1) == 1 {
			return nil, loadErr
		}
		return performanceCacheTestSnapshot(2, observedUntil), nil
	}))
	cache.clock = newMutablePerformanceClock(now)

	got, status, err := cache.Get(context.Background(), now)
	require.Nil(t, got)
	require.Equal(t, PerformanceUnavailable, status)
	require.ErrorIs(t, err, loadErr)

	got, status, err = cache.Get(context.Background(), now.Add(time.Minute-time.Nanosecond))
	require.Nil(t, got)
	require.Equal(t, PerformanceUnavailable, status)
	require.ErrorIs(t, err, loadErr)
	require.Equal(t, int64(1), calls.Load(), "failed cold loads are suppressed for less than one minute")

	got, status, err = cache.Get(context.Background(), now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, got, 2)
	require.Equal(t, int64(2), calls.Load(), "the exact one-minute boundary permits retry")
}

func TestGlobalModelPerformanceCacheSnapshotFailedRefreshPreservesOldValueAsStale(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	loadErr := errors.New("histogram read failed")
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		switch call := calls.Add(1); call {
		case 1:
			return performanceCacheTestSnapshot(1, observedUntil), nil
		case 2:
			return performanceCacheTestSnapshot(999, observedUntil), loadErr
		default:
			return performanceCacheTestSnapshot(3, observedUntil), nil
		}
	}))
	clock := newMutablePerformanceClock(now)
	cache.clock = clock

	initial, _, err := cache.Get(context.Background(), now)
	require.NoError(t, err)
	requirePerformanceSnapshotMarker(t, initial, 1)
	failureAt := now.Add(time.Hour)
	clock.Advance(time.Hour)

	stale, status, err := cache.Get(context.Background(), failureAt)
	require.Equal(t, PerformanceStale, status)
	require.ErrorIs(t, err, loadErr)
	requirePerformanceSnapshotMarker(t, stale, 1)
	require.Equal(t, now, stale.RefreshedAt)
	require.Equal(t, failureAt, stale.LastRefreshAttemptAt)

	stale, status, err = cache.Get(context.Background(), failureAt.Add(time.Minute-time.Nanosecond))
	require.Equal(t, PerformanceStale, status)
	require.ErrorIs(t, err, loadErr)
	requirePerformanceSnapshotMarker(t, stale, 1)
	require.Equal(t, int64(2), calls.Load())

	refreshed, status, err := cache.Get(context.Background(), failureAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, refreshed, 3)
	require.Equal(t, int64(3), calls.Load())
}

func TestGlobalModelPerformanceCacheFailureBackoffStartsWhenSlowLoaderFinishes(t *testing.T) {
	for _, delay := range []time.Duration{40 * time.Second, 70 * time.Second} {
		t.Run(delay.String(), func(t *testing.T) {
			startedAt := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
			clock := newMutablePerformanceClock(startedAt)
			loadErr := errors.New("slow log database failure")
			var calls atomic.Int64
			var observedMu sync.Mutex
			var observed []time.Time
			cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
				observedMu.Lock()
				observed = append(observed, observedUntil)
				observedMu.Unlock()
				if calls.Add(1) == 1 {
					clock.Advance(delay)
					return nil, loadErr
				}
				return performanceCacheTestSnapshot(2, observedUntil), nil
			}))
			cache.clock = clock

			got, status, err := cache.Get(context.Background(), startedAt)
			require.Nil(t, got)
			require.Equal(t, PerformanceUnavailable, status)
			require.ErrorIs(t, err, loadErr)
			failedAt := startedAt.Add(delay)

			got, status, err = cache.Get(context.Background(), failedAt.Add(time.Minute-time.Nanosecond))
			require.Nil(t, got)
			require.Equal(t, PerformanceUnavailable, status)
			require.ErrorIs(t, err, loadErr)
			require.Equal(t, int64(1), calls.Load(), "a slow failure must still receive a full minute of backoff after completion")

			got, status, err = cache.Get(context.Background(), failedAt.Add(time.Minute))
			require.NoError(t, err)
			require.Equal(t, PerformanceAvailable, status)
			requirePerformanceSnapshotMarker(t, got, 2)
			require.Equal(t, int64(2), calls.Load(), "the exact completion-plus-minute boundary permits retry")
			observedMu.Lock()
			require.Equal(t, []time.Time{startedAt, failedAt.Add(time.Minute)}, observed,
				"observed_until remains the UTC cutoff captured when each loader starts")
			observedMu.Unlock()
		})
	}
}

func TestGlobalModelPerformanceCacheLeaderCancellationDoesNotCancelLiveWaiter(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	lifecycle, cancelLifecycle := context.WithCancel(context.Background())
	defer cancelLifecycle()
	loadStarted := make(chan struct{})
	loadCanceled := make(chan struct{})
	releaseLoad := make(chan struct{})
	cache := newGlobalModelPerformanceCache(lifecycle, time.Minute, systemPerformanceClock{}, PerformanceSnapshotLoaderFunc(
		func(ctx context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			close(loadStarted)
			select {
			case <-ctx.Done():
				close(loadCanceled)
				return nil, ctx.Err()
			case <-releaseLoad:
				return performanceCacheTestSnapshot(1, observedUntil), nil
			}
		},
	))

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, status, err := cache.Get(leaderCtx, now)
		leaderDone <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
	}()
	<-loadStarted
	healthyDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, status, err := cache.Get(context.Background(), now)
		healthyDone <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
	}()
	waitForPerformanceCacheWaiters(t, cache, 2)

	cancelLeader()
	leader := <-leaderDone
	require.ErrorIs(t, leader.err, context.Canceled)
	select {
	case <-loadCanceled:
		t.Fatal("leader cancellation canceled a shared load with a live waiter")
	default:
	}
	close(releaseLoad)
	healthy := <-healthyDone
	require.NoError(t, healthy.err)
	require.Equal(t, PerformanceAvailable, healthy.status)
	requirePerformanceSnapshotMarker(t, healthy.snapshot, 1)
}

func TestGlobalModelPerformanceCacheCanceledWaiterReturnsWhileSharedLoadContinues(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	loadStarted := make(chan struct{})
	loadCanceled := make(chan struct{})
	releaseLoad := make(chan struct{})
	cache := newGlobalModelPerformanceCache(context.Background(), time.Minute, systemPerformanceClock{}, PerformanceSnapshotLoaderFunc(
		func(ctx context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			close(loadStarted)
			select {
			case <-ctx.Done():
				close(loadCanceled)
				return nil, ctx.Err()
			case <-releaseLoad:
				return performanceCacheTestSnapshot(1, observedUntil), nil
			}
		},
	))

	leaderDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, status, err := cache.Get(context.Background(), now)
		leaderDone <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
	}()
	<-loadStarted
	waiterCtx, cancelWaiter := context.WithCancel(context.Background())
	waiterDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, status, err := cache.Get(waiterCtx, now)
		waiterDone <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
	}()
	waitForPerformanceCacheWaiters(t, cache, 2)

	cancelWaiter()
	select {
	case waiter := <-waiterDone:
		require.ErrorIs(t, waiter.err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("canceled waiter remained blocked on the shared loader")
	}
	select {
	case <-loadCanceled:
		t.Fatal("one canceled waiter stopped the shared loader")
	default:
	}
	close(releaseLoad)
	leader := <-leaderDone
	require.NoError(t, leader.err)
	require.Equal(t, PerformanceAvailable, leader.status)
}

func TestGlobalModelPerformanceCacheAllWaitersCancelLoaderWithoutFailureBackoff(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	loadStarted := make(chan struct{})
	loadCanceled := make(chan struct{})
	var calls atomic.Int64
	cache := newGlobalModelPerformanceCache(context.Background(), time.Minute, systemPerformanceClock{}, PerformanceSnapshotLoaderFunc(
		func(ctx context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			if calls.Add(1) == 1 {
				close(loadStarted)
				<-ctx.Done()
				close(loadCanceled)
				return nil, ctx.Err()
			}
			return performanceCacheTestSnapshot(2, observedUntil), nil
		},
	))

	contexts := make([]context.Context, 2)
	cancels := make([]context.CancelFunc, 2)
	results := make(chan performanceCacheGetResult, 2)
	for i := range contexts {
		contexts[i], cancels[i] = context.WithCancel(context.Background())
		go func(ctx context.Context) {
			snapshot, status, err := cache.Get(ctx, now)
			results <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
		}(contexts[i])
	}
	<-loadStarted
	waitForPerformanceCacheWaiters(t, cache, 2)
	for _, cancel := range cancels {
		cancel()
	}
	for range contexts {
		require.ErrorIs(t, (<-results).err, context.Canceled)
	}
	select {
	case <-loadCanceled:
	case <-time.After(time.Second):
		t.Fatal("shared loader was not canceled after every waiter left")
	}

	retried, status, err := cache.Get(context.Background(), now.Add(time.Second))
	require.NoError(t, err, "waiter-driven cancellation must not create backend failure backoff")
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, retried, 2)
	require.Equal(t, int64(2), calls.Load())
}

func TestGlobalModelPerformanceCacheForgetsAbandonedNonCooperativeFlights(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	started := [3]chan struct{}{make(chan struct{}), make(chan struct{}), make(chan struct{})}
	release := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var releaseOnce [2]sync.Once
	for i := range release {
		index := i
		t.Cleanup(func() { releaseOnce[index].Do(func() { close(release[index]) }) })
	}
	abandonedErr := errors.New("abandoned loader failed after cancellation")
	var calls atomic.Int64
	cache := newGlobalModelPerformanceCache(context.Background(), time.Minute, systemPerformanceClock{}, PerformanceSnapshotLoaderFunc(
		func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			call := int(calls.Add(1))
			close(started[call-1])
			switch call {
			case 1:
				<-release[0]
				return nil, abandonedErr
			case 2:
				<-release[1]
				return performanceCacheTestSnapshot(2, observedUntil), nil
			default:
				return performanceCacheTestSnapshot(3, observedUntil), nil
			}
		},
	))

	cancelGeneration := func(index int) *performanceLoadFlight {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan performanceCacheGetResult, 1)
		go func() {
			snapshot, status, err := cache.Get(ctx, now.Add(time.Duration(index)*time.Second))
			result <- performanceCacheGetResult{snapshot: snapshot, status: status, err: err}
		}()
		select {
		case <-started[index]:
		case <-time.After(time.Second):
			cancel()
			<-result
			t.Fatalf("generation %d loader did not start; abandoned key still occupies singleflight group", index+1)
		}
		waitForPerformanceCacheWaiters(t, cache, 1)
		cache.flightMu.Lock()
		flight := cache.flight
		cache.flightMu.Unlock()
		require.NotNil(t, flight)

		cancel()
		got := <-result
		require.ErrorIs(t, got.err, context.Canceled)
		require.Equal(t, PerformanceUnavailable, got.status)
		return flight
	}
	forceNextGenerationToReuseKey := func() {
		cache.flightMu.Lock()
		cache.flightSeq = 0
		cache.flightMu.Unlock()
	}

	first := cancelGeneration(0)
	forceNextGenerationToReuseKey()
	second := cancelGeneration(1)
	require.Equal(t, first.key, second.key,
		"reusing the key makes singleflight Forget observable instead of relying on unique production keys")
	forceNextGenerationToReuseKey()

	current, status, err := cache.Get(context.Background(), now.Add(2*time.Second))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, current, 3)
	require.Equal(t, int64(3), calls.Load(), "each generation must start independently")

	for i := range release {
		releaseOnce[i].Do(func() { close(release[i]) })
	}
	waitForPerformanceFlightsFinished(t, cache, first, second)

	unchanged, status, err := cache.Get(context.Background(), now.Add(3*time.Second))
	require.NoError(t, err, "an abandoned late failure must not publish backend backoff")
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, unchanged, 3)
	require.Equal(t, int64(3), calls.Load(), "abandoned late results must not replace the current snapshot")
}

func TestGlobalModelPerformanceCacheReaderCannotMixOldFailureWithNewSnapshot(t *testing.T) {
	startedAt := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	clock := newMutablePerformanceClock(startedAt)
	loadErr := errors.New("old refresh failed")
	var calls atomic.Int64
	cache := newGlobalModelPerformanceCache(context.Background(), time.Minute, clock, PerformanceSnapshotLoaderFunc(
		func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
			switch calls.Add(1) {
			case 1:
				return performanceCacheTestSnapshot(1, observedUntil), nil
			case 2:
				return nil, loadErr
			default:
				return performanceCacheTestSnapshot(3, observedUntil), nil
			}
		},
	))
	_, _, err := cache.Get(context.Background(), startedAt)
	require.NoError(t, err)
	clock.Advance(time.Hour)
	failureAt := clock.Now()
	stale, status, err := cache.Get(context.Background(), failureAt)
	require.ErrorIs(t, err, loadErr)
	require.Equal(t, PerformanceStale, status)
	requirePerformanceSnapshotMarker(t, stale, 1)

	oldReaderLoaded := make(chan struct{})
	releaseOldReader := make(chan struct{})
	var stateLoads atomic.Int64
	cache.afterStateLoad = func() {
		if stateLoads.Add(1) == 1 {
			close(oldReaderLoaded)
			<-releaseOldReader
		}
	}
	oldReaderDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, gotStatus, getErr := cache.Get(context.Background(), failureAt.Add(30*time.Second))
		oldReaderDone <- performanceCacheGetResult{snapshot: snapshot, status: gotStatus, err: getErr}
	}()
	<-oldReaderLoaded

	clock.Advance(time.Minute)
	refreshed, status, err := cache.Get(context.Background(), failureAt.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, status)
	requirePerformanceSnapshotMarker(t, refreshed, 3)
	close(releaseOldReader)

	oldReader := <-oldReaderDone
	require.ErrorIs(t, oldReader.err, loadErr)
	require.Equal(t, PerformanceStale, oldReader.status)
	requirePerformanceSnapshotMarker(t, oldReader.snapshot, 1)
	require.Equal(t, failureAt, oldReader.snapshot.LastRefreshAttemptAt)
}

func TestGlobalModelPerformanceCacheSnapshotPublishesAtomicDeepCopies(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	refreshEntered := make(chan struct{})
	refreshRelease := make(chan struct{})
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		call := calls.Add(1)
		if call == 2 {
			close(refreshEntered)
			<-refreshRelease
		}
		return performanceCacheTestSnapshot(call, observedUntil), nil
	}))

	first, _, err := cache.Get(context.Background(), now)
	require.NoError(t, err)
	key := performanceCacheTestIdentity()
	first.Offers[key].HourlyTrendComponents30D[0].Components.RequestCount = 999
	first.Offers[ModelOfferIdentity{ModelName: "injected", Kind: OfferKindPlatform, SourceID: 99}] = GlobalOfferPerformanceSnapshot{}
	unmodified, _, err := cache.Get(context.Background(), now.Add(30*time.Minute))
	require.NoError(t, err)
	requirePerformanceSnapshotMarker(t, unmodified, 1)

	refreshDone := make(chan performanceCacheGetResult, 1)
	go func() {
		snapshot, status, refreshErr := cache.Get(context.Background(), now.Add(time.Hour))
		refreshDone <- performanceCacheGetResult{snapshot: snapshot, status: status, err: refreshErr}
	}()
	<-refreshEntered

	readers := pool.NewWithResults[performanceCacheGetResult]().WithMaxGoroutines(12)
	for range 120 {
		readers.Go(func() performanceCacheGetResult {
			snapshot, status, readErr := cache.Get(context.Background(), now.Add(30*time.Minute))
			return performanceCacheGetResult{snapshot: snapshot, status: status, err: readErr}
		})
	}
	readResults := readers.Wait()
	for _, result := range readResults {
		require.NoError(t, result.err)
		require.Equal(t, PerformanceAvailable, result.status)
		requirePerformanceSnapshotMarker(t, result.snapshot, 1)
	}
	close(refreshRelease)
	refreshed := <-refreshDone
	require.NoError(t, refreshed.err)
	require.Equal(t, PerformanceAvailable, refreshed.status)
	requirePerformanceSnapshotMarker(t, refreshed.snapshot, 2)

	after, _, err := cache.Get(context.Background(), now.Add(time.Hour+time.Minute))
	require.NoError(t, err)
	requirePerformanceSnapshotMarker(t, after, 2)
}

type performanceCacheGetResult struct {
	snapshot *GlobalModelPerformanceSnapshot
	status   PerformanceStatus
	err      error
}

type mutablePerformanceClock struct {
	mu  sync.Mutex
	now time.Time
}

func newMutablePerformanceClock(now time.Time) *mutablePerformanceClock {
	return &mutablePerformanceClock{now: now}
}

func (c *mutablePerformanceClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *mutablePerformanceClock) Advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	c.mu.Unlock()
}

func performanceCacheTestIdentity() ModelOfferIdentity {
	return ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: 41}
}

func performanceCacheTestSnapshot(marker int64, observedUntil time.Time) *GlobalModelPerformanceSnapshot {
	currentHour := observedUntil.UTC().Truncate(time.Hour)
	hours := make([]HourlyPerformanceComponents, MarketplacePerformanceHours)
	for i := range hours {
		hours[i] = HourlyPerformanceComponents{
			Hour: currentHour.Add(time.Duration(i-(MarketplacePerformanceHours-1)) * time.Hour),
			Components: PerformanceComponents{
				RequestCount: marker, SuccessCount: marker,
				InputTokens: marker, OutputTokens: marker,
				CacheReadTokens: marker, CacheWriteTokens: marker,
			},
		}
	}
	return &GlobalModelPerformanceSnapshot{
		RefreshedAt: observedUntil, ObservedUntil: observedUntil, LastRefreshAttemptAt: observedUntil,
		Offers: map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot{
			performanceCacheTestIdentity(): {HourlyTrendComponents30D: hours},
		},
	}
}

func requirePerformanceSnapshotMarker(t *testing.T, snapshot *GlobalModelPerformanceSnapshot, marker int64) {
	t.Helper()
	require.NotNil(t, snapshot)
	require.Len(t, snapshot.Offers, 1)
	hours := snapshot.Offers[performanceCacheTestIdentity()].HourlyTrendComponents30D
	require.Len(t, hours, MarketplacePerformanceHours)
	for _, hour := range hours {
		require.Equal(t, marker, hour.Components.RequestCount)
		require.Equal(t, marker, hour.Components.SuccessCount)
		require.Equal(t, marker, hour.Components.InputTokens)
		require.Equal(t, marker, hour.Components.OutputTokens)
		require.Equal(t, marker, hour.Components.CacheReadTokens)
		require.Equal(t, marker, hour.Components.CacheWriteTokens)
	}
}

func waitForPerformanceCacheWaiters(t *testing.T, cache *GlobalModelPerformanceCache, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.flightMu.Lock()
		got := 0
		if cache.flight != nil {
			got = cache.flight.waiters
		}
		cache.flightMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("performance cache waiters did not reach %d", want)
}

func waitForPerformanceFlightsFinished(
	t *testing.T,
	cache *GlobalModelPerformanceCache,
	flights ...*performanceLoadFlight,
) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.flightMu.Lock()
		finished := true
		for _, flight := range flights {
			finished = finished && flight.finished
		}
		cache.flightMu.Unlock()
		if finished {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("abandoned performance cache flights did not finish")
}
