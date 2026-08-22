package models

import "gorm.io/datatypes"

// APIRequestLog is the log-database record of a generic API request. Its
// fields are dispatch facts captured by the gateway; generic API requests do
// not have the LLM fallback-chain semantics.
type APIRequestLog struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	RequestID string `gorm:"size:64;not null;uniqueIndex" json:"request_id"`
	UserID    uint   `gorm:"index" json:"user_id"`
	TokenID   uint   `gorm:"index" json:"token_id"`
	TokenName string `gorm:"size:64" json:"token_name"`
	ClientIP  string `gorm:"size:64" json:"client_ip"`

	APIServiceID    uint   `gorm:"index" json:"api_service_id"`
	APIServiceName  string `gorm:"size:128" json:"api_service_name"`
	APIRouteID      uint   `gorm:"index" json:"api_route_id"`
	APIRouteName    string `gorm:"size:128" json:"api_route_name"`
	APIUpstreamID   uint   `gorm:"index" json:"api_upstream_id"`
	APIUpstreamName string `gorm:"size:128" json:"api_upstream_name"`

	Protocol APIProtocol `gorm:"size:16" json:"protocol"`
	Method   string      `gorm:"size:16" json:"method"`
	Subpath  string      `gorm:"type:text" json:"subpath"`

	SourceAgentID    string `gorm:"size:128;index" json:"source_agent_id"`
	ExecutionAgentID string `gorm:"size:128;index" json:"execution_agent_id"`
	AgentRouteID     uint   `gorm:"index" json:"agent_route_id"`
	AgentRoutePath   string `gorm:"size:256" json:"agent_route_path"`

	StatusCode         int   `gorm:"index" json:"status_code"`
	DurationMs         int   `json:"duration_ms"`
	FirstByteMs        int   `json:"first_byte_ms"`
	RequestBytes       int64 `json:"request_bytes"`
	ResponseBytes      int64 `json:"response_bytes"`
	WebSocketCloseCode *int  `json:"websocket_close_code"`

	ProviderDispatchKnown      bool                              `json:"provider_dispatch_known"`
	ProviderDispatched         bool                              `json:"provider_dispatched"`
	QuotaGateDecision          string                            `gorm:"size:16;index" json:"quota_gate_decision"`
	ErrorStage                 string                            `gorm:"size:32;index" json:"error_stage"`
	ErrorCode                  string                            `gorm:"size:64;index" json:"error_code"`
	ErrorMessage               string                            `gorm:"type:text" json:"error_message"`
	ServiceMissingAtSettlement bool                              `gorm:"index" json:"service_missing_at_settlement"`
	RateLimitDecision          string                            `gorm:"size:16;index" json:"rate_limit_decision"`
	RateLimitWaitMs            int                               `json:"rate_limit_wait_ms"`
	RateLimitReason            string                            `gorm:"size:256" json:"rate_limit_reason"`
	RateLimitHits              datatypes.JSONSlice[RateLimitHit] `gorm:"type:text" json:"rate_limit_hits"`
	UnitPrice                  int64                             `json:"unit_price"`
	TotalCost                  int64                             `json:"total_cost"`
	CreatedAt                  int64                             `gorm:"autoCreateTime;index" json:"created_at"`
}
