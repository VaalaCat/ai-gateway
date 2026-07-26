package historybackfill

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	masterbilling "github.com/VaalaCat/ai-gateway/internal/master/billing"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func TestBillingWriterCommitsRowsAndCursorAtomically(t *testing.T) {
	path := t.TempDir() + "/core.db"
	db, err := masterdatabase.NewConnector().OpenCore(path)
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	t.Cleanup(func() { closeDB(db) })
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	err = writer.writeHistoryWithHook(t.Context(), "billing", 51,
		[]models.BillingLog{{RequestID: "req-51", TotalCost: 11}},
		func() error { return errors.New("before cursor") })
	require.ErrorContains(t, err, "before cursor")
	requireModelCount(t, db, &models.BillingLog{}, 0)
	requireModelCount(t, db, &models.HistoryCursor{}, 0)
	requireModelCount(t, db, &models.TokenDailyBilling{}, 0)
	requireModelCount(t, db, &models.ChannelDailyBilling{}, 0)
	requireModelCount(t, db, &models.BillingHourlyBucket{}, 0)
	requireModelCount(t, db, &models.BillingProjectionReceipt{}, 0)
}

func TestBillingWriterProjectsCommittedLiveFactWithPendingReceipt(t *testing.T) {
	db := openBackfillCoreTarget(t)
	createdAt := time.Date(2026, time.July, 26, 3, 0, 0, 0, time.UTC).Unix()
	live := models.BillingLog{
		RequestID: "crashed-live", UserID: 7, TokenID: 8, TokenName: "live-token",
		ChannelID: 9, ChannelName: "live-channel", OwnerType: "admin", ModelName: "live-model",
		PromptTokens: 12, TotalCost: 34, Status: 1, CreatedAt: createdAt,
	}
	require.NoError(t, db.Create(&live).Error)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{
		RequestID: live.RequestID, BillingLogID: live.ID, State: models.BillingProjectionPending,
	}).Error)

	legacy := live
	legacy.ID = 0
	legacy.UserID = 99
	legacy.TotalCost = 999
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 1, []models.BillingLog{legacy}))

	requireProjectionTotals(t, db, &models.TokenDailyBilling{}, 1, 1, 1, 0, 34)
	requireProjectionTotals(t, db, &models.ChannelDailyBilling{}, 1, 1, 1, 0, 34)
	requireProjectionTotals(t, db, &models.BillingHourlyBucket{}, 1, 1, 1, 0, 34)
	requireModelCount(t, db, &models.BillingProjectionReceipt{}, 1)
}

func TestBillingWriterDoesNotReprojectFactWithReceipt(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	fact := models.BillingLog{RequestID: "already-projected", TotalCost: 11, Status: 1, CreatedAt: 1_800_000_000}
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 1, []models.BillingLog{fact}))
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 2, []models.BillingLog{fact}))

	requireProjectionTotals(t, db, &models.TokenDailyBilling{}, 1, 1, 1, 0, 11)
	requireModelCount(t, db, &models.BillingProjectionReceipt{}, 1)
}

func TestBillingWriterAndRealtimeAggregatorCompeteByReceipt(t *testing.T) {
	db := openBackfillCoreTarget(t)
	live := models.BillingLog{RequestID: "compete", UserID: 4, TokenID: 5, ChannelID: 6, TotalCost: 19, Status: 1, CreatedAt: 1_800_000_000}
	require.NoError(t, db.Create(&live).Error)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{
		RequestID: live.RequestID, BillingLogID: live.ID, State: models.BillingProjectionPending,
	}).Error)
	aggregator := masterbilling.NewAggregator(nil, nil, masterbilling.AggregatorOptions{})
	aggregator.SetProjectionFlushContextFn(func(ctx context.Context, facts []models.BillingLog) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return masterbilling.ProjectCommittedBillingFactsInTx(ctx, tx, facts)
		})
	})
	aggregator.SubmitPendingBilling(&live)

	legacy := live
	legacy.ID = 0
	legacy.TotalCost = 999
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 1, []models.BillingLog{legacy}))
	require.NoError(t, aggregator.FlushContext(t.Context()))

	requireProjectionTotals(t, db, &models.TokenDailyBilling{}, 1, 1, 1, 0, 19)
	requireModelCount(t, db, &models.BillingProjectionReceipt{}, 1)
}

func TestBillingWriterRejectsConflictingDuplicateRequestID(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}

	err := writer.WriteHistory(t.Context(), "billing", 2, []models.BillingLog{
		{RequestID: "duplicate", TotalCost: 1},
		{RequestID: "duplicate", TotalCost: 2},
	})
	require.ErrorContains(t, err, "conflicting duplicate")
	requireModelCount(t, db, &models.BillingLog{}, 0)
	requireModelCount(t, db, &models.HistoryCursor{}, 0)
}

func TestBillingWriterRejectsReceiptWithoutFact(t *testing.T) {
	db := openBackfillCoreTarget(t)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{RequestID: "orphan", ProjectedAtUnix: 1}).Error)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}

	err := writer.WriteHistory(t.Context(), "billing", 1, []models.BillingLog{{RequestID: "orphan"}})
	require.ErrorContains(t, err, "receipt")
	requireModelCount(t, db, &models.HistoryCursor{}, 0)
}

func TestBillingWriterBuildsDailyAndHourlyProjectionsForNewFacts(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	dayOne := time.Date(2026, time.July, 25, 23, 59, 0, 0, time.UTC).Unix()
	dayTwo := time.Date(2026, time.July, 26, 0, 1, 0, 0, time.UTC).Unix()

	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 3, []models.BillingLog{
		{RequestID: "success", UserID: 1, TokenID: 2, TokenName: "token", ChannelID: 3, ChannelName: "channel", OwnerType: "admin", ModelName: "model", Status: 1, PromptTokens: 10, CompletionTokens: 4, TotalCost: 26, CreatedAt: dayOne},
		{RequestID: "failure", UserID: 1, TokenID: 2, TokenName: "token", ChannelID: 3, ChannelName: "channel", OwnerType: "admin", ModelName: "model", Status: 0, PromptTokens: 5, TotalCost: 10, CreatedAt: dayTwo},
		{RequestID: "zero-dimensions", Status: 0, ModelName: "", TotalCost: 7, CreatedAt: dayTwo},
	}))

	requireProjectionTotals(t, db, &models.TokenDailyBilling{}, 3, 3, 1, 2, 43)
	requireProjectionTotals(t, db, &models.ChannelDailyBilling{}, 3, 3, 1, 2, 43)
	requireProjectionTotals(t, db, &models.BillingHourlyBucket{}, 3, 3, 1, 2, 43)
}

func TestBillingWriterProjectsEachRequestIDOnlyOnceAcrossLiveAndRepeatedBatches(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	createdAt := time.Date(2026, time.July, 26, 2, 0, 0, 0, time.UTC).Unix()
	live := models.BillingLog{RequestID: "live", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", Status: 1, TotalCost: 11, CreatedAt: createdAt}
	history := models.BillingLog{RequestID: "history", UserID: 1, TokenID: 2, ChannelID: 3, OwnerType: "admin", ModelName: "m", Status: 1, TotalCost: 13, CreatedAt: createdAt}

	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 1, []models.BillingLog{live}))
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 3, []models.BillingLog{
		{RequestID: "live", UserID: 9, TokenID: 9, ChannelID: 9, Status: 0, TotalCost: 999, CreatedAt: createdAt},
		history,
		history,
	}))
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 3, []models.BillingLog{live, history}))

	requireModelCount(t, db, &models.BillingLog{}, 2)
	requireProjectionTotals(t, db, &models.TokenDailyBilling{}, 1, 2, 2, 0, 24)
	requireProjectionTotals(t, db, &models.ChannelDailyBilling{}, 1, 2, 2, 0, 24)
	requireProjectionTotals(t, db, &models.BillingHourlyBucket{}, 1, 2, 2, 0, 24)
}

func TestBillingWriterUsesRequestIDInsteadOfLegacyNumericID(t *testing.T) {
	path := t.TempDir() + "/core.db"
	db, err := masterdatabase.NewConnector().OpenCore(path)
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	t.Cleanup(func() { closeDB(db) })
	live := models.BillingLog{ID: 51, RequestID: "live", TotalCost: 99}
	require.NoError(t, db.Create(&live).Error)
	require.NoError(t, db.Create(&models.BillingProjectionReceipt{
		RequestID: live.RequestID, BillingLogID: live.ID, State: models.BillingProjectionPending,
	}).Error)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}

	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 99, []models.BillingLog{
		{ID: 51, RequestID: "legacy", TotalCost: 1},
		{ID: 52, RequestID: "live", TotalCost: 2},
	}))
	requireModelCount(t, db, &models.BillingLog{}, 2)
	var cursor models.HistoryCursor
	require.NoError(t, db.Where("key = ?", "billing").First(&cursor).Error)
	require.Equal(t, uint(99), cursor.LastSourceID)
	require.EqualValues(t, 2, cursor.ProcessedRows)
}

func TestBillingBatchWriterRejectsUnavailableDatabase(t *testing.T) {
	err := (&BillingBatchWriter{}).WriteHistory(t.Context(), "billing", 1, []models.BillingLog{{RequestID: "legacy"}})
	require.ErrorContains(t, err, "unavailable")
}

func TestBillingWriterEmptyBatchAdvancesButNeverRegressesCursor(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 10, nil))
	require.NoError(t, writer.WriteHistory(t.Context(), "billing", 5, nil))
	var cursor models.HistoryCursor
	require.NoError(t, db.Where("key = ?", "billing").First(&cursor).Error)
	require.Equal(t, uint(10), cursor.LastSourceID)
	require.Zero(t, cursor.ProcessedRows)
}

func TestBillingWriterPropagatesCanceledContextAndBusyDatabase(t *testing.T) {
	db := openBackfillCoreTarget(t)
	writer := &BillingBatchWriter{DBFinder: func() *gorm.DB { return db }}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err := writer.WriteHistory(canceled, "billing", 1, []models.BillingLog{{RequestID: "canceled"}})
	require.ErrorIs(t, err, context.Canceled)

	path := filepath.Join(t.TempDir(), "busy.db")
	locker, err := gorm.Open(sqlite.Open("file:"+path+"?_pragma=busy_timeout(1)"), &gorm.Config{})
	require.NoError(t, err)
	target, err := gorm.Open(sqlite.Open("file:"+path+"?_pragma=busy_timeout(1)"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(target))
	lockerSQL, err := locker.DB()
	require.NoError(t, err)
	lockerSQL.SetMaxOpenConns(1)
	lockerSQL.SetMaxIdleConns(1)
	_, err = lockerSQL.Exec("BEGIN IMMEDIATE")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = lockerSQL.Exec("ROLLBACK") })
	busyContext, stopBusyWait := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stopBusyWait()
	err = (&BillingBatchWriter{DBFinder: func() *gorm.DB { return target }}).
		WriteHistory(busyContext, "billing", 1, []models.BillingLog{{RequestID: "busy"}})
	require.Error(t, err)
}

func requireModelCount(t *testing.T, db *gorm.DB, model any, want int64) {
	t.Helper()
	var got int64
	require.NoError(t, db.Model(model).Count(&got).Error)
	require.Equal(t, want, got)
}

func requireProjectionTotals(t *testing.T, db *gorm.DB, model any, rows, requests, successes, failures, totalCost int64) {
	t.Helper()
	var got struct {
		Rows      int64
		Requests  int64
		Successes int64
		Failures  int64
		TotalCost int64
	}
	require.NoError(t, db.Model(model).Select(
		"COUNT(*) AS rows, COALESCE(SUM(request_count), 0) AS requests, COALESCE(SUM(success_count), 0) AS successes, COALESCE(SUM(failed_count), 0) AS failures, COALESCE(SUM(total_cost), 0) AS total_cost",
	).Scan(&got).Error)
	require.Equal(t, rows, got.Rows)
	require.Equal(t, requests, got.Requests)
	require.Equal(t, successes, got.Successes)
	require.Equal(t, failures, got.Failures)
	require.Equal(t, totalCost, got.TotalCost)
}
