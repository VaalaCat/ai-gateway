// Package inflight 跟踪 agent 当前在途的 relay 请求,供看门狗告警与 master 远程诊断。
package inflight

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// Meta 是 Track 时已知的初值;Model/IsStream/ChannelName 也可 Track 后用 setter 补/改。
type Meta struct {
	ReqID       string
	ChannelID   uint
	ChannelName string
	Model       string
	IsStream    bool
	StartTime   time.Time
	// Cancel 取消该请求(打断用)。由 Relay 传入从请求 context 派生的 cancel;可为 nil。
	Cancel context.CancelFunc
}

// Snapshot 是某一时刻一条在途请求的只读视图(给 RPC / 看门狗用)。
type Snapshot struct {
	ID          int64  `json:"id"` // registry 内部自增 key,打断按此定位(避免 ReqID 缺失/重复)
	ReqID       string `json:"req_id"`
	ChannelID   uint   `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Model       string `json:"model"`
	IsStream    bool   `json:"is_stream"`
	Stage       string `json:"stage"`
	ElapsedMs   int64  `json:"elapsed_ms"`
}

// Entry 是一条在途请求的可变句柄。可变字段统一用 mu 保护(snapshot 频率低,mutex 足够清晰)。
type Entry struct {
	reqID     string
	channelID uint
	startTime time.Time
	reg       *Registry
	id        int64
	cancel    context.CancelFunc

	mu          sync.RWMutex
	stage       string
	model       string
	isStream    bool
	channelName string
}

// SetStage 更新当前阶段名(与 trace.Stage 字符串一致)。nil 接收者安全。
func (e *Entry) SetStage(s string) { e.set(func() { e.stage = s }) }

// SetModel / SetStream / SetChannel 用于 ctxBuild / dispatch 后补全信息。
func (e *Entry) SetModel(m string)      { e.set(func() { e.model = m }) }
func (e *Entry) SetStream(b bool)       { e.set(func() { e.isStream = b }) }
func (e *Entry) SetChannel(name string) { e.set(func() { e.channelName = name }) }

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
	return Snapshot{
		ID:          e.id,
		ReqID:       e.reqID,
		ChannelID:   e.channelID,
		ChannelName: e.channelName,
		Model:       e.model,
		IsStream:    e.isStream,
		Stage:       e.stage,
		ElapsedMs:   now.Sub(e.startTime).Milliseconds(),
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
		reqID:       meta.ReqID,
		channelID:   meta.ChannelID,
		startTime:   meta.StartTime,
		reg:         r,
		id:          r.nextID.Add(1),
		cancel:      meta.Cancel,
		stage:       "",
		model:       meta.Model,
		isStream:    meta.IsStream,
		channelName: meta.ChannelName,
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
							zap.Uint("channel_id", s.ChannelID),
							zap.String("channel", s.ChannelName),
							zap.String("model", s.Model),
							zap.String("stage", s.Stage),
							zap.Bool("stream", s.IsStream),
							zap.Int64("elapsed_ms", s.ElapsedMs))
					}
					return true
				})
			}
		}
	}()
	return func() { close(done) }
}
