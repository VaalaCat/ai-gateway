package apiusage

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"gorm.io/datatypes"
)

func BuildLogBatch(entry protocol.APIUsageEntry, settlement APISettlement, service *models.APIService) (logqueue.LogBatch, error) {
	if entry.RequestID == "" || settlement.RequestID != entry.RequestID {
		return logqueue.LogBatch{}, fmt.Errorf("api usage request_id is required and must match settlement")
	}
	serviceName := entry.ServiceName
	if service != nil {
		serviceName = service.Name
	}
	request := &models.APIRequestLog{
		RequestID: entry.RequestID, UserID: entry.UserID, TokenID: entry.TokenID, TokenName: entry.TokenName, ClientIP: entry.ClientIP,
		APIServiceID: entry.APIServiceID, APIServiceName: serviceName, APIRouteID: entry.APIRouteID, APIRouteName: entry.RouteName,
		APIUpstreamID: entry.APIUpstreamID, APIUpstreamName: entry.UpstreamName, Protocol: models.APIProtocol(entry.Protocol), Method: entry.Method, Subpath: entry.Subpath,
		SourceAgentID: settlement.SourceAgentID, ExecutionAgentID: entry.ExecutionAgentID, AgentRouteID: entry.AgentRouteID, AgentRoutePath: entry.AgentRoutePath,
		StatusCode: entry.StatusCode, DurationMs: entry.DurationMs, FirstByteMs: entry.FirstByteMs, RequestBytes: entry.RequestBytes, ResponseBytes: entry.ResponseBytes,
		ProviderDispatchKnown: entry.ProviderDispatchKnown, ProviderDispatched: entry.ProviderDispatched, QuotaGateDecision: entry.QuotaGateDecision,
		ErrorStage: entry.ErrorStage, ErrorCode: entry.ErrorCode, RateLimitDecision: entry.RateLimitDecision, RateLimitWaitMs: entry.RateLimitWaitMs,
		RateLimitReason: entry.RateLimitReason, RateLimitHits: datatypes.NewJSONSlice(entry.RateLimitHits), UnitPrice: settlement.Quota, TotalCost: settlement.Quota, CreatedAt: entry.Timestamp,
	}
	if entry.WebSocketCloseCode != 0 {
		value := entry.WebSocketCloseCode
		request.WebSocketCloseCode = &value
	}
	batch := logqueue.LogBatch{APIRequest: request}
	if entry.Trace != nil {
		trace, err := apiTraceFromEntry(entry.RequestID, entry.Trace)
		if err != nil {
			return logqueue.LogBatch{}, err
		}
		batch.APITraces = []models.APIRequestTrace{trace}
	}
	return batch, nil
}

func apiTraceFromEntry(requestID string, trace *apiattempt.APIExecutionTrace) (models.APIRequestTrace, error) {
	result := models.APIRequestTrace{RequestID: requestID, SourceRequestHeaders: datatypes.NewJSONType(copyHeaders(trace.SourceRequestHeaders)), SourceRequestTrailers: datatypes.NewJSONType(copyHeaders(trace.SourceRequestTrailers)),
		SourceRequestHeadersTruncated: trace.SourceRequestHeadersTruncated, SourceRequestTrailersTruncated: trace.SourceRequestTrailersTruncated,
		RequestHeaders: datatypes.NewJSONType(copyHeaders(trace.RequestHeaders)), RequestTrailers: datatypes.NewJSONType(copyHeaders(trace.RequestTrailers)), RequestHeadersTruncated: trace.RequestHeadersTruncated, RequestTrailersTruncated: trace.RequestTrailersTruncated,
		ResponseHeaders: datatypes.NewJSONType(copyHeaders(trace.ResponseHeaders)), ResponseTrailers: datatypes.NewJSONType(copyHeaders(trace.ResponseTrailers)), ResponseHeadersTruncated: trace.ResponseHeadersTruncated, ResponseTrailersTruncated: trace.ResponseTrailersTruncated,
		SourceRequestBody: datatypes.NewJSONType(copyBody(trace.SourceRequestBody)), RequestBody: datatypes.NewJSONType(copyBody(trace.RequestBody)), ResponseBody: datatypes.NewJSONType(copyBody(trace.ResponseBody))}
	return result, nil
}

func copyHeaders(headers map[string][]string) map[string][]string {
	if headers == nil {
		return nil
	}
	copy := make(map[string][]string, len(headers))
	for key, values := range headers {
		copy[key] = append([]string(nil), values...)
	}
	return copy
}

func copyBody(body *apiattempt.APIBodyCapture) models.APIBodyCapture {
	if body == nil {
		return models.APIBodyCapture{}
	}
	return models.APIBodyCapture{Captured: body.Captured, Status: body.Status, SkipReason: body.SkipReason, Data: body.Data, CapturedBytes: body.CapturedBytes, TotalBytes: body.TotalBytes, Truncated: body.Truncated}
}
