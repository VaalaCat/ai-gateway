// internal/agent/reporter/retry_queue_test.go
package reporter

import (
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestRetryQueue_PushDueOrdering(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	now := time.Now()
	q.push([]protocol.UsageLogEntry{entry("a"), entry("b")}, 1, now.Add(-time.Second)) // already due
	q.push([]protocol.UsageLogEntry{entry("c")}, 1, now.Add(time.Hour))                // not due yet
	if got := q.Len(); got != 3 {
		t.Fatalf("Len = %d, want 3", got)
	}
	due := q.due(now, 10)
	if len(due) != 2 || due[0].entry.RequestID != "a" || due[1].entry.RequestID != "b" {
		t.Fatalf("due = %+v, want [a b]", due)
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("Len after due = %d, want 1 (c stays, not due yet)", got)
	}
	if got := q.due(now, 10); len(got) != 0 {
		t.Fatalf("second due() call = %+v, want empty (c still not due)", got)
	}
}

// TestRetryQueue_DueRespectsMax 是边界用例:due(max) 不能一次性倾泻整个队列,
// 主/重试循环都靠这个上限控制单次唤醒处理多少条。
func TestRetryQueue_DueRespectsMax(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	now := time.Now()
	q.push([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")}, 1, now.Add(-time.Second))
	due := q.due(now, 2)
	if len(due) != 2 {
		t.Fatalf("due len = %d, want 2 (capped by max)", len(due))
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("Len after due(max=2) = %d, want 1 (leftover stays queued)", got)
	}
}

// TestRetryQueue_DueSkipsNotYetDueEvenIfNotAtHead 是关键回归用例:退避时长按
// attempts 指数增长,队列里先进去的条目不一定先到期。一条 attempts 很大、退避
// 很长的老条目排在前面还没到期,不该挡住排在它后面、attempts 小、退避短、
// 已经到期的新条目——如果 due() 只扫前缀碰到第一个未到期就收手,这里就会漏掉。
func TestRetryQueue_DueSkipsNotYetDueEvenIfNotAtHead(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	now := time.Now()
	q.push([]protocol.UsageLogEntry{entry("old-long-backoff")}, 5, now.Add(time.Hour))
	q.push([]protocol.UsageLogEntry{entry("new-short-backoff")}, 1, now.Add(-time.Second))
	due := q.due(now, 10)
	if len(due) != 1 || due[0].entry.RequestID != "new-short-backoff" {
		t.Fatalf("due = %+v, want only [new-short-backoff]", due)
	}
	if got := q.Len(); got != 1 {
		t.Fatalf("Len after due = %d, want 1 (old item still queued, not yet due)", got)
	}
}

func TestRetryQueue_OverflowDropsOldestLogged(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	q := newRetryQueue(2, zap.New(core))
	q.push([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")}, 1, time.Now())
	if got := q.Len(); got != 2 {
		t.Fatalf("Len = %d, want 2 (capped)", got)
	}
	got := logs.FilterMessage("retry queue overflow, dropped oldest entries").All()
	if len(got) != 1 {
		t.Fatalf("overflow log entries = %d, want 1", len(got))
	}
	fields := got[0].ContextMap()
	if fields["dropped"] != int64(1) || fields["pending_len"] != int64(2) {
		t.Fatalf("log fields = %v, want dropped=1 pending_len=2", fields)
	}
	due := q.due(time.Now().Add(time.Second), 10)
	if len(due) != 2 || due[0].entry.RequestID != "b" || due[1].entry.RequestID != "c" {
		t.Fatalf("due = %+v, want oldest 'a' dropped, [b c] remain", due)
	}
}

func TestRetryQueue_EmptyAndBounds(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	if got := q.due(time.Now(), 5); got != nil {
		t.Fatalf("due on empty = %v, want nil", got)
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len = %d, want 0", got)
	}
	q.push(nil, 1, time.Now()) // nil 输入不 panic
	if got := q.Len(); got != 0 {
		t.Fatalf("Len after nil push = %d, want 0", got)
	}
	if got := q.due(time.Now(), 0); got != nil { // max<=0 guard, 同 store.go 的 PeekBatch
		t.Fatalf("due(max=0) = %v, want nil", got)
	}
	if got := q.due(time.Now(), -1); got != nil {
		t.Fatalf("due(max=-1) = %v, want nil", got)
	}
}

func TestRetryQueue_DrainAll(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	future := time.Now().Add(time.Hour)
	q.push([]protocol.UsageLogEntry{entry("a"), entry("b")}, 1, future) // not due normally
	all := q.drainAll()
	if len(all) != 2 {
		t.Fatalf("drainAll len = %d, want 2 (ignores nextAt)", len(all))
	}
	if got := q.Len(); got != 0 {
		t.Fatalf("Len after drainAll = %d, want 0", got)
	}
	if got := q.drainAll(); got != nil {
		t.Fatalf("drainAll on empty = %v, want nil", got)
	}
}

func TestRetryQueueManagementOps(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	big := protocol.UsageLogEntry{RequestID: "big", Timestamp: 100,
		TraceData: `{"inbound_body":"` + strings.Repeat("x", 4096) + `"}`}
	small := protocol.UsageLogEntry{RequestID: "small", Timestamp: 200}
	q.push([]protocol.UsageLogEntry{big, small}, 1, time.Now().Add(time.Hour))

	// snapshotTop 按体积降序
	top := q.snapshotTop(50)
	if len(top) != 2 || top[0].entry.RequestID != "big" {
		t.Fatalf("snapshotTop order wrong: %+v", top)
	}
	if q.totalBytes() <= 0 || q.oldestTimestamp() != 100 {
		t.Fatalf("bytes=%d oldest=%d", q.totalBytes(), q.oldestTimestamp())
	}

	// retryNow 点名:只清 big 的退避
	if n := q.retryNow([]string{"big"}); n != 1 {
		t.Fatalf("retryNow hit = %d, want 1", n)
	}
	due := q.due(time.Now(), 10)
	if len(due) != 1 || due[0].entry.RequestID != "big" {
		t.Fatalf("only big should be due, got %+v", due)
	}
	q.pushItem(due[0]) // 放回

	// degrade 就地剥离并缩体积
	before := q.totalBytes()
	if n := q.degrade([]string{"big"}, DegradeStripTrace); n != 1 {
		t.Fatalf("degrade hit = %d, want 1", n)
	}
	if q.totalBytes() >= before {
		t.Fatal("degrade must shrink totalBytes")
	}
	top = q.snapshotTop(50)
	for _, it := range top {
		if it.entry.RequestID == "big" && (it.degrade != DegradeStripTrace || it.entry.TraceData != "") {
			t.Fatalf("big not degraded in place: %+v", it)
		}
	}
	// 只升不降
	q.degrade([]string{"big"}, DegradeBillingOnly)
	if n := q.degrade([]string{"big"}, DegradeStripTrace); n != 0 {
		t.Fatal("downgrade attempt must be a no-op miss")
	}

	// remove
	if n := q.remove([]string{"small", "ghost"}); n != 1 {
		t.Fatalf("remove hit = %d, want 1", n)
	}
	if q.Len() != 1 {
		t.Fatalf("len = %d, want 1", q.Len())
	}
}

func TestRetryNowAllAndEmptyQueue(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	if n := q.retryNow(nil); n != 0 {
		t.Fatal("empty queue retryNow must return 0")
	}
	q.push([]protocol.UsageLogEntry{{RequestID: "a"}, {RequestID: "b"}}, 2, time.Now().Add(time.Hour))
	if n := q.retryNow(nil); n != 2 {
		t.Fatalf("retryNow(nil) = %d, want 2", n)
	}
	if len(q.due(time.Now(), 10)) != 2 {
		t.Fatal("all items should be due after retryNow(nil)")
	}
}

func TestPushItemPreservesDegradeAndBytes(t *testing.T) {
	q := newRetryQueue(10, zap.NewNop())
	it := retryItem{entry: protocol.UsageLogEntry{RequestID: "a"}, attempts: 5,
		nextAt: time.Now().Add(time.Minute), bytes: 42, degrade: DegradeStripTrace}
	q.pushItem(it)
	got := q.snapshotTop(1)[0]
	if got.attempts != 5 || got.degrade != DegradeStripTrace || got.bytes != 42 {
		t.Fatalf("pushItem lost fields: %+v", got)
	}
}
