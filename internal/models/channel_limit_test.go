package models

import "testing"

func TestChannelLimit_Validate(t *testing.T) {
	cases := []struct {
		name    string
		limit   ChannelLimit
		wantErr bool
	}{
		{
			name:    "success: valid cost monthly rule",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: LimitMetricCost, Window: LimitWindowMonthly, Threshold: 10000000}}},
			wantErr: false,
		},
		{
			name:    "success: rolling_days with days",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: LimitMetricCalls, Window: LimitWindowRollingDays, Days: 7, Threshold: 50000}}},
			wantErr: false,
		},
		{
			name:    "success: empty limit (nothing configured)",
			limit:   ChannelLimit{},
			wantErr: false,
		},
		{
			name:    "failure: bad metric",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: "tokens", Window: LimitWindowDaily, Threshold: 1}}},
			wantErr: true,
		},
		{
			name:    "failure: bad window",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: LimitMetricCost, Window: "hourly", Threshold: 1}}},
			wantErr: true,
		},
		{
			name:    "failure: negative threshold",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: LimitMetricCost, Window: LimitWindowDaily, Threshold: -1}}},
			wantErr: true,
		},
		{
			name:    "boundary: rolling_days missing days",
			limit:   ChannelLimit{Rules: []LimitRule{{Metric: LimitMetricCalls, Window: LimitWindowRollingDays, Days: 0, Threshold: 1}}},
			wantErr: true,
		},
		{
			name:    "boundary: negative disable_at",
			limit:   ChannelLimit{DisableAt: -1},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.limit.Validate()
			if (err != nil) != tc.wantErr {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
