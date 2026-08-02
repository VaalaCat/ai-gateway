package model_marketplace

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/safeint"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
)

type MarketplaceHealthStatus string

const (
	MarketplaceHealthOperational MarketplaceHealthStatus = "operational"
	MarketplaceHealthDegraded    MarketplaceHealthStatus = "degraded"
	MarketplaceHealthOutage      MarketplaceHealthStatus = "outage"
	MarketplaceHealthUnknown     MarketplaceHealthStatus = "unknown"
)

type PerformanceSummary struct {
	Status        MarketplaceHealthStatus `json:"status"`
	SuccessRate   *float64                `json:"success_rate"`
	TTFTAvgMs     *float64                `json:"ttft_avg_ms"`
	TTFTP95Ms     *int64                  `json:"ttft_p95_ms"`
	TPSAvg        *float64                `json:"tps_avg"`
	TPSP5         *float64                `json:"tps_p5"`
	DurationP95Ms *int64                  `json:"duration_p95_ms"`
	TokenUnits    TokenUnits              `json:"token_units"`
}

type PerformanceStatusBucket struct {
	StartedAt  int64                   `json:"started_at"`
	EndedAt    int64                   `json:"ended_at"`
	Status     MarketplaceHealthStatus `json:"status"`
	InProgress bool                    `json:"in_progress"`
}

type PerformanceTrendPoint struct {
	StartedAt   int64                   `json:"started_at"`
	EndedAt     int64                   `json:"ended_at"`
	Status      MarketplaceHealthStatus `json:"status"`
	InProgress  bool                    `json:"in_progress"`
	SuccessRate *float64                `json:"success_rate"`
	TTFTAvgMs   *float64                `json:"ttft_avg_ms"`
	TPSAvg      *float64                `json:"tps_avg"`
	TokenUnits  TokenUnits              `json:"token_units"`
}

// PerformanceDiagnostics retains the selected window's sample facts for the
// administrator mapper. Ordinary JSON cannot serialize these internal fields.
type PerformanceDiagnostics struct {
	RequestCount        int64 `json:"-"`
	SuccessCount        int64 `json:"-"`
	FailureCount        int64 `json:"-"`
	StreamRequestCount  int64 `json:"-"`
	TTFTSampleCount     int64 `json:"-"`
	TPSSampleCount      int64 `json:"-"`
	DurationSampleCount int64 `json:"-"`
}

type OfferPerformanceSnapshot struct {
	Summary       PerformanceSummary        `json:"summary"`
	StatusBuckets []PerformanceStatusBucket `json:"status_buckets"`
	Trend         []PerformanceTrendPoint   `json:"trend"`
	Diagnostics   PerformanceDiagnostics    `json:"-"`
}

// ModelPerformanceSnapshot is the model-level performance projection. It is
// intentionally narrower than an offer snapshot: neither raw components nor
// diagnostic counters cross the model DTO boundary.
type ModelPerformanceSnapshot struct {
	PerformanceStatus PerformanceStatus
	Window            UsageWindow
	SuccessRate       *float64
	CacheHitRate      *float64
	StatusBuckets     []ModelPerformanceStatusBucket
}

type ModelPerformanceStatusBucket struct {
	StartedAt   int64
	EndedAt     int64
	SuccessRate *float64
	Status      MarketplaceHealthStatus
	InProgress  bool
}

// ModelPerformanceResult is an ordinary-user-safe projection. Raw components,
// source IDs, model names, and all sample counters exist only in the cache.
type ModelPerformanceResult struct {
	PerformanceStatus PerformanceStatus                   `json:"performance_status"`
	RefreshedAt       time.Time                           `json:"refreshed_at"`
	ObservedUntil     time.Time                           `json:"observed_until"`
	ModelStatus       MarketplaceHealthStatus             `json:"model_status"`
	Offers            map[string]OfferPerformanceSnapshot `json:"offers"`
	Model             ModelPerformanceSnapshot            `json:"-"`
	offerComponents   map[string][]HourlyPerformanceComponents
	projectionWindow  UsageWindow
	minSamples        int
}

type PerformanceSettings interface {
	Lookup(string) (string, bool)
}

type ModelPerformanceFinder struct {
	cache    *GlobalModelPerformanceCache
	settings PerformanceSettings
}

func NewModelPerformanceFinder(
	cache *GlobalModelPerformanceCache,
	settings PerformanceSettings,
) ModelPerformanceFinder {
	return ModelPerformanceFinder{cache: cache, settings: settings}
}

// Find projects only the re-enumerated visible real Offers supplied by the
// caller. Routing models pass no Offers and therefore never acquire a synthetic
// performance record.
func (f ModelPerformanceFinder) Find(
	ctx context.Context,
	offers []ModelOffer,
	window UsageWindow,
	now time.Time,
) (ModelPerformanceResult, error) {
	windowHours, bucketHours, err := performanceWindowShape(window)
	if err != nil {
		return ModelPerformanceResult{}, err
	}
	canonical, err := canonicalUsageOffers(offers)
	if err != nil {
		return ModelPerformanceResult{}, err
	}
	result := ModelPerformanceResult{
		PerformanceStatus: PerformanceAvailable,
		ObservedUntil:     now.UTC(),
		ModelStatus:       MarketplaceHealthUnknown,
		Offers:            make(map[string]OfferPerformanceSnapshot, len(canonical)),
		offerComponents:   make(map[string][]HourlyPerformanceComponents, len(canonical)),
		projectionWindow:  window,
		minSamples:        f.minSamples(),
	}
	if len(canonical) == 0 {
		return result, nil
	}
	if f.cache == nil {
		result.PerformanceStatus = PerformanceUnavailable
		return result, errors.New("model performance cache is required")
	}
	snapshot, status, cacheErr := f.cache.Get(ctx, now)
	result.PerformanceStatus = status
	if snapshot == nil {
		return result, cacheErr
	}
	result.RefreshedAt = snapshot.RefreshedAt
	result.ObservedUntil = snapshot.ObservedUntil
	visibleStatuses := make([]MarketplaceHealthStatus, 0, len(canonical))
	for _, offer := range canonical {
		offerComponents := snapshot.Offers[offer.Identity].HourlyTrendComponents30D
		offerSnapshot, projectErr := projectOfferPerformance(
			snapshot.Offers[offer.Identity], snapshot.ObservedUntil,
			windowHours, bucketHours, result.minSamples,
		)
		if projectErr != nil {
			return result, errors.Join(cacheErr, fmt.Errorf("project offer %q performance: %w", offer.OfferRef, projectErr))
		}
		result.Offers[offer.OfferRef] = offerSnapshot
		result.offerComponents[offer.OfferRef] = append([]HourlyPerformanceComponents(nil), offerComponents...)
		visibleStatuses = append(visibleStatuses, offerSnapshot.Summary.Status)
	}
	result.ModelStatus = aggregateVisiblePerformanceStatus(visibleStatuses)
	return result, cacheErr
}

func (f ModelPerformanceFinder) minSamples() int {
	if f.settings == nil {
		return consts.ModelMarketplaceDefaultMinSamples
	}
	raw, ok := f.settings.Lookup(consts.SettingKeyModelMarketplaceMinSamples)
	if !ok {
		return consts.ModelMarketplaceDefaultMinSamples
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < consts.ModelMarketplaceMinimumSamples || value > consts.ModelMarketplaceMaximumSamples {
		return consts.ModelMarketplaceDefaultMinSamples
	}
	return value
}

func performanceWindowShape(window UsageWindow) (windowHours int, bucketHours int, err error) {
	switch window {
	case UsageWindow24Hours:
		return 24, 1, nil
	case UsageWindow7Days:
		return 7 * 24, 6, nil
	case UsageWindow30Days:
		return MarketplacePerformanceHours, 24, nil
	default:
		return 0, 0, fmt.Errorf("invalid marketplace performance window %q", window)
	}
}

func projectOfferPerformance(
	source GlobalOfferPerformanceSnapshot,
	observedUntil time.Time,
	windowHours int,
	bucketHours int,
	minSamples int,
) (OfferPerformanceSnapshot, error) {
	normalized, err := normalizePerformanceHours(source.HourlyTrendComponents30D, observedUntil)
	if err != nil {
		return OfferPerformanceSnapshot{}, err
	}
	projection, err := projectPerformanceWindow(normalized, observedUntil, windowHours, bucketHours)
	if err != nil {
		return OfferPerformanceSnapshot{}, err
	}
	result := OfferPerformanceSnapshot{
		StatusBuckets: make([]PerformanceStatusBucket, 0, len(projection.Buckets)),
		Trend:         make([]PerformanceTrendPoint, 0, len(projection.Buckets)),
	}
	for _, bucket := range projection.Buckets {
		metrics, metricErr := performanceSummary(bucket.Components, minSamples)
		if metricErr != nil {
			return OfferPerformanceSnapshot{}, metricErr
		}
		result.StatusBuckets = append(result.StatusBuckets, PerformanceStatusBucket{
			StartedAt: bucket.StartedAt.Unix(), EndedAt: bucket.EndedAt.Unix(),
			Status: metrics.Status, InProgress: bucket.InProgress,
		})
		result.Trend = append(result.Trend, PerformanceTrendPoint{
			StartedAt: bucket.StartedAt.Unix(), EndedAt: bucket.EndedAt.Unix(),
			Status: metrics.Status, InProgress: bucket.InProgress,
			SuccessRate: metrics.SuccessRate, TTFTAvgMs: metrics.TTFTAvgMs,
			TPSAvg: metrics.TPSAvg, TokenUnits: metrics.TokenUnits,
		})
	}
	result.Summary, err = performanceSummary(projection.Summary, minSamples)
	if err != nil {
		return OfferPerformanceSnapshot{}, err
	}
	result.Diagnostics, err = performanceDiagnostics(projection.Summary)
	return result, err
}

type performanceWindowProjection struct {
	Buckets []performanceComponentsBucket
	Summary PerformanceComponents
}

type performanceComponentsBucket struct {
	StartedAt  time.Time
	EndedAt    time.Time
	InProgress bool
	Components PerformanceComponents
}

// projectPerformanceWindow is the single aggregation path shared by offer and
// model projections. Its input is already normalized to one UTC hour per
// element, and every aggregate addition goes through PerformanceComponents.Merge.
func projectPerformanceWindow(
	normalized []HourlyPerformanceComponents,
	observedUntil time.Time,
	windowHours int,
	bucketHours int,
) (performanceWindowProjection, error) {
	if windowHours <= 0 || bucketHours <= 0 || windowHours%bucketHours != 0 || len(normalized) < windowHours {
		return performanceWindowProjection{}, errors.New("invalid marketplace performance window shape")
	}
	selected := normalized[len(normalized)-windowHours:]
	projection := performanceWindowProjection{Buckets: make([]performanceComponentsBucket, 0, len(selected)/bucketHours)}
	for start := 0; start < len(selected); start += bucketHours {
		var components PerformanceComponents
		for _, hour := range selected[start : start+bucketHours] {
			var err error
			components, err = components.Merge(hour.Components)
			if err != nil {
				return performanceWindowProjection{}, err
			}
		}
		var err error
		projection.Summary, err = projection.Summary.Merge(components)
		if err != nil {
			return performanceWindowProjection{}, err
		}
		startedAt := selected[start].Hour.UTC()
		endedAt := startedAt.Add(time.Duration(bucketHours) * time.Hour)
		projection.Buckets = append(projection.Buckets, performanceComponentsBucket{
			StartedAt: startedAt, EndedAt: endedAt,
			InProgress: observedUntil.UTC().Before(endedAt), Components: components,
		})
	}
	return projection, nil
}

func projectModelPerformance(
	componentsByOffer [][]HourlyPerformanceComponents,
	observedUntil time.Time,
	window UsageWindow,
	minSamples int,
) (ModelPerformanceSnapshot, error) {
	windowHours, bucketHours, err := performanceWindowShape(window)
	if err != nil {
		return ModelPerformanceSnapshot{}, err
	}
	normalized, err := mergeOfferPerformanceHours(componentsByOffer, observedUntil)
	if err != nil {
		return ModelPerformanceSnapshot{}, err
	}
	projection, err := projectPerformanceWindow(normalized, observedUntil, windowHours, bucketHours)
	if err != nil {
		return ModelPerformanceSnapshot{}, err
	}
	if _, err := performanceDiagnostics(projection.Summary); err != nil {
		return ModelPerformanceSnapshot{}, err
	}
	result := ModelPerformanceSnapshot{
		Window: window, StatusBuckets: make([]ModelPerformanceStatusBucket, 0, len(projection.Buckets)),
	}
	result.SuccessRate = trustworthyModelSuccessRate(projection.Summary, minSamples)
	result.CacheHitRate, err = trustworthyModelCacheHitRate(projection.Summary, minSamples)
	if err != nil {
		return ModelPerformanceSnapshot{}, err
	}
	for _, bucket := range projection.Buckets {
		result.StatusBuckets = append(result.StatusBuckets, ModelPerformanceStatusBucket{
			StartedAt: bucket.StartedAt.Unix(), EndedAt: bucket.EndedAt.Unix(),
			SuccessRate: trustworthyModelSuccessRate(bucket.Components, minSamples),
			Status:      performanceHealthStatus(bucket.Components, minSamples), InProgress: bucket.InProgress,
		})
	}
	return result, nil
}

func mergeOfferPerformanceHours(
	componentsByOffer [][]HourlyPerformanceComponents,
	observedUntil time.Time,
) ([]HourlyPerformanceComponents, error) {
	merged, err := normalizePerformanceHours(nil, observedUntil)
	if err != nil {
		return nil, err
	}
	for _, source := range componentsByOffer {
		normalized, normalizeErr := normalizePerformanceHours(source, observedUntil)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		for index := range merged {
			mergedComponents, mergeErr := merged[index].Components.Merge(normalized[index].Components)
			if mergeErr != nil {
				return nil, mergeErr
			}
			merged[index].Components = mergedComponents
		}
	}
	return merged, nil
}

func trustworthyModelSuccessRate(components PerformanceComponents, minSamples int) *float64 {
	if minSamples < 1 {
		minSamples = consts.ModelMarketplaceDefaultMinSamples
	}
	if components.RequestCount < int64(minSamples) || components.RequestCount <= 0 || components.SuccessCount < 0 || components.SuccessCount > components.RequestCount {
		return nil
	}
	value := float64(components.SuccessCount) * 100 / float64(components.RequestCount)
	return &value
}

func trustworthyModelCacheHitRate(components PerformanceComponents, minSamples int) (*float64, error) {
	if minSamples < 1 {
		minSamples = consts.ModelMarketplaceDefaultMinSamples
	}
	if components.RequestCount < int64(minSamples) {
		return nil, nil
	}
	tokens, err := performanceTokenUnits(components)
	if err != nil {
		return nil, err
	}
	denominator, err := safeint.AddNonNegativeInt64(tokens.Input, tokens.CacheRead)
	if err != nil {
		return nil, fmt.Errorf("sum marketplace model cache tokens: %w", err)
	}
	if denominator == 0 {
		return nil, nil
	}
	value := float64(tokens.CacheRead) * 100 / float64(denominator)
	return &value, nil
}

func normalizePerformanceHours(
	source []HourlyPerformanceComponents,
	observedUntil time.Time,
) ([]HourlyPerformanceComponents, error) {
	if observedUntil.IsZero() {
		return nil, errors.New("model performance observed until is required")
	}
	currentHour := observedUntil.UTC().Truncate(time.Hour)
	firstHour := currentHour.Add(-(MarketplacePerformanceHours - 1) * time.Hour)
	result := make([]HourlyPerformanceComponents, MarketplacePerformanceHours)
	positions := make(map[int64]int, MarketplacePerformanceHours)
	for i := range result {
		hour := firstHour.Add(time.Duration(i) * time.Hour)
		result[i].Hour = hour
		positions[hour.Unix()] = i
	}
	for _, sourceHour := range source {
		hour := sourceHour.Hour.UTC()
		if hour.IsZero() || !hour.Equal(hour.Truncate(time.Hour)) {
			return nil, errors.New("model performance hour is not UTC-hour aligned")
		}
		position, ok := positions[hour.Unix()]
		if !ok {
			continue
		}
		merged, err := result[position].Components.Merge(sourceHour.Components)
		if err != nil {
			return nil, err
		}
		result[position].Components = merged
	}
	return result, nil
}

func performanceSummary(components PerformanceComponents, minSamples int) (PerformanceSummary, error) {
	tokens, err := performanceTokenUnits(components)
	if err != nil {
		return PerformanceSummary{}, err
	}
	ttftHasSamples, err := validatePerformanceHistogram("TTFT", components.TTFTHistogram)
	if err != nil {
		return PerformanceSummary{}, err
	}
	tpsHasSamples, err := validatePerformanceHistogram("TPS", components.TPSHistogram)
	if err != nil {
		return PerformanceSummary{}, err
	}
	durationHasSamples, err := validatePerformanceHistogram("duration", components.DurationHistogram)
	if err != nil {
		return PerformanceSummary{}, err
	}
	summary := PerformanceSummary{
		Status:     performanceHealthStatus(components, minSamples),
		TokenUnits: tokens,
	}
	if components.RequestCount > 0 {
		value := float64(components.SuccessCount) * 100 / float64(components.RequestCount)
		summary.SuccessRate = &value
	}
	if components.StreamRequestCount > 0 {
		value := float64(components.SumFirstResponseMs) / float64(components.StreamRequestCount)
		summary.TTFTAvgMs = &value
	}
	if components.SumGenerationMs > 0 {
		value := float64(components.SumStreamCompletionTokens) * 1000 / float64(components.SumGenerationMs)
		summary.TPSAvg = &value
	}
	if ttftHasSamples {
		value := ttfthist.EstimatePercentile(components.TTFTHistogram.Counts, 0.95, components.TTFTHistogram.Max)
		summary.TTFTP95Ms = &value
	}
	if tpsHasSamples {
		value := float64(tpshist.EstimateP5(components.TPSHistogram.Counts, components.TPSHistogram.Max))
		summary.TPSP5 = &value
	}
	if durationHasSamples {
		value := durhist.EstimatePercentile(components.DurationHistogram.Counts, 0.95, components.DurationHistogram.Max)
		summary.DurationP95Ms = &value
	}
	return summary, nil
}

func performanceTokenUnits(components PerformanceComponents) (TokenUnits, error) {
	total, err := safeint.AddNonNegativeInt64(
		components.InputTokens, components.OutputTokens,
		components.CacheReadTokens, components.CacheWriteTokens,
	)
	if err != nil {
		return TokenUnits{}, fmt.Errorf("sum marketplace performance token units: %w", err)
	}
	return TokenUnits{
		Input: components.InputTokens, Output: components.OutputTokens,
		CacheRead: components.CacheReadTokens, CacheWrite: components.CacheWriteTokens,
		Total: total,
	}, nil
}

func performanceDiagnostics(components PerformanceComponents) (PerformanceDiagnostics, error) {
	if components.RequestCount < 0 || components.SuccessCount < 0 || components.SuccessCount > components.RequestCount {
		return PerformanceDiagnostics{}, errors.New("marketplace performance request counters are inconsistent")
	}
	if components.StreamRequestCount < 0 {
		return PerformanceDiagnostics{}, errors.New("marketplace performance stream request count must be non-negative")
	}
	ttftSamples, err := performanceHistogramSampleCount("TTFT", components.TTFTHistogram)
	if err != nil {
		return PerformanceDiagnostics{}, err
	}
	tpsSamples, err := performanceHistogramSampleCount("TPS", components.TPSHistogram)
	if err != nil {
		return PerformanceDiagnostics{}, err
	}
	durationSamples, err := performanceHistogramSampleCount("duration", components.DurationHistogram)
	if err != nil {
		return PerformanceDiagnostics{}, err
	}
	return PerformanceDiagnostics{
		RequestCount: components.RequestCount, SuccessCount: components.SuccessCount,
		FailureCount:       components.RequestCount - components.SuccessCount,
		StreamRequestCount: components.StreamRequestCount,
		TTFTSampleCount:    ttftSamples, TPSSampleCount: tpsSamples, DurationSampleCount: durationSamples,
	}, nil
}

func validatePerformanceHistogram(name string, histogram PerformanceHistogram) (bool, error) {
	total, err := performanceHistogramSampleCount(name, histogram)
	return total > 0, err
}

func performanceHistogramSampleCount(name string, histogram PerformanceHistogram) (int64, error) {
	if histogram.Max < 0 {
		return 0, fmt.Errorf("%s histogram max must be non-negative", name)
	}
	total, err := safeint.AddNonNegativeInt64(histogram.Counts[:]...)
	if err != nil {
		return 0, fmt.Errorf("validate %s histogram counts: %w", name, err)
	}
	return total, nil
}

func performanceHealthStatus(components PerformanceComponents, minSamples int) MarketplaceHealthStatus {
	if minSamples < 1 {
		minSamples = consts.ModelMarketplaceDefaultMinSamples
	}
	if components.RequestCount < int64(minSamples) || components.RequestCount <= 0 {
		return MarketplaceHealthUnknown
	}
	if performanceRatioAtLeast(components.SuccessCount, components.RequestCount, 99) {
		return MarketplaceHealthOperational
	}
	if performanceRatioAtLeast(components.SuccessCount, components.RequestCount, 95) {
		return MarketplaceHealthDegraded
	}
	return MarketplaceHealthOutage
}

func performanceRatioAtLeast(successes, requests, percent int64) bool {
	if successes < 0 || requests <= 0 {
		return false
	}
	left := new(big.Int).Mul(big.NewInt(successes), big.NewInt(100))
	right := new(big.Int).Mul(big.NewInt(requests), big.NewInt(percent))
	return left.Cmp(right) >= 0
}

func aggregateVisiblePerformanceStatus(statuses []MarketplaceHealthStatus) MarketplaceHealthStatus {
	best := MarketplaceHealthUnknown
	for _, status := range statuses {
		switch status {
		case MarketplaceHealthOperational:
			return MarketplaceHealthOperational
		case MarketplaceHealthDegraded:
			best = MarketplaceHealthDegraded
		case MarketplaceHealthOutage:
			if best == MarketplaceHealthUnknown {
				best = MarketplaceHealthOutage
			}
		}
	}
	return best
}
