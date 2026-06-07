package publish

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
)

func TestProjectUsageEntry_BaseIdentity(t *testing.T) {
	rctx := &state.RelayContext{
		Input: state.RelayInput{
			RequestID: "req-1", Model: "gpt-4o", IsStream: true,
			StartTime: time.Now().Add(-2 * time.Second),
			UserInfo:  &app.UserInfo{UserID: 7, TokenID: 3, TokenName: "k1"},
		},
		State: &state.RelayState{FailPhase: state.PhaseCtxBuild},
	}
	e := ProjectUsageEntry(rctx)
	if e.RequestID != "req-1" || e.UserID != 7 || e.TokenID != 3 || e.ModelName != "gpt-4o" || !e.IsStream {
		t.Fatalf("base projection wrong: %+v", e)
	}
}

func TestProjectUsageEntry_RateLimit(t *testing.T) {
	rctx := &state.RelayContext{
		Input: state.RelayInput{RequestID: "r", StartTime: time.Now()},
		State: &state.RelayState{
			FailPhase: state.PhaseCtxBuild,
			RateLimit: &state.RateLimitRecord{Decision: "queued", WaitMs: 1200, Reason: "free-tier over"},
		},
	}
	e := ProjectUsageEntry(rctx)
	if e.RateLimitDecision != "queued" || e.RateLimitWaitMs != 1200 || e.RateLimitReason != "free-tier over" {
		t.Fatalf("rate limit not projected: %+v", e)
	}
}
