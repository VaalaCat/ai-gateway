package logqueue

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestLogDeliveryWorkerWaitsForSchemaReady(t *testing.T) {
	unmarked := newLogTestDB(t)
	require.NoError(t, unmarked.Exec("DELETE FROM database_layouts").Error)
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	require.True(t, queue.Enqueue(completeTestBatch("waiting")).Accepted)
	worker := NewLogDeliveryWorker(WorkerOptions{
		Queue: queue, Writer: &LogBatchWriter{DBFinder: func() *gorm.DB { return unmarked }},
		SnapshotPath: filepath.Join(t.TempDir(), "log_backlog.snapshot.gz"), PollInterval: 5 * time.Millisecond, FlushTimeout: 10 * time.Millisecond,
	})

	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	time.Sleep(30 * time.Millisecond)

	require.False(t, worker.SchemaReady())
	require.Equal(t, 1, queue.Stats().Pending)
}

func TestLogDeliveryWorkerConnectorMigratesThenConsumes(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	var current atomic.Pointer[gorm.DB]
	var attempts atomic.Int64
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	queue.Enqueue(completeTestBatch("recovered"))
	worker := NewLogDeliveryWorker(WorkerOptions{
		Queue: queue, Writer: &LogBatchWriter{DBFinder: current.Load},
		Connector: DatabaseConnectorFunc(func(context.Context) (*gorm.DB, error) {
			if attempts.Add(1) == 1 {
				return nil, errors.New("offline")
			}
			return db, nil
		}),
		Handoff: func(db *gorm.DB) *gorm.DB { return current.Swap(db) }, SnapshotPath: filepath.Join(t.TempDir(), "log_backlog.snapshot.gz"),
		PollInterval: 5 * time.Millisecond, RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond, FlushTimeout: 10 * time.Millisecond,
	})

	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	require.Eventually(t, func() bool { return queue.Stats().Pending+queue.Stats().Retry+queue.Stats().Inflight == 0 }, time.Second, 5*time.Millisecond)
	require.True(t, worker.SchemaReady())
	require.GreaterOrEqual(t, attempts.Load(), int64(2))
	requireCounts(t, db, map[any]int64{&models.RequestLog{}: 1})
}

func TestLogDeliveryWorkerRetryNowWakesConnectorAndKeepsBacklogOnFailure(t *testing.T) {
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	require.True(t, queue.Enqueue(completeTestBatch("waiting-for-log-db")).Accepted)
	attempted := make(chan struct{}, 4)
	worker := NewLogDeliveryWorker(WorkerOptions{
		Queue: queue,
		Connector: DatabaseConnectorFunc(func(context.Context) (*gorm.DB, error) {
			attempted <- struct{}{}
			return nil, errors.New("offline")
		}),
		SnapshotPath: filepath.Join(t.TempDir(), "log_backlog.snapshot.gz"),
		PollInterval: time.Hour, RetryBase: time.Hour, RetryMax: time.Hour,
	})
	require.NoError(t, worker.Start(t.Context()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("initial connector attempt did not run")
	}

	worker.RetryNow()
	select {
	case <-attempted:
	case <-time.After(time.Second):
		t.Fatal("RetryNow did not bypass connector backoff and wake worker")
	}
	status := worker.Status()
	require.Equal(t, 1, status.Queue.Pending)
	require.False(t, status.SchemaReady)
	require.Contains(t, status.LastError, "offline")
}

func TestLogDeliveryWorkerClearBacklogKeepsInflightAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log_backlog.snapshot.gz")
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	require.True(t, queue.Enqueue(completeTestBatch("inflight")).Accepted)
	require.True(t, queue.Enqueue(completeTestBatch("pending")).Accepted)
	require.Len(t, queue.TakeReady(1), 1)
	queue.Put(deliveryqueue.Item[LogBatch]{ID: "retry", Value: completeTestBatch("retry"), NextAttempt: time.Now().Add(time.Hour)}, deliveryqueue.Retry)
	worker := NewLogDeliveryWorker(WorkerOptions{Queue: queue, SnapshotPath: path})

	cleared, err := worker.ClearBacklog()
	require.NoError(t, err)
	require.Equal(t, 1, cleared.Pending)
	require.Equal(t, 1, cleared.Retry)
	require.Equal(t, 2, cleared.Items)
	require.Equal(t, 1, worker.Status().Queue.Inflight)

	snapshot, err := deliveryqueue.ReadSnapshot[LogBatch](path)
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 1)
	require.Equal(t, deliveryqueue.Inflight, snapshot.Items[0].State)
}

func TestLogDeliveryWorkerIsolatesPoisonAndContinues(t *testing.T) {
	db := newLogTestDB(t)
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	queue.Enqueue(completeTestBatch(""))
	queue.Enqueue(completeTestBatch("healthy"))
	worker := NewLogDeliveryWorker(WorkerOptions{
		Queue: queue, Writer: &LogBatchWriter{DBFinder: func() *gorm.DB { return db }},
		SnapshotPath: filepath.Join(t.TempDir(), "log_backlog.snapshot.gz"), PollInterval: time.Millisecond,
		RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond, FlushTimeout: 10 * time.Millisecond,
	})

	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	require.Eventually(t, func() bool {
		var count int64
		return db.Model(&models.RequestLog{}).Where("request_id = ?", "healthy").Count(&count).Error == nil && count == 1
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, worker.Stop(context.Background()))
	require.Equal(t, 1, queue.Stats().Retry)
	require.True(t, worker.SchemaReady(), "invalid payload is poison, not a database recovery signal")
}

func TestLogDeliveryWorkerRestoresSnapshotBeforeAccepting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log_backlog.snapshot.gz")
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	queue.Enqueue(completeTestBatch("snapshot"))
	require.NoError(t, (&deliveryqueue.Snapshotter[LogBatch]{Queue: queue, Path: path}).WriteNow())
	restored := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	worker := NewLogDeliveryWorker(WorkerOptions{Queue: restored, SnapshotPath: path, PollInterval: 5 * time.Millisecond, FlushTimeout: 10 * time.Millisecond})

	require.NoError(t, worker.Start(context.Background()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	require.Equal(t, 1, restored.Stats().Pending)
}

func TestLogDeliveryWorkerStopWaitsForAdmittedEnqueueBeforeSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log_backlog.snapshot.gz")
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	worker := NewLogDeliveryWorker(WorkerOptions{Queue: queue, SnapshotPath: path, PollInterval: time.Millisecond, FlushTimeout: 5 * time.Millisecond})
	admitted := make(chan struct{})
	release := make(chan struct{})
	var firstAdmission atomic.Bool
	worker.afterEnqueueAdmitted = func() {
		if firstAdmission.CompareAndSwap(false, true) {
			close(admitted)
			<-release
		}
	}
	require.NoError(t, worker.Start(t.Context()))
	result := make(chan deliveryqueue.EnqueueResult, 1)
	go func() { result <- worker.Enqueue(completeTestBatch("admitted")) }()
	<-admitted
	stopped := make(chan error, 1)
	go func() { stopped <- worker.Stop(context.Background()) }()
	select {
	case err := <-stopped:
		t.Fatalf("Stop returned before admitted enqueue completed: %v", err)
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	require.True(t, (<-result).Accepted)
	require.NoError(t, <-stopped)
	snapshot, err := deliveryqueue.ReadSnapshot[LogBatch](path)
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 1)
	require.Equal(t, "admitted", snapshot.Items[0].Item.Value.Request.RequestID)
}

func TestLogDeliveryWorkerParentCancellationWaitsForAdmissionAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log_backlog.snapshot.gz")
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, fixedLogClock{now: time.Unix(100, 0)})
	worker := NewLogDeliveryWorker(WorkerOptions{Queue: queue, SnapshotPath: path, PollInterval: time.Millisecond, FlushTimeout: 5 * time.Millisecond})
	parent, cancelParent := context.WithCancelCause(context.Background())
	admitted := make(chan struct{})
	release := make(chan struct{})
	finalizationRequested := make(chan struct{})
	var firstAdmission atomic.Bool
	worker.afterEnqueueAdmitted = func() {
		if firstAdmission.CompareAndSwap(false, true) {
			close(admitted)
			<-release
		}
	}
	worker.afterFinalizeRequest = func() { close(finalizationRequested) }
	require.NoError(t, worker.Start(parent))
	result := make(chan deliveryqueue.EnqueueResult, 1)
	go func() { result <- worker.Enqueue(completeTestBatch("parent-cancel")) }()
	<-admitted
	cancelParent(errors.New("parent stopped"))
	<-finalizationRequested
	close(release)
	require.True(t, (<-result).Accepted)
	require.True(t, worker.Enqueue(completeTestBatch("too-late")).Dropped)
	require.NoError(t, worker.Stop(context.Background()))
	require.NoError(t, worker.Stop(context.Background()), "Stop after parent cancellation must be idempotent")

	snapshot, err := deliveryqueue.ReadSnapshot[LogBatch](path)
	require.NoError(t, err)
	require.Len(t, snapshot.Items, 1)
	require.Equal(t, "parent-cancel", snapshot.Items[0].Item.Value.Request.RequestID)
}

type fixedLogClock struct{ now time.Time }

func (c fixedLogClock) Now() time.Time { return c.now }

func TestLogDeliveryWorkerPersistenceFailureForcesReconnectAndClosesOldDatabase(t *testing.T) {
	db1 := newLogTestDB(t)
	db2 := newLogTestDB(t)
	db3 := newLogTestDB(t)
	sql1, _ := db1.DB()
	sql2, _ := db2.DB()
	var current atomic.Pointer[gorm.DB]
	current.Store(db1)
	connections := make(chan *gorm.DB, 2)
	connections <- db2
	connections <- db3
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10}, BatchSize, nil)
	worker := NewLogDeliveryWorker(WorkerOptions{
		Queue: queue, Writer: &LogBatchWriter{DBFinder: current.Load},
		Connector: DatabaseConnectorFunc(func(context.Context) (*gorm.DB, error) { return <-connections, nil }),
		Handoff:   current.Swap, SnapshotPath: filepath.Join(t.TempDir(), "log_backlog.snapshot.gz"),
		PollInterval: time.Millisecond, RetryBase: time.Millisecond, RetryMax: 2 * time.Millisecond,
	})
	require.NoError(t, worker.Start(t.Context()))
	t.Cleanup(func() { _ = worker.Stop(context.Background()) })
	require.Eventually(t, worker.SchemaReady, time.Second, time.Millisecond)

	require.NoError(t, db1.Migrator().DropTable(&models.RequestLog{}))
	require.True(t, queue.Enqueue(completeTestBatch("recover-one")).Accepted)
	require.Eventually(t, func() bool {
		return current.Load() == db2 && queue.Stats().Pending+queue.Stats().Retry+queue.Stats().Inflight == 0
	}, time.Second, time.Millisecond)
	require.Error(t, sql1.Ping(), "first replaced pool must be closed")

	require.NoError(t, db2.Migrator().DropTable(&models.RequestLog{}))
	require.True(t, queue.Enqueue(completeTestBatch("recover-two")).Accepted)
	require.Eventually(t, func() bool {
		return current.Load() == db3 && queue.Stats().Pending+queue.Stats().Retry+queue.Stats().Inflight == 0
	}, time.Second, time.Millisecond)
	require.Error(t, sql2.Ping(), "second replaced pool must be closed")
	sql3, _ := db3.DB()
	require.NoError(t, sql3.Ping(), "current pool must remain open")
}
