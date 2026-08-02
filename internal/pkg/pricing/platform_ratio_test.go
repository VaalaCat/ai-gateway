package pricing

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidatePlatformPriceRatioUsesCanonicalClosedRange(t *testing.T) {
	tests := []struct {
		name    string
		value   float64
		wantErr bool
	}{
		{name: "zero", value: 0},
		{name: "upper boundary", value: 1000},
		{name: "below zero", value: -0.1, wantErr: true},
		{name: "above upper boundary", value: 1000.1, wantErr: true},
		{name: "nan", value: math.NaN(), wantErr: true},
		{name: "positive infinity", value: math.Inf(1), wantErr: true},
		{name: "negative infinity", value: math.Inf(-1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePlatformPriceRatio(tt.value)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}
