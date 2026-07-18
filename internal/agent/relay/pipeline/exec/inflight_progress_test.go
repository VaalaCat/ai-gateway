package exec

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
)

// snapshotCapturingDispatcher 每次 Dispatch 被调时抓一份当时的在途快照(取第 0 条),
// 用来断言"打某候选之前:已完成尝试链 + 进行中候选"的中途态。
type snapshotCapturingDispatcher struct {
	reg       *inflight.Registry
	results   []state.AttemptResult
	callCount int
	snaps     []inflight.Snapshot
}

func (d *snapshotCapturingDispatcher) Dispatch(_ *state.RelayContext, _ state.Attempt) state.AttemptResult {
	if all := d.reg.Snapshot(); len(all) > 0 {
		d.snaps = append(d.snaps, all[0])
	}
	var res state.AttemptResult
	if d.callCount < len(d.results) {
		res = d.results[d.callCount]
	}
	d.callCount++
	return res
}

func trackInflight(rctx *state.RelayContext) *inflight.Registry {
	reg := inflight.NewRegistry(nil, 0)
	rctx.Inflight = reg.Track(inflight.Meta{ReqID: "r1"})
	return reg
}

func chAttempt(id uint, name string) state.Attempt {
	return state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: id, Name: name}},
		RealModel: "gpt-4",
		Mode:      state.ModeNative,
	}
}

func TestExecutor_Inflight_SingleSuccess_CurrentThenCleared(t *testing.T) {
	d := &snapshotCapturingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	plan := state.AttemptPlan{Attempts: []state.Attempt{chAttempt(1, "c1")}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	reg := trackInflight(rctx)
	d.reg = reg
	(newLocalTestExecutor(d, nil, nil)).Run(rctx)

	if len(d.snaps) != 1 || d.snaps[0].CurrentAttempt == nil || d.snaps[0].CurrentAttempt.ChannelName != "c1" {
		t.Fatalf("current attempt at dispatch time wrong: %+v", d.snaps)
	}
	final := reg.Snapshot()[0]
	if final.CurrentAttempt != nil {
		t.Fatalf("current attempt should be cleared after Run, got %+v", final.CurrentAttempt)
	}
	if len(final.View.FallbackChain) != 1 || final.View.FallbackChain[0].ChannelName != "c1" {
		t.Fatalf("final fallback chain wrong: %+v", final.View.FallbackChain)
	}
}

func TestExecutor_Inflight_FallbackSecond_ShowsPriorAttemptAndCurrent(t *testing.T) {
	d := &snapshotCapturingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("first failed")},
		{PromptTokens: 7},
	}}
	plan := state.AttemptPlan{Attempts: []state.Attempt{chAttempt(1, "c1"), chAttempt(2, "c2")}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	reg := trackInflight(rctx)
	d.reg = reg
	(newLocalTestExecutor(d, nil, nil)).Run(rctx)

	if len(d.snaps) != 2 {
		t.Fatalf("want 2 dispatch snapshots, got %d", len(d.snaps))
	}
	second := d.snaps[1]
	if len(second.View.FallbackChain) != 1 || second.View.FallbackChain[0].ChannelName != "c1" || second.View.FallbackChain[0].Status != "fail" {
		t.Fatalf("prior settled attempt missing at 2nd dispatch: %+v", second.View.FallbackChain)
	}
	if second.CurrentAttempt == nil || second.CurrentAttempt.ChannelName != "c2" || second.CurrentAttempt.Seq != 2 {
		t.Fatalf("current attempt at 2nd dispatch wrong: %+v", second.CurrentAttempt)
	}
}

func TestExecutor_Inflight_BreakerOpenSkip_MarkedInChain(t *testing.T) {
	d := &snapshotCapturingDispatcher{results: []state.AttemptResult{
		{Err: resilience.ErrBreakerOpen},
		{PromptTokens: 9},
	}}
	plan := state.AttemptPlan{Attempts: []state.Attempt{chAttempt(1, "c1"), chAttempt(2, "c2")}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	reg := trackInflight(rctx)
	d.reg = reg
	(newLocalTestExecutor(d, nil, nil)).Run(rctx)

	final := reg.Snapshot()[0]
	if len(final.View.FallbackChain) != 2 {
		t.Fatalf("want 2 attempts in chain, got %+v", final.View.FallbackChain)
	}
	if !final.View.FallbackChain[0].BreakerOpen {
		t.Fatalf("first attempt should be marked breaker_open: %+v", final.View.FallbackChain[0])
	}
	if final.View.FallbackChain[1].Status != "ok" {
		t.Fatalf("second attempt should be ok: %+v", final.View.FallbackChain[1])
	}
}
