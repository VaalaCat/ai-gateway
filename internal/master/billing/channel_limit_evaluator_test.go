package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestEvaluateLimit(t *testing.T) {
	now := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)

	t.Run("success: no config → not disable", func(t *testing.T) {
		dis, _, _, err := evaluateLimit(models.ChannelLimit{}, now, func(models.LimitRule) (int64, error) { return 0, nil })
		if err != nil || dis {
			t.Fatalf("dis=%v err=%v want false/nil", dis, err)
		}
	})

	t.Run("success: cutoff passed → permanent disable", func(t *testing.T) {
		limit := models.ChannelLimit{DisableAt: now.Add(-time.Hour).Unix()}
		dis, reason, auto, err := evaluateLimit(limit, now, func(models.LimitRule) (int64, error) { return 0, nil })
		if err != nil || !dis || reason != "cutoff" || auto {
			t.Fatalf("dis=%v reason=%q auto=%v err=%v", dis, reason, auto, err)
		}
	})

	t.Run("boundary: cutoff in future → not disable", func(t *testing.T) {
		limit := models.ChannelLimit{DisableAt: now.Add(time.Hour).Unix()}
		dis, _, _, _ := evaluateLimit(limit, now, func(models.LimitRule) (int64, error) { return 0, nil })
		if dis {
			t.Fatalf("dis=true, want false (cutoff in future)")
		}
	})

	t.Run("success: monthly cost over → disable auto-recoverable", func(t *testing.T) {
		limit := models.ChannelLimit{Rules: []models.LimitRule{{Metric: models.LimitMetricCost, Window: models.LimitWindowMonthly, Threshold: 1000}}}
		dis, reason, auto, _ := evaluateLimit(limit, now, func(r models.LimitRule) (int64, error) { return 1000, nil })
		if !dis || reason != "cost/monthly" || !auto {
			t.Fatalf("dis=%v reason=%q auto=%v want true/cost/monthly/true", dis, reason, auto)
		}
	})

	t.Run("boundary: lifetime over → disable NOT auto-recoverable", func(t *testing.T) {
		limit := models.ChannelLimit{Rules: []models.LimitRule{{Metric: models.LimitMetricCalls, Window: models.LimitWindowLifetime, Threshold: 100}}}
		dis, reason, auto, _ := evaluateLimit(limit, now, func(r models.LimitRule) (int64, error) { return 100, nil })
		if !dis || reason != "calls/lifetime" || auto {
			t.Fatalf("dis=%v reason=%q auto=%v want true/calls/lifetime/false", dis, reason, auto)
		}
	})

	t.Run("failure: under threshold → not disable", func(t *testing.T) {
		limit := models.ChannelLimit{Rules: []models.LimitRule{{Metric: models.LimitMetricCost, Window: models.LimitWindowDaily, Threshold: 1000}}}
		dis, _, _, _ := evaluateLimit(limit, now, func(r models.LimitRule) (int64, error) { return 999, nil })
		if dis {
			t.Fatalf("dis=true want false (999 < 1000)")
		}
	})
}

func TestReconcile(t *testing.T) {
	const enabled, disabled = 1, 0
	manual := models.ChannelLimitState{} // 手动禁:无 Tripped
	autoTrip := models.ChannelLimitState{Tripped: true, Reason: "cost/monthly", AutoRecover: true, TrippedAt: 1}
	permTrip := models.ChannelLimitState{Tripped: true, Reason: "cutoff", AutoRecover: false, TrippedAt: 1}
	now := int64(1000)

	t.Run("enabled + shouldDisable → disable + write state", func(t *testing.T) {
		st, state, changed := reconcile(enabled, models.ChannelLimitState{}, true, "cost/monthly", true, now)
		if !changed || st != disabled || !state.Tripped || !state.AutoRecover || state.TrippedAt != now {
			t.Fatalf("st=%d state=%+v changed=%v", st, state, changed)
		}
	})

	t.Run("auto-disabled + recoverable + back under → re-enable + clear", func(t *testing.T) {
		st, state, changed := reconcile(disabled, autoTrip, false, "", false, now)
		if !changed || st != enabled || state.Tripped {
			t.Fatalf("st=%d state=%+v changed=%v want enable+clear", st, state, changed)
		}
	})

	t.Run("permanent trip (cutoff) + back under → stay disabled", func(t *testing.T) {
		_, _, changed := reconcile(disabled, permTrip, false, "", false, now)
		if changed {
			t.Fatalf("changed=true want false (permanent stays)")
		}
	})

	t.Run("manually disabled (no Tripped) → evaluator never touches", func(t *testing.T) {
		_, _, c1 := reconcile(disabled, manual, false, "", false, now)
		_, _, c2 := reconcile(disabled, manual, true, "cost/daily", true, now)
		if c1 || c2 {
			t.Fatalf("manual disabled touched: c1=%v c2=%v", c1, c2)
		}
	})

	t.Run("enabled + stale Tripped state + not disable → clear stale", func(t *testing.T) {
		st, state, changed := reconcile(enabled, autoTrip, false, "", false, now)
		if !changed || st != enabled || state.Tripped {
			t.Fatalf("st=%d state=%+v changed=%v want clear stale", st, state, changed)
		}
	})

	t.Run("enabled + no state + not disable → no-op", func(t *testing.T) {
		_, _, changed := reconcile(enabled, models.ChannelLimitState{}, false, "", false, now)
		if changed {
			t.Fatalf("changed=true want false (clean no-op)")
		}
	})
}
