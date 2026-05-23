package models

// UsageHourlyBucket 是小时级用量聚合表。
//
// 维度: (date, hour, channel_id, private_channel_id, model_name, agent_id)
// 故意不带 user_id —— 用户级 hour 趋势走 UsageLog 原表; 带 user_id 会让
// 基数膨胀到不可控 (1k user × 20 model × 5 ch × 24h ≈ 240w 行/天)。
//
// 维度约定与 ChannelDailyBilling 一致:
//
//	admin channel 行: (D, H, 5, 0, "gpt-4o", "cn-1")
//	BYOK   pchan 行: (D, H, 0, 7, "gpt-4o", "cn-1")
type UsageHourlyBucket struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Date             string `gorm:"size:10;uniqueIndex:idx_uhb_bucket" json:"date"`              // YYYY-MM-DD UTC
	Hour             int    `gorm:"uniqueIndex:idx_uhb_bucket" json:"hour"`                      // 0..23 UTC
	ChannelID        uint   `gorm:"uniqueIndex:idx_uhb_bucket;index" json:"channel_id"`          // 0 = BYOK
	PrivateChannelID uint   `gorm:"uniqueIndex:idx_uhb_bucket;index;default:0" json:"private_channel_id"` // 0 = admin
	ModelName        string `gorm:"size:128;uniqueIndex:idx_uhb_bucket;index" json:"model_name"` // e.g. "gpt-4o"
	AgentID          string `gorm:"size:64;uniqueIndex:idx_uhb_bucket;index" json:"agent_id"`    // e.g. "cn-1"

	OwnerType   string `gorm:"size:8;default:'admin'" json:"owner_type"`
	ChannelName string `gorm:"size:64" json:"channel_name"`
	ChannelType int    `json:"channel_type"`

	RequestCount     int64 `json:"request_count"`
	SuccessCount     int64 `json:"success_count"`
	FailedCount      int64 `json:"failed_count"`
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
	InputCost        int64 `json:"input_cost"`
	OutputCost       int64 `json:"output_cost"`
	TotalCost        int64 `json:"total_cost"`

	// 速度累计 (仅 stream + success + completion_tokens > 0 才入)
	StreamRequestCount        int64 `json:"stream_request_count"`
	SumFirstResponseMs        int64 `json:"sum_first_response_ms"`
	SumGenerationMs           int64 `json:"sum_generation_ms"`          // = Duration − FirstResponseMs
	SumStreamCompletionTokens int64 `json:"sum_stream_completion_tokens"`

	// 五段延迟累计 (仅成功)
	SumInboundDecodeMs    int64 `json:"sum_inbound_decode_ms"`
	SumUpstreamDispatchMs int64 `json:"sum_upstream_dispatch_ms"`
	SumUpstreamDecodeMs   int64 `json:"sum_upstream_decode_ms"`
	SumOutboundEncodeMs   int64 `json:"sum_outbound_encode_ms"`
	SumClientEncodeMs     int64 `json:"sum_client_encode_ms"`

	LastUsedAt int64 `gorm:"index" json:"last_used_at"`
	CreatedAt  int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  int64 `gorm:"autoUpdateTime" json:"updated_at"`
}
