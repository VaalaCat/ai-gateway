package master

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTunnelServerWiresRelayHubAsConnectivitySource(t *testing.T) {
	t.Parallel()
	cfg := &config.MasterRuntimeConfig{
		Master:  config.MasterConfig{Listen: ":0", DBPath: filepath.Join(t.TempDir(), "core.db"), JWTSecret: strings.Repeat("x", 32)},
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "agent.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	server, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	require.NotNil(t, server.RelayHub)
	require.NotNil(t, server.Connections)
	var source connectivity.RelaySource = server.RelayHub
	require.Same(t, server.RelayHub, source)
}

func TestTunnelEmbeddedAgentMountsDirectOwnersOnMasterRouter(t *testing.T) {
	masterConfig := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: filepath.Join(t.TempDir(), "core.db"), JWTSecret: strings.Repeat("x", 32),
		},
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "master-agent.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	server, err := New(masterConfig, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, server.Shutdown(context.Background())) })
	embedded, err := agent.NewEmbedded(
		&config.AgentRuntimeConfig{
			Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
			Runtime: config.RuntimeConfig{RelayTimeout: 30},
		},
		zap.NewNop(),
		&enrollment.Credentials{AgentID: "embedded-a", Secret: "secret"},
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, embedded.Shutdown(context.Background())) })
	require.NotNil(t, embedded.DirectSessionPool)
	require.Nil(t, embedded.DirectTunnelIngress)

	embedded.MountRoutes(server.Router)
	server.embeddedAgent = embedded
	require.NotNil(t, embedded.DirectTunnelIngress)
	directRouteMounted := false
	for _, route := range server.Router.Routes() {
		if route.Method == "GET" && route.Path == agenttunnel.DirectTunnelPath {
			directRouteMounted = true
			break
		}
	}
	require.True(t, directRouteMounted)

	require.NoError(t, server.Shutdown(t.Context()))
	require.Equal(t, app.ResourceCounts{}, server.ResourceCountsForTest())
}
