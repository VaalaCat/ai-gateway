package connectivity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/google/uuid"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

const (
	defaultProbeGlobalConcurrency  = 32
	defaultProbeSourceConcurrency  = 4
	defaultProbeSuccessTTL         = 5 * time.Minute
	defaultProbeFailureRetryMin    = 30 * time.Second
	defaultProbeFailureRetryMax    = 5 * time.Minute
	defaultProbeManualRetention    = 15 * time.Minute
	defaultProbeMaxProgressEntries = 1024
	defaultProbeMaxJobs            = 8192
	defaultProbeSweepInterval      = 30 * time.Second
)

var errProbeSchedulerClosed = errors.New("probe scheduler: closed")

type ProbeCaller interface {
	CallDirectProbe(ctx context.Context, sourceID string, sourceGeneration uint64, target protocol.DirectProbeTarget) (protocol.DirectProbeResult, error)
}

type ProbeTarget struct {
	AgentID           string
	Name              string
	Tags              []string
	Addresses         []protocol.Address
	EffectiveProxy    string
	ControlGeneration uint64
	Capabilities      []string
	PeerRouteMode     string
}

type ProbeTargetFinder interface {
	FindEnabledProbeTargets(ctx context.Context, targetAgentIDs []string) ([]ProbeTarget, error)
}

type ProbeSourceFinder interface {
	FindEnabledProbeSource(ctx context.Context, sourceAgentID string) (ProbeTarget, error)
}

type SchedulerOptions struct {
	GlobalConcurrency    int
	PerSourceConcurrency int
	SuccessTTL           time.Duration
	FailureRetryMin      time.Duration
	FailureRetryMax      time.Duration
	ManualRetention      time.Duration
	MaxProgressEntries   int
	Now                  func() time.Time
	ProbeTargetFinder    ProbeTargetFinder
}

type ProbeTimings struct {
	SuccessTTL      time.Duration
	FailureRetryMin time.Duration
	FailureRetryMax time.Duration
}

type probePriority uint8

const (
	probePriorityStale probePriority = iota + 1
	probePriorityRecovery
	probePriorityManual
)

type probeJobKey struct {
	sourceID      string
	targetAgentID string
	fingerprint   string
	path          probePath
}

type probeJob struct {
	key                   probeJobKey
	target                ProbeTarget
	source                ProbeTarget
	sourceGeneration      uint64
	sourceRelayGeneration uint64
	targetRelayGeneration uint64
	priority              probePriority
	sequence              uint64
	manualIDs             map[string]struct{}
	running               bool
	probeGeneration       uint64
	startedAt             time.Time
}

type probeHistory struct {
	nextAt          time.Time
	failureAttempts int
}

type manualProbeProgressEntry struct {
	sourceID string
	progress protocol.ManualProbeProgress
	pending  map[string]int
}

type Scheduler struct {
	control ProbeCaller
	service *Service
	finder  ProbeTargetFinder
	opts    SchedulerOptions

	mu             sync.Mutex
	jobs           map[probeJobKey]*probeJob
	history        map[probeJobKey]probeHistory
	progress       map[string]manualProbeProgressEntry
	progressExpiry map[string]time.Time
	activeBySource map[string]int
	active         int
	nextSequence   uint64
	nextGeneration uint64
	runCtx         context.Context
	runCancel      context.CancelCauseFunc
	running        bool
	closing        bool
	done           chan struct{}
	doneOnce       sync.Once
	wake           chan struct{}
	logger         *zap.Logger
	suppressor     *diagnostics.Suppressor
	runners        map[probePath]ProbeRunner
	timings        atomic.Pointer[ProbeTimings]
	timingsChanged chan struct{}
}

func NewScheduler(control ProbeCaller, service *Service, opts SchedulerOptions) *Scheduler {
	normalizeSchedulerOptions(&opts)
	logger := zap.NewNop()
	if service != nil {
		logger = service.options.Logger
	}
	scheduler := &Scheduler{
		control: control, service: service, finder: opts.ProbeTargetFinder, opts: opts,
		jobs: make(map[probeJobKey]*probeJob), history: make(map[probeJobKey]probeHistory),
		progress: make(map[string]manualProbeProgressEntry), progressExpiry: make(map[string]time.Time),
		activeBySource: make(map[string]int), done: make(chan struct{}),
		wake:   make(chan struct{}, opts.GlobalConcurrency),
		logger: logger, suppressor: diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}),
		runners: make(map[probePath]ProbeRunner, 2), timingsChanged: make(chan struct{}, 1),
	}
	scheduler.storeTimings(ProbeTimings{
		SuccessTTL: opts.SuccessTTL, FailureRetryMin: opts.FailureRetryMin, FailureRetryMax: opts.FailureRetryMax,
	})
	scheduler.registerRunner(&DirectProbeRunner{caller: control, service: service})
	if relayCaller, ok := control.(RelayProbeCaller); ok {
		scheduler.registerRunner(&RelayProbeRunner{caller: relayCaller, service: service})
	}
	return scheduler
}

func (s *Scheduler) SetTimings(successTTL, failureRetryMin, failureRetryMax time.Duration) {
	if s == nil {
		return
	}
	timings := normalizeProbeTimings(ProbeTimings{
		SuccessTTL: successTTL, FailureRetryMin: failureRetryMin, FailureRetryMax: failureRetryMax,
	})
	s.storeTimings(timings)
	select {
	case s.timingsChanged <- struct{}{}:
	default:
	}
}

func normalizeProbeTimings(timings ProbeTimings) ProbeTimings {
	if timings.SuccessTTL < 30*time.Second || timings.SuccessTTL > time.Hour {
		timings.SuccessTTL = defaultProbeSuccessTTL
	}
	if timings.FailureRetryMin < 5*time.Second || timings.FailureRetryMin > 5*time.Minute {
		timings.FailureRetryMin = defaultProbeFailureRetryMin
	}
	if timings.FailureRetryMax < timings.FailureRetryMin || timings.FailureRetryMax > time.Hour {
		timings.FailureRetryMax = defaultProbeFailureRetryMax
	}
	return timings
}

func (s *Scheduler) storeTimings(timings ProbeTimings) {
	copy := timings
	s.timings.Store(&copy)
}

func (s *Scheduler) currentTimings() ProbeTimings {
	if s != nil {
		if timings := s.timings.Load(); timings != nil {
			return *timings
		}
	}
	return ProbeTimings{
		SuccessTTL: defaultProbeSuccessTTL, FailureRetryMin: defaultProbeFailureRetryMin,
		FailureRetryMax: defaultProbeFailureRetryMax,
	}
}

func (s *Scheduler) registerRunner(runner ProbeRunner) {
	if s == nil || runner == nil {
		return
	}
	s.runners[runner.Path()] = runner
}

func normalizeSchedulerOptions(opts *SchedulerOptions) {
	if opts.GlobalConcurrency <= 0 {
		opts.GlobalConcurrency = defaultProbeGlobalConcurrency
	}
	if opts.PerSourceConcurrency <= 0 {
		opts.PerSourceConcurrency = defaultProbeSourceConcurrency
	}
	if opts.PerSourceConcurrency > opts.GlobalConcurrency {
		opts.PerSourceConcurrency = opts.GlobalConcurrency
	}
	if opts.SuccessTTL <= 0 {
		opts.SuccessTTL = defaultProbeSuccessTTL
	}
	if opts.FailureRetryMin <= 0 {
		opts.FailureRetryMin = defaultProbeFailureRetryMin
	}
	if opts.FailureRetryMax < opts.FailureRetryMin {
		opts.FailureRetryMax = defaultProbeFailureRetryMax
	}
	if opts.ManualRetention <= 0 {
		opts.ManualRetention = defaultProbeManualRetention
	}
	if opts.MaxProgressEntries <= 0 {
		opts.MaxProgressEntries = defaultProbeMaxProgressEntries
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
}

func (s *Scheduler) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("probe scheduler: nil run context")
	}
	s.mu.Lock()
	if s.running || s.closedLocked() {
		s.mu.Unlock()
		return errors.New("probe scheduler: already running or closed")
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	s.runCtx, s.runCancel, s.running = runCtx, cancel, true
	s.mu.Unlock()

	var workers conc.WaitGroup
	for range s.opts.GlobalConcurrency {
		workers.Go(func() { s.worker(runCtx) })
	}
	_ = s.enqueueAutomaticObserved(runCtx)
	s.signalWorkers()
	timer := time.NewTimer(s.currentSweepInterval())
	defer timer.Stop()
	for {
		select {
		case <-runCtx.Done():
			s.finishRun()
			workers.Wait()
			s.markStopped(context.Cause(runCtx))
			return context.Cause(runCtx)
		case <-timer.C:
			_ = s.enqueueAutomaticObserved(runCtx)
			s.cleanupProgress()
			timer.Reset(s.currentSweepInterval())
		case <-s.timingsChanged:
			resetProbeTimer(timer, s.currentSweepInterval())
		}
	}
}

func (s *Scheduler) currentSweepInterval() time.Duration {
	interval := defaultProbeSweepInterval
	timings := s.currentTimings()
	if timings.FailureRetryMin > interval {
		interval = timings.FailureRetryMin
	}
	if timings.SuccessTTL < interval {
		interval = timings.SuccessTTL
	}
	return interval
}

func resetProbeTimer(timer *time.Timer, after time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(after)
}

func (s *Scheduler) worker(ctx context.Context) {
	for {
		job, ok := s.nextJob()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-s.wake:
				continue
			}
		}
		s.runJob(ctx, job)
	}
}

func (s *Scheduler) runJob(ctx context.Context, job probeJob) {
	now := s.opts.Now()
	job.startedAt = now
	s.startManualProgress(job, now)
	runner := s.runners[job.key.path]
	if runner == nil {
		s.completeJobOutcome(job, false, errors.New("probe scheduler: runner is required"))
		return
	}
	outcome := runner.Run(ctx, job)
	s.completeJobOutcome(job, outcome.succeeded, outcome.callErr)
}

func (s *Scheduler) EnqueueManual(ctx context.Context, sourceID string, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	if err := validateManualProbeRequest(ctx, sourceID, scope); err != nil {
		return protocol.ProbeAck{}, err
	}
	sourceGeneration, ok := s.controlGeneration(sourceID)
	if !ok {
		return protocol.ProbeAck{}, errors.New("probe scheduler: source control session unavailable")
	}
	return s.enqueueManualSession(ctx, sourceID, sourceGeneration, scope)
}

func (s *Scheduler) EnqueueManualSession(ctx context.Context, sourceID string, expectedControlGeneration uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	if err := validateManualProbeRequest(ctx, sourceID, scope); err != nil {
		return protocol.ProbeAck{}, err
	}
	if expectedControlGeneration == 0 {
		return protocol.ProbeAck{}, fmt.Errorf("%w: source %s expected generation is zero", ErrConnectionGenerationChanged, sourceID)
	}
	return s.enqueueManualSession(ctx, sourceID, expectedControlGeneration, scope)
}

func validateManualProbeRequest(ctx context.Context, sourceID string, scope protocol.ProbeScope) error {
	if ctx == nil {
		return errors.New("probe scheduler: nil enqueue context")
	}
	if sourceID == "" {
		return errors.New("probe scheduler: source agent is required")
	}
	if err := validateProbeScope(scope); err != nil {
		return err
	}
	return nil
}

func (s *Scheduler) enqueueManualSession(ctx context.Context, sourceID string, expectedControlGeneration uint64, scope protocol.ProbeScope) (protocol.ProbeAck, error) {
	s.mu.Lock()
	closed := s.closedLocked()
	s.mu.Unlock()
	if closed {
		return protocol.ProbeAck{}, errProbeSchedulerClosed
	}
	if s.finder == nil {
		return protocol.ProbeAck{}, errors.New("probe scheduler: target finder is required")
	}
	if err := s.requireControlGeneration(sourceID, expectedControlGeneration); err != nil {
		return protocol.ProbeAck{}, err
	}
	targets, err := s.finder.FindEnabledProbeTargets(ctx, probeTargetIDsForScope(scope))
	// behavior change: cancellation after target discovery must not reserve scheduler state.
	if cause := context.Cause(ctx); cause != nil {
		return protocol.ProbeAck{}, cause
	}
	if err != nil {
		return protocol.ProbeAck{}, fmt.Errorf("probe scheduler targets: %w", err)
	}
	if err := s.requireControlGeneration(sourceID, expectedControlGeneration); err != nil {
		return protocol.ProbeAck{}, err
	}
	source, err := s.findProbeSource(ctx, sourceID, targets)
	if err != nil {
		return protocol.ProbeAck{}, fmt.Errorf("probe scheduler source: %w", err)
	}
	targets = s.filterProbeTargets(sourceID, scope, targets)
	now := s.opts.Now()
	type preparedManualJob struct {
		target ProbeTarget
		path   preparedProbePath
	}
	prepared := make([]preparedManualJob, 0, len(targets)*2)
	keys := make([]probeJobKey, 0, len(targets)*2)
	pending := make(map[string]int, len(targets))
	for _, target := range targets {
		paths := s.prepareProbePaths(sourceID, expectedControlGeneration, source, target)
		pending[target.AgentID] = len(paths)
		for _, path := range paths {
			prepared = append(prepared, preparedManualJob{target: target, path: path})
			keys = append(keys, probeJobKey{
				sourceID: sourceID, targetAgentID: target.AgentID, fingerprint: path.fingerprint, path: path.kind,
			})
		}
	}

	s.mu.Lock()
	if cause := context.Cause(ctx); cause != nil {
		s.mu.Unlock()
		return protocol.ProbeAck{}, cause
	}
	if s.closedLocked() {
		s.mu.Unlock()
		return protocol.ProbeAck{}, errProbeSchedulerClosed
	}
	s.cleanupProgressLocked(now)
	if len(s.progress) >= s.opts.MaxProgressEntries {
		s.mu.Unlock()
		return protocol.ProbeAck{}, errors.New("probe scheduler: manual progress capacity reached")
	}
	if !s.reserveManualCapacityLocked(keys) {
		s.mu.Unlock()
		return protocol.ProbeAck{}, errors.New("probe scheduler: probe queue capacity reached")
	}
	s.nextGeneration++
	probeGeneration := s.nextGeneration
	probeID := uuid.NewString()
	remainingTargets := 0
	for _, count := range pending {
		if count > 0 {
			remainingTargets++
		}
	}
	progress := protocol.ManualProbeProgress{
		ProbeID: probeID, State: "queued", TargetTotal: len(targets), Remaining: remainingTargets,
	}
	if remainingTargets == 0 {
		progress.State, progress.StartedAt, progress.CompletedAt = "completed", now.Unix(), now.Unix()
		s.progressExpiry[probeID] = now.Add(s.opts.ManualRetention)
	}
	s.progress[probeID] = manualProbeProgressEntry{sourceID: sourceID, progress: progress, pending: pending}
	for _, current := range prepared {
		s.enqueuePathLocked(
			probePriorityManual, sourceID, expectedControlGeneration, source, current.target, current.path,
			probeID, probeGeneration,
		)
	}
	snapshotSeq := uint64(0)
	if s.service != nil {
		snapshotSeq = s.service.sequence.Load()
	}
	s.mu.Unlock()
	s.signalWorkers()
	return protocol.ProbeAck{
		ProbeID: probeID, ProbeGeneration: probeGeneration, Scope: cloneProbeScope(scope),
		State: progress.State, TargetTotal: len(targets), SnapshotSeq: snapshotSeq,
	}, nil
}

func (s *Scheduler) requireControlGeneration(sourceID string, expected uint64) error {
	current, ok := s.controlGeneration(sourceID)
	if !ok || current != expected {
		return fmt.Errorf("%w: source %s expected generation %d is not current", ErrConnectionGenerationChanged, sourceID, expected)
	}
	return nil
}

func (s *Scheduler) findProbeSource(ctx context.Context, sourceID string, targets []ProbeTarget) (ProbeTarget, error) {
	for _, target := range targets {
		if target.AgentID == sourceID {
			return cloneProbeTarget(target), nil
		}
	}
	finder, ok := s.finder.(ProbeSourceFinder)
	if !ok {
		return ProbeTarget{AgentID: sourceID}, nil
	}
	return finder.FindEnabledProbeSource(ctx, sourceID)
}

func (s *Scheduler) Progress(probeID string) (protocol.ManualProbeProgress, bool) {
	s.mu.Lock()
	entry, ok := s.progress[probeID]
	s.mu.Unlock()
	return entry.progress, ok
}

func (s *Scheduler) FindProgressForSource(sourceID, probeID string) (protocol.ManualProbeProgress, bool) {
	if sourceID == "" || probeID == "" {
		return protocol.ManualProbeProgress{}, false
	}
	s.mu.Lock()
	entry, ok := s.progress[probeID]
	s.mu.Unlock()
	if !ok || entry.sourceID != sourceID {
		return protocol.ManualProbeProgress{}, false
	}
	return entry.progress, true
}

func (s *Scheduler) Cancel() {
	s.mu.Lock()
	cancel := s.runCancel
	s.closing = true
	s.mu.Unlock()
	if cancel != nil {
		cancel(errProbeSchedulerClosed)
	} else {
		s.markStopped(errProbeSchedulerClosed)
	}
}

func (s *Scheduler) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("probe scheduler: nil close context")
	}
	s.Cancel()
	select {
	case <-s.Done():
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Scheduler) Done() <-chan struct{} { return s.done }

func (s *Scheduler) ResourceCount() int {
	s.mu.Lock()
	active := s.active
	s.mu.Unlock()
	return active
}

func (s *Scheduler) nextJob() (probeJob, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var selected *probeJob
	for _, job := range s.jobs {
		if job.running || s.activeBySource[job.key.sourceID] >= s.opts.PerSourceConcurrency {
			continue
		}
		if selected == nil || job.priority > selected.priority || job.priority == selected.priority && job.sequence < selected.sequence {
			selected = job
		}
	}
	if selected == nil || s.active >= s.opts.GlobalConcurrency {
		return probeJob{}, false
	}
	selected.running = true
	s.active++
	s.activeBySource[selected.key.sourceID]++
	return cloneProbeJob(selected), true
}

func (s *Scheduler) completeJob(job probeJob, result protocol.DirectProbeResult, callErr error) {
	s.completeJobOutcome(job, callErr == nil && result.Eligible, callErr)
}

func (s *Scheduler) completeJobOutcome(job probeJob, succeeded bool, callErr error) {
	now := s.opts.Now()
	s.mu.Lock()
	current := s.jobs[job.key]
	manualIDs := job.manualIDs
	if current != nil && current.sequence == job.sequence {
		manualIDs = current.manualIDs
	}
	if current != nil && current.sequence == job.sequence {
		delete(s.jobs, job.key)
	}
	if s.active > 0 {
		s.active--
	}
	if s.activeBySource[job.key.sourceID] > 1 {
		s.activeBySource[job.key.sourceID]--
	} else {
		delete(s.activeBySource, job.key.sourceID)
	}
	history := s.history[job.key]
	if callErr == nil && succeeded {
		history = probeHistory{nextAt: s.nextSuccessAt(now)}
	} else {
		history.failureAttempts++
		history.nextAt = now.Add(s.failureDelay(job.key, history.failureAttempts))
	}
	s.history[job.key] = history
	for probeID := range manualIDs {
		if s.closedLocked() {
			s.cancelManualLocked(probeID, now)
		} else {
			s.completeManualTargetPathLocked(probeID, job.key.targetAgentID, now)
		}
	}
	s.trimHistoryLocked()
	s.mu.Unlock()
	s.signalWorkers()
}

func (s *Scheduler) completeManualTargetPathLocked(probeID, targetAgentID string, now time.Time) {
	entry, ok := s.progress[probeID]
	if !ok {
		return
	}
	if len(entry.pending) == 0 {
		s.completeManualLocked(probeID, now)
		return
	}
	remaining := entry.pending[targetAgentID]
	if remaining > 1 {
		entry.pending[targetAgentID] = remaining - 1
		s.progress[probeID] = entry
		return
	}
	if remaining == 1 {
		delete(entry.pending, targetAgentID)
		s.progress[probeID] = entry
		s.completeManualLocked(probeID, now)
	}
}

func (s *Scheduler) cancelManualLocked(probeID string, now time.Time) {
	entry, ok := s.progress[probeID]
	progress := entry.progress
	if !ok || progress.State == "completed" || progress.State == "cancelled" {
		return
	}
	progress.State, progress.CompletedAt = "cancelled", now.Unix()
	if progress.StartedAt == 0 {
		progress.StartedAt = now.Unix()
	}
	entry.progress = progress
	s.progress[probeID] = entry
	s.progressExpiry[probeID] = now.Add(s.opts.ManualRetention)
}

func (s *Scheduler) reserveManualCapacityLocked(keys []probeJobKey) bool {
	needed := 0
	seen := make(map[probeJobKey]struct{}, len(keys))
	for _, key := range keys {
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		if s.jobs[key] == nil {
			needed++
		}
	}
	for len(s.jobs)+needed > defaultProbeMaxJobs {
		var candidate *probeJob
		for _, job := range s.jobs {
			if job.running || len(job.manualIDs) > 0 {
				continue
			}
			if candidate == nil || job.priority < candidate.priority || job.priority == candidate.priority && job.sequence < candidate.sequence {
				candidate = job
			}
		}
		if candidate == nil {
			return false
		}
		delete(s.jobs, candidate.key)
	}
	return true
}

func (s *Scheduler) startManualProgress(job probeJob, now time.Time) {
	s.mu.Lock()
	for probeID := range job.manualIDs {
		entry, ok := s.progress[probeID]
		progress := entry.progress
		if !ok || progress.State == "completed" || progress.State == "cancelled" {
			continue
		}
		progress.State = "running"
		if progress.StartedAt == 0 {
			progress.StartedAt = now.Unix()
		}
		entry.progress = progress
		s.progress[probeID] = entry
	}
	s.mu.Unlock()
}

func (s *Scheduler) completeManualLocked(probeID string, now time.Time) {
	entry, ok := s.progress[probeID]
	progress := entry.progress
	if !ok || progress.State == "completed" || progress.State == "cancelled" {
		return
	}
	if progress.Remaining > 0 {
		progress.Remaining--
	}
	if progress.Remaining == 0 {
		progress.State, progress.CompletedAt = "completed", now.Unix()
		if progress.StartedAt == 0 {
			progress.StartedAt = now.Unix()
		}
		s.progressExpiry[probeID] = now.Add(s.opts.ManualRetention)
	}
	entry.progress = progress
	s.progress[probeID] = entry
}

func (s *Scheduler) finishRun() {
	now := s.opts.Now()
	s.mu.Lock()
	s.terminalizeLocked(now)
	s.mu.Unlock()
	s.signalWorkers()
}

func (s *Scheduler) markStopped(_ error) {
	s.mu.Lock()
	s.running = false
	s.closing = true
	s.terminalizeLocked(s.opts.Now())
	s.active = 0
	clear(s.activeBySource)
	s.mu.Unlock()
	s.doneOnce.Do(func() { close(s.done) })
}

func (s *Scheduler) terminalizeLocked(now time.Time) {
	for probeID := range s.progress {
		s.cancelManualLocked(probeID, now)
	}
	clear(s.jobs)
}

func (s *Scheduler) closedLocked() bool {
	return s.closing || s.runCtx != nil && context.Cause(s.runCtx) != nil
}

func (s *Scheduler) signalWorkers() {
	for range s.opts.GlobalConcurrency {
		select {
		case s.wake <- struct{}{}:
		default:
			return
		}
	}
}

func (s *Scheduler) controlGeneration(sourceID string) (uint64, bool) {
	if s.service == nil {
		return 0, false
	}
	fact, ok := s.service.controlFact(sourceID)
	return fact.Generation, ok
}

func (s *Scheduler) enqueueAutomatic(ctx context.Context) error {
	if ctx == nil {
		return errors.New("probe scheduler: nil automatic context")
	}
	if s.finder == nil || s.service == nil {
		return nil
	}
	type sourceEdges struct {
		sourceID string
		edges    []protocol.RouteEdgeSnapshot
	}
	sources := make([]sourceEdges, 0)
	targetIDSet := make(map[string]struct{})
	for _, sourceID := range s.service.RouteEdgeSources() {
		edges := s.service.RouteEdges(sourceID)
		if len(edges) == 0 {
			continue
		}
		sources = append(sources, sourceEdges{sourceID: sourceID, edges: edges})
		if _, relayEnabled := s.runners[probePathRelay]; relayEnabled {
			targetIDSet[sourceID] = struct{}{}
		}
		for _, edge := range edges {
			if edge.TargetAgentID != "" {
				targetIDSet[edge.TargetAgentID] = struct{}{}
			}
		}
	}
	if len(targetIDSet) == 0 {
		return nil
	}
	targetAgentIDs := make([]string, 0, len(targetIDSet))
	for targetAgentID := range targetIDSet {
		targetAgentIDs = append(targetAgentIDs, targetAgentID)
	}
	sort.Strings(targetAgentIDs)
	targets, err := s.finder.FindEnabledProbeTargets(ctx, targetAgentIDs)
	if err != nil {
		return fmt.Errorf("probe scheduler automatic targets: %w", err)
	}
	byID := make(map[string]ProbeTarget, len(targets))
	for _, target := range targets {
		if target.AgentID != "" && (len(target.Addresses) > 0 || s.runners[probePathRelay] != nil) {
			byID[target.AgentID] = cloneProbeTarget(target)
		}
	}
	now := s.opts.Now()
	for _, source := range sources {
		sourceID := source.sourceID
		sourceGeneration, ok := s.controlGeneration(sourceID)
		if !ok {
			continue
		}
		sourceTarget := byID[sourceID]
		if sourceTarget.AgentID == "" {
			sourceTarget.AgentID = sourceID
		}
		for _, edge := range source.edges {
			target, exists := byID[edge.TargetAgentID]
			if !exists || target.AgentID == sourceID {
				continue
			}
			for _, path := range s.prepareProbePaths(sourceID, sourceGeneration, sourceTarget, target) {
				key := probeJobKey{
					sourceID: sourceID, targetAgentID: target.AgentID, fingerprint: path.fingerprint, path: path.kind,
				}
				s.mu.Lock()
				history, hasHistory := s.history[key]
				if hasHistory && now.Before(history.nextAt) {
					s.mu.Unlock()
					continue
				}
				priority := s.automaticProbePriority(path.kind, sourceID, target.AgentID, edge, path.fingerprint)
				s.nextGeneration++
				s.enqueuePathLocked(priority, sourceID, sourceGeneration, sourceTarget, target, path, "", s.nextGeneration)
				s.mu.Unlock()
			}
		}
	}
	s.signalWorkers()
	return nil
}

func (s *Scheduler) automaticProbePriority(
	path probePath,
	sourceID string,
	targetAgentID string,
	edge protocol.RouteEdgeSnapshot,
	fingerprint string,
) probePriority {
	if path == probePathRelay {
		relay, exists := s.service.currentRelayTarget(sourceID, targetAgentID)
		if exists && (relay.CheckedAt > 0 && relay.State != protocol.RelayProbeReachable ||
			relay.RelayFingerprint != "" && relay.RelayFingerprint != fingerprint) {
			return probePriorityRecovery
		}
		return probePriorityStale
	}
	direct, exists := s.service.currentDirectTarget(sourceID, targetAgentID)
	edgeFailed := edge.LastDirectResult != "" && edge.LastDirectResult != "success"
	directFailed := exists && direct.CheckedAt > 0 && !direct.Eligible
	fingerprintChanged := exists && direct.AddressFingerprint != "" && direct.AddressFingerprint != fingerprint
	if edgeFailed || directFailed || fingerprintChanged {
		return probePriorityRecovery
	}
	return probePriorityStale
}

func (s *Scheduler) enqueueAutomaticObserved(ctx context.Context) error {
	err := s.enqueueAutomatic(ctx)
	key := diagnostics.SuppressionKey{
		Source: "master", PathKind: "connectivity", Stage: "target_discovery", ReasonCode: "automatic_probe_targets_failed",
	}
	now := s.opts.Now()
	if err == nil {
		if summary := s.suppressor.Recover(key, now); summary != nil {
			s.logger.Warn("automatic connectivity probe target discovery recovered",
				zap.String("path_kind", key.PathKind), zap.String("stage", key.Stage),
				zap.String("reason_code", key.ReasonCode), zap.Uint64("suppressed_count", summary.SuppressedCount))
		}
		return nil
	}
	if context.Cause(ctx) != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, errProbeSchedulerClosed) {
		return err
	}
	decision := s.suppressor.Observe(key, now)
	if decision.Summary != nil {
		s.logger.Warn("automatic connectivity probe target discovery failures suppressed",
			zap.String("path_kind", key.PathKind), zap.String("stage", key.Stage),
			zap.String("reason_code", key.ReasonCode), zap.Uint64("suppressed_count", decision.Summary.SuppressedCount))
	} else if decision.Allow {
		s.logger.Warn("automatic connectivity probe target discovery failed",
			zap.String("path_kind", key.PathKind), zap.String("stage", key.Stage),
			zap.String("reason_code", key.ReasonCode), zap.String("error", diagnostics.SanitizeText(err.Error())))
	}
	return err
}

func (s *Scheduler) enqueueLocked(priority probePriority, sourceID string, sourceGeneration uint64, target ProbeTarget, fingerprint, probeID string, probeGeneration uint64) {
	s.enqueuePathLocked(priority, sourceID, sourceGeneration, ProbeTarget{AgentID: sourceID}, target, preparedProbePath{
		kind: probePathDirect, fingerprint: fingerprint,
	}, probeID, probeGeneration)
}

func (s *Scheduler) enqueuePathLocked(
	priority probePriority,
	sourceID string,
	sourceGeneration uint64,
	source ProbeTarget,
	target ProbeTarget,
	path preparedProbePath,
	probeID string,
	probeGeneration uint64,
) {
	key := probeJobKey{sourceID: sourceID, targetAgentID: target.AgentID, fingerprint: path.fingerprint, path: path.kind}
	if existing := s.jobs[key]; existing != nil {
		if priority > existing.priority {
			existing.priority = priority
		}
		if probeID != "" {
			existing.manualIDs[probeID] = struct{}{}
			if existing.running {
				entry := s.progress[probeID]
				progress := entry.progress
				progress.State, progress.StartedAt = "running", s.opts.Now().Unix()
				entry.progress = progress
				s.progress[probeID] = entry
			}
		}
		return
	}
	if len(s.jobs) >= defaultProbeMaxJobs {
		return
	}
	s.nextSequence++
	manualIDs := make(map[string]struct{}, 1)
	if probeID != "" {
		manualIDs[probeID] = struct{}{}
	}
	s.jobs[key] = &probeJob{
		key: key, source: cloneProbeTarget(source), target: cloneProbeTarget(target), sourceGeneration: sourceGeneration,
		sourceRelayGeneration: path.sourceRelayGeneration, targetRelayGeneration: path.targetRelayGeneration,
		priority: priority, sequence: s.nextSequence, manualIDs: manualIDs, probeGeneration: probeGeneration,
	}
}

func (s *Scheduler) nextSuccessAt(now time.Time) time.Time {
	return now.Add(s.currentTimings().SuccessTTL)
}

func (s *Scheduler) failureDelay(key probeJobKey, attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	timings := s.currentTimings()
	delay := timings.FailureRetryMin
	for step := 1; step < attempt && delay < timings.FailureRetryMax; step++ {
		if delay > timings.FailureRetryMax/2 {
			delay = timings.FailureRetryMax
			break
		}
		delay *= 2
	}
	digest := sha256.Sum256([]byte(key.sourceID + "\x00" + key.targetAgentID + "\x00" + key.path.publicName() + "\x00" + key.fingerprint + "\x00" + strconv.Itoa(attempt)))
	jitterPermille := 800 + int(digest[0])*400/255
	delay = time.Duration(int64(delay) * int64(jitterPermille) / 1000)
	if delay > timings.FailureRetryMax {
		return timings.FailureRetryMax
	}
	return delay
}

func CanonicalProbeFingerprint(sourceID string, sourceGeneration uint64, target ProbeTarget) string {
	addresses := make([]string, 0, len(target.Addresses))
	for _, address := range target.Addresses {
		parsed, _ := url.Parse(strings.TrimSpace(address.URL))
		scheme := strings.ToLower(parsed.Scheme)
		addresses = append(addresses, scheme+"\x00"+strings.TrimSpace(address.URL)+"\x00"+address.Tag)
	}
	sort.Strings(addresses)
	canonical := strings.Join([]string{
		sourceID, strconv.FormatUint(sourceGeneration, 10), target.AgentID,
		strconv.FormatUint(target.ControlGeneration, 10), strings.TrimSpace(target.EffectiveProxy),
		strings.Join(addresses, "\x01"),
	}, "\x02")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

type preparedProbePath struct {
	kind                  probePath
	fingerprint           string
	sourceRelayGeneration uint64
	targetRelayGeneration uint64
}

func (s *Scheduler) prepareProbePaths(
	sourceID string,
	sourceGeneration uint64,
	source ProbeTarget,
	target ProbeTarget,
) []preparedProbePath {
	paths := make([]preparedProbePath, 0, 2)
	mode := source.PeerRouteMode
	if mode == "" {
		mode = consts.PeerRouteModeDirectFirst
	}
	if mode != consts.PeerRouteModeRelayOnly && len(target.Addresses) > 0 &&
		slices.Contains(target.Capabilities, protocol.AgentCapabilityDirectIngressV1) {
		paths = append(paths, preparedProbePath{
			kind: probePathDirect, fingerprint: CanonicalProbeFingerprint(sourceID, sourceGeneration, target),
		})
	}
	if s.runners[probePathRelay] == nil || s.service == nil ||
		!slices.Contains(source.Capabilities, protocol.AgentCapabilityRelayHTTPPingV1) ||
		!slices.Contains(target.Capabilities, protocol.AgentCapabilityRelayHTTPPingV1) {
		return paths
	}
	sourceRelay := s.service.relayFact(sourceID)
	targetRelay := s.service.relayFact(target.AgentID)
	if sourceRelay.Availability != "available" || !sourceRelay.AcceptingNewStreams ||
		targetRelay.Availability != "available" || !targetRelay.AcceptingNewStreams ||
		sourceRelay.Active.SessionGeneration == 0 || targetRelay.Active.SessionGeneration == 0 {
		return paths
	}
	paths = append(paths, preparedProbePath{
		kind: probePathRelay,
		fingerprint: CanonicalRelayProbeFingerprint(
			sourceID, sourceGeneration, sourceRelay.Active.SessionGeneration,
			target.AgentID, target.ControlGeneration, targetRelay.Active.SessionGeneration,
		),
		sourceRelayGeneration: sourceRelay.Active.SessionGeneration,
		targetRelayGeneration: targetRelay.Active.SessionGeneration,
	})
	return paths
}

func CanonicalRelayProbeFingerprint(
	sourceID string,
	sourceControlGeneration uint64,
	sourceRelayGeneration uint64,
	targetID string,
	targetControlGeneration uint64,
	targetRelayGeneration uint64,
) string {
	canonical := strings.Join([]string{
		sourceID,
		strconv.FormatUint(sourceControlGeneration, 10),
		strconv.FormatUint(sourceRelayGeneration, 10),
		targetID,
		strconv.FormatUint(targetControlGeneration, 10),
		strconv.FormatUint(targetRelayGeneration, 10),
	}, "\x02")
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func validateProbeScope(scope protocol.ProbeScope) error {
	switch scope.Kind {
	case "all_enabled":
		return nil
	case "tag":
		if strings.TrimSpace(scope.Tag) != "" {
			return nil
		}
	case "targets":
		if len(scope.TargetAgentIDs) > 0 {
			return nil
		}
	}
	return errors.New("probe scheduler: invalid scope")
}

func (s *Scheduler) filterProbeTargets(sourceID string, scope protocol.ProbeScope, targets []ProbeTarget) []ProbeTarget {
	wanted := make(map[string]struct{}, len(scope.TargetAgentIDs))
	for _, id := range scope.TargetAgentIDs {
		wanted[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(targets))
	result := make([]ProbeTarget, 0, len(targets))
	for _, target := range targets {
		if target.AgentID == "" || target.AgentID == sourceID ||
			len(target.Addresses) == 0 && s.runners[probePathRelay] == nil {
			continue
		}
		match := scope.Kind == "all_enabled" || scope.Kind == "tag" && slices.Contains(target.Tags, scope.Tag)
		if scope.Kind == "targets" {
			_, match = wanted[target.AgentID]
		}
		if _, duplicate := seen[target.AgentID]; !match || duplicate {
			continue
		}
		seen[target.AgentID] = struct{}{}
		result = append(result, cloneProbeTarget(target))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].AgentID < result[j].AgentID })
	return result
}

func probeTargetIDsForScope(scope protocol.ProbeScope) []string {
	if scope.Kind != "targets" {
		return nil
	}
	seen := make(map[string]struct{}, len(scope.TargetAgentIDs))
	result := make([]string, 0, len(scope.TargetAgentIDs))
	for _, targetAgentID := range scope.TargetAgentIDs {
		targetAgentID = strings.TrimSpace(targetAgentID)
		if targetAgentID == "" {
			continue
		}
		if _, exists := seen[targetAgentID]; exists {
			continue
		}
		seen[targetAgentID] = struct{}{}
		result = append(result, targetAgentID)
	}
	sort.Strings(result)
	return result
}

func cloneProbeTarget(target ProbeTarget) ProbeTarget {
	target.Tags = append([]string(nil), target.Tags...)
	target.Addresses = append([]protocol.Address(nil), target.Addresses...)
	target.Capabilities = append([]string(nil), target.Capabilities...)
	return target
}

func cloneProbeScope(scope protocol.ProbeScope) protocol.ProbeScope {
	scope.TargetAgentIDs = append([]string(nil), scope.TargetAgentIDs...)
	return scope
}

func cloneProbeJob(job *probeJob) probeJob {
	copy := *job
	copy.source = cloneProbeTarget(job.source)
	copy.target = cloneProbeTarget(job.target)
	copy.manualIDs = make(map[string]struct{}, len(job.manualIDs))
	for id := range job.manualIDs {
		copy.manualIDs[id] = struct{}{}
	}
	return copy
}

func (s *Scheduler) cleanupProgress() {
	s.mu.Lock()
	s.cleanupProgressLocked(s.opts.Now())
	s.mu.Unlock()
}

func (s *Scheduler) cleanupProgressLocked(now time.Time) {
	for probeID, expiresAt := range s.progressExpiry {
		if now.After(expiresAt) {
			delete(s.progressExpiry, probeID)
			delete(s.progress, probeID)
		}
	}
}

func (s *Scheduler) trimHistoryLocked() {
	for len(s.history) > defaultProbeMaxJobs {
		var oldestKey probeJobKey
		var oldest time.Time
		first := true
		for key, history := range s.history {
			if first || history.nextAt.Before(oldest) {
				oldestKey, oldest, first = key, history.nextAt, false
			}
		}
		delete(s.history, oldestKey)
	}
}
