package publish

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
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

func TestProjectUsageEntryAgentRouteScalars(t *testing.T) {
	tests := []struct {
		name          string
		execution     state.ExecutionResult
		wantExecution string
		wantSource    string
		wantID        uint
		wantPath      string
	}{
		{
			name:       "ordinary local without database route",
			execution:  state.ExecutionResult{RouteSourceAgentID: "source", AgentRoutePath: app.RoutePathLocal},
			wantSource: "source", wantPath: "local",
		},
		{
			name: "provider dispatch proves remote execution agent",
			execution: state.ExecutionResult{
				ProviderDispatched: true, ExecutionAgentID: "target-a", RouteSourceAgentID: "source",
				AgentRouteID: 7, AgentRouteKind: "token", AgentRoutePath: app.RoutePathDirect,
			},
			wantExecution: "target-a", wantSource: "source", wantID: 7, wantPath: "direct",
		},
		{
			name: "dispatch before failure cannot claim execution agent",
			execution: state.ExecutionResult{
				ExecutionAgentID: "unproven-target", RouteSourceAgentID: "source",
				AgentRouteID: 8, AgentRouteKind: "hard", AgentRoutePath: app.RoutePathRelay,
			},
			wantSource: "source", wantID: 8, wantPath: "relay",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx := &state.RelayContext{
				Input: state.RelayInput{RequestID: "req-route", StartTime: time.Now()},
				State: &state.RelayState{FailPhase: state.PhaseCtxBuild, Execution: tt.execution},
			}

			got := ProjectUsageEntry(rctx)

			require.NotNil(t, got.ExecutionAgentID)
			require.Equal(t, tt.wantExecution, *got.ExecutionAgentID)
			require.Equal(t, tt.wantSource, got.RouteSourceAgentID)
			require.Equal(t, tt.wantID, got.AgentRouteID)
			require.Equal(t, tt.wantPath, got.AgentRoutePath)
		})
	}
}
