// Package inflight 跟踪 agent 当前在途的 relay 请求,供看门狗告警与 master 远程诊断。
package inflight

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// Meta 是 Track 时已知的初值。
type Meta struct {
	ReqID     string
	StartTime time.Time
	Cancel    context.CancelFunc
}

// Snapshot 是某一时刻一条在途请求的只读视图。View 即"进行中的 usage_log"（与落库同构）。
type Snapshot struct {
	ID           int64                  `json:"id"`
	ReqID        string                 `json:"req_id"`
	View         protocol.UsageLogEntry `json:"view"`
	Stage        string                 `json:"stage"`
	ElapsedMs    int64                  `json:"elapsed_ms"`
	QueuedMs     int64                  `json:"queued_ms"`
	QueuedReason string                 `json:"queued_reason"`
}

// Entry 是一条在途请求的可变句柄。可变字段统一用 mu 保护(snapshot 频率低,mutex 足够清晰)。
type Entry struct {
	reqID     string
	startTime time.Time
	reg       *Registry
	id        int64
	cancel    context.CancelFunc

	mu           sync.RWMutex
	stage        string
	view         protocol.UsageLogEntry
	queuedAt     time.Time
	queuedReason string
}

// SetStage 更新当前阶段名(与 trace.Stage 字符串一致)。nil 接收者安全。
func (e *Entry) SetStage(s string) { e.set(func() { e.stage = s }) }

// Update 用阶段边界投影整份刷新（替代一堆 setter）。nil 接收者安全。
func (e *Entry) Update(view protocol.UsageLogEntry) { e.set(func() { e.view = view }) }

// MarkQueued 标记"正卡在限流队列"。reason=卡在哪条规则。重复调不重置起点。
func (e *Entry) MarkQueued(reason string) {
	e.set(func() {
		if e.queuedAt.IsZero() {
			e.queuedAt = time.Now()
		}
		e.queuedReason = reason
	})
}

// Unqueue 退出排队（排到名额 / 被拒 / 断连）。
func (e *Entry) Unqueue() { e.set(func() { e.queuedAt = time.Time{}; e.queuedReason = "" }) }

func (e *Entry) set(fn func()) {
	if e == nil {
		return
	}
	e.mu.Lock()
	fn()
	e.mu.Unlock()
}

// Done 注销该请求。nil 接收者安全。
func (e *Entry) Done() {
	if e == nil || e.reg == nil {
		return
	}
	e.reg.entries.Delete(e.id)
}

func (e *Entry) snapshot(now time.Time) Snapshot {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var queuedMs int64
	if !e.queuedAt.IsZero() {
		queuedMs = now.Sub(e.queuedAt).Milliseconds()
	}
	return Snapshot{
		ID:           e.id,
		ReqID:        e.reqID,
		View:         e.view,
		Stage:        e.stage,
		ElapsedMs:    now.Sub(e.startTime).Milliseconds(),
		QueuedMs:     queuedMs,
		QueuedReason: e.queuedReason,
	}
}

// Registry 持有所有在途 Entry。key 用自增 id,避免 ReqID 缺失/重复。
type Registry struct {
	entries sync.Map // int64 -> *Entry
	nextID  atomic.Int64
	logger  *zap.Logger
	warnAge time.Duration
}

// NewRegistry 创建注册表。logger 可为 nil。warnAge<=0 时禁用看门狗。
func NewRegistry(logger *zap.Logger, warnAge time.Duration) *Registry {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Registry{logger: logger, warnAge: warnAge}
}

// Track 登记一条在途请求,返回其句柄;调用方必须在请求结束时调用 Done。
func (r *Registry) Track(meta Meta) *Entry {
	if meta.StartTime.IsZero() {
		meta.StartTime = time.Now()
	}
	e := &Entry{
		reqID:     meta.ReqID,
		startTime: meta.StartTime,
		reg:       r,
		id:        r.nextID.Add(1),
		cancel:    meta.Cancel,
	}
	r.entries.Store(e.id, e)
	return e
}

// Snapshot 返回当前所有在途请求的快照。
func (r *Registry) Snapshot() []Snapshot {
	now := time.Now()
	out := make([]Snapshot, 0)
	r.entries.Range(func(_, v any) bool {
		out = append(out, v.(*Entry).snapshot(now))
		return true
	})
	return out
}

// Interrupt 取消 id 对应的在途请求(调其 cancel)。命中且 cancel 非空返回 true。
func (r *Registry) Interrupt(id int64) bool {
	v, ok := r.entries.Load(id)
	if !ok {
		return false
	}
	e := v.(*Entry)
	e.mu.RLock()
	cancel := e.cancel
	e.mu.RUnlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// StartWatchdog 每 interval 扫一遍,任何存活超过 warnAge 的请求打一条 WARN。
// 返回停止函数。warnAge<=0 或 interval<=0 时为 no-op。
func (r *Registry) StartWatchdog(interval time.Duration) (stop func()) {
	if r.warnAge <= 0 || interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				now := time.Now()
				r.entries.Range(func(_, v any) bool {
					s := v.(*Entry).snapshot(now)
					if time.Duration(s.ElapsedMs)*time.Millisecond >= r.warnAge {
						r.logger.Warn("in-flight relay request stuck",
							zap.String("req_id", s.ReqID),
							zap.Uint("channel_id", s.View.ChannelID),
							zap.String("model", s.View.ModelName),
							zap.String("stage", s.Stage),
							zap.Bool("stream", s.View.IsStream),
							zap.Int64("elapsed_ms", s.ElapsedMs))
					}
					return true
				})
			}
		}
	}()
	return func() { close(done) }
}
