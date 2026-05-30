package models

import (
	"testing"

	"gorm.io/datatypes"
)

func TestChannel_ResilienceJSONColumn(t *testing.T) {
	three := 3
	ch := Channel{}
	ch.Resilience = datatypes.NewJSONType(ChannelResilience{MaxRetries: &three})
	got := ch.Resilience.Data()
	if got.MaxRetries == nil || *got.MaxRetries != 3 {
		t.Fatalf("Resilience round-trip failed: %+v", got)
	}
}

func intp(v int) *int { return &v }

func TestChannelResilience_Validate(t *testing.T) {
	cases := []struct {
		name    string
		in      ChannelResilience
		wantErr bool
	}{
		{"all nil = no override", ChannelResilience{}, false},
		{"valid values", ChannelResilience{MaxRetries: intp(3), BackoffBaseMs: intp(200), BreakerThreshold: intp(5), BreakerCooldownMs: intp(30000)}, false},
		{"boundary ok: max_retries=10", ChannelResilience{MaxRetries: intp(10)}, false},
		{"boundary ok: breaker_threshold=1", ChannelResilience{BreakerThreshold: intp(1)}, false},
		{"reject negative max_retries (would be infinite retries)", ChannelResilience{MaxRetries: intp(-1)}, true},
		{"reject max_retries over 10", ChannelResilience{MaxRetries: intp(11)}, true},
		{"reject breaker_threshold=0 (would open permanently)", ChannelResilience{BreakerThreshold: intp(0)}, true},
		{"reject backoff over max", ChannelResilience{BackoffBaseMs: intp(60001)}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.in.Validate()
			if tc.wantErr != (err != nil) {
				t.Fatalf("Validate() err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
