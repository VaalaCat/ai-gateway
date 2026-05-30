package dao

import (
	"fmt"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

// WindowFilter 描述对 channel_daily_billings.date 的过滤方式。
// Kind:
//
//	"all"   → 不限日期(lifetime)
//	"since" → date >= SinceDate
//	"month" → date LIKE MonthPrefix+'%'
type WindowFilter struct {
	Kind        string
	SinceDate   string // "YYYY-MM-DD"
	MonthPrefix string // "YYYY-MM"
}

// ResolveWindow 把限额窗口 + now 解析成 date 过滤条件。now 应为 UTC。
func ResolveWindow(window string, days int, now time.Time) (WindowFilter, error) {
	day := func(t time.Time) string { return t.Format("2006-01-02") }
	switch window {
	case models.LimitWindowLifetime:
		return WindowFilter{Kind: "all"}, nil
	case models.LimitWindowDaily:
		return WindowFilter{Kind: "since", SinceDate: day(now)}, nil
	case models.LimitWindowWeekly:
		// 周一为周首。Go: Sunday=0..Saturday=6;周一偏移 = (weekday+6)%7。
		offset := (int(now.Weekday()) + 6) % 7
		monday := now.AddDate(0, 0, -offset)
		return WindowFilter{Kind: "since", SinceDate: day(monday)}, nil
	case models.LimitWindowMonthly:
		return WindowFilter{Kind: "month", MonthPrefix: now.Format("2006-01")}, nil
	case models.LimitWindowRollingDays:
		if days < 1 {
			return WindowFilter{}, fmt.Errorf("rolling_days requires days >= 1, got %d", days)
		}
		since := now.AddDate(0, 0, -(days - 1))
		return WindowFilter{Kind: "since", SinceDate: day(since)}, nil
	default:
		return WindowFilter{}, fmt.Errorf("invalid window %q", window)
	}
}
