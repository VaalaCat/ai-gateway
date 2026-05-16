package app

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

// TestAgentCacheImplementsInterface 编译期断言 *agentCache 满足 app.AgentCache。
func TestAgentCacheImplementsInterface(t *testing.T) {
	var _ appkg.AgentCache = (*agentCache)(nil)
}

// TestAgentCacheMatchRouteEmpty 边界：空 store / 空索引应返回 nil，不应 panic。
func TestAgentCacheMatchRouteEmpty(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	ac := newAgentCache(store)
	if got := ac.MatchRoute(0, "x", nil); got != nil {
		t.Errorf("empty index should return nil, got %v", got)
	}
}

// TestAgentCacheStoreMethodPassthrough happy path：
// 嵌入 *cache.Store 后 Store 接口方法（如 TokenCount）应能直接走通。
func TestAgentCacheStoreMethodPassthrough(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	ac := newAgentCache(store)
	if ac.TokenCount() != 0 {
		t.Errorf("empty store TokenCount should be 0, got %d", ac.TokenCount())
	}
}

// TestAgentCacheMatchRouteHit happy path：
// 写入一条 token-model 维度的 route，应能查到。
// 防止适配器写错委托对象（比如委托给一个新 RouteIndex 而不是 Store.RouteIndex）。
func TestAgentCacheMatchRouteHit(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	route := &models.AgentRoute{
		ID:         1,
		SourceType: "token",
		SourceID:   7,
		Model:      "gpt-4",
		AgentID:    "agent-A",
	}
	store.RouteIndex.Put(route)

	ac := newAgentCache(store)
	got := ac.MatchRoute(7, "gpt-4", nil)
	if got == nil {
		t.Fatal("expected MatchRoute to find the inserted route")
	}
	if got.ID != 1 {
		t.Errorf("expected route.ID=1, got %d", got.ID)
	}
}
