package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestDashboardSuccessRateUsesLayoutRequestLogModel(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout app.DatabaseLayoutMode
	}{
		{name: "legacy", layout: app.DatabaseLayoutLegacySingle},
		{name: "split", layout: app.DatabaseLayoutSplit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var core, logDB *gorm.DB
			if tc.layout == app.DatabaseLayoutSplit {
				core, logDB = setupStrictSplitDBs(t)
			} else {
				core = setupTestDB(t)
				logDB = core
			}
			q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: tc.layout})).Stats()
			start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
			rows := []models.UsageLog{
				{RequestID: tc.name + "-success-1", UserID: 7, ModelName: "wanted", Status: 1, CreatedAt: start},
				{RequestID: tc.name + "-success-2", UserID: 7, ModelName: "wanted", Status: 1, CreatedAt: start + 3599},
				{RequestID: tc.name + "-before", UserID: 7, ModelName: "wanted", Status: 1, CreatedAt: start - 1},
				{RequestID: tc.name + "-at-end", UserID: 7, ModelName: "wanted", Status: 1, CreatedAt: start + 3600},
				{RequestID: tc.name + "-failed", UserID: 7, ModelName: "wanted", Status: 0, CreatedAt: start + 1},
				{RequestID: tc.name + "-other-user", UserID: 8, ModelName: "wanted", Status: 1, CreatedAt: start + 1},
				{RequestID: tc.name + "-other-model", UserID: 7, ModelName: "other", Status: 1, CreatedAt: start + 1},
			}
			for _, row := range rows {
				if tc.layout == app.DatabaseLayoutSplit {
					request := models.RequestLog(row)
					require.NoError(t, logDB.Create(&request).Error)
				} else {
					require.NoError(t, logDB.Create(&row).Error)
				}
			}

			got, err := q.DashboardSuccessRate(
				ObsRange{Start: start, End: start + 3600, Gran: GranHour},
				Scope{IsAdmin: true},
				ObsFilter{UserID: 7, ModelName: "wanted"},
			)

			require.NoError(t, err)
			require.Equal(t, KpiMetric{Value: 2, Spark: []int64{2}}, got)
		})
	}
}

func TestDashboardSuccessRateGlobalModelFilterUsesRequestLogs(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Stats()
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{
		Date: "2026-07-23", Hour: 10, ChannelID: 1, ModelName: "wanted", AgentID: "stale",
		RequestCount: 99, SuccessCount: 99,
	}).Error)
	for _, row := range []models.RequestLog{
		models.RequestLog(models.UsageLog{RequestID: "wanted-success", UserID: 7, ModelName: "wanted", Status: 1, CreatedAt: start + 1}),
		models.RequestLog(models.UsageLog{RequestID: "wanted-failed", UserID: 8, ModelName: "wanted", Status: 0, CreatedAt: start + 2}),
		models.RequestLog(models.UsageLog{RequestID: "other-success", UserID: 9, ModelName: "other", Status: 1, CreatedAt: start + 3}),
	} {
		require.NoError(t, logDB.Create(&row).Error)
	}

	got, err := q.DashboardSuccessRate(
		ObsRange{Start: start, End: start + 3600, Gran: GranHour},
		Scope{IsAdmin: true},
		ObsFilter{ModelName: "wanted"},
	)

	require.NoError(t, err)
	require.Equal(t, KpiMetric{Value: 1, Spark: []int64{1}}, got)
}

func TestDashboardSuccessRateSplitEmptyRequestLogs(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Stats()
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()

	got, err := q.DashboardSuccessRate(
		ObsRange{Start: start, End: start + 3600, Gran: GranHour},
		Scope{IsAdmin: true},
		ObsFilter{UserID: 7},
	)

	require.NoError(t, err)
	require.Equal(t, KpiMetric{Spark: []int64{}}, got)
}

func TestDashboardSuccessRateReturnsLogDatabaseUnavailable(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Stats()

	_, err := q.DashboardSuccessRate(
		ObsRange{Start: 1, End: 2, Gran: GranHour},
		Scope{IsAdmin: true},
		ObsFilter{UserID: 7},
	)

	require.ErrorIs(t, err, ErrLogDatabaseUnavailable)
}

func TestDashboardStatisticsConsumersReadOnlyLogDatabase(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	forbidCoreBillingFactQueries(t, core)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, core.Create(&models.User{ID: 1, Username: "one"}).Error)
	require.NoError(t, logDB.Create(&models.UsageHourlyBucket{Date: "2026-07-23", Hour: 10, ChannelID: 1, ModelName: "m", RequestCount: 1, SuccessCount: 1}).Error)
	require.NoError(t, logDB.Create(&models.RequestLog{RequestID: "one", UserID: 1, TokenID: 2, ChannelID: 1, ModelName: "m", Status: 1, CreatedAt: start}).Error)
	q := NewAdminQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit})).Stats()
	r := ObsRange{Start: start, End: start + 3600, Gran: GranHour}

	_, err := q.DashboardKpis(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	_, err = q.MarketShareTrend("model", r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	_, err = q.ModelDistribution(r, Scope{IsAdmin: true}, 5, ObsFilter{})
	require.NoError(t, err)
	_, err = q.Leaderboard("user", "requests", 5, r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
}

func TestDashboardKpisAdminUsesLogFactsAndCoreUserMetadata(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "one", CreatedAt: start + 10}).Error)
	require.NoError(t, db.Create(&models.User{ID: 2, Username: "two", CreatedAt: start - 10}).Error)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{
		Date: "2026-07-23", Hour: 10, UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-5",
		RequestCount: 2, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 3, CacheWriteTokens: 4, TotalCost: 50,
	})

	got, err := q.DashboardKpis(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Requests.Value)
	require.Equal(t, int64(50), got.Cost.Value)
	require.Equal(t, int64(37), got.Tokens.Value)
	require.Equal(t, &KpiUsers{Value: 2, Active: 1, New: 1}, got.Users)
	require.NotNil(t, got.SuccessRate)
}

func TestDashboardKpisUserScopesLogFactsAndReturnsCoreQuota(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.User{ID: 7, Username: "seven", Quota: 1000, UsedQuota: 250}).Error)
	for _, row := range []models.BillingHourlyBucket{
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 1, ChannelID: 1, ModelName: "wanted", RequestCount: 3, PromptTokens: 30, TotalCost: 70},
		{Date: "2026-07-23", Hour: 10, UserID: 8, TokenID: 2, ChannelID: 1, ModelName: "wanted", RequestCount: 9, PromptTokens: 90, TotalCost: 900},
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 3, ChannelID: 1, ModelName: "other", RequestCount: 5, PromptTokens: 50, TotalCost: 500},
	} {
		seedBillingHourlyBucket(t, db, row)
	}

	got, err := q.DashboardKpis(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{UserID: 7}, ObsFilter{ModelName: "wanted"})
	require.NoError(t, err)
	require.Equal(t, int64(3), got.Requests.Value)
	require.Equal(t, int64(70), got.Cost.Value)
	require.Equal(t, &KpiQuota{Quota: 1000, UsedQuota: 250}, got.Quota)
	require.Nil(t, got.Users)
}

func TestDashboardKpisHonorsRequestFactPartialHourBoundaries(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for i, offset := range []int64{0, 1, 3598, 3599} {
		require.NoError(t, db.Select("*").Create(&models.UsageLog{
			RequestID: "partial-" + time.Unix(offset, 0).Format("150405") + string(rune('a'+i)),
			UserID:    1, ModelName: "gpt-5", PromptTokens: 1, CompletionTokens: 2, TotalCost: 10, CreatedAt: base + offset,
		}).Error)
	}
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "one"}).Error)

	got, err := q.DashboardKpis(ObsRange{Start: base + 1, End: base + 3599, Gran: GranHour}, Scope{UserID: 1}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Requests.Value)
	require.Equal(t, int64(20), got.Cost.Value)
	require.Equal(t, int64(6), got.Tokens.Value)
}

func TestDashboardKpisLongRangeUsesDailySpark(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for _, row := range []models.BillingHourlyBucket{
		{Date: "2026-07-01", Hour: 1, UserID: 1, ModelName: "gpt-5", RequestCount: 2},
		{Date: "2026-07-01", Hour: 2, UserID: 1, ModelName: "gpt-5", RequestCount: 3},
		{Date: "2026-07-09", Hour: 1, UserID: 1, ModelName: "gpt-5", RequestCount: 4},
	} {
		seedBillingHourlyBucket(t, db, row)
	}
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "one"}).Error)

	got, err := q.DashboardKpis(
		ObsRange{Start: start.Unix(), End: start.Add(9 * 24 * time.Hour).Unix(), Gran: GranDay},
		Scope{UserID: 1}, ObsFilter{ModelName: "gpt-5"},
	)

	require.NoError(t, err)
	require.Equal(t, []int64{5, 4}, got.Requests.Spark)
}

func TestDashboardTrendAdminUsesLogFacts(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{
		Date: "2026-07-23", Hour: 10, UserID: 1, TokenID: 2, ModelName: "gpt-5",
		RequestCount: 2, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 3, TotalCost: 50,
	})

	got, err := q.DashboardTrend(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].Requests)
	require.Equal(t, int64(33), got[0].Tokens)
	require.Equal(t, int64(50), got[0].Cost)
}

func TestDashboardTrendUserAppliesModelAndTokenFilters(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for _, row := range []models.BillingHourlyBucket{
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 2, ModelName: "wanted", RequestCount: 3, PromptTokens: 30, TotalCost: 70},
		{Date: "2026-07-23", Hour: 10, UserID: 8, TokenID: 2, ModelName: "wanted", RequestCount: 9, PromptTokens: 90, TotalCost: 900},
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 3, ModelName: "wanted", RequestCount: 5, PromptTokens: 50, TotalCost: 500},
	} {
		seedBillingHourlyBucket(t, db, row)
	}

	got, err := q.DashboardTrend(
		ObsRange{Start: start, End: start + 3600, Gran: GranHour},
		Scope{UserID: 7},
		ObsFilter{ModelName: "wanted", TokenID: 2},
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(3), got[0].Requests)
	require.Equal(t, int64(70), got[0].Cost)
}

func TestDashboardTrendHonorsRequestFactPartialHourBoundaries(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for i, offset := range []int64{0, 1, 3598, 3599} {
		require.NoError(t, db.Select("*").Create(&models.UsageLog{
			RequestID: "trend-partial-" + time.Unix(offset, 0).Format("150405") + string(rune('a'+i)),
			UserID:    1, ModelName: "gpt-5", PromptTokens: 1, CompletionTokens: 2, TotalCost: 10, CreatedAt: base + offset,
		}).Error)
	}

	got, err := q.DashboardTrend(
		ObsRange{Start: base + 1, End: base + 3599, Gran: GranHour},
		Scope{UserID: 1}, ObsFilter{},
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].Requests)
	require.Equal(t, int64(20), got[0].Cost)
}

func TestDashboardGlobalTrendAndSuccessMergeExactBoundaries(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-07-23", Hour: 11, ChannelID: 1, ModelName: "m", AgentID: "a",
		RequestCount: 2, SuccessCount: 1, PromptTokens: 20, TotalCost: 20,
	}).Error)
	for _, row := range []models.UsageLog{
		{RequestID: "left-out", ModelName: "m", Status: 1, PromptTokens: 100, TotalCost: 100, CreatedAt: base + 9},
		{RequestID: "left-in", ModelName: "m", Status: 1, PromptTokens: 3, TotalCost: 3, CreatedAt: base + 10},
		{RequestID: "right-in", ModelName: "m", Status: 1, PromptTokens: 4, TotalCost: 4, CreatedAt: base + 7200},
		{RequestID: "at-end", ModelName: "m", Status: 1, PromptTokens: 100, TotalCost: 100, CreatedAt: base + 7210},
	} {
		require.NoError(t, db.Select("*").Create(&row).Error)
	}
	r := ObsRange{Start: base + 10, End: base + 7210, Gran: GranHour}
	trend, err := q.DashboardTrend(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, trend, 3)
	require.Equal(t, []int64{1, 2, 1}, []int64{trend[0].Requests, trend[1].Requests, trend[2].Requests})
	success, err := q.DashboardSuccessRate(r, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(3), success.Value)
}

func TestHourlyTrendGlobalDayHybridWeightsStreamAggregates(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: "2026-07-23", Hour: 11, ChannelID: 1, ModelName: "m", AgentID: "a",
		RequestCount: 100, SuccessCount: 100, StreamRequestCount: 1,
		SumFirstResponseMs: 100, SumGenerationMs: 1000, SumStreamCompletionTokens: 100,
	}).Error)
	require.NoError(t, db.Select("*").Create(&models.UsageLog{
		RequestID: "boundary-stream", ModelName: "m", Status: 1, IsStream: true,
		FirstResponseMs: 1000, Duration: 2000, CompletionTokens: 10, CreatedAt: base + 10,
	}).Error)

	got, err := q.HourlyTrend(
		ObsRange{Start: base + 10, End: base + 7200, Gran: GranDay},
		Scope{IsAdmin: true}, ObsFilter{},
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(550), got[0].TTFTMs)
	require.InDelta(t, 55, got[0].TPS, 0.001)
}
