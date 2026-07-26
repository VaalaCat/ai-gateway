package billing

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

// Flush fn signatures match the dao.AdminBillingMutation batch methods.
// Injected via SetFlushFns so tests can use fakes; production wires the
// real dao calls in master/server.go.
type (
	TokensFlushFn               func(rows []dao.TokenDailyRow) error
	ChannelsFlushFn             func(rows []dao.ChannelDailyRow) error
	BillingHourlyFlushFn        func(rows []dao.BillingHourlyRow) error
	TokensFlushContextFn        func(context.Context, []dao.TokenDailyRow) error
	ChannelsFlushContextFn      func(context.Context, []dao.ChannelDailyRow) error
	BillingHourlyFlushContextFn func(context.Context, []dao.BillingHourlyRow) error
	CoreFlushContextFn          func(context.Context, []dao.TokenDailyRow, []dao.ChannelDailyRow, []dao.BillingHourlyRow) error
	ProjectionFlushContextFn    func(context.Context, []models.BillingLog) error
)

// tokenKey is the composite primary key of token_daily_billing rows.
type tokenKey struct {
	Date    string
	UserID  uint
	TokenID uint
}

// channelKey is the composite primary key of channel_daily_billing rows.
// BYOK rows (PrivateChannelID>0, ChannelID=0) and admin rows
// (ChannelID>0, PrivateChannelID=0) coexist via the (channel_id, private_channel_id)
// pair — both halves are part of the key.
type channelKey struct {
	Date             string
	ChannelID        uint
	PrivateChannelID uint
}

// billingHourlyKey is the complete unique key of billing_hourly_buckets.
type billingHourlyKey struct {
	Date             string
	Hour             int
	UserID           uint
	TokenID          uint
	ChannelID        uint
	PrivateChannelID uint
	OwnerType        string
	ModelName        string
}

type tokenDelta struct {
	TokenName        string
	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	TotalCost        int64
	LastUsedAt       int64
	UpdatedAt        int64
}

type channelDelta struct {
	ChannelName      string
	ChannelType      int
	OwnerType        string
	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	TotalCost        int64
	RawCost          int64
	LastUsedAt       int64
	UpdatedAt        int64
}

type billingHourlyDelta struct {
	TokenName   string
	ChannelName string
	ChannelType int

	RequestCount     int64
	SuccessCount     int64
	FailedCount      int64
	PromptTokens     int64
	CompletionTokens int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	InputCost        int64
	OutputCost       int64
	CacheReadCost    int64
	CacheWriteCost   int64
	TotalCost        int64
	RawCost          int64

	LastUsedAt int64
	UpdatedAt  int64
}

// BillingAggregateBatch is the shared, strongly typed projection output for
// committed billing facts. Both live aggregation flushes and history replay
// persist these rows with the same dimension and success semantics.
type BillingAggregateBatch struct {
	Tokens   []dao.TokenDailyRow
	Channels []dao.ChannelDailyRow
	Hourly   []dao.BillingHourlyRow
}

func BuildBillingAggregateBatch(logs []models.BillingLog) BillingAggregateBatch {
	tokens := make(map[tokenKey]*tokenDelta)
	channels := make(map[channelKey]*channelDelta)
	hourly := make(map[billingHourlyKey]*billingHourlyDelta)
	for index := range logs {
		log := &logs[index]
		date := billingFactDate(log)
		ts := billingFactTimestamp(log)
		ownerType := log.OwnerType
		if ownerType == "" {
			ownerType = "admin"
		}
		successCount, failedCount := aggSuccessFailureCounts(log.Status)
		accumulateTokenDelta(tokens, log, date, ts, successCount, failedCount)
		accumulateChannelDelta(channels, log, date, ts, ownerType, successCount, failedCount)
		accumulateBillingHourlyDelta(hourly, log, date, ts, ownerType, successCount, failedCount)
	}
	return buildBillingAggregateBatch(tokens, channels, hourly)
}

// AggregatorOptions configures the background flush cadence.
// FlushEvery <= 0 disables the ticker (callers must Flush manually or rely
// on Stop's force-flush). MaxRows > 0 enables proactive flush when the
// buffer reaches that many distinct keys across all 3 maps combined.
type AggregatorOptions struct {
	FlushEvery time.Duration
	MaxRows    int
}

type rebuildSliceKey struct {
	Date string
	Hour int
}

type dailyBuffers struct {
	tokens   map[tokenKey]*tokenDelta
	channels map[channelKey]*channelDelta
}

type hourlyBuffers struct {
	hourly map[billingHourlyKey]*billingHourlyDelta
}

type activeDateDailyRebuild struct {
	watermark        uint
	watermarkReady   bool
	pendingCommitted dailyBuffers
	pendingUnknown   dailyBuffers
	covered          dailyBuffers
	deferred         dailyBuffers
}

type activeHourRebuild struct {
	watermark uint
	covered   hourlyBuffers
	deferred  hourlyBuffers
}

func newDailyBuffers() dailyBuffers {
	return dailyBuffers{tokens: make(map[tokenKey]*tokenDelta), channels: make(map[channelKey]*channelDelta)}
}

func newHourlyBuffers() hourlyBuffers {
	return hourlyBuffers{hourly: make(map[billingHourlyKey]*billingHourlyDelta)}
}

// Aggregator buffers per-key deltas in memory and flushes them in
// batched UPSERTs. Designed to be called from settler AFTER its
// transaction commits (so a rollback can't leave the aggregator with
// orphan counts) — see Submit doc.
type Aggregator struct {
	admission          sync.RWMutex
	runGatesMu         sync.Mutex
	dateRunGates       map[string]chan struct{}
	hourRunGates       map[rebuildSliceKey]chan struct{}
	mu                 sync.Mutex
	tokens             map[tokenKey]*tokenDelta
	channels           map[channelKey]*channelDelta
	hourly             map[billingHourlyKey]*billingHourlyDelta
	projectionFacts    map[string]models.BillingLog
	deferredFacts      map[string]models.BillingLog
	activeDateDaily    map[string]*activeDateDailyRebuild
	activeHours        map[rebuildSliceKey]*activeHourRebuild
	completedDateDaily map[string]uint
	completedHours     map[rebuildSliceKey]uint

	flushEvery time.Duration
	maxRows    int

	app          dao.AppProvider
	logger       *zap.Logger
	stopCh       chan struct{}
	lifecycleMu  sync.Mutex
	started      bool
	closing      bool
	closeOnce    sync.Once
	closeErrMu   sync.Mutex
	closeErr     error
	workerCancel context.CancelCauseFunc
	workers      conc.WaitGroup
	done         chan struct{}
	flushCh      chan struct{}

	tokensFn      TokensFlushContextFn
	channelsFn    ChannelsFlushContextFn
	hourlyFn      BillingHourlyFlushContextFn
	coreFn        CoreFlushContextFn
	projectionFn  ProjectionFlushContextFn
	activeWorkers atomic.Int64
	activeTimers  atomic.Int64
	inflight      atomic.Int64
}

// NewAggregator constructs an aggregator. app may be nil for pure-memory
// tests. Start must be called separately to begin background flush; nil
// logger is allowed (no log lines).
func NewAggregator(app dao.AppProvider, logger *zap.Logger, opt AggregatorOptions) *Aggregator {
	return &Aggregator{
		dateRunGates:       make(map[string]chan struct{}),
		hourRunGates:       make(map[rebuildSliceKey]chan struct{}),
		tokens:             make(map[tokenKey]*tokenDelta),
		channels:           make(map[channelKey]*channelDelta),
		hourly:             make(map[billingHourlyKey]*billingHourlyDelta),
		projectionFacts:    make(map[string]models.BillingLog),
		deferredFacts:      make(map[string]models.BillingLog),
		activeDateDaily:    make(map[string]*activeDateDailyRebuild),
		activeHours:        make(map[rebuildSliceKey]*activeHourRebuild),
		completedDateDaily: make(map[string]uint),
		completedHours:     make(map[rebuildSliceKey]uint),
		flushEvery:         opt.FlushEvery,
		maxRows:            opt.MaxRows,
		app:                app,
		logger:             logger,
		stopCh:             make(chan struct{}),
		done:               make(chan struct{}),
		flushCh:            make(chan struct{}, 1),
	}
}

// SubmitBilling accumulates a committed billing fact into core rollups. It
// must only be called after the BillingLog + quota transaction commits.
func (a *Aggregator) SubmitBilling(log *models.BillingLog) {
	if log == nil {
		return
	}
	a.admission.RLock()
	defer a.admission.RUnlock()
	a.submitBillingWithoutAdmission(log)
}

// SubmitPendingBilling admits a fact owned by a durable pending receipt. If a
// daily or hourly rebuild owns either projection dimension, the complete fact
// is deferred until both dimensions are released.
func (a *Aggregator) SubmitPendingBilling(log *models.BillingLog) {
	if log == nil {
		return
	}
	a.admission.RLock()
	defer a.admission.RUnlock()
	a.submitPendingBillingWithoutAdmission(log)
}

func (a *Aggregator) submitPendingBillingWithoutAdmission(log *models.BillingLog) {
	if log == nil || log.RequestID == "" {
		return
	}
	date := billingFactDate(log)
	ts := billingFactTimestamp(log)
	key := rebuildSliceKey{Date: date, Hour: time.Unix(ts, 0).UTC().Hour()}
	successCount, failedCount := aggSuccessFailureCounts(log.Status)
	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}
	a.mu.Lock()
	if _, exists := a.projectionFacts[log.RequestID]; exists {
		a.mu.Unlock()
		return
	}
	if _, exists := a.deferredFacts[log.RequestID]; exists {
		a.mu.Unlock()
		return
	}
	if a.activeDateDaily[date] != nil || a.activeHours[key] != nil {
		a.deferredFacts[log.RequestID] = *log
		a.mu.Unlock()
		return
	}
	a.accumulatePendingFactInMainLocked(log, date, key, ts, ownerType, successCount, failedCount)
	total := len(a.tokens) + len(a.channels) + len(a.hourly)
	a.mu.Unlock()
	a.signalFlushThreshold(total)
}

func (a *Aggregator) accumulatePendingFactInMainLocked(log *models.BillingLog, date string, key rebuildSliceKey, ts int64, ownerType string, successCount, failedCount int64) {
	accumulateTokenDelta(a.tokens, log, date, ts, successCount, failedCount)
	accumulateChannelDelta(a.channels, log, date, ts, ownerType, successCount, failedCount)
	accumulateBillingHourlyDelta(a.hourly, log, key.Date, ts, ownerType, successCount, failedCount)
	a.projectionFacts[log.RequestID] = *log
}

func (a *Aggregator) submitBillingWithoutAdmission(log *models.BillingLog) {
	if log == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && a.logger != nil {
			a.logger.Error("aggregator_submit_panic", zap.Any("recover", r))
		}
	}()

	date := billingFactDate(log)
	ts := billingFactTimestamp(log)
	key := rebuildSliceKey{Date: date, Hour: time.Unix(ts, 0).UTC().Hour()}
	successCount, failedCount := aggSuccessFailureCounts(log.Status)
	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}
	a.mu.Lock()
	if log.RequestID != "" {
		if _, exists := a.projectionFacts[log.RequestID]; exists {
			a.mu.Unlock()
			return
		}
	}
	dailyInMain := a.submitDailyLocked(log, date, ts, ownerType, successCount, failedCount)
	hourlyInMain := a.submitHourlyLocked(log, key, ts, ownerType, successCount, failedCount)
	if dailyInMain && hourlyInMain && log.RequestID != "" {
		a.projectionFacts[log.RequestID] = *log
	}
	total := len(a.tokens) + len(a.channels) + len(a.hourly)
	a.mu.Unlock()

	// maxRows trigger: coalesced non-blocking send (signal-only; the
	// background goroutine performs the actual Flush). a.maxRows is set
	// once in NewAggregator and never mutated, so reading without lock
	// is safe.
	if (dailyInMain || hourlyInMain) && a.maxRows > 0 && total >= a.maxRows {
		select {
		case a.flushCh <- struct{}{}:
		default:
		}
	}
}

func (a *Aggregator) submitDailyLocked(log *models.BillingLog, date string, ts int64, ownerType string, successCount, failedCount int64) bool {
	if completed, ok := a.completedDateDaily[date]; ok && log.ID > 0 && log.ID <= completed {
		return false
	}
	target := dailyBuffers{tokens: a.tokens, channels: a.channels}
	inMain := true
	if active := a.activeDateDaily[date]; active != nil {
		inMain = false
		target = activeDateDailyTarget(active, log.ID)
	}
	accumulateTokenDelta(target.tokens, log, date, ts, successCount, failedCount)
	accumulateChannelDelta(target.channels, log, date, ts, ownerType, successCount, failedCount)
	return inMain
}

func activeDateDailyTarget(active *activeDateDailyRebuild, billingLogID uint) dailyBuffers {
	if !active.watermarkReady {
		if billingLogID > 0 {
			return active.pendingCommitted
		}
		return active.pendingUnknown
	}
	if billingLogID > 0 && billingLogID <= active.watermark {
		return active.covered
	}
	return active.deferred
}

func (a *Aggregator) submitHourlyLocked(log *models.BillingLog, key rebuildSliceKey, ts int64, ownerType string, successCount, failedCount int64) bool {
	if completed, ok := a.completedHours[key]; ok && log.ID > 0 && log.ID <= completed {
		return false
	}
	target := hourlyBuffers{hourly: a.hourly}
	inMain := true
	if active := a.activeHours[key]; active != nil {
		inMain = false
		if log.ID > 0 && log.ID <= active.watermark {
			target = active.covered
		} else {
			target = active.deferred
		}
	}
	accumulateBillingHourlyDelta(target.hourly, log, key.Date, ts, ownerType, successCount, failedCount)
	return inMain
}

func accumulateTokenDelta(target map[tokenKey]*tokenDelta, log *models.BillingLog, date string, ts, successCount, failedCount int64) {
	tk := tokenKey{Date: date, UserID: log.UserID, TokenID: log.TokenID}
	td := target[tk]
	if td == nil {
		td = &tokenDelta{}
		target[tk] = td
	}
	if ts >= td.UpdatedAt {
		td.TokenName, td.UpdatedAt = log.TokenName, ts
	}
	td.RequestCount++
	td.SuccessCount += successCount
	td.FailedCount += failedCount
	td.PromptTokens += int64(log.PromptTokens)
	td.CompletionTokens += int64(log.CompletionTokens)
	td.CacheReadTokens += int64(log.CacheReadTokens)
	td.CacheWriteTokens += int64(log.CacheWriteTokens)
	td.InputCost += log.InputCost
	td.OutputCost += log.OutputCost
	td.TotalCost += log.TotalCost
	if ts > td.LastUsedAt {
		td.LastUsedAt = ts
	}
}

func accumulateChannelDelta(target map[channelKey]*channelDelta, log *models.BillingLog, date string, ts int64, ownerType string, successCount, failedCount int64) {
	ck := channelKey{Date: date, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID}
	cd := target[ck]
	if cd == nil {
		cd = &channelDelta{}
		target[ck] = cd
	}
	if ts >= cd.UpdatedAt {
		cd.ChannelName, cd.ChannelType, cd.OwnerType, cd.UpdatedAt = log.ChannelName, log.ChannelType, ownerType, ts
	}
	cd.RequestCount++
	cd.SuccessCount += successCount
	cd.FailedCount += failedCount
	cd.PromptTokens += int64(log.PromptTokens)
	cd.CompletionTokens += int64(log.CompletionTokens)
	cd.CacheReadTokens += int64(log.CacheReadTokens)
	cd.CacheWriteTokens += int64(log.CacheWriteTokens)
	cd.InputCost += log.InputCost
	cd.OutputCost += log.OutputCost
	cd.TotalCost += log.TotalCost
	cd.RawCost += log.RawTotal()
	if ts > cd.LastUsedAt {
		cd.LastUsedAt = ts
	}
}

func accumulateBillingHourlyDelta(target map[billingHourlyKey]*billingHourlyDelta, log *models.BillingLog, date string, ts int64, ownerType string, successCount, failedCount int64) {
	hk := billingHourlyKey{
		Date: date, Hour: time.Unix(ts, 0).UTC().Hour(), UserID: log.UserID, TokenID: log.TokenID,
		ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID, OwnerType: ownerType, ModelName: log.ModelName,
	}
	hd := target[hk]
	if hd == nil {
		hd = &billingHourlyDelta{}
		target[hk] = hd
	}
	if ts >= hd.UpdatedAt {
		hd.TokenName, hd.ChannelName, hd.ChannelType, hd.UpdatedAt = log.TokenName, log.ChannelName, log.ChannelType, ts
	}
	hd.RequestCount++
	hd.SuccessCount += successCount
	hd.FailedCount += failedCount
	hd.PromptTokens += int64(log.PromptTokens)
	hd.CompletionTokens += int64(log.CompletionTokens)
	hd.CacheReadTokens += int64(log.CacheReadTokens)
	hd.CacheWriteTokens += int64(log.CacheWriteTokens)
	hd.InputCost += log.InputCost
	hd.OutputCost += log.OutputCost
	hd.CacheReadCost += log.CacheReadCost
	hd.CacheWriteCost += log.CacheWriteCost
	hd.TotalCost += log.TotalCost
	hd.RawCost += log.RawTotal()
	if ts > hd.LastUsedAt {
		hd.LastUsedAt = ts
	}
}

// Snapshot returns shallow copies of the three delta maps. Test-only.
func (a *Aggregator) Snapshot() (map[tokenKey]*tokenDelta, map[channelKey]*channelDelta, map[billingHourlyKey]*billingHourlyDelta) {
	a.mu.Lock()
	defer a.mu.Unlock()
	tk := make(map[tokenKey]*tokenDelta, len(a.tokens))
	for k, v := range a.tokens {
		cp := *v
		tk[k] = &cp
	}
	ck := make(map[channelKey]*channelDelta, len(a.channels))
	for k, v := range a.channels {
		cp := *v
		ck[k] = &cp
	}
	hk := make(map[billingHourlyKey]*billingHourlyDelta, len(a.hourly))
	for k, v := range a.hourly {
		cp := *v
		hk[k] = &cp
	}
	return tk, ck, hk
}

func billingFactTimestamp(log *models.BillingLog) int64 {
	if log.CreatedAt > 0 {
		return log.CreatedAt
	}
	return time.Now().Unix()
}

func billingFactDate(log *models.BillingLog) string {
	return time.Unix(billingFactTimestamp(log), 0).UTC().Format("2006-01-02")
}

func aggSuccessFailureCounts(status int) (int64, int64) {
	if status == 0 {
		return 0, 1
	}
	return 1, 0
}

// SetFlushFns installs the per-table batch persist functions. Pass nil
// individually to disable that table's flush (e.g. for partial mock tests).
func (a *Aggregator) SetFlushFns(t TokensFlushFn, c ChannelsFlushFn, h BillingHourlyFlushFn) {
	var tc TokensFlushContextFn
	var cc ChannelsFlushContextFn
	var hc BillingHourlyFlushContextFn
	if t != nil {
		tc = func(_ context.Context, rows []dao.TokenDailyRow) error { return t(rows) }
	}
	if c != nil {
		cc = func(_ context.Context, rows []dao.ChannelDailyRow) error { return c(rows) }
	}
	if h != nil {
		hc = func(_ context.Context, rows []dao.BillingHourlyRow) error { return h(rows) }
	}
	a.SetFlushContextFns(tc, cc, hc)
}

func (a *Aggregator) SetFlushContextFns(t TokensFlushContextFn, c ChannelsFlushContextFn, h BillingHourlyFlushContextFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokensFn = t
	a.channelsFn = c
	a.hourlyFn = h
}

// SetCoreFlushContextFn installs the atomic three-table core persistence
// operation used by split billing mode and online rebuild admission.
func (a *Aggregator) SetCoreFlushContextFn(fn CoreFlushContextFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.coreFn = fn
}

// SetProjectionFlushContextFn installs the durable fact-aware flush used in
// production. It allows persistence to exclude request IDs that already have
// receipts after a concurrent history replay.
func (a *Aggregator) SetProjectionFlushContextFn(fn ProjectionFlushContextFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.projectionFn = fn
}

// Flush snapshots the in-memory buffers, resets them, and persists the rows.
// Atomic core flush failures restore the snapshot. The transitional per-table
// test/legacy path retains its old clear-on-error behavior because a partial
// write cannot be retried without double-applying rows.
//
// Returns the FIRST error encountered (token > channel > billing-hourly
// order); subsequent errors are logged but not returned (avoid masking root
// cause). Empty buffers short-circuit without calling any fn.
func (a *Aggregator) Flush() error {
	return a.FlushContext(context.Background())
}

func (a *Aggregator) FlushContext(ctx context.Context) error {
	a.admission.Lock()
	defer a.admission.Unlock()
	return a.flushWithoutAdmission(ctx)
}

func (a *Aggregator) flushWithoutAdmission(ctx context.Context) error {
	if _, err := a.recoverPendingFactsWithoutAdmission(ctx); err != nil {
		return err
	}
	return a.flushBufferedWithoutAdmission(ctx)
}

func (a *Aggregator) flushBufferedWithoutAdmission(ctx context.Context) error {
	a.mu.Lock()
	if len(a.tokens) == 0 && len(a.channels) == 0 && len(a.hourly) == 0 && len(a.projectionFacts) == 0 {
		a.mu.Unlock()
		return nil
	}

	batch := buildBillingAggregateBatch(a.tokens, a.channels, a.hourly)

	// Retain the old maps so an atomic core flush can restore the exact
	// snapshot on failure while admission blocks concurrent Submit calls.
	tokenSnapshot, channelSnapshot, hourlySnapshot, factSnapshot := a.tokens, a.channels, a.hourly, a.projectionFacts
	a.tokens = make(map[tokenKey]*tokenDelta)
	a.channels = make(map[channelKey]*channelDelta)
	a.hourly = make(map[billingHourlyKey]*billingHourlyDelta)
	a.projectionFacts = make(map[string]models.BillingLog)
	tFn, cFn, hFn, coreFn, projectionFn := a.tokensFn, a.channelsFn, a.hourlyFn, a.coreFn, a.projectionFn
	a.mu.Unlock()

	if projectionFn != nil {
		facts := make([]models.BillingLog, 0, len(factSnapshot))
		for _, fact := range factSnapshot {
			facts = append(facts, fact)
		}
		var err error
		if len(facts) == 0 {
			err = errors.New("aggregator projection flush has rows without request identities")
		} else {
			a.inflight.Add(1)
			err = projectionFn(ctx, facts)
			a.inflight.Add(-1)
		}
		if err != nil {
			a.restoreCoreFlushSnapshot(tokenSnapshot, channelSnapshot, hourlySnapshot, factSnapshot)
			if a.logger != nil {
				a.logger.Warn("aggregator_flush_failed", zap.String("table", "core_billing_projection"), zap.Error(err))
			}
		}
		return err
	}

	if coreFn != nil {
		a.inflight.Add(1)
		err := coreFn(ctx, batch.Tokens, batch.Channels, batch.Hourly)
		a.inflight.Add(-1)
		if err != nil {
			a.restoreCoreFlushSnapshot(tokenSnapshot, channelSnapshot, hourlySnapshot, factSnapshot)
			if a.logger != nil {
				a.logger.Warn("aggregator_flush_failed", zap.String("table", "core_billing"), zap.Error(err))
			}
		}
		return err
	}

	var firstErr error
	logIfErr := func(label string, err error, rowsCount int) {
		if err == nil {
			return
		}
		if a.logger != nil {
			a.logger.Warn("aggregator_flush_failed",
				zap.String("table", label), zap.Int("rows", rowsCount), zap.Error(err))
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if tFn != nil {
		a.inflight.Add(1)
		err := tFn(ctx, batch.Tokens)
		a.inflight.Add(-1)
		logIfErr("token_daily", err, len(batch.Tokens))
	}
	if cFn != nil {
		a.inflight.Add(1)
		err := cFn(ctx, batch.Channels)
		a.inflight.Add(-1)
		logIfErr("channel_daily", err, len(batch.Channels))
	}
	if hFn != nil {
		a.inflight.Add(1)
		err := hFn(ctx, batch.Hourly)
		a.inflight.Add(-1)
		logIfErr("billing_hourly", err, len(batch.Hourly))
	}
	return firstErr
}

func (a *Aggregator) recoverPendingFactsWithoutAdmission(ctx context.Context) (int, error) {
	a.mu.Lock()
	configured := a.projectionFn != nil
	a.mu.Unlock()
	if !configured || a.app == nil || a.app.GetCoreDB() == nil {
		return 0, nil
	}
	facts, err := LoadPendingBillingFacts(ctx, a.app.GetCoreDB(), pendingProjectionBatchSize)
	if err != nil {
		return 0, fmt.Errorf("recover pending billing projections: %w", err)
	}
	for i := range facts {
		a.submitPendingBillingWithoutAdmission(&facts[i])
	}
	return len(facts), nil
}

func (a *Aggregator) drainPendingContext(ctx context.Context) error {
	a.mu.Lock()
	configured := a.projectionFn != nil
	a.mu.Unlock()
	if !configured || a.app == nil || a.app.GetCoreDB() == nil {
		return a.FlushContext(ctx)
	}
	a.admission.Lock()
	defer a.admission.Unlock()
	return a.drainPendingWithoutAdmission(ctx)
}

func (a *Aggregator) drainPendingWithoutAdmission(ctx context.Context) error {
	var previousFirstID uint
	for {
		facts, err := LoadPendingBillingFacts(ctx, a.app.GetCoreDB(), pendingProjectionBatchSize)
		if err != nil {
			return fmt.Errorf("drain pending billing projections: %w", err)
		}
		if len(facts) == 0 {
			return a.flushBufferedWithoutAdmission(ctx)
		}
		if previousFirstID != 0 && facts[0].ID == previousFirstID {
			return fmt.Errorf("drain pending billing projections: no progress at billing_log_id %d", previousFirstID)
		}
		previousFirstID = facts[0].ID
		for i := range facts {
			a.submitPendingBillingWithoutAdmission(&facts[i])
		}
		if err := a.flushBufferedWithoutAdmission(ctx); err != nil {
			return err
		}
	}
}

func (a *Aggregator) restoreCoreFlushSnapshot(
	tokens map[tokenKey]*tokenDelta,
	channels map[channelKey]*channelDelta,
	hourly map[billingHourlyKey]*billingHourlyDelta,
	facts map[string]models.BillingLog,
) {
	a.mu.Lock()
	a.tokens, a.channels, a.hourly, a.projectionFacts = tokens, channels, hourly, facts
	a.mu.Unlock()
}

func buildBillingAggregateBatch(
	tokens map[tokenKey]*tokenDelta,
	channels map[channelKey]*channelDelta,
	hourly map[billingHourlyKey]*billingHourlyDelta,
) BillingAggregateBatch {
	batch := BillingAggregateBatch{
		Tokens:   make([]dao.TokenDailyRow, 0, len(tokens)),
		Channels: make([]dao.ChannelDailyRow, 0, len(channels)),
		Hourly:   make([]dao.BillingHourlyRow, 0, len(hourly)),
	}
	for k, v := range tokens {
		batch.Tokens = append(batch.Tokens, dao.TokenDailyRow{
			Date: k.Date, UserID: k.UserID, TokenID: k.TokenID, TokenName: v.TokenName,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost, TotalCost: v.TotalCost,
			LastUsedAt: v.LastUsedAt, UpdatedAt: v.UpdatedAt,
		})
	}
	for k, v := range channels {
		batch.Channels = append(batch.Channels, dao.ChannelDailyRow{
			Date: k.Date, ChannelID: k.ChannelID, PrivateChannelID: k.PrivateChannelID,
			ChannelName: v.ChannelName, ChannelType: v.ChannelType, OwnerType: v.OwnerType,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost, TotalCost: v.TotalCost,
			RawCost:    v.RawCost,
			LastUsedAt: v.LastUsedAt, UpdatedAt: v.UpdatedAt,
		})
	}
	for k, v := range hourly {
		batch.Hourly = append(batch.Hourly, dao.BillingHourlyRow{
			Date: k.Date, Hour: k.Hour,
			UserID: k.UserID, TokenID: k.TokenID,
			ChannelID: k.ChannelID, PrivateChannelID: k.PrivateChannelID,
			OwnerType: k.OwnerType, ModelName: k.ModelName,
			TokenName: v.TokenName, ChannelName: v.ChannelName, ChannelType: v.ChannelType,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost,
			CacheReadCost: v.CacheReadCost, CacheWriteCost: v.CacheWriteCost,
			TotalCost: v.TotalCost, RawCost: v.RawCost,
			LastUsedAt: v.LastUsedAt, UpdatedAt: v.UpdatedAt,
		})
	}
	return batch
}

// RunCoreDateRebuild serializes complete rebuilds for the same date while
// allowing other dates and all Submit calls to proceed. When the selected
// targets exclude daily projections, it provides only the date-level run gate.
func (a *Aggregator) RunCoreDateRebuild(ctx context.Context, date string, rebuildDailyProjection bool, rebuildHours func() error, rebuildDaily func(maxBillingLogID uint) error) error {
	if err := a.validateCoreDateRebuild(ctx, date, rebuildDailyProjection, rebuildHours, rebuildDaily); err != nil {
		return err
	}
	release, err := a.acquireDateRun(ctx, date)
	if err != nil {
		return err
	}
	defer release()
	if !rebuildDailyProjection {
		return rebuildHours()
	}
	if err := a.beginCoreDateRebuild(ctx, date); err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			a.completeCoreDateRebuild(date, 0, errors.New("date rebuild callback panicked"))
		}
	}()
	if err := rebuildHours(); err != nil {
		a.completeCoreDateRebuild(date, 0, err)
		completed = true
		return err
	}
	watermark, err := a.publishCoreDailyWatermark(ctx, date)
	if err == nil {
		err = rebuildDaily(watermark) // Runs without admission held.
	}
	a.completeCoreDateRebuild(date, watermark, err)
	completed = true
	return err
}

// RunCoreHourRebuildSlice coordinates one hourly delete+replay transaction.
// Its callback runs without admission held.
func (a *Aggregator) RunCoreHourRebuildSlice(ctx context.Context, date string, hour int, rebuild func(maxBillingLogID uint) error) error {
	if err := a.validateCoreHourRebuild(ctx, date, hour, rebuild); err != nil {
		return err
	}
	key := rebuildSliceKey{Date: date, Hour: hour}
	release, err := a.acquireHourRun(ctx, key)
	if err != nil {
		return err
	}
	defer release()
	watermark, err := a.beginCoreHourRebuild(ctx, key)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			a.completeCoreHourRebuild(key, watermark, errors.New("hour rebuild callback panicked"))
		}
	}()
	rebuildErr := rebuild(watermark)
	a.completeCoreHourRebuild(key, watermark, rebuildErr)
	completed = true
	return rebuildErr
}

func (a *Aggregator) validateCoreDateRebuild(ctx context.Context, date string, rebuildDailyProjection bool, rebuildHours func() error, rebuildDaily func(uint) error) error {
	if err := a.validateCoreRebuildCommon(ctx, date); err != nil {
		return err
	}
	if rebuildHours == nil || (rebuildDailyProjection && rebuildDaily == nil) {
		return errors.New("core date rebuild: nil callback")
	}
	return nil
}

func (a *Aggregator) validateCoreHourRebuild(ctx context.Context, date string, hour int, rebuild func(uint) error) error {
	if err := a.validateCoreRebuildCommon(ctx, date); err != nil {
		return err
	}
	if rebuild == nil || hour < 0 || hour > 23 {
		return fmt.Errorf("core hour rebuild: invalid hour %d or nil callback", hour)
	}
	return nil
}

func (a *Aggregator) validateCoreRebuildCommon(ctx context.Context, date string) error {
	if ctx == nil {
		return errors.New("core rebuild: nil context")
	}
	if a.app == nil || a.app.GetCoreDB() == nil {
		return errors.New("core rebuild: core database is unavailable")
	}
	if parsed, err := time.Parse("2006-01-02", date); err != nil || parsed.Format("2006-01-02") != date {
		return fmt.Errorf("core rebuild: invalid date %q", date)
	}
	return nil
}

func (a *Aggregator) beginCoreDateRebuild(ctx context.Context, date string) error {
	a.admission.Lock()
	defer a.admission.Unlock()
	if err := a.requireCoreFlush(); err != nil {
		return err
	}
	if err := a.flushWithoutAdmission(ctx); err != nil {
		return fmt.Errorf("core date rebuild flush: %w", err)
	}
	a.mu.Lock()
	a.activeDateDaily[date] = &activeDateDailyRebuild{
		pendingCommitted: newDailyBuffers(), pendingUnknown: newDailyBuffers(),
		covered: newDailyBuffers(), deferred: newDailyBuffers(),
	}
	a.mu.Unlock()
	return nil
}

func (a *Aggregator) beginCoreHourRebuild(ctx context.Context, key rebuildSliceKey) (uint, error) {
	a.admission.Lock()
	defer a.admission.Unlock()
	if err := a.requireCoreFlush(); err != nil {
		return 0, err
	}
	if err := a.flushWithoutAdmission(ctx); err != nil {
		return 0, fmt.Errorf("core hour rebuild flush: %w", err)
	}
	watermark, err := a.readBillingLogWatermark(ctx)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	a.activeHours[key] = &activeHourRebuild{watermark: watermark, covered: newHourlyBuffers(), deferred: newHourlyBuffers()}
	a.mu.Unlock()
	return watermark, nil
}

func (a *Aggregator) publishCoreDailyWatermark(ctx context.Context, date string) (uint, error) {
	a.admission.Lock()
	defer a.admission.Unlock()
	watermark, err := a.readBillingLogWatermark(ctx)
	if err != nil {
		return 0, err
	}
	a.mu.Lock()
	active := a.activeDateDaily[date]
	active.watermark, active.watermarkReady = watermark, true
	mergeDailyBuffers(active.covered, active.pendingCommitted)
	mergeDailyBuffers(active.deferred, active.pendingUnknown)
	active.pendingCommitted, active.pendingUnknown = newDailyBuffers(), newDailyBuffers()
	a.mu.Unlock()
	return watermark, nil
}

func (a *Aggregator) completeCoreDateRebuild(date string, watermark uint, rebuildErr error) {
	a.admission.Lock()
	defer a.admission.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	active := a.activeDateDaily[date]
	main := dailyBuffers{tokens: a.tokens, channels: a.channels}
	if rebuildErr == nil {
		mergeDailyBuffers(main, active.deferred)
		a.completedDateDaily[date] = watermark
	} else {
		mergeDailyBuffers(main, active.pendingCommitted)
		mergeDailyBuffers(main, active.pendingUnknown)
		mergeDailyBuffers(main, active.covered)
		mergeDailyBuffers(main, active.deferred)
	}
	delete(a.activeDateDaily, date)
	a.releaseDeferredFactsLocked()
	a.signalFlushThreshold(len(a.tokens) + len(a.channels) + len(a.hourly))
}

func (a *Aggregator) completeCoreHourRebuild(key rebuildSliceKey, watermark uint, rebuildErr error) {
	a.admission.Lock()
	defer a.admission.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	active := a.activeHours[key]
	main := hourlyBuffers{hourly: a.hourly}
	if rebuildErr == nil {
		mergeHourlyBuffers(main, active.deferred)
		a.completedHours[key] = watermark
	} else {
		mergeHourlyBuffers(main, active.covered)
		mergeHourlyBuffers(main, active.deferred)
	}
	delete(a.activeHours, key)
	a.releaseDeferredFactsLocked()
	a.signalFlushThreshold(len(a.tokens) + len(a.channels) + len(a.hourly))
}

func (a *Aggregator) releaseDeferredFactsLocked() {
	for requestID, fact := range a.deferredFacts {
		date := billingFactDate(&fact)
		ts := billingFactTimestamp(&fact)
		key := rebuildSliceKey{Date: date, Hour: time.Unix(ts, 0).UTC().Hour()}
		if a.activeDateDaily[date] != nil || a.activeHours[key] != nil {
			continue
		}
		ownerType := fact.OwnerType
		if ownerType == "" {
			ownerType = "admin"
		}
		successCount, failedCount := aggSuccessFailureCounts(fact.Status)
		a.accumulatePendingFactInMainLocked(&fact, date, key, ts, ownerType, successCount, failedCount)
		delete(a.deferredFacts, requestID)
	}
}

func (a *Aggregator) requireCoreFlush() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.coreFn == nil && a.projectionFn == nil {
		return errors.New("core rebuild: atomic core flush is not configured")
	}
	return nil
}

func (a *Aggregator) readBillingLogWatermark(ctx context.Context) (uint, error) {
	var watermark uint
	if err := a.app.GetCoreDB().WithContext(ctx).Model(&models.BillingLog{}).
		Select("COALESCE(MAX(id), 0)").Scan(&watermark).Error; err != nil {
		return 0, fmt.Errorf("core rebuild watermark: %w", err)
	}
	return watermark, nil
}

func (a *Aggregator) acquireDateRun(ctx context.Context, date string) (func(), error) {
	a.runGatesMu.Lock()
	gate := a.dateRunGates[date]
	if gate == nil {
		gate = make(chan struct{}, 1)
		a.dateRunGates[date] = gate
	}
	a.runGatesMu.Unlock()
	return acquireRunGate(ctx, gate)
}

func (a *Aggregator) acquireHourRun(ctx context.Context, key rebuildSliceKey) (func(), error) {
	a.runGatesMu.Lock()
	gate := a.hourRunGates[key]
	if gate == nil {
		gate = make(chan struct{}, 1)
		a.hourRunGates[key] = gate
	}
	a.runGatesMu.Unlock()
	return acquireRunGate(ctx, gate)
}

func acquireRunGate(ctx context.Context, gate chan struct{}) (func(), error) {
	select {
	case gate <- struct{}{}:
		return func() { <-gate }, nil
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

func (a *Aggregator) signalFlushThreshold(total int) {
	if a.maxRows <= 0 || total < a.maxRows {
		return
	}
	select {
	case a.flushCh <- struct{}{}:
	default:
	}
}

func mergeDailyBuffers(dst, src dailyBuffers) {
	for key, value := range src.tokens {
		target := dst.tokens[key]
		if target == nil {
			copy := *value
			dst.tokens[key] = &copy
			continue
		}
		mergeTokenDelta(target, value)
	}
	for key, value := range src.channels {
		target := dst.channels[key]
		if target == nil {
			copy := *value
			dst.channels[key] = &copy
			continue
		}
		mergeChannelDelta(target, value)
	}
}

func mergeHourlyBuffers(dst, src hourlyBuffers) {
	for key, value := range src.hourly {
		target := dst.hourly[key]
		if target == nil {
			copy := *value
			dst.hourly[key] = &copy
			continue
		}
		mergeBillingHourlyDelta(target, value)
	}
}

func mergeTokenDelta(dst, src *tokenDelta) {
	if src.UpdatedAt >= dst.UpdatedAt {
		dst.TokenName, dst.UpdatedAt = src.TokenName, src.UpdatedAt
	}
	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.FailedCount += src.FailedCount
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.InputCost += src.InputCost
	dst.OutputCost += src.OutputCost
	dst.TotalCost += src.TotalCost
	if src.LastUsedAt > dst.LastUsedAt {
		dst.LastUsedAt = src.LastUsedAt
	}
}

func mergeChannelDelta(dst, src *channelDelta) {
	if src.UpdatedAt >= dst.UpdatedAt {
		dst.ChannelName, dst.ChannelType, dst.OwnerType, dst.UpdatedAt = src.ChannelName, src.ChannelType, src.OwnerType, src.UpdatedAt
	}
	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.FailedCount += src.FailedCount
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.InputCost += src.InputCost
	dst.OutputCost += src.OutputCost
	dst.TotalCost += src.TotalCost
	dst.RawCost += src.RawCost
	if src.LastUsedAt > dst.LastUsedAt {
		dst.LastUsedAt = src.LastUsedAt
	}
}

func mergeBillingHourlyDelta(dst, src *billingHourlyDelta) {
	if src.UpdatedAt >= dst.UpdatedAt {
		dst.TokenName, dst.ChannelName, dst.ChannelType, dst.UpdatedAt = src.TokenName, src.ChannelName, src.ChannelType, src.UpdatedAt
	}
	dst.RequestCount += src.RequestCount
	dst.SuccessCount += src.SuccessCount
	dst.FailedCount += src.FailedCount
	dst.PromptTokens += src.PromptTokens
	dst.CompletionTokens += src.CompletionTokens
	dst.CacheReadTokens += src.CacheReadTokens
	dst.CacheWriteTokens += src.CacheWriteTokens
	dst.InputCost += src.InputCost
	dst.OutputCost += src.OutputCost
	dst.CacheReadCost += src.CacheReadCost
	dst.CacheWriteCost += src.CacheWriteCost
	dst.TotalCost += src.TotalCost
	dst.RawCost += src.RawCost
	if src.LastUsedAt > dst.LastUsedAt {
		dst.LastUsedAt = src.LastUsedAt
	}
}

// Start spawns the background flush goroutine. It fires Flush on:
//   - ticker tick every flushEvery (skipped if flushEvery <= 0)
//   - maxRows-triggered signal on flushCh (coalesced via cap-1 buffer)
//
// Exits cleanly on ctx.Done() or Stop(). Caller is responsible for
// invoking Stop on shutdown to drain the final batch.
func (a *Aggregator) Start(ctx context.Context) {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	if a.started || a.closing || ctx == nil {
		return
	}
	a.started = true
	workerCtx, cancel := context.WithCancelCause(ctx)
	a.workerCancel = cancel
	a.activeWorkers.Add(1)
	a.workers.Go(func() {
		defer a.activeWorkers.Add(-1)
		if err := a.recoverPendingAtStartup(workerCtx); err != nil && a.logger != nil {
			a.logger.Warn("aggregator_startup_recovery_failed", zap.Error(err))
		}
		var ticker *time.Ticker
		var tickC <-chan time.Time
		if a.flushEvery > 0 {
			a.activeTimers.Add(1)
			ticker = time.NewTicker(a.flushEvery)
			defer func() { ticker.Stop(); a.activeTimers.Add(-1) }()
			tickC = ticker.C
		}
		for {
			select {
			case <-tickC:
				if err := a.FlushContext(workerCtx); err != nil && a.logger != nil {
					a.logger.Warn("aggregator_flush_tick_failed", zap.Error(err))
				}
			case <-a.flushCh:
				if err := a.FlushContext(workerCtx); err != nil && a.logger != nil {
					a.logger.Warn("aggregator_flush_threshold_failed", zap.Error(err))
				}
			case <-workerCtx.Done():
				return
			case <-a.stopCh:
				return
			}
		}
	})
}

func (a *Aggregator) recoverPendingAtStartup(ctx context.Context) error {
	a.mu.Lock()
	configured := a.projectionFn != nil
	a.mu.Unlock()
	if !configured || a.app == nil || a.app.GetCoreDB() == nil {
		return nil
	}
	facts, err := LoadPendingBillingFacts(ctx, a.app.GetCoreDB(), pendingProjectionBatchSize)
	if err != nil {
		return fmt.Errorf("recover startup billing projections: %w", err)
	}
	if len(facts) == 0 {
		return nil
	}
	a.admission.Lock()
	defer a.admission.Unlock()
	return a.drainPendingWithoutAdmission(ctx)
}

// Stop signals the background goroutine to exit and performs one final
// force-flush to drain whatever is buffered. Safe to call concurrently
// and idempotent (sync.Once guards the channel close; the final Flush
// is a no-op when the buffer is already empty).
func (a *Aggregator) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("aggregator: nil close context")
	}
	a.closeOnce.Do(func() {
		a.lifecycleMu.Lock()
		a.closing = true
		if a.workerCancel != nil {
			a.workerCancel(context.Cause(ctx))
		}
		close(a.stopCh)
		a.lifecycleMu.Unlock()
		go func() {
			a.workers.Wait()
			flushErr := a.drainPendingContext(ctx)
			a.closeErrMu.Lock()
			a.closeErr = flushErr
			a.closeErrMu.Unlock()
			if flushErr != nil && a.logger != nil {
				a.logger.Warn("aggregator_flush_on_shutdown_failed", zap.Error(flushErr))
			}
			close(a.done)
		}()
	})
	select {
	case <-a.done:
		return a.finalCloseError()
	case <-ctx.Done():
		select {
		case <-a.done:
			return errors.Join(context.Cause(ctx), a.finalCloseError())
		default:
			return context.Cause(ctx)
		}
	}
}

func (a *Aggregator) finalCloseError() error {
	a.closeErrMu.Lock()
	defer a.closeErrMu.Unlock()
	return a.closeErr
}

func (a *Aggregator) Stop(ctx context.Context) error { return a.Close(ctx) }

func (a *Aggregator) Done() <-chan struct{} { return a.done }

func (a *Aggregator) ResourceCounts() app.ResourceCounts {
	return app.ResourceCounts{LifecycleWorkers: a.activeWorkers.Load(), Timers: a.activeTimers.Load(), Inflight: a.inflight.Load()}
}
