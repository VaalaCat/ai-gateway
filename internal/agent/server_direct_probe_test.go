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
	"github.com/VaalaCat/ai-gateway/internal/agent/route"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentServerRegistersDirectProbeAndProjectsResultIntoGate(t *testing.T) {
	cfg := directProbeAgentConfig()
	server, err := NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "agent-a", Secret: "secret"})
	require.NoError(t, err)
	require.NotNil(t, server.directGate)
	require.NotNil(t, server.directProber)
	t.Cleanup(func() { shutdownDirectProbeServer(t, server) })

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.DirectIngressIdentity{
			Contract: protocol.DirectIngressContractV1, Role: "agent", AgentID: "wrong-agent",
		})
	}))
	t.Cleanup(target.Close)
	client := newOrderedAgentControlClient()
	server.registerControlHandlers(client)
	handler := client.handlers[consts.RPCAgentDirectProbe]
	require.NotNil(t, handler)
	raw, err := json.Marshal(protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", AddressFingerprint: "fp-b",
		Addresses: []protocol.Address{{URL: target.URL}},
	})
	require.NoError(t, err)
	value, err := handler(t.Context(), raw)
	require.NoError(t, err)
	result, ok := value.(protocol.DirectProbeResult)
	require.True(t, ok)
	require.Equal(t, "mismatch", result.Identity)
	require.Equal(t, route.DirectGateIdentity, server.directGate.Decision("agent-b", "fp-b"))
	routeFingerprint := agentproxy.CanonicalAddressFingerprint([]agentproxy.Address{{URL: target.URL}})
	require.Equal(t, route.DirectGateIdentity, server.directGate.Decision("agent-b", routeFingerprint))
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

func TestStandaloneAndEmbeddedAgentsExposeTargetedDirectIngressIdentity(t *testing.T) {
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

			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(
				http.MethodGet,
				protocol.DirectIngressIdentityPath+"?target_agent_id=agent-a",
				nil,
			))
			require.Equal(t, http.StatusOK, response.Code)
			var identity protocol.DirectIngressIdentity
			require.NoError(t, json.Unmarshal(response.Body.Bytes(), &identity))
			require.Equal(t, protocol.DirectIngressIdentity{
				Contract: protocol.DirectIngressContractV1, Role: "agent", AgentID: "agent-a",
			}, identity)

			for _, target := range []string{"", "wrong-agent"} {
				response := httptest.NewRecorder()
				router.ServeHTTP(response, httptest.NewRequest(
					http.MethodGet,
					protocol.DirectIngressIdentityPath+"?target_agent_id="+target,
					nil,
				))
				require.Equal(t, http.StatusNotFound, response.Code, target)
			}
		})
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
