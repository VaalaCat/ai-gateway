package cache

import (
	"context"
	"encoding/json"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func TestHandleSyncEvent_ModelRoutingGlobalCreate(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	r := models.ModelRouting{
		ID: 1, Name: "smart", Scope: models.RoutingScopeGlobal,
		Members: `[{"ref":"gpt-4o","priority":0,"weight":1}]`,
		Enabled: true,
	}
	data, _ := json.Marshal(r)
	s.HandleSyncEvent(events.EntityModelRouting, events.ActionCreate, data)

	cached := s.GetGlobalRouting(context.Background(), "smart")
	if cached == nil {
		t.Fatal("global routing should be in cache after create event")
	}
	if cached.Name != "smart" || len(cached.Members) != 1 || cached.Members[0].Ref != "gpt-4o" {
		t.Errorf("cache mismatch: %+v", cached)
	}
}

func TestHandleSyncEvent_ModelRoutingGlobalUpdate(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{
		ID: 1, Name: "smart", Scope: "global", Enabled: true,
		Members: []protocol.RoutingMember{{Ref: "old", Priority: 0, Weight: 1}},
	})
	r := models.ModelRouting{
		ID: 1, Name: "smart", Scope: models.RoutingScopeGlobal,
		Members: `[{"ref":"new","priority":0,"weight":1}]`,
		Enabled: true,
	}
	data, _ := json.Marshal(r)
	s.HandleSyncEvent(events.EntityModelRouting, events.ActionUpdate, data)

	cached := s.GetGlobalRouting(context.Background(), "smart")
	if cached == nil || cached.Members[0].Ref != "new" {
		t.Errorf("cache should reflect updated members: %+v", cached)
	}
}

func TestHandleSyncEvent_ModelRoutingGlobalDelete(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetGlobalRouting("smart", &protocol.SyncedRouting{Name: "smart", Scope: "global", Enabled: true})

	r := models.ModelRouting{ID: 1, Name: "smart", Scope: models.RoutingScopeGlobal}
	data, _ := json.Marshal(r)
	s.HandleSyncEvent(events.EntityModelRouting, events.ActionDelete, data)

	if s.GetGlobalRouting(context.Background(), "smart") != nil {
		t.Error("routing should be deleted")
	}
}

func TestHandleSyncEvent_ModelRoutingUserInvalidates(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	// 预写 user 42 的 cache
	s.SetUserRoutings(42, map[string]*protocol.SyncedRouting{
		"my": {Name: "my", Scope: "user", UserID: 42, Enabled: true},
	})

	r := models.ModelRouting{
		ID: 1, Name: "my", Scope: models.RoutingScopeUser, UserID: 42,
		Members: `[{"ref":"x","priority":0,"weight":1}]`, Enabled: true,
	}
	data, _ := json.Marshal(r)
	s.HandleSyncEvent(events.EntityModelRouting, events.ActionUpdate, data)

	// user 42 的 cache 应被 invalidate（再次 ResolveRouting 会 miss 走 Loader）
	if r2 := s.ResolveRouting(context.Background(), "my", protocol.RoutingOwner{UserID: 42}); r2 != nil {
		// nil Loader 配置下 miss 应返回 nil
		// 如果还能查到，说明 invalidate 没生效
		t.Errorf("user routing cache should be invalidated, got %+v", r2)
	}
}

func TestHandleSyncEvent_ModelRoutingTokenInvalidatesOnlyItsBlock(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetTokenRoutings(7, map[string]*protocol.SyncedRouting{
		"mine": {ID: 1, Name: "mine", Scope: models.RoutingScopeToken, TokenID: 7, Enabled: true},
	})
	s.SetTokenRoutings(8, map[string]*protocol.SyncedRouting{
		"kept": {ID: 2, Name: "kept", Scope: models.RoutingScopeToken, TokenID: 8, Enabled: true},
	})
	payload, _ := json.Marshal(models.ModelRouting{ID: 1, Name: "mine", Scope: models.RoutingScopeToken, TokenID: 7})
	s.HandleSyncEvent(events.EntityModelRouting, events.ActionUpdate, payload)

	if got := s.ResolveRouting(context.Background(), "mine", protocol.RoutingOwner{TokenID: 7}); got != nil {
		t.Fatalf("token 7 block was not invalidated: %+v", got)
	}
	if got := s.ResolveRouting(context.Background(), "kept", protocol.RoutingOwner{TokenID: 8}); got == nil {
		t.Fatal("token 8 block must remain cached")
	}
}

func TestApplyModelRoutingEvent_LogsApply(t *testing.T) {
	core, recorded := observer.New(zap.InfoLevel)
	logger := zap.New(core)

	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetLogger(logger)

	r := &models.ModelRouting{
		ID: 1, Name: "auto", Scope: "global", UserID: 0,
		Members: `[{"ref":"m1","priority":1,"weight":1},{"ref":"m2","priority":1,"weight":2}]`,
		Enabled: true,
	}
	s.applyModelRoutingEvent("create", r)

	entries := recorded.FilterMessage("routing apply").All()
	if len(entries) != 1 {
		t.Fatalf("want 1 'routing apply' log entry, got %d", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["name"] != "auto" {
		t.Fatalf("name field wrong: %v", fields["name"])
	}
	if fields["action"] != "create" {
		t.Fatalf("action field wrong: %v", fields["action"])
	}
	if fields["member_count"] != int64(2) {
		t.Fatalf("member_count want 2, got %v", fields["member_count"])
	}
}
