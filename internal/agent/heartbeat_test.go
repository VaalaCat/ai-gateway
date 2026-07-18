package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"go.uber.org/zap"
)

type fakeCaller struct {
	err      error
	calls    int
	closeCnt int
}

func (f *fakeCaller) Call(_ context.Context, _ string, _ any) (json.RawMessage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return json.RawMessage(`{"ok":true}`), nil
}
func (f *fakeCaller) Close() error { f.closeCnt++; return nil }

func TestHeartbeatTick_FailureNeverCloses(t *testing.T) {
	s := &Server{Logger: zap.NewNop()}
	f := &fakeCaller{err: errors.New("context deadline exceeded")}
	failures := 0
	for i := 0; i < 5; i++ { // 远超旧阈值 3
		failures = s.heartbeatTick(context.Background(), f, protocol.HeartbeatParams{}, 50*time.Millisecond, failures)
	}
	if failures != 5 {
		t.Fatalf("failures = %d, want 5 (计数保留用于诊断)", failures)
	}
	if f.closeCnt != 0 {
		t.Fatalf("Close called %d times, want 0 — 心跳失败不得断连", f.closeCnt)
	}
}

func TestHeartbeatTick_SuccessResets(t *testing.T) {
	s := &Server{Logger: zap.NewNop()}
	f := &fakeCaller{}
	failures := s.heartbeatTick(context.Background(), f, protocol.HeartbeatParams{}, 50*time.Millisecond, 4)
	if failures != 0 {
		t.Fatalf("failures = %d, want 0 after success", failures)
	}
}

func TestHeartbeatTick_NilClientNoop(t *testing.T) { // boundary
	s := &Server{Logger: zap.NewNop()}
	failures := s.heartbeatTick(context.Background(), nil, protocol.HeartbeatParams{}, 50*time.Millisecond, 2)
	if failures != 2 {
		t.Fatalf("failures = %d, want unchanged 2", failures)
	}
}

type blockingHeartbeatCaller struct {
	started chan struct{}
}

func (c *blockingHeartbeatCaller) Call(ctx context.Context, _ string, _ any) (json.RawMessage, error) {
	close(c.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestHeartbeatTickOwnerCancellationInterruptsCall(t *testing.T) {
	s := &Server{Logger: zap.NewNop()}
	caller := &blockingHeartbeatCaller{started: make(chan struct{})}
	ownerCtx, cancel := context.WithCancel(context.Background())
	result := make(chan int, 1)
	go func() {
		result <- s.heartbeatTick(ownerCtx, caller, protocol.HeartbeatParams{}, time.Hour, 0)
	}()
	select {
	case <-caller.started:
	case <-time.After(time.Second):
		t.Fatal("heartbeat call did not start")
	}
	cancel()
	select {
	case failures := <-result:
		if failures != 1 {
			t.Fatalf("failures = %d, want 1", failures)
		}
	case <-time.After(time.Second):
		t.Fatal("owner cancellation did not interrupt heartbeat call")
	}
}
