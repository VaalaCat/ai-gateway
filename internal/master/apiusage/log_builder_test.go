package apiusage

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

// Production break caught: an API terminal record must remain a typed API log
// throughout delivery, preserve request-id correlation, and not lose body or
// header/trailer capture flags during the source-to-log-db conversion.
func TestAPIUsageBuildLogBatchCorrelatesTraceAndPersistsCaptureFlags(t *testing.T) {
	batch, err := BuildLogBatch(protocol.APIUsageEntry{
		RequestID: "api-trace-1", UserID: 9, TokenID: 7, TokenName: "production", APIServiceID: 3,
		ServiceName: "weather", SourceAgentID: "edge", ExecutionAgentID: "worker",
		Protocol: "http", Method: "POST", Subpath: "/forecast", StatusCode: 201,
		ProviderDispatchKnown: true, ProviderDispatched: true,
		Trace: &apiattempt.APIExecutionTrace{
			SourceRequestHeaders:           map[string][]string{"X-Source": {"one"}},
			SourceRequestTrailersTruncated: true,
			SourceRequestBody:              &apiattempt.APIBodyCapture{Captured: true, Data: "source-body", Status: "captured", CapturedBytes: 11, TotalBytes: 19, Truncated: true},
			RequestHeaders:                 map[string][]string{"X-Upstream": {"two"}},
			RequestTrailers:                map[string][]string{"X-Request-Trailer": {"three"}},
			ResponseHeadersTruncated:       true,
			ResponseTrailers:               map[string][]string{"X-Response-Trailer": {"four"}},
			ResponseBody:                   &apiattempt.APIBodyCapture{Captured: false, Status: "skipped", SkipReason: "content_type"},
		},
	}, APISettlement{RequestID: "api-trace-1", Quota: 12345, SourceAgentID: "edge"}, &models.APIService{ID: 3, Name: "Weather", PricePerCall: 12345})

	require.NoError(t, err)
	require.NotNil(t, batch.APIRequest)
	require.Equal(t, "api-trace-1", batch.APIRequest.RequestID)
	require.Equal(t, "production", batch.APIRequest.TokenName)
	require.Equal(t, int64(12345), batch.APIRequest.UnitPrice)
	require.Equal(t, int64(12345), batch.APIRequest.TotalCost)
	require.False(t, batch.APIRequest.ServiceMissingAtSettlement)
	require.Len(t, batch.APITraces, 1)
	trace := batch.APITraces[0]
	require.Equal(t, "api-trace-1", trace.RequestID)
	require.Equal(t, []string{"one"}, trace.SourceRequestHeaders.Data()["X-Source"])
	require.True(t, trace.SourceRequestTrailersTruncated)
	require.Equal(t, "source-body", trace.SourceRequestBody.Data().Data)
	require.True(t, trace.SourceRequestBody.Data().Captured)
	require.Equal(t, "captured", trace.SourceRequestBody.Data().Status)
	require.Equal(t, int64(11), trace.SourceRequestBody.Data().CapturedBytes)
	require.Equal(t, int64(19), trace.SourceRequestBody.Data().TotalBytes)
	require.True(t, trace.SourceRequestBody.Data().Truncated)
	require.Equal(t, []string{"three"}, trace.RequestTrailers.Data()["X-Request-Trailer"])
	require.True(t, trace.ResponseHeadersTruncated)
	require.Equal(t, []string{"four"}, trace.ResponseTrailers.Data()["X-Response-Trailer"])
	require.False(t, trace.ResponseBody.Data().Captured)
	require.Equal(t, "skipped", trace.ResponseBody.Data().Status)
	require.Equal(t, "content_type", trace.ResponseBody.Data().SkipReason)
}

func TestAPIUsageBuildLogBatchPreservesEmptyTokenNameWithoutLookup(t *testing.T) {
	batch, err := BuildLogBatch(
		protocol.APIUsageEntry{RequestID: "api-empty-token", TokenID: 7, TokenName: ""},
		APISettlement{RequestID: "api-empty-token"},
		nil,
	)

	require.NoError(t, err)
	require.Equal(t, uint(7), batch.APIRequest.TokenID)
	require.Empty(t, batch.APIRequest.TokenName)
}
