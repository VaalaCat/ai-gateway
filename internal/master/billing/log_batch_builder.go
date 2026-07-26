package billing

import (
	"encoding/json"
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

func billingLogFromUsage(log *models.UsageLog) models.BillingLog {
	return models.BillingLog{
		RequestID: log.RequestID, UserID: log.UserID, TokenID: log.TokenID,
		TokenName: log.TokenName, ChannelID: log.ChannelID, PrivateChannelID: log.PrivateChannelID,
		OwnerType: normalizedOwnerType(log.OwnerType), ChannelName: log.ChannelName, ChannelType: log.ChannelType,
		ModelName: log.ModelName, PromptTokens: log.PromptTokens, CompletionTokens: log.CompletionTokens,
		CacheReadTokens: log.CacheReadTokens, CacheWriteTokens: log.CacheWriteTokens,
		InputCost: log.InputCost, OutputCost: log.OutputCost,
		CacheReadCost: log.CacheReadCost, CacheWriteCost: log.CacheWriteCost, TotalCost: log.TotalCost,
		RawInputCost: log.RawInputCost, RawOutputCost: log.RawOutputCost,
		RawCacheReadCost: log.RawCacheReadCost, RawCacheWriteCost: log.RawCacheWriteCost,
		BillingFactor: log.BillingFactor, PriceRatio: log.PriceRatio, Free: log.Free,
		Status: log.Status, CreatedAt: log.CreatedAt,
	}
}

func buildLogBatch(log models.UsageLog, entry protocol.UsageLogEntry) (logqueue.LogBatch, error) {
	traces, err := requestTraces(entry)
	if err != nil {
		return logqueue.LogBatch{}, err
	}
	log.ID = 0
	log.HasTrace = len(traces) > 0
	batch := logqueue.BuildRequestAggregateBatch(log)
	batch.Traces = traces
	return batch, nil
}

func requestTraces(entry protocol.UsageLogEntry) ([]models.RequestTrace, error) {
	if len(entry.AttemptTraces) > 0 {
		rows := make([]models.RequestTrace, 0, len(entry.AttemptTraces))
		for _, source := range entry.AttemptTraces {
			source.ID = 0
			source.RequestID = entry.RequestID
			rows = append(rows, models.RequestTrace(source))
		}
		return rows, nil
	}
	if entry.TraceData == "" {
		return nil, nil
	}
	var source models.UsageLogTrace
	if err := json.Unmarshal([]byte(entry.TraceData), &source); err != nil {
		return nil, fmt.Errorf("decode legacy trace: %w", err)
	}
	source.ID = 0
	source.RequestID = entry.RequestID
	source.AttemptIndex = 0
	return []models.RequestTrace{models.RequestTrace(source)}, nil
}

func normalizedOwnerType(ownerType string) string {
	if ownerType == "" {
		return "admin"
	}
	return ownerType
}
