package apiusage

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

const (
	QueueCapacityKey         = "api_usage_queue_capacity"
	DefaultQueueCapacity     = 10000
	MinimumQueueCapacity     = 100
	MaximumQueueCapacity     = 1000000
	WorkerConcurrencyKey     = "api_usage_worker_concurrency"
	DefaultWorkerConcurrency = 2
	MinimumWorkerConcurrency = 1
	MaximumWorkerConcurrency = 32

	maxSettlementAttempts = 3
)

type WorkerSettings struct {
	QueueCapacity int
	Concurrency   int
}

type WorkerSettingsFinder interface {
	Find(context.Context) WorkerSettings
}

type staticWorkerSettings struct{ value WorkerSettings }

func (s staticWorkerSettings) Find(context.Context) WorkerSettings { return s.value }

type coreWorkerSettings struct{ app dao.AppProvider }

func NewCoreWorkerSettingsFinder(application dao.AppProvider) WorkerSettingsFinder {
	return coreWorkerSettings{app: application}
}

func (f coreWorkerSettings) Find(ctx context.Context) WorkerSettings {
	if f.app == nil {
		return WorkerSettings{QueueCapacity: DefaultQueueCapacity, Concurrency: DefaultWorkerConcurrency}
	}
	settings := dao.NewAdminQuery(dao.NewContextWithContext(f.app, ctx)).Setting()
	return WorkerSettings{
		QueueCapacity: settings.LookupInt(QueueCapacityKey, DefaultQueueCapacity),
		Concurrency:   settings.LookupInt(WorkerConcurrencyKey, DefaultWorkerConcurrency),
	}
}

type WorkerOptions struct {
	Queue     *APIUsageQueue
	Settler   UsageSettler
	Settings  WorkerSettingsFinder
	Poll      time.Duration
	RetryBase time.Duration
	Metrics   Metrics
	Logger    *zap.Logger
}

type UsageSettler interface {
	Settle(context.Context, protocol.APIUsageEntry) (APISettlement, error)
}

// APIUsageWorker decouples HTTP acknowledgement from settlement. It retries
// only the settlement call; once Settle succeeds, LogDeliveryWorker owns any
// later Log DB retry and quota mutation is never invoked from that retry.
type APIUsageWorker struct {
	queue     *APIUsageQueue
	settler   UsageSettler
	settings  WorkerSettingsFinder
	poll      time.Duration
	retryBase time.Duration
	metrics   Metrics
	logger    *zap.Logger
	started   atomic.Bool
	stopping  atomic.Bool
	cancel    context.CancelFunc
	workers   conc.WaitGroup
	waiter    conc.WaitGroup
	done      chan struct{}
	stopOnce  sync.Once
}

func NewWorker(options WorkerOptions) *APIUsageWorker {
	if options.Settings == nil {
		options.Settings = staticWorkerSettings{value: WorkerSettings{QueueCapacity: DefaultQueueCapacity, Concurrency: 1}}
	}
	if options.Poll <= 0 {
		options.Poll = 20 * time.Millisecond
	}
	if options.RetryBase <= 0 {
		options.RetryBase = 20 * time.Millisecond
	}
	if options.Metrics == nil {
		options.Metrics = noopMetrics{}
	}
	if options.Logger == nil {
		options.Logger = zap.NewNop()
	}
	return &APIUsageWorker{queue: options.Queue, settler: options.Settler, settings: options.Settings, poll: options.Poll, retryBase: options.RetryBase, metrics: options.Metrics, logger: options.Logger, done: make(chan struct{})}
}

func (w *APIUsageWorker) Start(parent context.Context) {
	if w == nil || w.queue == nil || w.settler == nil || !w.started.CompareAndSwap(false, true) {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	settings := w.settings.Find(ctx)
	w.queue.UpdateCapacity(settings.QueueCapacity)
	concurrency := settings.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	for range concurrency {
		w.workers.Go(func() { w.run(ctx) })
	}
	w.waiter.Go(func() { w.workers.Wait(); close(w.done) })
}

func (w *APIUsageWorker) Stop(ctx context.Context) error {
	if w == nil || !w.started.Load() {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("api usage worker stop requires a context")
	}
	w.stopOnce.Do(func() { w.queue.CloseAdmission(); w.stopping.Store(true) })
	if err := w.Wait(ctx); err != nil {
		w.cancel()
		return err
	}
	return nil
}

// Wait keeps the worker's downstream dependencies owned until every
// settlement goroutine has actually returned. It does not initiate shutdown.
func (w *APIUsageWorker) Wait(ctx context.Context) error {
	if w == nil || !w.started.Load() {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("api usage worker wait requires a context")
	}
	select {
	case <-w.done:
		w.waiter.Wait()
		w.started.Store(false)
		w.cancel()
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (w *APIUsageWorker) run(ctx context.Context) {
	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()
	for {
		item, ok := w.queue.Take()
		if ok {
			w.settleWithRetry(ctx, item)
			continue
		}
		if w.stopping.Load() {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *APIUsageWorker) settleWithRetry(ctx context.Context, item QueuedAPIUsage) {
	var err error
	for attempt := 1; attempt <= maxSettlementAttempts; attempt++ {
		_, err = w.settler.Settle(ctx, item.Entry)
		if err == nil || errors.Is(err, context.Canceled) {
			return
		}
		if attempt == maxSettlementAttempts {
			break
		}
		delay := w.retryBase * time.Duration(1<<(attempt-1))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
	w.metrics.RetryExhausted()
	w.logger.Error("api usage settlement retries exhausted", zap.Int("attempts", maxSettlementAttempts), zap.Error(err))
}
