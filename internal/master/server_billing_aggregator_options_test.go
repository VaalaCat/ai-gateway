package master

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/stretchr/testify/require"
)

type billingAggregatorSettingStub map[string]int

func (s billingAggregatorSettingStub) LookupInt(key string, fallback int) int {
	value, ok := s[key]
	if !ok {
		return fallback
	}
	return value
}

func TestBuildBillingAggregatorOptions(t *testing.T) {
	tests := []struct {
		name     string
		settings billingAggregatorSettingStub
		want     billing.AggregatorOptions
	}{
		{
			name: "defaults keep live projections moving",
			want: billing.AggregatorOptions{FlushEvery: 30 * time.Second, MaxRows: 5000},
		},
		{
			name: "settings override both limits",
			settings: billingAggregatorSettingStub{
				"billing.aggregator_flush_interval_seconds": 7,
				"billing.aggregator_max_buffered_rows":      321,
			},
			want: billing.AggregatorOptions{FlushEvery: 7 * time.Second, MaxRows: 321},
		},
		{
			name: "zero explicitly disables automatic flush triggers",
			settings: billingAggregatorSettingStub{
				"billing.aggregator_flush_interval_seconds": 0,
				"billing.aggregator_max_buffered_rows":      0,
			},
			want: billing.AggregatorOptions{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, buildBillingAggregatorOptions(test.settings))
		})
	}
}
