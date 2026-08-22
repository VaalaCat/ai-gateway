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

func TestExecutor_ForgetsAffinityWhenFailureAdvancesPlan(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	key := affinity.Key{UserID: 1, RealModel: "m"}
	eng.Remember(key, state.SourceAdmin, 5, nil)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true},
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 6},
	}}
	// 覆写 UserInfo 设置真实 UserID，覆写 Plan 为粘性 attempt。
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context = c
	rctx.Input.UserInfo = &app.UserInfo{UserID: 1}

	ex := newLocalTestExecutor(failDispatcher{}, nil, nil)
	ex.Affinity = eng
	ex.Run(rctx)

	if _, ok := eng.Lookup(key); ok {
		t.Fatal("retryable affinity failure should Forget before advancing the plan")
	}
}

func TestExecutor_ForgetsOnlyCurrentTokenAffinity(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	currentKey := affinity.Key{UserID: 1, TokenID: 11, RealModel: "m"}
	otherKey := affinity.Key{UserID: 1, TokenID: 22, RealModel: "m"}
	eng.Remember(currentKey, state.SourceAdmin, 5, nil)
	eng.Remember(otherKey, state.SourceAdmin, 6, nil)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true},
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 7},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context = c
	rctx.Input.UserInfo = &app.UserInfo{UserID: 1, TokenID: 11}
	ex := newLocalTestExecutor(failDispatcher{}, nil, nil)
	ex.Affinity = eng
	ex.Run(rctx)

	if _, ok := eng.Lookup(currentKey); ok {
		t.Fatal("failed affinity attempt should forget the current token entry")
	}
	if _, ok := eng.Lookup(otherKey); !ok {
		t.Fatal("failed affinity attempt must preserve another token entry")
	}
}

func TestExecutor_ForgetsOnlyCurrentSessionAffinity(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	partition := state.AffinityPartition{ByUser: true, ByToken: true, ByModel: true}
	currentIdentity := state.AffinityIdentity{Key: "session-a", Partition: partition}
	otherIdentity := state.AffinityIdentity{Key: "session-b", Partition: partition}
	currentKey := affinity.BuildKey(currentIdentity, 1, 11, "m")
	otherKey := affinity.BuildKey(otherIdentity, 1, 11, "m")
	eng.Remember(currentKey, state.SourceAdmin, 5, nil)
	eng.Remember(otherKey, state.SourceAdmin, 6, nil)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/", nil)
	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 5, ByAffinity: true},
		{Channel: &models.Channel{}, RealModel: "m", Source: state.SourceAdmin, SourceID: 7},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Context = c
	rctx.Input.UserInfo = &app.UserInfo{UserID: 1, TokenID: 11}
	rctx.Input.AffinityIdentity = currentIdentity
	ex := newLocalTestExecutor(failDispatcher{}, nil, nil)
	ex.Affinity = eng
	ex.Run(rctx)

	if _, ok := eng.Lookup(currentKey); ok {
		t.Fatal("failed affinity attempt should forget the current session entry")
	}
	if _, ok := eng.Lookup(otherKey); !ok {
		t.Fatal("failed affinity attempt must preserve another session entry")
	}
}

// TestExecutor_AffinityNotForgottenOnSuccess 验证成功路径不误删粘性记录。
func TestExecutor_AffinityNotForgottenOnSuccess(t *testing.T) {
	eng := affinity.New(affStubCfg{})
	key := affinity.Key{UserID: 2, RealModel: "gpt-4"}
	eng.Remember(key, state.SourceAdmin, 9, nil)

	plan := state.AttemptPlan{Attempts: []state.Attempt{
		{Channel: &models.Channel{}, RealModel: "gpt-4", Source: state.SourceAdmin, SourceID: 9, ByAffinity: true},
	}}
	rctx := newTestExecutorRctx(plan, &stubExecAgent{})
	rctx.Input.UserInfo = &app.UserInfo{UserID: 2}

	successDisp := &recordingDispatcher{results: []state.AttemptResult{{PromptTokens: 5}}}
	ex := newLocalTestExecutor(successDisp, nil, nil)
	ex.Affinity = eng
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

	ex := newLocalTestExecutor(failDispatcher{}, nil, nil) // Affinity == nil
	// 不 panic 即通过
	ex.Run(rctx)
}
