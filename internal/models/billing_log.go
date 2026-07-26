package models

type BillingLog struct {
	ID                uint     `gorm:"primaryKey" json:"id"`
	RequestID         string   `gorm:"size:64;uniqueIndex" json:"request_id"`
	UserID            uint     `gorm:"index" json:"user_id"`
	TokenID           uint     `gorm:"index" json:"token_id"`
	TokenName         string   `gorm:"size:64" json:"token_name"`
	ChannelID         uint     `gorm:"index" json:"channel_id"`
	PrivateChannelID  uint     `gorm:"index;default:0" json:"private_channel_id"`
	OwnerType         string   `gorm:"size:8;default:'admin'" json:"owner_type"`
	ChannelName       string   `gorm:"size:64" json:"channel_name"`
	ChannelType       int      `json:"channel_type"`
	ModelName         string   `gorm:"size:128;index" json:"model_name"`
	PromptTokens      int      `json:"prompt_tokens"`
	CompletionTokens  int      `json:"completion_tokens"`
	CacheReadTokens   int      `json:"cache_read_tokens"`
	CacheWriteTokens  int      `json:"cache_write_tokens"`
	InputCost         int64    `json:"input_cost"`
	OutputCost        int64    `json:"output_cost"`
	CacheReadCost     int64    `json:"cache_read_cost"`
	CacheWriteCost    int64    `json:"cache_write_cost"`
	TotalCost         int64    `json:"total_cost"`
	RawInputCost      *int64   `json:"raw_input_cost"`
	RawOutputCost     *int64   `json:"raw_output_cost"`
	RawCacheReadCost  *int64   `json:"raw_cache_read_cost"`
	RawCacheWriteCost *int64   `json:"raw_cache_write_cost"`
	BillingFactor     *float64 `json:"billing_factor"`
	PriceRatio        float64  `gorm:"default:1" json:"price_ratio"`
	Free              bool     `json:"free"`
	Status            int      `json:"status"`
	CreatedAt         int64    `gorm:"autoCreateTime;index" json:"created_at"`
}

func (l *BillingLog) RawTotal() int64 {
	if l.RawInputCost == nil && l.RawOutputCost == nil &&
		l.RawCacheReadCost == nil && l.RawCacheWriteCost == nil {
		return l.TotalCost
	}
	var sum int64
	for _, cost := range []*int64{l.RawInputCost, l.RawOutputCost, l.RawCacheReadCost, l.RawCacheWriteCost} {
		if cost != nil {
			sum += *cost
		}
	}
	return sum
}
