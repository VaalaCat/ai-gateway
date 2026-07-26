package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestCoreDashboardKpisAdminUsesBillingFactsWithoutLogDatabase(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "one", CreatedAt: start + 10}).Error)
	require.NoError(t, db.Create(&models.User{ID: 2, Username: "two", CreatedAt: start - 10}).Error)
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{
		Date: "2026-07-23", Hour: 10, UserID: 1, TokenID: 1, ChannelID: 1, ModelName: "gpt-5",
		RequestCount: 2, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 3, CacheWriteTokens: 4, TotalCost: 50,
	})

	got, err := q.CoreDashboardKpis(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Requests.Value)
	require.Equal(t, int64(50), got.Cost.Value)
	require.Equal(t, int64(37), got.Tokens.Value)
	require.Equal(t, &KpiUsers{Value: 2, Active: 1, New: 1}, got.Users)
	require.Nil(t, got.SuccessRate)
}

func TestCoreDashboardKpisUserScopesBillingAndReturnsQuota(t *testing.T) {
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

	got, err := q.CoreDashboardKpis(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{UserID: 7}, ObsFilter{ModelName: "wanted"})
	require.NoError(t, err)
	require.Equal(t, int64(3), got.Requests.Value)
	require.Equal(t, int64(70), got.Cost.Value)
	require.Equal(t, &KpiQuota{Quota: 1000, UsedQuota: 250}, got.Quota)
	require.Nil(t, got.Users)
}

func TestCoreDashboardKpisHonorsPartialHourBoundaries(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for i, offset := range []int64{0, 1, 3598, 3599} {
		require.NoError(t, db.Create(&models.BillingLog{
			RequestID: "partial-" + time.Unix(offset, 0).Format("150405") + string(rune('a'+i)),
			UserID:    1, ModelName: "gpt-5", PromptTokens: 1, CompletionTokens: 2, TotalCost: 10, CreatedAt: base + offset,
		}).Error)
	}
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "one"}).Error)

	got, err := q.CoreDashboardKpis(ObsRange{Start: base + 1, End: base + 3599, Gran: GranHour}, Scope{UserID: 1}, ObsFilter{})
	require.NoError(t, err)
	require.Equal(t, int64(2), got.Requests.Value)
	require.Equal(t, int64(20), got.Cost.Value)
	require.Equal(t, int64(6), got.Tokens.Value)
}

func TestCoreDashboardTrendAdminUsesBillingFacts(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	seedBillingHourlyBucket(t, db, models.BillingHourlyBucket{
		Date: "2026-07-23", Hour: 10, UserID: 1, TokenID: 2, ModelName: "gpt-5",
		RequestCount: 2, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 3, TotalCost: 50,
	})

	got, err := q.CoreDashboardTrend(ObsRange{Start: start, End: start + 3600, Gran: GranHour}, Scope{IsAdmin: true}, ObsFilter{})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].Requests)
	require.Equal(t, int64(33), got[0].Tokens)
	require.Equal(t, int64(50), got[0].Cost)
}

func TestCoreDashboardTrendUserAppliesModelAndTokenFilters(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	start := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for _, row := range []models.BillingHourlyBucket{
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 2, ModelName: "wanted", RequestCount: 3, PromptTokens: 30, TotalCost: 70},
		{Date: "2026-07-23", Hour: 10, UserID: 8, TokenID: 2, ModelName: "wanted", RequestCount: 9, PromptTokens: 90, TotalCost: 900},
		{Date: "2026-07-23", Hour: 10, UserID: 7, TokenID: 3, ModelName: "wanted", RequestCount: 5, PromptTokens: 50, TotalCost: 500},
	} {
		seedBillingHourlyBucket(t, db, row)
	}

	got, err := q.CoreDashboardTrend(
		ObsRange{Start: start, End: start + 3600, Gran: GranHour},
		Scope{UserID: 7},
		ObsFilter{ModelName: "wanted", TokenID: 2},
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(3), got[0].Requests)
	require.Equal(t, int64(70), got[0].Cost)
}

func TestCoreDashboardTrendHonorsExactPartialHourBoundaries(t *testing.T) {
	q, db := setupBillingStatsQuery(t)
	base := time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC).Unix()
	for i, offset := range []int64{0, 1, 3598, 3599} {
		require.NoError(t, db.Create(&models.BillingLog{
			RequestID: "trend-partial-" + time.Unix(offset, 0).Format("150405") + string(rune('a'+i)),
			UserID:    1, ModelName: "gpt-5", PromptTokens: 1, CompletionTokens: 2, TotalCost: 10, CreatedAt: base + offset,
		}).Error)
	}

	got, err := q.CoreDashboardTrend(
		ObsRange{Start: base + 1, End: base + 3599, Gran: GranHour},
		Scope{UserID: 1}, ObsFilter{},
	)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].Requests)
	require.Equal(t, int64(20), got[0].Cost)
}
