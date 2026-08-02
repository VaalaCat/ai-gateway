// Package pricing contains shared validation for persisted pricing facts.
package pricing

import (
	"fmt"
	"math"
)

const (
	PlatformPriceRatioMin = 0.0
	PlatformPriceRatioMax = 1000.0
)

// ValidatePlatformPriceRatio enforces the canonical closed interval used by
// channel writes and every server-side pricing consumer.
func ValidatePlatformPriceRatio(value float64) error {
	if math.IsNaN(value) || math.IsInf(value, 0) ||
		value < PlatformPriceRatioMin || value > PlatformPriceRatioMax {
		return fmt.Errorf(
			"price_ratio must be finite and between %g and %g, got %g",
			PlatformPriceRatioMin,
			PlatformPriceRatioMax,
			value,
		)
	}
	return nil
}
