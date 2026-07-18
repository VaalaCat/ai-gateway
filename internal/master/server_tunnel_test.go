package master

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTunnelServerWiresRelayHubAsConnectivitySource(t *testing.T) {
	t.Parallel()
	cfg := &config.MasterRuntimeConfig{
		Master:  config.MasterConfig{Listen: ":0", DBPath: filepath.Join(t.TempDir(), "master.db"), JWTSecret: strings.Repeat("x", 32)},
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
