package limiter

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

type stubResolver struct {
	req     []*models.RequestLimiter
	attempt []*models.RequestLimiter
}

func (s stubResolver) EffectiveRequestLimiters(uint, uint) []*models.RequestLimiter { return s.req }
func (s stubResolver) EffectiveAttemptLimiters(uint, uint, string, uint) []*models.RequestLimiter {
	return s.attempt
}

func rctxWithUser(userID, groupID uint) *state.RelayContext {
	return &state.RelayContext{Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: userID, GroupID: groupID}}}
}

func TestGate_RequestLevel_RejectWhenFull(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "per_user", Action: "reject", Enabled: true},
	}}
	g := NewGate(res, NewMemStore())

	l1, err := g.AcquireRequest(rctxWithUser(7, 0))
	require.NoError(t, err)
	require.NotNil(t, l1)

	_, err = g.AcquireRequest(rctxWithUser(7, 0)) // 同用户第二个 → 满 → 拒
	require.ErrorIs(t, err, state.ErrRateLimited)

	l1.Release()
	l3, err := g.AcquireRequest(rctxWithUser(7, 0)) // 释放后又能占
	require.NoError(t, err)
	l3.Release()

	_, err = g.AcquireRequest(rctxWithUser(99, 0)) // 不同用户独立桶 → 放行
	require.NoError(t, err)
}

func TestGate_AllOrNothing_ReleaseOnPartialFail(t *testing.T) {
	// A 容量 5(够)、B 容量 1(占满)。第二个请求整体拒，且 A 不被泄漏占用。
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Name: "A", Metric: "concurrency", Capacity: 5, KeyBy: "shared", Action: "reject", Enabled: true},
		{ID: 2, Name: "B", Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "reject", Enabled: true},
	}}
	store := NewMemStore()
	g := NewGate(res, store)

	l1, err := g.AcquireRequest(rctxWithUser(1, 0))
	require.NoError(t, err)

	_, err = g.AcquireRequest(rctxWithUser(1, 0)) // B 满 → 整体拒
	require.ErrorIs(t, err, state.ErrRateLimited)

	l1.Release() // 释放 l1 占的 A+B
	for i := 0; i < 5; i++ {
		_, ok := store.TryConcurrency(BucketKey{LimiterID: 1, Bucket: "shared"}, 5)
		require.True(t, ok, "A 第 %d 次应可占（partial-fail 未泄漏）", i+1)
	}
}

func TestAcquireWaiting_MarksInflightQueued(t *testing.T) {
	res := stubResolver{req: []*models.RequestLimiter{
		{ID: 1, Name: "w", Metric: "concurrency", Capacity: 1, KeyBy: "shared", Action: "wait", QueueSize: 5, QueueTimeMs: 5000, Enabled: true},
	}}
	store := NewMemStore()
	g := NewGate(res, store)

	// 占住唯一名额，让第二个请求必须排队。
	l1, err := g.AcquireRequest(rctxWithUser(1, 0))
	require.NoError(t, err)

	// 第二个请求带 inflight 句柄。
	reg := inflight.NewRegistry(nil, 0)
	rctx2 := rctxWithUser(1, 0)
	rctx2.Inflight = reg.Track(inflight.Meta{ReqID: "q1", StartTime: time.Now()})

	done := make(chan error, 1)
	go func() {
		lease, err := g.AcquireRequest(rctx2) // 在 acquireWaiting 里阻塞
		if lease != nil {
			lease.Release()
		}
		done <- err
	}()

	// 轮询直到看到排队标记（不靠固定 sleep 命中竞态窗口）。
	deadline := time.After(2 * time.Second)
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	sawQueued := false
	for !sawQueued {
		select {
		case <-deadline:
			t.Fatal("超时仍未看到 inflight 进入 ratelimit_wait 排队")
		case <-tick.C:
			snaps := reg.Snapshot()
			require.Len(t, snaps, 1)
			if snaps[0].Stage == "ratelimit_wait" {
				require.NotEmpty(t, snaps[0].QueuedReason, "排队时应带原因")
				require.GreaterOrEqual(t, snaps[0].QueuedMs, int64(0))
				sawQueued = true
			}
		}
	}

	// 释放名额，排队请求应能拿到并退出。
	l1.Release()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("释放名额后排队请求未完成")
	}

	// Unqueue 已跑：QueuedMs 归 0、原因清空。
	snaps := reg.Snapshot()
	require.Len(t, snaps, 1)
	require.Equal(t, int64(0), snaps[0].QueuedMs, "Unqueue 后 QueuedMs 应为 0")
	require.Empty(t, snaps[0].QueuedReason, "Unqueue 后原因应清空")
}

func TestGate_AttemptLevel_PerChannelBucket(t *testing.T) {
	res := stubResolver{attempt: []*models.RequestLimiter{
		{ID: 1, Metric: "concurrency", Capacity: 1, KeyBy: "per_channel", ChannelScope: "admin", Action: "reject", Enabled: true},
	}}
	g := NewGate(res, NewMemStore())
	a45 := state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 45}}, Source: state.SourceAdmin, SourceID: 45}
	a99 := state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 99}}, Source: state.SourceAdmin, SourceID: 99}

	l45, err := g.AcquireAttempt(rctxWithUser(1, 0), a45)
	require.NoError(t, err)
	_, err = g.AcquireAttempt(rctxWithUser(1, 0), a45) // 渠道 45 满
	require.ErrorIs(t, err, state.ErrRateLimited)
	_, err = g.AcquireAttempt(rctxWithUser(1, 0), a99) // 渠道 99 独立桶 → 放行
	require.NoError(t, err)
	l45.Release()
}
