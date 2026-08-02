package safeint

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddNonNegativeInt64ChecksEveryBoundary(t *testing.T) {
	tests := []struct {
		name    string
		values  []int64
		want    int64
		wantErr string
	}{
		{name: "empty is zero", want: 0},
		{name: "exact maximum succeeds", values: []int64{math.MaxInt64 - 2, 1, 1}, want: math.MaxInt64},
		{name: "final addition overflows", values: []int64{math.MaxInt64, 1}, wantErr: "overflow"},
		{name: "intermediate addition overflows", values: []int64{math.MaxInt64 - 1, 1, 1}, wantErr: "overflow"},
		{name: "negative input is rejected", values: []int64{1, -1}, wantErr: "non-negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := AddNonNegativeInt64(tt.values...)
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}
