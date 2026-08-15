package genericapi

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	relaylimiter "github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/stretchr/testify/require"
)

type terminalWaitStore struct {
	tryConcurrency func(relaylimiter.BucketKey) (func(), bool)
	wake           chan struct{}
	waiterAdded    chan struct{}
	addedOnce      sync.Once
}

func (s *terminalWaitStore) TryConcurrency(key relaylimiter.BucketKey, _ int64) (func(), bool) {
	return s.tryConcurrency(key)
}
func (*terminalWaitStore) TryRate(relaylimiter.BucketKey, int64, int) bool { return true }
func (*terminalWaitStore) TryRateBatch([]relaylimiter.RateRequest) (int, bool) {
	return -1, true
}
func (s *terminalWaitStore) WaitC(relaylimiter.BucketKey) <-chan struct{} { return s.wake }
func (s *terminalWaitStore) AddWaiter(relaylimiter.BucketKey, int) bool {
	s.addedOnce.Do(func() { close(s.waiterAdded) })
	return true
}
func (*terminalWaitStore) RemoveWaiter(relaylimiter.BucketKey) {}

func waitLimiterIndex(queueTimeMs int) *cache.LimiterIndex {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{{
		ID: 2, Name: "wait", Enabled: true, Metric: models.LimiterMetricConcurrency,
		Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait,
		QueueSize: 1, QueueTimeMs: queueTimeMs,
	}})
	idx.LoadBindings([]models.LimiterBinding{{
		ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
	}})
	return idx
}

func TestLimiterWaitCancelReturnsTypedQueuedFacts(t *testing.T) {
	store := &terminalWaitStore{
		tryConcurrency: func(relaylimiter.BucketKey) (func(), bool) { return func() {}, false },
		wake:           make(chan struct{}), waiterAdded: make(chan struct{}),
	}
	ctx, cancel := context.WithCancelCause(t.Context())
	cause := errors.New("client canceled queued request")
	done := make(chan error, 1)
	go func() {
		_, err := NewLimiterGate(waitLimiterIndex(1_000), store).Acquire(ctx, APIRequestFacts{APIServiceID: 7, RequestID: "cancel"})
		done <- err
	}()
	<-store.waiterAdded
	cancel(cause)
	err := <-done
	require.ErrorIs(t, err, cause)
	facts := rateLimitResult(err)
	require.Equal(t, "queued", facts.RateLimitDecision)
	require.GreaterOrEqual(t, facts.RateLimitWaitMs, 0)
	require.Len(t, facts.RateLimitHits, 1)
	require.Equal(t, "queued", facts.RateLimitHits[0].Decision)
}

func TestWebSocketLimiterWaitRejectsImmediately(t *testing.T) {
	store := &terminalWaitStore{
		tryConcurrency: func(relaylimiter.BucketKey) (func(), bool) { return func() {}, false },
		wake:           make(chan struct{}), waiterAdded: make(chan struct{}),
	}
	startedAt := time.Now()
	permit, err := NewLimiterGate(waitLimiterIndex(5_000), store).Acquire(t.Context(), APIRequestFacts{
		APIServiceID: 7, RequestID: "websocket-no-wait", NoWait: true,
	})
	require.Nil(t, permit)
	require.ErrorIs(t, err, ErrAPIRateLimited)
	require.Less(t, time.Since(startedAt), 100*time.Millisecond)
	select {
	case <-store.waiterAdded:
		t.Fatal("WebSocket limiter registered a waiter")
	default:
	}
	facts := rateLimitResult(err)
	require.Equal(t, "rejected", facts.RateLimitDecision)
	require.Zero(t, facts.RateLimitWaitMs)
}

func TestLimiterWaitRetryRejectReturnsTypedAccumulatedFacts(t *testing.T) {
	rejectCalls := 0
	wake := make(chan struct{})
	close(wake)
	store := &terminalWaitStore{
		tryConcurrency: func(key relaylimiter.BucketKey) (func(), bool) {
			if key.LimiterID == 1 {
				rejectCalls++
				if rejectCalls == 1 {
					return func() {}, true
				}
				return func() {}, false
			}
			return func() {}, false
		},
		wake: wake, waiterAdded: make(chan struct{}),
	}
	idx := waitLimiterIndex(1_000)
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "late-reject", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
		{ID: 2, Name: "wait", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 1_000},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
	})

	permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "retry-reject"})
	require.Nil(t, permit)
	require.ErrorIs(t, err, ErrAPIRateLimited)
	facts := rateLimitResult(err)
	require.Equal(t, "rejected", facts.RateLimitDecision)
	require.Len(t, facts.RateLimitHits, 1)
	require.Equal(t, uint(1), facts.RateLimitHits[0].LimiterID)
	require.Equal(t, "rejected", facts.RateLimitHits[0].Decision)
}

func TestLimiterWaitTimeoutReturnsWaitDurationInTypedFacts(t *testing.T) {
	store := &terminalWaitStore{
		tryConcurrency: func(relaylimiter.BucketKey) (func(), bool) { return func() {}, false },
		wake:           make(chan struct{}), waiterAdded: make(chan struct{}),
	}
	permit, err := NewLimiterGate(waitLimiterIndex(5), store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "timeout"})
	require.Nil(t, permit)
	require.ErrorIs(t, err, ErrAPIRateLimited)
	facts := rateLimitResult(err)
	require.Equal(t, "rejected", facts.RateLimitDecision)
	require.GreaterOrEqual(t, facts.RateLimitWaitMs, 1)
	require.Len(t, facts.RateLimitHits, 1)
	require.GreaterOrEqual(t, facts.RateLimitHits[0].WaitMs, 1)
}

func TestAPILimiterRateBatchRejectsWithoutPartialCommit(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "first-rate", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
		{ID: 2, Name: "full-rate", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 1, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
	})
	store := relaylimiter.NewMemStore()
	fullKey := relaylimiter.BucketKey{LimiterID: 2, Bucket: "api_service:7:shared"}
	require.True(t, store.TryRate(fullKey, 1, 60_000))

	permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "atomic-reject"})
	require.ErrorIs(t, err, ErrAPIRateLimited)
	require.Nil(t, permit)
	usage := make(map[relaylimiter.BucketKey]int64)
	for _, bucket := range store.SnapshotBuckets() {
		usage[bucket.Key] = bucket.Occupied
	}
	require.Zero(t, usage[relaylimiter.BucketKey{LimiterID: 1, Bucket: "api_service:7:shared"}], "failed batch must not increment an earlier rate bucket")
	require.Equal(t, int64(1), usage[fullKey])
}

func TestLimiterWaitDoesNotDoubleCommitRate(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "rate", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
		{ID: 2, Name: "waiting-concurrency", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 1_000},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
	})
	store := relaylimiter.NewMemStore()
	concurrencyKey := relaylimiter.BucketKey{LimiterID: 2, Bucket: "api_service:7:shared"}
	release, ok := store.TryConcurrency(concurrencyKey, 1)
	require.True(t, ok)

	type acquireResult struct {
		permit APIPermit
		err    error
	}
	resultC := make(chan acquireResult, 1)
	go func() {
		permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "wait-once"})
		resultC <- acquireResult{permit: permit, err: err}
	}()
	require.Eventually(t, func() bool {
		for _, bucket := range store.SnapshotBuckets() {
			if bucket.Key == concurrencyKey && bucket.Waiters == 1 {
				return true
			}
		}
		return false
	}, time.Second, time.Millisecond)
	release()

	result := <-resultC
	require.NoError(t, result.err)
	require.NotNil(t, result.permit)
	result.permit.Release()
	rateKey := relaylimiter.BucketKey{LimiterID: 1, Bucket: "api_service:7:shared"}
	for _, bucket := range store.SnapshotBuckets() {
		if bucket.Key == rateKey {
			require.Equal(t, int64(1), bucket.Occupied, "one request must commit its rate batch once after concurrency admission")
			return
		}
	}
	t.Fatal("rate bucket was not committed")
}

func TestLimiterRateWaitsForWindowThenCommitsExactlyOnce(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{{
		ID: 1, Name: "rate-wait", Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 1, WindowMs: 20, KeyBy: models.LimiterKeyShared,
		Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 250,
	}})
	idx.LoadBindings([]models.LimiterBinding{{
		ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
	}})
	store := relaylimiter.NewMemStore()
	gate := NewLimiterGate(idx, store)

	first, err := gate.Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "rate-first"})
	require.NoError(t, err)
	first.Release()
	second, err := gate.Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "rate-second"})
	require.NoError(t, err)
	require.NotNil(t, second)
	facts := rateLimitResult(second)
	require.Equal(t, "queued", facts.RateLimitDecision)
	require.GreaterOrEqual(t, facts.RateLimitWaitMs, 1)
	require.Len(t, facts.RateLimitHits, 1)
	require.Equal(t, "queued", facts.RateLimitHits[0].Decision)
	second.Release()

	rateKey := relaylimiter.BucketKey{LimiterID: 1, Bucket: "api_service:7:shared"}
	for _, bucket := range store.SnapshotBuckets() {
		if bucket.Key == rateKey {
			require.Equal(t, int64(1), bucket.Occupied, "the successful retry must commit the rate batch once")
			return
		}
	}
	t.Fatal("rate bucket was not committed")
}

func TestLimiterRateWaitTerminalFacts(t *testing.T) {
	newGate := func(queueSize, queueTimeMs, windowMs int) (*LimiterGate, *relaylimiter.MemStore) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 1, Name: "rate-wait", Enabled: true, Metric: models.LimiterMetricRate,
			Capacity: 1, WindowMs: windowMs, KeyBy: models.LimiterKeyShared,
			Action: models.LimiterActionWait, QueueSize: queueSize, QueueTimeMs: queueTimeMs,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		}})
		store := relaylimiter.NewMemStore()
		return NewLimiterGate(idx, store), store
	}
	facts := APIRequestFacts{APIServiceID: 7, RequestID: "rate-terminal"}

	t.Run("timeout returns rejected wait facts", func(t *testing.T) {
		gate, _ := newGate(1, 5, 1_000)
		first, err := gate.Acquire(t.Context(), facts)
		require.NoError(t, err)
		first.Release()
		permit, err := gate.Acquire(t.Context(), facts)
		require.Nil(t, permit)
		require.ErrorIs(t, err, ErrAPIRateLimited)
		result := rateLimitResult(err)
		require.Equal(t, "rejected", result.RateLimitDecision)
		require.GreaterOrEqual(t, result.RateLimitWaitMs, 1)
		require.Len(t, result.RateLimitHits, 1)
		require.Equal(t, uint(1), result.RateLimitHits[0].LimiterID)
		require.Equal(t, "rejected", result.RateLimitHits[0].Decision)
		require.GreaterOrEqual(t, result.RateLimitHits[0].WaitMs, 1)
	})

	t.Run("cancel returns queued facts and original cause", func(t *testing.T) {
		gate, store := newGate(1, 1_000, 1_000)
		first, err := gate.Acquire(t.Context(), facts)
		require.NoError(t, err)
		first.Release()
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("cancel rate wait")
		done := make(chan error, 1)
		go func() {
			_, acquireErr := gate.Acquire(ctx, facts)
			done <- acquireErr
		}()
		require.Eventually(t, func() bool {
			for _, bucket := range store.SnapshotBuckets() {
				if bucket.Key.LimiterID == 1 && bucket.Waiters == 1 {
					return true
				}
			}
			return false
		}, time.Second, time.Millisecond)
		cancel(cause)
		err = <-done
		require.ErrorIs(t, err, cause)
		result := rateLimitResult(err)
		require.Equal(t, "queued", result.RateLimitDecision)
		require.Len(t, result.RateLimitHits, 1)
		require.Equal(t, "queued", result.RateLimitHits[0].Decision)
	})

	t.Run("queue full rejects without committing", func(t *testing.T) {
		gate, store := newGate(0, 1_000, 1_000)
		first, err := gate.Acquire(t.Context(), facts)
		require.NoError(t, err)
		first.Release()
		permit, err := gate.Acquire(t.Context(), facts)
		require.Nil(t, permit)
		require.ErrorIs(t, err, ErrAPIRateLimited)
		result := rateLimitResult(err)
		require.Equal(t, "rejected", result.RateLimitDecision)
		require.Len(t, result.RateLimitHits, 1)
		require.Equal(t, "rejected", result.RateLimitHits[0].Decision)
		for _, bucket := range store.SnapshotBuckets() {
			if bucket.Key.LimiterID == 1 {
				require.Equal(t, int64(1), bucket.Occupied)
			}
		}
	})
}

type rateWaitThenRejectStore struct {
	concurrencyCalls atomic.Int32
	releases         atomic.Int32
	rateCalls        atomic.Int32
}

func (s *rateWaitThenRejectStore) TryConcurrency(relaylimiter.BucketKey, int64) (func(), bool) {
	if s.concurrencyCalls.Add(1) > 1 {
		return func() {}, false
	}
	return func() { s.releases.Add(1) }, true
}
func (*rateWaitThenRejectStore) TryRate(relaylimiter.BucketKey, int64, int) bool { return true }
func (s *rateWaitThenRejectStore) TryRateBatch([]relaylimiter.RateRequest) (int, bool) {
	s.rateCalls.Add(1)
	return 0, false
}
func (*rateWaitThenRejectStore) WaitC(relaylimiter.BucketKey) <-chan struct{} {
	return make(chan struct{})
}
func (*rateWaitThenRejectStore) AddWaiter(relaylimiter.BucketKey, int) bool { return true }
func (*rateWaitThenRejectStore) RemoveWaiter(relaylimiter.BucketKey)        {}

func TestLimiterRateWaitRetryCanTerminateOnRejectLimiter(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "late-reject", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
		{ID: 2, Name: "rate-wait", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 1, WindowMs: 1_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 250},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
	})
	store := &rateWaitThenRejectStore{}
	permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "rate-then-reject"})
	require.Nil(t, permit)
	require.ErrorIs(t, err, ErrAPIRateLimited)
	result := rateLimitResult(err)
	require.Equal(t, "rejected", result.RateLimitDecision)
	require.GreaterOrEqual(t, result.RateLimitWaitMs, 1)
	require.Len(t, result.RateLimitHits, 1)
	require.Equal(t, uint(1), result.RateLimitHits[0].LimiterID)
	require.Equal(t, "rejected", result.RateLimitHits[0].Decision)
	require.Equal(t, int32(1), store.rateCalls.Load(), "the failed rate batch must not be retried after a reject limiter fails")
	require.Equal(t, int32(1), store.releases.Load(), "the initial rate failure must release admitted concurrency")
}

type failingAPIPermitStore struct {
	calls    []uint
	released []uint
	failID   uint
}

type queuedAPIPermitStore struct{ attempts atomic.Int32 }

func (s *queuedAPIPermitStore) TryConcurrency(relaylimiter.BucketKey, int64) (func(), bool) {
	if s.attempts.Add(1) == 1 {
		return nil, false
	}
	return func() {}, true
}
func (*queuedAPIPermitStore) TryRate(relaylimiter.BucketKey, int64, int) bool     { return true }
func (*queuedAPIPermitStore) TryRateBatch([]relaylimiter.RateRequest) (int, bool) { return -1, true }
func (*queuedAPIPermitStore) WaitC(relaylimiter.BucketKey) <-chan struct{} {
	ready := make(chan struct{})
	close(ready)
	return ready
}
func (*queuedAPIPermitStore) AddWaiter(relaylimiter.BucketKey, int) bool { return true }
func (*queuedAPIPermitStore) RemoveWaiter(relaylimiter.BucketKey)        {}

func TestLimiterWaitReportsQueuedDecisionAfterOneAdmissionRetry(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{{
		ID: 1, Name: "queued", Enabled: true, Metric: models.LimiterMetricConcurrency,
		Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 100,
	}})
	idx.LoadBindings([]models.LimiterBinding{{
		ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
	}})
	store := &queuedAPIPermitStore{}

	permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, APIRouteID: 9, RequestID: "queued"})
	require.NoError(t, err)
	require.NotNil(t, permit)
	facts := rateLimitResult(permit)
	require.Equal(t, "queued", facts.RateLimitDecision)
	require.Len(t, facts.RateLimitHits, 1)
	require.Equal(t, "queued", facts.RateLimitHits[0].Decision)
	require.Equal(t, int32(2), store.attempts.Load())
	permit.Release()
}

func TestMergeRateLimitResultSortsBoundsAndPreservesTotal(t *testing.T) {
	target := apiattempt.APIExecutionResult{RateLimitHits: make([]models.RateLimitHit, 40)}
	source := apiattempt.APIExecutionResult{RateLimitHits: make([]models.RateLimitHit, 40)}
	for index := range target.RateLimitHits {
		target.RateLimitHits[index] = validGenericRateHit(uint(80 - index))
		source.RateLimitHits[index] = validGenericRateHit(uint(40 - index))
	}
	mergeRateLimitResult(&target, source)
	require.Len(t, target.RateLimitHits, 64)
	for index := 1; index < len(target.RateLimitHits); index++ {
		require.LessOrEqual(t, target.RateLimitHits[index-1].LimiterID, target.RateLimitHits[index].LimiterID)
	}
	raw, err := json.Marshal(target)
	require.NoError(t, err)
	var facts struct {
		Total     int  `json:"rate_limit_hit_total"`
		Truncated bool `json:"rate_limit_hits_truncated"`
	}
	require.NoError(t, json.Unmarshal(raw, &facts))
	require.Equal(t, 80, facts.Total)
	require.True(t, facts.Truncated)
}

func TestResolvedSourceLimiterBucketUsesWinnerTarget(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "global", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 10, WindowMs: 60_000, KeyBy: models.LimiterKeyPerUser},
		{ID: 2, Name: "group", Enabled: true, Metric: models.LimiterMetricRate, Capacity: 20, WindowMs: 60_000, KeyBy: models.LimiterKeyPerUser},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetGlobal, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetUserGroup, TargetID: 7, Enabled: true},
	})
	values := idx.EffectiveSourceAPILimiters(5, 7, 70, 90)
	require.Len(t, values, 1)
	key := limiterBucket(values[0], APIRequestFacts{UserID: 5, GroupID: 7})
	require.Equal(t, "user_group:7:u:5", key.Bucket)
}

func validGenericRateHit(id uint) models.RateLimitHit {
	return models.RateLimitHit{
		LimiterID: id, Name: "limiter", Dimension: "rate/shared",
		Bucket: "global:0:shared", Decision: "allow",
	}
}

func (s *failingAPIPermitStore) TryConcurrency(key relaylimiter.BucketKey, _ int64) (func(), bool) {
	s.calls = append(s.calls, key.LimiterID)
	if key.LimiterID == s.failID {
		return func() {}, false
	}
	return func() { s.released = append(s.released, key.LimiterID) }, true
}
func (*failingAPIPermitStore) TryRate(relaylimiter.BucketKey, int64, int) bool     { return true }
func (*failingAPIPermitStore) TryRateBatch([]relaylimiter.RateRequest) (int, bool) { return -1, true }
func (*failingAPIPermitStore) WaitC(relaylimiter.BucketKey) <-chan struct{} {
	return make(chan struct{})
}
func (*failingAPIPermitStore) AddWaiter(relaylimiter.BucketKey, int) bool { return false }
func (*failingAPIPermitStore) RemoveWaiter(relaylimiter.BucketKey)        {}

func TestLimiterAPIServiceRouteUpstreamBindingsAllGate(t *testing.T) {
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{
		{ID: 1, Name: "service", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
		{ID: 2, Name: "route", Enabled: true, Metric: models.LimiterMetricConcurrency, Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject},
	})
	idx.LoadBindings([]models.LimiterBinding{
		{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true},
		{ID: 2, LimiterID: 2, TargetType: models.LimiterTargetAPIRoute, TargetID: 9, Enabled: true},
	})
	store := &failingAPIPermitStore{failID: 2}
	gate := NewLimiterGate(idx, store)

	permit, err := gate.Acquire(context.Background(), APIRequestFacts{UserID: 5, GroupID: 3, APIServiceID: 7, APIRouteID: 9, RequestID: "request"})
	require.ErrorIs(t, err, ErrAPIRateLimited)
	require.Nil(t, permit)
	require.Equal(t, []uint{1, 2}, store.calls)
	require.Equal(t, []uint{1}, store.released, "failure must release every already-held concurrency permit")
	facts := rateLimitResult(err)
	require.Equal(t, "rejected", facts.RateLimitDecision)
	require.Len(t, facts.RateLimitHits, 2)
	require.Equal(t, "allow", facts.RateLimitHits[0].Decision)
	require.Equal(t, "rejected", facts.RateLimitHits[1].Decision)
	require.Equal(t, "api_route:9:shared", facts.RateLimitHits[1].Bucket)
}

func TestAPILimiterBindingRejectsAndHotPathIgnoresChannelScope(t *testing.T) {
	limiter := models.RequestLimiter{KeyBy: models.LimiterKeyShared, ChannelScope: models.LimiterScopePrivate}
	require.False(t, models.ValidAPILimiterBinding(limiter, models.LimiterTargetAPIService))
	limiter.ChannelScope = ""
	require.True(t, models.ValidAPILimiterBinding(limiter, models.LimiterTargetAPIService))

	idx := cache.NewLimiterIndex()
	limiter.ID, limiter.Name, limiter.Enabled = 1, "legacy-dirty", true
	limiter.Metric, limiter.Capacity, limiter.Action = models.LimiterMetricConcurrency, 1, models.LimiterActionReject
	limiter.ChannelScope = models.LimiterScopePrivate
	idx.LoadLimiters([]models.RequestLimiter{limiter})
	idx.LoadBindings([]models.LimiterBinding{{ID: 1, LimiterID: 1, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true}})
	store := &failingAPIPermitStore{}
	permit, err := NewLimiterGate(idx, store).Acquire(t.Context(), APIRequestFacts{APIServiceID: 7, APIRouteID: 9, RequestID: "request"})
	require.NoError(t, err)
	require.NotNil(t, permit)
	permit.Release()
	require.Equal(t, []uint{1}, store.calls, "API hot path must not apply LLM ChannelScope filtering")
	facts := rateLimitResult(permit)
	require.Equal(t, "allow", facts.RateLimitDecision)
	require.Len(t, facts.RateLimitHits, 1)
}

func requireWireSafeLegacyLimiterResult(
	t *testing.T,
	result apiattempt.APIExecutionResult,
	wantID uint,
	wantName string,
	wantDecision string,
) {
	t.Helper()
	result.ProviderDispatchKnown = true
	require.Len(t, result.RateLimitHits, 1)
	require.Equal(t, wantID, result.RateLimitHits[0].LimiterID)
	require.Equal(t, wantName, result.RateLimitHits[0].Name)
	require.Equal(t, wantDecision, result.RateLimitHits[0].Decision)
	require.NoError(t, result.Validate())
	encoded, err := apiattempt.EncodeResultJSONWithin(result, 64<<10)
	require.NoError(t, err)
	decoded, err := apiattempt.DecodeResultJSONWithin(encoded, 64<<10)
	require.NoError(t, err)
	require.Equal(t, wantName, decoded.RateLimitHits[0].Name)
}

func TestLimiterGateUsesWireSafeNamesForEveryGenericAPIHitPath(t *testing.T) {
	legacyName := strings.Repeat("n", models.MaxRateLimitHitNameBytes+1)

	t.Run("source concurrency allow preserves ID and uses stable fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 41, Name: legacyName, Enabled: true, Metric: models.LimiterMetricConcurrency,
			Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 41, LimiterID: 41, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		}})

		permit, err := NewLimiterGate(idx, &failingAPIPermitStore{}).Acquire(
			t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "legacy-source-allow"},
		)

		require.NoError(t, err)
		require.NotNil(t, permit)
		defer permit.Release()
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(permit), 41, "limiter-41", "allow")
	})

	t.Run("source concurrency reject uses fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 42, Name: legacyName, Enabled: true, Metric: models.LimiterMetricConcurrency,
			Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 42, LimiterID: 42, TargetType: models.LimiterTargetAPIRoute, TargetID: 9, Enabled: true,
		}})

		permit, err := NewLimiterGate(idx, &failingAPIPermitStore{failID: 42}).Acquire(
			t.Context(), APIRequestFacts{APIServiceID: 7, APIRouteID: 9, RequestID: "legacy-source-reject"},
		)

		require.Nil(t, permit)
		require.ErrorIs(t, err, ErrAPIRateLimited)
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(err), 42, "limiter-42", "rejected")
	})

	t.Run("source concurrency queued success uses fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 43, Name: legacyName, Enabled: true, Metric: models.LimiterMetricConcurrency,
			Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 100,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 43, LimiterID: 43, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		}})

		permit, err := NewLimiterGate(idx, &queuedAPIPermitStore{}).Acquire(
			t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "legacy-source-queued"},
		)

		require.NoError(t, err)
		require.NotNil(t, permit)
		defer permit.Release()
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(permit), 43, "limiter-43", "queued")
	})

	t.Run("source queued terminal error uses fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 44, Name: legacyName, Enabled: true, Metric: models.LimiterMetricConcurrency,
			Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionWait, QueueSize: 1, QueueTimeMs: 1_000,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 44, LimiterID: 44, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		}})
		store := &terminalWaitStore{
			tryConcurrency: func(relaylimiter.BucketKey) (func(), bool) { return func() {}, false },
			wake:           make(chan struct{}), waiterAdded: make(chan struct{}),
		}
		ctx, cancel := context.WithCancelCause(t.Context())
		cause := errors.New("cancel legacy queued request")
		done := make(chan error, 1)
		go func() {
			_, err := NewLimiterGate(idx, store).Acquire(ctx, APIRequestFacts{APIServiceID: 7, RequestID: "legacy-source-cancel"})
			done <- err
		}()
		<-store.waiterAdded
		cancel(cause)
		err := <-done

		require.ErrorIs(t, err, cause)
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(err), 44, "limiter-44", "queued")
	})

	t.Run("upstream rate allow uses fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 45, Name: legacyName, Enabled: true, Metric: models.LimiterMetricRate,
			Capacity: 1, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 45, LimiterID: 45, TargetType: models.LimiterTargetAPIUpstream, TargetID: 11, Enabled: true,
		}})

		permit, err := NewLimiterGate(idx, relaylimiter.NewMemStore()).Acquire(
			t.Context(), APIRequestFacts{APIUpstreamID: 11, RequestID: "legacy-upstream-allow"},
		)

		require.NoError(t, err)
		require.NotNil(t, permit)
		defer permit.Release()
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(permit), 45, "limiter-45", "allow")
	})

	t.Run("upstream rate reject uses fallback", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 46, Name: legacyName, Enabled: true, Metric: models.LimiterMetricRate,
			Capacity: 1, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 46, LimiterID: 46, TargetType: models.LimiterTargetAPIUpstream, TargetID: 11, Enabled: true,
		}})
		store := relaylimiter.NewMemStore()
		require.True(t, store.TryRate(relaylimiter.BucketKey{LimiterID: 46, Bucket: "api_upstream:11:shared"}, 1, 60_000))

		permit, err := NewLimiterGate(idx, store).Acquire(
			t.Context(), APIRequestFacts{APIUpstreamID: 11, RequestID: "legacy-upstream-reject"},
		)

		require.Nil(t, permit)
		require.ErrorIs(t, err, ErrAPIRateLimited)
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(err), 46, "limiter-46", "rejected")
	})

	for _, test := range []struct {
		name       string
		legacyName string
	}{
		{name: "leading whitespace", legacyName: " legacy"},
		{name: "control character", legacyName: "legacy\nname"},
		{name: "invalid UTF-8", legacyName: string([]byte{0xff})},
	} {
		t.Run("fallback rejects "+test.name, func(t *testing.T) {
			idx := cache.NewLimiterIndex()
			idx.LoadLimiters([]models.RequestLimiter{{
				ID: 47, Name: test.legacyName, Enabled: true, Metric: models.LimiterMetricConcurrency,
				Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
			}})
			idx.LoadBindings([]models.LimiterBinding{{
				ID: 47, LimiterID: 47, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
			}})
			permit, err := NewLimiterGate(idx, &failingAPIPermitStore{}).Acquire(
				t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "legacy-invalid-class"},
			)
			require.NoError(t, err)
			defer permit.Release()
			requireWireSafeLegacyLimiterResult(t, rateLimitResult(permit), 47, "limiter-47", "allow")
		})
	}

	t.Run("valid existing name remains unchanged", func(t *testing.T) {
		idx := cache.NewLimiterIndex()
		idx.LoadLimiters([]models.RequestLimiter{{
			ID: 48, Name: "existing-valid-name", Enabled: true, Metric: models.LimiterMetricConcurrency,
			Capacity: 1, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}})
		idx.LoadBindings([]models.LimiterBinding{{
			ID: 48, LimiterID: 48, TargetType: models.LimiterTargetAPIService, TargetID: 7, Enabled: true,
		}})
		permit, err := NewLimiterGate(idx, &failingAPIPermitStore{}).Acquire(
			t.Context(), APIRequestFacts{APIServiceID: 7, RequestID: "valid-existing-name"},
		)
		require.NoError(t, err)
		defer permit.Release()
		requireWireSafeLegacyLimiterResult(t, rateLimitResult(permit), 48, "existing-valid-name", "allow")
	})
}
