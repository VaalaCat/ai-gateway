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

func TestModelMarketplaceSelectedTokenUsageReferenceIsolatesEveryDimension(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 24*60*60
	factor := 999.0
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("wanted-a", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, start, 1, 2, 3, 4, rawCosts(10, 20, 3, 4), 5, &factor),
		marketplaceRequest("wanted-b-legacy", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, end-1, 10, 20, 30, 40, [4]*int64{}, 7, nil),
		marketplaceRequest("other-token", 7, 71, "gpt-4o", MarketplaceUsageOfferPlatform, 11, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("other-user", 8, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("other-model", 7, 70, "claude-3", MarketplaceUsageOfferPlatform, 11, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("other-source", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 12, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("same-id-private", 7, 70, "gpt-4o", MarketplaceUsageOfferPrivate, 11, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("before-window", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, start-1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("right-boundary", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, end, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
	)
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))
	offer := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 11}

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: []MarketplaceUsageOffer{offer}, Range: MarketplaceUsageRange{Start: start, End: end},
	})
	require.NoError(t, err)
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44,
		ReferenceCost: int64TestPointer(44), GatewayChargeCost: 12,
	}, got[offer])
}

func TestModelMarketplaceSelectedTokenUsageReferenceReadsLegacyUsageLogs(t *testing.T) {
	db := setupTestDB(t)
	require.NoError(t, db.AutoMigrate(&models.RequestLog{}))
	offer := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 11}
	wanted := models.UsageLog(marketplaceRequest(
		"legacy-wanted", 7, 70, offer.ModelName, offer.Kind, offer.SourceID,
		1, 1, 2, 3, 4, rawCosts(10, 20, 30, 40), 17, nil,
	))
	require.NoError(t, db.Select("*").Create(&wanted).Error)
	seedMarketplaceRequestLogs(t, db,
		marketplaceRequest("split-interference", 7, 70, offer.ModelName, offer.Kind, offer.SourceID, 1, 100, 0, 0, 0, rawCosts(999, 0, 0, 0), 999, nil),
	)
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{
		db: db, logDB: db, layoutMode: app.DatabaseLayoutLegacySingle,
	}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: []MarketplaceUsageOffer{offer},
		Range: MarketplaceUsageRange{Start: 1, End: 2},
	})

	require.NoError(t, err)
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
		ReferenceCost: int64TestPointer(100), GatewayChargeCost: 17,
	}, got[offer])
}

func TestModelMarketplaceUsageReferenceTreatsPartialNullRawBucketsAsZero(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	factor := 999.0
	offer := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 11}
	seedMarketplaceRequestLogs(t, logDB, marketplaceRequest(
		"partial-raw", 7, 70, offer.ModelName, offer.Kind, offer.SourceID,
		1, 1, 2, 3, 4,
		[4]*int64{int64TestPointer(10), nil, int64TestPointer(30), nil},
		777, &factor,
	))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: []MarketplaceUsageOffer{offer},
		Range: MarketplaceUsageRange{Start: 1, End: 2},
	})

	require.NoError(t, err)
	require.Equal(t, int64TestPointer(40), got[offer].ReferenceCost)
	require.Equal(t, int64(777), got[offer].GatewayChargeCost)
}

func TestModelMarketplaceOwnerChannelUsageReferenceIncludesSharedRecipientsAndChecksOwnership(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	require.NoError(t, core.Create([]models.PrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 21}, OwnerID: 7, Name: "owned"},
		{ChannelCore: models.ChannelCore{ID: 22}, OwnerID: 8, Name: "other"},
	}).Error)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 7*24*60*60
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("owner-call", 7, 70, "gpt-4o", MarketplaceUsageOfferPrivate, 21, start, 1, 2, 3, 4, rawCosts(10, 20, 30, 40), 9, nil),
		marketplaceRequest("shared-call", 9, 90, "gpt-4o", MarketplaceUsageOfferPrivate, 21, start+1, 10, 20, 30, 40, rawCosts(1, 2, 3, 4), 8, nil),
		marketplaceRequest("other-model", 9, 90, "other", MarketplaceUsageOfferPrivate, 21, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
		marketplaceRequest("other-channel", 8, 80, "gpt-4o", MarketplaceUsageOfferPrivate, 22, start+1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
	)
	coreQueries, logQueries := 0, 0
	countCore := func(*gorm.DB) { coreQueries++ }
	countLog := func(*gorm.DB) { logQueries++ }
	require.NoError(t, core.Callback().Query().After("gorm:query").Register("test:marketplace_owner_core", countCore))
	require.NoError(t, core.Callback().Row().After("gorm:row").Register("test:marketplace_owner_core", countCore))
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_owner_log", countLog))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_owner_log", countLog))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))
	offer := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPrivate, SourceID: 21}

	got, err := query.FindOwnerChannelUsage(context.Background(), OwnerChannelUsageScope{
		ViewerUserID: 7, Offers: []MarketplaceUsageOffer{offer}, Range: MarketplaceUsageRange{Start: start, End: end},
	})
	require.NoError(t, err)
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 11, OutputTokens: 22, CacheReadTokens: 33, CacheWriteTokens: 44,
		ReferenceCost: int64TestPointer(110), GatewayChargeCost: 17,
	}, got[offer])
	require.Equal(t, 1, coreQueries, "owner scope must batch-check all private channel ownership in one core query")
	require.Equal(t, 1, logQueries, "owner scope must aggregate all owned offers in one log query")

	_, err = query.FindOwnerChannelUsage(context.Background(), OwnerChannelUsageScope{
		ViewerUserID: 8, Offers: []MarketplaceUsageOffer{offer}, Range: MarketplaceUsageRange{Start: start, End: end},
	})
	require.ErrorIs(t, err, ErrMarketplaceUsageNotOwner)
}

func TestModelMarketplaceAdminOfferUsageReferenceBatchesPlatformAndPrivateSources(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Unix()
	end := start + 30*24*60*60
	platform := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 11}
	private := MarketplaceUsageOffer{ModelName: "claude-3", Kind: MarketplaceUsageOfferPrivate, SourceID: 21}
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("platform-a", 7, 70, platform.ModelName, platform.Kind, platform.SourceID, start, 1, 2, 3, 4, rawCosts(10, 20, 30, 40), 5, nil),
		marketplaceRequest("platform-b", 8, 80, platform.ModelName, platform.Kind, platform.SourceID, start+1, 10, 20, 30, 40, rawCosts(1, 2, 3, 4), 6, nil),
		marketplaceRequest("private", 9, 90, private.ModelName, private.Kind, private.SourceID, start+2, 5, 6, 7, 8, rawCosts(4, 3, 2, 1), 7, nil),
	)
	queryCount := 0
	callback := "test:marketplace_usage_admin_count"
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register(callback, countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register(callback, countQuery))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))

	got, err := query.FindAdminOfferUsage(context.Background(), AdminOfferUsageScope{
		Offers: []MarketplaceUsageOffer{platform, private}, Range: MarketplaceUsageRange{Start: start, End: end},
	})
	require.NoError(t, err)
	require.Equal(t, 1, queryCount, "admin offer totals must use one aggregate query regardless of offer count")
	require.Equal(t, int64(11), got[platform].InputTokens)
	require.Equal(t, int64TestPointer(110), got[platform].ReferenceCost)
	require.Equal(t, int64(11), got[platform].GatewayChargeCost)
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8,
		ReferenceCost: int64TestPointer(10), GatewayChargeCost: 7,
	}, got[private])
}

func TestModelMarketplaceUsageReferencePreservesLegacyCompletenessByOfferKind(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	offers := []MarketplaceUsageOffer{
		{ModelName: "platform-legacy", Kind: MarketplaceUsageOfferPlatform, SourceID: 11},
		{ModelName: "private-free-legacy", Kind: MarketplaceUsageOfferPrivate, SourceID: 21},
		{ModelName: "private-fee-legacy", Kind: MarketplaceUsageOfferPrivate, SourceID: 22},
		{ModelName: "private-partial", Kind: MarketplaceUsageOfferPrivate, SourceID: 23},
		{ModelName: "private-mixed", Kind: MarketplaceUsageOfferPrivate, SourceID: 24},
	}
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("platform-legacy", 7, 70, offers[0].ModelName, offers[0].Kind, offers[0].SourceID, 1, 1, 0, 0, 0, [4]*int64{}, 13, nil),
		marketplaceRequest("private-free-legacy", 7, 70, offers[1].ModelName, offers[1].Kind, offers[1].SourceID, 1, 1, 0, 0, 0, [4]*int64{}, 0, nil),
		marketplaceRequest("private-fee-legacy", 7, 70, offers[2].ModelName, offers[2].Kind, offers[2].SourceID, 1, 1, 0, 0, 0, [4]*int64{}, 7, nil),
		marketplaceRequest("private-partial", 7, 70, offers[3].ModelName, offers[3].Kind, offers[3].SourceID, 1, 1, 0, 0, 0, [4]*int64{int64TestPointer(10), nil, nil, nil}, 3, nil),
		marketplaceRequest("private-mixed-known", 7, 70, offers[4].ModelName, offers[4].Kind, offers[4].SourceID, 1, 1, 0, 0, 0, rawCosts(20, 0, 0, 0), 2, nil),
		marketplaceRequest("private-mixed-unknown", 7, 70, offers[4].ModelName, offers[4].Kind, offers[4].SourceID, 1, 1, 0, 0, 0, [4]*int64{}, 4, nil),
	)
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: offers, Range: MarketplaceUsageRange{Start: 1, End: 2},
	})

	require.NoError(t, err)
	require.Equal(t, int64TestPointer(13), got[offers[0]].ReferenceCost, "platform legacy rows retain TotalCost fallback")
	require.Nil(t, got[offers[1]].ReferenceCost, "free BYOK legacy reference is unknown, not zero")
	require.Equal(t, int64(0), got[offers[1]].GatewayChargeCost)
	require.Nil(t, got[offers[2]].ReferenceCost, "service-fee BYOK legacy reference is unknown")
	require.Equal(t, int64(7), got[offers[2]].GatewayChargeCost)
	require.Equal(t, int64TestPointer(10), got[offers[3]].ReferenceCost, "partial raw rows are complete with nil buckets treated as zero")
	require.Nil(t, got[offers[4]].ReferenceCost, "one unknown row makes the aggregate incomplete")
}

func TestModelMarketplaceUsageOwnerTypeClassificationIsFailClosed(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	platform := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 11}
	private := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPrivate, SourceID: 11}
	rows := []models.RequestLog{
		marketplaceRequest("owner-null", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, 1, 1, 0, 0, 0, rawCosts(1, 0, 0, 0), 1, nil),
		marketplaceRequest("owner-empty", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, 1, 1, 0, 0, 0, rawCosts(2, 0, 0, 0), 2, nil),
		marketplaceRequest("owner-admin", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, 1, 1, 0, 0, 0, rawCosts(3, 0, 0, 0), 3, nil),
		marketplaceRequest("owner-private", 7, 70, "gpt-4o", MarketplaceUsageOfferPrivate, 11, 1, 1, 0, 0, 0, rawCosts(10, 0, 0, 0), 10, nil),
		marketplaceRequest("owner-future", 7, 70, "gpt-4o", MarketplaceUsageOfferPlatform, 11, 1, 100, 0, 0, 0, rawCosts(100, 0, 0, 0), 100, nil),
	}
	rows[4].OwnerType = "future"
	rows[4].PrivateChannelID = 11
	seedMarketplaceRequestLogs(t, logDB, rows...)
	require.NoError(t, logDB.Model(&models.RequestLog{}).Where("request_id = ?", "owner-null").UpdateColumn("owner_type", nil).Error)
	require.NoError(t, logDB.Model(&models.RequestLog{}).Where("request_id = ?", "owner-empty").UpdateColumn("owner_type", "").Error)
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: []MarketplaceUsageOffer{platform, private},
		Range: MarketplaceUsageRange{Start: 1, End: 2},
	})

	require.NoError(t, err)
	require.Equal(t, int64(3), got[platform].InputTokens, "NULL, empty, and admin are the only platform owner types")
	require.Equal(t, int64TestPointer(6), got[platform].ReferenceCost)
	require.Equal(t, int64(1), got[private].InputTokens)
	require.Equal(t, int64TestPointer(10), got[private].ReferenceCost)
}

func TestModelMarketplaceUsageReferenceQueriesRejectInvalidScopesWithoutSQL(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	queryCount := 0
	callback := "test:marketplace_usage_invalid_count"
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register(callback, countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register(callback, countQuery))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))
	validOffer := MarketplaceUsageOffer{ModelName: "gpt-4o", Kind: MarketplaceUsageOfferPlatform, SourceID: 1}
	validRange := MarketplaceUsageRange{Start: 1, End: 2}

	_, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{UserID: 7, Offers: []MarketplaceUsageOffer{validOffer}, Range: validRange})
	require.ErrorContains(t, err, "token")
	_, err = query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{UserID: 7, TokenID: 70, Offers: []MarketplaceUsageOffer{{Kind: MarketplaceUsageOfferPlatform, SourceID: 1}}, Range: validRange})
	require.ErrorContains(t, err, "model")
	_, err = query.FindAdminOfferUsage(context.Background(), AdminOfferUsageScope{Offers: []MarketplaceUsageOffer{validOffer}, Range: MarketplaceUsageRange{Start: 2, End: 2}})
	require.ErrorContains(t, err, "range")
	require.Zero(t, queryCount)
}

func TestModelMarketplaceSelectedTokenUsageReferenceQueryCountDoesNotGrowPerOffer(t *testing.T) {
	core, logDB := setupStrictSplitDBs(t)
	offers := make([]MarketplaceUsageOffer, 0, 40)
	for i := 1; i <= 40; i++ {
		offers = append(offers, MarketplaceUsageOffer{ModelName: fmt.Sprintf("model-%02d", i), Kind: MarketplaceUsageOfferPlatform, SourceID: uint(i)})
	}
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("one-match", 7, 70, "model-01", MarketplaceUsageOfferPlatform, 1, 1, 1, 0, 0, 0, rawCosts(1, 0, 0, 0), 1, nil),
	)
	queryCount := 0
	callback := "test:marketplace_usage_selected_count"
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register(callback, countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register(callback, countQuery))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: offers, Range: MarketplaceUsageRange{Start: 1, End: 2},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(1), got[offers[0]].InputTokens)
	require.Equal(t, 1, queryCount)
}

func TestModelMarketplaceSelectedTokenUsageReferenceBatchesLargeOfferSets(t *testing.T) {
	const (
		offerCount        = 1_001
		expectedBatchSize = 250
	)
	core, logDB := setupStrictSplitDBs(t)
	offers := make([]MarketplaceUsageOffer, 0, offerCount)
	for i := 1; i <= offerCount; i++ {
		offers = append(offers, MarketplaceUsageOffer{
			ModelName: fmt.Sprintf("model-%04d", i),
			Kind:      MarketplaceUsageOfferPlatform,
			SourceID:  uint(i),
		})
	}
	seedMarketplaceRequestLogs(t, logDB,
		marketplaceRequest("batch-first", 7, 70, offers[0].ModelName, offers[0].Kind, offers[0].SourceID, 1, 1, 2, 3, 4, rawCosts(10, 20, 30, 40), 11, nil),
		marketplaceRequest("batch-middle", 7, 70, offers[500].ModelName, offers[500].Kind, offers[500].SourceID, 1, 5, 6, 7, 8, rawCosts(1, 2, 3, 4), 12, nil),
		marketplaceRequest("batch-last", 7, 70, offers[offerCount-1].ModelName, offers[offerCount-1].Kind, offers[offerCount-1].SourceID, 1, 9, 10, 11, 12, rawCosts(4, 3, 2, 1), 13, nil),
	)
	queryCount := 0
	countQuery := func(*gorm.DB) { queryCount++ }
	require.NoError(t, logDB.Callback().Query().After("gorm:query").Register("test:marketplace_usage_large_count", countQuery))
	require.NoError(t, logDB.Callback().Row().After("gorm:row").Register("test:marketplace_usage_large_count", countQuery))
	query := NewModelMarketplaceUsageQuery(NewContext(&testApp{
		db: core, logDB: logDB, layoutMode: app.DatabaseLayoutSplit,
	}))

	got, err := query.FindSelectedTokenUsage(context.Background(), SelectedTokenUsageScope{
		UserID: 7, TokenID: 70, Offers: offers, Range: MarketplaceUsageRange{Start: 1, End: 2},
	})

	require.NoError(t, err)
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4,
		ReferenceCost: int64TestPointer(100), GatewayChargeCost: 11,
	}, got[offers[0]])
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 5, OutputTokens: 6, CacheReadTokens: 7, CacheWriteTokens: 8,
		ReferenceCost: int64TestPointer(10), GatewayChargeCost: 12,
	}, got[offers[500]])
	require.Equal(t, MarketplaceUsageTotals{
		InputTokens: 9, OutputTokens: 10, CacheReadTokens: 11, CacheWriteTokens: 12,
		ReferenceCost: int64TestPointer(10), GatewayChargeCost: 13,
	}, got[offers[offerCount-1]])
	require.Equal(t, (offerCount+expectedBatchSize-1)/expectedBatchSize, queryCount,
		"query count must be ceil(offer_count/batch_size), never one query per offer")
}

func seedMarketplaceRequestLogs(t *testing.T, db *gorm.DB, rows ...models.RequestLog) {
	t.Helper()
	require.NoError(t, db.Create(&rows).Error)
}

func marketplaceRequest(
	requestID string,
	userID, tokenID uint,
	modelName string,
	kind MarketplaceUsageOfferKind,
	sourceID uint,
	createdAt int64,
	input, output, cacheRead, cacheWrite int,
	raw [4]*int64,
	totalCost int64,
	billingFactor *float64,
) models.RequestLog {
	row := models.UsageLog{
		RequestID: requestID, UserID: userID, TokenID: tokenID, ModelName: modelName, CreatedAt: createdAt,
		PromptTokens: input, CompletionTokens: output, CacheReadTokens: cacheRead, CacheWriteTokens: cacheWrite,
		RawInputCost: raw[0], RawOutputCost: raw[1], RawCacheReadCost: raw[2], RawCacheWriteCost: raw[3],
		TotalCost: totalCost, BillingFactor: billingFactor,
	}
	if kind == MarketplaceUsageOfferPrivate {
		row.OwnerType = "private"
		row.PrivateChannelID = sourceID
	} else {
		row.OwnerType = "admin"
		row.ChannelID = sourceID
	}
	return models.RequestLog(row)
}

func rawCosts(input, output, cacheRead, cacheWrite int64) [4]*int64 {
	return [4]*int64{int64TestPointer(input), int64TestPointer(output), int64TestPointer(cacheRead), int64TestPointer(cacheWrite)}
}

func int64TestPointer(value int64) *int64 { return &value }
