package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type localProviderStub struct {
	result attemptexec.ProviderResult
	calls  int
}

func (s *localProviderStub) Execute(_ *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
	s.calls++
	return s.result
}

type localNilDispatcher struct{}

func (*localNilDispatcher) Dispatch(*state.RelayContext, state.Attempt) state.AttemptResult {
	panic("unavailable dispatcher must not execute")
}

func TestLocalAttemptExecutorMapsProviderResults(t *testing.T) {
	providerFailure := errors.New("provider failed")
	tests := []struct {
		name        string
		provider    attemptexec.ProviderResult
		wantKind    AttemptOutcomeKind
		wantKnown   bool
		wantStart   bool
		wantAdvance bool
	}{
		{name: "success", provider: attemptexec.ProviderResult{Outcome: state.AttemptResult{PromptTokens: 4}, Dispatches: 1, ProviderDispatched: true}, wantKind: AttemptSucceeded, wantKnown: true},
		{name: "known provider failure", provider: attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: providerFailure}, Dispatches: 1, ProviderDispatched: true}, wantKind: AttemptProviderFailed, wantKnown: true, wantAdvance: true},
		{name: "written failure", provider: attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: providerFailure, Written: true}, Dispatches: 1, ProviderDispatched: true}, wantKind: AttemptProviderFailed, wantKnown: true, wantStart: true},
		{name: "canceled", provider: attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: context.Canceled}}, wantKind: AttemptCanceled, wantKnown: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &localProviderStub{result: tt.provider}
			executor := NewLocalAttemptExecutor("source-a", provider)
			outcome := executor.Execute(&state.RelayContext{}, state.Attempt{RealModel: "model-a"})

			require.Equal(t, tt.wantKind, outcome.Kind)
			require.Equal(t, app.RoutePathLocal, outcome.Path)
			require.Equal(t, "source-a", outcome.ExecutionAgentID)
			require.Equal(t, tt.wantKnown, outcome.ProviderResultKnown)
			require.Equal(t, tt.provider.ProviderDispatched, outcome.ProviderDispatched)
			require.Equal(t, tt.wantStart, outcome.ResponseStarted)
			require.Equal(t, tt.wantAdvance, outcome.PlanAdvanceAllowed)
			require.Equal(t, 1, provider.calls)
			require.Equal(t, []models.AgentPathRecord{{
				AgentID: "source-a", Path: models.AgentPathLocal, Result: models.AgentPathSelected,
				Stage: models.AgentPathDispatch, CommitState: models.AgentPathCommitted,
			}}, outcome.AgentPaths)
		})
	}
}

func TestLocalAttemptExecutorUnavailableProviderFailsWithoutDispatch(t *testing.T) {
	executor := NewLocalAttemptExecutor("source-a", nil)
	outcome := executor.Execute(nil, state.Attempt{})
	require.Equal(t, AttemptExecutionRejected, outcome.Kind)
	require.True(t, outcome.ProviderResultKnown)
	require.False(t, outcome.ProviderDispatched)
	require.Error(t, outcome.Result.Err)
	require.Equal(t, app.RoutePathLocal, outcome.Path)
}

func TestLocalAttemptExecutorRejectsInvalidProviderDependenciesWithoutDispatch(t *testing.T) {
	var typedNilProvider *attemptexec.Executor
	var typedNilDispatcher *localNilDispatcher
	tests := []struct {
		name     string
		provider attemptexec.ProviderAttemptExecutor
	}{
		{name: "typed nil provider", provider: typedNilProvider},
		{name: "nil dispatcher", provider: attemptexec.NewProviderExecutor(nil, nil, nil)},
		{name: "typed nil dispatcher", provider: attemptexec.NewProviderExecutor(typedNilDispatcher, nil, nil)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var outcome AttemptOutcome
			require.NotPanics(t, func() {
				outcome = NewLocalAttemptExecutor("source-a", tt.provider).Execute(nil, state.Attempt{})
			})
			require.Equal(t, AttemptExecutionRejected, outcome.Kind)
			require.Equal(t, "provider_executor_unavailable", outcome.ReasonCode)
			require.Equal(t, app.RoutePathLocal, outcome.Path)
			require.Equal(t, tunnel.PreCommit, outcome.Commit)
			require.True(t, outcome.ProviderResultKnown)
			require.False(t, outcome.ProviderDispatched)
			require.False(t, outcome.ResponseStarted)
			require.Error(t, outcome.Result.Err)
		})
	}
}
