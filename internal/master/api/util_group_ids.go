package api

import "fmt"

// ValidateAllowedGroupIDs validates a slice of user-group IDs for token_template.
// Returns error if length > 100 or any ID is zero.
func ValidateAllowedGroupIDs(ids []uint) error {
	if len(ids) > 100 {
		return fmt.Errorf("allowed_group_ids length %d exceeds max 100", len(ids))
	}
	for _, id := range ids {
		if id == 0 {
			return fmt.Errorf("allowed_group_ids must contain non-zero IDs")
		}
	}
	return nil
}

// NormalizeAllowedGroupIDs converts an arbitrary JSON value (typically from a
// map[string]any update payload) into []uint, rejecting non-array or
// non-integer / negative values. A nil input returns (nil, nil).
func NormalizeAllowedGroupIDs(v any) ([]uint, error) {
	if v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("allowed_group_ids must be an array of unsigned integers")
	}
	out := make([]uint, 0, len(arr))
	for _, e := range arr {
		f, ok := e.(float64)
		if !ok || f < 0 || f != float64(uint(f)) {
			return nil, fmt.Errorf("allowed_group_ids contains non-integer or negative value: %v", e)
		}
		out = append(out, uint(f))
	}
	return out, nil
}
