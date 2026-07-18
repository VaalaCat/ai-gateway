package entitycache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sourcegraph/conc"
	"github.com/stretchr/testify/require"
)

func TestLRUCache_BasicSetGetDelete(t *testing.T) {
	c, err := NewLRUCache[string, int](Config[string, int]{Capacity: 4})
	if err != nil {
		t.Fatalf("NewLRUCache: %v", err)
	}

	if _, ok := c.Peek("a"); ok {
		t.Fatal("empty cache should miss")
	}

	c.Set("a", 1)
	if v, ok := c.Peek("a"); !ok || v != 1 {
		t.Fatalf("Peek hit: got (%d, %v)", v, ok)
	}

	c.Delete("a")
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Peek after delete should miss")
	}
}

func TestLRUCache_EvictsOnCapacity(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 2})
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3) // 触发 LRU 淘汰最旧的 "a"

	if _, ok := c.Peek("a"); ok {
		t.Fatal("a should have been evicted")
	}
	if _, ok := c.Peek("b"); !ok {
		t.Fatal("b should still be present")
	}
	if _, ok := c.Peek("c"); !ok {
		t.Fatal("c should be present")
	}

	if s := c.Stats(); s.Evictions == 0 {
		t.Fatalf("Evictions = %d, want >= 1", s.Evictions)
	}
}

func TestLRUCache_ApplyIfPresent(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 4})

	// Apply(Set) on absent key 应静默丢弃
	c.Apply(ActionSet, "a", 1)
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Apply(Set) on absent key must NOT warm cache")
	}

	// 先 Set，再 Apply(Set) 应覆盖
	c.Set("a", 1)
	c.Apply(ActionSet, "a", 2)
	if v, _ := c.Peek("a"); v != 2 {
		t.Fatalf("Apply(Set) on present key should overwrite, got %d", v)
	}

	// Apply(Delete) 在已存在时删除
	c.Apply(ActionDelete, "a", 0)
	if _, ok := c.Peek("a"); ok {
		t.Fatal("Apply(Delete) should remove existing entry")
	}

	// Apply(Delete) 在不存在时幂等无错
	c.Apply(ActionDelete, "absent", 0)
}

func TestLRUCache_GetWithoutLoaderReturnsMiss(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 4})
	v, ok, err := c.Get(context.Background(), "missing")
	if ok || v != 0 || err != nil {
		t.Fatalf("Get without loader on miss should be (0, false, nil), got (%d, %v, %v)", v, ok, err)
	}
}

func TestLRUCache_StatsAndLen(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 4})
	c.Set("a", 1)
	c.Set("b", 2)
	if c.Len() != 2 {
		t.Fatalf("Len = %d, want 2", c.Len())
	}

	if v, ok, _ := c.Get(context.Background(), "a"); !ok || v != 1 {
		t.Fatal("Get hit failed")
	}
	if s := c.Stats(); s.Hits == 0 {
		t.Fatal("Stats.Hits should accumulate on hit")
	}

	c.Get(context.Background(), "absent")
	if s := c.Stats(); s.Misses == 0 {
		t.Fatal("Stats.Misses should accumulate on miss")
	}

	if s := c.Stats(); s.Capacity != 4 {
		t.Fatalf("Stats.Capacity = %d, want 4", s.Capacity)
	}
}

func TestLRUCache_RangeIteratesEntries(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 4})
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	got := map[string]int{}
	c.Range(func(k string, v int) bool {
		got[k] = v
		return true
	})
	if len(got) != 3 {
		t.Fatalf("Range visited %d, want 3", len(got))
	}
}

func TestLRUCache_RejectsZeroCapacity(t *testing.T) {
	if _, err := NewLRUCache[string, int](Config[string, int]{Capacity: 0}); err == nil {
		t.Fatal("Capacity=0 should error")
	}
	if _, err := NewLRUCache[string, int](Config[string, int]{Capacity: -1}); err == nil {
		t.Fatal("Capacity<0 should error")
	}
	if _, err := NewLRUCache[string, int](Config[string, int]{Capacity: 4, MaxConcurrentLoads: -1}); err == nil {
		t.Fatal("MaxConcurrentLoads<0 should error")
	}
}

type stubLoader[K comparable, V any] struct {
	calls atomic.Int64
	fn    func(context.Context, K) (V, error)
}

func (l *stubLoader[K, V]) Load(ctx context.Context, k K) (V, error) {
	l.calls.Add(1)
	return l.fn(ctx, k)
}

func TestLRUCache_LoaderFillsOnMiss(t *testing.T) {
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, k string) (int, error) {
			if k == "exists" {
				return 42, nil
			}
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 4,
		Loader:   loader,
	})

	v, ok, err := c.Get(context.Background(), "exists")
	if !ok || v != 42 || err != nil {
		t.Fatalf("Loader hit: got (%d, %v, %v)", v, ok, err)
	}

	// 第二次应直接命中本地，不再调 loader
	v, ok, err = c.Get(context.Background(), "exists")
	if !ok || v != 42 || err != nil || loader.calls.Load() != 1 {
		t.Fatalf("Second Get should hit cache without re-loading: calls=%d", loader.calls.Load())
	}
}

func TestLRUCache_LoaderErrorTransparent(t *testing.T) {
	want := errors.New("network down")
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, want
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 4,
		Loader:   loader,
	})

	_, _, err := c.Get(context.Background(), "k")
	if !errors.Is(err, want) {
		t.Fatalf("loader error should propagate, got %v", err)
	}

	// 错误不写缓存：再 Get 应再次调 loader
	c.Get(context.Background(), "k")
	if loader.calls.Load() != 2 {
		t.Fatalf("loader should be retried on error: calls=%d", loader.calls.Load())
	}
}

func TestLRUCacheLifecycleJoinsDetachedColdLoadAfterCallerCancel(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	exited := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(ctx context.Context, _ string) (int, error) {
		close(entered)
		<-ctx.Done()
		close(exited)
		return 0, context.Cause(ctx)
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity:  4,
		Loader:    loader,
		Refresh:   func() RefreshConfig { return RefreshConfig{LoadTimeout: time.Hour} },
		Lifecycle: lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	caller, cancel := context.WithCancelCause(context.Background())
	callerCause := errors.New("caller left")
	result := make(chan error, 1)
	go func() {
		_, _, err := c.Get(caller, "blocked")
		result <- err
	}()
	<-entered
	cancel(callerCause)
	if err := <-result; !errors.Is(err, callerCause) {
		t.Fatalf("Get = %v, want caller cause", err)
	}
	loads, refreshes := lifecycle.ResourceCounts()
	if loads != 1 || refreshes != 0 {
		t.Fatalf("counts before Close = loads:%d refreshes:%d", loads, refreshes)
	}
	lifecycle.Close()
	<-exited
	loads, refreshes = lifecycle.ResourceCounts()
	if loads != 0 || refreshes != 0 {
		t.Fatalf("counts after Close = loads:%d refreshes:%d", loads, refreshes)
	}
	select {
	case <-lifecycle.Done():
	default:
		t.Fatal("Lifecycle.Done remained open after loader exit")
	}
}

func TestLRUCacheDetachedColdMissHasOneOwnerForHundredFollowers(t *testing.T) {
	const callers = 100
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	release := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(context.Context, string) (int, error) {
		close(entered)
		<-release
		return 42, nil
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity: 4,
		Loader:   loader,
		Refresh: func() RefreshConfig {
			return RefreshConfig{LoadTimeout: time.Minute}
		},
		Lifecycle: lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lifecycle.Close)
	var releaseOnce sync.Once
	releaseLoad := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseLoad)

	start := make(chan struct{})
	returned := make(chan int, callers)
	results := make([]int, callers)
	errs := make([]error, callers)
	cancels := make([]context.CancelCauseFunc, callers)
	var workers conc.WaitGroup
	for i := range callers {
		ctx, cancel := context.WithCancelCause(context.Background())
		cancels[i] = cancel
		workers.Go(func() {
			<-start
			results[i], _, errs[i] = c.Get(ctx, "same")
			returned <- i
		})
	}
	close(start)
	<-entered
	require.Eventually(t, func() bool {
		loads, refreshes := lifecycle.ResourceCounts()
		return loader.calls.Load() == 1 && loads == 1 && refreshes == 0
	}, time.Second, time.Millisecond, "one detached load owner")

	followerCause := errors.New("follower canceled")
	for i := 1; i <= callers/2; i++ {
		cancels[i](followerCause)
	}
	for range callers / 2 {
		i := <-returned
		if i == 0 || !errors.Is(errs[i], followerCause) {
			t.Fatalf("canceled follower %d returned (%d, %v)", i, results[i], errs[i])
		}
	}
	loads, refreshes := lifecycle.ResourceCounts()
	if loads != 1 || loader.calls.Load() != 1 {
		t.Fatalf("after follower cancellation: loads=%d loader_calls=%d, want 1/1", loads, loader.calls.Load())
	}

	releaseLoad()
	workers.Wait()
	if errs[0] != nil || results[0] != 42 {
		t.Fatalf("owner result = (%d, %v), want (42, nil)", results[0], errs[0])
	}
	require.Eventually(t, func() bool {
		loads, refreshes := lifecycle.ResourceCounts()
		return loads == 0 && refreshes == 0
	}, time.Second, time.Millisecond, "detached load owner exit")
	lifecycle.Close()
	loads, refreshes = lifecycle.ResourceCounts()
	if loads != 0 || refreshes != 0 {
		t.Fatalf("counts after Close = loads:%d refreshes:%d", loads, refreshes)
	}
}

func TestLRUCacheRejectsUniqueLoadsAboveLimit(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan string, 2)
	release := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(_ context.Context, key string) (int, error) {
		entered <- key
		<-release
		return len(key), nil
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity:           8,
		MaxConcurrentLoads: 2,
		Loader:             loader,
		Refresh:            func() RefreshConfig { return RefreshConfig{LoadTimeout: time.Minute} },
		Lifecycle:          lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lifecycle.Close)
	var workers conc.WaitGroup
	for _, key := range []string{"a", "bb"} {
		key := key
		workers.Go(func() { _, _, _ = c.Get(context.Background(), key) })
	}
	<-entered
	<-entered

	if _, _, err := c.Get(context.Background(), "ccc"); !errors.Is(err, ErrLoadLimitReached) {
		t.Fatalf("overflow Get error = %v, want %v", err, ErrLoadLimitReached)
	}
	canceledCause := errors.New("caller canceled before admission")
	canceledCtx, cancel := context.WithCancelCause(context.Background())
	cancel(canceledCause)
	if _, _, err := c.Get(canceledCtx, "dddd"); !errors.Is(err, canceledCause) {
		t.Fatalf("canceled overflow Get error = %v, want %v", err, canceledCause)
	}
	if got := loader.calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want 2", got)
	}
	close(release)
	workers.Wait()
}

func TestLRUCacheSameKeyFollowerJoinsWhenUniqueLoadLimitIsFull(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	release := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(context.Context, string) (int, error) {
		close(entered)
		<-release
		return 42, nil
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity:           4,
		MaxConcurrentLoads: 1,
		Loader:             loader,
		Refresh:            func() RefreshConfig { return RefreshConfig{LoadTimeout: time.Minute} },
		Lifecycle:          lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lifecycle.Close)
	result := make(chan error, 1)
	go func() {
		_, _, err := c.Get(context.Background(), "same")
		result <- err
	}()
	<-entered
	flight, leader, err := c.beginLoad("same")
	if err != nil || leader || flight == nil {
		t.Fatalf("same-key follower admission = (flight:%v leader:%v err:%v), want existing flight", flight != nil, leader, err)
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatalf("leader Get: %v", err)
	}
}

func TestLRUCacheLoadLimitCapacityReturnsAfterCompletion(t *testing.T) {
	lifecycle := NewLifecycle()
	releaseFirst := make(chan struct{})
	enteredFirst := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(_ context.Context, key string) (int, error) {
		if key == "first" {
			close(enteredFirst)
			<-releaseFirst
		}
		return len(key), nil
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity:           4,
		MaxConcurrentLoads: 1,
		Loader:             loader,
		Refresh:            func() RefreshConfig { return RefreshConfig{LoadTimeout: time.Minute} },
		Lifecycle:          lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lifecycle.Close)
	firstDone := make(chan error, 1)
	go func() {
		_, _, err := c.Get(context.Background(), "first")
		firstDone <- err
	}()
	<-enteredFirst
	if _, _, err := c.Get(context.Background(), "overflow"); !errors.Is(err, ErrLoadLimitReached) {
		t.Fatalf("overflow Get error = %v, want %v", err, ErrLoadLimitReached)
	}
	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if value, found, err := c.Get(context.Background(), "second"); err != nil || !found || value != len("second") {
		t.Fatalf("second Get after release = (%d, %v, %v)", value, found, err)
	}
}

func TestLRUCacheLifecycleCloseReleasesLoadLimitCapacity(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	loader := &stubLoader[string, int]{fn: func(ctx context.Context, _ string) (int, error) {
		close(entered)
		<-ctx.Done()
		return 0, context.Cause(ctx)
	}}
	c, err := NewLRUCache(Config[string, int]{
		Capacity:           4,
		MaxConcurrentLoads: 1,
		Loader:             loader,
		Refresh:            func() RefreshConfig { return RefreshConfig{LoadTimeout: time.Minute} },
		Lifecycle:          lifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := c.Get(context.Background(), "first")
		result <- err
	}()
	<-entered
	lifecycle.Close()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("Get after Lifecycle.Close = %v, want canceled", err)
	}
	c.loadMu.Lock()
	flights := len(c.loadFlights)
	c.loadMu.Unlock()
	if flights != 0 {
		t.Fatalf("load flights after Close = %d, want 0", flights)
	}
	if _, _, err := c.Get(context.Background(), "second"); !errors.Is(err, context.Canceled) {
		t.Fatalf("new Get after Lifecycle.Close = %v, want canceled", err)
	}
}

func TestLRUCacheDelayedMissUsesValueStoredBeforeFlightAdmission(t *testing.T) {
	c, err := NewLRUCache(Config[string, int]{Capacity: 4})
	if err != nil {
		t.Fatal(err)
	}
	c.store("same", 42)

	flight, leader, err := c.beginLoad("same")
	if err != nil {
		t.Fatal(err)
	}
	if leader {
		c.finishLoad("same", flight, loadOutcome[int]{value: 42, found: true})
		t.Fatal("delayed miss became a second load leader after value was stored")
	}
	select {
	case <-flight.done:
		if flight.outcome.err != nil || !flight.outcome.found || flight.outcome.value != 42 {
			t.Fatalf("completed flight = %+v, want cached value 42", flight.outcome)
		}
	default:
		t.Fatal("cached value did not produce a completed flight")
	}
}

func TestLRUCacheCallerOwnedColdMissPropagatesCallerCause(t *testing.T) {
	lifecycle := NewLifecycle()
	entered := make(chan struct{})
	loaderCanceled := make(chan error, 1)
	loader := &stubLoader[string, int]{fn: func(ctx context.Context, _ string) (int, error) {
		close(entered)
		<-ctx.Done()
		loaderCanceled <- context.Cause(ctx)
		return 0, context.Cause(ctx)
	}}
	c, err := NewLRUCache(Config[string, int]{Capacity: 4, Loader: loader, Lifecycle: lifecycle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lifecycle.Close)

	callerCtx, cancel := context.WithCancelCause(context.Background())
	callerCause := errors.New("caller canceled")
	result := make(chan error, 1)
	var worker conc.WaitGroup
	worker.Go(func() {
		_, _, err := c.Get(callerCtx, "same")
		result <- err
	})
	<-entered
	cancel(callerCause)
	select {
	case cause := <-loaderCanceled:
		if !errors.Is(cause, callerCause) {
			t.Fatalf("loader cause = %v, want %v", cause, callerCause)
		}
	case <-time.After(time.Second):
		t.Fatal("caller-owned loader did not observe caller cancellation")
	}
	worker.Wait()
	if err := <-result; !errors.Is(err, callerCause) {
		t.Fatalf("Get = %v, want %v", err, callerCause)
	}
	loads, refreshes := lifecycle.ResourceCounts()
	if loads != 0 || refreshes != 0 {
		t.Fatalf("caller-owned load registered lifecycle workers: loads:%d refreshes:%d", loads, refreshes)
	}
}

func TestLRUCache_SingleflightDeduplicates(t *testing.T) {
	gate := make(chan struct{})
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			<-gate
			return 7, nil
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 4,
		Loader:   loader,
	})

	const N = 50
	results := make([]int, N)
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			v, _, err := c.Get(context.Background(), "same")
			results[i], errs[i] = v, err
		}(i)
	}

	// 给 goroutine 时间排队，再放行 loader
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if loader.calls.Load() != 1 {
		t.Fatalf("singleflight should dedupe to 1 call, got %d", loader.calls.Load())
	}
	for i := 0; i < N; i++ {
		if errs[i] != nil || results[i] != 7 {
			t.Fatalf("worker %d: (%d, %v)", i, results[i], errs[i])
		}
	}
}

func TestLRUCache_SingleflightErrorPropagatedToAllWaiters(t *testing.T) {
	gate := make(chan struct{})
	want := fmt.Errorf("rpc fail")
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			<-gate
			return 0, want
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 4,
		Loader:   loader,
	})

	const N = 10
	errs := make([]error, N)
	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _, err := c.Get(context.Background(), "k")
			errs[i] = err
		}(i)
	}
	time.Sleep(20 * time.Millisecond)
	close(gate)
	wg.Wait()

	if loader.calls.Load() != 1 {
		t.Fatalf("singleflight should dedupe to 1 call, got %d", loader.calls.Load())
	}
	for i := 0; i < N; i++ {
		if !errors.Is(errs[i], want) {
			t.Fatalf("worker %d should observe rpc error, got %v", i, errs[i])
		}
	}
}

func TestLRUCache_NegativeCacheSuppressesReload(t *testing.T) {
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    4,
		Loader:      loader,
		NegativeTTL: 30 * time.Second,
	})

	// 首次 miss，loader 调用一次
	_, ok, err := c.Get(context.Background(), "k")
	if ok || !errors.Is(err, ErrNotFound) {
		t.Fatalf("first Get should be NotFound, got (ok=%v, err=%v)", ok, err)
	}
	if loader.calls.Load() != 1 {
		t.Fatalf("loader should be called once: %d", loader.calls.Load())
	}

	// TTL 内再来不应再次调用 loader
	for i := 0; i < 5; i++ {
		_, _, err := c.Get(context.Background(), "k")
		if !errors.Is(err, ErrNotFound) {
			t.Fatalf("subsequent Get should hit negative cache, got %v", err)
		}
	}
	if loader.calls.Load() != 1 {
		t.Fatalf("loader should not be re-called: %d", loader.calls.Load())
	}

	// 负缓存计入 NegativeHits
	if s := c.Stats(); s.NegativeHits < 5 {
		t.Fatalf("NegativeHits = %d, want >= 5", s.NegativeHits)
	}
}

func TestLRUCache_NegativeCacheExpires(t *testing.T) {
	now := time.Now()
	clock := &now
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    4,
		Loader:      loader,
		NegativeTTL: 30 * time.Second,
		Now:         func() time.Time { return *clock },
	})

	c.Get(context.Background(), "k") // 写入负缓存
	if loader.calls.Load() != 1 {
		t.Fatal("expect 1 call after first miss")
	}

	// 推进时钟到 31 秒后
	*clock = now.Add(31 * time.Second)
	c.Get(context.Background(), "k") // 应再次调 loader
	if loader.calls.Load() != 2 {
		t.Fatalf("after TTL expiry loader should be re-called: %d", loader.calls.Load())
	}
}

func TestLRUCache_NegativeCacheDisabledByZeroTTL(t *testing.T) {
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 4,
		Loader:   loader,
		// NegativeTTL: 0，禁用
	})

	for i := 0; i < 3; i++ {
		c.Get(context.Background(), "k")
	}
	if loader.calls.Load() != 3 {
		t.Fatalf("with negative TTL=0, loader should be called every time: %d", loader.calls.Load())
	}
}

func TestLRUCache_DeleteRemovesNegativeEntry(t *testing.T) {
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    4,
		Loader:      loader,
		NegativeTTL: 30 * time.Second,
	})

	c.Get(context.Background(), "k")
	if loader.calls.Load() != 1 {
		t.Fatal("expected 1 call")
	}

	// Apply(Delete) 抹掉负缓存
	c.Apply(ActionDelete, "k", 0)

	// 再次 Get 应重新调用 loader
	c.Get(context.Background(), "k")
	if loader.calls.Load() != 2 {
		t.Fatalf("after delete, loader should be re-called: %d", loader.calls.Load())
	}
}

func TestLRUCache_ExplicitRemoveDoesNotCountAsEviction(t *testing.T) {
	c, _ := NewLRUCache[string, int](Config[string, int]{Capacity: 4})
	c.Set("a", 1)
	c.Set("b", 2)

	// 显式删除——不应计入 Evictions
	c.Delete("a")
	c.Apply(ActionDelete, "b", 0)

	if s := c.Stats(); s.Evictions != 0 {
		t.Fatalf("explicit Remove must not increment Evictions, got %d", s.Evictions)
	}
}

func TestLRUCache_NegativeCacheExpiryDoesNotCountAsEviction(t *testing.T) {
	now := time.Now()
	clock := &now
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    4,
		Loader:      loader,
		NegativeTTL: 30 * time.Second,
		Now:         func() time.Time { return *clock },
	})

	c.Get(context.Background(), "k") // 写入负缓存
	*clock = now.Add(31 * time.Second)
	c.Get(context.Background(), "k") // TTL 过期 → cache.Remove + 重新拉

	// 仅 capacity 驱逐计数；TTL 过期清理不应该计入
	if s := c.Stats(); s.Evictions != 0 {
		t.Fatalf("TTL expiry remove must not increment Evictions, got %d", s.Evictions)
	}
}

func TestLRUCache_OnEvictFiresOnCapacityEviction(t *testing.T) {
	var evicted []string
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity: 2,
		OnEvict: func(k string, _ int) {
			evicted = append(evicted, k)
		},
	})
	c.Set("a", 1)
	c.Set("b", 2)
	c.Set("c", 3)
	if len(evicted) != 1 || evicted[0] != "a" {
		t.Fatalf("OnEvict events: %v", evicted)
	}
}

func TestLRUCache_OnEvictNotFiredForNegativeEntry(t *testing.T) {
	loader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	var evicted int
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    1,
		Loader:      loader,
		NegativeTTL: 30 * time.Second,
		OnEvict: func(_ string, _ int) {
			evicted++
		},
	})
	c.Get(context.Background(), "neg")
	c.Set("real", 1) // 把 neg 挤掉
	if evicted != 0 {
		t.Fatalf("OnEvict should not fire for negative entry eviction; got %d", evicted)
	}
}

func TestLRU_PositiveSetClearsNegative(t *testing.T) {
	notFoundLoader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    8,
		NegativeTTL: time.Hour,
		Loader:      notFoundLoader,
	})
	if _, ok, _ := c.Get(context.Background(), "k"); ok {
		t.Fatal("expected miss")
	}
	c.Set("k", 42)
	v, ok, _ := c.Get(context.Background(), "k")
	if !ok || v != 42 {
		t.Fatalf("Set should clear negative; got ok=%v v=%d", ok, v)
	}
}

func TestLRU_ApplySetClearsNegative(t *testing.T) {
	notFoundLoader := &stubLoader[string, int]{
		fn: func(_ context.Context, _ string) (int, error) {
			return 0, ErrNotFound
		},
	}
	c, _ := NewLRUCache[string, int](Config[string, int]{
		Capacity:    8,
		NegativeTTL: time.Hour,
		Loader:      notFoundLoader,
	})
	c.Get(context.Background(), "k")
	c.Apply(ActionSet, "k", 7)
	v, ok, _ := c.Get(context.Background(), "k")
	if !ok || v != 7 {
		t.Fatalf("Apply(Set) should clear negative; got ok=%v v=%d", ok, v)
	}
}

// errLoader 返回固定错误，触发 loadAndStore 的非 ErrNotFound 分支。
type errLoader struct{ err error }

func (l errLoader) Load(_ context.Context, _ uint) (string, error) { return "", l.err }

func TestLRU_LoadErrorsCounter(t *testing.T) {
	c, err := NewLRUCache(Config[uint, string]{
		Capacity:    4,
		Loader:      errLoader{err: errors.New("rpc down")},
		NegativeTTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, gerr := c.Get(context.Background(), uint(1)); gerr == nil {
		t.Fatal("expected loader error to propagate")
	}
	if got := c.Stats().LoadErrors; got != 1 {
		t.Fatalf("LoadErrors = %d, want 1", got)
	}
	c2, _ := NewLRUCache(Config[uint, string]{
		Capacity: 4, Loader: errLoader{err: ErrNotFound}, NegativeTTL: time.Minute,
	})
	_, _, _ = c2.Get(context.Background(), uint(1))
	if got := c2.Stats().LoadErrors; got != 0 {
		t.Fatalf("ErrNotFound should not count as LoadError, got %d", got)
	}
}

func TestLRU_InvalidationsCounter(t *testing.T) {
	c, _ := NewLRUCache(Config[uint, string]{Capacity: 4})
	c.Set(uint(1), "v")
	c.Delete(uint(1))
	c.Delete(uint(2)) // 删不存在的也计数（显式失效意图）
	if got := c.Stats().Invalidations; got != 2 {
		t.Fatalf("Invalidations = %d, want 2", got)
	}
}

func TestLRU_InvalidationsCounter_Apply(t *testing.T) {
	c, _ := NewLRUCache(Config[uint, string]{Capacity: 4})
	c.Set(1, "v")
	c.Apply(ActionDelete, 1, "")
	c.Apply(ActionDelete, 2, "") // 不存在的也计数（与 Delete 一致，意图驱动）
	if got := c.Stats().Invalidations; got != 2 {
		t.Fatalf("Apply(ActionDelete) Invalidations = %d, want 2", got)
	}
}
