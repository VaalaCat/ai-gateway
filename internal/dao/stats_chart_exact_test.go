package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestFoldDistributionBucketsAlwaysCalculatesRatios(t *testing.T) {
	tests := []struct {
		name    string
		buckets []Bucket
		topN    int
		want    []Bucket
	}{
		{
			name:    "fewer than top n",
			buckets: []Bucket{{Name: "a", Value: 3}, {Name: "b", Value: 1}},
			topN:    3,
			want:    []Bucket{{Name: "a", Value: 3, Ratio: 0.75}, {Name: "b", Value: 1, Ratio: 0.25}},
		},
		{
			name:    "equal to top n",
			buckets: []Bucket{{Name: "a", Value: 2}, {Name: "b", Value: 1}},
			topN:    2,
			want:    []Bucket{{Name: "a", Value: 2, Ratio: 2.0 / 3}, {Name: "b", Value: 1, Ratio: 1.0 / 3}},
		},
		{
			name:    "greater than top n",
			buckets: []Bucket{{Name: "a", Value: 4}, {Name: "b", Value: 2}, {Name: "c", Value: 1}},
			topN:    2,
			want:    []Bucket{{Name: "a", Value: 4, Ratio: 4.0 / 7}, {Name: "b", Value: 2, Ratio: 2.0 / 7}, {Name: "others", Value: 1, Ratio: 1.0 / 7}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foldDistributionBuckets(tt.buckets, tt.topN)
			require.Equal(t, tt.want, got)
			var sum float64
			for _, bucket := range got {
				sum += bucket.Ratio
			}
			require.InDelta(t, 1, sum, 1e-12)
		})
	}

	require.Equal(t, []Bucket{{Name: "zero", Value: 0}}, foldDistributionBuckets([]Bucket{{Name: "zero", Value: 0}}, 5))
	require.Empty(t, foldDistributionBuckets(nil, 5))
}

func TestBillingTokenTrendUsesCoreExactFactsWithoutLogDatabase(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Stats()
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "inside", UserID: 2, TokenID: 7, ModelName: "m", PromptTokens: 3, CompletionTokens: 4, CacheReadTokens: 5, CacheWriteTokens: 6, TotalCost: 9, CreatedAt: base + 2}).Error)
	require.NoError(t, core.Create(&models.BillingLog{RequestID: "outside", UserID: 2, TokenID: 7, ModelName: "m", PromptTokens: 100, TotalCost: 100, CreatedAt: base + 1}).Error)
	got, err := q.HourlyTrend(ObsRange{Start: base + 2, End: base + 3, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{UserID: 2, TokenID: 7, ModelName: "m"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[0].Requests)
	require.Equal(t, int64(18), got[0].Tokens)
	require.Equal(t, int64(9), got[0].Cost)
	require.Zero(t, got[0].TTFTMs)
	require.Zero(t, got[0].TPS)
}

func TestModelDistributionUsesCoreExactWindowAndFoldsOthers(t *testing.T) {
	q, core := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	for _, row := range []models.BillingLog{
		{RequestID: "outside", ModelName: "outside", CreatedAt: base + 1},
		{RequestID: "a1", ModelName: "a", CreatedAt: base + 2},
		{RequestID: "a2", ModelName: "a", CreatedAt: base + 3},
		{RequestID: "b1", ModelName: "b", CreatedAt: base + 4},
	} {
		require.NoError(t, core.Create(&row).Error)
	}
	got, err := q.ModelDistribution(ObsRange{Start: base + 2, End: base + 5, Gran: GranHour}, Scope{IsAdmin: true}, 1, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, []Bucket{{Name: "a", Value: 2, Ratio: 2.0 / 3}, {Name: "others", Value: 1, Ratio: 1.0 / 3}}, got)
}

func TestMarketShareUsesAllTokenKindsExactWindowAndKeepsChannelIdentity(t *testing.T) {
	q, core := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	rows := []models.BillingLog{
		{RequestID: "outside", ChannelID: 3, ChannelName: "same", PromptTokens: 999, CreatedAt: base + 1},
		{RequestID: "cache-wins", ChannelID: 2, ChannelName: "same", PromptTokens: 1, CacheReadTokens: 100, CreatedAt: base + 2},
		{RequestID: "prompt", ChannelID: 1, ChannelName: "same", PromptTokens: 50, CreatedAt: base + 3},
	}
	for i := range rows {
		require.NoError(t, core.Create(&rows[i]).Error)
	}
	got, err := q.MarketShareTrend("channel", ObsRange{Start: base + 2, End: base + 4, Gran: GranHour}, Scope{IsAdmin: true}, 1, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"same (#2)", "others"}, got.SeriesOrder)
	require.Equal(t, int64(101), got.Buckets[0].Series["same (#2)"])
	require.Equal(t, int64(50), got.Buckets[0].Series["others"])
}

func TestMetricTrendUsesExactBoundaryRequestLogsAndDisambiguatesChannels(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Stats()
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	for _, row := range []models.RequestLog{
		models.RequestLog(models.UsageLog{RequestID: "outside", ChannelID: 1, ChannelName: "same", ModelName: "m", TotalCost: 99, CreatedAt: base + 1}),
		models.RequestLog(models.UsageLog{RequestID: "inside-1", ChannelID: 1, ChannelName: "same", ModelName: "m", TotalCost: 3, CreatedAt: base + 2}),
		models.RequestLog(models.UsageLog{RequestID: "inside-2", ChannelID: 2, ChannelName: "same", ModelName: "m", TotalCost: 4, CreatedAt: base + 3}),
	} {
		require.NoError(t, logDB.Create(&row).Error)
	}
	r := ObsRange{Start: base + 2, End: base + 4, Gran: GranHour}
	cost, err := q.MetricTrendGrouped("cost", "sum", "channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	requests, err := q.MetricTrendGrouped("requests", "sum", "channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"same (#1)", "same (#2)"}, cost.SeriesOrder)
	require.Equal(t, cost.SeriesOrder, requests.SeriesOrder)
	require.Equal(t, float64(3), cost.Buckets[0].Series["same (#1)"])
	require.Equal(t, float64(4), cost.Buckets[0].Series["same (#2)"])
}

func TestChannelDimensionsKeepAdminAndPrivateChannelIdentity(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	date := "2026-07-20"
	start := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC).Unix()
	type channelFact struct {
		ownerType        string
		channelID        uint
		privateChannelID uint
		name             string
		value            int64
	}
	facts := []channelFact{
		{ownerType: "admin", channelID: 7, name: "same", value: 50},
		{ownerType: "private", privateChannelID: 7, name: "same", value: 40},
		{ownerType: "private", privateChannelID: 8, name: "same", value: 30},
		{ownerType: "private", privateChannelID: 9, name: "alpha", value: 20},
		{ownerType: "private", privateChannelID: 10, name: "beta", value: 10},
	}
	for i, fact := range facts {
		require.NoError(t, db.Create(&models.BillingHourlyBucket{
			Date: date, Hour: 10, UserID: uint(i + 1), TokenID: uint(i + 1),
			OwnerType: fact.ownerType, ChannelID: fact.channelID, PrivateChannelID: fact.privateChannelID,
			ChannelName: fact.name, ModelName: "m", PromptTokens: fact.value, RequestCount: fact.value,
		}).Error)
		require.NoError(t, db.Create(&models.UsageHourlyBucket{
			Date: date, Hour: 10, AgentID: fmt.Sprintf("agent-%d", i),
			OwnerType: fact.ownerType, ChannelID: fact.channelID, PrivateChannelID: fact.privateChannelID,
			ChannelName: fact.name, ModelName: "m", TotalCost: fact.value, RequestCount: fact.value,
		}).Error)
	}
	r := ObsRange{Start: start, End: start + 86400, Gran: GranDay}

	market, err := q.MarketShareTrend("channel", r, Scope{IsAdmin: true}, 3, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, []string{"same (#7)", "same (#private:7)", "same (#private:8)", "others"}, market.SeriesOrder)
	require.Equal(t, int64(50), market.Buckets[0].Series["same (#7)"])
	require.Equal(t, int64(40), market.Buckets[0].Series["same (#private:7)"])
	require.Equal(t, int64(30), market.Buckets[0].Series["same (#private:8)"])
	require.Equal(t, int64(30), market.Buckets[0].Series["others"])
	marketAll, err := q.MarketShareTrend("channel", r, Scope{IsAdmin: true}, 10, ObsFilter{})
	require.NoError(t, err)
	require.Contains(t, marketAll.SeriesOrder, "alpha")
	require.Contains(t, marketAll.SeriesOrder, "beta")

	metric, err := q.MetricTrendGrouped("cost", "sum", "channel", r, Scope{IsAdmin: true}, 3, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, market.SeriesOrder, metric.SeriesOrder)
	require.Equal(t, float64(50), metric.Buckets[0].Series["same (#7)"])
	require.Equal(t, float64(40), metric.Buckets[0].Series["same (#private:7)"])
	require.Equal(t, float64(30), metric.Buckets[0].Series["same (#private:8)"])
	require.Equal(t, float64(30), metric.Buckets[0].Series["others"])
	metricAll, err := q.MetricTrendGrouped("cost", "sum", "channel", r, Scope{IsAdmin: true}, 10, ObsFilter{})
	require.NoError(t, err)
	require.Contains(t, metricAll.SeriesOrder, "alpha")
	require.Contains(t, metricAll.SeriesOrder, "beta")
}

func TestMetricTrendPartialSpeedEligibilityMatchesHourlyRollup(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Stats()
	base := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-07-20", Hour: 10, ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", AgentID: "a",
		RequestCount: 6, SumFirstResponseMs: 400, StreamRequestCount: 3,
		SumStreamCompletionTokens: 60, SumGenerationMs: 1800,
	}).Error)
	raw := []models.UsageLog{
		{RequestID: "ttft-only", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: true, Status: 1, FirstResponseMs: 100, Duration: 500},
		{RequestID: "tps-zero-first", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: true, Status: 1, FirstResponseMs: 0, CompletionTokens: 20, Duration: 1000},
		{RequestID: "nonpositive-generation", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: true, Status: 1, FirstResponseMs: 100, CompletionTokens: 30, Duration: 100},
		{RequestID: "failed", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: true, Status: 0, FirstResponseMs: 200, CompletionTokens: 40, Duration: 1000},
		{RequestID: "nonstream", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: false, Status: 1, FirstResponseMs: 300, CompletionTokens: 50, Duration: 1000},
		{RequestID: "both", ChannelID: 1, OwnerType: "admin", ChannelName: "channel", ModelName: "m", IsStream: true, Status: 1, FirstResponseMs: 200, CompletionTokens: 40, Duration: 1000},
	}
	for i := range raw {
		raw[i].CreatedAt = base + 3600 + int64(i+1)
		require.NoError(t, db.Create(&raw[i]).Error)
	}
	r := ObsRange{Start: base, End: base + 3600 + 30, Gran: GranHour}

	ttftRows, err := metricTrendStackRows(db, "usage_logs", "ttft", "channel", r, 0, "")
	require.NoError(t, err)
	require.Len(t, ttftRows, 2)
	for _, row := range ttftRows {
		require.Equal(t, float64(400), row.Num)
		require.Equal(t, float64(3), row.Den)
	}
	tpsRows, err := metricTrendStackRows(db, "usage_logs", "tps", "channel", r, 0, "")
	require.NoError(t, err)
	require.Len(t, tpsRows, 2)
	for _, row := range tpsRows {
		require.Equal(t, float64(60000), row.Num)
		require.Equal(t, float64(1800), row.Den)
	}

	ttft, err := q.MetricTrendGrouped("ttft", "avg", "channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	tps, err := q.MetricTrendGrouped("tps", "avg", "channel", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, ttft.Buckets, 2)
	require.Len(t, tps.Buckets, 2)
	for i := range ttft.Buckets {
		require.InDelta(t, 400.0/3, ttft.Buckets[i].Series["channel"], 1e-9)
		require.InDelta(t, 60000.0/1800, tps.Buckets[i].Series["channel"], 1e-9)
	}
}
