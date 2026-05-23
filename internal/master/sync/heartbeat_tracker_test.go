package sync

import (
	"context"
	"encoding/json"
	gosync "sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newTrackerForTest(t *testing.T) *HeartbeatTracker {
	t.Helper()
	return NewHeartbeatTracker(nil, zap.NewNop(), 0)
}

func TestHeartbeatTracker_TouchAndGet(t *testing.T) {
	tr := newTrackerForTest(t)

	// success: 单次 Touch 后能 Get 出来
	tr.Touch("agent-a", 1000)
	ts, ok := tr.Get("agent-a")
	require.True(t, ok)
	require.Equal(t, int64(1000), ts)

	// success: 多次 Touch 取最新值
	tr.Touch("agent-a", 2000)
	ts, ok = tr.Get("agent-a")
	require.True(t, ok)
	require.Equal(t, int64(2000), ts)

	// failure: 未 Touch 的 agent 返回 false
	_, ok = tr.Get("ghost")
	require.False(t, ok)

	// boundary: Touch ts=0 也算 dirty
	tr.Touch("agent-b", 0)
	ts, ok = tr.Get("agent-b")
	require.True(t, ok)
	require.Equal(t, int64(0), ts)
}

func TestHeartbeatTracker_GetMany(t *testing.T) {
	tr := newTrackerForTest(t)
	tr.Touch("a", 100)
	tr.Touch("b", 200)

	// success: 部分命中
	got := tr.GetMany([]string{"a", "b", "missing"})
	require.Equal(t, int64(100), got["a"])
	require.Equal(t, int64(200), got["b"])
	_, ok := got["missing"]
	require.False(t, ok)

	// boundary: 空入参返回空 map
	got = tr.GetMany(nil)
	require.Empty(t, got)
}

// fakeAgentMutator captures BatchUpdateLastSeen calls for testing.
// mu protects calls/err so ticker goroutine writes don't race with
// main goroutine reads in Eventually/post-Stop assertions.
type fakeAgentMutator struct {
	mu    gosync.Mutex
	calls []map[string]int64
	err   error
}

func (f *fakeAgentMutator) record(updates map[string]int64) error {
	cp := make(map[string]int64, len(updates))
	for k, v := range updates {
		cp[k] = v
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cp)
	return f.err
}

func (f *fakeAgentMutator) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeAgentMutator) snapshotCalls() []map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]int64, len(f.calls))
	copy(out, f.calls)
	return out
}

func TestHeartbeatTracker_Flush(t *testing.T) {
	tr := newTrackerForTest(t)
	fake := &fakeAgentMutator{}
	tr.SetLastSeenPersistFn(fake.record)

	// success: dirty 集合落地
	tr.Touch("a", 100)
	tr.Touch("b", 200)
	require.NoError(t, tr.Flush())
	require.Len(t, fake.calls, 1)
	require.Equal(t, map[string]int64{"a": 100, "b": 200}, fake.calls[0])

	// boundary: 空 dirty 不发起调用
	require.NoError(t, tr.Flush())
	require.Len(t, fake.calls, 1, "无 dirty 不应再次调用")

	// failure: dao 返回 error 时 Flush 返回 error
	fake.err = assert.AnError
	tr.Touch("c", 300)
	callsBefore := len(fake.calls)
	require.Error(t, tr.Flush())
	require.Equal(t, callsBefore+1, len(fake.calls), "失败仍发起 1 次调用")

	// invariant: dirty 在失败后已清空（不重试），所以下一次 Flush 无新调用
	fake.err = nil
	require.NoError(t, tr.Flush())
	require.Equal(t, callsBefore+1, len(fake.calls), "失败后 dirty 已清，下次 Flush 不发起调用")

	// success: Touch 后再次 Flush 能落库（验证 dirty 重新工作）
	tr.Touch("c", 400)
	require.NoError(t, tr.Flush())
	require.Equal(t, int64(400), fake.calls[len(fake.calls)-1]["c"])
}

func TestHeartbeatTracker_TickerFlushesPeriodically(t *testing.T) {
	tr := NewHeartbeatTracker(nil, zap.NewNop(), 20*time.Millisecond)
	fake := &fakeAgentMutator{}
	tr.SetLastSeenPersistFn(fake.record)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tr.Start(ctx)

	// success: Touch 后等 ticker 自动 flush
	tr.Touch("a", 100)
	require.Eventually(t, func() bool {
		return fake.callCount() >= 1
	}, time.Second, 5*time.Millisecond, "ticker 应在 20ms 内 flush")

	// success: Stop force flush 最后一批
	tr.Touch("b", 200)
	tr.Stop()
	found := false
	for _, c := range fake.snapshotCalls() {
		if _, ok := c["b"]; ok {
			found = true
			break
		}
	}
	require.True(t, found, "Stop 应 force flush 把最后一批落库")
}

func TestHeartbeatTracker_StopConcurrentSafe(t *testing.T) {
	tr := NewHeartbeatTracker(nil, zap.NewNop(), 0)
	fake := &fakeAgentMutator{}
	tr.SetLastSeenPersistFn(fake.record)

	// success: concurrent Stop 不应 panic（close closed channel）
	const callers = 20
	var wg gosync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func() {
			defer wg.Done()
			tr.Stop()
		}()
	}
	wg.Wait()
	// 验证多次 Stop 后仍能正确 close stopCh（不再 panic）
	select {
	case <-tr.stopCh:
		// ok, stopCh 已 close
	default:
		t.Fatal("stopCh 未被 close")
	}
}

func TestHeartbeatTracker_NoTickerWhenFlushEveryZero(t *testing.T) {
	tr := NewHeartbeatTracker(nil, zap.NewNop(), 0)
	fake := &fakeAgentMutator{}
	tr.SetLastSeenPersistFn(fake.record)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// boundary: flushEvery=0 表示不启动 ticker
	tr.Start(ctx)
	tr.Touch("a", 100)
	time.Sleep(40 * time.Millisecond)
	require.Zero(t, fake.callCount(), "flushEvery=0 不应触发自动 flush")
	tr.Stop()
}

func TestHeartbeatTracker_ConfigChanged(t *testing.T) {
	tr := newTrackerForTest(t)

	params := protocol.HeartbeatParams{
		HTTPAddresses: json.RawMessage(`["http://a:1"]`),
		Tags:          "x",
		ProxyURL:      "http://proxy:8080",
		ListenPort:    9000,
	}

	// success: 冷启动第一次永远算变化
	require.True(t, tr.ConfigChanged("a", params))

	// success: 同 params 第二次返回 false
	require.False(t, tr.ConfigChanged("a", params))

	// success: 改 HTTPAddresses → 变化
	p2 := params
	p2.HTTPAddresses = json.RawMessage(`["http://b:2"]`)
	require.True(t, tr.ConfigChanged("a", p2))

	// success: 改 Tags → 变化
	p3 := p2
	p3.Tags = "y"
	require.True(t, tr.ConfigChanged("a", p3))

	// success: 改 ProxyURL → 变化
	p4 := p3
	p4.ProxyURL = "http://proxy:9999"
	require.True(t, tr.ConfigChanged("a", p4))

	// success: 改 ListenPort → 变化
	p5 := p4
	p5.ListenPort = 9001
	require.True(t, tr.ConfigChanged("a", p5))

	// boundary: 同 agent 的 cfgFp 已更新，再发一次相同 p5 → false
	require.False(t, tr.ConfigChanged("a", p5))

	// boundary: Uptime / CachedTokens 等非指纹字段变化不算 ConfigChanged
	p6 := p5
	p6.Uptime = 999999
	p6.CachedTokens = 42
	require.False(t, tr.ConfigChanged("a", p6))

	// boundary: 不同 agent_id 各自独立
	require.True(t, tr.ConfigChanged("b", params), "agent b 冷启动")
}
