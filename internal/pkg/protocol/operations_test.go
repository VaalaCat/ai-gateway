package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestAgentOperationProtocolJSONExact(t *testing.T) {
	require.Equal(t, "agent.operation", consts.RPCAgentOperation)

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{
			name: "request omits optional zero values",
			value: protocol.OperationRequest{
				AgentID: "agent-a", Operation: "full_sync", ExpectedEpoch: "master-a",
			},
			want: `{"agent_id":"agent-a","operation":"full_sync","expected_epoch":"master-a"}`,
		},
		{
			name: "request keeps generation guards and typed parameters",
			value: protocol.OperationRequest{
				AgentID: "agent-a", Operation: "direct_circuit_reset", TargetAgentID: "agent-b",
				RequestID: "request-17", ExpectedEpoch: "master-a", ExpectedControlGeneration: 7,
				ExpectedRelayGeneration: 11,
			},
			want: `{"agent_id":"agent-a","operation":"direct_circuit_reset","target_agent_id":"agent-b","request_id":"request-17","expected_epoch":"master-a","expected_control_generation":7,"expected_relay_generation":11}`,
		},
		{
			name:  "ack",
			value: protocol.OperationAck{OperationID: "operation-1", State: "accepted", SnapshotSeq: 23},
			want:  `{"operation_id":"operation-1","state":"accepted","snapshot_seq":23}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := json.Marshal(test.value)
			require.NoError(t, err)
			require.Equal(t, test.want, string(got))
		})
	}
}

func TestAgentOperationProtocolRoundTripKeepsGenerationGuards(t *testing.T) {
	want := protocol.OperationRequest{
		AgentID: "agent-a", Operation: "interrupt", RequestID: "41", ExpectedEpoch: "master-a",
		ExpectedControlGeneration: 9, ExpectedRelayGeneration: 12,
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got protocol.OperationRequest
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got)
}
