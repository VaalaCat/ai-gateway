package agent

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/stretchr/testify/require"
)

func TestServerBuildsOneGenericAPITargetForSourceAndBothTunnelIngresses(t *testing.T) {
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	t.Cleanup(store.Close)
	server := &Server{
		Cfg:   &config.AgentRuntimeConfig{Agent: config.AgentConfig{}},
		Creds: &enrollment.Credentials{AgentID: "source-a"}, Store: store,
	}

	runtime := server.genericAPIRuntime()
	require.NotNil(t, runtime.local)
	require.NotNil(t, runtime.target)
	require.NotNil(t, runtime.localWebSocket)
	require.NotNil(t, runtime.webSocketTarget)
	require.Same(t, runtime, server.genericAPIRuntime())
	require.NotNil(t, server.newGenericAPIProtocolHandler())
	require.NotNil(t, server.newGenericAPIWebSocketProtocolHandler())
}
