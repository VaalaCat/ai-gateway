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

// ChannelDailyBilling 按天聚合 channel 维度用量。
//
// 同时承担两类来源：
//   - admin channel: channel_id>0, private_channel_id=0, owner_type="admin"
//   - BYOK channel: channel_id=0, private_channel_id>0, owner_type="private"
//
// 唯一键是 (date, channel_id, private_channel_id) 三列联合，保证两类行
// 不会因为对方那一列都填 0 而互相冲突——同一天内 admin channel 5 落在
// (date, 5, 0)，BYOK pchan 7 落在 (date, 0, 7)，互不影响。
//
// 旧 unique index idx_channel_daily_billing_date_channel 由 migrate.go 的
// dropLegacyChannelBillingIndex 在启动时自动 drop（SQLite IF EXISTS 幂等）。
type ChannelDailyBilling struct {
	ID               uint   `gorm:"primaryKey" json:"id"`
	Date             string `gorm:"size:10;uniqueIndex:idx_cdb_date_channel_pchan" json:"date"`
	ChannelID        uint   `gorm:"uniqueIndex:idx_cdb_date_channel_pchan;index" json:"channel_id"`
	PrivateChannelID uint   `gorm:"uniqueIndex:idx_cdb_date_channel_pchan;index;default:0" json:"private_channel_id"`
	OwnerType        string `gorm:"size:8;default:'admin'" json:"owner_type"` // "admin" | "private"
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
