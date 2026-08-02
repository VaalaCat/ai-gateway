package logqueue

import (
	"context"
	"errors"
	"testing"
	"time"

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

func TestLogBatchWriterMaintainsDailyBilling(t *testing.T) {
	db := newLogTestDB(t)
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}
	logs := dailyBillingFixture()

	for _, log := range logs {
		batch := BuildRequestAggregateBatch(log)
		require.NoError(t, writer.Write(t.Context(), []LogBatch{batch}))
	}

	var token models.TokenDailyBilling
	require.NoError(t, db.Where("date = ? AND user_id = ? AND token_id = ?", "2026-07-24", 7, 70).First(&token).Error)
	require.Equal(t, "renamed-free-token", token.TokenName, "the newest request snapshot wins")
	require.EqualValues(t, 2, token.RequestCount)
	require.EqualValues(t, 1, token.SuccessCount)
	require.EqualValues(t, 1, token.FailedCount)
	require.EqualValues(t, 23, token.PromptTokens)
	require.EqualValues(t, 12, token.CompletionTokens)
	require.EqualValues(t, 7, token.CacheReadTokens)
	require.EqualValues(t, 3, token.CacheWriteTokens)
	require.EqualValues(t, 150, token.InputCost)
	require.EqualValues(t, 90, token.OutputCost)
	require.EqualValues(t, 240, token.TotalCost)
	require.EqualValues(t, time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC).Unix(), token.LastUsedAt)

	var admin models.ChannelDailyBilling
	require.NoError(t, db.Where("date = ? AND channel_id = ? AND private_channel_id = ?", "2026-07-24", 10, 0).First(&admin).Error)
	require.Equal(t, "admin-renamed-free", admin.ChannelName)
	require.Equal(t, 2, admin.ChannelType)
	require.Equal(t, "admin", admin.OwnerType)
	require.EqualValues(t, 2, admin.RequestCount)
	require.EqualValues(t, 1, admin.SuccessCount)
	require.EqualValues(t, 1, admin.FailedCount)
	require.EqualValues(t, 240, admin.TotalCost)
	require.EqualValues(t, 900, admin.RawCost)

	var private models.ChannelDailyBilling
	require.NoError(t, db.Where("date = ? AND channel_id = ? AND private_channel_id = ?", "2026-07-24", 0, 30).First(&private).Error)
	require.Equal(t, "private", private.OwnerType)
	require.Equal(t, "byok", private.ChannelName)
	require.Equal(t, 9, private.ChannelType)
	require.EqualValues(t, 1, private.RequestCount)
	require.EqualValues(t, 120, private.TotalCost)
	require.EqualValues(t, 300, private.RawCost)
}

func TestLogBatchWriterDailyBillingFailureRollsBackAllRows(t *testing.T) {
	for _, target := range []struct {
		name  string
		model any
	}{
		{name: "token daily", model: &models.TokenDailyBilling{}},
		{name: "channel daily", model: &models.ChannelDailyBilling{}},
	} {
		t.Run(target.name, func(t *testing.T) {
			db := newLogTestDB(t)
			require.NoError(t, db.Migrator().DropTable(target.model))
			batch := completeTestBatch("daily-rollback-" + target.name)
			batch.TokenDaily = []models.TokenDailyBilling{{Date: "2026-07-24", UserID: 7, TokenID: 70, RequestCount: 1}}
			batch.ChannelDaily = []models.ChannelDailyBilling{{Date: "2026-07-24", ChannelID: 10, RequestCount: 1}}

			err := (&LogBatchWriter{DBFinder: func() *gorm.DB { return db }}).Write(t.Context(), []LogBatch{batch})

			require.Error(t, err)
			requireCounts(t, db, map[any]int64{
				&models.RequestLog{}: 0, &models.RequestTrace{}: 0,
				&models.UsageHourlyBucket{}: 0, &models.UsageDurationHistogram{}: 0,
				&models.UsageTTFTHistogram{}: 0, &models.UsageTPSHistogram{}: 0,
				&models.UsageUserTTFTHistogram{}: 0, &models.UsageUserTPSHistogram{}: 0,
			})
			if target.name == "token daily" {
				requireCounts(t, db, map[any]int64{&models.ChannelDailyBilling{}: 0})
			} else {
				requireCounts(t, db, map[any]int64{&models.TokenDailyBilling{}: 0})
			}
		})
	}
}

func TestLogBatchWriterDailyBillingDuplicateRequestIsExactlyOnce(t *testing.T) {
	db := newLogTestDB(t)
	writer := LogBatchWriter{DBFinder: func() *gorm.DB { return db }}
	batch := BuildRequestAggregateBatch(dailyBillingFixture()[0])

	require.NoError(t, writer.Write(t.Context(), []LogBatch{batch}))
	require.NoError(t, writer.Write(t.Context(), []LogBatch{batch}))

	var token models.TokenDailyBilling
	require.NoError(t, db.First(&token).Error)
	require.EqualValues(t, 1, token.RequestCount)
	var channel models.ChannelDailyBilling
	require.NoError(t, db.First(&channel).Error)
	require.EqualValues(t, 1, channel.RequestCount)
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

func dailyBillingFixture() []models.UsageLog {
	paidAt := time.Date(2026, 7, 24, 10, 0, 0, 0, time.UTC).Unix()
	freeAt := time.Date(2026, 7, 24, 11, 0, 0, 0, time.UTC).Unix()
	byokAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC).Unix()
	paidRawInput, paidRawOutput := int64(300), int64(300)
	freeRawInput, freeRawOutput := int64(150), int64(150)
	byokRawInput, byokRawOutput := int64(100), int64(200)
	return []models.UsageLog{
		{
			RequestID: "daily-paid", UserID: 7, TokenID: 70, TokenName: "paid-token", ChannelID: 10, OwnerType: "admin", ChannelName: "admin", ChannelType: 1,
			Status: 1, PromptTokens: 20, CompletionTokens: 10, CacheReadTokens: 6, CacheWriteTokens: 2, InputCost: 150, OutputCost: 90, TotalCost: 240,
			RawInputCost: &paidRawInput, RawOutputCost: &paidRawOutput, CreatedAt: paidAt,
		},
		{
			RequestID: "daily-free", UserID: 7, TokenID: 70, TokenName: "renamed-free-token", ChannelID: 10, OwnerType: "admin", ChannelName: "admin-renamed-free", ChannelType: 2,
			Free: true, Status: 0, PromptTokens: 3, CompletionTokens: 2, CacheReadTokens: 1, CacheWriteTokens: 1,
			RawInputCost: &freeRawInput, RawOutputCost: &freeRawOutput, CreatedAt: freeAt,
		},
		{
			RequestID: "daily-byok", UserID: 8, TokenID: 80, TokenName: "byok-token", PrivateChannelID: 30, OwnerType: "private", ChannelName: "byok", ChannelType: 9,
			Free: true, Status: 1, PromptTokens: 4, CompletionTokens: 1, CacheReadTokens: 2, CacheWriteTokens: 1, InputCost: 80, OutputCost: 40, TotalCost: 120,
			RawInputCost: &byokRawInput, RawOutputCost: &byokRawOutput, CreatedAt: byokAt,
		},
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
