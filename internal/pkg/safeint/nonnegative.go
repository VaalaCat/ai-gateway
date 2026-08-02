// Package safeint provides checked arithmetic for persisted non-negative
// counters and costs.
package safeint

import (
	"errors"
	"math"
)

// AddNonNegativeInt64 returns the exact sum of values. Negative inputs and
// signed overflow are rejected instead of being allowed to wrap.
func AddNonNegativeInt64(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 {
			return 0, errors.New("safeint: values must be non-negative")
		}
		if total > math.MaxInt64-value {
			return 0, errors.New("safeint: int64 addition overflow")
		}
		total += value
	}
	return total, nil
}
