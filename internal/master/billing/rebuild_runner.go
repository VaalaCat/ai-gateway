package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/google/uuid"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

// JobStatus is the lifecycle state of a RebuildJob.
type JobStatus string

const (
	JobStatusRunning   JobStatus = "running"
	JobStatusSucceeded JobStatus = "succeeded"
	JobStatusFailed    JobStatus = "failed"
	JobStatusCanceled  JobStatus = "canceled"
)

// RebuildJob is an in-memory record of one async rebuild request.
// Terminal jobs (succeeded/failed/canceled) are GC'd after retain duration.
type RebuildJob struct {
	ID           string
	Filter       dao.BillingRebuildFilter
	Status       JobStatus
	DoneSlices   int64
	TotalSlices  int64
	ReplayedLogs int64
	StartedAt    int64
	FinishedAt   int64
	Error        string

	mu     sync.Mutex
	cancel context.CancelFunc
}

// Snapshot returns a lock-free copy of the job's mutable fields. Callers
// outside RebuildRunner must use this rather than reading j.Status directly
// (race-detector flags direct reads, since run() updates Status under j.mu
// and counters via sync/atomic).
func (j *RebuildJob) Snapshot() RebuildJob {
	j.mu.Lock()
	defer j.mu.Unlock()
	return RebuildJob{
		ID:           j.ID,
		Filter:       j.Filter,
		Status:       j.Status,
		DoneSlices:   atomic.LoadInt64(&j.DoneSlices),
		TotalSlices:  j.TotalSlices,
		ReplayedLogs: atomic.LoadInt64(&j.ReplayedLogs),
		StartedAt:    j.StartedAt,
		FinishedAt:   j.FinishedAt,
		Error:        j.Error,
	}
}

// SliceFn is the per-slice dao call. Injectable for tests and used by the
// transitional legacy production path before strict split activation.
type SliceFn func(date string, hour int, targets []string, resetDailyForDate bool) (*dao.BillingRebuildResult, error)
type SliceContextFn func(context.Context, string, int, []string, bool) (*dao.BillingRebuildResult, error)

type CoreRebuildAdmission interface {
	RunCoreDateRebuild(context.Context, string, bool, func() error, func(maxBillingLogID uint) error) error
	RunCoreHourRebuildSlice(context.Context, string, int, func(maxBillingLogID uint) error) error
}

// RebuildRunner schedules per-(date,hour) rebuild calls in the background.
// Status lives in memory only — master restart drops all jobs (clients re-poll
// → 404 → retrigger). Terminal jobs are retained for `retain` duration
// before gc removes them.
type RebuildRunner struct {
	mu     sync.RWMutex
	jobs   map[string]*RebuildJob
	app    dao.AppProvider
	logger *zap.Logger
	retain time.Duration

	sliceFn       SliceContextFn
	coreAdmission CoreRebuildAdmission
	sliceSleep    time.Duration
	stopCh        chan struct{}
	closeOnce     sync.Once
	rootCtx       context.Context
	cancel        context.CancelCauseFunc
	started       bool
	closing       bool
	workers       conc.WaitGroup
	done          chan struct{}
	activeWorkers atomic.Int64
	activeTimers  atomic.Int64
	inflight      atomic.Int64
}

// NewRebuildRunner constructs the runner and spawns a gc goroutine for
// terminal-job cleanup. app may be nil for pure-memory tests (in which case
// SetSliceFn must be called before any Submit that should actually persist).
// retain controls how long terminal jobs stay visible via Get/List.
func NewRebuildRunner(app dao.AppProvider, logger *zap.Logger, retain time.Duration) *RebuildRunner {
	return &RebuildRunner{
		jobs:   make(map[string]*RebuildJob),
		app:    app,
		logger: logger,
		retain: retain,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
}

// SetSliceFn overrides the per-slice executor. Used by tests; production
// passes nil and lets run() fall back to dao.RebuildHourSlice (see T3.4).
func (r *RebuildRunner) SetSliceFn(fn SliceFn) {
	if fn == nil {
		r.SetSliceContextFn(nil)
		return
	}
	r.SetSliceContextFn(func(_ context.Context, date string, hour int, targets []string, reset bool) (*dao.BillingRebuildResult, error) {
		return fn(date, hour, targets, reset)
	})
}

func (r *RebuildRunner) SetSliceContextFn(fn SliceContextFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sliceFn = fn
}

func (r *RebuildRunner) SetCoreRebuildAdmission(admission CoreRebuildAdmission) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.coreAdmission = admission
}

// SetSliceSleep configures an inter-slice pause applied after each successful
// (date,hour) slice inside run(). Zero (the default) disables sleeping, so
// existing tests that don't call this keep running at full speed. Production
// wiring sets this from the admin-configurable rebuild_slice_sleep_ms setting
// so background rebuild replay trickles rather than competing with peak DB
// I/O. The sleep is implemented as a ctx-cancelable select in run(), not a
// bare time.Sleep, so job cancellation still takes effect promptly mid-sleep.
func (r *RebuildRunner) SetSliceSleep(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sliceSleep = d
}

func (r *RebuildRunner) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.closing || ctx == nil {
		return
	}
	r.started = true
	r.rootCtx, r.cancel = context.WithCancelCause(ctx)
	r.activeWorkers.Add(1)
	r.activeTimers.Add(1)
	r.workers.Go(func() {
		defer r.activeWorkers.Add(-1)
		defer r.activeTimers.Add(-1)
		r.gcLoop()
	})
}

// Submit registers a new job and starts its goroutine. Returns immediately
// with the job's initial state (Status=running, TotalSlices set).
// Validates date range; returns error for empty / inverted / unparseable.
func (r *RebuildRunner) Submit(filter dao.BillingRebuildFilter) (*RebuildJob, error) {
	if filter.StartDate == "" && filter.EndDate == "" {
		return nil, fmt.Errorf("at least one of start_date or end_date is required")
	}
	startDate, endDate, err := runnerNormalizeDateRange(filter.StartDate, filter.EndDate)
	if err != nil {
		return nil, err
	}
	filter.StartDate = startDate
	filter.EndDate = endDate
	days, err := enumerateDays(startDate, endDate)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.closing || !r.started {
		r.mu.Unlock()
		return nil, fmt.Errorf("rebuild runner not accepting jobs")
	}
	ctx, cancel := context.WithCancel(r.rootCtx)
	job := &RebuildJob{
		ID:          uuid.NewString(),
		Filter:      filter,
		Status:      JobStatusRunning,
		TotalSlices: int64(len(days) * 24),
		StartedAt:   time.Now().Unix(),
		cancel:      cancel,
	}
	r.jobs[job.ID] = job
	r.activeWorkers.Add(1)
	r.workers.Go(func() {
		defer r.activeWorkers.Add(-1)
		r.run(ctx, job, days)
	})
	r.mu.Unlock()
	return job, nil
}

// Get returns the in-memory job by ID; ok=false if unknown or gc'd.
func (r *RebuildRunner) Get(id string) (*RebuildJob, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	j, ok := r.jobs[id]
	return j, ok
}

// List returns a snapshot of all currently-tracked jobs (running + retained terminals).
func (r *RebuildRunner) List() []*RebuildJob {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*RebuildJob, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	return out
}

// runnerNormalizeDateRange normalizes single-ended ranges and validates ordering.
// Empty start defaults to end; empty end defaults to start; start > end is an error.
// Both endpoints must parse as YYYY-MM-DD.
func runnerNormalizeDateRange(start, end string) (string, string, error) {
	if start == "" {
		start = end
	}
	if end == "" {
		end = start
	}
	if _, err := time.Parse("2006-01-02", start); err != nil {
		return "", "", fmt.Errorf("parse start_date %q: %w", start, err)
	}
	if _, err := time.Parse("2006-01-02", end); err != nil {
		return "", "", fmt.Errorf("parse end_date %q: %w", end, err)
	}
	if start > end {
		return "", "", fmt.Errorf("start_date > end_date")
	}
	return start, end, nil
}

// enumerateDays returns YYYY-MM-DD strings for [start, end] inclusive.
func enumerateDays(start, end string) ([]string, error) {
	s, err := time.Parse("2006-01-02", start)
	if err != nil {
		return nil, fmt.Errorf("parse start: %w", err)
	}
	e, err := time.Parse("2006-01-02", end)
	if err != nil {
		return nil, fmt.Errorf("parse end: %w", err)
	}
	out := []string{}
	for d := s; !d.After(e); d = d.AddDate(0, 0, 1) {
		out = append(out, d.Format("2006-01-02"))
	}
	return out, nil
}

// run is the per-job worker goroutine. The legacy injected path schedules 24
// per-hour calls and resets daily rows at hour zero. The core path serializes
// each complete date, rebuilds selected hourly slices, then atomically replaces
// the selected daily projection group. Any error fails the whole job; context
// cancellation marks it canceled.
func (r *RebuildRunner) run(ctx context.Context, job *RebuildJob, days []string) {
	r.mu.RLock()
	fn := r.sliceFn
	admission := r.coreAdmission
	sliceSleep := r.sliceSleep
	r.mu.RUnlock()

	var err error
	if fn != nil {
		err = r.runSlices(ctx, job, days, sliceSleep, fn)
	} else if r.app == nil {
		err = errors.New("no slice fn and no app provider")
	} else if admission == nil {
		err = errors.New("core rebuild admission is not configured")
	} else {
		var daily, hourly bool
		daily, hourly, err = coreRebuildSelection(job.Filter.Targets)
		if err == nil {
			err = r.runCoreDates(ctx, job, days, sliceSleep, admission, daily, hourly)
		}
	}
	r.finishJob(ctx, job, err)
}

func (r *RebuildRunner) runCoreDates(
	ctx context.Context,
	job *RebuildJob,
	days []string,
	sliceSleep time.Duration,
	admission CoreRebuildAdmission,
	rebuildDaily bool,
	rebuildHourly bool,
) error {
	for _, date := range days {
		rebuildDate := date
		var dailyResult *dao.BillingRebuildResult
		err := admission.RunCoreDateRebuild(ctx, rebuildDate, rebuildDaily, func() error {
			if !rebuildHourly {
				return nil
			}
			return r.runCoreHoursForDate(ctx, job, rebuildDate, sliceSleep, admission)
		}, func(watermark uint) error {
			r.inflight.Add(1)
			var err error
			dailyResult, err = dao.NewAdminMutation(dao.NewContextWithContext(r.app, ctx)).Billing().
				RebuildCoreDailyForDateThroughID(ctx, rebuildDate, &watermark)
			r.inflight.Add(-1)
			return err
		})
		if err != nil {
			return err
		}
		if !rebuildHourly {
			atomic.AddInt64(&job.DoneSlices, 24)
			if dailyResult != nil {
				atomic.AddInt64(&job.ReplayedLogs, dailyResult.ReplayedLogs)
			}
		}
	}
	return nil
}

func coreRebuildSelection(targets []string) (daily, hourly bool, err error) {
	if len(targets) == 0 {
		return true, true, nil
	}
	for _, target := range targets {
		switch target {
		case dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily:
			daily = true
		case dao.RebuildTargetHourlyBucket:
			hourly = true
		case dao.RebuildTargetDurationHistogram, dao.RebuildTargetTTFTHistogram, dao.RebuildTargetTPSHistogram:
		default:
			return false, false, fmt.Errorf("%w: %q", dao.ErrInvalidRebuildTarget, target)
		}
	}
	return daily, hourly, nil
}

func (r *RebuildRunner) runCoreHoursForDate(
	ctx context.Context,
	job *RebuildJob,
	date string,
	sliceSleep time.Duration,
	admission CoreRebuildAdmission,
) error {
	for hour := 0; hour < 24; hour++ {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		result, err := r.runCoreHour(ctx, date, hour, job.Filter.Targets, admission)
		if err != nil {
			r.logSliceError(job.ID, date, hour, err)
			return err
		}
		if result == nil {
			return errors.New("rebuild slice returned nil result")
		}
		atomic.AddInt64(&job.DoneSlices, 1)
		atomic.AddInt64(&job.ReplayedLogs, result.ReplayedLogs)
		if err := waitForNextSlice(ctx, sliceSleep); err != nil {
			return err
		}
	}
	return nil
}

func (r *RebuildRunner) runCoreHour(
	ctx context.Context,
	date string,
	hour int,
	targets []string,
	admission CoreRebuildAdmission,
) (*dao.BillingRebuildResult, error) {
	var result *dao.BillingRebuildResult
	err := admission.RunCoreHourRebuildSlice(ctx, date, hour, func(watermark uint) error {
		r.inflight.Add(1)
		var rebuildErr error
		result, rebuildErr = dao.NewAdminMutation(dao.NewContextWithContext(r.app, ctx)).Billing().
			RebuildCoreHourSliceThroughID(ctx, date, hour, targets, &watermark)
		r.inflight.Add(-1)
		return rebuildErr
	})
	return result, err
}

func (r *RebuildRunner) logSliceError(jobID, date string, hour int, err error) {
	if r.logger == nil {
		return
	}
	r.logger.Error("rebuild_slice_failed",
		zap.String("job_id", jobID),
		zap.String("date", date),
		zap.Int("hour", hour),
		zap.Error(err))
}

func (r *RebuildRunner) runSlices(
	ctx context.Context,
	job *RebuildJob,
	days []string,
	sliceSleep time.Duration,
	fn SliceContextFn,
) error {
	for _, date := range days {
		for hour := 0; hour < 24; hour++ {
			if err := context.Cause(ctx); err != nil {
				return err
			}
			r.inflight.Add(1)
			result, err := fn(ctx, date, hour, job.Filter.Targets, hour == 0)
			r.inflight.Add(-1)
			if err != nil {
				r.logSliceError(job.ID, date, hour, err)
				return err
			}
			if result == nil {
				return errors.New("rebuild slice returned nil result")
			}
			atomic.AddInt64(&job.DoneSlices, 1)
			atomic.AddInt64(&job.ReplayedLogs, result.ReplayedLogs)
			if err := waitForNextSlice(ctx, sliceSleep); err != nil {
				return err
			}
		}
	}
	return nil
}

func waitForNextSlice(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return context.Cause(ctx)
	case <-timer.C:
		return nil
	}
}

func (r *RebuildRunner) finishJob(ctx context.Context, job *RebuildJob, err error) {
	job.mu.Lock()
	defer job.mu.Unlock()
	if job.Status != JobStatusRunning {
		return
	}
	job.FinishedAt = time.Now().Unix()
	if context.Cause(ctx) != nil {
		job.Status = JobStatusCanceled
		return
	}
	if err != nil {
		job.Status = JobStatusFailed
		job.Error = err.Error()
		return
	}
	job.Status = JobStatusSucceeded
}

func (r *RebuildRunner) gcLoop() {
	tickEvery := r.retain / 2
	if tickEvery <= 0 {
		tickEvery = time.Minute
	}
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			r.gc()
		case <-r.rootCtx.Done():
			return
		}
	}
}

func (r *RebuildRunner) gc() {
	now := time.Now().Unix()
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, j := range r.jobs {
		j.mu.Lock()
		status := j.Status
		finishedAt := j.FinishedAt
		j.mu.Unlock()
		if status == JobStatusRunning {
			continue
		}
		if finishedAt > 0 && now-finishedAt > int64(r.retain.Seconds()) {
			delete(r.jobs, id)
		}
	}
}

// Stop signals the runner to exit, cancels every in-flight job's context,
// and stops the gc loop. Idempotent + concurrent-safe via sync.Once.
// Returns after issuing cancel signals; in-flight goroutines mark themselves
// canceled at their next ctx check.
func (r *RebuildRunner) Close(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("rebuild runner: nil close context")
	}
	r.closeOnce.Do(func() {
		r.mu.Lock()
		r.closing = true
		close(r.stopCh)
		if r.cancel != nil {
			r.cancel(context.Cause(ctx))
		}
		for _, j := range r.jobs {
			j.mu.Lock()
			if j.Status == JobStatusRunning && j.cancel != nil {
				j.cancel()
			}
			j.mu.Unlock()
		}
		r.mu.Unlock()
		go func() {
			r.workers.Wait()
			close(r.done)
		}()
	})
	select {
	case <-r.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (r *RebuildRunner) Stop(ctx context.Context) error { return r.Close(ctx) }

func (r *RebuildRunner) Done() <-chan struct{} { return r.done }

func (r *RebuildRunner) ResourceCounts() app.ResourceCounts {
	return app.ResourceCounts{LifecycleWorkers: r.activeWorkers.Load(), Timers: r.activeTimers.Load(), Inflight: r.inflight.Load()}
}
