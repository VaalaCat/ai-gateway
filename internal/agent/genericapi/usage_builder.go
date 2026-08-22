package genericapi

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

type UsageBuilder struct{ now func() time.Time }

func NewUsageBuilder(now func() time.Time) *UsageBuilder {
	if now == nil {
		now = time.Now
	}
	return &UsageBuilder{now: now}
}

func (b *UsageBuilder) Build(execution APIExecution) protocol.APIUsageEntry {
	entry := protocol.APIUsageEntry{
		SourceAgentID: execution.SourceAgentID, StatusCode: execution.StatusCode,
		DurationMs: execution.DurationMs, APIUpstreamID: execution.Result.APIUpstreamID,
		FirstByteMs: execution.Result.FirstByteMs, RequestBytes: execution.Result.RequestBytes,
		ResponseBytes: execution.Result.ResponseBytes, WebSocketCloseCode: execution.Result.WebSocketCloseCode,
		ProviderDispatchKnown: execution.Result.ProviderDispatchKnown,
		ProviderDispatched:    execution.Result.ProviderDispatched,
		QuotaGateDecision:     execution.QuotaGateDecision,
		ErrorStage:            execution.Result.ErrorStage, ErrorCode: execution.Result.ErrorCode, ErrorMessage: execution.Result.ErrorMessage,
		RateLimitDecision:      execution.Result.RateLimitDecision,
		RateLimitWaitMs:        execution.Result.RateLimitWaitMs,
		RateLimitReason:        execution.Result.RateLimitReason,
		RateLimitHits:          append([]models.RateLimitHit(nil), execution.Result.RateLimitHits...),
		RateLimitHitTotal:      execution.Result.RateLimitHitTotal,
		RateLimitHitsTruncated: execution.Result.RateLimitHitsTruncated,
		Trace:                  execution.Result.Trace,
	}
	if b != nil && b.now != nil {
		entry.Timestamp = b.now().Unix()
	}
	projectAPIRequest(&entry, execution.Request)
	if execution.Err != nil {
		gateway := gatewayErrorFor(execution.Err)
		if entry.StatusCode == 0 || entry.StatusCode < 400 {
			entry.StatusCode = gateway.status
		}
		if entry.ErrorCode == "" {
			entry.ErrorCode = gateway.code
		}
		if entry.ErrorStage == "" {
			entry.ErrorStage = "gateway"
		}
	}
	if entry.StatusCode == 0 && execution.Result.UpstreamStatus != 0 {
		entry.StatusCode = execution.Result.UpstreamStatus
	}
	return entry
}

func projectAPIRequest(entry *protocol.APIUsageEntry, rc *RequestContext) {
	if entry == nil || rc == nil {
		return
	}
	entry.RequestID, entry.UserID, entry.TokenID, entry.TokenName = rc.RequestID, rc.UserID, rc.TokenID, rc.TokenName
	entry.APIServiceID, entry.APIRouteID = rc.Service.ID, rc.Route.ID
	entry.ServiceName, entry.RouteName = rc.Service.Name, rc.Route.Slug
	entry.Protocol, entry.Subpath = rc.Protocol, rc.Subpath
	entry.ExecutionAgentID, entry.AgentRouteID = rc.Agent.ExecutionAgentID, rc.Agent.AgentRouteID
	entry.AgentRoutePath = string(rc.Agent.AgentRoutePath)
	entry.UpstreamName = rc.UpstreamName
	if rc.Context != nil && rc.Context.Request != nil {
		entry.Method = rc.Context.Request.Method
		entry.ClientIP = rc.Context.ClientIP()
	}
}
