package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestAgentDirectListenPortRequiresOwnedHTTPListener(t *testing.T) {
	tests := []struct {
		name             string
		ownsHTTPListener bool
		listen           string
		want             int
	}{
		{name: "standalone listener", ownsHTTPListener: true, listen: ":8140", want: 8140},
		{name: "embedded shared listener", ownsHTTPListener: false, listen: ":8140", want: 0},
		{name: "standalone invalid listener", ownsHTTPListener: true, listen: "unix:/tmp/agent.sock", want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := &Server{
				Cfg:              &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: test.listen}},
				ownsHTTPListener: test.ownsHTTPListener,
			}
			require.Equal(t, test.want, server.directListenPort())
		})
	}
}

func TestAgentDirectAddressHandlerIsBoundToCurrentAuthSession(t *testing.T) {
	firstClient := newOrderedAgentControlClient()
	firstClient.fullSyncAgentItems = json.RawMessage(`[{"agent_id":"target"}]`)
	store := agentcache.NewStore(nil, config.AgentCacheConfig{})
	store.SetAgent(&models.Agent{AgentID: "target"})
	server := &Server{
		Cfg:    &config.AgentRuntimeConfig{Agent: config.AgentConfig{Listen: ":0"}},
		Logger: zap.NewNop(),
		Creds:  &enrollment.Credentials{AgentID: "agent-a", Secret: "secret-a"},
		Store:  store,
		Syncer: agentcache.NewSyncer(store, firstClient, nil, zap.NewNop(), time.Hour),
	}

	first, err := server.startAgentAuthSession(context.Background(), firstClient)
	require.NoError(t, err)
	firstHandler := firstClient.inlineHandler(consts.RPCSyncAutoAddrUpdate)
	require.NotNil(t, firstHandler)
	invokeDirectAddressesUpdate(t, firstHandler, protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 9, Sequence: 1,
		HTTPAddresses: []protocol.Address{{URL: "http://first:8140", Tag: "auto-detected"}},
	})
	require.Contains(t, store.GetAgent("target").HTTPAddresses, "http://first:8140")

	secondClient := newOrderedAgentControlClient()
	secondClient.fullSyncAgentItems = json.RawMessage(`[{"agent_id":"target"}]`)
	server.Syncer.SetClient(secondClient)
	second, err := server.startAgentAuthSession(context.Background(), secondClient)
	if err != nil {
		server.stopAgentAuthSession(first)
		t.Fatal(err)
	}
	t.Cleanup(func() { server.stopAgentAuthSession(second) })
	require.Empty(t, store.GetAgent("target").HTTPAddresses)

	invokeDirectAddressesUpdate(t, firstHandler, protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 9, Sequence: 99,
		HTTPAddresses: []protocol.Address{{URL: "http://stale:8140", Tag: "auto-detected"}},
	})
	require.Empty(t, store.GetAgent("target").HTTPAddresses)

	currentHandler := secondClient.inlineHandler(consts.RPCSyncAutoAddrUpdate)
	require.NotNil(t, currentHandler)
	invokeDirectAddressesUpdate(t, currentHandler, protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-b", AgentID: "target", SessionGeneration: 10, Sequence: 100,
		HTTPAddresses: []protocol.Address{{URL: "http://wrong-master:8140", Tag: "auto-detected"}},
	})
	require.Empty(t, store.GetAgent("target").HTTPAddresses)
	invokeDirectAddressesUpdate(t, currentHandler, protocol.AgentDirectAddressesUpdate{
		MasterInstanceID: "master-a", AgentID: "target", SessionGeneration: 10, Sequence: 2,
		HTTPAddresses: []protocol.Address{{URL: "http://current:8140", Tag: "auto-detected"}},
	})
	require.Contains(t, store.GetAgent("target").HTTPAddresses, "http://current:8140")
}

func invokeDirectAddressesUpdate(
	t *testing.T,
	handler func(context.Context, json.RawMessage) (any, error),
	update protocol.AgentDirectAddressesUpdate,
) {
	t.Helper()
	raw, err := json.Marshal(update)
	require.NoError(t, err)
	_, err = handler(context.Background(), raw)
	require.NoError(t, err)
}
