package limiter

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func rctxWithState(userID uint) *state.RelayContext {
	return &state.RelayContext{
		Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: userID}},
		State: &state.RelayState{},
	}
}

func TestGate_Record_Reject(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Name: "u-conc", Metric: "concurrency", Capacity: 1, KeyBy: "per_user", Action: "reject", Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, _ := g.AcquireRequest(rctxWithState(7))
	_ = l1
	rctx := rctxWithState(7)
	_, err := g.AcquireRequest(rctx) // 满 → 拒
	require.ErrorIs(t, err, state.ErrRateLimited)
	require.NotNil(t, rctx.State.RateLimit)
	require.Equal(t, "rejected", rctx.State.RateLimit.Decision)
	require.Contains(t, rctx.State.RateLimit.Reason, "u-conc")
	require.Len(t, rctx.State.RateLimit.Hits, 1)
	require.Equal(t, "u:7", rctx.State.RateLimit.Hits[0].Bucket, "命中明细应记录具体分桶键")
}

func TestGate_Record_QueueTimeout(t *testing.T) {
	// 排队等满整个 queue_time 后被超时拒绝：必须记录 rejected 决策，且 wait_ms 反映真实排队时长。
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Name: "w-conc", Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 120, Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l1, _ := g.AcquireRequest(rctxWithState(1)) // 占住不放 → 排队 → 超时
	rctx := rctxWithState(1)
	start := time.Now()
	_, err := g.AcquireRequest(rctx)
	require.ErrorIs(t, err, state.ErrRateLimited)
	require.GreaterOrEqual(t, time.Since(start), 100*time.Millisecond)
	require.NotNil(t, rctx.State.RateLimit, "排队超时 429 必须可观测")
	require.Equal(t, "rejected", rctx.State.RateLimit.Decision)
	require.Greater(t, rctx.State.RateLimit.WaitMs, 0, "排队时长应计入 wait_ms")
	require.Contains(t, rctx.State.RateLimit.Reason, "w-conc")
	require.Len(t, rctx.State.RateLimit.Hits, 1)
	require.Equal(t, "shared", rctx.State.RateLimit.Hits[0].Bucket, "shared 规则桶键固定为 shared")
	_ = l1
}

func TestGate_Record_AllowNoCrashWhenStateNil(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "per_user", Action: "reject", Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	l, err := g.AcquireRequest(rctxWithUser(1, 0)) // State 为 nil：记录应跳过、不 panic
	require.NoError(t, err)
	l.Release()
}
