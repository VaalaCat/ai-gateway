package exec

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type LocalAttemptExecutor interface {
	Execute(*state.RelayContext, state.Attempt) AttemptOutcome
}

type localAttemptExecutor struct {
	sourceAgentID string
	provider      attemptexec.ProviderAttemptExecutor
}

func NewLocalAttemptExecutor(sourceAgentID string, provider attemptexec.ProviderAttemptExecutor) LocalAttemptExecutor {
	return &localAttemptExecutor{sourceAgentID: sourceAgentID, provider: provider}
}

func (e *localAttemptExecutor) Execute(rctx *state.RelayContext, attempt state.Attempt) AttemptOutcome {
	if e == nil || !attemptexec.ProviderExecutorAvailable(e.provider) {
		return localProviderUnavailable(e)
	}
	provider := e.provider.Execute(rctx, attempt)
	outcome := AttemptOutcome{
		Kind:                localOutcomeKind(provider),
		Result:              provider.Outcome,
		ExecutionAgentID:    e.sourceAgentID,
		Path:                app.RoutePathLocal,
		Commit:              tunnel.Committed,
		ProviderResultKnown: true,
		ProviderDispatched:  provider.ProviderDispatched || provider.Dispatches > 0,
		ResponseStarted:     provider.Outcome.Written,
		Dispatches:          provider.Dispatches,
	}
	outcome.PlanAdvanceAllowed = provider.Outcome.Err != nil && outcome.Kind != AttemptCanceled &&
		!outcome.ResponseStarted && !resilience.Classify(provider.Outcome).AbortAll
	outcome.AgentPaths = []models.AgentPathRecord{localPathRecord(e.sourceAgentID)}
	return outcome
}

func localOutcomeKind(provider attemptexec.ProviderResult) AttemptOutcomeKind {
	if provider.Outcome.Err == nil {
		return AttemptSucceeded
	}
	if errors.Is(provider.Outcome.Err, context.Canceled) || errors.Is(provider.Outcome.Err, context.DeadlineExceeded) {
		return AttemptCanceled
	}
	if provider.ProviderDispatched || provider.Dispatches > 0 {
		return AttemptProviderFailed
	}
	return AttemptExecutionRejected
}

func localProviderUnavailable(executor *localAttemptExecutor) AttemptOutcome {
	sourceAgentID := ""
	if executor != nil {
		sourceAgentID = executor.sourceAgentID
	}
	return AttemptOutcome{
		Kind: AttemptExecutionRejected, Result: state.AttemptResult{Err: errors.New("local provider executor unavailable")},
		ExecutionAgentID: sourceAgentID, Path: app.RoutePathLocal, Commit: tunnel.PreCommit,
		ProviderResultKnown: true, ReasonCode: "provider_executor_unavailable",
		AgentPaths: []models.AgentPathRecord{{
			AgentID: sourceAgentID, Path: models.AgentPathLocal, Result: models.AgentPathRejected,
			Stage: models.AgentPathDispatch, CommitState: models.AgentPathNotCommitted,
			ReasonCode: "provider_executor_unavailable",
		}},
	}
}

func localPathRecord(agentID string) models.AgentPathRecord {
	return models.AgentPathRecord{
		AgentID: agentID, Path: models.AgentPathLocal, Result: models.AgentPathSelected,
		Stage: models.AgentPathDispatch, CommitState: models.AgentPathCommitted,
	}
}
