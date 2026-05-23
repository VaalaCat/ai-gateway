package cache

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

// routingStubWSClient 是用于路由测试的最小 WSClient stub。
type routingStubWSClient struct {
	respond func(method string, params any) (json.RawMessage, error)
}

func (c *routingStubWSClient) OnNotification(_ string, _ app.NotificationHandler) {}
func (c *routingStubWSClient) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	return c.respond(method, params)
}
func (c *routingStubWSClient) Notify(_ string, _ any) error { return nil }
func (c *routingStubWSClient) Close() error                 { return nil }
func (c *routingStubWSClient) ReadLoop()                    {}

func TestResolveRouting_UserOverridesGlobal(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: true})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"smart": {Name: "smart", Scope: "user", UserID: 42, Enabled: true},
	})
	r := s.ResolveRouting("smart", 42)
	if r == nil || r.Scope != "user" {
		t.Errorf("user routing should win, got %+v", r)
	}
	r2 := s.ResolveRouting("smart", 99)
	if r2 == nil || r2.Scope != "global" {
		t.Errorf("user 99 has no override, should fall to global, got %+v", r2)
	}
}

func TestResolveRouting_Disabled(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: false})
	if s.ResolveRouting("smart", 0) != nil {
		t.Errorf("disabled global routing should return nil")
	}
}

func TestResolveRouting_UserDisabledFallsToGlobal(t *testing.T) {
	// 用户级 disabled 时穿透到 global
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: true})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"smart": {Name: "smart", Scope: "user", UserID: 42, Enabled: false},
	})
	r := s.ResolveRouting("smart", 42)
	if r == nil || r.Scope != "global" {
		t.Errorf("user disabled should fall to global, got %+v", r)
	}
}

func TestResolveRouting_NoRouting(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if s.ResolveRouting("nonexistent", 42) != nil {
		t.Errorf("expected nil for nonexistent routing")
	}
}

// TestUserRoutings_LoaderMiss 验证 Loader found=false 时 ResolveRouting 返回 nil。
func TestUserRoutings_LoaderMiss(t *testing.T) {
	cli := &routingStubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: false}
		b, _ := json.Marshal(resp)
		return b, nil
	}}
	s := NewStore(cli, config.AgentCacheConfig{})
	// LRU miss → Loader called → Found=false → ErrNotFound → ResolveRouting returns nil
	if got := s.ResolveRouting("fast", 7); got != nil {
		t.Errorf("loader miss: expected nil, got %+v", got)
	}
}

// TestUserRoutings_LoaderFound 验证 Loader 返回数据时 ResolveRouting 能拿到正确 routing。
func TestUserRoutings_LoaderFound(t *testing.T) {
	routings := []*protocol.SyncedRouting{
		{ID: 1, Name: "fast", Scope: "user", UserID: 7, Enabled: true},
	}
	payload, _ := json.Marshal(map[string]any{"routings": routings})

	cli := &routingStubWSClient{respond: func(_ string, _ any) (json.RawMessage, error) {
		resp := protocol.FetchEntityResponse{Found: true, Data: payload}
		b, _ := json.Marshal(resp)
		return b, nil
	}}
	s := NewStore(cli, config.AgentCacheConfig{})
	got := s.ResolveRouting("fast", 7)
	if got == nil {
		t.Fatal("loader found: expected routing, got nil")
	}
	if got.Name != "fast" || got.Scope != "user" {
		t.Errorf("unexpected routing: %+v", got)
	}
}

func TestLoadGlobalRoutings_ReplacesAndFilters(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	// 预先有 stale routing
	s.SetGlobalRouting("stale", &protocol.SyncedRouting{Name: "stale", Scope: "global", Enabled: true})

	items := []models.ModelRouting{
		{ID: 1, Name: "smart", Scope: "global", Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`, Enabled: true},
		{ID: 2, Name: "off", Scope: "global", Members: `[{"ref":"x"}]`, Enabled: false}, // 应被过滤
	}
	s.LoadGlobalRoutings(items)

	if s.GetGlobalRouting("stale") != nil {
		t.Error("stale routing should be replaced (cleared)")
	}
	if s.GetGlobalRouting("smart") == nil {
		t.Error("smart routing should be loaded")
	}
	if s.GetGlobalRouting("off") != nil {
		t.Error("disabled routing should not be in cache")
	}
}

func TestGetAllModelNames_IncludesRoutings(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	// 模拟一个 channel 提供 model "gpt-4o"
	s.LoadChannels([]models.Channel{
		{ChannelCore: models.ChannelCore{ID: 1, Name: "ch", Type: 1, Status: 1}, Models: "gpt-4o"},
	})
	s.RebuildModelIndex()
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: true})
	s.SetGlobalRouting("off", &protocol.SyncedRouting{Name: "off", Scope: "global", Enabled: false})

	names := s.GetAllModelNames()
	has := func(n string) bool {
		for _, x := range names {
			if x == n {
				return true
			}
		}
		return false
	}
	if !has("gpt-4o") {
		t.Errorf("expected gpt-4o, got %v", names)
	}
	if !has("smart") {
		t.Errorf("expected smart, got %v", names)
	}
	if has("off") {
		t.Errorf("disabled routing 'off' should not appear, got %v", names)
	}
}

func TestCacheSnapshot_IncludesRoutingEntities(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})

	items := []models.ModelRouting{
		{ID: 1, Name: "auto", Scope: "global", Enabled: true, Members: `[{"ref":"m1","priority":1,"weight":1}]`},
	}
	s.LoadGlobalRoutings(items)
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"u-route": {ID: 2, Name: "u-route", Scope: "user", UserID: 42, Enabled: true},
	})

	snap := s.CacheSnapshot()

	mr, ok := snap["model_routing"]
	if !ok {
		t.Fatalf("snapshot missing 'model_routing' key")
	}
	if mr.Size != 1 {
		t.Fatalf("model_routing size want 1, got %d", mr.Size)
	}

	ur, ok := snap["user_routings"]
	if !ok {
		t.Fatalf("snapshot missing 'user_routings' key")
	}
	if ur.Size != 1 {
		t.Fatalf("user_routings size want 1, got %d", ur.Size)
	}
}

func TestListGlobalRoutingNames_OnlyEnabledSorted(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: true})
	s.SetGlobalRouting("disabled-one", &protocol.SyncedRouting{Name: "disabled-one", Scope: "global", Enabled: false})
	s.SetGlobalRouting("alpha", &protocol.SyncedRouting{Name: "alpha", Scope: "global", Enabled: true})

	got := s.ListGlobalRoutingNames()
	want := []string{"alpha", "smart"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want=%d (got=%v)", len(got), len(want), got)
	}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("[%d] got=%q want=%q", i, n, want[i])
		}
	}
}

func TestListGlobalRoutingNames_Empty(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if got := s.ListGlobalRoutingNames(); len(got) != 0 {
		t.Errorf("empty store should return empty slice, got %v", got)
	}
}

func TestListUserRoutingNames_OnlyEnabledSorted(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"alpha":    {Name: "alpha", Scope: "user", UserID: 42, Enabled: true},
		"disabled": {Name: "disabled", Scope: "user", UserID: 42, Enabled: false},
		"smart":    {Name: "smart", Scope: "user", UserID: 42, Enabled: true},
	})

	got := s.ListUserRoutingNames(42)
	want := []string{"alpha", "smart"}
	if len(got) != len(want) {
		t.Fatalf("len=%d, want=%d (got=%v)", len(got), len(want), got)
	}
	for i, n := range got {
		if n != want[i] {
			t.Errorf("[%d] got=%q want=%q", i, n, want[i])
		}
	}
}

func TestListUserRoutingNames_ZeroUserIDReturnsNil(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	if got := s.ListUserRoutingNames(0); got != nil {
		t.Errorf("userID=0 should return nil, got %v", got)
	}
}

func TestListUserRoutingNames_OtherUserNotIncluded(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"only-42": {Name: "only-42", Scope: "user", UserID: 42, Enabled: true},
	})
	if got := s.ListUserRoutingNames(99); len(got) != 0 {
		t.Errorf("user 99 should not see user 42's routings, got %v", got)
	}
}
