package upstream

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type EventSnapshot struct {
	Usage           llmkit.Usage
	FirstResponseMs int
	FinishReason    string
	ResponseText    string
	EventErr        error
}

// EventMonitor collects one synchronized snapshot from an IR event stream.
type EventMonitor struct {
	mu              sync.Mutex
	usage           llmkit.Usage
	firstResponseMs int
	finishReason    string
	responseText    strings.Builder
	eventErr        error
	done            chan struct{}
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

func (m *EventMonitor) EventError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventErr
}

func (m *EventMonitor) Done() <-chan struct{} {
	return m.done
}

func (m *EventMonitor) Snapshot() EventSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return EventSnapshot{
		Usage:           m.usage,
		FirstResponseMs: m.firstResponseMs,
		FinishReason:    m.finishReason,
		ResponseText:    m.responseText.String(),
		EventErr:        m.eventErr,
	}
}

func (m *EventMonitor) FinalSnapshot() EventSnapshot {
	<-m.done
	return m.Snapshot()
}

// MonitorEvents wraps an event channel, intercepting events to collect usage
// data and measure time-to-first-response. It returns a new channel that
// passes all events through and an EventMonitor that is populated as events
// flow through. FinalSnapshot waits until monitoring has stopped naturally or
// because the attempt context was canceled.
//
// 包级纯函数：不依赖 Handler 状态，方便 nativeBackend 不持有 *Handler 也能调用。
func MonitorEvents(ctx context.Context, events <-chan llmkit.Event, startTime time.Time) (<-chan llmkit.Event, *EventMonitor) {
	mon := &EventMonitor{done: make(chan struct{})}
	out := make(chan llmkit.Event, 64)

	go func() {
		defer close(out)
		defer close(mon.done)
		firstContent := true
		for {
			var ev llmkit.Event
			var ok bool
			select {
			case <-ctx.Done():
				return
			case ev, ok = <-events:
				if !ok {
					return
				}
			}

			mon.mu.Lock()
			if ev.Type == llmkit.EventError {
				message := "upstream response stream failed"
				if ev.Error != nil && ev.Error.Message != "" {
					message = ev.Error.Message
				}
				if mon.eventErr == nil {
					mon.eventErr = errors.New(message)
				}
			}
			if firstContent && isContentEvent(ev.Type) {
				mon.firstResponseMs = int(time.Since(startTime).Milliseconds())
				firstContent = false
			}
			if ev.Type == llmkit.EventUsage && ev.Usage != nil {
				mon.usage = *ev.Usage
			}
			if ev.FinishReason != "" {
				mon.finishReason = ev.FinishReason
			}
			if ev.Type == llmkit.EventContentDelta && ev.Delta != nil && ev.Delta.Text != "" {
				mon.responseText.WriteString(ev.Delta.Text)
			}
			mon.mu.Unlock()

			select {
			case out <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	return out, mon
}
