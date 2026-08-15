package logqueue

import (
	"encoding/json"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// LogBatch is the complete non-billing write unit for one request. The
// request_id unique key makes replay safe; aggregate deltas are applied only
// when the request row is new.
type LogBatch struct {
	Request      models.RequestLog               `json:"request"`
	Traces       []models.RequestTrace           `json:"traces,omitempty"`
	TokenDaily   []models.TokenDailyBilling      `json:"token_daily,omitempty"`
	ChannelDaily []models.ChannelDailyBilling    `json:"channel_daily,omitempty"`
	Hourly       []models.UsageHourlyBucket      `json:"hourly,omitempty"`
	Duration     []models.UsageDurationHistogram `json:"duration,omitempty"`
	TTFT         []models.UsageTTFTHistogram     `json:"ttft,omitempty"`
	TPS          []models.UsageTPSHistogram      `json:"tps,omitempty"`
	UserTTFT     []models.UsageUserTTFTHistogram `json:"user_ttft,omitempty"`
	UserTPS      []models.UsageUserTPSHistogram  `json:"user_tps,omitempty"`
	APIRequest   *models.APIRequestLog           `json:"api_request,omitempty"`
	APITraces    []models.APIRequestTrace        `json:"api_traces,omitempty"`
}

func BatchSize(batch LogBatch) int64 {
	encoded, err := json.Marshal(batch)
	if err != nil {
		return 0
	}
	return int64(len(encoded))
}
