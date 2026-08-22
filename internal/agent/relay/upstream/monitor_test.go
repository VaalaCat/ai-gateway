package upstream

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

func TestMonitorEvents_FinalSnapshotAfterNaturalDrain(t *testing.T) {
	ctx := context.Background()
	events := make(chan llmkit.Event, 4)
	events <- llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "hello"},
	}
	events <- llmkit.Event{
		Type: llmkit.EventUsage,
		Usage: &llmkit.Usage{
			PromptTokens: 7, CompletionTokens: 11,
			CacheReadTokens: 3, CacheWriteTokens: 5,
		},
	}
	events <- llmkit.Event{Type: llmkit.EventDone, FinishReason: "stop"}
	close(events)

	monitored, monitor := MonitorEvents(ctx, events, time.Now().Add(-time.Second))
	for range monitored {
	}
	snapshot := monitor.FinalSnapshot()

	if snapshot.Usage.PromptTokens != 7 || snapshot.Usage.CompletionTokens != 11 ||
		snapshot.Usage.CacheReadTokens != 3 || snapshot.Usage.CacheWriteTokens != 5 {
		t.Fatalf("final usage = %+v, want prompt=7 completion=11 cache_read=3 cache_write=5", snapshot.Usage)
	}
	if snapshot.ResponseText != "hello" {
		t.Fatalf("final response text = %q, want hello", snapshot.ResponseText)
	}
	if snapshot.FinishReason != "stop" {
		t.Fatalf("final finish reason = %q, want stop", snapshot.FinishReason)
	}
	if snapshot.FirstResponseMs < 900 {
		t.Fatalf("final first response = %dms, want at least 900ms", snapshot.FirstResponseMs)
	}
	if snapshot.EventErr != nil {
		t.Fatalf("final event error = %v, want nil", snapshot.EventErr)
	}
}

func TestMonitorEvents_CancelUnblocksUndrainedOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan llmkit.Event, 70)
	events <- llmkit.Event{
		Type:  llmkit.EventContentDelta,
		Delta: &llmkit.DeltaPayload{Text: "observed"},
	}
	for range 69 {
		events <- llmkit.Event{Type: llmkit.EventStreamStart}
	}

	monitored, monitor := MonitorEvents(ctx, events, time.Now())
	if event := <-monitored; event.Delta == nil || event.Delta.Text != "observed" {
		t.Fatalf("first monitored event = %+v, want observed content", event)
	}
	cancel()

	select {
	case <-monitor.Done():
	case <-time.After(time.Second):
		t.Fatal("monitor did not stop after attempt context cancellation")
	}
	if got := monitor.FinalSnapshot().ResponseText; got != "observed" {
		t.Fatalf("final response text = %q, want observed event before blocked forwarding", got)
	}
}

func TestMonitorEvents_EmptyStreamHasZeroFinalSnapshot(t *testing.T) {
	events := make(chan llmkit.Event)
	close(events)

	monitored, monitor := MonitorEvents(context.Background(), events, time.Now())
	for range monitored {
	}
	snapshot := monitor.FinalSnapshot()

	if snapshot.Usage != (llmkit.Usage{}) || snapshot.ResponseText != "" ||
		snapshot.FinishReason != "" || snapshot.FirstResponseMs != 0 ||
		snapshot.EventErr != nil {
		t.Fatalf("empty final snapshot = %+v, want zero value", snapshot)
	}
	select {
	case <-monitor.Done():
	default:
		t.Fatal("monitor Done must be closed after the source stream closes")
	}
}

func TestMonitorEvents_EventErrorIsIncludedInFinalSnapshot(t *testing.T) {
	events := make(chan llmkit.Event, 1)
	events <- llmkit.Event{
		Type:  llmkit.EventError,
		Error: &llmkit.ErrorPayload{Message: "stream broke"},
	}
	close(events)

	monitored, monitor := MonitorEvents(context.Background(), events, time.Now())
	for range monitored {
	}
	got := monitor.FinalSnapshot().EventErr
	if got == nil || !errors.Is(got, monitor.EventError()) || got.Error() != "stream broke" {
		t.Fatalf("final event error = %v, want stream broke", got)
	}
}

// TestEventMonitor_SetFirstResponseMsRaceFree 直接验证 EventMonitor.SetFirstResponseMs /
// FirstResponseMs 在并发读写下不出 data race（参考 commit 2d54822 修复点）。
//
// 之前 MonitorEvents 后台 goroutine 与主线程 non-stream 路径会同时写 firstResponseMs：
//   - goroutine: 流到第一个 content event 时 SetFirstResponseMs
//   - 主线程:     HTTP 响应到达后覆盖
//
// 集成测试只通过 streaming 路径隐式触发——本测试以最小成本直接打 race 点，
// `go test -race` 跑时 race detector 命中即 FAIL。
//
// 断言：
//  1. 不报 data race（靠 -race）；
//  2. 并发结束后读出的值必须是某一次写入的值（不撕裂）。
func TestEventMonitor_SetFirstResponseMsRaceFree(t *testing.T) {
	const writers = 20
	const readers = 20
	const iterations = 200

	m := &EventMonitor{}

	var wg sync.WaitGroup
	wg.Add(writers + readers)

	// writers: 每个 goroutine 写一个固定值 (1..writers)，便于最终断言 "值 ∈ {1..N}"
	for w := 1; w <= writers; w++ {
		v := w
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				m.SetFirstResponseMs(v)
			}
		}()
	}

	// readers: 并发读；不做断言（只为让 -race 探测器看到读写并发）
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_ = m.FirstResponseMs()
			}
		}()
	}

	wg.Wait()

	final := m.FirstResponseMs()
	if final < 1 || final > writers {
		t.Errorf("final FirstResponseMs = %d, want some value in [1,%d] (撕裂或脏读)", final, writers)
	}
}

// TestEventMonitor_FirstResponseMsHappensBefore 验证显式 Lock/Unlock 形成 happens-before：
// 单线程先 set 后 get，读出必须是最后一次写入的值（基础正确性，跟 race-free 是两个事）。
func TestEventMonitor_FirstResponseMsHappensBefore(t *testing.T) {
	m := &EventMonitor{}
	m.SetFirstResponseMs(42)
	if got := m.FirstResponseMs(); got != 42 {
		t.Errorf("FirstResponseMs() = %d, want 42", got)
	}
	m.SetFirstResponseMs(100)
	if got := m.FirstResponseMs(); got != 100 {
		t.Errorf("FirstResponseMs() = %d, want 100 (覆盖语义)", got)
	}
}
