package historybackfill

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type onlineBackfillFixture struct {
	t       *testing.T
	source  *gorm.DB
	core    *gorm.DB
	logDB   *gorm.DB
	worker  *Worker
	nextID  uint
	closeMu sync.Mutex
	closed  int
}

func newOnlineBackfillFixture(t *testing.T) *onlineBackfillFixture {
	t.Helper()
	sourcePath := filepath.Join(t.TempDir(), "master.db")
	source, err := gorm.Open(sqlite.Open(sourcePath), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, source.AutoMigrate(&models.UsageLog{}, &models.UsageLogTrace{}))
	core, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "core.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(core))
	logDB, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "log.db")), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(logDB))
	require.NoError(t, core.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutMonolith),
		SourcePath: sourcePath, State: string(StatePending), StartedAtUnix: time.Now().Unix(),
	}).Error)
	f := &onlineBackfillFixture{t: t, source: source, core: core, logDB: logDB, nextID: 1}
	f.worker = f.newWorker(&BillingBatchWriter{DBFinder: func() *gorm.DB { return core }})
	return f
}

func (f *onlineBackfillFixture) newWorker(billing BillingHistoryBatchWriter) *Worker {
	f.t.Helper()
	backfiller := New(Options{
		Reader:       NewLegacyReader(f.source, masterdatabase.LegacyLayoutMonolith),
		CoreDBFinder: func() *gorm.DB { return f.core }, LogDBFinder: func() *gorm.DB { return f.logDB },
		BillingHistoryWriter: billing,
		RequestHistoryWriter: &masterlogqueue.HistoricalRequestBatchWriter{DBFinder: func() *gorm.DB { return f.logDB }},
		BatchSize:            2,
	})
	return NewWorker(WorkerOptions{
		Backfiller: backfiller, RetryInterval: time.Hour,
		CloseLegacy: func() error {
			f.closeMu.Lock()
			defer f.closeMu.Unlock()
			f.closed++
			return nil
		},
	})
}

func (f *onlineBackfillFixture) startWorker() *Worker {
	f.t.Helper()
	require.NoError(f.t, f.worker.Start(f.t.Context()))
	f.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(f.t, f.worker.Stop(ctx))
	})
	return f.worker
}

func (f *onlineBackfillFixture) appendLegacyUsage(requestID string) {
	f.t.Helper()
	id := f.nextID
	f.nextID++
	require.NoError(f.t, f.source.Create(&models.UsageLog{ID: id, RequestID: requestID, CreatedAt: int64(id)}).Error)
}

func (f *onlineBackfillFixture) hasBilling(requestID string) bool {
	var count int64
	require.NoError(f.t, f.core.Model(&models.BillingLog{}).Where("request_id = ?", requestID).Count(&count).Error)
	return count == 1
}

func (f *onlineBackfillFixture) cursor(db *gorm.DB, key string) models.HistoryCursor {
	f.t.Helper()
	var cursor models.HistoryCursor
	err := db.Where("key = ?", key).First(&cursor).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.HistoryCursor{Key: key}
	}
	require.NoError(f.t, err)
	return cursor
}

func waitForWorkerState(t *testing.T, worker *Worker, state State) {
	t.Helper()
	require.Eventually(t, func() bool { return worker.Status().State == state }, time.Second, time.Millisecond)
}

func TestWorkerReturnsFromCaughtUpWhenSourceGrows(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.appendLegacyUsage("initial")
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)

	f.appendLegacyUsage("late")
	worker.RetryNow()
	require.Eventually(t, func() bool {
		return worker.Status().State == StateCaughtUp && f.hasBilling("late")
	}, time.Second, time.Millisecond)
}

func TestCompleteRunsFinalPassAndStopsPolling(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)
	f.appendLegacyUsage("before-complete")

	require.NoError(t, worker.Complete(t.Context()))
	require.Equal(t, StateCompleted, worker.Status().State)
	require.True(t, f.hasBilling("before-complete"))
	require.Equal(t, 1, f.closed)

	f.appendLegacyUsage("after-complete")
	worker.RetryNow()
	time.Sleep(20 * time.Millisecond)
	require.False(t, f.hasBilling("after-complete"))
}

type failingBillingWriter struct{ err error }

func (w failingBillingWriter) WriteHistory(context.Context, string, uint, []models.BillingLog) error {
	return w.err
}

func TestCompleteRejectsIncompleteBilling(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.appendLegacyUsage("billing-fails")
	f.worker = f.newWorker(failingBillingWriter{err: errors.New("target is busy")})
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateDegraded)

	require.ErrorContains(t, worker.Complete(t.Context()), "billing is not caught up")
	require.Equal(t, StateDegraded, worker.Status().State)
	require.Equal(t, 0, f.closed)
}

func TestSkipMarksOnlyNonCriticalLogCursors(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)

	require.NoError(t, worker.SkipRemaining(t.Context()))
	require.False(t, f.cursor(f.core, billingCursorKey).Skipped)
	require.True(t, f.cursor(f.logDB, requestCursorKey).Skipped)
	require.True(t, f.cursor(f.logDB, traceCursorKey).Skipped)
	var migration models.HistoryMigration
	require.NoError(t, f.core.First(&migration, models.HistoryMigrationSingletonID).Error)
	require.True(t, migration.SkipLogHistory)
}

func TestSkipMarksReturnsErrorWhenLogDatabaseUnavailable(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.worker.backfiller.options.LogDBFinder = func() *gorm.DB { return nil }
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateDegraded)

	require.ErrorContains(t, worker.SkipRemaining(t.Context()), "log database is unavailable")
	var migration models.HistoryMigration
	require.NoError(t, f.core.First(&migration, models.HistoryMigrationSingletonID).Error)
	require.False(t, migration.SkipLogHistory)
}

func TestCompleteIsIdempotent(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)

	require.NoError(t, worker.Complete(t.Context()))
	f.worker.backfiller.options.LogDBFinder = func() *gorm.DB { return nil }
	require.NoError(t, worker.Complete(t.Context()))
	require.Equal(t, 1, f.closed)
}

func TestCompleteRetriesLegacyCloseBeforePersistingCompleted(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	var attempts atomic.Int32
	f.worker.closeLegacy = func() error {
		if attempts.Add(1) == 1 {
			f.worker.backfiller.options.Reader = NewLegacyReader(nil, masterdatabase.LegacyLayoutMonolith)
			return errors.New("close failed")
		}
		return nil
	}
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)

	require.ErrorContains(t, worker.Complete(t.Context()), "close failed")
	status := worker.Status()
	require.Equal(t, StateDegraded, status.State)
	require.False(t, status.CanDeleteSource)
	require.NoError(t, worker.Complete(t.Context()))
	require.EqualValues(t, 2, attempts.Load())
}

func TestCompleteRetriesTerminalPersistenceWithoutReclosingLegacy(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	var closeCalls atomic.Int32
	f.worker.closeLegacy = func() error { closeCalls.Add(1); return nil }
	var failCompleted atomic.Bool
	failCompleted.Store(true)
	require.NoError(t, f.core.Callback().Update().Before("gorm:update").Register("test:fail_completed_state", func(db *gorm.DB) {
		values, ok := db.Statement.Dest.(map[string]any)
		if ok && failCompleted.Load() && values["state"] == string(StateCompleted) {
			db.AddError(errors.New("completed state unavailable"))
		}
	}))
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)

	require.ErrorContains(t, worker.Complete(t.Context()), "completed state unavailable")
	require.EqualValues(t, 1, closeCalls.Load())
	require.Equal(t, StateDegraded, worker.Status().State)
	failCompleted.Store(false)
	require.NoError(t, worker.Complete(t.Context()))
	require.EqualValues(t, 1, closeCalls.Load())
	require.Equal(t, StateCompleted, worker.Status().State)
}

func TestConcurrentCompleteCallsAreIdempotent(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	waitForWorkerState(t, worker, StateCaughtUp)
	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			results <- worker.Complete(context.Background())
		}()
	}
	close(start)
	require.NoError(t, <-results)
	require.NoError(t, <-results)
	require.Equal(t, StateCompleted, worker.Status().State)
}

func TestStatusDegradesWhenStatePersistenceFailsThenRecovers(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	var failUpdates atomic.Bool
	failUpdates.Store(true)
	require.NoError(t, f.core.Callback().Update().Before("gorm:update").Register("test:fail_history_state", func(db *gorm.DB) {
		if failUpdates.Load() && db.Statement.Table == (models.HistoryMigration{}).TableName() {
			db.AddError(errors.New("state store unavailable"))
		}
	}))
	worker := f.startWorker()
	require.Eventually(t, func() bool {
		status := worker.Status()
		return status.State == StateDegraded && status.LastError == "update history migration: state store unavailable" && !status.CanComplete
	}, time.Second, time.Millisecond)

	failUpdates.Store(false)
	worker.RetryNow()
	waitForWorkerState(t, worker, StateCaughtUp)
	require.Empty(t, worker.Status().LastError)
}

func TestCompletedWorkerDoesNotReadLegacySourceOnStart(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	require.NoError(t, f.core.Model(&models.HistoryMigration{}).
		Where("id = ?", models.HistoryMigrationSingletonID).
		Update("state", string(StateCompleted)).Error)
	f.worker.backfiller.options.Reader = nil
	worker := f.startWorker()
	require.Eventually(t, func() bool {
		select {
		case <-worker.done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
	require.Equal(t, StateCompleted, worker.Status().State)
}

func TestWorkerRetriesLegacyReaderOpenUntilSourceRecovers(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.appendLegacyUsage("opened-after-retry")
	f.worker.backfiller.options.Reader = nil
	var available atomic.Bool
	var attempts atomic.Int32
	f.worker.retryInterval = 5 * time.Millisecond
	f.worker.openLegacyReader = func() (*LegacyReader, func() error, error) {
		attempts.Add(1)
		if !available.Load() {
			return nil, nil, errors.New("legacy source unavailable")
		}
		return NewLegacyReader(f.source, masterdatabase.LegacyLayoutMonolith), func() error {
			f.closeMu.Lock()
			defer f.closeMu.Unlock()
			f.closed++
			return nil
		}, nil
	}
	worker := f.startWorker()
	require.Eventually(t, func() bool {
		status := worker.Status()
		return attempts.Load() >= 2 && status.State == StateDegraded && status.LastError != ""
	}, time.Second, time.Millisecond)

	available.Store(true)
	require.Eventually(t, func() bool {
		return worker.Status().State == StateCaughtUp && f.hasBilling("opened-after-retry")
	}, time.Second, time.Millisecond)
	require.GreaterOrEqual(t, attempts.Load(), int32(3))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, worker.Stop(ctx))
	require.Equal(t, 1, f.closed)
}

func TestStopHaltsLegacyReaderOpenRetries(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.worker.backfiller.options.Reader = nil
	var attempts atomic.Int32
	f.worker.retryInterval = time.Millisecond
	f.worker.openLegacyReader = func() (*LegacyReader, func() error, error) {
		attempts.Add(1)
		return nil, nil, errors.New("legacy source unavailable")
	}
	require.NoError(t, f.worker.Start(t.Context()))
	require.Eventually(t, func() bool { return attempts.Load() >= 3 }, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, f.worker.Stop(ctx))
	stoppedAt := attempts.Load()
	time.Sleep(10 * time.Millisecond)
	require.Equal(t, stoppedAt, attempts.Load())
}

func TestCompleteValidatesContext(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	require.ErrorContains(t, worker.Complete(nil), "context is nil")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, worker.Complete(ctx), context.Canceled)
}

func TestSkipMarksValidatesContext(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	worker := f.startWorker()
	require.ErrorContains(t, worker.SkipRemaining(nil), "context is nil")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, worker.SkipRemaining(ctx), context.Canceled)
}

type blockingBillingWriter struct {
	started chan struct{}
	release chan struct{}
	inner   BillingHistoryBatchWriter
}

func (w *blockingBillingWriter) WriteHistory(ctx context.Context, key string, id uint, rows []models.BillingLog) error {
	select {
	case <-w.started:
	default:
		close(w.started)
	}
	select {
	case <-w.release:
		return w.inner.WriteHistory(ctx, key, id, rows)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func TestStopAndCompleteAreSerializedWithActivePass(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.appendLegacyUsage("blocked")
	blocking := &blockingBillingWriter{
		started: make(chan struct{}), release: make(chan struct{}),
		inner: &BillingBatchWriter{DBFinder: func() *gorm.DB { return f.core }},
	}
	f.worker = f.newWorker(blocking)
	worker := f.startWorker()
	require.Eventually(t, func() bool {
		select {
		case <-blocking.started:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	completeDone := make(chan error, 1)
	go func() { completeDone <- worker.Complete(context.Background()) }()
	require.Eventually(t, func() bool { return len(worker.commands) == 1 }, time.Second, time.Millisecond)
	stopDone := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		stopDone <- worker.Stop(ctx)
	}()
	select {
	case <-stopDone:
		t.Fatal("stop returned before the active batch completed")
	case <-completeDone:
		t.Fatal("complete returned before the active batch completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(blocking.release)
	require.NoError(t, <-stopDone)
	require.ErrorIs(t, <-completeDone, errWorkerStopped)
}

func TestStopTimeoutDoesNotCancelActivePassAndWorkerFinishesAfterRelease(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.appendLegacyUsage("blocked-stop")
	blocking := &blockingBillingWriter{
		started: make(chan struct{}), release: make(chan struct{}),
		inner: &BillingBatchWriter{DBFinder: func() *gorm.DB { return f.core }},
	}
	f.worker = f.newWorker(blocking)
	worker := f.startWorker()
	select {
	case <-blocking.started:
	case <-time.After(time.Second):
		t.Fatal("active history pass did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, worker.Stop(ctx), context.DeadlineExceeded)
	select {
	case <-worker.done:
		t.Fatal("worker stopped before active pass was released")
	default:
	}
	close(blocking.release)
	require.Eventually(t, func() bool {
		select {
		case <-worker.done:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)
}
