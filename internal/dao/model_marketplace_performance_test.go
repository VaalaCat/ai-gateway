package dao

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestModelMarketplacePerformanceQueryAggregatesCompleteOfferIdentityAndHistograms(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	observedUntil := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	hour := observedUntil.Truncate(time.Hour)
	date := hour.Format(time.DateOnly)

	require.NoError(t, logDB.Create([]models.UsageHourlyBucket{
		{
			Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "a",
			RequestCount: 2, SuccessCount: 1, StreamRequestCount: 1, SumFirstResponseMs: 100,
			SumGenerationMs: 900, SumStreamCompletionTokens: 90,
			PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40,
		},
		{
			Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "b",
			RequestCount: 3, SuccessCount: 3, StreamRequestCount: 2, SumFirstResponseMs: 500,
			SumGenerationMs: 1100, SumStreamCompletionTokens: 110,
			PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
		},
		// Same numeric source ID but a private source: it must not collide.
		{
			Date: date, Hour: hour.Hour(), PrivateChannelID: 41, OwnerType: "private", ModelName: "gpt-4o", AgentID: "p",
			RequestCount: 7, SuccessCount: 6, PromptTokens: 70,
		},
		// Same source, different model: model is part of the key.
		{
			Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "claude-3", AgentID: "a",
			RequestCount: 11, SuccessCount: 10,
		},
		// Outside the fixed 720-hour window.
		{
			Date: hour.Add(-720 * time.Hour).Format(time.DateOnly), Hour: hour.Add(-720 * time.Hour).Hour(),
			ChannelID: 41, ModelName: "gpt-4o", AgentID: "old", RequestCount: 999, SuccessCount: 999,
		},
		// Ambiguous dimensions are not a real Offer and must fail closed.
		{
			Date: date, Hour: hour.Hour(), ChannelID: 9, PrivateChannelID: 9,
			ModelName: "invalid", AgentID: "bad", RequestCount: 99, SuccessCount: 99,
		},
	}).Error)

	require.NoError(t, logDB.Create([]models.UsageTTFTHistogram{
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "a", H3: 2, MaxFirstResponseMs: 290},
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "b", H3: 3, H4: 1, MaxFirstResponseMs: 480},
	}).Error)
	require.NoError(t, logDB.Create([]models.UsageTPSHistogram{
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "a", H5: 4, MaxTps: 49},
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "b", H5: 5, MaxTps: 50},
	}).Error)
	require.NoError(t, logDB.Create([]models.UsageDurationHistogram{
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "a", H6: 6, MaxDurationMs: 9_500},
		{Date: date, Hour: hour.Hour(), ChannelID: 41, ModelName: "gpt-4o", AgentID: "b", H6: 7, MaxDurationMs: 9_900},
	}).Error)

	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_performance_queries", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_performance_queries", countQuery))
	query := NewModelMarketplacePerformanceQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindGlobalPerformance(context.Background(), observedUntil)

	require.NoError(t, err)
	require.Equal(t, 4, queryCount, "hourly components and three histogram tables are fixed-batch queries")
	require.Len(t, got, 3, "platform/private/model dimensions must stay distinct and invalid dimensions are ignored")
	platformKey := MarketplacePerformanceOfferKey{
		ModelName: "gpt-4o", Kind: MarketplacePerformanceOfferPlatform, SourceID: 41,
	}
	platform := got[platformKey]
	require.Len(t, platform, MarketplacePerformanceHours)
	require.Equal(t, hour.Add(-(MarketplacePerformanceHours-1)*time.Hour), platform[0].Hour)
	require.Equal(t, hour, platform[MarketplacePerformanceHours-1].Hour)
	components := platform[MarketplacePerformanceHours-1].Components
	require.Equal(t, PerformanceComponents{
		RequestCount: 5, SuccessCount: 4,
		StreamRequestCount: 3, SumFirstResponseMs: 600,
		SumGenerationMs: 2_000, SumStreamCompletionTokens: 200,
		InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44,
		TTFTHistogram:     PerformanceHistogram{Counts: [17]int64{3: 5, 4: 1}, Max: 480},
		TPSHistogram:      PerformanceHistogram{Counts: [17]int64{5: 9}, Max: 50},
		DurationHistogram: PerformanceHistogram{Counts: [17]int64{6: 13}, Max: 9_900},
	}, components)
	require.Equal(t, int64(7), got[MarketplacePerformanceOfferKey{
		ModelName: "gpt-4o", Kind: MarketplacePerformanceOfferPrivate, SourceID: 41,
	}][MarketplacePerformanceHours-1].Components.RequestCount)
	require.Equal(t, int64(11), got[MarketplacePerformanceOfferKey{
		ModelName: "claude-3", Kind: MarketplacePerformanceOfferPlatform, SourceID: 41,
	}][MarketplacePerformanceHours-1].Components.RequestCount)
}

func TestModelMarketplacePerformanceQueryAllowsOnlyApprovedOwnerTypesAndAnchorsHistograms(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	observedUntil := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	hour := observedUntil.Truncate(time.Hour)
	date := hour.Format(time.DateOnly)
	rows := []models.UsageHourlyBucket{
		{Date: date, Hour: hour.Hour(), ChannelID: 11, OwnerType: "admin", ModelName: "approved", AgentID: "admin", RequestCount: 1},
		{Date: date, Hour: hour.Hour(), ChannelID: 11, OwnerType: "admin", ModelName: "approved", AgentID: "null", RequestCount: 2},
		{Date: date, Hour: hour.Hour(), ChannelID: 11, OwnerType: "admin", ModelName: "approved", AgentID: "empty", RequestCount: 3},
		{Date: date, Hour: hour.Hour(), PrivateChannelID: 11, OwnerType: "private", ModelName: "approved", AgentID: "private", RequestCount: 4},
		{Date: date, Hour: hour.Hour(), ChannelID: 12, OwnerType: "future", ModelName: "unknown", AgentID: "future", RequestCount: 100},
		{Date: date, Hour: hour.Hour(), ChannelID: 13, OwnerType: "private", ModelName: "wrong-shape", AgentID: "private-admin-id", RequestCount: 100},
		{Date: date, Hour: hour.Hour(), PrivateChannelID: 14, OwnerType: "admin", ModelName: "wrong-shape", AgentID: "admin-private-id", RequestCount: 100},
		{Date: date, Hour: hour.Hour(), OwnerType: "admin", ModelName: "zero-ids", AgentID: "zero", RequestCount: 100},
		{Date: date, Hour: hour.Hour(), ChannelID: 15, PrivateChannelID: 15, OwnerType: "admin", ModelName: "dual-ids", AgentID: "dual", RequestCount: 100},
	}
	require.NoError(t, logDB.Create(&rows).Error)
	require.NoError(t, logDB.Model(&models.UsageHourlyBucket{}).Where("agent_id = ?", "null").UpdateColumn("owner_type", nil).Error)
	require.NoError(t, logDB.Model(&models.UsageHourlyBucket{}).Where("agent_id = ?", "empty").UpdateColumn("owner_type", "").Error)
	require.NoError(t, logDB.Create([]models.UsageTTFTHistogram{
		{Date: date, Hour: hour.Hour(), ChannelID: 11, ModelName: "approved", AgentID: "approved", H0: 1, MaxFirstResponseMs: 49},
		{Date: hour.Add(-time.Hour).Format(time.DateOnly), Hour: hour.Add(-time.Hour).Hour(), ChannelID: 11, ModelName: "approved", AgentID: "unanchored-hour", H1: 2, MaxFirstResponseMs: 99},
		{Date: date, Hour: hour.Hour(), ChannelID: 12, ModelName: "unknown", AgentID: "unknown-owner", H2: 3, MaxFirstResponseMs: 199},
		{Date: date, Hour: hour.Hour(), ChannelID: 99, ModelName: "hist-only", AgentID: "orphan", H3: 4, MaxFirstResponseMs: 299},
	}).Error)

	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_performance_owner_queries", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_performance_owner_queries", countQuery))
	query := NewModelMarketplacePerformanceQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindGlobalPerformance(context.Background(), observedUntil)

	require.NoError(t, err)
	require.Equal(t, 4, queryCount, "owner allowlisting and histogram anchoring must keep the fixed four queries")
	require.Len(t, got, 2, "only approved platform and private owner types may create Offers")
	platform := got[MarketplacePerformanceOfferKey{ModelName: "approved", Kind: MarketplacePerformanceOfferPlatform, SourceID: 11}]
	private := got[MarketplacePerformanceOfferKey{ModelName: "approved", Kind: MarketplacePerformanceOfferPrivate, SourceID: 11}]
	require.Len(t, platform, MarketplacePerformanceHours)
	require.Len(t, private, MarketplacePerformanceHours)
	require.Equal(t, int64(6), platform[MarketplacePerformanceHours-1].Components.RequestCount,
		"admin, legacy NULL, and legacy empty are the complete platform allowlist")
	require.Equal(t, int64(4), private[MarketplacePerformanceHours-1].Components.RequestCount)
	require.Equal(t, PerformanceHistogram{Counts: [17]int64{0: 1}, Max: 49}, platform[MarketplacePerformanceHours-1].Components.TTFTHistogram)
	require.Equal(t, PerformanceHistogram{}, platform[MarketplacePerformanceHours-2].Components.TTFTHistogram,
		"a histogram cannot populate an hour without an approved hourly fact coordinate")
	require.NotContains(t, got, MarketplacePerformanceOfferKey{ModelName: "unknown", Kind: MarketplacePerformanceOfferPlatform, SourceID: 12})
	require.NotContains(t, got, MarketplacePerformanceOfferKey{ModelName: "hist-only", Kind: MarketplacePerformanceOfferPlatform, SourceID: 99})
}

func TestModelMarketplacePerformanceQuerySupportsLegacyLayoutAndEmptyHours(t *testing.T) {
	db := setupTestDB(t)
	observedUntil := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	firstHour := observedUntil.Truncate(time.Hour).Add(-(MarketplacePerformanceHours - 1) * time.Hour)
	require.NoError(t, db.Create(&models.UsageHourlyBucket{
		Date: firstHour.Format(time.DateOnly), Hour: firstHour.Hour(), ChannelID: 7,
		ModelName: "legacy", AgentID: "legacy-agent", RequestCount: 1, SuccessCount: 1,
	}).Error)
	query := NewModelMarketplacePerformanceQuery(NewContext(&testApp{
		db: db, layoutMode: app.DatabaseLayoutLegacySingle,
	}))

	got, err := query.FindGlobalPerformance(context.Background(), observedUntil)

	require.NoError(t, err)
	hours := got[MarketplacePerformanceOfferKey{ModelName: "legacy", Kind: MarketplacePerformanceOfferPlatform, SourceID: 7}]
	require.Len(t, hours, MarketplacePerformanceHours)
	require.Equal(t, firstHour, hours[0].Hour)
	require.Equal(t, int64(1), hours[0].Components.RequestCount)
	require.Zero(t, hours[1].Components.RequestCount, "missing database rows must remain explicit zero buckets")
}

func TestModelMarketplacePerformanceQueryCountDoesNotGrowPerOffer(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	observedUntil := time.Date(2026, 7, 31, 10, 30, 0, 0, time.UTC)
	hour := observedUntil.Truncate(time.Hour)
	rows := make([]models.UsageHourlyBucket, 0, 64)
	for i := 1; i <= 64; i++ {
		rows = append(rows, models.UsageHourlyBucket{
			Date: hour.Format(time.DateOnly), Hour: hour.Hour(), ChannelID: uint(i),
			ModelName: fmt.Sprintf("model-%02d", i), AgentID: "a", RequestCount: 1,
		})
	}
	require.NoError(t, logDB.Create(&rows).Error)
	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_performance_bounded", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_performance_bounded", countQuery))
	query := NewModelMarketplacePerformanceQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindGlobalPerformance(context.Background(), observedUntil)

	require.NoError(t, err)
	require.Len(t, got, len(rows))
	require.Equal(t, 4, queryCount, "query count is fixed, never one query per Offer")
}

func TestModelMarketplacePerformanceQueryRejectsZeroObservedUntilWithoutSQL(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_performance_invalid", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_performance_invalid", countQuery))
	query := NewModelMarketplacePerformanceQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	_, err := query.FindGlobalPerformance(context.Background(), time.Time{})

	require.ErrorContains(t, err, "observed until")
	require.Zero(t, queryCount)
}
