package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestUpsertBillingHourlyBucketsUsesCompleteDimensions(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.BillingHourlyBucket{}))
	m := NewAdminMutation(ctx).Billing()
	rows := []BillingHourlyRow{
		{Date: "2026-07-23", Hour: 6, UserID: 1, TokenID: 2, ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin", ModelName: "m", TokenName: "old", ChannelName: "old", RequestCount: 1, SuccessCount: 1, PromptTokens: 10, InputCost: 5, TotalCost: 5, RawCost: 8, LastUsedAt: 100, UpdatedAt: 100},
		{Date: "2026-07-23", Hour: 6, UserID: 1, TokenID: 2, ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin", ModelName: "m", TokenName: "new", ChannelName: "new", RequestCount: 1, FailedCount: 1, CompletionTokens: 20, OutputCost: 7, TotalCost: 7, RawCost: 9, LastUsedAt: 90, UpdatedAt: 110},
		{Date: "2026-07-23", Hour: 6, UserID: 9, TokenID: 2, ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin", ModelName: "m", RequestCount: 1, LastUsedAt: 120, UpdatedAt: 120},
	}
	require.NoError(t, m.UpsertBillingHourlyBuckets(t.Context(), rows))

	var got []models.BillingHourlyBucket
	require.NoError(t, db.Order("user_id").Find(&got).Error)
	require.Len(t, got, 2)
	require.Equal(t, int64(2), got[0].RequestCount)
	require.Equal(t, int64(1), got[0].SuccessCount)
	require.Equal(t, int64(1), got[0].FailedCount)
	require.Equal(t, int64(10), got[0].PromptTokens)
	require.Equal(t, int64(20), got[0].CompletionTokens)
	require.Equal(t, int64(12), got[0].TotalCost)
	require.Equal(t, int64(17), got[0].RawCost)
	require.Equal(t, "new", got[0].TokenName)
	require.Equal(t, "new", got[0].ChannelName)
	require.Equal(t, int64(100), got[0].LastUsedAt)
}

func TestUpsertBillingHourlyBucketsRollsBackWholeBatch(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingHourlyBucket{}))
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_second_billing_hourly BEFORE INSERT ON billing_hourly_buckets WHEN NEW.user_id = 2 BEGIN SELECT RAISE(ABORT, 'forced batch failure'); END`).Error)
	err := NewAdminMutation(ctx).Billing().UpsertBillingHourlyBuckets(t.Context(), []BillingHourlyRow{
		{Date: "2026-07-23", Hour: 6, UserID: 1, OwnerType: "admin", ModelName: "m", RequestCount: 1},
		{Date: "2026-07-23", Hour: 6, UserID: 2, OwnerType: "admin", ModelName: "m", RequestCount: 1},
	})
	require.Error(t, err)
	var count int64
	require.NoError(t, db.Model(&models.BillingHourlyBucket{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestRebuildBillingHourlyOnlyReplacesRequestedWindow(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.BillingHourlyBucket{}))
	q := NewAdminQuery(ctx).Billing()
	inside := models.BillingLog{RequestID: "inside", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", Status: 1, PromptTokens: 10, TotalCost: 4, CreatedAt: 1784786400}
	outside := models.BillingLog{RequestID: "outside", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", Status: 1, PromptTokens: 20, TotalCost: 8, CreatedAt: 1784793600}
	require.NoError(t, db.Create(&inside).Error)
	require.NoError(t, db.Create(&outside).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{Date: "2026-07-23", Hour: 6, UserID: 99, OwnerType: "admin", ModelName: "stale", RequestCount: 9}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{Date: "2026-07-23", Hour: 10, UserID: 99, OwnerType: "admin", ModelName: "keep", RequestCount: 7}).Error)

	_, err := q.RebuildBillingHourly(t.Context(), 1784782800, 1784790000)
	require.NoError(t, err)

	var got []models.BillingHourlyBucket
	require.NoError(t, db.Order("hour, user_id").Find(&got).Error)
	require.Len(t, got, 2)
	require.Equal(t, "m", got[0].ModelName)
	require.Equal(t, int64(1), got[0].RequestCount)
	require.Equal(t, int64(10), got[0].PromptTokens)
	require.Equal(t, "keep", got[1].ModelName)

	_, err = q.RebuildBillingHourly(t.Context(), 1784782800, 1784790000)
	require.NoError(t, err)
	var rebuilt models.BillingHourlyBucket
	require.NoError(t, db.Where("model_name = ?", "m").First(&rebuilt).Error)
	require.Equal(t, int64(1), rebuilt.RequestCount)
	require.Equal(t, int64(10), rebuilt.PromptTokens)
}

func TestRebuildBillingHourlyRejectsInvalidWindow(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.BillingHourlyBucket{}))
	_, err := NewAdminQuery(ctx).Billing().RebuildBillingHourly(t.Context(), 10, 10)
	require.ErrorContains(t, err, "invalid range")
}

func TestRebuildBillingHourlyExpandsPartialHoursSymmetrically(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.BillingHourlyBucket{}))
	stamp := func(hour, minute int) int64 {
		return time.Date(2026, 7, 23, hour, minute, 0, 0, time.UTC).Unix()
	}
	for _, fact := range []models.BillingLog{
		{RequestID: "left-edge", UserID: 1, OwnerType: "admin", ModelName: "m", PromptTokens: 1, CreatedAt: stamp(6, 15)},
		{RequestID: "middle", UserID: 1, OwnerType: "admin", ModelName: "m", PromptTokens: 2, CreatedAt: stamp(7, 0)},
		{RequestID: "right-edge", UserID: 1, OwnerType: "admin", ModelName: "m", PromptTokens: 4, CreatedAt: stamp(7, 45)},
		{RequestID: "outside", UserID: 1, OwnerType: "admin", ModelName: "keep", PromptTokens: 8, CreatedAt: stamp(8, 0)},
	} {
		require.NoError(t, db.Create(&fact).Error)
	}
	require.NoError(t, db.Create(&models.BillingHourlyBucket{Date: "2026-07-23", Hour: 6, UserID: 99, OwnerType: "admin", ModelName: "stale-6"}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{Date: "2026-07-23", Hour: 7, UserID: 99, OwnerType: "admin", ModelName: "stale-7"}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{Date: "2026-07-23", Hour: 8, UserID: 99, OwnerType: "admin", ModelName: "keep-existing"}).Error)

	result, err := NewAdminQuery(ctx).Billing().RebuildBillingHourly(t.Context(), stamp(6, 30), stamp(7, 30))
	require.NoError(t, err)
	require.Equal(t, stamp(6, 0), result.EffectiveFrom)
	require.Equal(t, stamp(8, 0), result.EffectiveTo)

	var rebuilt []models.BillingHourlyBucket
	require.NoError(t, db.Where("model_name = ?", "m").Order("hour").Find(&rebuilt).Error)
	require.Len(t, rebuilt, 2)
	require.Equal(t, int64(1), rebuilt[0].PromptTokens)
	require.Equal(t, int64(6), rebuilt[1].PromptTokens)
	var kept int64
	require.NoError(t, db.Model(&models.BillingHourlyBucket{}).Where("model_name = ?", "keep-existing").Count(&kept).Error)
	require.Equal(t, int64(1), kept)
}

func TestCompleteHourWindowKeepsAlignedUpperBound(t *testing.T) {
	tests := []struct {
		name     string
		from     int64
		to       int64
		wantFrom int64
		wantTo   int64
	}{
		{
			name:     "aligned bounds",
			from:     time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC).Unix(),
			to:       time.Date(2026, 7, 23, 7, 0, 0, 0, time.UTC).Unix(),
			wantFrom: time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC).Unix(),
			wantTo:   time.Date(2026, 7, 23, 7, 0, 0, 0, time.UTC).Unix(),
		},
		{
			name:     "partial bounds",
			from:     time.Date(2026, 7, 23, 6, 30, 0, 0, time.UTC).Unix(),
			to:       time.Date(2026, 7, 23, 7, 30, 0, 0, time.UTC).Unix(),
			wantFrom: time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC).Unix(),
			wantTo:   time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC).Unix(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFrom, gotTo := completeHourWindow(tt.from, tt.to)
			require.Equal(t, tt.wantFrom, gotFrom)
			require.Equal(t, tt.wantTo, gotTo)
		})
	}
}

func TestUpsertCoreBillingRowsRollsBackAllTablesOnFailure(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingHourlyBucket{}))
	require.NoError(t, db.Migrator().DropTable(&models.ChannelDailyBilling{}))

	mutation := NewAdminMutation(ctx).Billing()
	err := mutation.UpsertCoreBillingRows(t.Context(),
		[]TokenDailyRow{{Date: "2026-07-23", UserID: 1, TokenID: 2, RequestCount: 1}},
		[]ChannelDailyRow{{Date: "2026-07-23", ChannelID: 3, RequestCount: 1}},
		[]BillingHourlyRow{{Date: "2026-07-23", Hour: 6, UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", RequestCount: 1}},
	)
	require.Error(t, err)

	var tokenCount int64
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Count(&tokenCount).Error)
	require.Zero(t, tokenCount, "channel write failure must roll back the earlier token upsert")
}

func TestRebuildCoreHourSliceThroughZeroWatermarkIsBounded(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingHourlyBucket{}, &models.BillingProjectionReceipt{}))
	fact := models.BillingLog{RequestID: "after-watermark", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", PromptTokens: 23, CreatedAt: time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix()}
	require.NoError(t, db.Create(&fact).Error)

	zero := uint(0)
	result, err := NewAdminMutation(ctx).Billing().RebuildCoreHourSliceThroughID(t.Context(), "2026-07-23", 6, nil, &zero)
	require.NoError(t, err)
	require.Zero(t, result.ReplayedLogs)
	var count int64
	require.NoError(t, db.Model(&models.BillingHourlyBucket{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestRebuildCoreHourSliceOnlyReplacesHourlyProjection(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingHourlyBucket{}, &models.BillingProjectionReceipt{}))
	date := "2026-07-23"
	fact := models.BillingLog{RequestID: "hour-only", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", PromptTokens: 11, CreatedAt: time.Date(2026, 7, 23, 6, 10, 0, 0, time.UTC).Unix()}
	require.NoError(t, db.Create(&fact).Error)
	require.NoError(t, db.Create(&models.TokenDailyBilling{Date: date, UserID: 9, TokenID: 9, PromptTokens: 101}).Error)
	require.NoError(t, db.Create(&models.ChannelDailyBilling{Date: date, ChannelID: 9, PromptTokens: 103}).Error)

	watermark := fact.ID
	result, err := NewAdminMutation(ctx).Billing().RebuildCoreHourSliceThroughID(t.Context(), date, 6, nil, &watermark)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.ReplayedLogs)
	var tokenTotal, channelTotal, hourlyTotal int64
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&tokenTotal).Error)
	require.NoError(t, db.Model(&models.ChannelDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&channelTotal).Error)
	require.NoError(t, db.Model(&models.BillingHourlyBucket{}).Select("SUM(prompt_tokens)").Scan(&hourlyTotal).Error)
	require.Equal(t, int64(101), tokenTotal)
	require.Equal(t, int64(103), channelTotal)
	require.Equal(t, int64(11), hourlyTotal)
}

func TestRebuildCoreDailyForDateThroughIDIsAtomicAndBounded(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingProjectionReceipt{}))
	date := "2026-07-23"
	first := models.BillingLog{RequestID: "daily-first", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", PromptTokens: 5, CreatedAt: time.Date(2026, 7, 23, 1, 0, 0, 0, time.UTC).Unix()}
	second := models.BillingLog{RequestID: "daily-second", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", PromptTokens: 7, CreatedAt: time.Date(2026, 7, 23, 20, 0, 0, 0, time.UTC).Unix()}
	require.NoError(t, db.Create(&first).Error)
	require.NoError(t, db.Create(&second).Error)

	watermark := first.ID
	result, err := NewAdminMutation(ctx).Billing().RebuildCoreDailyForDateThroughID(t.Context(), date, &watermark)
	require.NoError(t, err)
	require.Equal(t, int64(1), result.ReplayedLogs)
	var tokenTotal, channelTotal int64
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&tokenTotal).Error)
	require.NoError(t, db.Model(&models.ChannelDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&channelTotal).Error)
	require.Equal(t, int64(5), tokenTotal)
	require.Equal(t, int64(5), channelTotal)

	require.NoError(t, db.Exec(`CREATE TRIGGER fail_daily_channel BEFORE INSERT ON channel_daily_billings BEGIN SELECT RAISE(ABORT, 'forced daily failure'); END`).Error)
	err = func() error {
		_, rebuildErr := NewAdminMutation(ctx).Billing().RebuildCoreDailyForDateThroughID(t.Context(), date, nil)
		return rebuildErr
	}()
	require.Error(t, err)
	tokenTotal, channelTotal = 0, 0
	require.NoError(t, db.Model(&models.TokenDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&tokenTotal).Error)
	require.NoError(t, db.Model(&models.ChannelDailyBilling{}).Select("SUM(prompt_tokens)").Scan(&channelTotal).Error)
	require.Equal(t, int64(5), tokenTotal, "token delete/replay must roll back with channel failure")
	require.Equal(t, int64(5), channelTotal)
}

func TestRebuildCoreDailyZeroWatermarkIsBounded(t *testing.T) {
	ctx, db := setupAdminContext(t)
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.TokenDailyBilling{}, &models.ChannelDailyBilling{}, &models.BillingProjectionReceipt{}))
	fact := models.BillingLog{RequestID: "daily-after-zero", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", PromptTokens: 9, CreatedAt: time.Date(2026, 7, 23, 6, 0, 0, 0, time.UTC).Unix()}
	require.NoError(t, db.Create(&fact).Error)
	zero := uint(0)
	result, err := NewAdminMutation(ctx).Billing().RebuildCoreDailyForDateThroughID(t.Context(), "2026-07-23", &zero)
	require.NoError(t, err)
	require.Zero(t, result.ReplayedLogs)
}
