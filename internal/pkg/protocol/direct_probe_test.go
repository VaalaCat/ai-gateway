package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestDirectProbeProtocolJSONExact(t *testing.T) {
	require.Equal(t, "agent.directProbe", consts.RPCAgentDirectProbe)
	require.Equal(t, "agent_direct_ingress_v1", protocol.AgentCapabilityDirectIngressV1)
	require.Equal(t, "agent.relayProbe", consts.RPCAgentRelayProbe)
	require.Equal(t, "agent_relay_http_ping_v1", protocol.AgentCapabilityRelayHTTPPingV1)
	require.Equal(t, "direct_ingress_v1", protocol.DirectIngressContractV1)
	require.Equal(t, "/v1/direct-ingress/identity", protocol.DirectIngressIdentityPath)

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "target",
			value: protocol.DirectProbeTarget{
				TargetAgentID: "agent-b", Addresses: []agentproxy.Address{{URL: "https://b.example", Tag: "wan"}},
				EffectiveProxy: "http://proxy.example", AddressFingerprint: "fp-b", TargetGeneration: 12,
			},
			want: `{"target_agent_id":"agent-b","addresses":[{"url":"https://b.example","tag":"wan"}],"effective_proxy":"http://proxy.example","address_fingerprint":"fp-b","target_generation":12}`,
		},
		{
			name: "direct ingress identity",
			value: protocol.DirectIngressIdentity{
				Contract: protocol.DirectIngressContractV1, Role: "agent", AgentID: "agent-b",
			},
			want: `{"contract":"direct_ingress_v1","role":"agent","agent_id":"agent-b"}`,
		},
		{
			name: "ordered direct addresses update",
			value: protocol.AgentDirectAddressesUpdate{
				MasterInstanceID: "master-a", AgentID: "agent-b", SessionGeneration: 7, Sequence: 9,
				HTTPAddresses: []protocol.Address{{URL: "http://10.0.0.2:8140", Tag: "auto-detected"}},
			},
			want: `{"master_instance_id":"master-a","agent_id":"agent-b","session_generation":7,"sequence":9,"http_addresses":[{"url":"http://10.0.0.2:8140","tag":"auto-detected"}]}`,
		},
		{
			name: "direct addresses tombstone is explicit empty list",
			value: protocol.AgentDirectAddressesUpdate{
				MasterInstanceID: "master-a", AgentID: "agent-b", SessionGeneration: 7, Sequence: 10,
				HTTPAddresses: []protocol.Address{},
			},
			want: `{"master_instance_id":"master-a","agent_id":"agent-b","session_generation":7,"sequence":10,"http_addresses":[]}`,
		},
		{
			name: "result omits empty reason",
			value: protocol.DirectProbeResult{
				TargetAgentID: "agent-b", AddressFingerprint: "fp-b", Network: "reachable", Identity: "verified",
				Eligible: true, LatencyMS: 19, CheckedAt: 123,
			},
			want: `{"target_agent_id":"agent-b","address_fingerprint":"fp-b","network":"reachable","identity":"verified","eligible":true,"latency_ms":19,"checked_at":123}`,
		},
		{
			name:  "scope omits optional fields",
			value: protocol.ProbeScope{Kind: "all_enabled"},
			want:  `{"kind":"all_enabled"}`,
		},
		{
			name: "ack",
			value: protocol.ProbeAck{
				ProbeID: "probe-a", ProbeGeneration: 7, Scope: protocol.ProbeScope{Kind: "tag", Tag: "wan"},
				State: "queued", TargetTotal: 3, SnapshotSeq: 41,
			},
			want: `{"probe_id":"probe-a","probe_generation":7,"scope":{"kind":"tag","tag":"wan"},"state":"queued","target_total":3,"snapshot_seq":41}`,
		},
		{
			name: "progress omits zero timestamps",
			value: protocol.ManualProbeProgress{
				ProbeID: "probe-a", State: "running", TargetTotal: 3, Remaining: 2,
			},
			want: `{"probe_id":"probe-a","state":"running","target_total":3,"remaining":2}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.value)
			require.NoError(t, err)
			require.JSONEq(t, test.want, string(got))
			require.Equal(t, test.want, string(got))
		})
	}
}

func TestDirectProbeProtocolRoundTripKeepsOptionalValues(t *testing.T) {
	want := protocol.ManualProbeProgress{
		ProbeID: "probe-z", State: "completed", TargetTotal: 1, Remaining: 0,
		StartedAt: 100, CompletedAt: 110,
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got protocol.ManualProbeProgress
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got)
}
