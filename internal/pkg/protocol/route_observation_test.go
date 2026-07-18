package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRouteEventAndEdgePayloadJSONIsStable(t *testing.T) {
	event := RouteEvent{
		TargetAgentID: "target-a", RouteID: 7, SelectorKind: "agent_tag", PathKind: "relay",
		Result: "error", Stage: "open", ReasonCode: "relay_not_ready", CommitState: "pre_commit",
		AddressFingerprint: "fp-a", DurationMS: 12, ObservedAt: 34, Sequence: 35,
	}
	data, err := json.Marshal(RouteTelemetryBatch{Generation: 9, Events: []RouteEvent{event}})
	require.NoError(t, err)
	require.JSONEq(t, `{"generation":9,"events":[{"target_agent_id":"target-a","route_id":7,"selector_kind":"agent_tag","path_kind":"relay","result":"error","stage":"open","reason_code":"relay_not_ready","commit_state":"pre_commit","address_fingerprint":"fp-a","duration_ms":12,"observed_at":34,"sequence":35}]}`, string(data))

	digest, err := json.Marshal(RouteEdgeDigest{Generation: 9, CoveredThrough: 35, Edges: []RouteEdgeSnapshot{{
		TargetAgentID: "target-a", RouteID: 7, SelectorKind: "agent_tag", LastUsedAt: 34,
		LastDirectResult: "relay_not_ready", AddressFingerprint: "fp-a", SuccessCount: 2, LatencyTotalMS: 24,
	}}})
	require.NoError(t, err)
	require.JSONEq(t, `{"generation":9,"edges":[{"target_agent_id":"target-a","route_id":7,"selector_kind":"agent_tag","last_used_at":34,"last_direct_result":"relay_not_ready","address_fingerprint":"fp-a","success_count":2,"latency_total_ms":24}],"covered_through":35}`, string(digest))
}

func TestRouteEventPayloadKeepsZeroValues(t *testing.T) {
	data, err := json.Marshal(RouteTelemetryBatch{Events: []RouteEvent{{}}})
	require.NoError(t, err)
	require.Contains(t, string(data), `"generation":0`)
	require.Contains(t, string(data), `"route_id":0`)
	require.Contains(t, string(data), `"duration_ms":0`)
	require.NotContains(t, string(data), `"sequence"`)

	digest, err := json.Marshal(RouteEdgeDigest{})
	require.NoError(t, err)
	require.NotContains(t, string(digest), `"covered_through"`)
}

func TestRouteFailureObservationsRoundTripWithoutChangingEdgeFields(t *testing.T) {
	original := RouteEvent{
		RequestID: "request-1", TargetAgentID: "target-a", RouteID: 7,
		PathKind: "relay", Result: "success", Stage: "dial", ReasonCode: "direct_dns", CommitState: "committed",
		Failures: []RouteFailureObservation{{
			PathKind: "direct", Stage: "dial", CommitState: "pre_commit", ReasonCode: "direct_dns",
		}},
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)
	require.JSONEq(t, `{
		"request_id":"request-1","target_agent_id":"target-a","route_id":7,"selector_kind":"",
		"path_kind":"relay","result":"success","stage":"dial","reason_code":"direct_dns",
		"commit_state":"committed","address_fingerprint":"","duration_ms":0,"observed_at":0,
		"failures":[{"path_kind":"direct","stage":"dial","commit_state":"pre_commit","reason_code":"direct_dns"}]
	}`, string(data))

	var decoded RouteEvent
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, original, decoded)
}
