package sync

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	gosync "sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/sourcegraph/conc"
	"go.uber.org/zap"
)

// HeartbeatTracker holds the in-memory authoritative source for agent last_seen
// and config fingerprints. last_seen is batch-flushed to DB periodically; the
// config fingerprint is only used to skip mergeAgentConfig SELECTs on
// unchanged heartbeats.
type HeartbeatTracker struct {
	mu    gosync.RWMutex
	seen  map[string]int64
	dirty map[string]struct{}
	cfgFp map[string]configFingerprintState

	flushEvery    time.Duration
	app           dao.AppProvider
	logger        *zap.Logger
	stopCh        chan struct{}
	lifecycleMu   gosync.Mutex
	started       bool
	closing       bool
	closeOnce     gosync.Once
	workerCancel  context.CancelCauseFunc
	workers       conc.WaitGroup
	done          chan struct{}
	persistFn     LastSeenPersistContextFn
	activeWorkers atomic.Int64
	activeTimers  atomic.Int64
	inflight      atomic.Int64
}

type configFingerprintState struct {
	generation  uint64
	fingerprint string
}

// LastSeenPersistFn is the function Flush calls to persist updates.
// Inject the real dao.BatchUpdateLastSeen for production; nil makes Flush a no-op.
type LastSeenPersistFn func(updates map[string]int64) error
type LastSeenPersistContextFn func(context.Context, map[string]int64) error

// NewHeartbeatTracker constructs a tracker. app may be nil for pure-memory
// tests. flushEvery <= 0 disables the background ticker.
func NewHeartbeatTracker(app dao.AppProvider, logger *zap.Logger, flushEvery time.Duration) *HeartbeatTracker {
	return &HeartbeatTracker{
		seen:       make(map[string]int64),
		dirty:      make(map[string]struct{}),
		cfgFp:      make(map[string]configFingerprintState),
		flushEvery: flushEvery,
		app:        app,
		logger:     logger,
		stopCh:     make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Touch records a heartbeat/connect event purely in memory.
func (t *HeartbeatTracker) Touch(agentID string, ts int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if current, ok := t.seen[agentID]; ok && ts <= current {
		return
	}
	t.seen[agentID] = ts
	t.dirty[agentID] = struct{}{}
}

// Get returns the in-memory last_seen; (0, false) when not present.
func (t *HeartbeatTracker) Get(agentID string) (int64, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	ts, ok := t.seen[agentID]
	return ts, ok
}

// GetMany returns only the in-memory hits among ids.
func (t *HeartbeatTracker) GetMany(ids []string) map[string]int64 {
	out := make(map[string]int64, len(ids))
	if len(ids) == 0 {
		return out
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, id := range ids {
		if ts, ok := t.seen[id]; ok {
			out[id] = ts
		}
	}
	return out
}

// SetLastSeenPersistFn installs the persistence function used by Flush.
func (t *HeartbeatTracker) SetLastSeenPersistFn(fn LastSeenPersistFn) {
	if fn == nil {
		t.SetLastSeenPersistContextFn(nil)
		return
	}
	t.SetLastSeenPersistContextFn(func(_ context.Context, updates map[string]int64) error {
		return fn(updates)
	})
}

func (t *HeartbeatTracker) SetLastSeenPersistContextFn(fn LastSeenPersistContextFn) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.persistFn = fn
}

// Flush snapshots dirty entries to DB and clears the dirty set.
// On persistence failure: dirty is cleared but updates are dropped.
// Live agents recover on their next Touch (re-marks dirty). For agents that
// never heartbeat again, last_seen may stay stale in DB — acceptable, since
// such agents are stale anyway and the UI reads memory first.
func (t *HeartbeatTracker) Flush() error {
	return t.FlushContext(context.Background())
}

func (t *HeartbeatTracker) FlushContext(ctx context.Context) error {
	t.mu.Lock()
	if len(t.dirty) == 0 {
		t.mu.Unlock()
		return nil
	}
	snapshot := make(map[string]int64, len(t.dirty))
	for id := range t.dirty {
		snapshot[id] = t.seen[id]
	}
	t.dirty = make(map[string]struct{})
	fn := t.persistFn
	t.mu.Unlock()

	if fn == nil {
		return nil
	}
	t.inflight.Add(1)
	defer t.inflight.Add(-1)
	return fn(ctx, snapshot)
}

// Start spawns a background goroutine that calls Flush every flushEvery.
// When ctx is canceled (or Stop is called), the goroutine exits.
// flushEvery <= 0 disables the ticker entirely; callers must Flush manually
// or rely on Stop's force-flush.
func (t *HeartbeatTracker) Start(ctx context.Context) {
	t.lifecycleMu.Lock()
	defer t.lifecycleMu.Unlock()
	if t.started || t.closing || ctx == nil {
		return
	}
	t.started = true
	workerCtx, cancel := context.WithCancelCause(ctx)
	t.workerCancel = cancel
	if t.flushEvery <= 0 {
		return
	}
	t.activeWorkers.Add(1)
	t.activeTimers.Add(1)
	t.workers.Go(func() {
		defer t.activeWorkers.Add(-1)
		defer t.activeTimers.Add(-1)
		ticker := time.NewTicker(t.flushEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := t.FlushContext(workerCtx); err != nil && t.logger != nil {
					t.logger.Warn("heartbeat_flush failed", zap.Error(err))
				}
			case <-workerCtx.Done():
				return
			case <-t.stopCh:
				return
			}
		}
	})
}

// Stop signals the ticker goroutine to exit and runs one final Flush
// to persist anything still in dirty. Safe to call concurrently and idempotent:
// stopCh 关闭走 sync.Once，Flush 自身由 mu 保护可重复调用。
func (t *HeartbeatTracker) Close(ctx context.Context) error {
	if ctx == nil {
		return errors.New("heartbeat tracker: nil close context")
	}
	t.closeOnce.Do(func() {
		t.lifecycleMu.Lock()
		t.closing = true
		if t.workerCancel != nil {
			t.workerCancel(context.Cause(ctx))
		}
		close(t.stopCh)
		t.lifecycleMu.Unlock()
		go func() {
			t.workers.Wait()
			if err := t.FlushContext(ctx); err != nil && t.logger != nil {
				t.logger.Warn("heartbeat_flush failed on shutdown", zap.Error(err))
			}
			close(t.done)
		}()
	})
	select {
	case <-t.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (t *HeartbeatTracker) Stop(ctx context.Context) error { return t.Close(ctx) }

func (t *HeartbeatTracker) Done() <-chan struct{} { return t.done }

func (t *HeartbeatTracker) ResourceCounts() app.ResourceCounts {
	return app.ResourceCounts{
		LifecycleWorkers: t.activeWorkers.Load(),
		Timers:           t.activeTimers.Load(),
		Inflight:         t.inflight.Load(),
	}
}

// configFingerprint hashes the heartbeat configuration fields used to decide
// whether mergeAgentConfig needs to inspect the persisted agent again.
func configFingerprint(params protocol.HeartbeatParams) string {
	payload := struct {
		Addrs      json.RawMessage `json:"addrs,omitempty"`
		Tags       string          `json:"tags,omitempty"`
		ProxyURL   string          `json:"proxy_url,omitempty"`
		ListenPort int             `json:"listen_port,omitempty"`
	}{
		Addrs:      params.HTTPAddresses,
		Tags:       params.Tags,
		ProxyURL:   params.ProxyURL,
		ListenPort: params.ListenPort,
	}
	raw, _ := json.Marshal(payload)
	sum := sha1.Sum(raw)
	return hex.EncodeToString(sum[:8])
}

// ConfigChanged returns whether the current generation must merge params.
// Newer generations always merge, stale generations are ignored, and repeated
// fingerprints within the same generation skip mergeAgentConfig's SELECT.
func (t *HeartbeatTracker) ConfigChanged(agentID string, generation uint64, params protocol.HeartbeatParams) bool {
	fp := configFingerprint(params)
	t.mu.Lock()
	defer t.mu.Unlock()
	current, ok := t.cfgFp[agentID]
	if ok && generation < current.generation {
		return false
	}
	if ok && generation == current.generation && current.fingerprint == fp {
		return false
	}
	t.cfgFp[agentID] = configFingerprintState{generation: generation, fingerprint: fp}
	return true
}
