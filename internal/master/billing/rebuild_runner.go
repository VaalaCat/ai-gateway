package billing

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/google/uuid"
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

// SliceFn is the per-slice dao call. Injectable for tests; production wires
// dao.RebuildHourSlice via RebuildRunner.SetSliceFn (T3.4 default fallback
// uses dao.RebuildHourSlice when app is non-nil).
type SliceFn func(date string, hour int, targets []string, resetDailyForDate bool) (*dao.BillingRebuildResult, error)

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

	sliceFn  SliceFn
	stopCh   chan struct{}
	stopOnce sync.Once
}

// NewRebuildRunner constructs the runner and spawns a gc goroutine for
// terminal-job cleanup. app may be nil for pure-memory tests (in which case
// SetSliceFn must be called before any Submit that should actually persist).
// retain controls how long terminal jobs stay visible via Get/List.
func NewRebuildRunner(app dao.AppProvider, logger *zap.Logger, retain time.Duration) *RebuildRunner {
	r := &RebuildRunner{
		jobs:   make(map[string]*RebuildJob),
		app:    app,
		logger: logger,
		retain: retain,
		stopCh: make(chan struct{}),
	}
	go r.gcLoop()
	return r
}

// SetSliceFn overrides the per-slice executor. Used by tests; production
// passes nil and lets run() fall back to dao.RebuildHourSlice (see T3.4).
func (r *RebuildRunner) SetSliceFn(fn SliceFn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sliceFn = fn
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

	ctx, cancel := context.WithCancel(context.Background())
	job := &RebuildJob{
		ID:          uuid.NewString(),
		Filter:      filter,
		Status:      JobStatusRunning,
		TotalSlices: int64(len(days) * 24),
		StartedAt:   time.Now().Unix(),
		cancel:      cancel,
	}
	r.mu.Lock()
	r.jobs[job.ID] = job
	r.mu.Unlock()

	go r.run(ctx, job, days)
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

// run is the per-job worker goroutine. Schedules per-(date,hour) calls in
// sequence: 24 slices per day, hour=0 carries resetDailyForDate=true to
// clear the day's accumulator rows before replaying. Updates DoneSlices /
// ReplayedLogs atomically as it goes. Any slice error fails the whole job;
// ctx cancellation marks it canceled.
func (r *RebuildRunner) run(ctx context.Context, job *RebuildJob, days []string) {
	r.mu.RLock()
	fn := r.sliceFn
	r.mu.RUnlock()
	if fn == nil {
		if r.app == nil {
			job.mu.Lock()
			job.Status = JobStatusFailed
			job.Error = "no slice fn and no app provider"
			job.FinishedAt = time.Now().Unix()
			job.mu.Unlock()
			return
		}
		// Default production path: dao.RebuildHourSlice on a fresh per-call
		// context, so each slice gets its own transaction.
		fn = func(date string, hour int, targets []string, resetDaily bool) (*dao.BillingRebuildResult, error) {
			return dao.NewAdminMutation(dao.NewContext(r.app)).Billing().RebuildHourSlice(date, hour, targets, resetDaily)
		}
	}

	for _, d := range days {
		for h := 0; h < 24; h++ {
			if ctx.Err() != nil {
				job.mu.Lock()
				if job.Status == JobStatusRunning {
					job.Status = JobStatusCanceled
					job.FinishedAt = time.Now().Unix()
				}
				job.mu.Unlock()
				return
			}
			res, err := fn(d, h, job.Filter.Targets, h == 0)
			if err != nil {
				job.mu.Lock()
				job.Status = JobStatusFailed
				job.Error = err.Error()
				job.FinishedAt = time.Now().Unix()
				job.mu.Unlock()
				if r.logger != nil {
					r.logger.Error("rebuild_slice_failed",
						zap.String("job_id", job.ID),
						zap.String("date", d),
						zap.Int("hour", h),
						zap.Error(err))
				}
				return
			}
			atomic.AddInt64(&job.DoneSlices, 1)
			atomic.AddInt64(&job.ReplayedLogs, res.ReplayedLogs)
		}
	}

	job.mu.Lock()
	job.Status = JobStatusSucceeded
	job.FinishedAt = time.Now().Unix()
	job.mu.Unlock()
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
		case <-r.stopCh:
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
func (r *RebuildRunner) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, j := range r.jobs {
		j.mu.Lock()
		if j.Status == JobStatusRunning && j.cancel != nil {
			j.cancel()
		}
		j.mu.Unlock()
	}
}
