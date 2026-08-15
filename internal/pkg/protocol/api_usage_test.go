package protocol

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPIUsageEntryJSONRoundTripPreservesIndependentAPIFacts(t *testing.T) {
	var want APIUsageEntry
	require.NoError(t, json.Unmarshal([]byte(`{
		"request_id":"request","api_service_id":7,"api_route_id":9,"api_upstream_id":11,
		"source_agent_id":"source","execution_agent_id":"execution","provider_dispatch_known":true,
		"rate_limit_hit_total":2,"rate_limit_hits_truncated":true,
		"rate_limit_hits":[{"limiter_id":1,"name":"one","dimension":"rate/shared","bucket":"global:0:shared","decision":"allow"}]
	}`), &want))
	encoded, err := json.Marshal(want)
	require.NoError(t, err)
	var fields map[string]any
	require.NoError(t, json.Unmarshal(encoded, &fields))
	require.EqualValues(t, 2, fields["rate_limit_hit_total"])
	require.Equal(t, true, fields["rate_limit_hits_truncated"])
	var got APIUsageEntry
	require.NoError(t, json.Unmarshal(encoded, &got))
	require.Equal(t, want, got)
}

func TestReportedUsageRequiresExactlyOneTypedEntryAndUsesTypedQueueIdentity(t *testing.T) {
	llm := UsageLogEntry{RequestID: "shared"}
	api := APIUsageEntry{RequestID: "shared"}
	require.False(t, (ReportedUsage{}).Valid())
	require.False(t, (ReportedUsage{LLM: &llm, API: &api}).Valid())
	require.True(t, (ReportedUsage{LLM: &llm}).Valid())
	require.True(t, (ReportedUsage{API: &api}).Valid())
	require.Equal(t, "llm:shared", (ReportedUsage{LLM: &llm}).QueueID())
	require.Equal(t, "api:shared", (ReportedUsage{API: &api}).QueueID())
}

func TestUsageReportDecodesLegacyLogsOnlyAndRoundTripsMixedPayload(t *testing.T) {
	var legacy UsageReport
	require.NoError(t, json.Unmarshal([]byte(`{"agent_id":"legacy","logs":[{"request_id":"llm"}]}`), &legacy))
	require.Equal(t, "llm", legacy.Logs[0].RequestID)
	require.Empty(t, legacy.APIRequests)

	want := UsageReport{
		AgentID: "mixed", Logs: []UsageLogEntry{{RequestID: "llm"}},
		APIRequests: []APIUsageEntry{{RequestID: "api"}},
	}
	raw, err := json.Marshal(want)
	require.NoError(t, err)
	var got UsageReport
	require.NoError(t, json.Unmarshal(raw, &got))
	require.Equal(t, want, got)
}
