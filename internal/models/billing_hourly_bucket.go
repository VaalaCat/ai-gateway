package models

type BillingHourlyBucket struct {
	ID uint `gorm:"primaryKey" json:"id"`

	Date             string `gorm:"size:10;uniqueIndex:idx_bhb_bucket,priority:1;index:idx_bhb_model_user,priority:2;index:idx_bhb_user_token,priority:3;index:idx_bhb_user_window,priority:2;index:idx_bhb_window_user,priority:1" json:"date"`
	Hour             int    `gorm:"uniqueIndex:idx_bhb_bucket,priority:2;index:idx_bhb_model_user,priority:3;index:idx_bhb_user_token,priority:4;index:idx_bhb_user_window,priority:3;index:idx_bhb_window_user,priority:2" json:"hour"`
	UserID           uint   `gorm:"uniqueIndex:idx_bhb_bucket,priority:3;index:idx_bhb_model_user,priority:4;index:idx_bhb_user_token,priority:1;index:idx_bhb_user_window,priority:1;index:idx_bhb_window_user,priority:3" json:"user_id"`
	TokenID          uint   `gorm:"uniqueIndex:idx_bhb_bucket,priority:4;index:idx_bhb_user_token,priority:2" json:"token_id"`
	ChannelID        uint   `gorm:"uniqueIndex:idx_bhb_bucket,priority:5" json:"channel_id"`
	PrivateChannelID uint   `gorm:"uniqueIndex:idx_bhb_bucket,priority:6;default:0" json:"private_channel_id"`
	OwnerType        string `gorm:"size:8;uniqueIndex:idx_bhb_bucket,priority:7;default:'admin'" json:"owner_type"`
	ModelName        string `gorm:"size:128;uniqueIndex:idx_bhb_bucket,priority:8;index:idx_bhb_model_user,priority:1;index:idx_bhb_user_token,priority:5;index:idx_bhb_user_window,priority:4" json:"model_name"`

	TokenName   string `gorm:"size:64" json:"token_name"`
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
	CacheReadCost    int64 `json:"cache_read_cost"`
	CacheWriteCost   int64 `json:"cache_write_cost"`
	TotalCost        int64 `json:"total_cost"`
	RawCost          int64 `json:"raw_cost"`

	LastUsedAt int64 `gorm:"index" json:"last_used_at"`
	CreatedAt  int64 `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt  int64 `gorm:"autoUpdateTime" json:"updated_at"`
}
