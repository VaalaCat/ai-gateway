package historybackfill

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/sourcegraph/conc"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	defaultRetryInterval = 30 * time.Second
	maxFinalPasses       = 10_000
)

var errWorkerStopped = errors.New("history backfill worker stopped")

type WorkerOptions struct {
	Backfiller       *Backfiller
	RetryInterval    time.Duration
	OpenLegacyReader LegacyReaderOpener
	CloseLegacy      func() error
	Now              func() time.Time
}

type LegacyReaderOpener func() (*LegacyReader, func() error, error)

type commandKind uint8

const (
	commandComplete commandKind = iota + 1
	commandSkip
)

type workerCommand struct {
	kind commandKind
	ctx  context.Context
	done chan error
}

type Worker struct {
	backfiller       *Backfiller
	retryInterval    time.Duration
	openLegacyReader LegacyReaderOpener
	closeLegacy      func() error
	nowTime          func() time.Time

	mu                 sync.Mutex
	started            bool
	stopping           bool
	terminal           bool
	batchRunning       bool
	rowsPerSecond      float64
	runtimeLastError   string
	runtimeDegraded    bool
	finalPassConfirmed bool
	legacyClosePending bool
	legacyClosed       bool
	done               chan struct{}
	stop               chan struct{}
	wake               chan struct{}
	commands           chan workerCommand
	stopOnce           sync.Once
	workers            conc.WaitGroup
}

func NewWorker(options WorkerOptions) *Worker {
	if options.RetryInterval <= 0 {
		options.RetryInterval = defaultRetryInterval
	}
	worker := &Worker{
		backfiller: options.Backfiller, retryInterval: options.RetryInterval,
		openLegacyReader: options.OpenLegacyReader, closeLegacy: options.CloseLegacy, nowTime: options.Now,
		done: make(chan struct{}), stop: make(chan struct{}), wake: make(chan struct{}, 1),
		commands: make(chan workerCommand, 1),
	}
	return worker
}

func (w *Worker) Start(parent context.Context) error {
	if parent == nil {
		return fmt.Errorf("history backfill worker requires a context")
	}
	if err := context.Cause(parent); err != nil {
		return err
	}
	if w == nil || w.backfiller == nil {
		return fmt.Errorf("history backfill worker requires a backfiller")
	}
	w.mu.Lock()
	if w.started {
		w.mu.Unlock()
		return fmt.Errorf("history backfill worker already started")
	}
	w.started = true
	w.mu.Unlock()
	w.workers.Go(w.run)
	w.workers.Go(func() {
		select {
		case <-parent.Done():
			w.requestStop()
		case <-w.done:
		}
	})
	return nil
}

func (w *Worker) run() {
	defer close(w.done)
	for {
		if w.shouldStop() || w.isTerminal() || w.persistedTerminalState() {
			return
		}
		if w.isLegacyClosed() {
			if !w.waitForWork() {
				return
			}
			continue
		}
		if err := w.ensureLegacyReader(); err != nil {
			if !w.waitForWork() {
				return
			}
			continue
		}
		if w.shouldStop() {
			return
		}
		select {
		case command := <-w.commands:
			command.done <- w.handleCommand(command)
			close(command.done)
			continue
		default:
		}

		result, err := w.executePass(context.Background())
		if err == nil && result.CopiedRows > 0 {
			continue
		}
		if !w.waitForWork() {
			return
		}
	}
}

func (w *Worker) ensureLegacyReader() error {
	if w.backfiller.options.Reader != nil {
		return nil
	}
	if w.openLegacyReader == nil {
		return w.recordReaderOpenFailure(fmt.Errorf("legacy history reader opener is unavailable"))
	}
	reader, closeLegacy, err := w.openLegacyReader()
	if err != nil {
		return w.recordReaderOpenFailure(fmt.Errorf("open legacy history reader: %w", err))
	}
	if reader == nil || closeLegacy == nil {
		if closeLegacy != nil {
			_ = closeLegacy()
		}
		return w.recordReaderOpenFailure(fmt.Errorf("open legacy history reader: incomplete reader ownership"))
	}
	w.backfiller.options.Reader = reader
	w.closeLegacy = closeLegacy
	return nil
}

func (w *Worker) recordReaderOpenFailure(err error) error {
	persistErr := w.updateMigration(context.Background(), map[string]any{
		"state": string(StateDegraded), "last_error": err.Error(),
	})
	if persistErr != nil {
		err = fmt.Errorf("%w; persist degraded history state: %v", err, persistErr)
	}
	w.setRuntimeFailure(err)
	return err
}

func (w *Worker) persistedTerminalState() bool {
	state, err := w.migrationState()
	return err == nil && (state == StateCompleted || state == StateSourceDeleted)
}

func (w *Worker) migrationState() (State, error) {
	core, err := findHistoryDB(w.backfiller.options.CoreDBFinder, "core")
	if err != nil {
		return StateDegraded, err
	}
	var migration models.HistoryMigration
	if err := core.Select("state").First(&migration, models.HistoryMigrationSingletonID).Error; err != nil {
		return StateDegraded, fmt.Errorf("read history migration state: %w", err)
	}
	return State(migration.State), nil
}

func (w *Worker) waitForWork() bool {
	timer := time.NewTimer(w.retryInterval)
	defer timer.Stop()
	select {
	case <-w.stop:
		return false
	case command := <-w.commands:
		command.done <- w.handleCommand(command)
		close(command.done)
		return true
	case <-w.wake:
		return true
	case <-timer.C:
		return true
	}
}

func (w *Worker) handleCommand(command workerCommand) error {
	if err := context.Cause(command.ctx); err != nil {
		return err
	}
	switch command.kind {
	case commandComplete:
		return w.complete(command.ctx)
	case commandSkip:
		return w.skipRemaining(command.ctx)
	default:
		return fmt.Errorf("unknown history backfill command %d", command.kind)
	}
}

func (w *Worker) executePass(ctx context.Context) (PassResult, error) {
	if err := w.updateMigration(ctx, map[string]any{"state": string(StateCopying)}); err != nil {
		w.setRuntimeFailure(err)
		return PassResult{}, err
	}
	started := time.Now()
	w.setBatchRunning(true)
	result, err := w.backfiller.RunPass(ctx)
	w.setBatchRunning(false)
	if err != nil {
		persistErr := w.updateMigration(context.WithoutCancel(ctx), map[string]any{
			"state": string(StateDegraded), "last_error": err.Error(),
		})
		if persistErr != nil {
			err = fmt.Errorf("%w; persist degraded history state: %v", err, persistErr)
		}
		w.setRuntimeFailure(err)
		return result, err
	}
	now := w.now()
	state := StateCopying
	if result.CaughtUp {
		state = StateCaughtUp
	}
	if err := w.updateMigration(ctx, map[string]any{
		"state": string(state), "last_error": "", "last_successful_at_unix": now.Unix(),
	}); err != nil {
		w.setRuntimeFailure(err)
		return result, err
	}
	w.mu.Lock()
	seconds := time.Since(started).Seconds()
	if result.CopiedRows > 0 && seconds > 0 {
		w.rowsPerSecond = finiteRate(float64(result.CopiedRows) / seconds)
	}
	w.mu.Unlock()
	w.clearRuntimeFailure()
	return result, nil
}

func (w *Worker) RetryNow() {
	if w == nil {
		return
	}
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

func (w *Worker) Complete(ctx context.Context) error {
	if err := validateCommandContext(ctx, "complete"); err != nil {
		return err
	}
	state, err := w.migrationState()
	if err == nil && (state == StateCompleted || state == StateSourceDeleted) {
		return nil
	}
	return w.sendCommand(workerCommand{kind: commandComplete, ctx: ctx, done: make(chan error, 1)})
}

func (w *Worker) complete(ctx context.Context) error {
	w.mu.Lock()
	legacyClosed := w.legacyClosed
	finalPassConfirmed := w.finalPassConfirmed
	w.mu.Unlock()
	if legacyClosed {
		return w.persistCompleted(ctx)
	}
	if finalPassConfirmed {
		return w.closeLegacyAndComplete(ctx)
	}
	for pass := 0; pass < maxFinalPasses; pass++ {
		result, err := w.executePass(ctx)
		if err != nil {
			return w.describeIncomplete(ctx, err)
		}
		if result.CaughtUp {
			if err := w.requireAllStagesCaughtUp(ctx); err != nil {
				return err
			}
			w.mu.Lock()
			w.finalPassConfirmed = true
			w.legacyClosePending = true
			w.mu.Unlock()
			return w.closeLegacyAndComplete(ctx)
		}
	}
	return fmt.Errorf("history backfill final pass limit reached")
}

func (w *Worker) closeLegacyAndComplete(ctx context.Context) error {
	if err := w.closeLegacyReader(); err != nil {
		w.setRuntimeFailure(err)
		persistErr := w.updateMigration(context.WithoutCancel(ctx), map[string]any{
			"state": string(StateDegraded), "last_error": err.Error(),
		})
		if persistErr != nil {
			return fmt.Errorf("%w; persist degraded history state: %v", err, persistErr)
		}
		return err
	}
	return w.persistCompleted(ctx)
}

func (w *Worker) persistCompleted(ctx context.Context) error {
	now := w.now().Unix()
	if err := w.updateMigration(ctx, map[string]any{
		"state": string(StateCompleted), "completed_at_unix": now,
		"last_successful_at_unix": now, "last_error": "",
	}); err != nil {
		w.setRuntimeFailure(err)
		return err
	}
	w.mu.Lock()
	w.terminal = true
	w.runtimeDegraded = false
	w.runtimeLastError = ""
	w.mu.Unlock()
	return nil
}

func (w *Worker) describeIncomplete(ctx context.Context, passErr error) error {
	billingCaughtUp, err := w.billingCaughtUp(ctx)
	if err != nil || !billingCaughtUp {
		return fmt.Errorf("billing is not caught up: %w", passErr)
	}
	return fmt.Errorf("log history is not caught up: %w", passErr)
}

func (w *Worker) SkipRemaining(ctx context.Context) error {
	if err := validateCommandContext(ctx, "skip history backfill"); err != nil {
		return err
	}
	return w.sendCommand(workerCommand{kind: commandSkip, ctx: ctx, done: make(chan error, 1)})
}

func (w *Worker) skipRemaining(ctx context.Context) error {
	logDB, err := findHistoryDB(w.backfiller.options.LogDBFinder, "log")
	if err != nil {
		return err
	}
	if err := logDB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, key := range []string{requestCursorKey, traceCursorKey} {
			if err := markCursorSkipped(tx, key, w.now().Unix()); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("skip log history: %w", err)
	}
	return w.updateMigration(ctx, map[string]any{"skip_log_history": true})
}

func markCursorSkipped(tx *gorm.DB, key string, now int64) error {
	var cursor models.HistoryCursor
	err := tx.Where("key = ?", key).First(&cursor).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("read history cursor %q: %w", key, err)
	}
	cursor.Key = key
	cursor.Skipped = true
	cursor.UpdatedAtUnix = now
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"last_source_id", "processed_rows", "skipped", "updated_at_unix"}),
	}).Create(&cursor).Error
}

func (w *Worker) sendCommand(command workerCommand) error {
	w.mu.Lock()
	started, stopping := w.started, w.stopping
	w.mu.Unlock()
	if !started {
		return fmt.Errorf("history backfill worker is not started")
	}
	if stopping {
		if w.commandAlreadyCompleted(command) {
			return nil
		}
		return errWorkerStopped
	}
	select {
	case w.commands <- command:
	case <-w.done:
		if w.commandAlreadyCompleted(command) {
			return nil
		}
		return errWorkerStopped
	case <-command.ctx.Done():
		return context.Cause(command.ctx)
	}
	select {
	case err := <-command.done:
		return err
	case <-w.done:
		select {
		case err := <-command.done:
			return err
		default:
			if w.commandAlreadyCompleted(command) {
				return nil
			}
			return errWorkerStopped
		}
	case <-command.ctx.Done():
		return context.Cause(command.ctx)
	}
}

func (w *Worker) commandAlreadyCompleted(command workerCommand) bool {
	if command.kind != commandComplete {
		return false
	}
	state, err := w.migrationState()
	return err == nil && (state == StateCompleted || state == StateSourceDeleted)
}

func validateCommandContext(ctx context.Context, operation string) error {
	if ctx == nil {
		return fmt.Errorf("%s context is nil", operation)
	}
	return context.Cause(ctx)
}

func (w *Worker) billingCaughtUp(ctx context.Context) (bool, error) {
	core, err := findHistoryDB(w.backfiller.options.CoreDBFinder, "core")
	if err != nil {
		return false, err
	}
	cursor, err := readCursorStatus(core, billingCursorKey)
	if err != nil {
		return false, err
	}
	batch, err := w.backfiller.options.Reader.ReadBilling(ctx, cursor.LastSourceID, 1)
	return len(batch.Rows) == 0, err
}

func (w *Worker) requireAllStagesCaughtUp(ctx context.Context) error {
	billing, err := w.billingCaughtUp(ctx)
	if err != nil || !billing {
		return fmt.Errorf("billing is not caught up: %w", err)
	}
	if !w.backfiller.options.Reader.HasLogHistory() {
		return nil
	}
	logDB, err := findHistoryDB(w.backfiller.options.LogDBFinder, "log")
	if err != nil {
		return err
	}
	checks := []struct {
		key  string
		read func(context.Context, uint, int) (int, error)
	}{
		{requestCursorKey, func(ctx context.Context, after uint, limit int) (int, error) {
			batch, err := w.backfiller.options.Reader.ReadRequests(ctx, after, limit)
			return len(batch.Rows), err
		}},
		{traceCursorKey, func(ctx context.Context, after uint, limit int) (int, error) {
			batch, err := w.backfiller.options.Reader.ReadTraces(ctx, after, limit)
			return len(batch.Rows), err
		}},
	}
	for _, check := range checks {
		cursor, err := readCursorStatus(logDB, check.key)
		if err != nil {
			return err
		}
		if cursor.Skipped {
			continue
		}
		rows, err := check.read(ctx, cursor.LastSourceID, 1)
		if err != nil {
			return err
		}
		if rows != 0 {
			return fmt.Errorf("log history %s is not caught up", check.key)
		}
	}
	return nil
}

func (w *Worker) closeLegacyReader() error {
	w.mu.Lock()
	if w.legacyClosed {
		w.mu.Unlock()
		return nil
	}
	w.mu.Unlock()
	if w.closeLegacy != nil {
		if err := w.closeLegacy(); err != nil {
			return fmt.Errorf("close legacy history database: %w", err)
		}
	}
	w.mu.Lock()
	w.legacyClosePending = false
	w.legacyClosed = true
	w.mu.Unlock()
	return nil
}

func (w *Worker) setRuntimeFailure(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	w.runtimeDegraded = true
	w.runtimeLastError = err.Error()
	w.mu.Unlock()
}

func (w *Worker) clearRuntimeFailure() {
	w.mu.Lock()
	w.runtimeDegraded = false
	w.runtimeLastError = ""
	w.mu.Unlock()
}

func (w *Worker) Stop(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("stop history backfill context is nil")
	}
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started {
		return w.closeLegacyReader()
	}
	w.requestStop()
	select {
	case <-w.done:
		w.workers.Wait()
		return w.closeLegacyReader()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (w *Worker) requestStop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.stopping = true
	w.mu.Unlock()
	w.stopOnce.Do(func() { close(w.stop) })
}

func (w *Worker) shouldStop() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stopping
}

func (w *Worker) isTerminal() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.terminal
}

func (w *Worker) isLegacyClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.finalPassConfirmed || w.legacyClosePending || w.legacyClosed
}

func (w *Worker) setBatchRunning(running bool) {
	w.mu.Lock()
	w.batchRunning = running
	w.mu.Unlock()
}
