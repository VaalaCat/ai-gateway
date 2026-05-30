package exec

import (
	"errors"
	"net/http"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// recorderMutatingDispatcher 模拟真实后端(passthrough/native)对 Recorder 的副作用:
// 每次 dispatch 按预设结果把状态写进 rctx.State.Recorder——
//   - 上游错误状态(UpstreamError): WithUpstreamStatus(该码) + WithFail(StageUpstreamStatus)
//   - 成功: WithUpstreamStatus(200)
// recordingDispatcher(exec_test.go 里)是纯 stub 不碰 recorder,无法复现状态泄漏,故单列。
type recorderMutatingDispatcher struct {
	results []state.AttemptResult
	n       int
}

func (d *recorderMutatingDispatcher) Dispatch(rctx *state.RelayContext, _ state.Attempt) state.AttemptResult {
	rec := rctx.State.Recorder
	var res state.AttemptResult
	if d.n < len(d.results) {
		res = d.results[d.n]
	}
	d.n++
	var up *common.UpstreamError
	if res.Err != nil && errors.As(res.Err, &up) {
		rec.WithUpstreamStatus(&http.Response{StatusCode: up.Status, Header: http.Header{}})
		rec.WithFail(trace.StageUpstreamStatus, res.Err)
	} else if res.Err == nil {
		rec.WithUpstreamStatus(&http.Response{StatusCode: http.StatusOK, Header: http.Header{}})
	}
	return res
}

// reproSettings 满足 resilience.SettingsReader: MaxRetries=2(共 3 次 dispatch),
// BreakerThreshold=10 确保 3 次失败不会让熔断器提前 open 截断重试。
type reproSettings struct{}

func (reproSettings) Settings() settings.AgentSettings {
	return settings.AgentSettings{
		MaxRetriesPerChannel: 2,
		RetryBackoffBaseMs:   1,
		RetryBackoffMaxMs:    2,
		BreakerThreshold:     10,
		BreakerCooldownMs:    50,
	}
}

// runResetRepro 用真实 resilience.Runner 跑单候选 + recorder-mutating dispatcher。
func runResetRepro(results []state.AttemptResult) *state.RelayContext {
	d := &recorderMutatingDispatcher{results: results}
	e := &Executor{
		Dispatcher: d,
		Resilience: &resilience.Runner{Settings: reproSettings{}, Breakers: resilience.NewRegistry()},
	}
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 1}}, RealModel: "gpt-4", Source: state.SourceAdmin, Mode: state.ModeNative},
	}}
	rctx := &state.RelayContext{
		Context: nil,
		Agent:   &stubExecAgent{},
		Input:   state.RelayInput{Body: []byte(`{"model":"x"}`), UserInfo: &app.UserInfo{TokenID: 1}},
		State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0), Plan: plan}, // 关 trace
	}
	e.Run(rctx)
	return rctx
}

// TestRun_RecorderResetBetweenInnerRetries_FailThenSuccess 钉死 Defect B:
// [504,504,成功] 后,被采纳的成功候选 snapshot 不得残留旧 504 的失败状态。
func TestRun_RecorderResetBetweenInnerRetries_FailThenSuccess(t *testing.T) {
	rctx := runResetRepro([]state.AttemptResult{
		{Err: &common.UpstreamError{Status: 504}},
		{Err: &common.UpstreamError{Status: 504}},
		{}, // 第 3 次成功
	})
	if rctx.State.Execution.Err != nil {
		t.Fatalf("最后成功,Execution.Err 应为 nil,得到 %v", rctx.State.Execution.Err)
	}
	snap := rctx.State.Recorder.Attempts()[0]
	if snap.FailStage != trace.StageNone {
		t.Errorf("成功收尾的 snapshot.FailStage=%q,应为 %q(旧 504 泄漏)", snap.FailStage, trace.StageNone)
	}
	if snap.Verbose {
		t.Errorf("关 trace 的成功请求 snapshot.Verbose=true(旧 504 泄漏 → has_trace 会误置)")
	}
	if snap.UpstreamStatus != http.StatusOK {
		t.Errorf("snapshot.UpstreamStatus=%d,应为 200(最后一次真实状态)", snap.UpstreamStatus)
	}
}

// TestRun_RecorderResetBetweenInnerRetries_AllFail 边界:全失败时仍要保留失败 trace。
func TestRun_RecorderResetBetweenInnerRetries_AllFail(t *testing.T) {
	rctx := runResetRepro([]state.AttemptResult{
		{Err: &common.UpstreamError{Status: 504}},
		{Err: &common.UpstreamError{Status: 504}},
		{Err: &common.UpstreamError{Status: 504}},
	})
	if rctx.State.Execution.Err == nil {
		t.Fatalf("全失败,Execution.Err 不应为 nil")
	}
	snap := rctx.State.Recorder.Attempts()[0]
	if snap.FailStage != trace.StageUpstreamStatus {
		t.Errorf("全失败 snapshot.FailStage=%q,应为 %q", snap.FailStage, trace.StageUpstreamStatus)
	}
	if snap.UpstreamStatus != 504 {
		t.Errorf("snapshot.UpstreamStatus=%d,应为 504", snap.UpstreamStatus)
	}
	if !snap.Verbose {
		t.Errorf("失败请求 snapshot.Verbose 应为 true")
	}
}

// TestRun_RecorderResetBetweenInnerRetries_FirstTrySuccess 回归:首发即成功不引入脏状态。
func TestRun_RecorderResetBetweenInnerRetries_FirstTrySuccess(t *testing.T) {
	rctx := runResetRepro([]state.AttemptResult{{}})
	if rctx.State.Execution.Err != nil {
		t.Fatalf("首发成功,Execution.Err 应为 nil,得到 %v", rctx.State.Execution.Err)
	}
	snap := rctx.State.Recorder.Attempts()[0]
	if snap.FailStage != trace.StageNone || snap.Verbose {
		t.Errorf("首发成功不应 verbose: FailStage=%q Verbose=%v", snap.FailStage, snap.Verbose)
	}
}
