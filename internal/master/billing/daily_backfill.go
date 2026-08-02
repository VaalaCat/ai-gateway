package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type DailyBillingBackfill struct {
	mu          sync.Mutex
	app         dao.AppProvider
	runner      *RebuildRunner
	logger      *zap.Logger
	rebuilder   dao.LogDailyBillingRebuilder
	onCompleted func(context.Context) error

	ctx     context.Context
	cancel  context.CancelCauseFunc
	started bool
	closing bool
	done    chan struct{}
	workers conc.WaitGroup
}

func NewDailyBillingBackfill(
	app dao.AppProvider,
	runner *RebuildRunner,
	logger *zap.Logger,
	onCompleted func(context.Context) error,
) *DailyBillingBackfill {
	return &DailyBillingBackfill{
		app: app, runner: runner, logger: logger,
		rebuilder:   dao.NewLogDailyBillingRebuilder(app),
		onCompleted: onCompleted,
		done:        make(chan struct{}),
	}
}

func (c *DailyBillingBackfill) SetRebuilder(rebuilder dao.LogDailyBillingRebuilder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	c.rebuilder = rebuilder
	if c.runner != nil {
		c.runner.SetDailyRebuilder(rebuilder)
	}
}

func (c *DailyBillingBackfill) Start(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.closing || ctx == nil {
		return
	}
	c.started = true
	c.ctx, c.cancel = context.WithCancelCause(ctx)
	c.workers.Go(func() {
		defer close(c.done)
		if err := c.prepareAndSubmit(c.ctx); err != nil && context.Cause(c.ctx) == nil {
			c.logError("daily_billing_backfill_start_failed", err)
		}
	})
}

func (c *DailyBillingBackfill) prepareAndSubmit(ctx context.Context) error {
	marker, found, err := findDailyBackfillMarker(ctx, c.app, models.DailyBillingBackfillVersion)
	if err != nil {
		return err
	}
	if found && marker.State == models.DailyBillingBackfillCompleted {
		return nil
	}

	if !found || marker.StartDate == "" || marker.EndDate == "" {
		bounds, boundsErr := c.rebuilder.FindRequestLogDateBounds(ctx)
		if boundsErr != nil {
			return c.recordStartupFailure(ctx, marker, boundsErr)
		}
		if bounds.Empty {
			if err := createOrCompleteEmptyBackfill(ctx, c.app, models.DailyBillingBackfillVersion); err != nil {
				return err
			}
			return c.finishCompletedBackfill(ctx)
		}
		marker.Version = models.DailyBillingBackfillVersion
		marker.StartDate = bounds.StartDate
		marker.EndDate = bounds.EndDate
	}

	startDate := marker.StartDate
	if marker.LastCompletedDate != "" {
		next, ok, nextErr := c.rebuilder.FindNextRequestLogDate(ctx, marker.LastCompletedDate, marker.EndDate)
		if nextErr != nil {
			return c.recordStartupFailure(ctx, marker, nextErr)
		}
		if !ok {
			if err := updateDailyBackfillCompleted(ctx, c.app, models.DailyBillingBackfillVersion); err != nil {
				return err
			}
			return c.finishCompletedBackfill(ctx)
		}
		startDate = next
	}
	if err := savePendingDailyBackfill(ctx, c.app, marker); err != nil {
		return err
	}
	if c.runner == nil {
		return c.recordStartupFailure(ctx, marker, errors.New("daily billing backfill runner is nil"))
	}
	_, err = c.runner.submitAutomaticDaily(dao.BillingRebuildFilter{
		StartDate: startDate,
		EndDate:   marker.EndDate,
		Targets:   []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily},
	}, models.DailyBillingBackfillVersion, c.finishCompletedBackfill)
	if err != nil {
		return c.recordStartupFailure(ctx, marker, err)
	}
	return nil
}

func (c *DailyBillingBackfill) finishCompletedBackfill(ctx context.Context) error {
	if c.onCompleted == nil {
		return nil
	}
	if err := c.onCompleted(ctx); err != nil {
		return fmt.Errorf("run daily billing backfill completion callback: %w", err)
	}
	return nil
}

func (c *DailyBillingBackfill) recordStartupFailure(ctx context.Context, marker models.DailyBillingBackfill, cause error) error {
	if context.Cause(ctx) != nil {
		return cause
	}
	marker.Version = models.DailyBillingBackfillVersion
	if err := saveFailedDailyBackfill(ctx, c.app, marker, cause); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (c *DailyBillingBackfill) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("daily billing backfill: nil close context")
	}
	c.mu.Lock()
	if !c.closing {
		c.closing = true
		if c.cancel != nil {
			c.cancel(context.Cause(ctx))
		}
		if !c.started {
			close(c.done)
		}
	}
	done := c.done
	c.mu.Unlock()
	select {
	case <-done:
		c.workers.Wait()
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (c *DailyBillingBackfill) Done() <-chan struct{} { return c.done }

func (c *DailyBillingBackfill) ResourceCounts() app.ResourceCounts {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.started {
		return app.ResourceCounts{}
	}
	select {
	case <-c.done:
		return app.ResourceCounts{}
	default:
		return app.ResourceCounts{LifecycleWorkers: 1, Inflight: 1}
	}
}

func (c *DailyBillingBackfill) logError(message string, err error) {
	if c.logger != nil {
		c.logger.Error(message, zap.Error(err))
	}
}

func dailyBackfillDB(ctx context.Context, application dao.AppProvider) (*gorm.DB, error) {
	if application == nil {
		return nil, fmt.Errorf("%w: daily billing backfill", dao.ErrLogDatabaseUnavailable)
	}
	db := application.GetLogDB()
	if db == nil || db.Error != nil {
		return nil, fmt.Errorf("%w: daily billing backfill", dao.ErrLogDatabaseUnavailable)
	}
	return db.WithContext(ctx), nil
}

func findDailyBackfillMarker(ctx context.Context, application dao.AppProvider, version uint) (models.DailyBillingBackfill, bool, error) {
	db, err := dailyBackfillDB(ctx, application)
	if err != nil {
		return models.DailyBillingBackfill{}, false, err
	}
	var marker models.DailyBillingBackfill
	err = db.First(&marker, "version = ?", version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.DailyBillingBackfill{}, false, nil
	}
	if err != nil {
		return models.DailyBillingBackfill{}, false, fmt.Errorf("find daily billing backfill marker: %w", err)
	}
	return marker, true, nil
}

func savePendingDailyBackfill(ctx context.Context, application dao.AppProvider, marker models.DailyBillingBackfill) error {
	db, err := dailyBackfillDB(ctx, application)
	if err != nil {
		return err
	}
	marker.State = models.DailyBillingBackfillPending
	marker.UpdatedAtUnix = time.Now().Unix()
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "version"}},
		DoUpdates: clause.Assignments(map[string]any{
			"state": models.DailyBillingBackfillPending, "start_date": marker.StartDate,
			"end_date": marker.EndDate, "last_completed_date": marker.LastCompletedDate,
			"updated_at_unix": marker.UpdatedAtUnix,
		}),
	}).Create(&marker).Error
}

func saveFailedDailyBackfill(ctx context.Context, application dao.AppProvider, marker models.DailyBillingBackfill, cause error) error {
	db, err := dailyBackfillDB(ctx, application)
	if err != nil {
		return err
	}
	marker.State = models.DailyBillingBackfillFailed
	marker.LastError = cause.Error()
	marker.UpdatedAtUnix = time.Now().Unix()
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "version"}},
		DoUpdates: clause.Assignments(map[string]any{
			"state": marker.State, "start_date": marker.StartDate, "end_date": marker.EndDate,
			"last_completed_date": marker.LastCompletedDate, "last_error": marker.LastError,
			"updated_at_unix": marker.UpdatedAtUnix,
		}),
	}).Create(&marker).Error
}

func createOrCompleteEmptyBackfill(ctx context.Context, application dao.AppProvider, version uint) error {
	db, err := dailyBackfillDB(ctx, application)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	marker := models.DailyBillingBackfill{
		Version: version, State: models.DailyBillingBackfillCompleted,
		CompletedAtUnix: now, UpdatedAtUnix: now,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "version"}},
		DoUpdates: clause.Assignments(map[string]any{
			"state": models.DailyBillingBackfillCompleted, "last_error": "",
			"completed_at_unix": now, "updated_at_unix": now,
		}),
	}).Create(&marker).Error
}

func updateDailyBackfillRunning(ctx context.Context, application dao.AppProvider, version uint) error {
	now := time.Now().Unix()
	return updateDailyBackfillMarker(ctx, application, version, map[string]any{
		"state": models.DailyBillingBackfillRunning, "last_error": "",
		"started_at_unix": now, "completed_at_unix": int64(0), "updated_at_unix": now,
	})
}

func updateDailyBackfillCompleted(ctx context.Context, application dao.AppProvider, version uint) error {
	now := time.Now().Unix()
	return updateDailyBackfillMarker(ctx, application, version, map[string]any{
		"state": models.DailyBillingBackfillCompleted, "last_error": "",
		"completed_at_unix": now, "updated_at_unix": now,
	})
}

func updateDailyBackfillFailed(ctx context.Context, application dao.AppProvider, version uint, cause error) error {
	return updateDailyBackfillMarker(ctx, application, version, map[string]any{
		"state": models.DailyBillingBackfillFailed, "last_error": cause.Error(),
		"updated_at_unix": time.Now().Unix(),
	})
}

func updateDailyBackfillMarker(ctx context.Context, application dao.AppProvider, version uint, values map[string]any) error {
	db, err := dailyBackfillDB(ctx, application)
	if err != nil {
		return err
	}
	result := db.Model(&models.DailyBillingBackfill{}).Where("version = ?", version).Updates(values)
	if result.Error != nil {
		return fmt.Errorf("update daily billing backfill marker: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("update daily billing backfill marker version %d: not found", version)
	}
	return nil
}
