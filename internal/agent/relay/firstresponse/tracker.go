package firstresponse

import (
	"sync/atomic"
	"time"
)

// Tracker 记录响应数据首次到达客户端 Writer 的时间。
// 同时存在事件时间与首字节回退时间时，最终读取优先返回事件时间。
type Tracker struct {
	startedAt    time.Time
	firstByteMs  atomic.Int64
	firstEventMs atomic.Int64
}

func NewTracker(startedAt time.Time) *Tracker {
	return &Tracker{startedAt: startedAt}
}

func (tracker *Tracker) Milliseconds() int {
	if tracker == nil {
		return 0
	}
	if milliseconds := tracker.firstEventMs.Load(); milliseconds > 0 {
		return int(milliseconds)
	}
	return int(tracker.firstByteMs.Load())
}

func (tracker *Tracker) observeFirstByte(observedAt time.Time) {
	tracker.observeOnce(&tracker.firstByteMs, observedAt)
}

func (tracker *Tracker) observeFirstEvent(observedAt time.Time) {
	tracker.observeOnce(&tracker.firstEventMs, observedAt)
}

func (tracker *Tracker) observeOnce(target *atomic.Int64, observedAt time.Time) {
	if tracker == nil {
		return
	}
	milliseconds := observedAt.Sub(tracker.startedAt).Milliseconds()
	if milliseconds < 1 {
		milliseconds = 1
	}
	target.CompareAndSwap(0, milliseconds)
}
