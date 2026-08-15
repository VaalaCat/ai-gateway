package protocol

import (
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
)

// APITraceEntry is the bounded Generic API execution trace returned by the
// execution Agent. Source-side trace enrichment remains owned by UsageBuilder.
type APITraceEntry = apiattempt.APIExecutionTrace

// APIUsageEntry is the Source Agent's single terminal record for one Generic
// API request. It is intentionally separate from the LLM UsageLogEntry shape.
type APIUsageEntry struct {
	RequestID string `json:"request_id"`
	UserID    uint   `json:"user_id"`
	TokenID   uint   `json:"token_id"`
	TokenName string `json:"token_name"`
	ClientIP  string `json:"client_ip"`

	APIServiceID  uint   `json:"api_service_id"`
	APIRouteID    uint   `json:"api_route_id"`
	APIUpstreamID uint   `json:"api_upstream_id"`
	ServiceName   string `json:"service_name"`
	RouteName     string `json:"route_name"`
	UpstreamName  string `json:"upstream_name"`

	Protocol string `json:"protocol"`
	Method   string `json:"method"`
	Subpath  string `json:"subpath"`

	SourceAgentID    string `json:"source_agent_id"`
	ExecutionAgentID string `json:"execution_agent_id"`
	AgentRouteID     uint   `json:"agent_route_id"`
	AgentRoutePath   string `json:"agent_route_path"`

	StatusCode         int   `json:"status_code"`
	DurationMs         int   `json:"duration_ms"`
	FirstByteMs        int   `json:"first_byte_ms"`
	RequestBytes       int64 `json:"request_bytes"`
	ResponseBytes      int64 `json:"response_bytes"`
	WebSocketCloseCode int   `json:"websocket_close_code"`

	ProviderDispatchKnown bool   `json:"provider_dispatch_known"`
	ProviderDispatched    bool   `json:"provider_dispatched"`
	QuotaGateDecision     string `json:"quota_gate_decision"`
	ErrorStage            string `json:"error_stage"`
	ErrorCode             string `json:"error_code"`

	RateLimitDecision      string                `json:"rate_limit_decision"`
	RateLimitWaitMs        int                   `json:"rate_limit_wait_ms"`
	RateLimitReason        string                `json:"rate_limit_reason"`
	RateLimitHits          []models.RateLimitHit `json:"rate_limit_hits,omitempty"`
	RateLimitHitTotal      int                   `json:"rate_limit_hit_total"`
	RateLimitHitsTruncated bool                  `json:"rate_limit_hits_truncated"`

	Trace     *APITraceEntry `json:"trace,omitempty"`
	Timestamp int64          `json:"timestamp"`
}
