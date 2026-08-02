package pricing

import (
	"errors"
	"fmt"
	"math"
)

var ErrInvalidModelPrices = errors.New("invalid model prices")

type modelPriceBucket struct {
	name  string
	value float64
}

// ValidateModelPrices enforces the persisted four-bucket model-price contract.
// Zero is a valid price; negative and non-finite amounts are not.
func ValidateModelPrices(input, output, cacheRead, cacheWrite float64) error {
	buckets := [...]modelPriceBucket{
		{name: "input_price", value: input},
		{name: "output_price", value: output},
		{name: "cache_read_price", value: cacheRead},
		{name: "cache_write_price", value: cacheWrite},
	}
	for _, bucket := range buckets {
		if bucket.value < 0 || math.IsNaN(bucket.value) || math.IsInf(bucket.value, 0) {
			return fmt.Errorf("%w: %s must be finite and non-negative", ErrInvalidModelPrices, bucket.name)
		}
	}
	return nil
}
