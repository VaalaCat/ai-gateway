package system

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/gin-gonic/gin"
)

// isBadRequest 判断 err 是否是 HTTP 400 的 APIError。
func isBadRequest(err error) bool {
	if err == nil {
		return false
	}
	apiErr, ok := err.(*api.APIError)
	return ok && apiErr.Status == http.StatusBadRequest
}

// newSettingsContext 构造一个带 SQLite DB 和内存事件总线的测试 Context。
func newSettingsContext(t *testing.T) *app.Context {
	t.Helper()
	db := setupTestDB(t)
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	testApp := app.NewApplication()
	testApp.SetDB(db)
	testApp.SetEventBus(eventbus.NewMemoryBus())
	return &app.Context{
		Context: ginCtx,
		App:     testApp,
	}
}

// TestUpdateSettings_BYOKBillingMode_GarbageRejected 是核心回归测试：
// byok_billing_mode="garbage" 应该被拒绝（BadRequest）。
// 在 fix 之前，由于 continue 跳过了 settingDefs 校验，该测试会失败（错误地 pass）。
func TestUpdateSettings_BYOKBillingMode_GarbageRejected(t *testing.T) {
	c := newSettingsContext(t)
	h := &Handler{}

	_, err := h.UpdateSettings(c, UpdateSettingsRequest{
		Settings: map[string]string{
			consts.SettingKeyBYOKBillingMode: "garbage",
		},
	})

	if !isBadRequest(err) {
		t.Errorf("UpdateSettings(byok_billing_mode=garbage): want BadRequest, got %v", err)
	}
}

// TestUpdateSettings_BYOKBillingMode_ValidValuesAccepted 回归守卫：
// "free" 和 "service_fee" 两个合法枚举值应该被接受。
func TestUpdateSettings_BYOKBillingMode_ValidValuesAccepted(t *testing.T) {
	for _, v := range []string{consts.BYOKBillingModeFree, consts.BYOKBillingModeServiceFee} {
		c := newSettingsContext(t)
		h := &Handler{}

		_, err := h.UpdateSettings(c, UpdateSettingsRequest{
			Settings: map[string]string{
				consts.SettingKeyBYOKBillingMode: v,
			},
		})

		if err != nil {
			t.Errorf("UpdateSettings(byok_billing_mode=%q): want success, got %v", v, err)
		}
	}
}

// TestUpdateSettings_AgentOnlyIntKey_Accepted 回归守卫：
// retry_max_channels 是纯 agent key，合法整数应该被接受。
func TestUpdateSettings_AgentOnlyIntKey_Accepted(t *testing.T) {
	c := newSettingsContext(t)
	h := &Handler{}

	_, err := h.UpdateSettings(c, UpdateSettingsRequest{
		Settings: map[string]string{
			"retry_max_channels": "3",
		},
	})

	if err != nil {
		t.Errorf("UpdateSettings(retry_max_channels=3): want success, got %v", err)
	}
}

// TestUpdateSettings_AgentOnlyIntKey_BadValueRejected 回归守卫：
// retry_max_channels="notanumber" 应该被拒绝（BadRequest）。
func TestUpdateSettings_AgentOnlyIntKey_BadValueRejected(t *testing.T) {
	c := newSettingsContext(t)
	h := &Handler{}

	_, err := h.UpdateSettings(c, UpdateSettingsRequest{
		Settings: map[string]string{
			"retry_max_channels": "notanumber",
		},
	})

	if !isBadRequest(err) {
		t.Errorf("UpdateSettings(retry_max_channels=notanumber): want BadRequest, got %v", err)
	}
}

func TestUpdateSettings_BreakerEnabled_AcceptsZeroAndOne(t *testing.T) {
	for _, v := range []string{"0", "1"} {
		t.Run(v, func(t *testing.T) {
			c := newSettingsContext(t)
			h := Handler{}
			_, err := h.UpdateSettings(c, UpdateSettingsRequest{
				Settings: map[string]string{"breaker_enabled": v},
			})
			if err != nil {
				t.Errorf("UpdateSettings(breaker_enabled=%s): want success, got %v", v, err)
			}
		})
	}
}

func TestUpdateSettings_BreakerEnabled_BadValueRejected(t *testing.T) {
	c := newSettingsContext(t)
	h := Handler{}
	_, err := h.UpdateSettings(c, UpdateSettingsRequest{
		Settings: map[string]string{"breaker_enabled": "2"},
	})
	if !isBadRequest(err) {
		t.Fatal("UpdateSettings(breaker_enabled=2): want BadRequest")
	}
}
