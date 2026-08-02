package billing

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	billingLogRetentionInterval   = 24 * time.Hour
	billingLogRetentionBatchSize  = 500
	billingLogRetentionBatchPause = time.Second
)

// BillingLogRetentionDaysFinder returns the validated live retention policy.
type BillingLogRetentionDaysFinder func(context.Context) (int, error)

// LogRetentionWorker removes expired billing facts in small, cancelable batches.
type LogRetentionWorker struct {
	mu                sync.Mutex
	db                *gorm.DB
	findRetentionDays BillingLogRetentionDaysFinder
	logger            *zap.Logger
	now               func() time.Time
	interval          time.Duration
	batchLimit        int
	batchPause        time.Duration

	cancel  context.CancelCauseFunc
	started bool
	closing bool
	done    chan struct{}
	workers conc.WaitGroup
}

func NewLogRetentionWorker(db *gorm.DB, findRetentionDays BillingLogRetentionDaysFinder, logger *zap.Logger) *LogRetentionWorker {
	return &LogRetentionWorker{
		db: db, findRetentionDays: findRetentionDays, logger: logger,
		now:        time.Now,
		interval:   billingLogRetentionInterval,
		batchLimit: billingLogRetentionBatchSize,
		batchPause: billingLogRetentionBatchPause,
		done:       make(chan struct{}),
	}
}

// FindBillingLogRetentionDays reads the retention policy without hiding storage
// or validation failures. Only an absent row selects the documented default.
func FindBillingLogRetentionDays(ctx context.Context, db *gorm.DB) (int, error) {
	if ctx == nil {
		return 0, errors.New("find billing log retention days: nil context")
	}
	if db == nil {
		return 0, errors.New("find billing log retention days: nil database")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	var setting models.Setting
	result := db.WithContext(ctx).
		Where("key = ?", consts.SettingKeyBillingLogRetentionDays).
		Limit(1).
		Find(&setting)
	if result.Error != nil {
		return 0, fmt.Errorf("find billing log retention days: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return consts.DefaultBillingLogRetentionDays, nil
	}
	days, err := strconv.Atoi(setting.Value)
	if err != nil {
		return 0, fmt.Errorf("find billing log retention days: parse %q: %w", setting.Value, err)
	}
	if days < consts.MinimumBillingLogRetentionDays || days > consts.MaximumBillingLogRetentionDays {
		return 0, fmt.Errorf(
			"find billing log retention days: value %d must be between %d and %d",
			days,
			consts.MinimumBillingLogRetentionDays,
			consts.MaximumBillingLogRetentionDays,
		)
	}
	return days, nil
}

// Start runs one cleanup immediately, then repeats it once per interval.
func (w *LogRetentionWorker) Start(ctx context.Context) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.closing || ctx == nil {
		return
	}
	w.started = true
	workerCtx, cancel := context.WithCancelCause(ctx)
	w.cancel = cancel
	w.workers.Go(func() {
		defer close(w.done)
		w.run(workerCtx)
	})
}

func (w *LogRetentionWorker) run(ctx context.Context) {
	for {
		if err := w.RunOnce(ctx); err != nil {
			if context.Cause(ctx) != nil {
				return
			}
			w.logError("billing log retention failed", err)
		}
		if err := waitForRetentionTimer(ctx, w.interval); err != nil {
			return
		}
	}
}

// RunOnce removes eligible billing logs with a forward keyset scan. Backdated
// rows inserted behind the current (created_at, id) cursor remain for the next
// RunOnce, which starts from an empty cursor.
func (w *LogRetentionWorker) RunOnce(ctx context.Context) error {
	if ctx == nil {
		return errors.New("billing log retention: nil context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if w.findRetentionDays == nil {
		return errors.New("billing log retention: retention setting unavailable")
	}
	// behavior change: setting read/parse/range failures fail closed before the
	// first delete instead of silently selecting the five-day default.
	days, err := w.findRetentionDays(ctx)
	if err != nil {
		return fmt.Errorf("billing log retention setting: %w", err)
	}
	if days < consts.MinimumBillingLogRetentionDays || days > consts.MaximumBillingLogRetentionDays {
		return fmt.Errorf("billing log retention days must be between %d and %d", consts.MinimumBillingLogRetentionDays, consts.MaximumBillingLogRetentionDays)
	}
	cutoff := w.now().UTC().Add(-time.Duration(days) * 24 * time.Hour)
	cursor := dao.BillingLogDeleteCursor{}
	for {
		batch, err := dao.DeleteBillingLogsBeforeBatch(ctx, w.db, cutoff, cursor, w.batchLimit)
		if err != nil {
			return err
		}
		if !batch.HasMore {
			return nil
		}
		cursor = batch.Next
		if err := waitForRetentionTimer(ctx, w.batchPause); err != nil {
			return err
		}
	}
}

func waitForRetentionTimer(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func (w *LogRetentionWorker) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("billing log retention: nil close context")
	}
	w.mu.Lock()
	if !w.closing {
		w.closing = true
		if w.cancel != nil {
			w.cancel(errors.New("billing log retention: closing"))
		}
		if !w.started {
			close(w.done)
		}
	}
	done := w.done
	w.mu.Unlock()
	select {
	case <-done:
		w.workers.Wait()
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (w *LogRetentionWorker) Done() <-chan struct{} { return w.done }

func (w *LogRetentionWorker) ResourceCounts() app.ResourceCounts {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started {
		return app.ResourceCounts{}
	}
	select {
	case <-w.done:
		return app.ResourceCounts{}
	default:
		return app.ResourceCounts{LifecycleWorkers: 1, Timers: 1, Inflight: 1}
	}
}

func (w *LogRetentionWorker) logError(message string, err error) {
	if w.logger != nil {
		w.logger.Error(message, zap.Error(err))
	}
}
