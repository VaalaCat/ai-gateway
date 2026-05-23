package models

type UsageLog struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	UserID           uint   `gorm:"index" json:"user_id"`
	TokenID          uint   `gorm:"index" json:"token_id"`
	ChannelID        uint   `gorm:"index" json:"channel_id"`
	PrivateChannelID uint   `gorm:"index;default:0" json:"private_channel_id"`     // 0 = 非 BYOK 请求
	OwnerType        string `gorm:"size:8;default:'admin'" json:"owner_type"` // "admin" | "private"
	AgentID          string `gorm:"index;size:64" json:"agent_id"`
	ModelName        string `gorm:"size:128" json:"model_name"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	IsStream         bool   `json:"is_stream"`
	Duration         int    `json:"duration"`
	RequestID        string `gorm:"size:64;uniqueIndex" json:"request_id"`
	ClientIP         string `gorm:"size:64" json:"client_ip"`
	CreatedAt        int64  `gorm:"autoCreateTime;index" json:"created_at"`

	// Enhanced logging fields
	TokenName        string `gorm:"size:64" json:"token_name"`
	ChannelName      string `gorm:"size:64" json:"channel_name"`
	ChannelType      int    `json:"channel_type"`
	UpstreamModel    string `gorm:"size:128" json:"upstream_model"`
	FirstResponseMs  int    `json:"first_response_ms"`
	CacheReadTokens  int    `json:"cache_read_tokens"`
	CacheWriteTokens int    `json:"cache_write_tokens"`
	InboundProtocol  string `gorm:"size:16" json:"inbound_protocol"`
	OutboundProtocol string `gorm:"size:16" json:"outbound_protocol"`
	UseLegacy        bool   `json:"use_legacy"`
	Status           int    `json:"status"` // 1=成功, 0=失败
	ErrorMessage     string `gorm:"type:text" json:"error_message"`
	Other            string `gorm:"type:text" json:"other"`
	HasTrace         bool   `json:"has_trace"`
	TokenSource      string `gorm:"size:32" json:"token_source"`
	RoutingName        string `gorm:"size:128;index" json:"routing_name"`
	ErrorStage         string `gorm:"size:32;index" json:"error_stage"`
	InboundDecodeMs    int    `json:"inbound_decode_ms"`
	OutboundEncodeMs   int    `json:"outbound_encode_ms"`
	UpstreamDispatchMs int    `json:"upstream_dispatch_ms"`
	UpstreamDecodeMs   int    `json:"upstream_decode_ms"`
	ClientEncodeMs     int    `json:"client_encode_ms"`
}
