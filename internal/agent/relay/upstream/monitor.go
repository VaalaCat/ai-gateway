package upstream

import (
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
)

// EventMonitor collects usage and timing information from an IR event stream.
//
// firstResponseMs 在两处被写：
//   - MonitorEvents 启动的 goroutine（流到第一个 content event 时）
//   - 主调用方在 non-stream 路径（用 HTTP 响应时间覆盖）
//
// 这两处天然在两个 goroutine 上下文中触发，所以加 mu 保护 firstResponseMs 写入；
// firstResponseMs 的读取（构造 AttemptResult 时）发生在 monitoredEvents 已被完全 drain
// 之后，按文档契约属于安全顺序，但读侧也用 Lock/Unlock 走最小成本兜底。
type EventMonitor struct {
	mu              sync.Mutex
	Usage           codec.Usage
	firstResponseMs int
	FinishReason    string
	ResponseText    strings.Builder
}

// SetFirstResponseMs 是 firstResponseMs 的唯一写入入口；外部主线程和
// MonitorEvents goroutine 都通过它写，避免 data race（-race 探测器报警）。
func (m *EventMonitor) SetFirstResponseMs(ms int) {
	m.mu.Lock()
	m.firstResponseMs = ms
	m.mu.Unlock()
}

// FirstResponseMs 读侧也加锁，跟 SetFirstResponseMs 形成 happens-before。
// 调用点保证发生在 events channel 已 close（goroutine 退出）之后，所以
// 不会与写并发，但显式加锁让规则简单可审计。
func (m *EventMonitor) FirstResponseMs() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.firstResponseMs
}

// MonitorEvents wraps an event channel, intercepting events to collect usage
// data and measure time-to-first-response. It returns a new channel that
// passes all events through and an EventMonitor that is populated as events
// flow through. The monitor values are only guaranteed to be final after the
// returned channel is fully drained (closed).
//
// 包级纯函数：不依赖 Handler 状态，方便 nativeBackend 不持有 *Handler 也能调用。
func MonitorEvents(events <-chan codec.Event, startTime time.Time) (<-chan codec.Event, *EventMonitor) {
	mon := &EventMonitor{}
	out := make(chan codec.Event, 64)

	go func() {
		defer close(out)
		firstContent := true
		for ev := range events {
			// Track first content-bearing event for time-to-first-response.
			// We match any event that carries actual content, not just control
			// signals (StreamStart, Usage, Done, Error).
			if firstContent && isContentEvent(ev.Type) {
				mon.SetFirstResponseMs(int(time.Since(startTime).Milliseconds()))
				firstContent = false
			}

			// Collect usage
			if ev.Type == codec.EventUsage && ev.Usage != nil {
				mon.Usage = *ev.Usage
			}

			// Collect finish reason
			if ev.FinishReason != "" {
				mon.FinishReason = ev.FinishReason
			}

			// Accumulate response text for token estimation
			if ev.Type == codec.EventContentDelta && ev.Delta != nil && ev.Delta.Text != "" {
				mon.ResponseText.WriteString(ev.Delta.Text)
			}

			out <- ev
		}
	}()

	return out, mon
}
