package dao

import (
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
)

func TestChannelWindowUsage(t *testing.T) {
	core, db := setupStrictSplitDBs(t)
	forbidCoreBillingFactQueries(t, core)
	ctx := NewContext(&testApp{db: core, logDB: db, layoutMode: app.DatabaseLayoutSplit})
	q := NewAdminQuery(ctx).Channel()

	// channel 5: 三天数据(raw_cost 与 total_cost 不同,模拟折扣/免费);channel 6 / BYOK 行: 干扰
	rows := []models.ChannelDailyBilling{
		{Date: "2026-05-25", ChannelID: 5, PrivateChannelID: 0, RequestCount: 100, TotalCost: 1000, RawCost: 2000},
		{Date: "2026-05-26", ChannelID: 5, PrivateChannelID: 0, RequestCount: 200, TotalCost: 2000, RawCost: 4000},
		{Date: "2026-05-27", ChannelID: 5, PrivateChannelID: 0, RequestCount: 50, TotalCost: 500, RawCost: 1000},
		{Date: "2026-05-27", ChannelID: 6, PrivateChannelID: 0, RequestCount: 999, TotalCost: 9999, RawCost: 9999},
		{Date: "2026-05-27", ChannelID: 0, PrivateChannelID: 7, RequestCount: 888, TotalCost: 8888, RawCost: 8888},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("success: since 05-26 仅 channel 5 在区间内,折后/折前各自汇总", func(t *testing.T) {
		u, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "since", SinceDate: "2026-05-26"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if u.Calls != 250 || u.BilledCost != 2500 || u.RawCost != 5000 {
			t.Fatalf("calls=%d billed=%d raw=%d want 250/2500/5000", u.Calls, u.BilledCost, u.RawCost)
		}
	})

	t.Run("success: all(lifetime) 汇总 channel 5 全历史", func(t *testing.T) {
		u, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "all"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if u.Calls != 350 || u.BilledCost != 3500 || u.RawCost != 7000 {
			t.Fatalf("calls=%d billed=%d raw=%d want 350/3500/7000", u.Calls, u.BilledCost, u.RawCost)
		}
	})

	t.Run("success: month prefix 汇总 2026-05 内 channel 5 全部", func(t *testing.T) {
		u, err := q.ChannelWindowUsage(5, WindowFilter{Kind: "month", MonthPrefix: "2026-05"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if u.Calls != 350 || u.BilledCost != 3500 || u.RawCost != 7000 {
			t.Fatalf("calls=%d billed=%d raw=%d want 350/3500/7000", u.Calls, u.BilledCost, u.RawCost)
		}
	})

	t.Run("boundary: 无数据渠道 → 全 0", func(t *testing.T) {
		u, err := q.ChannelWindowUsage(999, WindowFilter{Kind: "all"})
		if err != nil {
			t.Fatalf("err=%v", err)
		}
		if u.Calls != 0 || u.BilledCost != 0 || u.RawCost != 0 {
			t.Fatalf("calls=%d billed=%d raw=%d want 0/0/0", u.Calls, u.BilledCost, u.RawCost)
		}
	})
}

func TestChannelWindowUsageReturnsLogDatabaseUnavailable(t *testing.T) {
	core, _ := setupStrictSplitDBs(t)
	q := NewAdminQuery(NewContext(&testApp{db: core, layoutMode: app.DatabaseLayoutSplit})).Channel()

	_, err := q.ChannelWindowUsage(1, WindowFilter{Kind: "all"})
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrLogDatabaseUnavailable))
}
