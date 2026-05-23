package relay

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
)

// listModelsResponse mirrors the JSON shape returned by ListModels.
type listModelsResponse struct {
	Object string `json:"object"`
	Data   []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// runListModels executes ListModels against a populated store and returns the model IDs.
func runListModels(t *testing.T, store *cache.Store, ui *app.UserInfo) []string {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/models", func(c *gin.Context) {
		if ui != nil {
			c.Set(consts.CtxKeyUserInfo, ui)
		}
		ListModels(store)(c)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp listModelsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	ids := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		ids = append(ids, m.ID)
	}
	return ids
}

// listModelDetailed 用于断言 owned_by 字段（routing vs model 区分）。
type listModelDetailed struct {
	ID      string
	OwnedBy string
}

func runListModelsDetailed(t *testing.T, store *cache.Store, ui *app.UserInfo) []listModelDetailed {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/models", func(c *gin.Context) {
		if ui != nil {
			c.Set(consts.CtxKeyUserInfo, ui)
		}
		ListModels(store)(c)
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	out := make([]listModelDetailed, 0, len(resp.Data))
	for _, m := range resp.Data {
		out = append(out, listModelDetailed{ID: m.ID, OwnedBy: m.OwnedBy})
	}
	return out
}

// newStoreWithChannels returns a store seeded with the given channels (re-indexed).
func newStoreWithChannels(t *testing.T, channels ...models.Channel) *cache.Store {
	t.Helper()
	s := cache.NewStore(nil, config.AgentCacheConfig{})
	s.LoadChannels(channels)
	s.RebuildModelIndex()
	return s
}

func TestListModels_NoRestrictions(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,gpt-3.5-turbo"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "claude-3-opus"},
	)
	got := runListModels(t, store, &app.UserInfo{})
	want := map[string]bool{"gpt-4o": true, "gpt-3.5-turbo": true, "claude-3-opus": true}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got=%v)", len(got), len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected model %q", id)
		}
	}
}

func TestListModels_TokenModelsOnly(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,gpt-3.5-turbo,claude-3-opus"},
	)
	ui := &app.UserInfo{TokenModels: []string{"gpt-4o"}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o]", got)
	}
}

func TestListModels_ChannelWhitelistOnly(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "gpt-3.5-turbo"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}, Models: "claude-3-opus"},
	)
	ui := &app.UserInfo{AllowedChannelIDs: []uint{1}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o]", got)
	}
}

func TestListModels_OverlappingModelsDeduped(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "gpt-4o,claude-3-opus"},
	)
	ui := &app.UserInfo{AllowedChannelIDs: []uint{1}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o] (single dedup'd entry)", got)
	}
}

func TestListModels_DisabledChannelExcluded(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusDisabled}, Models: "claude-3-opus"},
	)
	ui := &app.UserInfo{AllowedChannelIDs: []uint{2}}
	got := runListModels(t, store, ui)
	if len(got) != 0 {
		t.Fatalf("got=%v, want empty list (channel 2 is disabled)", got)
	}
}

func TestListModels_GhostChannelIDIgnored(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "claude-3-opus"},
	)
	ui := &app.UserInfo{AllowedChannelIDs: []uint{1, 999}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o] (ghost id 999 must be ignored)", got)
	}
}

func TestListModels_BothFiltersIntersect(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,gpt-3.5-turbo"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "gpt-4o-mini,claude-3-opus"},
	)
	ui := &app.UserInfo{
		AllowedChannelIDs: []uint{1},
		TokenModels:       []string{"gpt-.*"},
	}
	got := runListModels(t, store, ui)
	want := map[string]bool{"gpt-4o": true, "gpt-3.5-turbo": true}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want=%d (got=%v)", len(got), len(want), got)
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected model %q", id)
		}
	}
}

// TestListModels_GroupChannelWhitelistOnly — GroupAllowedChannelIDs=[1], no token whitelist.
// Only models served by channel 1 should appear.
func TestListModels_GroupChannelWhitelistOnly(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "gpt-3.5-turbo"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}, Models: "claude-3-opus"},
	)
	ui := &app.UserInfo{GroupAllowedChannelIDs: []uint{1}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o]", got)
	}
}

// TestListModels_GroupAndTokenChannelIntersect — group:[1,2], token:[2,3] → only ch2's model.
func TestListModels_GroupAndTokenChannelIntersect(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 2, Status: consts.StatusEnabled}, Models: "gpt-3.5-turbo"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 3, Status: consts.StatusEnabled}, Models: "claude-3-opus"},
	)
	ui := &app.UserInfo{
		GroupAllowedChannelIDs: []uint{1, 2},
		AllowedChannelIDs:      []uint{2, 3},
	}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-3.5-turbo" {
		t.Fatalf("got=%v, want [gpt-3.5-turbo] (only channel 2 in both whitelists)", got)
	}
}

// TestListModels_GroupModelsPattern — GroupModels=["gpt-4.*"] filters by name pattern.
func TestListModels_GroupModelsPattern(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,gpt-3.5-turbo"},
	)
	ui := &app.UserInfo{GroupModels: []string{"gpt-4.*"}}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o]", got)
	}
}

// TestListModels_GroupAndTokenModelsIntersect — group:["gpt-.*"] AND token:["gpt-4o"] → only "gpt-4o".
func TestListModels_GroupAndTokenModelsIntersect(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,gpt-3.5-turbo,claude-3-opus"},
	)
	ui := &app.UserInfo{
		GroupModels: []string{"gpt-.*"},
		TokenModels: []string{"gpt-4o"},
	}
	got := runListModels(t, store, ui)
	if len(got) != 1 || got[0] != "gpt-4o" {
		t.Fatalf("got=%v, want [gpt-4o]", got)
	}
}

// TestListModels_GroupAndTokenModelsConflict — group:["claude-.*"] AND token:["gpt-4o"] → empty.
func TestListModels_GroupAndTokenModelsConflict(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,claude-3-opus"},
	)
	ui := &app.UserInfo{
		GroupModels: []string{"claude-.*"},
		TokenModels: []string{"gpt-4o"},
	}
	got := runListModels(t, store, ui)
	if len(got) != 0 {
		t.Fatalf("got=%v, want empty (conflicting group and token model filters)", got)
	}
}

// TestListModels_GlobalRoutingIncluded —— enabled global routing 应该作为 model 出现，owned_by 区分。
func TestListModels_GlobalRoutingIncluded(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.SetGlobalRouting("smart-router", &protocol.SyncedRouting{
		Name: "smart-router", Scope: "global", Enabled: true,
	})
	got := runListModelsDetailed(t, store, &app.UserInfo{})

	var foundRouting bool
	for _, m := range got {
		if m.ID == "smart-router" {
			if m.OwnedBy != "ai-gateway-routing" {
				t.Errorf("routing owned_by = %q, want ai-gateway-routing", m.OwnedBy)
			}
			foundRouting = true
		}
	}
	if !foundRouting {
		t.Errorf("smart-router not in response: %v", got)
	}
}

// TestListModels_DisabledGlobalRoutingExcluded —— disabled routing 不应该出现。
func TestListModels_DisabledGlobalRoutingExcluded(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.SetGlobalRouting("disabled-router", &protocol.SyncedRouting{
		Name: "disabled-router", Scope: "global", Enabled: false,
	})
	got := runListModelsDetailed(t, store, &app.UserInfo{})
	for _, m := range got {
		if m.ID == "disabled-router" {
			t.Errorf("disabled routing should not appear: %v", got)
		}
	}
}

// TestListModels_UserRoutingIncludedForOwner —— 当前 user 的 user routing 应该出现。
func TestListModels_UserRoutingIncludedForOwner(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"my-personal-router": {
			Name: "my-personal-router", Scope: "user", UserID: 42, Enabled: true,
		},
	})
	got := runListModelsDetailed(t, store, &app.UserInfo{UserID: 42})

	var found bool
	for _, m := range got {
		if m.ID == "my-personal-router" && m.OwnedBy == "ai-gateway-routing" {
			found = true
		}
	}
	if !found {
		t.Errorf("user routing not in response: %v", got)
	}
}

// TestListModels_UserRoutingNotIncludedForOtherUser —— user 42 的 routing 不应被 user 99 看到。
func TestListModels_UserRoutingNotIncludedForOtherUser(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"user42-only": {Name: "user42-only", Scope: "user", UserID: 42, Enabled: true},
	})
	got := runListModelsDetailed(t, store, &app.UserInfo{UserID: 99})
	for _, m := range got {
		if m.ID == "user42-only" {
			t.Errorf("user 42's routing should not appear for user 99: %v", got)
		}
	}
}

// TestListModels_TokenModelsDoesNotFilterRouting —— TokenModels=[gpt-4o] 时
// claude-router (routing) 仍应出现；claude-3-opus (model) 应被过滤。
func TestListModels_TokenModelsDoesNotFilterRouting(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o,claude-3-opus"},
	)
	store.SetGlobalRouting("claude-router", &protocol.SyncedRouting{
		Name: "claude-router", Scope: "global", Enabled: true,
	})
	ui := &app.UserInfo{TokenModels: []string{"gpt-4o"}}
	got := runListModelsDetailed(t, store, ui)

	var hasGPT, hasRouting bool
	for _, m := range got {
		if m.ID == "gpt-4o" && m.OwnedBy == "ai-gateway" {
			hasGPT = true
		}
		if m.ID == "claude-router" && m.OwnedBy == "ai-gateway-routing" {
			hasRouting = true
		}
		if m.ID == "claude-3-opus" {
			t.Errorf("claude-3-opus should be filtered by TokenModels, but appeared: %v", got)
		}
	}
	if !hasGPT {
		t.Errorf("gpt-4o missing: %v", got)
	}
	if !hasRouting {
		t.Errorf("claude-router missing despite TokenModels=[gpt-4o]: %v", got)
	}
}

// TestListModels_RoutingNameOverridesModel —— routing 跟 model 同名时，
// response 中只保留 routing 项，owned_by 反映 routing 优先。
func TestListModels_RoutingNameOverridesModel(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.SetGlobalRouting("gpt-4o", &protocol.SyncedRouting{
		Name: "gpt-4o", Scope: "global", Enabled: true,
	})
	got := runListModelsDetailed(t, store, &app.UserInfo{})

	var count int
	var ownedBy string
	for _, m := range got {
		if m.ID == "gpt-4o" {
			count++
			ownedBy = m.OwnedBy
		}
	}
	if count != 1 {
		t.Errorf("gpt-4o appears %d times, want 1: %v", count, got)
	}
	if ownedBy != "ai-gateway-routing" {
		t.Errorf("gpt-4o owned_by = %q, want ai-gateway-routing (routing precedence)", ownedBy)
	}
}

// TestListModels_BYOKModelAppears —— 用户配了 BYOK channel 包含 gpt-7，
// /v1/models 必须列出该 model，OwnedBy="ai-gateway-byok"。
func TestListModels_BYOKModelAppears(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	store.OverrideVisiblePrivateChannels(42, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 100, Status: consts.StatusEnabled}, OwnerID: 42, Models: []string{"gpt-7"}},
	})

	got := runListModelsDetailed(t, store, &app.UserInfo{UserID: 42})

	var count int
	for _, m := range got {
		if m.ID == "gpt-7" {
			count++
			if m.OwnedBy != "ai-gateway-byok" {
				t.Errorf("gpt-7 owned_by = %q, want ai-gateway-byok", m.OwnedBy)
			}
		}
	}
	if count != 1 {
		t.Errorf("gpt-7 appears %d times, want 1: %v", count, got)
	}
}

// TestListModels_BYOKOverridesAdminWhitelistBlock —— 用户 token 白名单不含 gpt-5，
// 但用户配了 BYOK gpt-5，必须能在 /v1/models 看到（修复"看不到但调得通"）。
func TestListModels_BYOKOverridesAdminWhitelistBlock(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
		models.Channel{ChannelCore: models.ChannelCore{ID: 99, Status: consts.StatusEnabled}, Models: "gpt-5"},
	)
	store.OverrideVisiblePrivateChannels(42, []protocol.SyncedPrivateChannel{
		{ChannelCore: models.ChannelCore{ID: 100, Status: consts.StatusEnabled}, OwnerID: 42, Models: []string{"gpt-5"}},
	})
	ui := &app.UserInfo{
		UserID:            42,
		AllowedChannelIDs: []uint{1}, // 排除 channel 99 → admin 段不应有 gpt-5
	}

	got := runListModelsDetailed(t, store, ui)

	var gpt5OwnedBy string
	var gpt5Count int
	for _, m := range got {
		if m.ID == "gpt-5" {
			gpt5OwnedBy = m.OwnedBy
			gpt5Count++
		}
	}
	if gpt5Count != 1 {
		t.Fatalf("gpt-5 appears %d times, want 1: %v", gpt5Count, got)
	}
	if gpt5OwnedBy != "ai-gateway-byok" {
		t.Errorf("gpt-5 owned_by = %q, want ai-gateway-byok", gpt5OwnedBy)
	}
}

// TestListModels_NoBYOKMeansNoBYOKOwner —— 用户没配 BYOK 时，
// 响应里不应出现任何 OwnedBy="ai-gateway-byok" 的项。
func TestListModels_NoBYOKMeansNoBYOKOwner(t *testing.T) {
	store := newStoreWithChannels(t,
		models.Channel{ChannelCore: models.ChannelCore{ID: 1, Status: consts.StatusEnabled}, Models: "gpt-4o"},
	)
	got := runListModelsDetailed(t, store, &app.UserInfo{UserID: 42})

	for _, m := range got {
		if m.OwnedBy == "ai-gateway-byok" {
			t.Errorf("unexpected byok-owned item: %v (full=%v)", m, got)
		}
	}
}
