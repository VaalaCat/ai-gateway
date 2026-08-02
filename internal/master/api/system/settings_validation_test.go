package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

const relayDefaultURIKey = "agent.relay_default_uri"

func TestSetupTestDBClosesOwnedDatabaseAfterTestCleanup(t *testing.T) {
	var ping func() error
	t.Run("fixture", func(t *testing.T) {
		db := setupTestDB(t)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		require.NoError(t, sqlDB.Ping())
		ping = sqlDB.Ping
	})

	require.NotNil(t, ping)
	require.Error(t, ping())
}

type settingsRecordingBus struct {
	*eventbus.MemoryBus
	mu        sync.Mutex
	contexts  []context.Context
	events    []eventbus.Event
	onPublish func()
	failAt    map[int]error
}

func newSettingsRecordingBus() *settingsRecordingBus {
	return &settingsRecordingBus{MemoryBus: eventbus.NewMemoryBus()}
}

func (b *settingsRecordingBus) Publish(ctx context.Context, event eventbus.Event) error {
	b.mu.Lock()
	attempt := len(b.events)
	b.contexts = append(b.contexts, ctx)
	b.events = append(b.events, event)
	onPublish := b.onPublish
	publishErr := b.failAt[attempt]
	b.mu.Unlock()

	if onPublish != nil {
		onPublish()
	}
	if publishErr != nil {
		return publishErr
	}
	return b.MemoryBus.Publish(ctx, event)
}

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
	c, _ := newSettingsContextWithBus(t)
	return c
}

func newSettingsContextWithBus(t *testing.T) (*app.Context, *settingsRecordingBus) {
	t.Helper()
	db := setupTestDB(t)
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Request = httptest.NewRequest(http.MethodPut, "/api/admin/system/settings", nil)
	testApp := app.NewApplication()
	testApp.SetCoreDB(db)
	bus := newSettingsRecordingBus()
	testApp.SetEventBus(bus)
	t.Cleanup(func() { require.NoError(t, bus.Close()) })
	return &app.Context{
		Context:      ginCtx,
		App:          testApp,
		OwnerContext: t.Context(),
	}, bus
}

func TestUpdateSettings_RelayDefaultURIRejectsInvalidBeforeWritesOrPublishes(t *testing.T) {
	t.Parallel()

	const secret = "relay-setting-secret"
	tests := map[string]string{
		"http":               "http://relay.example/tunnel?token=" + secret,
		"relative":           "/tunnel?token=" + secret,
		"missing hostname":   "ws:///tunnel?token=" + secret,
		"userinfo":           "wss://user:pass@relay.example/tunnel?token=" + secret,
		"fragment":           "wss://relay.example/tunnel?token=" + secret + "#fragment",
		"empty fragment":     "wss://relay.example/tunnel#",
		"malformed query":    "wss://relay.example/tunnel?token=" + secret + "&bad=%zz",
		"too many bytes":     "wss://relay.example/" + strings.Repeat("a", 2049-len("wss://relay.example/")),
		"canonical overflow": "wss://relay.example/" + strings.Repeat("a", 2048-len("wss://relay.example/")-len("界")) + "界",
		"whitespace only":    "   ",
	}

	for name, raw := range tests {
		name, raw := name, raw
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, bus := newSettingsContextWithBus(t)
			_, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
				relayDefaultURIKey:  raw,
				"fallback_sleep_ms": "2500",
			}})

			require.True(t, isBadRequest(err), "error = %v", err)
			require.EqualError(t, err, "invalid value for "+relayDefaultURIKey)
			require.NotContains(t, err.Error(), secret)
			require.NotContains(t, err.Error(), raw)
			var count int64
			require.NoError(t, c.App.GetCoreDB().Model(&models.Setting{}).Count(&count).Error)
			require.Zero(t, count)
			require.Empty(t, bus.events)
		})
	}
}

func TestUpdateSettings_RelayDefaultURIAcceptsAndStoresEmptyWSAndWSS(t *testing.T) {
	t.Parallel()
	const prefix = "wss://relay.example/"
	padding := strings.Repeat("a", 2048-len(prefix)-len("%E7%95%8C"))

	tests := map[string]struct {
		raw  string
		want string
	}{
		"empty":                      {raw: "", want: ""},
		"ws":                         {raw: "ws://relay.example/tunnel?token=secret", want: "ws://relay.example/tunnel?token=secret"},
		"wss":                        {raw: "  WSS://relay.example/tunnel?token=secret  ", want: "wss://relay.example/tunnel?token=secret"},
		"canonical storage boundary": {raw: prefix + padding + "界", want: prefix + padding + "%E7%95%8C"},
	}

	for name, tt := range tests {
		name, tt := name, tt
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			c, bus := newSettingsContextWithBus(t)
			response, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
				relayDefaultURIKey: tt.raw,
			}})
			require.NoError(t, err)
			require.Equal(t, tt.want, response.Settings[relayDefaultURIKey])

			var stored models.Setting
			require.NoError(t, c.App.GetCoreDB().Where("key = ?", relayDefaultURIKey).First(&stored).Error)
			require.Equal(t, tt.want, stored.Value)
			require.LessOrEqual(t, len(stored.Value), 2048)
			require.Len(t, bus.events, 1)
			require.Equal(t, events.SettingUpdateTopic.Value(), bus.events[0].Topic)
			var payload models.Setting
			require.NoError(t, json.Unmarshal(bus.events[0].Payload, &payload))
			require.Equal(t, models.Setting{Key: relayDefaultURIKey, Value: tt.want}, payload)
		})
	}
}

type settingsContextKey struct{}

func TestUpdateSettings_PublishesWithRequestContext(t *testing.T) {
	c, bus := newSettingsContextWithBus(t)
	const marker = "settings-request-context"
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), settingsContextKey{}, marker))

	_, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		relayDefaultURIKey: "wss://relay.example/tunnel",
	}})
	require.NoError(t, err)
	require.Len(t, bus.contexts, 1)
	require.Equal(t, marker, bus.contexts[0].Value(settingsContextKey{}))
}

func TestUpdateSettingsEmptyRequestsReleaseSerialization(t *testing.T) {
	c, bus := newSettingsContextWithBus(t)
	h := &Handler{}
	for _, empty := range []map[string]string{nil, {}} {
		_, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: empty})
		require.NoError(t, err)
	}
	_, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{"fallback_sleep_ms": "0"}})
	require.NoError(t, err)
	require.Len(t, bus.events, 1)
}

func TestUpdateSettingsCommittedPublishFailuresReturnAuthoritativeSuccess(t *testing.T) {
	const secret = "must-not-appear-in-log"
	for _, failureIndex := range []int{0, 1, 2} {
		t.Run([]string{"first", "middle", "last"}[failureIndex], func(t *testing.T) {
			c, bus := newSettingsContextWithBus(t)
			bus.failAt = map[int]error{failureIndex: errors.New("publish failed with " + secret)}
			core, observed := observer.New(zap.WarnLevel)
			c.Logger = zap.New(core)
			response, err := (&Handler{}).UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
				consts.SettingAgentRelayDefaultURI: "wss://relay.example/tunnel?token=" + secret,
				"affinity_enabled":                 "0",
				"fallback_sleep_ms":                "0",
			}})
			require.NoError(t, err)
			require.Equal(t, "wss://relay.example/tunnel?token="+secret, response.Settings[consts.SettingAgentRelayDefaultURI])
			require.Equal(t, "0", response.Settings["affinity_enabled"])
			require.Equal(t, "0", response.Settings["fallback_sleep_ms"])

			require.Len(t, bus.events, 3)
			publishedKeys := make([]string, 0, 3)
			for _, event := range bus.events {
				var setting models.Setting
				require.NoError(t, json.Unmarshal(event.Payload, &setting))
				publishedKeys = append(publishedKeys, setting.Key)
			}
			require.Equal(t, []string{
				"affinity_enabled",
				consts.SettingAgentRelayDefaultURI,
				"fallback_sleep_ms",
			}, publishedKeys)

			var count int64
			require.NoError(t, c.App.GetCoreDB().Model(&models.Setting{}).Where("key IN ?", publishedKeys).Count(&count).Error)
			require.EqualValues(t, 3, count)
			require.Equal(t, 1, observed.Len())
			logText := observed.All()[0].Message + observed.All()[0].ContextMap()["code"].(string)
			require.NotContains(t, logText, secret)
			require.Equal(t, "settings_publish_after_commit_failed", observed.All()[0].ContextMap()["code"])
			require.EqualValues(t, 1, observed.All()[0].ContextMap()["failed"])
			require.EqualValues(t, 3, observed.All()[0].ContextMap()["attempted"])
		})
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

func TestConnectivityProbeTimingSettingsDefaultsValidationAndRefresh(t *testing.T) {
	c := newSettingsContext(t)
	const marker = "probe-timing-refresh-context"
	c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), settingsContextKey{}, marker))
	refreshCalls := 0
	var refreshContext context.Context
	h := &Handler{RefreshProbeTimings: func(ctx context.Context) {
		refreshCalls++
		refreshContext = ctx
	}}

	defaults, err := h.GetSettings(c, GetSettingsRequest{})
	require.NoError(t, err)
	require.Equal(t, "300", defaults.Settings[consts.SettingAgentConnectivityProbeSuccessTTLSeconds])
	require.Equal(t, "30", defaults.Settings[consts.SettingAgentConnectivityProbeFailureRetryMinSeconds])
	require.Equal(t, "300", defaults.Settings[consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds])

	updated, err := h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		consts.SettingAgentConnectivityProbeSuccessTTLSeconds:      "120",
		consts.SettingAgentConnectivityProbeFailureRetryMinSeconds: "15",
		consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds: "90",
	}})
	require.NoError(t, err)
	require.Equal(t, 1, refreshCalls)
	require.Equal(t, marker, refreshContext.Value(settingsContextKey{}))
	require.Equal(t, "120", updated.Settings[consts.SettingAgentConnectivityProbeSuccessTTLSeconds])
	require.Equal(t, "15", updated.Settings[consts.SettingAgentConnectivityProbeFailureRetryMinSeconds])
	require.Equal(t, "90", updated.Settings[consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds])

	_, err = h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		consts.SettingAgentConnectivityProbeSuccessTTLSeconds: "29",
	}})
	require.True(t, isBadRequest(err))
	_, err = h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		consts.SettingAgentConnectivityProbeFailureRetryMinSeconds: "91",
	}})
	require.True(t, isBadRequest(err), "stored max is 90, so a partial min update must preserve the cross-field invariant")
	_, err = h.UpdateSettings(c, UpdateSettingsRequest{Settings: map[string]string{
		consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds: "3601",
	}})
	require.True(t, isBadRequest(err))
	require.Equal(t, 1, refreshCalls, "rejected settings must not update the live scheduler")
}

func TestBillingLogRetentionSettingsDefaultAndRange(t *testing.T) {
	got, err := (&Handler{}).GetSettings(newSettingsContext(t), GetSettingsRequest{})
	require.NoError(t, err)
	require.Equal(t, "5", got.Settings[consts.SettingKeyBillingLogRetentionDays])

	for _, value := range []string{"1", "5", "365"} {
		t.Run("accept_"+value, func(t *testing.T) {
			updated, updateErr := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{
				consts.SettingKeyBillingLogRetentionDays: value,
			}})
			require.NoError(t, updateErr)
			require.Equal(t, value, updated.Settings[consts.SettingKeyBillingLogRetentionDays])
		})
	}
	for _, value := range []string{"0", "366", "not-a-number"} {
		t.Run("reject_"+value, func(t *testing.T) {
			_, updateErr := (&Handler{}).UpdateSettings(newSettingsContext(t), UpdateSettingsRequest{Settings: map[string]string{
				consts.SettingKeyBillingLogRetentionDays: value,
			}})
			require.True(t, isBadRequest(updateErr), "error = %v", updateErr)
		})
	}
	require.NotContains(t, settings.Defaults(), consts.SettingKeyBillingLogRetentionDays)
}
