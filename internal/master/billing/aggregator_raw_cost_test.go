package billing

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestAggregator_AccumulatesRawCost(t *testing.T) {
	a := newAggregatorForTest(t)

	// 免费渠道行:TotalCost 清零,但 Raw* 桶保留原价
	free := &models.BillingLog{
		UserID: 1, TokenID: 2, OwnerType: "admin",
		ChannelID: 3, ChannelName: "c", ChannelType: 1,
		ModelName: "m", Status: 1, CreatedAt: 1700000000,
		TotalCost:    0,
		RawInputCost: ptrI64(40), RawOutputCost: ptrI64(60),
	}
	a.SubmitBilling(free)
	a.SubmitBilling(free) // 同 key 累加

	_, channels, _ := a.Snapshot()
	require.Len(t, channels, 1)
	for _, cd := range channels {
		require.Equal(t, int64(0), cd.TotalCost, "免费行折后成本应为 0")
		require.Equal(t, int64(200), cd.RawCost, "折前原价应累加: (40+60)*2")
	}
}

func ptrI64(v int64) *int64 { return &v }
