package genericapi

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	relaylimiter "github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
)

type APILimiterFinder interface {
	EffectiveSourceAPILimiters(userID, groupID, serviceID, routeID uint) []cache.APILimiter
	EffectiveUpstreamAPILimiters(upstreamID uint) []cache.APILimiter
}

type LimiterGate struct {
	finder APILimiterFinder
	store  apiPermitStore
}

type apiPermitStore interface {
	relaylimiter.PermitStore
	relaylimiter.RateBatchStore
}

func NewLimiterGate(finder APILimiterFinder, store apiPermitStore) *LimiterGate {
	return &LimiterGate{finder: finder, store: store}
}

func (g *LimiterGate) Acquire(ctx context.Context, facts APIRequestFacts) (APIPermit, error) {
	if ctx == nil || g == nil || g.finder == nil || g.store == nil || facts.RequestID == "" {
		return nil, fmt.Errorf("%w: limiter facts unavailable", ErrExecutionUnavailable)
	}
	limiters := g.limiters(facts)
	sort.Slice(limiters, func(i, j int) bool {
		if limiters[i].Limiter.Action != limiters[j].Limiter.Action {
			return limiters[i].Limiter.Action == models.LimiterActionReject
		}
		return limiters[i].Limiter.ID < limiters[j].Limiter.ID
	})
	return g.acquire(ctx, facts, limiters)
}

func (g *LimiterGate) limiters(facts APIRequestFacts) []cache.APILimiter {
	if facts.APIUpstreamID != 0 {
		return g.finder.EffectiveUpstreamAPILimiters(facts.APIUpstreamID)
	}
	return g.finder.EffectiveSourceAPILimiters(facts.UserID, facts.GroupID, facts.APIServiceID, facts.APIRouteID)
}

func (g *LimiterGate) acquire(ctx context.Context, facts APIRequestFacts, values []cache.APILimiter) (APIPermit, error) {
	permit, failed := g.tryAdmission(facts, values)
	if failed == nil {
		return permit, nil
	}
	if failed.Limiter.Action != models.LimiterActionWait {
		return nil, rejectedAPILimitError(permit, *failed, facts)
	}
	if facts.NoWait {
		return nil, rejectedAPILimitError(permit, *failed, facts)
	}
	queuedAt := time.Now()
	queuedIDs := map[uint]struct{}{failed.Limiter.ID: {}}
	deadline := time.Now().Add(queueTime(values))
	for time.Now().Before(deadline) {
		key := limiterBucket(*failed, facts)
		if !g.store.AddWaiter(key, failed.Limiter.QueueSize) {
			return nil, rejectedAPILimitError(permit, *failed, facts, elapsedMs(queuedAt))
		}
		select {
		case <-ctx.Done():
			g.store.RemoveWaiter(key)
			return nil, queuedAPILimitError(context.Cause(ctx), permit, *failed, facts, elapsedMs(queuedAt))
		case <-g.store.WaitC(key):
		case <-time.After(min(50*time.Millisecond, time.Until(deadline))):
		}
		g.store.RemoveWaiter(key)
		permit, failed = g.tryAdmission(facts, values)
		if failed == nil {
			markQueuedAPILimit(permit, queuedIDs, int(time.Since(queuedAt)/time.Millisecond))
			return permit, nil
		}
		if failed.Limiter != nil {
			queuedIDs[failed.Limiter.ID] = struct{}{}
		}
		if failed.Limiter.Action != models.LimiterActionWait {
			return nil, rejectedAPILimitError(permit, *failed, facts, elapsedMs(queuedAt))
		}
	}
	return nil, rejectedAPILimitError(permit, *failed, facts, elapsedMs(queuedAt))
}

func (g *LimiterGate) tryAdmission(facts APIRequestFacts, values []cache.APILimiter) (*apiPermit, *cache.APILimiter) {
	permit, failed := g.tryConcurrency(facts, values)
	if failed != nil {
		return permit, failed
	}
	return g.tryRateBatch(facts, values, permit)
}

func elapsedMs(startedAt time.Time) int {
	return max(0, int(time.Since(startedAt)/time.Millisecond))
}

func markQueuedAPILimit(permit *apiPermit, limiterIDs map[uint]struct{}, waitMs int) {
	if permit == nil {
		return
	}
	permit.rate.RateLimitDecision = "queued"
	permit.rate.RateLimitWaitMs = max(0, waitMs)
	for index := range permit.rate.RateLimitHits {
		if _, ok := limiterIDs[permit.rate.RateLimitHits[index].LimiterID]; !ok {
			continue
		}
		permit.rate.RateLimitHits[index].Decision = "queued"
		permit.rate.RateLimitHits[index].WaitMs = permit.rate.RateLimitWaitMs
	}
}

func (g *LimiterGate) tryConcurrency(facts APIRequestFacts, values []cache.APILimiter) (*apiPermit, *cache.APILimiter) {
	permit := &apiPermit{}
	for index := range values {
		value := values[index]
		if value.Limiter == nil || !value.Limiter.Enabled || value.Limiter.Metric != models.LimiterMetricConcurrency {
			continue
		}
		key := limiterBucket(value, facts)
		hit := buildAPIRateLimitHit(value.Limiter, key, "allow", 0)
		release, ok := g.store.TryConcurrency(key, value.Limiter.Capacity)
		if !ok {
			permit.Release()
			return permit, &values[index]
		}
		permit.rate.RateLimitHits = append(permit.rate.RateLimitHits, hit)
		if release != nil {
			permit.releases = append(permit.releases, release)
		}
	}
	if len(permit.rate.RateLimitHits) > 0 {
		permit.rate.RateLimitDecision = "allow"
	}
	return permit, nil
}

func (g *LimiterGate) tryRateBatch(
	facts APIRequestFacts,
	values []cache.APILimiter,
	permit *apiPermit,
) (*apiPermit, *cache.APILimiter) {
	requests := make([]relaylimiter.RateRequest, 0, len(values))
	rateLimiters := make([]cache.APILimiter, 0, len(values))
	for _, value := range values {
		if value.Limiter == nil || !value.Limiter.Enabled || value.Limiter.Metric != models.LimiterMetricRate {
			continue
		}
		requests = append(requests, relaylimiter.RateRequest{
			Key: limiterBucket(value, facts), Capacity: value.Limiter.Capacity, WindowMs: value.Limiter.WindowMs,
		})
		rateLimiters = append(rateLimiters, value)
	}
	rejected, ok := g.store.TryRateBatch(requests)
	if !ok {
		permit.Release()
		return permit, &rateLimiters[rejected]
	}
	for index, value := range rateLimiters {
		permit.rate.RateLimitHits = append(
			permit.rate.RateLimitHits,
			buildAPIRateLimitHit(value.Limiter, requests[index].Key, "allow", 0),
		)
	}
	if permit.rate.RateLimitDecision == "" && len(permit.rate.RateLimitHits) > 0 {
		permit.rate.RateLimitDecision = "allow"
	}
	return permit, nil
}

type apiRateLimitError struct {
	cause  error
	result apiattempt.APIExecutionResult
}

func (e *apiRateLimitError) Error() string {
	if e == nil || e.cause == nil {
		return ErrAPIRateLimited.Error()
	}
	return e.cause.Error()
}
func (e *apiRateLimitError) Unwrap() error {
	if e == nil || e.cause == nil {
		return ErrAPIRateLimited
	}
	return e.cause
}
func (e *apiRateLimitError) RateLimitResult() apiattempt.APIExecutionResult {
	return cloneRateLimitResult(e.result)
}

func rejectedAPILimitError(permit *apiPermit, failed cache.APILimiter, facts APIRequestFacts, waited ...int) error {
	result := rateLimitResult(permit)
	waitMs := result.RateLimitWaitMs
	if len(waited) > 0 && waited[0] > waitMs {
		waitMs = waited[0]
	}
	if failed.Limiter != nil {
		key := limiterBucket(failed, facts)
		result.RateLimitHits = append(
			result.RateLimitHits,
			buildAPIRateLimitHit(failed.Limiter, key, "rejected", waitMs),
		)
	}
	result.RateLimitDecision = "rejected"
	result.RateLimitWaitMs = waitMs
	result.RateLimitReason = CodeRateLimited
	return &apiRateLimitError{cause: ErrAPIRateLimited, result: result}
}

func queuedAPILimitError(
	cause error,
	permit *apiPermit,
	failed cache.APILimiter,
	facts APIRequestFacts,
	waitMs int,
) error {
	result := rateLimitResult(permit)
	if failed.Limiter != nil {
		key := limiterBucket(failed, facts)
		result.RateLimitHits = append(
			result.RateLimitHits,
			buildAPIRateLimitHit(failed.Limiter, key, "queued", waitMs),
		)
	}
	result.RateLimitDecision = "queued"
	result.RateLimitWaitMs = waitMs
	return &apiRateLimitError{cause: cause, result: result}
}

type apiRateLimitResult interface {
	RateLimitResult() apiattempt.APIExecutionResult
}

func rateLimitResult(value any) apiattempt.APIExecutionResult {
	if provider, ok := value.(apiRateLimitResult); ok && provider != nil {
		return cloneRateLimitResult(provider.RateLimitResult())
	}
	return apiattempt.APIExecutionResult{}
}

func buildAPIRateLimitHit(
	limiter *models.RequestLimiter,
	key relaylimiter.BucketKey,
	decision string,
	waitMs int,
) models.RateLimitHit {
	return models.RateLimitHit{
		LimiterID: limiter.ID,
		Name:      models.WireSafeRequestLimiterName(limiter.ID, limiter.Name),
		Dimension: limiter.Metric + "/" + limiter.KeyBy,
		Bucket:    key.Bucket,
		Decision:  decision,
		WaitMs:    waitMs,
	}
}

func cloneRateLimitResult(value apiattempt.APIExecutionResult) apiattempt.APIExecutionResult {
	return apiattempt.NormalizeRateLimitResult(value)
}

func mergeRateLimitResult(target *apiattempt.APIExecutionResult, source apiattempt.APIExecutionResult) {
	if target == nil {
		return
	}
	current := apiattempt.NormalizeRateLimitResult(*target)
	source = apiattempt.NormalizeRateLimitResult(source)
	current.RateLimitHitTotal += source.RateLimitHitTotal
	current.RateLimitHitsTruncated = current.RateLimitHitsTruncated || source.RateLimitHitsTruncated
	current.RateLimitHits = append(current.RateLimitHits, source.RateLimitHits...)
	*target = current
	if rateLimitDecisionRank(source.RateLimitDecision) > rateLimitDecisionRank(target.RateLimitDecision) {
		target.RateLimitDecision = source.RateLimitDecision
		target.RateLimitReason = source.RateLimitReason
	}
	if source.RateLimitWaitMs > target.RateLimitWaitMs {
		target.RateLimitWaitMs = source.RateLimitWaitMs
	}
	*target = apiattempt.NormalizeRateLimitResult(*target)
}

func rateLimitDecisionRank(value string) int {
	switch value {
	case "rejected":
		return 3
	case "queued":
		return 2
	case "allow":
		return 1
	default:
		return 0
	}
}

func limiterBucket(value cache.APILimiter, facts APIRequestFacts) relaylimiter.BucketKey {
	prefix := value.TargetType
	if prefix == "" {
		prefix = models.LimiterTargetGlobal
	}
	prefix += ":" + strconv.FormatUint(uint64(value.TargetID), 10)
	suffix := "shared"
	switch value.Limiter.KeyBy {
	case models.LimiterKeyPerUser:
		suffix = "u:" + strconv.FormatUint(uint64(facts.UserID), 10)
	case models.LimiterKeyPerGroup:
		suffix = "g:" + strconv.FormatUint(uint64(facts.GroupID), 10)
	}
	return relaylimiter.BucketKey{LimiterID: value.Limiter.ID, Bucket: prefix + ":" + suffix}
}

func queueTime(values []cache.APILimiter) time.Duration {
	duration := 120 * time.Second
	for _, value := range values {
		if value.Limiter != nil && value.Limiter.Action == models.LimiterActionWait && value.Limiter.QueueTimeMs > 0 {
			candidate := time.Duration(value.Limiter.QueueTimeMs) * time.Millisecond
			if candidate < duration {
				duration = candidate
			}
		}
	}
	return duration
}

type apiPermit struct {
	once     sync.Once
	releases []func()
	rate     apiattempt.APIExecutionResult
}

func (p *apiPermit) RateLimitResult() apiattempt.APIExecutionResult {
	if p == nil {
		return apiattempt.APIExecutionResult{}
	}
	return cloneRateLimitResult(p.rate)
}

func (p *apiPermit) Release() {
	if p == nil {
		return
	}
	p.once.Do(func() {
		for index := len(p.releases) - 1; index >= 0; index-- {
			p.releases[index]()
		}
	})
}
