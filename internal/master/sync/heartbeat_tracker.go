package sync

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	gosync "sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
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
	cfgFp map[string]string

	flushEvery time.Duration
	app        dao.AppProvider
	logger     *zap.Logger
	stopCh     chan struct{}
	stopOnce   gosync.Once
	persistFn  LastSeenPersistFn
}

// LastSeenPersistFn is the function Flush calls to persist updates.
// Inject the real dao.BatchUpdateLastSeen for production; nil makes Flush a no-op.
type LastSeenPersistFn func(updates map[string]int64) error

// NewHeartbeatTracker constructs a tracker. app may be nil for pure-memory
// tests. flushEvery <= 0 disables the background ticker.
func NewHeartbeatTracker(app dao.AppProvider, logger *zap.Logger, flushEvery time.Duration) *HeartbeatTracker {
	return &HeartbeatTracker{
		seen:       make(map[string]int64),
		dirty:      make(map[string]struct{}),
		cfgFp:      make(map[string]string),
		flushEvery: flushEvery,
		app:        app,
		logger:     logger,
		stopCh:     make(chan struct{}),
	}
}

// Touch records a heartbeat/connect event purely in memory.
func (t *HeartbeatTracker) Touch(agentID string, ts int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
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
	return fn(snapshot)
}

// Start spawns a background goroutine that calls Flush every flushEvery.
// When ctx is canceled (or Stop is called), the goroutine exits.
// flushEvery <= 0 disables the ticker entirely; callers must Flush manually
// or rely on Stop's force-flush.
func (t *HeartbeatTracker) Start(ctx context.Context) {
	if t.flushEvery <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(t.flushEvery)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := t.Flush(); err != nil && t.logger != nil {
					t.logger.Warn("heartbeat_flush failed", zap.Error(err))
				}
			case <-ctx.Done():
				return
			case <-t.stopCh:
				return
			}
		}
	}()
}

// Stop signals the ticker goroutine to exit and runs one final Flush
// to persist anything still in dirty. Safe to call concurrently and idempotent:
// stopCh 关闭走 sync.Once，Flush 自身由 mu 保护可重复调用。
func (t *HeartbeatTracker) Stop() {
	t.stopOnce.Do(func() {
		close(t.stopCh)
	})
	if err := t.Flush(); err != nil && t.logger != nil {
		t.logger.Warn("heartbeat_flush failed on shutdown", zap.Error(err))
	}
}

// configFingerprint 计算 mergeAgentConfig 实际写 DB 时关心的 4 个字段
// 的 sha1 前 16 字节十六进制。字段集来自 hub.go:472 mergeAgentConfig 实现。
// 必须保持"指纹变化 ⟺ mergeAgentConfig 会写 DB"。
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

// ConfigChanged 返回 params 是否与上次记录的指纹不同，并在变化时更新指纹。
// 冷启动（首次见到该 agent）永远返回 true。
// 用于跳过无变化心跳的 mergeAgentConfig SELECT。
func (t *HeartbeatTracker) ConfigChanged(agentID string, params protocol.HeartbeatParams) bool {
	fp := configFingerprint(params)
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cfgFp[agentID] == fp {
		return false
	}
	t.cfgFp[agentID] = fp
	return true
}
