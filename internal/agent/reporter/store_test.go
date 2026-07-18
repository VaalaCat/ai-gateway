// internal/agent/reporter/store_test.go
package reporter

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func entry(id string) protocol.UsageLogEntry {
	return protocol.UsageLogEntry{RequestID: id}
}

func TestMemStore_AppendPeekAck(t *testing.T) {
	s := NewMemPendingUsageStore(10, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")})
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3", s.Len())
	}
	batch := s.PeekBatch(2)
	if len(batch) != 2 || batch[0].RequestID != "a" || batch[1].RequestID != "b" {
		t.Fatalf("PeekBatch = %+v, want [a b]", batch)
	}
	// Peek 不删除
	if s.Len() != 3 {
		t.Fatalf("Len after peek = %d, want 3", s.Len())
	}
	s.Ack(batch)
	if s.Len() != 1 {
		t.Fatalf("Len after ack = %d, want 1", s.Len())
	}
	rest := s.PeekBatch(10)
	if len(rest) != 1 || rest[0].RequestID != "c" {
		t.Fatalf("rest = %+v, want [c]", rest)
	}
}

func TestMemStore_OverflowDropsOldest(t *testing.T) {
	s := NewMemPendingUsageStore(3, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c"), entry("d")})
	if s.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (capped)", s.Len())
	}
	batch := s.PeekBatch(3)
	if batch[0].RequestID != "b" || batch[2].RequestID != "d" {
		t.Fatalf("batch = %+v, want oldest 'a' dropped", batch)
	}
}

func TestMemStore_EmptyAndBounds(t *testing.T) {
	s := NewMemPendingUsageStore(10, zap.NewNop())
	if got := s.PeekBatch(5); len(got) != 0 {
		t.Fatalf("PeekBatch on empty = %v, want empty", got)
	}
	s.Ack([]protocol.UsageLogEntry{entry("x"), entry("y")}) // ack 持有量之外的条目不 panic
	if s.Len() != 0 {
		t.Fatalf("Len = %d, want 0", s.Len())
	}
	s.Append(nil) // nil 输入不 panic
	if s.Len() != 0 {
		t.Fatalf("Len after nil append = %d, want 0", s.Len())
	}
}

func TestMemStore_ConcurrentAppend(t *testing.T) {
	s := NewMemPendingUsageStore(1000, zap.NewNop())
	done := make(chan struct{})
	for i := 0; i < 10; i++ {
		go func() {
			for j := 0; j < 50; j++ {
				s.Append([]protocol.UsageLogEntry{entry("x")})
			}
			done <- struct{}{}
		}()
	}
	for i := 0; i < 10; i++ {
		<-done
	}
	if s.Len() != 500 {
		t.Fatalf("Len = %d, want 500", s.Len())
	}
}

func TestMemStore_OverflowLogsDropDetails(t *testing.T) {
	core, logs := observer.New(zap.ErrorLevel)
	s := NewMemPendingUsageStore(2, zap.New(core))
	s.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")})
	got := logs.FilterMessage("pending usage store overflow, dropped oldest entries").All()
	if len(got) != 1 {
		t.Fatalf("overflow log entries = %d, want 1", len(got))
	}
	fields := got[0].ContextMap()
	if fields["dropped"] != int64(1) || fields["pending_len"] != int64(2) {
		t.Fatalf("log fields = %v, want dropped=1 pending_len=2", fields)
	}
}

func TestMemStore_NegativeSizeGuards(t *testing.T) {
	s := NewMemPendingUsageStore(10, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")})

	// PeekBatch(-1) should return nil and not panic
	batch := s.PeekBatch(-1)
	if batch != nil {
		t.Fatalf("PeekBatch(-1) = %v, want nil", batch)
	}
	if s.Len() != 3 {
		t.Fatalf("Len after PeekBatch(-1) = %d, want 3", s.Len())
	}

	// PeekBatch(0) should return nil and not panic
	batch = s.PeekBatch(0)
	if batch != nil {
		t.Fatalf("PeekBatch(0) = %v, want nil", batch)
	}
	if s.Len() != 3 {
		t.Fatalf("Len after PeekBatch(0) = %d, want 3", s.Len())
	}

	// Ack(nil) should not panic and state unchanged
	s.Ack(nil)
	if s.Len() != 3 {
		t.Fatalf("Len after Ack(nil) = %d, want 3", s.Len())
	}

	// Ack([]) should not panic and state unchanged
	s.Ack([]protocol.UsageLogEntry{})
	if s.Len() != 3 {
		t.Fatalf("Len after Ack([]) = %d, want 3", s.Len())
	}
}

// TestMemStore_AckByIdentitySurvivesConcurrentOverflow 是 MUST-FIX 回归用例:
// uploader 的 Peek→POST→Ack 窗口不是原子的,期间若并发 Append 触发溢出丢弃了
// peeked 批次里最老的一条,identity-based Ack 必须跳过它(已被溢出日志计过),
// 而不是像旧的按下标计数版本那样连坐删掉后面从未投递过的条目。
//
// behavior change: Ack 允许任意位置删除(支持并发上传);'b' 现在即使不在
// 队首也会被正确移除,不再要求"存活 peeked 条目仍是队首前缀"这个单消费者假设。
func TestMemStore_AckByIdentitySurvivesConcurrentOverflow(t *testing.T) {
	s := NewMemPendingUsageStore(3, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{entry("a"), entry("b"), entry("c")})

	// uploader 开始一次上传:peek 到 [a, b],此时 HTTP 请求正在途中
	peeked := s.PeekBatch(2)
	if len(peeked) != 2 || peeked[0].RequestID != "a" || peeked[1].RequestID != "b" {
		t.Fatalf("peeked = %+v, want [a b]", peeked)
	}

	// 在 RTT 期间,新条目 'd' 到达,store(limit=3) 溢出,从头部丢弃 'a'
	s.Append([]protocol.UsageLogEntry{entry("d")})
	if got := s.Len(); got != 3 {
		t.Fatalf("Len after overflow append = %d, want 3", got)
	}

	// POST 成功返回,uploader 用它 peek 到的批次(仍是 [a, b])去 Ack
	s.Ack(peeked)

	// 'a' 已被溢出丢弃(已计入溢出日志),acking 它应被跳过;
	// 只有真正投递成功的 'b' 该被移除。'c' 和 'd' 从未投递,必须完好保留。
	rest := s.PeekBatch(10)
	ids := make([]string, len(rest))
	for i, e := range rest {
		ids[i] = e.RequestID
	}
	if len(rest) != 2 || rest[0].RequestID != "c" || rest[1].RequestID != "d" {
		t.Fatalf("rest after ack = %v, want [c d] (undelivered 'c' must survive)", ids)
	}
}

func TestAckRemovesFromAnyPosition(t *testing.T) {
	s := NewMemPendingUsageStore(10, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{{RequestID: "a"}, {RequestID: "b"}, {RequestID: "c"}})
	// 乱序回执:先确认中间的 b
	s.Ack([]protocol.UsageLogEntry{{RequestID: "b"}})
	if s.Len() != 2 {
		t.Fatalf("len = %d, want 2", s.Len())
	}
	batch := s.PeekBatch(10)
	if batch[0].RequestID != "a" || batch[1].RequestID != "c" {
		t.Fatalf("got %v, want [a c]", []string{batch[0].RequestID, batch[1].RequestID})
	}
}

func TestAckUnknownIDIsNoop(t *testing.T) {
	s := NewMemPendingUsageStore(10, zap.NewNop())
	s.Append([]protocol.UsageLogEntry{{RequestID: "a"}})
	s.Ack([]protocol.UsageLogEntry{{RequestID: "ghost"}})
	if s.Len() != 1 {
		t.Fatalf("len = %d, want 1", s.Len())
	}
}

func TestBytesAndOldestTimestampTracking(t *testing.T) {
	s := NewMemPendingUsageStore(2, zap.NewNop())
	if s.Bytes() != 0 || s.OldestTimestamp() != 0 {
		t.Fatal("empty store should report zero bytes/oldest")
	}
	s.Append([]protocol.UsageLogEntry{{RequestID: "a", Timestamp: 100}, {RequestID: "b", Timestamp: 200}})
	if s.Bytes() <= 0 {
		t.Fatal("bytes should be positive after append")
	}
	if s.OldestTimestamp() != 100 {
		t.Fatalf("oldest = %d, want 100", s.OldestTimestamp())
	}
	// 溢出丢最老:limit=2 再进一条,a 被丢,oldest 变 200,bytes 相应减少
	before := s.Bytes()
	s.Append([]protocol.UsageLogEntry{{RequestID: "c", Timestamp: 300}})
	if s.OldestTimestamp() != 200 {
		t.Fatalf("oldest after overflow = %d, want 200", s.OldestTimestamp())
	}
	if s.Bytes() >= before+1000 {
		t.Fatal("bytes should account for dropped entry")
	}
	// Ack 后归零
	s.Ack([]protocol.UsageLogEntry{{RequestID: "b"}, {RequestID: "c"}})
	if s.Len() != 0 || s.Bytes() != 0 || s.OldestTimestamp() != 0 {
		t.Fatalf("drained store should be zeroed: len=%d bytes=%d oldest=%d", s.Len(), s.Bytes(), s.OldestTimestamp())
	}
}
