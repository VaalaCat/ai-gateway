package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestBuildBillingAggregateBatchPreservesCompleteProjectionSemantics(t *testing.T) {
	dayOne := time.Date(2026, 7, 25, 23, 59, 0, 0, time.UTC).Unix()
	dayTwo := time.Date(2026, 7, 26, 0, 1, 0, 0, time.UTC).Unix()
	rawInput, rawOutput, rawRead, rawWrite := int64(11), int64(22), int64(33), int64(44)
	batch := BuildBillingAggregateBatch([]models.BillingLog{
		{
			RequestID: "admin", UserID: 1, TokenID: 2, TokenName: "admin-token",
			ChannelID: 3, PrivateChannelID: 0, ChannelName: "admin-channel", ChannelType: 4,
			OwnerType: "", ModelName: "admin-model", PromptTokens: 10, CompletionTokens: 20,
			CacheReadTokens: 30, CacheWriteTokens: 40, InputCost: 100, OutputCost: 200,
			CacheReadCost: 300, CacheWriteCost: 400, TotalCost: 1000,
			RawInputCost: &rawInput, RawOutputCost: &rawOutput, RawCacheReadCost: &rawRead, RawCacheWriteCost: &rawWrite,
			Status: 1, CreatedAt: dayOne,
		},
		{
			RequestID: "private", UserID: 7, TokenID: 8, TokenName: "private-token",
			ChannelID: 0, PrivateChannelID: 9, ChannelName: "private-channel", ChannelType: 10,
			OwnerType: "private", ModelName: "private-model", PromptTokens: 1, CompletionTokens: 2,
			CacheReadTokens: 3, CacheWriteTokens: 4, InputCost: 10, OutputCost: 20,
			CacheReadCost: 30, CacheWriteCost: 40, TotalCost: 100,
			Status: 0, CreatedAt: dayTwo,
		},
	})

	require.ElementsMatch(t, []dao.TokenDailyRow{
		{Date: "2026-07-25", UserID: 1, TokenID: 2, TokenName: "admin-token", RequestCount: 1, SuccessCount: 1, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, InputCost: 100, OutputCost: 200, TotalCost: 1000, LastUsedAt: dayOne, UpdatedAt: dayOne},
		{Date: "2026-07-26", UserID: 7, TokenID: 8, TokenName: "private-token", RequestCount: 1, FailedCount: 1, PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, InputCost: 10, OutputCost: 20, TotalCost: 100, LastUsedAt: dayTwo, UpdatedAt: dayTwo},
	}, batch.Tokens)
	require.ElementsMatch(t, []dao.ChannelDailyRow{
		{Date: "2026-07-25", ChannelID: 3, PrivateChannelID: 0, ChannelName: "admin-channel", ChannelType: 4, OwnerType: "admin", RequestCount: 1, SuccessCount: 1, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, InputCost: 100, OutputCost: 200, TotalCost: 1000, RawCost: 110, LastUsedAt: dayOne, UpdatedAt: dayOne},
		{Date: "2026-07-26", ChannelID: 0, PrivateChannelID: 9, ChannelName: "private-channel", ChannelType: 10, OwnerType: "private", RequestCount: 1, FailedCount: 1, PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, InputCost: 10, OutputCost: 20, TotalCost: 100, RawCost: 100, LastUsedAt: dayTwo, UpdatedAt: dayTwo},
	}, batch.Channels)
	require.ElementsMatch(t, []dao.BillingHourlyRow{
		{Date: "2026-07-25", Hour: 23, UserID: 1, TokenID: 2, ChannelID: 3, PrivateChannelID: 0, OwnerType: "admin", ModelName: "admin-model", TokenName: "admin-token", ChannelName: "admin-channel", ChannelType: 4, RequestCount: 1, SuccessCount: 1, PromptTokens: 10, CompletionTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, InputCost: 100, OutputCost: 200, CacheReadCost: 300, CacheWriteCost: 400, TotalCost: 1000, RawCost: 110, LastUsedAt: dayOne, UpdatedAt: dayOne},
		{Date: "2026-07-26", Hour: 0, UserID: 7, TokenID: 8, ChannelID: 0, PrivateChannelID: 9, OwnerType: "private", ModelName: "private-model", TokenName: "private-token", ChannelName: "private-channel", ChannelType: 10, RequestCount: 1, FailedCount: 1, PromptTokens: 1, CompletionTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, InputCost: 10, OutputCost: 20, CacheReadCost: 30, CacheWriteCost: 40, TotalCost: 100, RawCost: 100, LastUsedAt: dayTwo, UpdatedAt: dayTwo},
	}, batch.Hourly)
}
