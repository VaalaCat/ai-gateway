package channel

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
)

func TestSanitizeChannelLimitFields(t *testing.T) {
	t.Run("success: valid limit object passes, limit_state dropped", func(t *testing.T) {
		updates := map[string]any{
			"limit": map[string]any{
				"rules": []any{map[string]any{"metric": "cost", "window": "monthly", "threshold": float64(1000)}},
			},
			"limit_state": map[string]any{"tripped": true}, // 客户端伪造,应被丢弃
		}
		if err := sanitizeChannelLimitFields(updates); err != nil {
			t.Fatalf("err=%v want nil", err)
		}
		if _, ok := updates["limit_state"]; ok {
			t.Fatalf("limit_state should be dropped")
		}
		// 校验后 limit 应被回填成强类型 JSONType(保证 GORM 正确落库)
		lj, ok := updates["limit"].(interface{ Data() models.ChannelLimit })
		if !ok {
			t.Fatalf("limit type=%T want datatypes.JSONType[ChannelLimit]", updates["limit"])
		}
		if len(lj.Data().Rules) != 1 || lj.Data().Rules[0].Metric != "cost" {
			t.Fatalf("limit not round-tripped: %+v", lj.Data())
		}
	})

	t.Run("failure: invalid limit rejected", func(t *testing.T) {
		updates := map[string]any{
			"limit": map[string]any{
				"rules": []any{map[string]any{"metric": "tokens", "window": "monthly", "threshold": float64(1)}},
			},
		}
		if err := sanitizeChannelLimitFields(updates); err == nil {
			t.Fatalf("err=nil want validation error")
		}
	})

	t.Run("boundary: status present → limit_state cleared to empty (manual op)", func(t *testing.T) {
		updates := map[string]any{"status": float64(1)}
		if err := sanitizeChannelLimitFields(updates); err != nil {
			t.Fatalf("err=%v", err)
		}
		v, ok := updates["limit_state"]
		if !ok {
			t.Fatalf("status change must set empty limit_state")
		}
		jt, ok := v.(interface {
			Data() models.ChannelLimitState
		})
		if !ok {
			t.Fatalf("limit_state type=%T want datatypes.JSONType[ChannelLimitState]", v)
		}
		if jt.Data().Tripped {
			t.Fatalf("cleared state must have Tripped=false")
		}
	})

	t.Run("security: status + spoofed limit_state → clean empty state wins", func(t *testing.T) {
		updates := map[string]any{
			"status":      float64(1),
			"limit_state": map[string]any{"tripped": true, "reason": "spoofed"},
		}
		if err := sanitizeChannelLimitFields(updates); err != nil {
			t.Fatalf("err=%v", err)
		}
		jt, ok := updates["limit_state"].(interface {
			Data() models.ChannelLimitState
		})
		if !ok {
			t.Fatalf("limit_state type=%T want JSONType", updates["limit_state"])
		}
		if jt.Data().Tripped || jt.Data().Reason != "" {
			t.Fatalf("spoofed state survived: %+v", jt.Data())
		}
	})

	t.Run("boundary: no limit/status → no-op", func(t *testing.T) {
		updates := map[string]any{"name": "x"}
		if err := sanitizeChannelLimitFields(updates); err != nil {
			t.Fatalf("err=%v", err)
		}
		if _, ok := updates["limit_state"]; ok {
			t.Fatalf("should not inject limit_state when no status change")
		}
	})
}
