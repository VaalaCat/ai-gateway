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

var ErrDailyBillingRebuildRunning = errors.New("daily billing rebuild already running")

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

	mu                     sync.Mutex
	cancel                 context.CancelFunc
	dailyBackfillVersion   uint
	dailyTargets           dao.DailyBillingRebuildTargets
	dailyDates             []string
	dailyRebuilder         dao.LogDailyBillingRebuilder
	dailyBackfillCompleted func(context.Context) error
}

// Snapshot returns a synchronized copy of the job's mutable fields. Callers
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

// RebuildRunner schedules log-owned daily billing rebuilds in the background.
// Status lives in memory only — master restart drops all jobs (clients re-poll
// → 404 → retrigger). Terminal jobs are retained for `retain` duration
// before gc removes them.
type RebuildRunner struct {
	mu     sync.RWMutex
	jobs   map[string]*RebuildJob
	app    dao.AppProvider
	logger *zap.Logger
	retain time.Duration

	dailyRebuilder dao.LogDailyBillingRebuilder
	sliceSleep     time.Duration
	stopCh         chan struct{}
	closeOnce      sync.Once
	rootCtx        context.Context
	cancel         context.CancelCauseFunc
	started        bool
	closing        bool
	workers        conc.WaitGroup
	done           chan struct{}
	activeWorkers  atomic.Int64
	activeTimers   atomic.Int64
	inflight       atomic.Int64
}

// NewRebuildRunner constructs the runner and spawns a gc goroutine for
// terminal-job cleanup. Tests with a nil app must inject a daily rebuilder.
// retain controls how long terminal jobs stay visible via Get/List.
func NewRebuildRunner(app dao.AppProvider, logger *zap.Logger, retain time.Duration) *RebuildRunner {
	runner := &RebuildRunner{
		jobs:   make(map[string]*RebuildJob),
		app:    app,
		logger: logger,
		retain: retain,
		stopCh: make(chan struct{}),
		done:   make(chan struct{}),
	}
	if app != nil {
		runner.dailyRebuilder = dao.NewLogDailyBillingRebuilder(app)
	}
	return runner
}

func (r *RebuildRunner) SetDailyRebuilder(rebuilder dao.LogDailyBillingRebuilder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dailyRebuilder = rebuilder
}

// SetSliceSleep configures an inter-slice pause applied after each successful
// date slice inside run(). Zero (the default) disables sleeping, so
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

// Submit discovers the actual request-log dates, fixes TotalSlices, then
// registers the job and starts its worker. It validates the date range and
// returns an error for empty, inverted, or unparseable input.
func (r *RebuildRunner) Submit(filter dao.BillingRebuildFilter) (*RebuildJob, error) {
	return r.submit(filter, 0, nil)
}

func (r *RebuildRunner) submitAutomaticDaily(
	filter dao.BillingRebuildFilter,
	version uint,
	onCompleted func(context.Context) error,
) (*RebuildJob, error) {
	if version == 0 {
		return nil, errors.New("automatic daily rebuild requires a marker version")
	}
	return r.submit(filter, version, onCompleted)
}

func (r *RebuildRunner) submit(
	filter dao.BillingRebuildFilter,
	dailyBackfillVersion uint,
	dailyBackfillCompleted func(context.Context) error,
) (*RebuildJob, error) {
	if filter.StartDate == "" && filter.EndDate == "" {
		return nil, fmt.Errorf("at least one of start_date or end_date is required")
	}
	startDate, endDate, err := runnerNormalizeDateRange(filter.StartDate, filter.EndDate)
	if err != nil {
		return nil, err
	}
	filter.StartDate = startDate
	filter.EndDate = endDate
	dailyTargets, err := dao.NormalizeDailyBillingRebuildTargets(filter.Targets)
	if err != nil {
		return nil, err
	}

	r.mu.RLock()
	if r.closing || !r.started {
		r.mu.RUnlock()
		return nil, fmt.Errorf("rebuild runner not accepting jobs")
	}
	if r.hasRunningDailyJobLocked() {
		r.mu.RUnlock()
		return nil, ErrDailyBillingRebuildRunning
	}
	rootCtx := r.rootCtx
	dailyRebuilder := r.dailyRebuilder
	r.mu.RUnlock()
	dailyDates, err := findDailyRebuildDates(rootCtx, dailyRebuilder, filter.StartDate, filter.EndDate)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	if r.closing || !r.started {
		r.mu.Unlock()
		return nil, fmt.Errorf("rebuild runner not accepting jobs")
	}
	if r.hasRunningDailyJobLocked() {
		r.mu.Unlock()
		return nil, ErrDailyBillingRebuildRunning
	}
	ctx, cancel := context.WithCancel(r.rootCtx)
	job := &RebuildJob{
		ID:                     uuid.NewString(),
		Filter:                 filter,
		Status:                 JobStatusRunning,
		TotalSlices:            int64(len(dailyDates)),
		StartedAt:              time.Now().Unix(),
		cancel:                 cancel,
		dailyBackfillVersion:   dailyBackfillVersion,
		dailyTargets:           dailyTargets,
		dailyDates:             dailyDates,
		dailyRebuilder:         dailyRebuilder,
		dailyBackfillCompleted: dailyBackfillCompleted,
	}
	r.jobs[job.ID] = job
	r.activeWorkers.Add(1)
	r.workers.Go(func() {
		defer r.activeWorkers.Add(-1)
		r.run(ctx, job)
	})
	r.mu.Unlock()
	return job, nil
}

func (r *RebuildRunner) hasRunningDailyJobLocked() bool {
	for _, job := range r.jobs {
		job.mu.Lock()
		running := job.Status == JobStatusRunning
		job.mu.Unlock()
		if running && dailyOnlyTargets(job.Filter.Targets) {
			return true
		}
	}
	return false
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

func (r *RebuildRunner) run(ctx context.Context, job *RebuildJob) {
	r.mu.RLock()
	sliceSleep := r.sliceSleep
	r.mu.RUnlock()

	err := r.runLogDaily(ctx, job, sliceSleep, job.dailyRebuilder)
	r.finishJob(ctx, job, err)
}

func dailyOnlyTargets(targets []string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, target := range targets {
		if target != dao.RebuildTargetTokenDaily && target != dao.RebuildTargetChannelDaily {
			return false
		}
	}
	return true
}

func (r *RebuildRunner) runLogDaily(
	ctx context.Context,
	job *RebuildJob,
	sliceSleep time.Duration,
	rebuilder dao.LogDailyBillingRebuilder,
) error {
	if rebuilder == nil {
		return errors.New("daily billing rebuilder is not configured")
	}
	version := job.dailyBackfillVersion
	if version > 0 {
		if err := updateDailyBackfillRunning(ctx, r.app, version); err != nil {
			return r.failAutomaticDaily(ctx, version, err)
		}
	}
	for _, date := range job.dailyDates {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		r.inflight.Add(1)
		result, rebuildErr := rebuilder.RebuildLogDailyDate(ctx, date, version, job.dailyTargets)
		r.inflight.Add(-1)
		if rebuildErr != nil {
			return r.failAutomaticDaily(ctx, version, rebuildErr)
		}
		if result == nil {
			return r.failAutomaticDaily(ctx, version, errors.New("daily billing rebuild returned nil result"))
		}
		atomic.AddInt64(&job.DoneSlices, 1)
		atomic.AddInt64(&job.ReplayedLogs, result.ReplayedLogs)
		if err := waitForNextSlice(ctx, sliceSleep); err != nil {
			return err
		}
	}
	if version > 0 {
		if err := updateDailyBackfillCompleted(ctx, r.app, version); err != nil {
			return r.failAutomaticDaily(ctx, version, err)
		}
		if job.dailyBackfillCompleted != nil {
			if err := job.dailyBackfillCompleted(ctx); err != nil {
				return err
			}
		}
	}
	return nil
}

func findDailyRebuildDates(
	ctx context.Context,
	rebuilder dao.LogDailyBillingRebuilder,
	startDate, endDate string,
) ([]string, error) {
	if rebuilder == nil {
		return nil, errors.New("daily billing rebuilder is not configured")
	}
	previous, err := previousDailyDate(startDate)
	if err != nil {
		return nil, err
	}
	dates := make([]string, 0)
	for {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		date, ok, err := rebuilder.FindNextRequestLogDate(ctx, previous, endDate)
		if err != nil {
			return nil, err
		}
		if !ok {
			return dates, nil
		}
		if date <= previous || date > endDate {
			return nil, fmt.Errorf("daily billing rebuilder returned out-of-order date %q after %q", date, previous)
		}
		dates = append(dates, date)
		previous = date
	}
}

func (r *RebuildRunner) failAutomaticDaily(ctx context.Context, version uint, rebuildErr error) error {
	if version == 0 || context.Cause(ctx) != nil {
		return rebuildErr
	}
	if markerErr := updateDailyBackfillFailed(ctx, r.app, version, rebuildErr); markerErr != nil {
		return errors.Join(rebuildErr, markerErr)
	}
	return rebuildErr
}

func previousDailyDate(date string) (string, error) {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("parse daily rebuild start date %q: %w", date, err)
	}
	return parsed.AddDate(0, 0, -1).Format("2006-01-02"), nil
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
