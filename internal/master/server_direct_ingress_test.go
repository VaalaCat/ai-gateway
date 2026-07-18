package master

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/rpc"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestSharedMasterListenerProvesEmbeddedDirectIngressIdentity(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded-agent.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	server, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	httpServer := httptest.NewServer(server.Router)
	t.Cleanup(httpServer.Close)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	})
	parsed, err := url.Parse(httpServer.URL)
	require.NoError(t, err)
	require.NoError(t, server.SetupEmbeddedAgentForTest(parsed.Host))

	pingResponse, err := http.Get(httpServer.URL + "/ping")
	require.NoError(t, err)
	defer pingResponse.Body.Close()
	require.Equal(t, http.StatusOK, pingResponse.StatusCode)
	var ping map[string]any
	require.NoError(t, json.NewDecoder(pingResponse.Body).Decode(&ping))
	require.Equal(t, "master", ping["role"])

	result := rpc.NewDirectProber(rpc.DirectProberOptions{}).Probe(t.Context(), protocol.DirectProbeTarget{
		TargetAgentID: "embedded", AddressFingerprint: "embedded-shared-listener",
		Addresses: []protocol.Address{{URL: httpServer.URL}},
	})
	require.Equal(t, "reachable", result.Network)
	require.Equal(t, "verified", result.Identity)
	require.True(t, result.Eligible)
	require.Empty(t, result.ReasonCode)
}
