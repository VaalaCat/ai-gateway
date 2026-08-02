package dao

import (
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupFinalReviewStats(t *testing.T) (*adminStatsQuery, *gorm.DB, *gorm.DB) {
	t.Helper()
	core, logDB := setupStrictSplitDBs(t)
	provider := &testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}
	return NewAdminQuery(NewContext(provider)).Stats().(*adminStatsQuery), core, logDB
}

func createFinalReviewRequest(t *testing.T, db *gorm.DB, log models.UsageLog) {
	t.Helper()
	request := models.RequestLog(log)
	require.NoError(t, db.Select("*").Create(&request).Error)
}

func TestHourlyTrendUserDayMergesDailyInteriorWithExactBoundaryFacts(t *testing.T) {
	q, _, logDB := setupFinalReviewStats(t)
	for _, row := range []models.TokenDailyBilling{
		{Date: "2026-07-29", UserID: 7, TokenID: 1, RequestCount: 101, PromptTokens: 1001, TotalCost: 1010},
		{Date: "2026-07-30", UserID: 7, TokenID: 1, RequestCount: 2, PromptTokens: 20, CompletionTokens: 30, CacheReadTokens: 4, CacheWriteTokens: 5, TotalCost: 50},
		{Date: "2026-07-31", UserID: 7, TokenID: 1, RequestCount: 101, CompletionTokens: 1002, TotalCost: 1020},
	} {
		require.NoError(t, logDB.Create(&row).Error)
	}

	timestamps := map[string]int64{
		"before-start": time.Date(2026, 7, 29, 11, 59, 59, 0, time.UTC).Unix(),
		"at-start":     time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC).Unix(),
		"full-day-raw": time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC).Unix(),
		"before-end":   time.Date(2026, 7, 31, 11, 59, 59, 0, time.UTC).Unix(),
		"at-end":       time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC).Unix(),
	}
	requests := []models.UsageLog{
		{RequestID: "before-start", UserID: 7, TokenID: 1, PromptTokens: 1000, TotalCost: 1000, Status: 1, CreatedAt: timestamps["before-start"]},
		{RequestID: "at-start", UserID: 7, TokenID: 1, PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, TotalCost: 10, Status: 1, CreatedAt: timestamps["at-start"]},
		// The complete middle day deliberately disagrees with request facts: the
		// result must keep using token_daily_billings for complete UTC days.
		{RequestID: "full-day-raw", UserID: 7, TokenID: 1, PromptTokens: 9999, TotalCost: 9999, Status: 1, CreatedAt: timestamps["full-day-raw"]},
		{RequestID: "before-end", UserID: 7, TokenID: 1, PromptTokens: 2, CompletionTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5, TotalCost: 20, Status: 1, CreatedAt: timestamps["before-end"]},
		{RequestID: "at-end", UserID: 7, TokenID: 1, CompletionTokens: 1000, TotalCost: 1000, Status: 1, CreatedAt: timestamps["at-end"]},
	}
	for _, request := range requests {
		createFinalReviewRequest(t, logDB, request)
	}

	got, err := q.HourlyTrend(ObsRange{
		Start: timestamps["at-start"], End: timestamps["at-end"], Gran: GranDay,
	}, Scope{UserID: 7}, ObsFilter{})

	require.NoError(t, err)
	require.Equal(t, []TimeBucket{
		{Ts: time.Date(2026, 7, 29, 0, 0, 0, 0, time.UTC).Unix(), Label: "2026-07-29", Cost: 10, Requests: 1, Tokens: 10, PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, CacheHitRate: 75},
		{Ts: time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC).Unix(), Label: "2026-07-30", Cost: 50, Requests: 2, Tokens: 59, PromptTokens: 20, CompletionTokens: 30, CacheReadTokens: 4, CacheWriteTokens: 5, CacheHitRate: 100.0 * 4 / 24},
		{Ts: time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC).Unix(), Label: "2026-07-31", Cost: 20, Requests: 1, Tokens: 14, PromptTokens: 2, CompletionTokens: 3, CacheReadTokens: 4, CacheWriteTokens: 5, CacheHitRate: 100.0 * 4 / 6},
	}, got)
}

func TestSpeedCompareMergesFullHourHistogramsWithExactBoundarySamples(t *testing.T) {
	q, _, logDB := setupFinalReviewStats(t)
	start, end := seedFinalReviewSpeedWindow(t, logDB)
	r := ObsRange{Start: start, End: end, Gran: GranHour}

	for _, dimension := range []string{"model", "channel"} {
		t.Run(dimension, func(t *testing.T) {
			got, err := q.SpeedCompare(dimension, r, Scope{IsAdmin: true}, 10, ObsFilter{})
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Equal(t, int64(200), got[0].TTFTMs)
			require.InDelta(t, 50, got[0].TPS, 0.0001)
			require.Equal(t, int64(459), got[0].TTFTP95Ms)
			require.Equal(t, float64(51), got[0].TPSP5)
			if dimension == "model" {
				require.Equal(t, "exact-model", got[0].Name)
			} else {
				require.Equal(t, uint(5), got[0].ID)
				require.Equal(t, "exact-channel", got[0].Name)
			}
		})
	}
}

func TestMonitoringMetricsMergeFullHoursWithExactBoundaryFacts(t *testing.T) {
	q, core, logDB := setupFinalReviewStats(t)
	start, end := seedFinalReviewSpeedWindow(t, logDB)
	require.NoError(t, core.Create(&models.Agent{AgentID: "exact-agent", Name: "Exact Agent", Status: 1, LastSeen: 42}).Error)
	r := ObsRange{Start: start, End: end, Gran: GranHour}

	channels, err := q.ChannelMetrics(r)
	require.NoError(t, err)
	require.Len(t, channels, 1)
	require.Equal(t, uint(5), channels[0].ID)
	require.Equal(t, int64(4), channels[0].Requests)
	require.Equal(t, int64(200), channels[0].TTFTAvgMs)
	require.InDelta(t, 50, channels[0].TPSAvg, 0.0001)
	require.Equal(t, int64(459), channels[0].TTFTP95Ms)
	require.Equal(t, float64(51), channels[0].TPSP5)
	require.Equal(t, int64(1950), channels[0].LatencyP95Ms)
	require.Equal(t, int64(4), sumFinalReviewSpark(channels[0].Spark24h))

	agents, err := q.AgentMetrics(r)
	require.NoError(t, err)
	require.Len(t, agents, 1)
	require.Equal(t, "exact-agent", agents[0].ID)
	require.Equal(t, "Exact Agent", agents[0].Name)
	require.Equal(t, int64(4), agents[0].Requests)
	require.Equal(t, int64(200), agents[0].TTFTAvgMs)
	require.InDelta(t, 50, agents[0].TPSAvg, 0.0001)
	require.Equal(t, int64(459), agents[0].TTFTP95Ms)
	require.Equal(t, float64(51), agents[0].TPSP5)
	require.Equal(t, int64(1950), agents[0].LatencyP95Ms)
	require.Equal(t, int64(4), sumFinalReviewSpark(agents[0].Spark24h))
}

func TestChannelModelBreakdownUsesExactFactsForPartialHour(t *testing.T) {
	q, _, logDB := setupFinalReviewStats(t)
	hour := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	rawInput, rawOutput := int64(12), int64(18)
	for index, request := range []models.UsageLog{
		{RequestID: "breakdown-before", ChannelID: 5, ModelName: "exact-model", PromptTokens: 1000, TotalCost: 1000, Status: 1, CreatedAt: hour.Add(20 * time.Minute).Unix()},
		{RequestID: "breakdown-inside", ChannelID: 5, ModelName: "exact-model", PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, TotalCost: 10, RawInputCost: &rawInput, RawOutputCost: &rawOutput, Status: 1, CreatedAt: hour.Add(35 * time.Minute).Unix()},
		{RequestID: "breakdown-at-end", ChannelID: 5, ModelName: "exact-model", CompletionTokens: 1000, TotalCost: 1000, Status: 1, CreatedAt: hour.Add(45 * time.Minute).Unix()},
	} {
		request.RequestID = fmt.Sprintf("%s-%d", request.RequestID, index)
		createFinalReviewRequest(t, logDB, request)
	}
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{
		Date: "2026-07-30", Hour: 10, ChannelID: 5, ModelName: "exact-model", AgentID: "exact-agent",
		RequestCount: 3, PromptTokens: 1001, CompletionTokens: 1002, CacheReadTokens: 3, CacheWriteTokens: 4,
		TotalCost: 2010, RawCost: 2030,
	}).Error)

	got, err := q.ChannelModelBreakdown(5, ObsRange{
		Start: hour.Add(30 * time.Minute).Unix(), End: hour.Add(45 * time.Minute).Unix(), Gran: GranHour,
	})

	require.NoError(t, err)
	require.Equal(t, []ChannelModelBreakdownRow{{
		ModelName: "exact-model", Requests: 1, PromptTokens: 1, CompletionTokens: 2,
		CacheReadTokens: 3, CacheWriteTokens: 4, TotalCost: 10, RawCost: 30,
	}}, got)
}

func seedFinalReviewSpeedWindow(t *testing.T, db *gorm.DB) (int64, int64) {
	t.Helper()
	base := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	requests := []models.UsageLog{
		{RequestID: "speed-before", CreatedAt: base.Add(29*time.Minute + 59*time.Second).Unix(), FirstResponseMs: 30000, Duration: 31000, CompletionTokens: 500},
		{RequestID: "speed-start", CreatedAt: base.Add(30 * time.Minute).Unix(), FirstResponseMs: 100, Duration: 1100, CompletionTokens: 50},
		{RequestID: "speed-end-inside", CreatedAt: base.Add(2*time.Hour + 29*time.Minute + 59*time.Second).Unix(), FirstResponseMs: 300, Duration: 1300, CompletionTokens: 50},
		{RequestID: "speed-at-end", CreatedAt: base.Add(2*time.Hour + 30*time.Minute).Unix(), FirstResponseMs: 30000, Duration: 31000, CompletionTokens: 500},
	}
	for _, request := range requests {
		request.ChannelID = 5
		request.ChannelName = "exact-channel"
		request.ModelName = "exact-model"
		request.AgentID = "exact-agent"
		request.OwnerType = "admin"
		request.Status = 1
		request.IsStream = true
		request.PromptTokens = 1
		request.TotalCost = 1
		createFinalReviewRequest(t, db, request)
	}

	for _, bucket := range []models.UsageHourlyBucket{
		{Date: "2026-07-30", Hour: 10, RequestCount: 2, SuccessCount: 2, StreamRequestCount: 2, SumFirstResponseMs: 30100, SumGenerationMs: 2000, SumStreamCompletionTokens: 550},
		{Date: "2026-07-30", Hour: 11, RequestCount: 2, SuccessCount: 2, StreamRequestCount: 2, SumFirstResponseMs: 400, SumGenerationMs: 2000, SumStreamCompletionTokens: 100},
		{Date: "2026-07-30", Hour: 12, RequestCount: 2, SuccessCount: 2, StreamRequestCount: 2, SumFirstResponseMs: 30300, SumGenerationMs: 2000, SumStreamCompletionTokens: 550},
	} {
		bucket.ChannelID = 5
		bucket.ChannelName = "exact-channel"
		bucket.ModelName = "exact-model"
		bucket.AgentID = "exact-agent"
		bucket.OwnerType = "admin"
		require.NoError(t, db.Create(&bucket).Error)
	}
	for _, histogram := range []models.UsageTTFTHistogram{
		{Date: "2026-07-30", Hour: 10, H2: 1, H16: 1, MaxFirstResponseMs: 30000},
		{Date: "2026-07-30", Hour: 11, H3: 2, MaxFirstResponseMs: 200},
		{Date: "2026-07-30", Hour: 12, H4: 1, H16: 1, MaxFirstResponseMs: 30000},
	} {
		histogram.ChannelID, histogram.ModelName, histogram.AgentID = 5, "exact-model", "exact-agent"
		require.NoError(t, db.Create(&histogram).Error)
	}
	for _, histogram := range []models.UsageTPSHistogram{
		{Date: "2026-07-30", Hour: 10, H6: 1, H15: 1, MaxTps: 500},
		{Date: "2026-07-30", Hour: 11, H6: 2, MaxTps: 50},
		{Date: "2026-07-30", Hour: 12, H6: 1, H15: 1, MaxTps: 500},
	} {
		histogram.ChannelID, histogram.ModelName, histogram.AgentID = 5, "exact-model", "exact-agent"
		require.NoError(t, db.Create(&histogram).Error)
	}
	for _, histogram := range []models.UsageDurationHistogram{
		{Date: "2026-07-30", Hour: 10, H2: 1, H10: 1, MaxDurationMs: 31000},
		{Date: "2026-07-30", Hour: 11, H2: 2, MaxDurationMs: 1200},
		{Date: "2026-07-30", Hour: 12, H2: 1, H10: 1, MaxDurationMs: 31000},
	} {
		histogram.ChannelID, histogram.ModelName, histogram.AgentID = 5, "exact-model", "exact-agent"
		require.NoError(t, db.Create(&histogram).Error)
	}
	return base.Add(30 * time.Minute).Unix(), base.Add(2*time.Hour + 30*time.Minute).Unix()
}

func sumFinalReviewSpark(values []int64) int64 {
	var total int64
	for _, value := range values {
		total += value
	}
	return total
}
