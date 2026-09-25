package upstream

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type EventSnapshot struct {
	Usage        llmkit.Usage
	FinishReason string
	ResponseText string
	EventErr     error
}

// EventMonitor 从 IR 事件流中收集一份并发安全的最终快照。
type EventMonitor struct {
	mu           sync.Mutex
	usage        llmkit.Usage
	finishReason string
	responseText strings.Builder
	eventErr     error
	done         chan struct{}
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
		Usage:        m.usage,
		FinishReason: m.finishReason,
		ResponseText: m.responseText.String(),
		EventErr:     m.eventErr,
	}
}

func (m *EventMonitor) FinalSnapshot() EventSnapshot {
	<-m.done
	return m.Snapshot()
}

// MonitorEvents 包装事件通道并收集 usage、结束原因、响应文本和流错误。
// 返回通道原样转发所有事件；FinalSnapshot 会等待自然结束或 attempt context 取消。
//
// 包级纯函数：不依赖 Handler 状态，方便 nativeBackend 不持有 *Handler 也能调用。
func MonitorEvents(ctx context.Context, events <-chan llmkit.Event) (<-chan llmkit.Event, *EventMonitor) {
	mon := &EventMonitor{done: make(chan struct{})}
	out := make(chan llmkit.Event, 64)

	go func() {
		defer close(out)
		defer close(mon.done)
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
