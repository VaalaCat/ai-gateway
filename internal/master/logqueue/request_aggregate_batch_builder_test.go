package logqueue

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestBuildRequestAggregateBatchBuildsDailyBillingDeltas(t *testing.T) {
	log := dailyBillingFixture()[1]

	batch := BuildRequestAggregateBatch(log)

	require.Len(t, batch.TokenDaily, 1)
	require.Equal(t, models.TokenDailyBilling{
		Date: "2026-07-24", UserID: 7, TokenID: 70, TokenName: "renamed-free-token",
		RequestCount: 1, FailedCount: 1, PromptTokens: 3, CompletionTokens: 2, CacheReadTokens: 1, CacheWriteTokens: 1,
		LastUsedAt: log.CreatedAt, CreatedAt: log.CreatedAt, UpdatedAt: log.CreatedAt,
	}, batch.TokenDaily[0])
	require.Len(t, batch.ChannelDaily, 1)
	require.Equal(t, models.ChannelDailyBilling{
		Date: "2026-07-24", ChannelID: 10, OwnerType: "admin", ChannelName: "admin-renamed-free", ChannelType: 2,
		RequestCount: 1, FailedCount: 1, PromptTokens: 3, CompletionTokens: 2, CacheReadTokens: 1, CacheWriteTokens: 1,
		RawCost: log.RawTotal(), LastUsedAt: log.CreatedAt, CreatedAt: log.CreatedAt, UpdatedAt: log.CreatedAt,
	}, batch.ChannelDaily[0])
}
