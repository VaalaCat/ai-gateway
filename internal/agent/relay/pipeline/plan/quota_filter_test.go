package plan

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

// withRequest 给 rctx 装一个带 context 的 *http.Request，
// 让 quotaFilter 的 fctx.Rctx.Request.Context() 不 nil-panic。
func withRequest(rctx *state.RelayContext) *state.RelayContext {
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "/", nil)
	rctx.Request = req
	return rctx
}

// quotaTestFixture 装配 quotaFilter 测试用的 FilterContext + 可控 cache。
type quotaTestFixture struct {
	cache *stubAgentCache
	fctx  *FilterContext
}

// newQuotaFixture 构造一个 quotaFilter 测试夹具:
//   - model 在 cache 里映射到 mc（nil 表示不注册→GetModelConfig 返回 nil）
//   - userID>0 时注册 UserInfo;user!=nil 时 cache.GetUser 命中返回它
//   - 注入 settings(MinQuotaReserve / BYOKBillingMode)
func newQuotaFixture(model string, mc *models.ModelConfig, userID uint, user *protocol.SyncedUser, s settings.AgentSettings) *quotaTestFixture {
	cache := &stubAgentCache{
		settings:     s,
		modelConfigs: map[string]*models.ModelConfig{},
		users:        map[uint]*protocol.SyncedUser{},
	}
	if mc != nil {
		cache.modelConfigs[model] = mc
	}
	if user != nil {
		cache.users[userID] = user
	}
	var ui *app.UserInfo
	if userID > 0 {
		ui = &app.UserInfo{UserID: userID}
	}
	rctx := withRequest(newTestRelayContext(cache, model, ui, 0))
	return &quotaTestFixture{
		cache: cache,
		fctx:  &FilterContext{Rctx: rctx, RealModel: model},
	}
}

func adminPaid() ScoredCandidate {
	return ScoredCandidate{Channel: &models.Channel{Free: false}, Source: state.SourceAdmin}
}

func adminFree() ScoredCandidate {
	return ScoredCandidate{Channel: &models.Channel{Free: true}, Source: state.SourceAdmin}
}

func quotaPricedMC() *models.ModelConfig { return &models.ModelConfig{InputPrice: 0.001} }

// serviceFeeSettings: BYOK 计费 + 指定 reserve 阈值。
func serviceFeeSettings(reserve int64) settings.AgentSettings {
	return settings.AgentSettings{
		MinQuotaReserve: reserve,
		BYOKBillingMode: consts.BYOKBillingModeServiceFee,
	}
}

// 1. 未定价模型(nil/all-zero mc) → 全留,且 GetUser 不被查询。
func TestQuotaFilter_UnpricedModel_AllKept_NoUserLookup(t *testing.T) {
	for _, tc := range []struct {
		name string
		mc   *models.ModelConfig
	}{
		{"nil mc", nil},
		{"all-zero mc", &models.ModelConfig{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newQuotaFixture("m", tc.mc, 7, nil, serviceFeeSettings(100))
			in := []ScoredCandidate{adminPaid(), adminPaid()}

			got, code := quotaFilter{}.Apply(fx.fctx, in)

			if len(got) != 2 || code != DropNone {
				t.Fatalf("got len=%d code=%v, want len=2 DropNone", len(got), code)
			}
			if fx.cache.getUserCalls != 0 {
				t.Errorf("GetUser called %d times, want 0 (unpriced → no balance read)", fx.cache.getUserCalls)
			}
		})
	}
}

// 2. 已定价 + 候选全免费 → 全留,GetUser 不被查询。
func TestQuotaFilter_AllFreeCandidates_AllKept_NoUserLookup(t *testing.T) {
	fx := newQuotaFixture("m", quotaPricedMC(), 7, nil, serviceFeeSettings(100))
	in := []ScoredCandidate{adminFree(), adminFree()}

	got, code := quotaFilter{}.Apply(fx.fctx, in)

	if len(got) != 2 || code != DropNone {
		t.Fatalf("got len=%d code=%v, want len=2 DropNone", len(got), code)
	}
	if fx.cache.getUserCalls != 0 {
		t.Errorf("GetUser called %d times, want 0 (all-free → no balance read)", fx.cache.getUserCalls)
	}
}

// 3. 付费 + Quota>reserve → 全留。
func TestQuotaFilter_PaidQuotaAboveReserve_AllKept(t *testing.T) {
	fx := newQuotaFixture("m", quotaPricedMC(), 7,
		&protocol.SyncedUser{ID: 7, Quota: 1000}, serviceFeeSettings(100))
	in := []ScoredCandidate{adminPaid(), adminFree()}

	got, code := quotaFilter{}.Apply(fx.fctx, in)

	if len(got) != 2 || code != DropNone {
		t.Fatalf("got len=%d code=%v, want len=2 DropNone", len(got), code)
	}
}

// 4. 付费+免费混合 + Quota<=reserve → 只留免费,DropNone。
func TestQuotaFilter_PaidFreeMix_QuotaBelowReserve_OnlyFreeKept(t *testing.T) {
	fx := newQuotaFixture("m", quotaPricedMC(), 7,
		&protocol.SyncedUser{ID: 7, Quota: 50}, serviceFeeSettings(100))
	paid := adminPaid()
	free := adminFree()
	in := []ScoredCandidate{paid, free}

	got, code := quotaFilter{}.Apply(fx.fctx, in)

	if code != DropNone {
		t.Fatalf("code = %v, want DropNone", code)
	}
	if len(got) != 1 {
		t.Fatalf("kept len = %d, want 1 (only free)", len(got))
	}
	if !got[0].Channel.Free {
		t.Errorf("kept candidate must be free, got Free=%v", got[0].Channel.Free)
	}
}

// 5. 仅付费 + Quota<=reserve → 空 + DropInsufficientQuota。
func TestQuotaFilter_PaidOnly_QuotaBelowReserve_Blocked(t *testing.T) {
	fx := newQuotaFixture("m", quotaPricedMC(), 7,
		&protocol.SyncedUser{ID: 7, Quota: 50}, serviceFeeSettings(100))
	in := []ScoredCandidate{adminPaid(), adminPaid()}

	got, code := quotaFilter{}.Apply(fx.fctx, in)

	if len(got) != 0 {
		t.Errorf("kept len = %d, want 0", len(got))
	}
	if code != DropInsufficientQuota {
		t.Errorf("code = %v, want DropInsufficientQuota", code)
	}
}

// 6. UserInfo nil 或 UserID==0 → 全留(系统 token 不限)。
func TestQuotaFilter_SystemToken_AllKept(t *testing.T) {
	t.Run("nil UserInfo", func(t *testing.T) {
		cache := &stubAgentCache{
			settings:     serviceFeeSettings(100),
			modelConfigs: map[string]*models.ModelConfig{"m": quotaPricedMC()},
		}
		rctx := withRequest(newTestRelayContext(cache, "m", nil, 0))
		fctx := &FilterContext{Rctx: rctx, RealModel: "m"}
		in := []ScoredCandidate{adminPaid(), adminPaid()}

		got, code := quotaFilter{}.Apply(fctx, in)
		if len(got) != 2 || code != DropNone {
			t.Fatalf("got len=%d code=%v, want len=2 DropNone", len(got), code)
		}
	})

	t.Run("UserID==0", func(t *testing.T) {
		fx := newQuotaFixture("m", quotaPricedMC(), 0, nil, serviceFeeSettings(100))
		// UserID==0 → newQuotaFixture 不建 UserInfo;但显式确保 UserInfo 存在且 ID=0 也放行。
		fx.fctx.Rctx.Input.UserInfo = &app.UserInfo{UserID: 0}
		in := []ScoredCandidate{adminPaid(), adminPaid()}

		got, code := quotaFilter{}.Apply(fx.fctx, in)
		if len(got) != 2 || code != DropNone {
			t.Fatalf("got len=%d code=%v, want len=2 DropNone", len(got), code)
		}
	})
}

// 7. GetUser 返回 nil → 全留(降级乐观放行)。
func TestQuotaFilter_GetUserNil_DegradeOpen(t *testing.T) {
	// user==nil → cache.GetUser 命中不到 → 返回 nil。
	fx := newQuotaFixture("m", quotaPricedMC(), 7, nil, serviceFeeSettings(100))
	in := []ScoredCandidate{adminPaid(), adminPaid()}

	got, code := quotaFilter{}.Apply(fx.fctx, in)

	if len(got) != 2 || code != DropNone {
		t.Fatalf("got len=%d code=%v, want len=2 DropNone (degrade-open)", len(got), code)
	}
	if fx.cache.getUserCalls != 1 {
		t.Errorf("GetUser called %d times, want 1 (priced+paid → must attempt read)", fx.cache.getUserCalls)
	}
}

func TestTargetSelectionCacheLoadHonorsRequestCancellation(t *testing.T) {
	fx := newQuotaFixture("m", quotaPricedMC(), 7, nil, serviceFeeSettings(100))
	ctx, cancel := context.WithCancel(context.Background())
	fx.fctx.Rctx.Request = fx.fctx.Rctx.Request.WithContext(ctx)
	entered := make(chan struct{})
	fx.cache.getUser = func(ctx context.Context, _ uint) *protocol.SyncedUser {
		close(entered)
		<-ctx.Done()
		return nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = quotaFilter{}.Apply(fx.fctx, []ScoredCandidate{adminPaid()})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("target selection did not enter cache load")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("target selection cache load ignored request cancellation")
	}
}

// 8. 边界:Quota==reserve → 拦(paid-only → DropInsufficientQuota);Quota==reserve+1 → 留。
func TestQuotaFilter_ReserveBoundary(t *testing.T) {
	t.Run("Quota==reserve blocked", func(t *testing.T) {
		fx := newQuotaFixture("m", quotaPricedMC(), 7,
			&protocol.SyncedUser{ID: 7, Quota: 100}, serviceFeeSettings(100))
		in := []ScoredCandidate{adminPaid()}

		got, code := quotaFilter{}.Apply(fx.fctx, in)
		if len(got) != 0 || code != DropInsufficientQuota {
			t.Fatalf("got len=%d code=%v, want len=0 DropInsufficientQuota (Quota==reserve 拦)",
				len(got), code)
		}
	})

	t.Run("Quota==reserve+1 kept", func(t *testing.T) {
		fx := newQuotaFixture("m", quotaPricedMC(), 7,
			&protocol.SyncedUser{ID: 7, Quota: 101}, serviceFeeSettings(100))
		in := []ScoredCandidate{adminPaid()}

		got, code := quotaFilter{}.Apply(fx.fctx, in)
		if len(got) != 1 || code != DropNone {
			t.Fatalf("got len=%d code=%v, want len=1 DropNone (Quota==reserve+1 放行)",
				len(got), code)
		}
	})
}
