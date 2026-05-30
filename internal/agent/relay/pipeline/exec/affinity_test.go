package exec

import (
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/affinity"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
)

type affStubCfg struct{}

func (affStubCfg) Settings() settings.AgentSettings {
	return settings.AgentSettings{AffinityEnabled: 1, AffinityTTLSec: 300}
}

type failDispatcher struct{}

func (failDispatcher) Dispatch(*state.RelayContext, state.Attempt) state.AttemptResult {
	return state.AttemptResult{Err: errors.New("upstream 500")}
}

func TestExecutor_ForgetsAffinityOnHardFailure(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	key := affinity.Key{UserID: 1, RealModel: "m"}
	eng.Remember(key, state.SourceAdmin, 5)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true},
	}}
	// newTestExecutorRctx 提供完整 Agent 使 maybeForward 不 panic；
	// 覆写 UserInfo 设置真实 UserID，覆写 Plan 为粘性 attempt。
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context = c
	rctx.Input.UserInfo = &app.UserInfo{UserID: 1}

	ex := &Executor{Dispatcher: failDispatcher{}, Affinity: eng}
	ex.Run(rctx)

	if _, ok := eng.Lookup(key); ok {
		t.Fatal("hard failure of affinity attempt should Forget the sticky entry")
	}
}

// TestExecutor_AffinityNotForgottenOnSuccess 验证成功路径不误删粘性记录。
func TestExecutor_AffinityNotForgottenOnSuccess(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	key := affinity.Key{UserID: 2, RealModel: "gpt-4"}
	eng.Remember(key, state.SourceAdmin, 9)

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "gpt-4", Source: state.SourceAdmin, SourceID: 9, ByAffinity: true},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Input.UserInfo = &app.UserInfo{UserID: 2}

	successDisp := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	ex := &Executor{Dispatcher: successDisp, Affinity: eng}
	ex.Run(rctx)

	if _, ok := eng.Lookup(key); !ok {
		t.Fatal("successful affinity attempt should NOT Forget the sticky entry")
	}
}

// TestExecutor_AffinityNilSafe 验证 Affinity==nil 时不 panic（向后兼容）。
func TestExecutor_AffinityNilSafe(t *testing.T) {
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "m", ByAffinity: true},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Input.UserInfo = &app.UserInfo{UserID: 3}

	ex := &Executor{Dispatcher: failDispatcher{}} // Affinity == nil
	// 不 panic 即通过
	ex.Run(rctx)
}
