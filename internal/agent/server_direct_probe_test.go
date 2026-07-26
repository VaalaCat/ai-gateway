package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentServerRegistersDirectProbeWithSessionPool(t *testing.T) {
	cfg := directProbeAgentConfig()
	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"})
	require.NoError(t, err)
	require.NotNil(t, server.DirectSessionPool)
	require.NotNil(t, server.directGate)
	require.NotNil(t, server.directProber)
	t.Cleanup(func() { shutdownDirectProbeServer(t, server) })

	client := newOrderedAgentControlClient()
	server.registerControlHandlers(client)
	handler := client.handlers[consts.RPCAgentDirectProbe]
	require.NotNil(t, handler)
	raw, err := json.Marshal(protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b",
		Addresses: []protocol.Address{{URL: "https://agent-b.example"}},
		Policy:    protocol.ProbeRespectBusinessPolicy,
	})
	require.NoError(t, err)
	value, err := handler(t.Context(), raw)
	require.NoError(t, err)
	result, ok := value.(protocol.DirectProbeResult)
	require.True(t, ok)
	require.Equal(t, "credentials", result.Stage)
	require.Equal(t, consts.RouteErrorDirectAuthUnavailable, result.ReasonCode)
}

func TestAgentServerBuildsRelayProberAfterTunnelManagerAndRegistersControlHandler(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T) *Server
	}{
		{
			name: "standalone",
			build: func(t *testing.T) *Server {
				cfg := directProbeAgentConfig()
				cfg.Agent.CredentialsFile = filepath.Join(t.TempDir(), "credentials.json")
				require.NoError(t, os.WriteFile(
					cfg.Agent.CredentialsFile,
					[]byte(`{"agent_id":"agent-a","secret":"secret"}`),
					0o600,
				))
				server, err := New(cfg, zap.NewNop())
				require.NoError(t, err)
				return server
			},
		},
		{
			name: "embedded",
			build: func(t *testing.T) *Server {
				server, err := NewEmbedded(
					directProbeAgentConfig(), zap.NewNop(),
					&enrollment.Credentials{AgentID: "agent-a", Secret: "secret"},
				)
				require.NoError(t, err)
				require.Nil(t, server.relayProber)
				server.MountRoutes(gin.New())
				return server
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := test.build(t)
			t.Cleanup(func() { shutdownDirectProbeServer(t, server) })
			require.NotNil(t, server.TunnelManager)
			require.NotNil(t, server.relayProber)

			client := newOrderedAgentControlClient()
			server.registerControlHandlers(client)
			require.NotNil(t, client.handlers[consts.RPCAgentRelayProbe])
		})
	}
}

func TestEmbeddedAgentDoesNotReplaceMasterPing(t *testing.T) {
	cfg := directProbeAgentConfig()
	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "embedded-a", Secret: "secret"})
	require.NoError(t, err)
	t.Cleanup(func() { shutdownDirectProbeServer(t, server) })
	require.Nil(t, server.Router)
}

func TestStandaloneAndEmbeddedAgentsCreateDirectSessionPoolAndTunnelIngress(t *testing.T) {
	tests := []struct {
		name      string
		newRouter func(*testing.T) (*Server, http.Handler)
	}{
		{
			name: "standalone",
			newRouter: func(t *testing.T) (*Server, http.Handler) {
				cfg := directProbeAgentConfig()
				cfg.Agent.CredentialsFile = filepath.Join(t.TempDir(), "credentials.json")
				require.NoError(t, os.WriteFile(
					cfg.Agent.CredentialsFile,
					[]byte(`{"agent_id":"agent-a","secret":"secret"}`),
					0o600,
				))
				server, err := New(cfg, zap.NewNop())
				require.NoError(t, err)
				return server, server.Router
			},
		},
		{
			name: "embedded",
			newRouter: func(t *testing.T) (*Server, http.Handler) {
				server, err := NewEmbedded(
					directProbeAgentConfig(), zap.NewNop(),
					&enrollment.Credentials{AgentID: "agent-a", Secret: "secret"},
				)
				require.NoError(t, err)
				engine := gin.New()
				server.MountRoutes(engine)
				return server, engine
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, router := test.newRouter(t)
			t.Cleanup(func() { shutdownDirectProbeServer(t, server) })
			require.NotNil(t, server.DirectSessionPool)
			require.NotNil(t, server.directProber)
			require.NotNil(t, server.DirectTunnelIngress)

			for _, tunnelTest := range []struct {
				target string
				status int
			}{
				{target: "agent-a", status: http.StatusUnauthorized},
				{target: "wrong-agent", status: http.StatusForbidden},
			} {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(
					http.MethodGet,
					agenttunnel.DirectTunnelPath+"?target_agent_id="+tunnelTest.target,
					nil,
				))
				require.Equal(t, tunnelTest.status, response.Code, tunnelTest.target)
			}
		})
	}
}

func TestDirectIngressShutdownClosesSharedServerIngress(t *testing.T) {
	server, err := NewEmbedded(
		directProbeAgentConfig(), zap.NewNop(),
		&enrollment.Credentials{AgentID: "embedded-a", Secret: "secret"},
	)
	require.NoError(t, err)
	server.MountRoutes(gin.New())
	ingress := server.DirectTunnelIngress
	require.NotNil(t, ingress)

	shutdownDirectProbeServer(t, server)
	select {
	case <-ingress.Done():
	case <-time.After(time.Second):
		t.Fatal("direct tunnel ingress did not finish with server shutdown")
	}
}

func shutdownDirectProbeServer(t *testing.T, server *Server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, server.Shutdown(ctx))
}

func directProbeAgentConfig() *config.AgentRuntimeConfig {
	return &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{Listen: ":0"},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 300, ReportBufferSize: 10,
			ReportFlushInterval: 5, HeartbeatInterval: 30,
		},
	}
}
