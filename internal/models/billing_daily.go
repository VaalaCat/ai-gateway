package models

type TokenDailyBilling struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	Date             string `gorm:"size:10;uniqueIndex:idx_token_daily_billing_date_user_token" json:"date"`
	UserID           uint   `gorm:"uniqueIndex:idx_token_daily_billing_date_user_token;index" json:"user_id"`
	TokenID          uint   `gorm:"uniqueIndex:idx_token_daily_billing_date_user_token;index" json:"token_id"`
	TokenName        string `gorm:"size:64" json:"token_name"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `gorm:"index" json:"last_used_at"`
	CreatedAt        int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        int64  `gorm:"autoUpdateTime" json:"updated_at"`
}

type ChannelDailyBilling struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	Date             string `gorm:"size:10;uniqueIndex:idx_channel_daily_billing_date_channel" json:"date"`
	ChannelID        uint   `gorm:"uniqueIndex:idx_channel_daily_billing_date_channel;index" json:"channel_id"`
	ChannelName      string `gorm:"size:64" json:"channel_name"`
	ChannelType      int    `json:"channel_type"`
	RequestCount     int64  `json:"request_count"`
	SuccessCount     int64  `json:"success_count"`
	FailedCount      int64  `json:"failed_count"`
	PromptTokens     int64  `json:"prompt_tokens"`
	CompletionTokens int64  `json:"completion_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	InputCost        int64  `json:"input_cost"`
	OutputCost       int64  `json:"output_cost"`
	TotalCost        int64  `json:"total_cost"`
	LastUsedAt       int64  `gorm:"index" json:"last_used_at"`
	CreatedAt        int64  `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        int64  `gorm:"autoUpdateTime" json:"updated_at"`
}
