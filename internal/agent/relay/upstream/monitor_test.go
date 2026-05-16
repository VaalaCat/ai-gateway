package upstream

import (
	"sync"
	"testing"
)

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
