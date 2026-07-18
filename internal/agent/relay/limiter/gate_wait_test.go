package limiter

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGateWaitHonorsRequestCancellation(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 5000, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	held, err := g.AcquireRequest(rctxWithUser(1, 0))
	require.NoError(t, err)
	defer held.Release()

	ctx, cancel := context.WithCancel(context.Background())
	c := &gin.Context{Request: (&http.Request{}).WithContext(ctx)}
	rctx := rctxWithUser(1, 0)
	rctx.Context = c
	done := make(chan error, 1)
	go func() {
		_, err := g.AcquireRequest(rctx)
		done <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("limiter wait ignored request cancellation")
	}
}

func TestGate_Wait_BlockThenAcquire(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 2000, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, err := g.AcquireRequest(rctxWithUser(1, 0))
	require.NoError(t, err)
	go func() { time.Sleep(80 * time.Millisecond); l1.Release() }()

	start := time.Now()
	l2, err := g.AcquireRequest(rctxWithUser(1, 0)) // 排队 ~80ms 后拿到
	require.NoError(t, err)
	require.GreaterOrEqual(t, time.Since(start), 60*time.Millisecond)
	l2.Release()
}

func TestGate_Wait_QueueFull_Reject(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 0, QueueTimeMs: 1000, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, _ := g.AcquireRequest(rctxWithUser(1, 0))
	_, err := g.AcquireRequest(rctxWithUser(1, 0)) // queue_size 0 → 不排 → 拒
	require.ErrorIs(t, err, state.ErrRateLimited)
	l1.Release()
}

func TestGate_Wait_Timeout_Reject(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 120, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, _ := g.AcquireRequest(rctxWithUser(1, 0)) // 占住不放
	start := time.Now()
	_, err := g.AcquireRequest(rctxWithUser(1, 0)) // 等 ~120ms 超时
	require.ErrorIs(t, err, state.ErrRateLimited)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	_ = l1
}

func TestGate_RejectPriorityOverWait(t *testing.T) {
	// 一条 wait(满) + 一条 reject(满)：应立即拒，不排队。
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Name: "w", Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 5000, Enabled: true},
		{ID: 2, Name: "r", Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "reject", Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, _ := g.AcquireRequest(rctxWithUser(1, 0)) // 占满两者
	start := time.Now()
	_, err := g.AcquireRequest(rctxWithUser(1, 0))
	require.ErrorIs(t, err, state.ErrRateLimited)
	require.Less(t, time.Since(start), 60*time.Millisecond, "reject 应立即返回，不排队")
	_ = l1
}
