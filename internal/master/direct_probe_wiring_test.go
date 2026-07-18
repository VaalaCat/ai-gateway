package master

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

type probeSessionCallerStub struct {
	method      string
	sourceID    string
	generation  uint64
	direct      protocol.DirectProbeTarget
	relay       protocol.RelayProbeTarget
	invalidJSON bool
}

type probeTargetControlStub struct {
	addresses map[string][]agentproxy.Address
	facts     map[string]connectivity.ControlSessionFact
}

func (s probeTargetControlStub) GetAgentAddresses(agentID, dbHTTPAddrs string) []agentproxy.Address {
	if addresses, ok := s.addresses[agentID]; ok {
		return append([]agentproxy.Address(nil), addresses...)
	}
	return agentproxy.ParseAddresses(dbHTTPAddrs)
}

func (s probeTargetControlStub) GetControlSession(agentID string) (connectivity.ControlSessionFact, bool) {
	fact, ok := s.facts[agentID]
	return fact, ok
}

func (s *probeSessionCallerStub) CallSessionContext(ctx context.Context, sourceID string, generation uint64, method string, params any, _ time.Duration) (json.RawMessage, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	s.method, s.sourceID, s.generation = method, sourceID, generation
	if s.invalidJSON {
		return json.RawMessage(`{`), nil
	}
	switch target := params.(type) {
	case protocol.DirectProbeTarget:
		s.direct = target
		return json.Marshal(protocol.DirectProbeResult{
			TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
			Network: "reachable", Identity: "verified", Eligible: true,
		})
	case protocol.RelayProbeTarget:
		s.relay = target
		return json.Marshal(protocol.RelayProbeResult{
			TargetAgentID: target.TargetAgentID, State: protocol.RelayProbeReachable,
		})
	default:
		return nil, errors.New("unexpected probe params")
	}
}

func TestMasterProbeCallerUsesGenerationBoundTypedRPCAndContext(t *testing.T) {
	control := &probeSessionCallerStub{}
	caller := masterProbeCaller{control: control}
	target := protocol.DirectProbeTarget{
		TargetAgentID: "target", Addresses: []protocol.Address{{URL: "https://target"}},
		AddressFingerprint: "fp", TargetGeneration: 12,
	}
	result, err := caller.CallDirectProbe(t.Context(), "source", 9, target)
	require.NoError(t, err)
	require.True(t, result.Eligible)
	require.Equal(t, consts.RPCAgentDirectProbe, control.method)
	require.Equal(t, "source", control.sourceID)
	require.Equal(t, uint64(9), control.generation)
	require.Equal(t, target, control.direct)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = caller.CallDirectProbe(cancelled, "source", 9, target)
	require.ErrorIs(t, err, context.Canceled)
}

func TestMasterProbeCallerUsesGenerationBoundRelayRPC(t *testing.T) {
	control := &probeSessionCallerStub{}
	caller := masterProbeCaller{control: control}
	target := protocol.RelayProbeTarget{
		TargetAgentID: "target", SourceRelayGeneration: 19, TargetRelayGeneration: 23,
	}
	result, err := caller.CallRelayProbe(t.Context(), "source", 9, target)
	require.NoError(t, err)
	require.Equal(t, protocol.RelayProbeReachable, result.State)
	require.Equal(t, consts.RPCAgentRelayProbe, control.method)
	require.Equal(t, "source", control.sourceID)
	require.Equal(t, uint64(9), control.generation)
	require.Equal(t, target, control.relay)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = caller.CallRelayProbe(cancelled, "source", 9, target)
	require.ErrorIs(t, err, context.Canceled)

	_, err = (masterProbeCaller{}).CallRelayProbe(t.Context(), "source", 9, target)
	require.ErrorContains(t, err, "control hub is required")
	_, err = (masterProbeCaller{control: &probeSessionCallerStub{invalidJSON: true}}).
		CallRelayProbe(t.Context(), "source", 9, target)
	require.ErrorContains(t, err, "relay probe response")
}

func TestMasterServerOwnsProbeScheduler(t *testing.T) {
	server := newLifecycleMasterServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	})
	require.NotNil(t, server.ProbeScheduler)
	require.IsType(t, (*connectivity.Scheduler)(nil), server.ProbeScheduler)
}

func TestMasterProbeTargetFinderFiltersDisabledAgentsForIDQuery(t *testing.T) {
	server := newLifecycleMasterServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	})
	require.NoError(t, server.DB.Create(&models.Agent{
		AgentID: "probe-enabled", Name: "enabled", Status: consts.StatusEnabled,
		HTTPAddresses: `[{"url":"http://enabled"}]`,
	}).Error)
	require.NoError(t, server.DB.Create(&models.Agent{
		AgentID: "probe-disabled", Name: "disabled", Status: consts.StatusEnabled,
		HTTPAddresses: `[{"url":"http://disabled"}]`,
	}).Error)
	require.NoError(t, server.DB.Model(&models.Agent{}).
		Where("agent_id = ?", "probe-disabled").
		Update("status", consts.StatusDisabled).Error)
	finder := masterProbeTargetFinder{application: server.App}

	targets, err := finder.FindEnabledProbeTargets(t.Context(), []string{"probe-disabled", "probe-enabled"})
	require.NoError(t, err)
	require.Len(t, targets, 1)
	require.Equal(t, "probe-enabled", targets[0].AgentID)
}

func TestMasterProbeTargetFinderUsesMergedAddresses(t *testing.T) {
	server := newLifecycleMasterServer(t)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		require.NoError(t, server.Shutdown(ctx))
	})
	require.NoError(t, server.DB.Create(&models.Agent{
		AgentID: "probe-merged", Name: "merged", Status: consts.StatusEnabled,
		HTTPAddresses: `[{"url":"http://configured","tag":"configured"}]`,
	}).Error)

	tests := []struct {
		name       string
		control    probeTargetControl
		want       []agentproxy.Address
		generation uint64
	}{
		{name: "configured addresses without control", want: []agentproxy.Address{{URL: "http://configured", Tag: "configured"}}},
		{name: "control merged addresses override configured", control: probeTargetControlStub{
			addresses: map[string][]agentproxy.Address{"probe-merged": {{URL: "http://10.0.0.9:8088", Tag: "auto-detected"}}},
			facts:     map[string]connectivity.ControlSessionFact{"probe-merged": {Generation: 9}},
		}, want: []agentproxy.Address{{URL: "http://10.0.0.9:8088", Tag: "auto-detected"}}, generation: 9},
		{name: "control may project an empty merged set", control: probeTargetControlStub{
			addresses: map[string][]agentproxy.Address{"probe-merged": {}},
		}, want: nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			finder := masterProbeTargetFinder{application: server.App, control: test.control}
			targets, err := finder.FindEnabledProbeTargets(t.Context(), []string{"probe-merged"})
			require.NoError(t, err)
			require.Len(t, targets, 1)
			require.Equal(t, test.want, targets[0].Addresses)
			require.Equal(t, test.generation, targets[0].ControlGeneration)
		})
	}
}
