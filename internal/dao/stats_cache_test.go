package dao

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStatsCacheCoalescesConcurrentIdenticalQueries(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := newStatsCache(16, 15*time.Second, func() time.Time { return now })
	key := QueryKey{Name: "dashboard", From: 1, To: 2, Scope: "admin", Model: "gpt-5"}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	load := func(context.Context) (any, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []int{1, 2, 3}, nil
	}

	const workers = 12
	results := make(chan []int, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := cache.Get(t.Context(), key, load)
			if err != nil {
				errs <- err
				return
			}
			results <- value.([]int)
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	for result := range results {
		require.Equal(t, []int{1, 2, 3}, result)
	}
	require.Equal(t, int64(1), calls.Load())
}

func TestStatsCacheDoesNotCoalesceDifferentQueryKeys(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var calls atomic.Int64
	load := func(context.Context) (any, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return "ok", nil
	}

	var wg sync.WaitGroup
	for _, key := range []QueryKey{{Name: "trend", Model: "a"}, {Name: "trend", Model: "b"}} {
		wg.Add(1)
		go func(key QueryKey) {
			defer wg.Done()
			_, _ = cache.Get(t.Context(), key, load)
		}(key)
	}
	<-started
	<-started
	close(release)
	wg.Wait()
	require.Equal(t, int64(2), calls.Load())
}

func TestStatsCacheExpiresAtFifteenSecondBoundary(t *testing.T) {
	now := time.Unix(1_000, 0)
	cache := newStatsCache(16, 15*time.Second, func() time.Time { return now })
	key := QueryKey{Name: "dashboard"}
	var calls int
	load := func(context.Context) (any, error) {
		calls++
		return calls, nil
	}

	first, err := cache.Get(t.Context(), key, load)
	require.NoError(t, err)
	now = now.Add(15*time.Second - time.Nanosecond)
	beforeBoundary, err := cache.Get(t.Context(), key, load)
	require.NoError(t, err)
	now = now.Add(time.Nanosecond)
	atBoundary, err := cache.Get(t.Context(), key, load)
	require.NoError(t, err)
	require.Equal(t, 1, first)
	require.Equal(t, 1, beforeBoundary)
	require.Equal(t, 2, atBoundary)
}

func TestStatsCacheDoesNotCacheErrorsOrCanceledLoads(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	key := QueryKey{Name: "dashboard"}
	wantErr := errors.New("query failed")
	var errorCalls int
	for range 2 {
		_, err := cache.Get(t.Context(), key, func(context.Context) (any, error) {
			errorCalls++
			return nil, wantErr
		})
		require.ErrorIs(t, err, wantErr)
	}
	require.Equal(t, 2, errorCalls)

	var canceledCalls int
	_, err := cache.Get(t.Context(), QueryKey{Name: "canceled"}, func(context.Context) (any, error) {
		canceledCalls++
		return nil, context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 1, canceledCalls)
	var retryCalls int
	value, err := cache.Get(t.Context(), QueryKey{Name: "canceled"}, func(context.Context) (any, error) {
		retryCalls++
		return "fresh", nil
	})
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
	require.Equal(t, 1, retryCalls)
}

func TestStatsCacheReturnsIsolatedValues(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	key := QueryKey{Name: "dashboard"}
	var calls int
	load := func(context.Context) (any, error) {
		calls++
		return map[string][]int{"values": {1, 2}}, nil
	}

	firstValue, err := cache.Get(t.Context(), key, load)
	require.NoError(t, err)
	first := firstValue.(map[string][]int)
	first["values"][0] = 99
	first["extra"] = []int{3}

	secondValue, err := cache.Get(t.Context(), key, load)
	require.NoError(t, err)
	second := secondValue.(map[string][]int)
	require.Equal(t, map[string][]int{"values": {1, 2}}, second)
	require.Equal(t, 1, calls)
}

func TestStatsCacheReturnsIsolatedValuesToWaitersInSameFlight(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	key := QueryKey{Name: "same-flight-isolation"}
	started := make(chan struct{})
	release := make(chan struct{})
	load := func(context.Context) (any, error) {
		close(started)
		<-release
		return map[string][]map[string]int{"nested": {{"value": 1}}}, nil
	}

	type result struct {
		value map[string][]map[string]int
		err   error
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			value, err := cache.Get(t.Context(), key, load)
			if err != nil {
				results <- result{err: err}
				return
			}
			results <- result{value: value.(map[string][]map[string]int)}
		}()
	}
	<-started
	waitForStatsCacheWaiters(t, cache, key, 2)
	close(release)
	firstResult := <-results
	secondResult := <-results
	require.NoError(t, firstResult.err)
	require.NoError(t, secondResult.err)
	first := firstResult.value
	second := secondResult.value
	first["nested"][0]["value"] = 99
	first["nested"] = append(first["nested"], map[string]int{"extra": 2})
	require.Equal(t, map[string][]map[string]int{"nested": {{"value": 1}}}, second)
}

func TestStatsCacheLeaderCancellationDoesNotCancelHealthyWaiter(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	key := QueryKey{Name: "leader-cancel"}
	started := make(chan struct{})
	release := make(chan struct{})
	load := func(ctx context.Context) (any, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
			return "healthy", nil
		}
	}

	leaderCtx, cancelLeader := context.WithCancel(t.Context())
	leaderErr := make(chan error, 1)
	go func() {
		_, err := cache.Get(leaderCtx, key, load)
		leaderErr <- err
	}()
	<-started
	healthyResult := make(chan any, 1)
	healthyErr := make(chan error, 1)
	joined := make(chan struct{})
	go func() {
		close(joined)
		value, err := cache.Get(t.Context(), key, load)
		healthyResult <- value
		healthyErr <- err
	}()
	<-joined
	waitForStatsCacheWaiters(t, cache, key, 2)
	cancelLeader()
	require.ErrorIs(t, <-leaderErr, context.Canceled)
	close(release)
	require.NoError(t, <-healthyErr)
	require.Equal(t, "healthy", <-healthyResult)
}

func TestStatsCacheCancelsSharedLoadAfterOnlyWaiterLeavesAndDoesNotCache(t *testing.T) {
	cache := newStatsCache(16, 15*time.Second, time.Now)
	key := QueryKey{Name: "only-waiter-cancel"}
	loadStarted := make(chan struct{})
	loadCanceled := make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := cache.Get(ctx, key, func(ctx context.Context) (any, error) {
			close(loadStarted)
			<-ctx.Done()
			close(loadCanceled)
			return nil, ctx.Err()
		})
		result <- err
	}()
	<-loadStarted
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	select {
	case <-loadCanceled:
	case <-time.After(time.Second):
		t.Fatal("shared load was not canceled after its only waiter left")
	}

	value, err := cache.Get(t.Context(), key, func(context.Context) (any, error) { return "fresh", nil })
	require.NoError(t, err)
	require.Equal(t, "fresh", value)
}

func waitForStatsCacheWaiters(t *testing.T, cache *StatsCache, key QueryKey, want int) {
	t.Helper()
	baseKey := cache.singleflightKey(key)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		cache.flightMu.Lock()
		flight := cache.flights[baseKey]
		got := 0
		if flight != nil {
			got = flight.waiters
		}
		cache.flightMu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("stats cache waiters did not reach %d", want)
}

func TestStatsCacheEvictsLeastRecentlyUsedEntryAtCapacity(t *testing.T) {
	cache := newStatsCache(2, 15*time.Second, time.Now)
	var calls int
	load := func(context.Context) (any, error) { calls++; return calls, nil }
	a := QueryKey{Name: "a"}
	b := QueryKey{Name: "b"}
	c := QueryKey{Name: "c"}
	for _, key := range []QueryKey{a, b, a, c, b} {
		_, err := cache.Get(t.Context(), key, load)
		require.NoError(t, err)
	}
	require.Equal(t, 4, calls, "a is refreshed before c is inserted, so b must be evicted")
}

func TestStatsCacheClearPreventsOldInflightLoadFromRefillingNewGeneration(t *testing.T) {
	cache := newStatsCache(16, time.Minute, time.Now)
	key := QueryKey{Name: "clear-generation"}
	oldStarted := make(chan struct{})
	oldRelease := make(chan struct{})
	oldDone := make(chan error, 1)
	go func() {
		_, err := cache.Get(t.Context(), key, func(context.Context) (any, error) {
			close(oldStarted)
			<-oldRelease // Deliberately ignores cancellation.
			return "old", nil
		})
		oldDone <- err
	}()
	<-oldStarted

	cache.Clear()
	var freshCalls int
	fresh, err := cache.Get(t.Context(), key, func(context.Context) (any, error) {
		freshCalls++
		return "new", nil
	})
	require.NoError(t, err)
	require.Equal(t, "new", fresh)
	close(oldRelease)
	require.ErrorIs(t, <-oldDone, context.Canceled)

	cached, err := cache.Get(t.Context(), key, func(context.Context) (any, error) {
		t.Fatal("old flight refilled cache or new generation cache was purged")
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, "new", cached)
	require.Equal(t, 1, freshCalls)
}

func TestStatsCacheClearRemovesCompletedEntries(t *testing.T) {
	cache := newStatsCache(16, time.Minute, time.Now)
	key := QueryKey{Name: "clear-completed"}
	first, err := cache.Get(t.Context(), key, func(context.Context) (any, error) { return "old", nil })
	require.NoError(t, err)
	require.Equal(t, "old", first)

	cache.Clear()
	calls := 0
	second, err := cache.Get(t.Context(), key, func(context.Context) (any, error) {
		calls++
		return "new", nil
	})
	require.NoError(t, err)
	require.Equal(t, "new", second)
	require.Equal(t, 1, calls)
}
