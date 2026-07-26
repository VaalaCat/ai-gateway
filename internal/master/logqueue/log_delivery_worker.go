package logqueue

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/sourcegraph/conc"
	"gorm.io/gorm"
)

type DatabaseConnector interface {
	Open(context.Context) (*gorm.DB, error)
}

type DatabaseConnectorFunc func(context.Context) (*gorm.DB, error)

func (f DatabaseConnectorFunc) Open(ctx context.Context) (*gorm.DB, error) { return f(ctx) }

type SQLiteLogConnector struct {
	Path      string
	Connector *masterdatabase.Connector
}

func (c SQLiteLogConnector) Open(context.Context) (*gorm.DB, error) {
	connector := c.Connector
	if connector == nil {
		connector = masterdatabase.NewConnector()
	}
	return connector.OpenLog(c.Path)
}

type WorkerOptions struct {
	Queue        *deliveryqueue.Queue[LogBatch]
	Writer       *LogBatchWriter
	Settings     SettingsFinder
	Connector    DatabaseConnector
	Handoff      func(*gorm.DB) *gorm.DB
	Metrics      DeliveryMetrics
	SnapshotPath string

	PollInterval     time.Duration
	SettingsInterval time.Duration
	SnapshotInterval time.Duration
	FlushTimeout     time.Duration
	RetryBase        time.Duration
	RetryMax         time.Duration
	OnError          func(error)
}

type LogDeliveryWorker struct {
	queue     *deliveryqueue.Queue[LogBatch]
	writer    *LogBatchWriter
	settings  SettingsFinder
	connector DatabaseConnector
	handoff   func(*gorm.DB) *gorm.DB
	metrics   DeliveryMetrics
	onError   func(error)

	pollInterval     time.Duration
	settingsInterval time.Duration
	flushTimeout     time.Duration
	retryBase        time.Duration
	retryMax         time.Duration
	snapshotter      *deliveryqueue.Snapshotter[LogBatch]

	accepting         atomic.Bool
	schemaReady       atomic.Bool
	reconnectRequired atomic.Bool
	retryRequested    atomic.Bool
	finalizeRequested atomic.Bool
	// admissionMu linearizes accepting checks with Stop. Once Stop acquires
	// it, every earlier accepted enqueue has already reached the queue.
	admissionMu          sync.Mutex
	lifecycleMu          sync.Mutex
	statusMu             sync.RWMutex
	lastError            string
	started              bool
	cancel               context.CancelCauseFunc
	parent               context.Context
	done                 chan struct{}
	wake                 chan struct{}
	workers              conc.WaitGroup
	watchers             conc.WaitGroup
	finalizeOnce         sync.Once
	afterEnqueueAdmitted func() // deterministic concurrency test hook
	afterFinalizeRequest func() // deterministic concurrency test hook
}

func NewLogDeliveryWorker(options WorkerOptions) *LogDeliveryWorker {
	if options.Queue == nil {
		defaults := DefaultDeliverySettings()
		options.Queue = deliveryqueue.New(deliveryqueue.Limits{MaxEntries: defaults.QueueMaxEntries, MaxBytes: defaults.QueueMaxBytes}, BatchSize, nil)
	}
	if options.Settings == nil {
		options.Settings = staticSettingsFinder{settings: DefaultDeliverySettings()}
	}
	if options.Metrics == nil {
		options.Metrics = noopDeliveryMetrics{}
	}
	if options.PollInterval <= 0 {
		options.PollInterval = 100 * time.Millisecond
	}
	if options.SettingsInterval <= 0 {
		options.SettingsInterval = 5 * time.Second
	}
	if options.FlushTimeout <= 0 {
		options.FlushTimeout = 2 * time.Second
	}
	if options.RetryBase <= 0 {
		options.RetryBase = time.Second
	}
	worker := &LogDeliveryWorker{
		queue: options.Queue, writer: options.Writer, settings: options.Settings,
		connector: options.Connector, handoff: options.Handoff, metrics: options.Metrics,
		onError: options.OnError, pollInterval: options.PollInterval, settingsInterval: options.SettingsInterval, flushTimeout: options.FlushTimeout,
		retryBase: options.RetryBase, retryMax: options.RetryMax, wake: make(chan struct{}, 1),
		done: make(chan struct{}),
	}
	worker.snapshotter = &deliveryqueue.Snapshotter[LogBatch]{
		Queue: options.Queue, Path: options.SnapshotPath, Interval: options.SnapshotInterval,
		OnError: func(err error) { worker.metrics.SnapshotFailure(); worker.report(err) },
	}
	return worker
}

type staticSettingsFinder struct{ settings DeliverySettings }

func (f staticSettingsFinder) Find(context.Context) DeliverySettings { return f.settings }

func (w *LogDeliveryWorker) Start(parent context.Context) error {
	w.lifecycleMu.Lock()
	defer w.lifecycleMu.Unlock()
	if parent == nil {
		return fmt.Errorf("log delivery worker requires a context")
	}
	if err := context.Cause(parent); err != nil {
		return err
	}
	if w.started {
		return fmt.Errorf("log delivery worker already started")
	}
	if err := w.snapshotter.Restore(); err != nil {
		w.metrics.SnapshotFailure()
		w.report(err)
	}
	ctx, cancel := context.WithCancelCause(context.WithoutCancel(parent))
	w.cancel = cancel
	w.parent = parent
	w.started = true
	w.accepting.Store(true)
	w.workers.Go(func() { w.snapshotter.Run(ctx) })
	w.workers.Go(func() { w.run(ctx) })
	w.watchers.Go(func() {
		select {
		case <-parent.Done():
			w.finalize(context.Cause(parent))
		case <-w.done:
		}
	})
	w.signal()
	return nil
}

func (w *LogDeliveryWorker) Enqueue(batch LogBatch) deliveryqueue.EnqueueResult {
	w.admissionMu.Lock()
	defer w.admissionMu.Unlock()
	if !w.accepting.Load() || w.finalizeRequested.Load() || context.Cause(w.parent) != nil {
		return deliveryqueue.EnqueueResult{Dropped: true, Error: "log delivery worker is not accepting"}
	}
	if w.afterEnqueueAdmitted != nil {
		w.afterEnqueueAdmitted()
	}
	result := w.queue.Enqueue(batch)
	w.observe()
	w.signal()
	return result
}

func (w *LogDeliveryWorker) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("log delivery worker stop requires a context")
	}
	w.lifecycleMu.Lock()
	if !w.started {
		w.lifecycleMu.Unlock()
		return nil
	}
	w.lifecycleMu.Unlock()
	select {
	case <-w.done:
		w.watchers.Wait()
		return nil
	default:
	}
	w.requestFinalization()
	w.signal()
	flushCtx, cancelFlush := context.WithTimeout(ctx, w.flushTimeout)
	w.waitForDrain(flushCtx)
	cancelFlush()
	w.finalize(errors.New("log delivery worker stopped"))
	w.watchers.Wait()
	return nil
}

func (w *LogDeliveryWorker) SchemaReady() bool                     { return w.schemaReady.Load() }
func (w *LogDeliveryWorker) Queue() *deliveryqueue.Queue[LogBatch] { return w.queue }

type DeliveryStatus struct {
	Queue       deliveryqueue.Stats
	SchemaReady bool
	LastError   string
}

func (w *LogDeliveryWorker) Status() DeliveryStatus {
	w.statusMu.RLock()
	lastError := w.lastError
	w.statusMu.RUnlock()
	return DeliveryStatus{Queue: w.queue.Stats(), SchemaReady: w.schemaReady.Load(), LastError: lastError}
}

func (w *LogDeliveryWorker) RetryNow() {
	w.queue.RetryNow()
	w.retryRequested.Store(true)
	w.observe()
	w.signal()
}

func (w *LogDeliveryWorker) ClearBacklog() (deliveryqueue.ClearResult, error) {
	cleared := w.queue.ClearBacklog()
	w.metrics.BacklogCleared(w.queue.Stats().Dropped, uint64(cleared.Items))
	w.observe()
	if err := w.snapshotter.WriteNow(); err != nil {
		w.metrics.SnapshotFailure()
		w.report(err)
		return cleared, err
	}
	w.signal()
	return cleared, nil
}

func (w *LogDeliveryWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	var nextConnect time.Time
	connectAttempts := 0
	settings := DefaultDeliverySettings()
	var nextSettings time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
		if time.Now().After(nextSettings) {
			settings = w.applySettings(ctx)
			nextSettings = time.Now().Add(w.settingsInterval)
		}
		if w.retryRequested.Swap(false) {
			nextConnect = time.Time{}
		}
		if !w.schemaReady.Load() {
			if time.Now().Before(nextConnect) {
				continue
			}
			if err := w.prepareDatabase(ctx); err != nil {
				w.report(err)
				connectAttempts++
				delay := deliveryqueue.Backoff("log-database", connectAttempts, w.retryBase, w.retryMaximum(settings), 0.2)
				nextConnect = time.Now().Add(delay)
				w.observe()
				continue
			}
			connectAttempts = 0
		}
		w.deliverReady(ctx, settings)
	}
}

func (w *LogDeliveryWorker) requestFinalization() {
	first := w.finalizeRequested.CompareAndSwap(false, true)
	if first && w.afterFinalizeRequest != nil {
		w.afterFinalizeRequest()
	}
	w.admissionMu.Lock()
	w.accepting.Store(false)
	w.admissionMu.Unlock()
}

func (w *LogDeliveryWorker) finalize(cause error) {
	w.finalizeOnce.Do(func() {
		w.requestFinalization()
		if cause == nil {
			cause = errors.New("log delivery worker finalized")
		}
		w.lifecycleMu.Lock()
		cancel := w.cancel
		w.lifecycleMu.Unlock()
		if cancel != nil {
			cancel(cause)
		}
		w.workers.Wait()
		if err := w.snapshotter.WriteNow(); err != nil {
			w.metrics.SnapshotFailure()
			w.report(err)
		}
		w.observe()
		close(w.done)
	})
}

func (w *LogDeliveryWorker) applySettings(ctx context.Context) DeliverySettings {
	settings := w.settings.Find(ctx)
	w.queue.UpdateLimits(deliveryqueue.Limits{MaxEntries: settings.QueueMaxEntries, MaxBytes: settings.QueueMaxBytes})
	return settings
}

func (w *LogDeliveryWorker) prepareDatabase(ctx context.Context) error {
	var db *gorm.DB
	opened := false
	forceOpen := w.reconnectRequired.Load()
	if w.writer != nil && w.writer.DBFinder != nil {
		db = w.writer.DBFinder()
	}
	if forceOpen || !databaseUsable(ctx, db) {
		if w.connector == nil {
			return fmt.Errorf("log database is unavailable")
		}
		openedDB, err := w.connector.Open(ctx)
		if err != nil {
			return fmt.Errorf("open log database: %w", err)
		}
		db = openedDB
		opened = true
	}
	if err := models.MigrateLogDB(db); err != nil {
		closeOpenedDatabase(db, opened)
		return err
	}
	if opened && w.handoff == nil {
		closeOpenedDatabase(db, true)
		return fmt.Errorf("log database connector requires an atomic handoff")
	}
	if w.handoff != nil {
		old := w.handoff(db)
		if old != db {
			closeOpenedDatabase(old, true)
		}
	}
	w.reconnectRequired.Store(false)
	w.schemaReady.Store(true)
	w.observe()
	return nil
}

func closeOpenedDatabase(db *gorm.DB, opened bool) {
	if !opened || db == nil {
		return
	}
	if sqlDB, err := db.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

func databaseUsable(ctx context.Context, db *gorm.DB) bool {
	if db == nil {
		return false
	}
	sqlDB, err := db.DB()
	return err == nil && sqlDB.PingContext(ctx) == nil
}

func (w *LogDeliveryWorker) deliverReady(ctx context.Context, settings DeliverySettings) {
	items := w.queue.TakeReady(settings.DeliveryBatchSize)
	if len(items) == 0 {
		w.observe()
		return
	}
	ids := make([]string, len(items))
	batches := make([]LogBatch, len(items))
	for i, item := range items {
		ids[i], batches[i] = item.ID, item.Value
	}
	if w.writer == nil {
		w.retry(ids, fmt.Errorf("log batch writer is unavailable"), settings)
		return
	}
	if err := w.writer.Write(ctx, batches); err != nil {
		w.metrics.WriteFailure()
		if !errors.Is(err, ErrInvalidLogBatch) {
			w.reconnectRequired.Store(true)
			w.schemaReady.Store(false)
		}
		w.retry(ids, err, settings)
		return
	}
	w.queue.Ack(ids...)
	w.observe()
	w.signal()
}

func (w *LogDeliveryWorker) retry(ids []string, err error, settings DeliverySettings) {
	w.queue.RetryWithBackoff(ids, err, w.retryBase, w.retryMaximum(settings))
	w.report(err)
	w.observe()
}

func (w *LogDeliveryWorker) retryMaximum(settings DeliverySettings) time.Duration {
	if w.retryMax > 0 {
		return w.retryMax
	}
	return time.Duration(settings.BackoffMaxSeconds) * time.Second
}

func (w *LogDeliveryWorker) waitForDrain(ctx context.Context) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		stats := w.queue.Stats()
		if stats.Pending+stats.Retry+stats.Inflight == 0 {
			return
		}
		select {
		case <-w.done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.queue.RetryNow()
			w.signal()
		}
	}
}

func (w *LogDeliveryWorker) signal() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *LogDeliveryWorker) observe() { w.metrics.Observe(w.queue.Stats(), w.schemaReady.Load()) }

func (w *LogDeliveryWorker) report(err error) {
	if err == nil {
		return
	}
	w.statusMu.Lock()
	w.lastError = err.Error()
	w.statusMu.Unlock()
	if w.onError != nil {
		w.onError(err)
	}
}
