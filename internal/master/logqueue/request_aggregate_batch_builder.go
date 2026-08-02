package logqueue

import (
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/durhist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tpshist"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ttfthist"
)

// BuildRequestAggregateBatch returns the request row and every aggregate delta
// derived from it. Trace rows remain the caller's responsibility.
func BuildRequestAggregateBatch(log models.UsageLog) LogBatch {
	log.ID = 0
	batch := LogBatch{
		Request:      models.RequestLog(log),
		TokenDaily:   []models.TokenDailyBilling{requestTokenDailyDelta(log)},
		ChannelDaily: []models.ChannelDailyBilling{requestChannelDailyDelta(log)},
		Hourly:       []models.UsageHourlyBucket{requestHourlyDelta(log)},
	}
	appendRequestHistograms(&batch, log)
	return batch
}

func requestTokenDailyDelta(log models.UsageLog) models.TokenDailyBilling {
	ts := log.CreatedAt
	success, failed := int64(0), int64(1)
	if log.Status != 0 {
		success, failed = 1, 0
	}
	return models.TokenDailyBilling{
		Date:   time.Unix(ts, 0).UTC().Format("2006-01-02"),
		UserID: log.UserID, TokenID: log.TokenID, TokenName: log.TokenName,
		RequestCount: 1, SuccessCount: success, FailedCount: failed,
		PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens: int64(log.CacheReadTokens), CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost: log.InputCost, OutputCost: log.OutputCost, TotalCost: log.TotalCost,
		LastUsedAt: ts, CreatedAt: ts, UpdatedAt: ts,
	}
}

func requestChannelDailyDelta(log models.UsageLog) models.ChannelDailyBilling {
	ts := log.CreatedAt
	success, failed := int64(0), int64(1)
	if log.Status != 0 {
		success, failed = 1, 0
	}
	return models.ChannelDailyBilling{
		Date:      time.Unix(ts, 0).UTC().Format("2006-01-02"),
		ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID,
		OwnerType: normalizedOwnerType(log.OwnerType), ChannelName: log.ChannelName, ChannelType: log.ChannelType,
		RequestCount: 1, SuccessCount: success, FailedCount: failed,
		PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens: int64(log.CacheReadTokens), CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost: log.InputCost, OutputCost: log.OutputCost, TotalCost: log.TotalCost, RawCost: log.RawTotal(),
		LastUsedAt: ts, CreatedAt: ts, UpdatedAt: ts,
	}
}

func requestHourlyDelta(log models.UsageLog) models.UsageHourlyBucket {
	ts := log.CreatedAt
	dateHour := time.Unix(ts, 0).UTC()
	success, failed := int64(0), int64(1)
	if log.Status != 0 {
		success, failed = 1, 0
	}
	row := models.UsageHourlyBucket{
		Date: dateHour.Format("2006-01-02"), Hour: dateHour.Hour(),
		ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID,
		ModelName: log.ModelName, AgentID: log.AgentID,
		OwnerType: normalizedOwnerType(log.OwnerType), ChannelName: log.ChannelName, ChannelType: log.ChannelType,
		RequestCount: 1, SuccessCount: success, FailedCount: failed,
		PromptTokens: int64(log.PromptTokens), CompletionTokens: int64(log.CompletionTokens),
		CacheReadTokens: int64(log.CacheReadTokens), CacheWriteTokens: int64(log.CacheWriteTokens),
		InputCost: log.InputCost, OutputCost: log.OutputCost, TotalCost: log.TotalCost, RawCost: log.RawTotal(),
		LastUsedAt: ts, CreatedAt: ts, UpdatedAt: ts,
	}
	if log.Status == 1 {
		row.SumInboundDecodeMs = int64(log.InboundDecodeMs)
		row.SumUpstreamDispatchMs = int64(log.UpstreamDispatchMs)
		row.SumUpstreamDecodeMs = int64(log.UpstreamDecodeMs)
		row.SumOutboundEncodeMs = int64(log.OutboundEncodeMs)
		row.SumClientEncodeMs = int64(log.ClientEncodeMs)
	}
	if eligibleTTFTSample(log) {
		row.StreamRequestCount = 1
		row.SumFirstResponseMs = int64(log.FirstResponseMs)
	}
	if eligibleTPSSample(log) {
		row.SumGenerationMs = int64(log.Duration - log.FirstResponseMs)
		row.SumStreamCompletionTokens = int64(log.CompletionTokens)
	}
	return row
}

func appendRequestHistograms(batch *LogBatch, log models.UsageLog) {
	ts := log.CreatedAt
	dateHour := time.Unix(ts, 0).UTC()
	date, hour := dateHour.Format("2006-01-02"), dateHour.Hour()
	if log.Status == 1 {
		values := [17]int64{}
		values[durhist.SlotIndex(int64(log.Duration))] = 1
		batch.Duration = []models.UsageDurationHistogram{durationHistogram(date, hour, log, values, int64(log.Duration), ts)}
	}
	if eligibleTTFTSample(log) {
		values := [17]int64{}
		ttft := int64(log.FirstResponseMs)
		values[ttfthist.SlotIndex(ttft)] = 1
		batch.TTFT = []models.UsageTTFTHistogram{ttftHistogram(date, hour, log, values, ttft, ts)}
		if log.UserID > 0 {
			batch.UserTTFT = []models.UsageUserTTFTHistogram{userTTFTHistogram(date, hour, log, values, ttft, ts)}
		}
	}
	if eligibleTPSSample(log) {
		values := [17]int64{}
		tps := tpshist.TokensPerSecond(int64(log.CompletionTokens), int64(log.Duration-log.FirstResponseMs))
		values[tpshist.SlotIndex(tps)] = 1
		batch.TPS = []models.UsageTPSHistogram{tpsHistogram(date, hour, log, values, tps, ts)}
		if log.UserID > 0 {
			batch.UserTPS = []models.UsageUserTPSHistogram{userTPSHistogram(date, hour, log, values, tps, ts)}
		}
	}
}

func eligibleTTFTSample(log models.UsageLog) bool {
	return log.IsStream && log.Status == 1 && log.FirstResponseMs > 0
}

func eligibleTPSSample(log models.UsageLog) bool {
	return log.IsStream && log.Status == 1 && log.CompletionTokens > 0 && log.Duration-log.FirstResponseMs > 0
}

func normalizedOwnerType(ownerType string) string {
	if ownerType == "" {
		return "admin"
	}
	return ownerType
}

func durationHistogram(date string, hour int, log models.UsageLog, h [17]int64, max, ts int64) models.UsageDurationHistogram {
	return models.UsageDurationHistogram{Date: date, Hour: hour, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID, ModelName: log.ModelName, AgentID: log.AgentID, MaxDurationMs: max,
		H0: h[0], H1: h[1], H2: h[2], H3: h[3], H4: h[4], H5: h[5], H6: h[6], H7: h[7], H8: h[8], H9: h[9], H10: h[10], H11: h[11], H12: h[12], H13: h[13], H14: h[14], H15: h[15], H16: h[16], CreatedAt: ts, UpdatedAt: ts}
}

func ttftHistogram(date string, hour int, log models.UsageLog, h [17]int64, max, ts int64) models.UsageTTFTHistogram {
	return models.UsageTTFTHistogram{Date: date, Hour: hour, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID, ModelName: log.ModelName, AgentID: log.AgentID, MaxFirstResponseMs: max,
		H0: h[0], H1: h[1], H2: h[2], H3: h[3], H4: h[4], H5: h[5], H6: h[6], H7: h[7], H8: h[8], H9: h[9], H10: h[10], H11: h[11], H12: h[12], H13: h[13], H14: h[14], H15: h[15], H16: h[16], CreatedAt: ts, UpdatedAt: ts}
}

func tpsHistogram(date string, hour int, log models.UsageLog, h [17]int64, max, ts int64) models.UsageTPSHistogram {
	return models.UsageTPSHistogram{Date: date, Hour: hour, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID, ModelName: log.ModelName, AgentID: log.AgentID, MaxTps: max,
		H0: h[0], H1: h[1], H2: h[2], H3: h[3], H4: h[4], H5: h[5], H6: h[6], H7: h[7], H8: h[8], H9: h[9], H10: h[10], H11: h[11], H12: h[12], H13: h[13], H14: h[14], H15: h[15], H16: h[16], CreatedAt: ts, UpdatedAt: ts}
}

func userTTFTHistogram(date string, hour int, log models.UsageLog, h [17]int64, max, ts int64) models.UsageUserTTFTHistogram {
	return models.UsageUserTTFTHistogram{Date: date, Hour: hour, UserID: log.UserID, ModelName: log.ModelName, MaxFirstResponseMs: max,
		H0: h[0], H1: h[1], H2: h[2], H3: h[3], H4: h[4], H5: h[5], H6: h[6], H7: h[7], H8: h[8], H9: h[9], H10: h[10], H11: h[11], H12: h[12], H13: h[13], H14: h[14], H15: h[15], H16: h[16], CreatedAt: ts, UpdatedAt: ts}
}

func userTPSHistogram(date string, hour int, log models.UsageLog, h [17]int64, max, ts int64) models.UsageUserTPSHistogram {
	return models.UsageUserTPSHistogram{Date: date, Hour: hour, UserID: log.UserID, ModelName: log.ModelName, MaxTps: max,
		H0: h[0], H1: h[1], H2: h[2], H3: h[3], H4: h[4], H5: h[5], H6: h[6], H7: h[7], H8: h[8], H9: h[9], H10: h[10], H11: h[11], H12: h[12], H13: h[13], H14: h[14], H15: h[15], H16: h[16], CreatedAt: ts, UpdatedAt: ts}
}
