package dao

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestChannelWindowUsage(t *testing.T) {
	ctx, db := setupAdminContext(t)
	q := NewAdminQuery(ctx).Channel()

	// channel 5: 三天数据;channel 6: 干扰;BYOK 行: 干扰
	rows := []models.ChannelDailyBilling{
		{Date: "2026-05-25", ChannelID: 5, PrivateChannelID: 0, RequestCount: 100, TotalCost: 1000},
		{Date: "2026-05-26", ChannelID: 5, PrivateChannelID: 0, RequestCount: 200, TotalCost: 2000},
		{Date: "2026-05-27", ChannelID: 5, PrivateChannelID: 0, RequestCount: 50, TotalCost: 500},
		{Date: "2026-05-27", ChannelID: 6, PrivateChannelID: 0, RequestCount: 999, TotalCost: 9999}, // 别的 channel
		{Date: "2026-05-27", ChannelID: 0, PrivateChannelID: 7, RequestCount: 888, TotalCost: 8888}, // BYOK 行
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("success: since 05-26 sums only channel 5 in range", func(t *testing.T) {
		calls, cost, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "since", SinceDate: "2026-05-26"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if calls != 250 || cost != 2500 {
			t.Fatalf("calls=%d cost=%d want 250/2500", calls, cost)
		}
	})

	t.Run("success: all (lifetime) sums full history of channel 5 only", func(t *testing.T) {
		calls, cost, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "all"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if calls != 350 || cost != 3500 {
			t.Fatalf("calls=%d cost=%d want 350/3500", calls, cost)
		}
	})

	t.Run("success: month prefix", func(t *testing.T) {
		calls, _, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "month", MonthPrefix: "2026-05"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if calls != 350 {
			t.Fatalf("calls=%d want 350", calls)
		}
	})

	t.Run("boundary: channel with no rows → 0/0", func(t *testing.T) {
		calls, cost, err := q.ChannelWindowUsage(999, WindowFilter{Kind: "all"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if calls != 0 || cost != 0 {
			t.Fatalf("calls=%d cost=%d want 0/0", calls, cost)
		}
	})
}
