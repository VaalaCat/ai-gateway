package billing

import (
	"context"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

// Flush fn signatures match the dao.AdminBillingMutation batch methods.
// Injected via SetFlushFns so tests can use fakes; production wires the
// real dao calls in master/server.go.
type (
	TokensFlushFn   func(rows []dao.TokenDailyRow) error
	ChannelsFlushFn func(rows []dao.ChannelDailyRow) error
	HourlyFlushFn   func(rows []dao.HourlyBucketRow) error
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

// hourlyKey is the composite primary key of usage_hourly_bucket rows.
// Filled in T2.1 to allow the map allocation; the delta struct + accumulation
// rules land in T2.2.
type hourlyKey struct {
	Date             string
	Hour             int
	ChannelID        uint
	PrivateChannelID uint
	ModelName        string
	AgentID          string
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
	LastUsedAt       int64
	UpdatedAt        int64
}

// hourlyDelta mirrors UsageHourlyBucket counters. Stream-only metrics
// accumulate when IsStream && Status==1 && CompletionTokens>0; the five
// latency segments accumulate only on Status==1. See
// dao.adminBillingMutation.UpsertHourlyBucket for the source-of-truth
// conditional logic this struct shadows.
type hourlyDelta struct {
	ChannelName string
	ChannelType int
	OwnerType   string

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

	// 仅 IsStream && Status==1 && CompletionTokens>0 时累加
	StreamRequestCount        int64
	SumFirstResponseMs        int64
	SumGenerationMs           int64
	SumStreamCompletionTokens int64

	// 仅 Status==1 时累加
	SumInboundDecodeMs    int64
	SumUpstreamDispatchMs int64
	SumUpstreamDecodeMs   int64
	SumOutboundEncodeMs   int64
	SumClientEncodeMs     int64

	LastUsedAt int64
	UpdatedAt  int64
}

// AggregatorOptions configures the background flush cadence.
// FlushEvery <= 0 disables the ticker (callers must Flush manually or rely
// on Stop's force-flush). MaxRows > 0 enables proactive flush when the
// buffer reaches that many distinct keys across all 3 maps combined.
type AggregatorOptions struct {
	FlushEvery time.Duration
	MaxRows    int
}

// Aggregator buffers per-key deltas in memory and flushes them in
// batched UPSERTs. Designed to be called from settler AFTER its
// transaction commits (so a rollback can't leave the aggregator with
// orphan counts) — see Submit doc.
type Aggregator struct {
	mu       sync.Mutex
	tokens   map[tokenKey]*tokenDelta
	channels map[channelKey]*channelDelta
	hourly   map[hourlyKey]*hourlyDelta

	flushEvery time.Duration
	maxRows    int

	app      dao.AppProvider
	logger   *zap.Logger
	stopCh   chan struct{}
	stopOnce sync.Once
	flushCh  chan struct{}

	tokensFn   TokensFlushFn
	channelsFn ChannelsFlushFn
	hourlyFn   HourlyFlushFn
}

// NewAggregator constructs an aggregator. app may be nil for pure-memory
// tests. Start must be called separately to begin background flush; nil
// logger is allowed (no log lines).
func NewAggregator(app dao.AppProvider, logger *zap.Logger, opt AggregatorOptions) *Aggregator {
	return &Aggregator{
		tokens:     make(map[tokenKey]*tokenDelta),
		channels:   make(map[channelKey]*channelDelta),
		hourly:     make(map[hourlyKey]*hourlyDelta),
		flushEvery: opt.FlushEvery,
		maxRows:    opt.MaxRows,
		app:        app,
		logger:     logger,
		stopCh:     make(chan struct{}),
		flushCh:    make(chan struct{}, 1),
	}
}

// Submit accumulates one UsageLog into the in-memory tokens, channels
// and hourly bucket deltas under a single lock (so the three maps stay
// consistent with one another even under concurrent producers).
//
// MUST be called only after the settler's transaction commits — if the
// transaction rolls back but Submit ran, the aggregate would
// over-count vs. UsageLog. nil log is a no-op.
//
// Submit catches panics defensively (e.g. malformed UsageLog) and drops
// the single record rather than crashing settler. UsageLog itself was
// already persisted, so rebuild can recover any lost aggregation.
func (a *Aggregator) Submit(log *models.UsageLog) {
	if log == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil && a.logger != nil {
			a.logger.Error("aggregator_submit_panic", zap.Any("recover", r))
		}
	}()

	date := aggBillingDate(log)
	ts := aggBillingTimestamp(log)
	successCount, failedCount := aggSuccessFailureCounts(log.Status)
	ownerType := log.OwnerType
	if ownerType == "" {
		ownerType = "admin"
	}

	total := func() int {
		a.mu.Lock()
		defer a.mu.Unlock()

		tk := tokenKey{Date: date, UserID: log.UserID, TokenID: log.TokenID}
		td, ok := a.tokens[tk]
		if !ok {
			td = &tokenDelta{}
			a.tokens[tk] = td
		}
		td.TokenName = log.TokenName
		td.RequestCount += 1
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
		td.UpdatedAt = ts

		ck := channelKey{Date: date, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID}
		cd, ok := a.channels[ck]
		if !ok {
			cd = &channelDelta{}
			a.channels[ck] = cd
		}
		cd.ChannelName = log.ChannelName
		cd.ChannelType = log.ChannelType
		cd.OwnerType = ownerType
		cd.RequestCount += 1
		cd.SuccessCount += successCount
		cd.FailedCount += failedCount
		cd.PromptTokens += int64(log.PromptTokens)
		cd.CompletionTokens += int64(log.CompletionTokens)
		cd.CacheReadTokens += int64(log.CacheReadTokens)
		cd.CacheWriteTokens += int64(log.CacheWriteTokens)
		cd.InputCost += log.InputCost
		cd.OutputCost += log.OutputCost
		cd.TotalCost += log.TotalCost
		if ts > cd.LastUsedAt {
			cd.LastUsedAt = ts
		}
		cd.UpdatedAt = ts

		// hourly bucket
		hour := time.Unix(ts, 0).UTC().Hour()
		hk := hourlyKey{
			Date: date, Hour: hour,
			ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID,
			ModelName: log.ModelName, AgentID: log.AgentID,
		}
		hd, ok := a.hourly[hk]
		if !ok {
			hd = &hourlyDelta{}
			a.hourly[hk] = hd
		}
		hd.ChannelName = log.ChannelName
		hd.ChannelType = log.ChannelType
		hd.OwnerType = ownerType
		hd.RequestCount += 1
		hd.SuccessCount += successCount
		hd.FailedCount += failedCount
		hd.PromptTokens += int64(log.PromptTokens)
		hd.CompletionTokens += int64(log.CompletionTokens)
		hd.CacheReadTokens += int64(log.CacheReadTokens)
		hd.CacheWriteTokens += int64(log.CacheWriteTokens)
		hd.InputCost += log.InputCost
		hd.OutputCost += log.OutputCost
		hd.TotalCost += log.TotalCost

		if log.IsStream && log.Status == 1 && log.CompletionTokens > 0 {
			hd.StreamRequestCount += 1
			hd.SumFirstResponseMs += int64(log.FirstResponseMs)
			hd.SumGenerationMs += int64(log.Duration - log.FirstResponseMs)
			hd.SumStreamCompletionTokens += int64(log.CompletionTokens)
		}
		if log.Status == 1 {
			hd.SumInboundDecodeMs += int64(log.InboundDecodeMs)
			hd.SumUpstreamDispatchMs += int64(log.UpstreamDispatchMs)
			hd.SumUpstreamDecodeMs += int64(log.UpstreamDecodeMs)
			hd.SumOutboundEncodeMs += int64(log.OutboundEncodeMs)
			hd.SumClientEncodeMs += int64(log.ClientEncodeMs)
		}
		if ts > hd.LastUsedAt {
			hd.LastUsedAt = ts
		}
		hd.UpdatedAt = ts

		return len(a.tokens) + len(a.channels) + len(a.hourly)
	}()

	// maxRows trigger: coalesced non-blocking send (signal-only; the
	// background goroutine performs the actual Flush). a.maxRows is set
	// once in NewAggregator and never mutated, so reading without lock
	// is safe.
	if a.maxRows > 0 && total >= a.maxRows {
		select {
		case a.flushCh <- struct{}{}:
		default:
		}
	}
}

// Snapshot returns shallow copies of the three delta maps. Test-only.
func (a *Aggregator) Snapshot() (map[tokenKey]*tokenDelta, map[channelKey]*channelDelta, map[hourlyKey]*hourlyDelta) {
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
	hk := make(map[hourlyKey]*hourlyDelta, len(a.hourly))
	for k, v := range a.hourly {
		cp := *v
		hk[k] = &cp
	}
	return tk, ck, hk
}

// aggBillingTimestamp mirrors the unexported dao helper. We copy rather
// than export from dao to keep dao's surface area unchanged.
func aggBillingTimestamp(log *models.UsageLog) int64 {
	if log.CreatedAt > 0 {
		return log.CreatedAt
	}
	return time.Now().Unix()
}

func aggBillingDate(log *models.UsageLog) string {
	return time.Unix(aggBillingTimestamp(log), 0).UTC().Format("2006-01-02")
}

func aggSuccessFailureCounts(status int) (int64, int64) {
	if status == 0 {
		return 0, 1
	}
	return 1, 0
}

// SetFlushFns installs the per-table batch persist functions. Pass nil
// individually to disable that table's flush (e.g. for partial mock tests).
func (a *Aggregator) SetFlushFns(t TokensFlushFn, c ChannelsFlushFn, h HourlyFlushFn) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokensFn = t
	a.channelsFn = c
	a.hourlyFn = h
}

// Flush snapshots the in-memory buffers, resets them, and calls each
// per-table batch persist fn. On any per-table error, the buffer is
// still considered flushed (cleared) — retries would double-apply,
// and rebuild can recover lost aggregation from UsageLog.
//
// Returns the FIRST error encountered (token > channel > hourly order);
// subsequent errors are logged but not returned (avoid masking root cause).
// Empty buffers short-circuit without calling any fn.
func (a *Aggregator) Flush() error {
	a.mu.Lock()
	if len(a.tokens) == 0 && len(a.channels) == 0 && len(a.hourly) == 0 {
		a.mu.Unlock()
		return nil
	}

	// build row slices while holding the lock
	tokenRows := make([]dao.TokenDailyRow, 0, len(a.tokens))
	for k, v := range a.tokens {
		tokenRows = append(tokenRows, dao.TokenDailyRow{
			Date: k.Date, UserID: k.UserID, TokenID: k.TokenID, TokenName: v.TokenName,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost, TotalCost: v.TotalCost,
			LastUsedAt: v.LastUsedAt, UpdatedAt: v.UpdatedAt,
		})
	}
	channelRows := make([]dao.ChannelDailyRow, 0, len(a.channels))
	for k, v := range a.channels {
		channelRows = append(channelRows, dao.ChannelDailyRow{
			Date: k.Date, ChannelID: k.ChannelID, PrivateChannelID: k.PrivateChannelID,
			ChannelName: v.ChannelName, ChannelType: v.ChannelType, OwnerType: v.OwnerType,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost, TotalCost: v.TotalCost,
			LastUsedAt: v.LastUsedAt, UpdatedAt: v.UpdatedAt,
		})
	}
	hourlyRows := make([]dao.HourlyBucketRow, 0, len(a.hourly))
	for k, v := range a.hourly {
		hourlyRows = append(hourlyRows, dao.HourlyBucketRow{
			Date: k.Date, Hour: k.Hour,
			ChannelID: k.ChannelID, PrivateChannelID: k.PrivateChannelID,
			ModelName: k.ModelName, AgentID: k.AgentID,
			ChannelName: v.ChannelName, ChannelType: v.ChannelType, OwnerType: v.OwnerType,
			RequestCount: v.RequestCount, SuccessCount: v.SuccessCount, FailedCount: v.FailedCount,
			PromptTokens: v.PromptTokens, CompletionTokens: v.CompletionTokens,
			CacheReadTokens: v.CacheReadTokens, CacheWriteTokens: v.CacheWriteTokens,
			InputCost: v.InputCost, OutputCost: v.OutputCost, TotalCost: v.TotalCost,
			StreamRequestCount:        v.StreamRequestCount,
			SumFirstResponseMs:        v.SumFirstResponseMs,
			SumGenerationMs:           v.SumGenerationMs,
			SumStreamCompletionTokens: v.SumStreamCompletionTokens,
			SumInboundDecodeMs:        v.SumInboundDecodeMs,
			SumUpstreamDispatchMs:     v.SumUpstreamDispatchMs,
			SumUpstreamDecodeMs:       v.SumUpstreamDecodeMs,
			SumOutboundEncodeMs:       v.SumOutboundEncodeMs,
			SumClientEncodeMs:         v.SumClientEncodeMs,
			LastUsedAt:                v.LastUsedAt,
			UpdatedAt:                 v.UpdatedAt,
		})
	}

	// reset buffers + capture fn refs while still under lock
	a.tokens = make(map[tokenKey]*tokenDelta)
	a.channels = make(map[channelKey]*channelDelta)
	a.hourly = make(map[hourlyKey]*hourlyDelta)
	tFn, cFn, hFn := a.tokensFn, a.channelsFn, a.hourlyFn
	a.mu.Unlock()

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
		logIfErr("token_daily", tFn(tokenRows), len(tokenRows))
	}
	if cFn != nil {
		logIfErr("channel_daily", cFn(channelRows), len(channelRows))
	}
	if hFn != nil {
		logIfErr("hourly_bucket", hFn(hourlyRows), len(hourlyRows))
	}
	return firstErr
}

// Start spawns the background flush goroutine. It fires Flush on:
//   - ticker tick every flushEvery (skipped if flushEvery <= 0)
//   - maxRows-triggered signal on flushCh (coalesced via cap-1 buffer)
//
// Exits cleanly on ctx.Done() or Stop(). Caller is responsible for
// invoking Stop on shutdown to drain the final batch.
func (a *Aggregator) Start(ctx context.Context) {
	go func() {
		var ticker *time.Ticker
		var tickC <-chan time.Time
		if a.flushEvery > 0 {
			ticker = time.NewTicker(a.flushEvery)
			defer ticker.Stop()
			tickC = ticker.C
		}
		for {
			select {
			case <-tickC:
				if err := a.Flush(); err != nil && a.logger != nil {
					a.logger.Warn("aggregator_flush_tick_failed", zap.Error(err))
				}
			case <-a.flushCh:
				if err := a.Flush(); err != nil && a.logger != nil {
					a.logger.Warn("aggregator_flush_threshold_failed", zap.Error(err))
				}
			case <-ctx.Done():
				return
			case <-a.stopCh:
				return
			}
		}
	}()
}

// Stop signals the background goroutine to exit and performs one final
// force-flush to drain whatever is buffered. Safe to call concurrently
// and idempotent (sync.Once guards the channel close; the final Flush
// is a no-op when the buffer is already empty).
func (a *Aggregator) Stop() {
	a.stopOnce.Do(func() {
		close(a.stopCh)
	})
	if err := a.Flush(); err != nil && a.logger != nil {
		a.logger.Warn("aggregator_flush_on_shutdown_failed", zap.Error(err))
	}
}
