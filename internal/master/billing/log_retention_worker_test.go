package billing

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
	"gorm.io/gorm"
)

func TestBillingLogRetentionKeepsExactCutoff(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	db := openRetentionDB(t)
	seedBillingLog(t, db, "old", now.Add(-5*24*time.Hour-time.Second))
	seedBillingLog(t, db, "boundary", now.Add(-5*24*time.Hour))
	worker := newRetentionWorkerForTest(db, now, 5, 2)

	require.NoError(t, worker.RunOnce(t.Context()))
	requireMissingBillingRequest(t, db, "old")
	requirePresentBillingRequest(t, db, "boundary")
	require.True(t, db.Migrator().HasIndex(&models.BillingLog{}, "idx_billing_logs_created_id"))
}

func TestBillingLogRetentionBackdatedInsertBeforeCursorWaitsForNextRun(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	cutoff := now.Add(-5 * 24 * time.Hour)
	db := openRetentionDB(t)
	seedBillingLog(t, db, "cursor-row", cutoff.Add(-2*time.Second))
	seedBillingLog(t, db, "tail-row", cutoff.Add(-time.Second))
	var inserted atomic.Bool
	const callbackName = "test:insert_backdated_billing_log"
	require.NoError(t, db.Callback().Delete().Before("gorm:delete").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table != "billing_logs" || !inserted.CompareAndSwap(false, true) {
			return
		}
		if err := tx.Session(&gorm.Session{NewDB: true}).
			Exec("INSERT INTO billing_logs (request_id, created_at) VALUES (?, ?)", "backdated-row", cutoff.Add(-3*time.Second).Unix()).Error; err != nil {
			tx.AddError(err)
		}
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Delete().Remove(callbackName)) })
	worker := newRetentionWorkerForTest(db, now, 5, 1)

	require.NoError(t, worker.RunOnce(t.Context()))
	requireMissingBillingRequest(t, db, "cursor-row")
	requireMissingBillingRequest(t, db, "tail-row")
	requirePresentBillingRequest(t, db, "backdated-row")

	require.NoError(t, worker.RunOnce(t.Context()))
	requireMissingBillingRequest(t, db, "backdated-row")
}

func TestBillingLogRetentionFailureStopsRunAndNextIntervalContinues(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	db := openRetentionDB(t)
	for index := range 3 {
		seedBillingLog(t, db, fmt.Sprintf("old-%d", index), now.Add(-6*24*time.Hour+time.Duration(index)*time.Second))
	}
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_last_retention_delete BEFORE DELETE ON billing_logs WHEN OLD.request_id = 'old-2' BEGIN SELECT RAISE(FAIL, 'forced retention failure'); END`).Error)
	core, observed := observer.New(zap.ErrorLevel)
	worker := newRetentionWorkerForTest(db, now, 5, 2)
	worker.logger = zap.New(core)
	worker.interval = 10 * time.Millisecond

	worker.Start(t.Context())
	require.Eventually(t, func() bool {
		return billingLogCount(t, db) == 1 && observed.Len() > 0
	}, time.Second, time.Millisecond)
	requirePresentBillingRequest(t, db, "old-2")
	require.Contains(t, observed.All()[0].Message, "billing log retention failed")

	require.NoError(t, db.Exec(`DROP TRIGGER fail_last_retention_delete`).Error)
	require.Eventually(t, func() bool { return billingLogCount(t, db) == 0 }, time.Second, time.Millisecond)
	require.NoError(t, worker.Close(t.Context()))
	requireClosed(t, worker.Done())
}

func TestBillingLogRetentionBatchPauseIsCancelable(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	db := openRetentionDB(t)
	for index := range 3 {
		seedBillingLog(t, db, fmt.Sprintf("cancel-%d", index), now.Add(-6*24*time.Hour+time.Duration(index)*time.Second))
	}
	worker := newRetentionWorkerForTest(db, now, 5, 2)
	worker.batchPause = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- worker.RunOnce(ctx) }()

	require.Eventually(t, func() bool { return billingLogCount(t, db) == 1 }, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, int64(1), billingLogCount(t, db))
}

func TestBillingLogRetentionEmptyTableCompletes(t *testing.T) {
	worker := newRetentionWorkerForTest(
		openRetentionDB(t),
		time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		5,
		2,
	)

	require.NoError(t, worker.RunOnce(t.Context()))
}

func TestFindBillingLogRetentionDaysDefaultsOnlyWhenMissing(t *testing.T) {
	tests := []struct {
		name   string
		stored *string
		want   int
	}{
		{name: "missing", want: consts.DefaultBillingLogRetentionDays},
		{name: "minimum", stored: retentionSettingValue("1"), want: 1},
		{name: "maximum", stored: retentionSettingValue("365"), want: 365},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openRetentionDB(t)
			if tt.stored != nil {
				require.NoError(t, db.Create(&models.Setting{
					Key:   consts.SettingKeyBillingLogRetentionDays,
					Value: *tt.stored,
				}).Error)
			}

			got, err := FindBillingLogRetentionDays(t.Context(), db)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBillingLogRetentionSettingFailuresDeleteNothing(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		dropSQL string
	}{
		{name: "empty", value: ""},
		{name: "malformed", value: "five"},
		{name: "below minimum", value: "0"},
		{name: "above maximum", value: "366"},
		{name: "database error", dropSQL: "DROP TABLE settings"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
			db := openRetentionDB(t)
			seedBillingLog(t, db, "must-remain", now.Add(-6*24*time.Hour))
			if tt.dropSQL != "" {
				require.NoError(t, db.Exec(tt.dropSQL).Error)
			} else {
				require.NoError(t, db.Create(&models.Setting{
					Key:   consts.SettingKeyBillingLogRetentionDays,
					Value: tt.value,
				}).Error)
			}
			worker := NewLogRetentionWorker(db, func(ctx context.Context) (int, error) {
				return FindBillingLogRetentionDays(ctx, db)
			}, zap.NewNop())
			worker.now = func() time.Time { return now }

			require.Error(t, worker.RunOnce(t.Context()))
			requirePresentBillingRequest(t, db, "must-remain")
		})
	}
}

func TestBillingLogRetentionSettingFailureStopsRoundAndNextIntervalContinues(t *testing.T) {
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	db := openRetentionDB(t)
	seedBillingLog(t, db, "old-after-retry", now.Add(-6*24*time.Hour))
	lookupErr := errors.New("transient retention setting failure")
	firstFailed := make(chan struct{})
	nextEntered := make(chan struct{}, 1)
	allowSuccess := make(chan struct{})
	var attempts atomic.Int64
	finder := func(ctx context.Context) (int, error) {
		if attempts.Add(1) == 1 {
			close(firstFailed)
			return 0, lookupErr
		}
		select {
		case nextEntered <- struct{}{}:
		default:
		}
		select {
		case <-allowSuccess:
			return consts.DefaultBillingLogRetentionDays, nil
		case <-ctx.Done():
			return 0, context.Cause(ctx)
		}
	}
	core, observed := observer.New(zap.ErrorLevel)
	worker := NewLogRetentionWorker(db, finder, zap.New(core))
	worker.now = func() time.Time { return now }
	worker.interval = time.Millisecond
	worker.batchLimit = 2
	worker.batchPause = 0

	worker.Start(t.Context())
	<-firstFailed
	requirePresentBillingRequest(t, db, "old-after-retry")
	select {
	case <-nextEntered:
	case <-time.After(time.Second):
		t.Fatal("next retention round did not retry the setting lookup")
	}
	requirePresentBillingRequest(t, db, "old-after-retry")
	close(allowSuccess)
	require.Eventually(t, func() bool { return billingLogCount(t, db) == 0 }, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, observed.Len(), 1)
	require.NoError(t, worker.Close(t.Context()))
}

func TestBillingLogRetentionBlockedSettingLookupIsCanceledByClose(t *testing.T) {
	db := openRetentionDB(t)
	lookupEntered := make(chan struct{}, 1)
	lookupCanceled := make(chan error, 1)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:block_retention_setting_lookup", func(tx *gorm.DB) {
		if tx.Statement.Table != "settings" {
			return
		}
		select {
		case lookupEntered <- struct{}{}:
		default:
		}
		<-tx.Statement.Context.Done()
		cause := context.Cause(tx.Statement.Context)
		tx.AddError(cause)
		select {
		case lookupCanceled <- cause:
		default:
		}
	}))
	worker := NewLogRetentionWorker(db, func(ctx context.Context) (int, error) {
		return FindBillingLogRetentionDays(ctx, db)
	}, zap.NewNop())

	worker.Start(t.Context())
	select {
	case <-lookupEntered:
	case <-time.After(time.Second):
		t.Fatal("retention setting lookup did not reach the database")
	}
	closeCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	require.NoError(t, worker.Close(closeCtx))
	require.Error(t, <-lookupCanceled)
	requireClosed(t, worker.Done())
}

func newRetentionWorkerForTest(db *gorm.DB, now time.Time, retentionDays, batchLimit int) *LogRetentionWorker {
	worker := NewLogRetentionWorker(db, func(context.Context) (int, error) { return retentionDays, nil }, zap.NewNop())
	worker.now = func() time.Time { return now }
	worker.batchLimit = batchLimit
	worker.batchPause = 0
	return worker
}

func openRetentionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "retention.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.AutoMigrate(&models.BillingLog{}, &models.Setting{}))
	return db
}

func retentionSettingValue(value string) *string { return &value }

func seedBillingLog(t *testing.T, db *gorm.DB, requestID string, createdAt time.Time) {
	t.Helper()
	require.NoError(t, db.Create(&models.BillingLog{RequestID: requestID, CreatedAt: createdAt.Unix()}).Error)
}

func requireMissingBillingRequest(t *testing.T, db *gorm.DB, requestID string) {
	t.Helper()
	require.ErrorIs(t, db.First(&models.BillingLog{}, "request_id = ?", requestID).Error, gorm.ErrRecordNotFound)
}

func requirePresentBillingRequest(t *testing.T, db *gorm.DB, requestID string) {
	t.Helper()
	require.NoError(t, db.First(&models.BillingLog{}, "request_id = ?", requestID).Error)
}

func billingLogCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(&models.BillingLog{}).Count(&count).Error)
	return count
}

func requireClosed(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not close")
	}
}
