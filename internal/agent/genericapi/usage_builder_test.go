package genericapi

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
)

func TestUsageBuilderProjectsErrorMessageSuccessFailureAndNilBoundary(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	builder := NewUsageBuilder(func() time.Time { return now })
	rc := &RequestContext{
		Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
		Route:   protocol.SyncedAPIRoute{ID: 9, Slug: "forecast"}, Protocol: ProtocolHTTP,
		Subpath: "/today", TokenID: 3, TokenName: "production", UserID: 5, RequestID: "request",
		Agent:        AgentPick{ExecutionAgentID: "execution", AgentRouteID: 4, AgentRoutePath: app.RoutePathDirect},
		UpstreamName: "Primary",
	}
	entry := builder.Build(APIExecution{
		Request: rc, SourceAgentID: "source", StatusCode: http.StatusCreated, DurationMs: 12, QuotaGateDecision: "allow",
		Result: apiattempt.APIExecutionResult{
			APIUpstreamID: 11, UpstreamStatus: http.StatusCreated, ProviderDispatchKnown: true,
			ProviderDispatched: true, RequestBytes: 20, ResponseBytes: 30, FirstByteMs: 4,
			ErrorMessage: "connection refused",
		},
	})
	require.Equal(t, "request", entry.RequestID)
	require.Equal(t, "production", entry.TokenName)
	require.Equal(t, uint(11), entry.APIUpstreamID)
	require.Equal(t, "Primary", entry.UpstreamName)
	require.Equal(t, "allow", entry.QuotaGateDecision)
	require.Equal(t, "execution", entry.ExecutionAgentID)
	require.Equal(t, http.StatusCreated, entry.StatusCode)
	require.Equal(t, now.Unix(), entry.Timestamp)
	require.Equal(t, "connection refused", entry.ErrorMessage)

	failed := builder.Build(APIExecution{Request: rc, SourceAgentID: "source", Err: ErrAPIRateLimited})
	require.Equal(t, http.StatusTooManyRequests, failed.StatusCode)
	require.Equal(t, CodeRateLimited, failed.ErrorCode)
	require.False(t, failed.ProviderDispatchKnown)

	require.NotPanics(t, func() {
		zero := builder.Build(APIExecution{Err: errors.New("unknown")})
		require.Equal(t, CodeUnavailable, zero.ErrorCode)
	})
}

func TestUsageBuilderPreservesEmptyTokenNameBoundary(t *testing.T) {
	entry := NewUsageBuilder(nil).Build(APIExecution{Request: &RequestContext{
		Service: protocol.SyncedAPIService{ID: 1}, Route: protocol.SyncedAPIRoute{ID: 2},
		TokenID: 3, TokenName: "",
	}})

	require.Equal(t, uint(3), entry.TokenID)
	require.Empty(t, entry.TokenName)
}
