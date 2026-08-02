package api

import (
	"fmt"

	"github.com/VaalaCat/ai-gateway/internal/consts"
)

// ValidateStatusValue checks that the provided value is a valid status
// (consts.StatusEnabled or consts.StatusDisabled). The value may come from
// a JSON-decoded map[string]any (float64) or from a typed int field.
func ValidateStatusValue(v any) error {
	n, ok := toIntStrict(v)
	if !ok {
		return fmt.Errorf("status must be an integer, got %T", v)
	}
	if n != consts.StatusEnabled && n != consts.StatusDisabled {
		return fmt.Errorf("status must be %d (enabled) or %d (disabled), got %d",
			consts.StatusEnabled, consts.StatusDisabled, n)
	}
	return nil
}

// ValidateAutoBanValue 保证 auto_ban 与渠道 status 使用相同的严格 0/1 契约。
// map JSON 解码后的数字可以是 float64，但仍必须是精确的二态整数。
func ValidateAutoBanValue(v any) error {
	n, ok := toIntStrict(v)
	if !ok {
		return fmt.Errorf("auto_ban must be an integer, got %T", v)
	}
	if n != 0 && n != 1 {
		return fmt.Errorf("auto_ban must be 0 (disabled) or 1 (enabled), got %d", n)
	}
	return nil
}

func toIntStrict(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	case float64:
		if n != float64(int(n)) {
			return 0, false
		}
		return int(n), true
	case float32:
		if n != float32(int(n)) {
			return 0, false
		}
		return int(n), true
	default:
		return 0, false
	}
}

// StatusEqualsEnabled reports whether v decodes to consts.StatusEnabled.
// Call only after ValidateStatusValue has confirmed v is a legal status.
func StatusEqualsEnabled(v any) bool {
	n, ok := toIntStrict(v)
	return ok && n == consts.StatusEnabled
}
