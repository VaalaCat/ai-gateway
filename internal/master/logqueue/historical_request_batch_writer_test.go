package logqueue

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestHistoricalRequestWriterCommitsCursorAtomically(t *testing.T) {
	db := newHistoricalWriterDB(t)
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	err := writer.writeRequestsWithHook(t.Context(), "requests", 41,
		[]models.RequestLog{{RequestID: "req-41", CreatedAt: 100}},
		func() error { return errors.New("before cursor") })
	require.ErrorContains(t, err, "before cursor")
	requireHistoricalCount(t, db, &models.RequestLog{}, 0)
	requireHistoricalCount(t, db, &models.HistoryCursor{}, 0)
}

func TestHistoricalRequestWriterUsesBusinessKeysAndRebuildsAggregatesOnce(t *testing.T) {
	db := newHistoricalWriterDB(t)
	require.NoError(t, db.Create(&models.RequestLog{ID: 41, RequestID: "live", CreatedAt: 200}).Error)
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	rows := []models.RequestLog{
		{ID: 41, RequestID: "legacy", UserID: 7, ModelName: "m", Status: 1, IsStream: true, FirstResponseMs: 100, Duration: 1100, CompletionTokens: 10, CreatedAt: 1_720_005_400},
		{ID: 42, RequestID: "live", CreatedAt: 100},
	}
	require.NoError(t, writer.WriteRequests(t.Context(), "requests", 99, rows))
	require.NoError(t, writer.WriteRequests(t.Context(), "requests", 99, rows))
	requireHistoricalCount(t, db, &models.RequestLog{}, 2)
	var hourly models.UsageHourlyBucket
	require.NoError(t, db.First(&hourly).Error)
	require.EqualValues(t, 1, hourly.RequestCount)
	var cursor models.HistoryCursor
	require.NoError(t, db.Where("key = ?", "requests").First(&cursor).Error)
	require.Equal(t, uint(99), cursor.LastSourceID)
}

func TestHistoricalRequestWriterDeduplicatesTraceBusinessKey(t *testing.T) {
	db := newHistoricalWriterDB(t)
	require.NoError(t, db.Create(&models.RequestTrace{ID: 51, RequestID: "live", AttemptIndex: 0}).Error)
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteTraces(t.Context(), "traces", 88, []models.RequestTrace{
		{ID: 51, RequestID: "legacy", AttemptIndex: 0},
		{ID: 52, RequestID: "live", AttemptIndex: 0},
	}))
	requireHistoricalCount(t, db, &models.RequestTrace{}, 2)
}

func TestBuildRequestAggregateBatchTreatsEveryNonzeroStatusAsSuccess(t *testing.T) {
	batch := BuildRequestAggregateBatch(models.UsageLog{RequestID: "accepted", Status: 2, CreatedAt: 100})
	require.Len(t, batch.Hourly, 1)
	require.EqualValues(t, 1, batch.Hourly[0].SuccessCount)
	require.Zero(t, batch.Hourly[0].FailedCount)
}

func TestHistoricalTraceWriterRollsBackRowsWhenCursorWriteIsInterrupted(t *testing.T) {
	db := newHistoricalWriterDB(t)
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	err := writer.writeTracesWithHook(t.Context(), "traces", 51,
		[]models.RequestTrace{{RequestID: "req-51", AttemptIndex: 0}},
		func() error { return errors.New("before cursor") })
	require.ErrorContains(t, err, "before cursor")
	requireHistoricalCount(t, db, &models.RequestTrace{}, 0)
	requireHistoricalCount(t, db, &models.HistoryCursor{}, 0)
}

func TestHistoricalRequestWriterHandlesEmptyAndCanceledInput(t *testing.T) {
	db := newHistoricalWriterDB(t)
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteRequests(t.Context(), "requests", 7, nil))
	var cursor models.HistoryCursor
	require.NoError(t, db.Where("key = ?", "requests").First(&cursor).Error)
	require.Equal(t, uint(7), cursor.LastSourceID)

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	err := writer.WriteTraces(canceled, "traces", 1, []models.RequestTrace{{RequestID: "canceled"}})
	require.ErrorIs(t, err, context.Canceled)
	requireHistoricalCount(t, db, &models.RequestTrace{}, 0)
}

func newHistoricalWriterDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	return db
}

func requireHistoricalCount(t *testing.T, db *gorm.DB, model any, want int64) {
	t.Helper()
	var got int64
	require.NoError(t, db.Model(model).Count(&got).Error)
	require.Equal(t, want, got)
}

func TestHistoricalRequestBatchWriterSplitsLargeBatchesWithinSQLiteVariableLimit(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))

	requests := make([]models.RequestLog, 2_000)
	traces := make([]models.RequestTrace, 2_000)
	for i := range requests {
		requestID := fmt.Sprintf("history-%04d", i)
		requests[i] = models.RequestLog{ID: uint(i + 1), RequestID: requestID, CreatedAt: int64(i + 1)}
		traces[i] = models.RequestTrace{ID: uint(i + 1), RequestID: requestID, AttemptIndex: 0, CreatedAt: int64(i + 1)}
	}

	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	require.NoError(t, writer.WriteHistory(t.Context(), requests, traces))

	var requestRows, traceRows int64
	require.NoError(t, db.Model(&models.RequestLog{}).Count(&requestRows).Error)
	require.NoError(t, db.Model(&models.RequestTrace{}).Count(&traceRows).Error)
	require.EqualValues(t, len(requests), requestRows)
	require.EqualValues(t, len(traces), traceRows)
	var firstRequest models.RequestLog
	require.NoError(t, db.First(&firstRequest, 1).Error)
	require.Equal(t, "history-0000", firstRequest.RequestID)
	var lastTrace models.RequestTrace
	require.NoError(t, db.First(&lastTrace, 2_000).Error)
	require.Equal(t, "history-1999", lastTrace.RequestID)
}

func TestHistoricalRequestBatchWriterRejectsBusinessKeyConflictWithoutOverwrite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	require.NoError(t, db.Create(&models.RequestLog{ID: 1, RequestID: "already-live", CreatedAt: 200}).Error)

	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	err = writer.WriteHistory(t.Context(), []models.RequestLog{
		{ID: 900, RequestID: "already-live", CreatedAt: 100},
		{ID: 901, RequestID: "history-only", CreatedAt: 101},
	}, []models.RequestTrace{
		{ID: 700, RequestID: "already-live", AttemptIndex: 0, CreatedAt: 100},
		{ID: 701, RequestID: "history-only", AttemptIndex: 0, CreatedAt: 101},
	})
	require.ErrorContains(t, err, "conflict")

	var requests []models.RequestLog
	require.NoError(t, db.Order("request_id").Find(&requests).Error)
	require.Len(t, requests, 1)
	require.Equal(t, "already-live", requests[0].RequestID)
	require.EqualValues(t, 200, requests[0].CreatedAt)
	var traces []models.RequestTrace
	require.NoError(t, db.Order("request_id").Find(&traces).Error)
	require.Empty(t, traces)
	var hourlyRows, histogramRows int64
	require.NoError(t, db.Model(&models.UsageHourlyBucket{}).Count(&hourlyRows).Error)
	require.NoError(t, db.Model(&models.UsageTTFTHistogram{}).Count(&histogramRows).Error)
	require.Zero(t, hourlyRows)
	require.Zero(t, histogramRows)
}

func TestHistoricalRequestBatchWriterPreservesIDsAndReplaysIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	requests := []models.RequestLog{{ID: 901, RequestID: "history-only", CreatedAt: 101}}
	traces := []models.RequestTrace{{ID: 701, RequestID: "history-only", AttemptIndex: 0, CreatedAt: 101}}

	require.NoError(t, writer.WriteHistory(t.Context(), requests, traces))
	require.NoError(t, writer.WriteHistory(t.Context(), requests, traces))
	var request models.RequestLog
	require.NoError(t, db.First(&request, 901).Error)
	require.Equal(t, "history-only", request.RequestID)
	var trace models.RequestTrace
	require.NoError(t, db.First(&trace, 701).Error)
	require.Equal(t, "history-only", trace.RequestID)
}

func TestHistoricalRequestBatchWriterAcceptsMatchingIdentityWithoutRevalidatingLogContent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	require.NoError(t, db.Create(&models.RequestLog{ID: 901, RequestID: "history-only", CreatedAt: 101}).Error)

	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	// behavior change: old logs only prove identity; billing keeps full-content verification.
	require.NoError(t, writer.WriteHistory(t.Context(), []models.RequestLog{{
		ID: 901, RequestID: "history-only", CreatedAt: 999,
	}}, nil))

	var stored models.RequestLog
	require.NoError(t, db.First(&stored, 901).Error)
	require.EqualValues(t, 101, stored.CreatedAt)
}

func TestHistoricalRequestBatchWriterRejectsUnavailableDatabase(t *testing.T) {
	writer := &HistoricalRequestBatchWriter{}
	err := writer.WriteHistory(t.Context(), []models.RequestLog{{RequestID: "history"}}, nil)
	require.ErrorContains(t, err, "unavailable")
}

func TestHistoricalRequestBatchWriterRejectsMissingLegacyID(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}

	err = writer.WriteHistory(t.Context(), []models.RequestLog{{RequestID: "history"}}, nil)
	require.ErrorContains(t, err, "invalid identity")
	err = writer.WriteHistory(t.Context(), nil, []models.RequestTrace{{RequestID: "history"}})
	require.ErrorContains(t, err, "invalid identity")
}

func TestHistoricalRequestBatchWriterAcceptsEmptyBatch(t *testing.T) {
	writer := &HistoricalRequestBatchWriter{}
	require.NoError(t, writer.WriteHistory(t.Context(), nil, nil))
}

func TestHistoricalRequestBatchWriterReplacesAggregatesIdempotently(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(db))
	writer := &HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return db }}
	batch := HistoricalAggregateBatch{
		Hourly: []models.UsageHourlyBucket{{Date: "2026-07-24", Hour: 6, ChannelID: 2, ModelName: "m", AgentID: "a", RequestCount: 7}},
		TTFT:   []models.UsageTTFTHistogram{{Date: "2026-07-24", Hour: 6, ChannelID: 2, ModelName: "m", AgentID: "a", H2: 3, MaxFirstResponseMs: 20}},
	}

	require.NoError(t, writer.ReplaceAggregates(t.Context(), batch))
	require.NoError(t, writer.ReplaceAggregates(t.Context(), batch))

	var hourly models.UsageHourlyBucket
	require.NoError(t, db.First(&hourly).Error)
	require.EqualValues(t, 7, hourly.RequestCount)
	var ttft models.UsageTTFTHistogram
	require.NoError(t, db.First(&ttft).Error)
	require.EqualValues(t, 3, ttft.H2)
}
