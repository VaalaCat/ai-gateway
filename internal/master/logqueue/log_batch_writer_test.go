package logqueue

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogBatchWriterInsertsRequestAndAggregatesAtomically(t *testing.T) {
	db := newLogTestDB(t)
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}
	batch := completeTestBatch("req-1")

	require.NoError(t, writer.Write(context.Background(), []LogBatch{batch}))
	requireCounts(t, db, map[any]int64{
		&models.RequestLog{}: 1, &models.RequestTrace{}: 1,
		&models.UsageHourlyBucket{}: 1, &models.UsageDurationHistogram{}: 1,
		&models.UsageTTFTHistogram{}: 1, &models.UsageTPSHistogram{}: 1,
		&models.UsageUserTTFTHistogram{}: 1, &models.UsageUserTPSHistogram{}: 1,
	})
}

func TestLogBatchWriterRetryDoesNotDoubleAddAggregates(t *testing.T) {
	db := newLogTestDB(t)
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}
	batch := completeTestBatch("req-retry")

	require.NoError(t, writer.Write(context.Background(), []LogBatch{batch}))
	require.NoError(t, writer.Write(context.Background(), []LogBatch{batch}))

	var hourly models.UsageHourlyBucket
	require.NoError(t, db.First(&hourly).Error)
	require.Equal(t, int64(1), hourly.RequestCount)
	var duration models.UsageDurationHistogram
	require.NoError(t, db.First(&duration).Error)
	require.Equal(t, int64(1), duration.H2)
	var ttft models.UsageTTFTHistogram
	require.NoError(t, db.First(&ttft).Error)
	require.Equal(t, int64(1), ttft.H1)
	var tps models.UsageTPSHistogram
	require.NoError(t, db.First(&tps).Error)
	require.Equal(t, int64(1), tps.H3)
	var userTTFT models.UsageUserTTFTHistogram
	require.NoError(t, db.First(&userTTFT).Error)
	require.Equal(t, int64(1), userTTFT.H1)
	var userTPS models.UsageUserTPSHistogram
	require.NoError(t, db.First(&userTPS).Error)
	require.Equal(t, int64(1), userTPS.H3)
}

func TestLogBatchWriterRollsBackRequestWhenAggregateFails(t *testing.T) {
	db := newLogTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&models.UsageHourlyBucket{}))
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}

	err := writer.Write(context.Background(), []LogBatch{completeTestBatch("req-rollback")})
	require.Error(t, err)
	requireCounts(t, db, map[any]int64{&models.RequestLog{}: 0, &models.RequestTrace{}: 0})
}

func TestLogBatchWriterEmptyBatchIsNoop(t *testing.T) {
	called := false
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { called = true; return nil }}
	require.NoError(t, writer.Write(context.Background(), nil))
	require.False(t, called)
}

func TestLogBatchWriterRejectsTraceForAnotherRequest(t *testing.T) {
	db := newLogTestDB(t)
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}
	batch := completeTestBatch("req-owner")
	batch.Traces[0].RequestID = "req-other"

	err := writer.Write(context.Background(), []LogBatch{batch})

	require.ErrorContains(t, err, "trace request_id")
	require.True(t, errors.Is(err, ErrInvalidLogBatch))
	requireCounts(t, db, map[any]int64{&models.RequestLog{}: 0, &models.RequestTrace{}: 0})
}

func TestLogBatchWriterClassifiesDatabaseErrorsAsPersistenceFailures(t *testing.T) {
	db := newLogTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&models.RequestLog{}))
	err := (&LogBatchWriter{DBFinder: func() *gorm.DB { return db }}).Write(t.Context(), []LogBatch{completeTestBatch("db-error")})
	require.True(t, errors.Is(err, ErrLogPersistenceFailed))
	require.False(t, errors.Is(err, ErrInvalidLogBatch))
}

func TestLogBatchWriterTraceConflictIsIdempotent(t *testing.T) {
	tests := []struct {
		name   string
		seed   []models.RequestTrace
		traces []models.RequestTrace
		want   int64
	}{
		{name: "duplicate in batch", traces: []models.RequestTrace{{RequestID: "req-trace", AttemptIndex: 0}, {RequestID: "req-trace", AttemptIndex: 0}}, want: 1},
		{name: "existing orphan", seed: []models.RequestTrace{{RequestID: "req-trace", AttemptIndex: 0}}, traces: []models.RequestTrace{{RequestID: "req-trace", AttemptIndex: 0}}, want: 1},
		{name: "multiple attempts", traces: []models.RequestTrace{{RequestID: "req-trace", AttemptIndex: 0}, {RequestID: "req-trace", AttemptIndex: 1}}, want: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newLogTestDB(t)
			if len(tt.seed) > 0 {
				require.NoError(t, db.Create(&tt.seed).Error)
			}
			batch := completeTestBatch("req-trace")
			batch.Traces = tt.traces
			require.NoError(t, (&LogBatchWriter{DBFinder: func() *gorm.DB { return db }}).Write(t.Context(), []LogBatch{batch}))
			var traces int64
			require.NoError(t, db.Model(&models.RequestTrace{}).Where("request_id = ?", "req-trace").Count(&traces).Error)
			require.Equal(t, tt.want, traces)
			var hourly models.UsageHourlyBucket
			require.NoError(t, db.First(&hourly).Error)
			require.Equal(t, int64(1), hourly.RequestCount)
		})
	}
}

func newLogTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	require.NoError(t, models.MigrateLogDB(db))
	return db
}

func completeTestBatch(requestID string) LogBatch {
	return LogBatch{
		Request:  models.RequestLog{RequestID: requestID, UserID: 7, ModelName: "gpt-test", CreatedAt: 10},
		Traces:   []models.RequestTrace{{RequestID: requestID, AttemptIndex: 0}},
		Hourly:   []models.UsageHourlyBucket{{Date: "2026-07-23", Hour: 7, ChannelID: 2, ModelName: "gpt-test", AgentID: "a", RequestCount: 1}},
		Duration: []models.UsageDurationHistogram{{Date: "2026-07-23", Hour: 7, ChannelID: 2, ModelName: "gpt-test", AgentID: "a", H2: 1, MaxDurationMs: 20}},
		TTFT:     []models.UsageTTFTHistogram{{Date: "2026-07-23", Hour: 7, ChannelID: 2, ModelName: "gpt-test", AgentID: "a", H1: 1, MaxFirstResponseMs: 10}},
		TPS:      []models.UsageTPSHistogram{{Date: "2026-07-23", Hour: 7, ChannelID: 2, ModelName: "gpt-test", AgentID: "a", H3: 1, MaxTps: 30}},
		UserTTFT: []models.UsageUserTTFTHistogram{{Date: "2026-07-23", Hour: 7, UserID: 7, ModelName: "gpt-test", H1: 1, MaxFirstResponseMs: 10}},
		UserTPS:  []models.UsageUserTPSHistogram{{Date: "2026-07-23", Hour: 7, UserID: 7, ModelName: "gpt-test", H3: 1, MaxTps: 30}},
	}
}

func requireCounts(t *testing.T, db *gorm.DB, wants map[any]int64) {
	t.Helper()
	for model, want := range wants {
		var got int64
		require.NoError(t, db.Model(model).Count(&got).Error)
		require.Equal(t, want, got, "%T", model)
	}
}
