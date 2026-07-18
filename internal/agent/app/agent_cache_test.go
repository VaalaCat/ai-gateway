package app

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	appkg "github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

// TestAgentCacheImplementsInterface 编译期断言 *agentCache 满足 app.AgentCache。
func TestAgentCacheImplementsInterface(t *testing.T) {
	var _ appkg.AgentCache = (*agentCache)(nil)
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

func TestAgentCacheRouteFindersDelegateToStoreRouteIndex(t *testing.T) {
	tests := []struct {
		name  string
		route *models.AgentRoute
		find  func(appkg.AgentCache, uint, string) *models.AgentRoute
	}{
		{
			name:  "token route",
			route: &models.AgentRoute{ID: 11, SourceType: "token", SourceID: 7, Model: "gpt-4o", AgentID: "token-agent"},
			find: func(cache appkg.AgentCache, sourceID uint, model string) *models.AgentRoute {
				return cache.FindTokenRoute(sourceID, model)
			},
		},
		{
			name:  "admin channel route",
			route: &models.AgentRoute{ID: 12, SourceType: "channel", SourceID: 7, Model: "gpt-4o", AgentID: "channel-agent"},
			find: func(cache appkg.AgentCache, sourceID uint, model string) *models.AgentRoute {
				return cache.FindAdminChannelRoute(sourceID, model)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := cache.NewStore(nil, config.AgentCacheConfig{})
			ac := newAgentCache(store)

			require.Nil(t, tt.find(ac, tt.route.SourceID, tt.route.Model))
			store.RouteIndex.Put(tt.route)
			require.Equal(t, tt.route, tt.find(ac, tt.route.SourceID, tt.route.Model))
			require.Nil(t, tt.find(ac, tt.route.SourceID, "claude-3-5"))
		})
	}
}
