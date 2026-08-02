package model_marketplace

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"golang.org/x/sync/singleflight"
)

const (
	MarketplacePerformanceHours = dao.MarketplacePerformanceHours
	performanceRefreshInterval  = time.Hour
	performanceFailureBackoff   = time.Minute
	performanceLoadTimeout      = 30 * time.Second
)

var errPerformanceFlightNoWaiters = errors.New("model performance refresh has no waiters")

type PerformanceComponents = dao.PerformanceComponents
type PerformanceHistogram = dao.PerformanceHistogram
type HourlyPerformanceComponents = dao.HourlyPerformanceComponents

type PerformanceStatus string

const (
	PerformanceAvailable   PerformanceStatus = "available"
	PerformanceStale       PerformanceStatus = "stale"
	PerformanceUnavailable PerformanceStatus = "unavailable"
)

// GlobalOfferPerformanceSnapshot intentionally stores only reprojectable raw
// hourly components. Viewer visibility, health thresholds, and DTOs are built
// on every Finder call and are never cached here.
type GlobalOfferPerformanceSnapshot struct {
	HourlyTrendComponents30D []HourlyPerformanceComponents `json:"-"`
}

// GlobalModelPerformanceSnapshot is immutable once published through the
// cache. Get always returns another deep copy, so callers cannot mutate it.
type GlobalModelPerformanceSnapshot struct {
	RefreshedAt          time.Time                                             `json:"-"`
	ObservedUntil        time.Time                                             `json:"-"`
	LastRefreshAttemptAt time.Time                                             `json:"-"`
	Offers               map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot `json:"-"`
}

type PerformanceSnapshotLoader interface {
	Load(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error)
}

type PerformanceSnapshotLoaderFunc func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error)

func (f PerformanceSnapshotLoaderFunc) Load(ctx context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
	return f(ctx, observedUntil)
}

type Clock interface {
	Now() time.Time
}

type systemPerformanceClock struct{}

func (systemPerformanceClock) Now() time.Time { return time.Now() }

type performanceCacheState struct {
	snapshot         *GlobalModelPerformanceSnapshot
	attemptStartedAt time.Time
	retryAfter       time.Time
	err              error
}

type performanceLoadFlight struct {
	ctx              context.Context
	cancel           context.CancelCauseFunc
	timeoutCancel    context.CancelFunc
	key              string
	attemptStartedAt time.Time
	waiters          int
	finished         bool
}

// GlobalModelPerformanceCache permanently retains the last successful value.
// One atomic state keeps the snapshot, failure, and retry boundary in the same
// generation. Shared loads belong to the process lifecycle rather than to an
// arbitrary request that happened to arrive first.
type GlobalModelPerformanceCache struct {
	state       atomic.Pointer[performanceCacheState]
	group       singleflight.Group
	clock       Clock
	loader      PerformanceSnapshotLoader
	lifecycle   context.Context
	loadTimeout time.Duration
	flightMu    sync.Mutex
	flight      *performanceLoadFlight
	flightSeq   uint64
	// afterStateLoad is a deterministic package-test barrier for proving that
	// one Get never combines fields from different immutable state generations.
	afterStateLoad func()
}

func NewGlobalModelPerformanceCache(loader PerformanceSnapshotLoader) *GlobalModelPerformanceCache {
	return newGlobalModelPerformanceCache(context.Background(), performanceLoadTimeout, systemPerformanceClock{}, loader)
}

func NewGlobalModelPerformanceCacheWithLifecycle(
	lifecycle context.Context,
	loader PerformanceSnapshotLoader,
) *GlobalModelPerformanceCache {
	return newGlobalModelPerformanceCache(lifecycle, performanceLoadTimeout, systemPerformanceClock{}, loader)
}

func newGlobalModelPerformanceCache(
	lifecycle context.Context,
	loadTimeout time.Duration,
	clock Clock,
	loader PerformanceSnapshotLoader,
) *GlobalModelPerformanceCache {
	if lifecycle == nil {
		lifecycle = context.Background()
	}
	if loadTimeout <= 0 {
		loadTimeout = performanceLoadTimeout
	}
	if clock == nil {
		clock = systemPerformanceClock{}
	}
	return &GlobalModelPerformanceCache{
		clock: clock, loader: loader, lifecycle: lifecycle, loadTimeout: loadTimeout,
	}
}

// Get blocks on cold load and hourly refresh. A failed refresh never replaces
// current: callers receive stale data (or unavailable when cold) plus the error
// strictly for diagnostics.
func (c *GlobalModelPerformanceCache) Get(
	ctx context.Context,
	now time.Time,
) (*GlobalModelPerformanceSnapshot, PerformanceStatus, error) {
	if c == nil {
		return nil, PerformanceUnavailable, errors.New("model performance cache is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, PerformanceUnavailable, err
	}
	if err := c.lifecycle.Err(); err != nil {
		return nil, PerformanceUnavailable, performanceContextCause(c.lifecycle)
	}
	if now.IsZero() {
		now = c.clock.Now()
	}
	now = now.UTC()
	state := c.state.Load()
	if c.afterStateLoad != nil {
		c.afterStateLoad()
	}
	if performanceStateFresh(state, now) || performanceFailureSuppressed(state, now) {
		return performanceResultFromState(state)
	}

	flight, err := c.joinPerformanceFlight(now)
	if err != nil {
		return nil, PerformanceUnavailable, err
	}
	defer c.leavePerformanceFlight(flight)
	result := c.group.DoChan(flight.key, func() (any, error) {
		defer c.finishPerformanceFlight(flight)
		return c.refreshSnapshotInsideFlight(flight)
	})
	select {
	case <-ctx.Done():
		return nil, PerformanceUnavailable, ctx.Err()
	case <-c.lifecycle.Done():
		return nil, PerformanceUnavailable, performanceContextCause(c.lifecycle)
	case loaded := <-result:
		if loaded.Val == nil {
			if loaded.Err != nil {
				return nil, PerformanceUnavailable, loaded.Err
			}
			return nil, PerformanceUnavailable, errors.New("model performance refresh returned no state")
		}
		loadedState, ok := loaded.Val.(*performanceCacheState)
		if !ok {
			return nil, PerformanceUnavailable, fmt.Errorf("model performance refresh returned %T", loaded.Val)
		}
		return performanceResultFromState(loadedState)
	}
}

// refreshSnapshotInsideFlight repeats all freshness/backoff checks because a
// caller may have passed the outer check just before another flight published.
func (c *GlobalModelPerformanceCache) refreshSnapshotInsideFlight(
	flight *performanceLoadFlight,
) (*performanceCacheState, error) {
	state := c.state.Load()
	if performanceStateFresh(state, flight.attemptStartedAt) {
		return state, nil
	}
	if performanceFailureSuppressed(state, flight.attemptStartedAt) {
		return state, state.err
	}
	if c.loader == nil {
		return c.completePerformanceFailure(flight, state, errors.New("model performance snapshot loader is required"))
	}
	loaded, err := c.loader.Load(flight.ctx, flight.attemptStartedAt)
	if err != nil {
		return c.completePerformanceFailure(flight, state, err)
	}
	if err := flight.ctx.Err(); err != nil {
		return c.completePerformanceFailure(flight, state, performanceContextCause(flight.ctx))
	}
	if loaded == nil {
		return c.completePerformanceFailure(flight, state, errors.New("model performance snapshot loader returned nil"))
	}
	published := cloneGlobalModelPerformanceSnapshot(loaded)
	published.RefreshedAt = flight.attemptStartedAt
	published.ObservedUntil = flight.attemptStartedAt
	published.LastRefreshAttemptAt = flight.attemptStartedAt
	next := &performanceCacheState{snapshot: published, attemptStartedAt: flight.attemptStartedAt}
	if !c.publishPerformanceState(flight, next, false) {
		return nil, performanceUnpublishedFlightError(flight)
	}
	return next, nil
}

func (c *GlobalModelPerformanceCache) completePerformanceFailure(
	flight *performanceLoadFlight,
	previous *performanceCacheState,
	loadErr error,
) (*performanceCacheState, error) {
	if flight.ctx.Err() != nil && c.performanceFlightCanceledWithoutBackendFailure(flight) {
		return nil, performanceContextCause(flight.ctx)
	}
	failedAt := c.clock.Now().UTC()
	next := &performanceCacheState{
		attemptStartedAt: flight.attemptStartedAt,
		retryAfter:       failedAt.Add(performanceFailureBackoff),
		err:              loadErr,
	}
	if previous != nil {
		next.snapshot = previous.snapshot
	}
	if !c.publishPerformanceState(flight, next, true) {
		return nil, performanceUnpublishedFlightError(flight)
	}
	return next, loadErr
}

func (c *GlobalModelPerformanceCache) joinPerformanceFlight(
	attemptStartedAt time.Time,
) (*performanceLoadFlight, error) {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	if err := c.lifecycle.Err(); err != nil {
		return nil, performanceContextCause(c.lifecycle)
	}
	if c.flight != nil {
		c.flight.waiters++
		return c.flight, nil
	}
	timeoutCtx, timeoutCancel := context.WithTimeout(c.lifecycle, c.loadTimeout)
	sharedCtx, cancel := context.WithCancelCause(timeoutCtx)
	c.flightSeq++
	flight := &performanceLoadFlight{
		ctx: sharedCtx, cancel: cancel, timeoutCancel: timeoutCancel,
		key:              fmt.Sprintf("global-model-performance#%d", c.flightSeq),
		attemptStartedAt: attemptStartedAt, waiters: 1,
	}
	c.flight = flight
	return flight, nil
}

func (c *GlobalModelPerformanceCache) leavePerformanceFlight(flight *performanceLoadFlight) {
	c.flightMu.Lock()
	flight.waiters--
	forget := flight.waiters == 0 && !flight.finished && c.flight == flight
	if forget {
		c.flight = nil
		flight.cancel(errPerformanceFlightNoWaiters)
		flight.timeoutCancel()
	}
	c.flightMu.Unlock()
	if forget {
		c.group.Forget(flight.key)
	}
}

func (c *GlobalModelPerformanceCache) finishPerformanceFlight(flight *performanceLoadFlight) {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	flight.finished = true
	if c.flight == flight {
		c.flight = nil
	}
	flight.cancel(nil)
	flight.timeoutCancel()
}

func (c *GlobalModelPerformanceCache) publishPerformanceState(
	flight *performanceLoadFlight,
	next *performanceCacheState,
	allowDeadlineFailure bool,
) bool {
	c.flightMu.Lock()
	defer c.flightMu.Unlock()
	if c.flight != flight || flight.finished || c.lifecycle.Err() != nil {
		return false
	}
	if errors.Is(context.Cause(flight.ctx), errPerformanceFlightNoWaiters) {
		return false
	}
	if !allowDeadlineFailure && flight.ctx.Err() != nil {
		return false
	}
	c.state.Store(next)
	return true

}

func (c *GlobalModelPerformanceCache) performanceFlightCanceledWithoutBackendFailure(flight *performanceLoadFlight) bool {
	return errors.Is(context.Cause(flight.ctx), errPerformanceFlightNoWaiters) || c.lifecycle.Err() != nil

}

func performanceResultFromState(state *performanceCacheState) (*GlobalModelPerformanceSnapshot, PerformanceStatus, error) {
	if state == nil {
		return nil, PerformanceUnavailable, errors.New("model performance cache has no state")
	}
	if state.err != nil {
		snapshot := cloneGlobalModelPerformanceSnapshot(state.snapshot)
		if snapshot == nil {
			return nil, PerformanceUnavailable, state.err
		}
		snapshot.LastRefreshAttemptAt = state.attemptStartedAt
		return snapshot, PerformanceStale, state.err
	}
	if state.snapshot == nil {
		return nil, PerformanceUnavailable, errors.New("model performance refresh produced no snapshot")
	}
	return cloneGlobalModelPerformanceSnapshot(state.snapshot), PerformanceAvailable, nil
}

func performanceStateFresh(state *performanceCacheState, now time.Time) bool {
	return state != nil && state.err == nil && performanceSnapshotFresh(state.snapshot, now)
}

func performanceFailureSuppressed(state *performanceCacheState, now time.Time) bool {
	return state != nil && state.err != nil && now.Before(state.retryAfter)
}

func performanceSnapshotFresh(snapshot *GlobalModelPerformanceSnapshot, now time.Time) bool {
	return snapshot != nil && !snapshot.RefreshedAt.IsZero() && now.Before(snapshot.RefreshedAt.Add(performanceRefreshInterval))
}

func performanceContextCause(ctx context.Context) error {
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	return ctx.Err()
}

func performanceUnpublishedFlightError(flight *performanceLoadFlight) error {
	if err := performanceContextCause(flight.ctx); err != nil {
		return err
	}
	return errPerformanceFlightNoWaiters
}

func cloneGlobalModelPerformanceSnapshot(source *GlobalModelPerformanceSnapshot) *GlobalModelPerformanceSnapshot {
	if source == nil {
		return nil
	}
	clone := &GlobalModelPerformanceSnapshot{
		RefreshedAt: source.RefreshedAt, ObservedUntil: source.ObservedUntil,
		LastRefreshAttemptAt: source.LastRefreshAttemptAt,
		Offers:               make(map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot, len(source.Offers)),
	}
	for identity, offer := range source.Offers {
		clone.Offers[identity] = GlobalOfferPerformanceSnapshot{
			HourlyTrendComponents30D: append([]HourlyPerformanceComponents(nil), offer.HourlyTrendComponents30D...),
		}
	}
	return clone
}

type modelPerformanceSnapshotLoader struct {
	query dao.ModelMarketplacePerformanceQuery
}

func NewModelPerformanceSnapshotLoader(query dao.ModelMarketplacePerformanceQuery) PerformanceSnapshotLoader {
	return &modelPerformanceSnapshotLoader{query: query}
}

func (l *modelPerformanceSnapshotLoader) Load(
	ctx context.Context,
	observedUntil time.Time,
) (*GlobalModelPerformanceSnapshot, error) {
	if l == nil || l.query == nil {
		return nil, errors.New("model performance query is required")
	}
	rows, err := l.query.FindGlobalPerformance(ctx, observedUntil)
	if err != nil {
		return nil, fmt.Errorf("load global model performance: %w", err)
	}
	snapshot := &GlobalModelPerformanceSnapshot{
		ObservedUntil: observedUntil.UTC(),
		Offers:        make(map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot, len(rows)),
	}
	for key, hours := range rows {
		identity, conversionErr := performanceIdentityFromDAOKey(key)
		if conversionErr != nil {
			return nil, conversionErr
		}
		snapshot.Offers[identity] = GlobalOfferPerformanceSnapshot{
			HourlyTrendComponents30D: append([]HourlyPerformanceComponents(nil), hours...),
		}
	}
	return snapshot, nil
}

func performanceIdentityFromDAOKey(key dao.MarketplacePerformanceOfferKey) (ModelOfferIdentity, error) {
	identity := ModelOfferIdentity{ModelName: key.ModelName, SourceID: key.SourceID}
	switch key.Kind {
	case dao.MarketplacePerformanceOfferPlatform:
		identity.Kind = OfferKindPlatform
	case dao.MarketplacePerformanceOfferPrivate:
		identity.Kind = OfferKindPrivate
	default:
		return ModelOfferIdentity{}, fmt.Errorf("unsupported marketplace performance offer kind %q", key.Kind)
	}
	if identity.ModelName == "" || identity.SourceID == 0 {
		return ModelOfferIdentity{}, errors.New("incomplete marketplace performance offer identity")
	}
	return identity, nil
}
