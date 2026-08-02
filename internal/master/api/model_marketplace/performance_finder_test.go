package model_marketplace

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestModelPerformanceFinderSnapshotUsesWeightedComponentsAndMergedHistograms(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	snapshot := performanceFinderTestSnapshot(now)
	hours := snapshot.Offers[performanceFinderPlatformIdentity(41)].HourlyTrendComponents30D
	hours[len(hours)-2].Components = PerformanceComponents{
		RequestCount: 1, SuccessCount: 1,
		StreamRequestCount: 1, SumFirstResponseMs: 100,
		SumStreamCompletionTokens: 100, SumGenerationMs: 1_000,
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
		TTFTHistogram:     PerformanceHistogram{Counts: [17]int64{1: 1}, Max: 90},
		TPSHistogram:      PerformanceHistogram{Counts: [17]int64{2: 1}, Max: 19},
		DurationHistogram: PerformanceHistogram{Counts: [17]int64{1: 1}, Max: 900},
	}
	hours[len(hours)-1].Components = PerformanceComponents{
		RequestCount: 9, SuccessCount: 8,
		StreamRequestCount: 9, SumFirstResponseMs: 9_000,
		SumStreamCompletionTokens: 900, SumGenerationMs: 9_000,
		InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40,
		TTFTHistogram:     PerformanceHistogram{Counts: [17]int64{4: 9}, Max: 490},
		TPSHistogram:      PerformanceHistogram{Counts: [17]int64{6: 9}, Max: 74},
		DurationHistogram: PerformanceHistogram{Counts: [17]int64{5: 9}, Max: 7_400},
	}
	snapshot.Offers[performanceFinderPlatformIdentity(41)] = GlobalOfferPerformanceSnapshot{HourlyTrendComponents30D: hours}
	settings := app.NewMasterSettingsSnapshot()
	settings.Replace(map[string]string{"model_marketplace_min_samples": "1"})
	finder := NewModelPerformanceFinder(NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) { return snapshot, nil },
	)), settings)
	offer := performanceFinderOffer("offer-platform", performanceFinderPlatformIdentity(41), OfferPlatform)

	result, err := finder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now)

	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, result.PerformanceStatus)
	require.Equal(t, MarketplaceHealthOutage, result.ModelStatus)
	performance := result.Offers[offer.OfferRef]
	require.NotNil(t, performance.Summary.SuccessRate)
	require.InDelta(t, 90.0, *performance.Summary.SuccessRate, 0.000001)
	require.NotNil(t, performance.Summary.TTFTAvgMs)
	require.InDelta(t, 910.0, *performance.Summary.TTFTAvgMs, 0.000001,
		"TTFT must be (100+9000)/(1+9), not an average of hourly averages")
	require.NotNil(t, performance.Summary.TPSAvg)
	require.InDelta(t, 100.0, *performance.Summary.TPSAvg, 0.000001,
		"TPS must be (100+900)*1000/(1000+9000)")
	require.Equal(t, int64Pointer(488), performance.Summary.TTFTP95Ms,
		"P95 must be calculated after merging existing TTFT histogram slots")
	require.Equal(t, float64Pointer(15), performance.Summary.TPSP5,
		"P5 must be calculated after merging existing TPS histogram slots")
	require.Equal(t, int64Pointer(7_361), performance.Summary.DurationP95Ms,
		"P95 must be calculated after merging existing duration histogram slots")
	require.Equal(t, TokenUnits{Input: 11, Output: 22, CacheRead: 33, CacheWrite: 44, Total: 110}, performance.Summary.TokenUnits)
	require.Equal(t, PerformanceDiagnostics{
		RequestCount: 10, SuccessCount: 9, FailureCount: 1, StreamRequestCount: 10,
		TTFTSampleCount: 10, TPSSampleCount: 10, DurationSampleCount: 10,
	}, performance.Diagnostics)
}

// Break caught: skipping cache work for an empty available Offer set must not
// discard the request metadata needed to project the fixed unknown history.
func TestModelPerformanceFinderEmptyOfferSetKeepsProjectionMetadataWithoutCacheRead(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 30, 0, 123, time.FixedZone("test", 8*60*60))
	var loads atomic.Int64
	settings := app.NewMasterSettingsSnapshot()
	settings.Replace(map[string]string{consts.SettingKeyModelMarketplaceMinSamples: "37"})
	finder := NewModelPerformanceFinder(NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) {
			loads.Add(1)
			return nil, errors.New("empty offer set must not load performance")
		},
	)), settings)

	for _, test := range []struct {
		window      UsageWindow
		bucketCount int
	}{
		{window: UsageWindow24Hours, bucketCount: 24},
		{window: UsageWindow7Days, bucketCount: 28},
		{window: UsageWindow30Days, bucketCount: 30},
	} {
		t.Run(string(test.window), func(t *testing.T) {
			result, err := finder.Find(context.Background(), nil, test.window, now)
			require.NoError(t, err)
			require.Equal(t, now.UTC(), result.ObservedUntil)
			require.Equal(t, test.window, result.projectionWindow)
			require.Equal(t, 37, result.minSamples)

			projected, err := performanceForComposedModel(result, composedRealModel{})
			require.NoError(t, err)
			require.Equal(t, PerformanceAvailable, projected.Model.PerformanceStatus)
			require.Equal(t, test.window, projected.Model.Window)
			require.Len(t, projected.Model.StatusBuckets, test.bucketCount)
			for _, bucket := range projected.Model.StatusBuckets {
				require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
				require.Nil(t, bucket.SuccessRate)
			}
		})
	}
	require.Zero(t, loads.Load())
}

// Break caught: averaging per-offer success/cache percentages would overstate
// a model whose high-volume offer fails most requests.
func TestProjectModelPerformance(t *testing.T) {
	observedUntil := time.Date(2026, 8, 2, 10, 30, 0, 0, time.UTC)
	currentHour := observedUntil.Truncate(time.Hour)
	weightedOffers := [][]HourlyPerformanceComponents{
		{{Hour: currentHour, Components: PerformanceComponents{
			RequestCount: 1, SuccessCount: 1, InputTokens: 70,
		}}},
		{{Hour: currentHour, Components: PerformanceComponents{
			RequestCount: 99, SuccessCount: 9, CacheReadTokens: 30,
		}}},
	}

	tests := []struct {
		name            string
		components      [][]HourlyPerformanceComponents
		window          UsageWindow
		minSamples      int
		wantSuccessRate *float64
		wantCacheRate   *float64
		wantBuckets     int
		wantLastStatus  MarketplaceHealthStatus
		wantErr         string
	}{
		{
			name:       "weights offer SLA and cache numerators by raw components",
			components: weightedOffers, window: UsageWindow24Hours, minSamples: 1,
			wantSuccessRate: float64Pointer(10), wantCacheRate: float64Pointer(30),
			wantBuckets: 24, wantLastStatus: MarketplaceHealthOutage,
		},
		{
			name:       "zero request and cache denominators stay unknown",
			components: [][]HourlyPerformanceComponents{{{Hour: currentHour}}},
			window:     UsageWindow24Hours, minSamples: 1,
			wantBuckets: 24, wantLastStatus: MarketplaceHealthUnknown,
		},
		{
			name: "positive low sample stays untrusted for SLA and cache rate",
			components: [][]HourlyPerformanceComponents{{{Hour: currentHour, Components: PerformanceComponents{
				RequestCount: 1, SuccessCount: 1, InputTokens: 70, CacheReadTokens: 30,
			}}}},
			window: UsageWindow24Hours, minSamples: 2,
			wantBuckets: 24, wantLastStatus: MarketplaceHealthUnknown,
		},
		{
			name: "positive sample meeting threshold publishes SLA and cache rate",
			components: [][]HourlyPerformanceComponents{{{Hour: currentHour, Components: PerformanceComponents{
				RequestCount: 1, SuccessCount: 1, InputTokens: 70, CacheReadTokens: 30,
			}}}},
			window: UsageWindow24Hours, minSamples: 1,
			wantSuccessRate: float64Pointer(100), wantCacheRate: float64Pointer(30),
			wantBuckets: 24, wantLastStatus: MarketplaceHealthOperational,
		},
		{
			name:       "seven days emits six hour buckets",
			components: weightedOffers, window: UsageWindow7Days, minSamples: 1,
			wantSuccessRate: float64Pointer(10), wantCacheRate: float64Pointer(30),
			wantBuckets: 28, wantLastStatus: MarketplaceHealthOutage,
		},
		{
			name:       "thirty days emits daily buckets",
			components: weightedOffers, window: UsageWindow30Days, minSamples: 1,
			wantSuccessRate: float64Pointer(10), wantCacheRate: float64Pointer(30),
			wantBuckets: 30, wantLastStatus: MarketplaceHealthOutage,
		},
		{
			name:       "invalid hour fails instead of moving facts into a neighboring bucket",
			components: [][]HourlyPerformanceComponents{{{Hour: currentHour.Add(30 * time.Minute)}}},
			window:     UsageWindow24Hours, minSamples: 1, wantErr: "aligned",
		},
		{
			name: "component overflow fails instead of producing a wrapped rate",
			components: [][]HourlyPerformanceComponents{
				{{Hour: currentHour, Components: PerformanceComponents{RequestCount: math.MaxInt64, SuccessCount: math.MaxInt64}}},
				{{Hour: currentHour, Components: PerformanceComponents{RequestCount: 1, SuccessCount: 1}}},
			},
			window: UsageWindow24Hours, minSamples: 1, wantErr: "overflow",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := projectModelPerformance(test.components, observedUntil, test.window, test.minSamples)
			if test.wantErr != "" {
				require.ErrorContains(t, err, test.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, test.window, got.Window)
			require.Len(t, got.StatusBuckets, test.wantBuckets)
			require.Equal(t, currentHour.Add(time.Hour).Unix(), got.StatusBuckets[len(got.StatusBuckets)-1].EndedAt)
			require.Equal(t, test.wantLastStatus, got.StatusBuckets[len(got.StatusBuckets)-1].Status)
			if test.wantSuccessRate == nil {
				require.Nil(t, got.SuccessRate)
			} else {
				require.InDelta(t, *test.wantSuccessRate, *got.SuccessRate, 0.000001)
			}
			if test.wantCacheRate == nil {
				require.Nil(t, got.CacheHitRate)
			} else {
				require.InDelta(t, *test.wantCacheRate, *got.CacheHitRate, 0.000001)
			}
			for _, bucket := range got.StatusBuckets[:len(got.StatusBuckets)-1] {
				require.Equal(t, MarketplaceHealthUnknown, bucket.Status)
				require.Nil(t, bucket.SuccessRate)
			}
		})
	}
}

func TestPerformanceSummaryValidatesHistogramCountsAndMaxBeforeEstimating(t *testing.T) {
	t.Run("one slot at MaxInt64 is valid", func(t *testing.T) {
		components := PerformanceComponents{
			RequestCount: 1, SuccessCount: 1,
			TTFTHistogram:     PerformanceHistogram{Counts: [17]int64{0: math.MaxInt64}, Max: 49},
			TPSHistogram:      PerformanceHistogram{Counts: [17]int64{0: math.MaxInt64}, Max: 4},
			DurationHistogram: PerformanceHistogram{Counts: [17]int64{0: math.MaxInt64}, Max: 499},
		}

		summary, err := performanceSummary(components, 1)

		require.NoError(t, err)
		require.NotNil(t, summary.TTFTP95Ms)
		require.NotNil(t, summary.TPSP5)
		require.NotNil(t, summary.DurationP95Ms)
	})

	for _, test := range []struct {
		name       string
		components PerformanceComponents
		wantErr    string
	}{
		{
			name: "TTFT cross-slot total overflow",
			components: PerformanceComponents{
				TTFTHistogram: PerformanceHistogram{Counts: [17]int64{0: math.MaxInt64, 1: 1}, Max: 99},
			},
			wantErr: "TTFT histogram",
		},
		{
			name: "TPS negative slot",
			components: PerformanceComponents{
				TPSHistogram: PerformanceHistogram{Counts: [17]int64{2: -1}, Max: 19},
			},
			wantErr: "TPS histogram",
		},
		{
			name: "duration negative max",
			components: PerformanceComponents{
				DurationHistogram: PerformanceHistogram{Counts: [17]int64{16: 1}, Max: -1},
			},
			wantErr: "duration histogram",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := performanceSummary(test.components, 1)
			require.ErrorContains(t, err, test.wantErr)
		})
	}

	t.Run("overflow slots interpolate against their typed max", func(t *testing.T) {
		components := PerformanceComponents{
			RequestCount: 20, SuccessCount: 20,
			TTFTHistogram:     PerformanceHistogram{Counts: [17]int64{16: 20}, Max: 40_000},
			TPSHistogram:      PerformanceHistogram{Counts: [17]int64{16: 20}, Max: 1_000},
			DurationHistogram: PerformanceHistogram{Counts: [17]int64{16: 20}, Max: 400_000},
		}

		summary, err := performanceSummary(components, 1)

		require.NoError(t, err)
		require.Equal(t, int64Pointer(39_500), summary.TTFTP95Ms)
		require.Equal(t, float64Pointer(762), summary.TPSP5)
		require.Equal(t, int64Pointer(395_000), summary.DurationP95Ms)
	})
}

func TestModelPerformanceFinderSnapshotProjectsExactWindowPointsAndCurrentBucket(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	snapshot := performanceFinderTestSnapshot(now)
	hours := snapshot.Offers[performanceFinderPlatformIdentity(41)].HourlyTrendComponents30D
	hours[len(hours)-1].Components = PerformanceComponents{
		RequestCount: 20, SuccessCount: 20, InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
	}
	snapshot.Offers[performanceFinderPlatformIdentity(41)] = GlobalOfferPerformanceSnapshot{HourlyTrendComponents30D: hours}
	finder := NewModelPerformanceFinder(NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) { return snapshot, nil },
	)), nil)
	offer := performanceFinderOffer("offer-platform", performanceFinderPlatformIdentity(41), OfferPlatform)
	currentHour := now.Truncate(time.Hour)

	tests := []struct {
		name       string
		window     UsageWindow
		points     int
		firstStart time.Time
	}{
		{name: "24 hourly points", window: UsageWindow24Hours, points: 24, firstStart: currentHour.Add(-23 * time.Hour)},
		{name: "28 six-hour points", window: UsageWindow7Days, points: 28, firstStart: currentHour.Add(-167 * time.Hour)},
		{name: "30 daily points", window: UsageWindow30Days, points: 30, firstStart: currentHour.Add(-719 * time.Hour)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := finder.Find(context.Background(), []ModelOffer{offer}, test.window, now.Add(20*time.Minute))
			require.NoError(t, err)
			performance := result.Offers[offer.OfferRef]
			require.Len(t, performance.StatusBuckets, test.points)
			require.Len(t, performance.Trend, test.points)
			require.Equal(t, test.firstStart.Unix(), performance.StatusBuckets[0].StartedAt)
			require.Equal(t, currentHour.Add(time.Hour).Unix(), performance.StatusBuckets[test.points-1].EndedAt)
			require.True(t, performance.StatusBuckets[test.points-1].InProgress)
			require.Equal(t, MarketplaceHealthOperational, performance.StatusBuckets[test.points-1].Status)
			empty := performance.Trend[test.points-2]
			require.Equal(t, MarketplaceHealthUnknown, empty.Status)
			require.Nil(t, empty.SuccessRate)
			require.Nil(t, empty.TTFTAvgMs)
			require.Nil(t, empty.TPSAvg)
			require.Equal(t, TokenUnits{}, empty.TokenUnits)
			require.Equal(t, TokenUnits{Input: 1, Output: 2, CacheRead: 3, CacheWrite: 4, Total: 10}, performance.Trend[test.points-1].TokenUnits)
		})
	}
}

func TestModelPerformanceStatusBucketUsesMinSamplesAndExactSLAEdges(t *testing.T) {
	tests := []struct {
		name       string
		requests   int64
		successes  int64
		minSamples int
		want       MarketplaceHealthStatus
	}{
		{name: "empty is unknown", requests: 0, successes: 0, minSamples: 20, want: MarketplaceHealthUnknown},
		{name: "nineteen is below default", requests: 19, successes: 19, minSamples: 20, want: MarketplaceHealthUnknown},
		{name: "twenty meets minimum", requests: 20, successes: 20, minSamples: 20, want: MarketplaceHealthOperational},
		{name: "exactly ninety nine", requests: 100, successes: 99, minSamples: 20, want: MarketplaceHealthOperational},
		{name: "below ninety nine", requests: 100, successes: 98, minSamples: 20, want: MarketplaceHealthDegraded},
		{name: "exactly ninety five", requests: 100, successes: 95, minSamples: 20, want: MarketplaceHealthDegraded},
		{name: "below ninety five", requests: 100, successes: 94, minSamples: 20, want: MarketplaceHealthOutage},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := performanceHealthStatus(PerformanceComponents{
				RequestCount: test.requests, SuccessCount: test.successes,
			}, test.minSamples)
			require.Equal(t, test.want, got)
		})
	}
}

func TestModelPerformanceFinderSnapshotReadsHotMinSamplesAndFiltersModelStatusByVisibleOffers(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	platformIdentity := performanceFinderPlatformIdentity(41)
	privateIdentity := ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPrivate, SourceID: 41}
	snapshot := performanceFinderTestSnapshot(now)
	platform := snapshot.Offers[platformIdentity]
	platform.HourlyTrendComponents30D[len(platform.HourlyTrendComponents30D)-1].Components = PerformanceComponents{RequestCount: 19, SuccessCount: 19}
	snapshot.Offers[platformIdentity] = platform
	private := performanceFinderEmptyOfferSnapshot(now)
	private.HourlyTrendComponents30D[len(private.HourlyTrendComponents30D)-1].Components = PerformanceComponents{RequestCount: 20, SuccessCount: 0}
	snapshot.Offers[privateIdentity] = private
	settings := app.NewMasterSettingsSnapshot()
	settings.Replace(map[string]string{"model_marketplace_min_samples": "20"})
	var loads atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) {
		loads.Add(1)
		return snapshot, nil
	}))
	finder := NewModelPerformanceFinder(cache, settings)
	platformOffer := performanceFinderOffer("platform", platformIdentity, OfferPlatform)
	privateOffer := performanceFinderOffer("private", privateIdentity, OfferOwned)

	before, err := finder.Find(context.Background(), []ModelOffer{platformOffer}, UsageWindow24Hours, now)
	require.NoError(t, err)
	require.Equal(t, MarketplaceHealthUnknown, before.ModelStatus)
	settings.Update(map[string]string{"model_marketplace_min_samples": "10"})
	after, err := finder.Find(context.Background(), []ModelOffer{platformOffer}, UsageWindow24Hours, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, MarketplaceHealthOperational, after.ModelStatus,
		"status must be reprojected with the live setting without refreshing raw components")
	require.Equal(t, int64(1), loads.Load())

	privateOnly, err := finder.Find(context.Background(), []ModelOffer{privateOffer}, UsageWindow24Hours, now.Add(2*time.Minute))
	require.NoError(t, err)
	require.Equal(t, MarketplaceHealthOutage, privateOnly.ModelStatus,
		"an invisible healthy platform Offer must not change the visible private Offer status")
	both, err := finder.Find(context.Background(), []ModelOffer{privateOffer, platformOffer}, UsageWindow24Hours, now.Add(3*time.Minute))
	require.NoError(t, err)
	require.Equal(t, MarketplaceHealthOperational, both.ModelStatus,
		"visible model status priority is operational > degraded > outage > unknown")
}

func TestModelPerformanceFinderSnapshotOrdinaryJSONNeverContainsSamplesOrInternalIdentity(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	snapshot := performanceFinderTestSnapshot(now)
	offerSnapshot := snapshot.Offers[performanceFinderPlatformIdentity(41)]
	offerSnapshot.HourlyTrendComponents30D[len(offerSnapshot.HourlyTrendComponents30D)-1].Components = PerformanceComponents{
		RequestCount: 20, SuccessCount: 19, InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
	}
	snapshot.Offers[performanceFinderPlatformIdentity(41)] = offerSnapshot
	finder := NewModelPerformanceFinder(NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) { return snapshot, nil },
	)), nil)
	offer := performanceFinderOffer("safe-ref", performanceFinderPlatformIdentity(41), OfferPlatform)

	result, err := finder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now)
	require.NoError(t, err)
	payload, err := json.Marshal(result)
	require.NoError(t, err)
	serialized := string(payload)
	for _, forbidden := range []string{
		"request_count", "success_count", "failed_count", "sample_count",
		"failure_count", "stream_request_count", "ttft_sample_count", "tps_sample_count",
		"duration_sample_count", "sum_first_response_ms", "source_id", "model_name",
	} {
		require.NotContains(t, serialized, forbidden)
	}
	require.Contains(t, serialized, `"safe-ref"`)
	require.Contains(t, serialized, `"cache_read":3`)
}

func TestModelPerformanceFinderSnapshotDegradesCacheFailuresWithoutDiscardingMarketplaceResult(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	loadErr := errors.New("performance facts unavailable")
	var calls atomic.Int64
	cache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		if calls.Add(1) == 1 {
			return nil, loadErr
		}
		return performanceFinderTestSnapshot(observedUntil), nil
	}))
	cache.clock = newMutablePerformanceClock(now)
	finder := NewModelPerformanceFinder(cache, nil)
	offer := performanceFinderOffer("offer", performanceFinderPlatformIdentity(41), OfferPlatform)

	unavailable, err := finder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now)
	require.ErrorIs(t, err, loadErr, "the error remains available for diagnostics")
	require.Equal(t, PerformanceUnavailable, unavailable.PerformanceStatus)
	require.Equal(t, MarketplaceHealthUnknown, unavailable.ModelStatus)
	require.Empty(t, unavailable.Offers, "callers can still return catalog and pricing while omitting performance")

	available, err := finder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now.Add(time.Minute))
	require.NoError(t, err)
	require.Equal(t, PerformanceAvailable, available.PerformanceStatus)
	refreshedAt := available.RefreshedAt

	// A separate cache demonstrates the old-value failure path without changing
	// the cold-failure state machine asserted above.
	var staleCalls atomic.Int64
	staleCache := NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(func(_ context.Context, observedUntil time.Time) (*GlobalModelPerformanceSnapshot, error) {
		if staleCalls.Add(1) == 1 {
			return performanceFinderTestSnapshot(observedUntil), nil
		}
		return nil, loadErr
	}))
	staleFinder := NewModelPerformanceFinder(staleCache, nil)
	_, err = staleFinder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now)
	require.NoError(t, err)
	stale, err := staleFinder.Find(context.Background(), []ModelOffer{offer}, UsageWindow24Hours, now.Add(time.Hour))
	require.ErrorIs(t, err, loadErr)
	require.Equal(t, PerformanceStale, stale.PerformanceStatus)
	require.Equal(t, refreshedAt.Add(-time.Minute), stale.RefreshedAt)
	require.Contains(t, stale.Offers, offer.OfferRef, "stale raw components remain projectable")
}

func TestModelPerformanceFinderSnapshotRejectsInvalidWindowAndIdentityBeforeCacheRead(t *testing.T) {
	now := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	var loads atomic.Int64
	finder := NewModelPerformanceFinder(NewGlobalModelPerformanceCache(PerformanceSnapshotLoaderFunc(
		func(context.Context, time.Time) (*GlobalModelPerformanceSnapshot, error) {
			loads.Add(1)
			return performanceFinderTestSnapshot(now), nil
		},
	)), nil)

	_, err := finder.Find(context.Background(), nil, UsageWindow("yesterday"), now)
	require.ErrorContains(t, err, "window")
	malformed := performanceFinderOffer("bad", performanceFinderPlatformIdentity(41), OfferPlatform)
	malformed.Identity.ModelName = ""
	_, err = finder.Find(context.Background(), []ModelOffer{malformed}, UsageWindow24Hours, now)
	require.Error(t, err)
	require.Zero(t, loads.Load())
}

func performanceFinderTestSnapshot(observedUntil time.Time) *GlobalModelPerformanceSnapshot {
	return &GlobalModelPerformanceSnapshot{
		ObservedUntil: observedUntil,
		Offers: map[ModelOfferIdentity]GlobalOfferPerformanceSnapshot{
			performanceFinderPlatformIdentity(41): performanceFinderEmptyOfferSnapshot(observedUntil),
		},
	}
}

func performanceFinderEmptyOfferSnapshot(observedUntil time.Time) GlobalOfferPerformanceSnapshot {
	currentHour := observedUntil.UTC().Truncate(time.Hour)
	hours := make([]HourlyPerformanceComponents, MarketplacePerformanceHours)
	for i := range hours {
		hours[i].Hour = currentHour.Add(time.Duration(i-(MarketplacePerformanceHours-1)) * time.Hour)
	}
	return GlobalOfferPerformanceSnapshot{HourlyTrendComponents30D: hours}
}

func performanceFinderPlatformIdentity(sourceID uint) ModelOfferIdentity {
	return ModelOfferIdentity{ModelName: "gpt-4o", Kind: OfferKindPlatform, SourceID: sourceID}
}

func performanceFinderOffer(ref string, identity ModelOfferIdentity, ownership ModelOfferOwnership) ModelOffer {
	return ModelOffer{
		OfferRef: ref, Kind: identity.Kind, DisplayName: strings.ToUpper(ref), Ownership: ownership, Available: true,
		SupportedEndpoints: []SupportedEndpoint{EndpointChatCompletions}, Identity: identity,
	}
}

func int64Pointer(value int64) *int64       { return &value }
func float64Pointer(value float64) *float64 { return &value }
