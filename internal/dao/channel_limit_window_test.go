package dao

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestResolveWindow(t *testing.T) {
	// 固定参考时刻:2026-05-27(周三)14:03 UTC
	now := time.Date(2026, 5, 27, 14, 3, 0, 0, time.UTC)

	cases := []struct {
		name      string
		window    string
		days      int
		wantKind  string
		wantSince string
		wantMonth string
		wantErr   bool
	}{
		{name: "lifetime", window: models.LimitWindowLifetime, wantKind: "all"},
		{name: "daily", window: models.LimitWindowDaily, wantKind: "since", wantSince: "2026-05-27"},
		{name: "weekly (Mon of week)", window: models.LimitWindowWeekly, wantKind: "since", wantSince: "2026-05-25"},
		{name: "monthly", window: models.LimitWindowMonthly, wantKind: "month", wantMonth: "2026-05"},
		{name: "rolling N=1 → today only", window: models.LimitWindowRollingDays, days: 1, wantKind: "since", wantSince: "2026-05-27"},
		{name: "rolling N=7", window: models.LimitWindowRollingDays, days: 7, wantKind: "since", wantSince: "2026-05-21"},
		{name: "bad window", window: "hourly", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wf, err := ResolveWindow(tc.window, tc.days, now)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if wf.Kind != tc.wantKind {
				t.Fatalf("Kind=%q want %q", wf.Kind, tc.wantKind)
			}
			if wf.SinceDate != tc.wantSince {
				t.Fatalf("SinceDate=%q want %q", wf.SinceDate, tc.wantSince)
			}
			if wf.MonthPrefix != tc.wantMonth {
				t.Fatalf("MonthPrefix=%q want %q", wf.MonthPrefix, tc.wantMonth)
			}
		})
	}
}

func TestResolveWindow_CrossMonthRolling(t *testing.T) {
	// 边界:月初往回滚到上月。2026-06-02 滚动 5 天 → 2026-05-29。
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	wf, err := ResolveWindow(models.LimitWindowRollingDays, 5, now)
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if wf.SinceDate != "2026-05-29" {
		t.Fatalf("SinceDate=%q want 2026-05-29", wf.SinceDate)
	}
}
