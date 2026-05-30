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
