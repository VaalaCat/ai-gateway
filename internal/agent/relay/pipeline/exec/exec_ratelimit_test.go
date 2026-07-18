package exec

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

type noopLease struct{}

func (noopLease) Release() {}

type stubGate struct {
	onRequest func() (state.RateLease, error)
	onAttempt func(a state.Attempt) (state.RateLease, error)
}

func (g stubGate) AcquireRequest(*state.RelayContext) (state.RateLease, error) {
	if g.onRequest != nil {
		return g.onRequest()
	}
	return noopLease{}, nil
}
func (g stubGate) AcquireAttempt(_ *state.RelayContext, a state.Attempt) (state.RateLease, error) {
	if g.onAttempt != nil {
		return g.onAttempt(a)
	}
	return noopLease{}, nil
}

func TestExecutor_RequestRateLimited_NoDispatch(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{}}}
	gate := stubGate{
		onRequest: func() (state.RateLease, error) { return nil, state.ErrRateLimited },
	}
	e := newLocalTestExecutor(backend, nil, gate)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "x", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	require.ErrorIs(t, rctx.State.Execution.Err, state.ErrRateLimited)
	require.Equal(t, 0, backend.callCount, "请求级拒绝不应 dispatch")
	require.False(t, rctx.State.Execution.ProviderDispatched)
}

func TestExecutor_AttemptRateLimited_FallsBack(t *testing.T) {
	backend := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 7}}}
	gate := stubGate{
		onAttempt: func(a state.Attempt) (state.RateLease, error) {
			if a.Channel.ID == 1 {
				return nil, state.ErrRateLimited // 渠道 1 被限 → fallback
			}
			return noopLease{}, nil
		},
	}
	e := newLocalTestExecutor(backend, nil, gate)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "x", Mode: state.ModeNative},
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 2}}, RealModel: "x", Mode: state.ModeNative},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	e.Run(rctx)

	require.NoError(t, rctx.State.Execution.Err)
	require.Equal(t, 1, backend.callCount, "只有渠道 2 实际 dispatch")
	require.Equal(t, uint(2), rctx.State.Execution.Used.Channel.ID)
	require.True(t, rctx.State.Execution.ProviderDispatched)
}
