package exec

import (
	"context"
	"errors"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type AttemptOutcomeKind = attemptproxy.ResultKind

const (
	AttemptSucceeded            = attemptproxy.ResultSucceeded
	AttemptProviderFailed       = attemptproxy.ResultProviderFailed
	AttemptExecutionRejected    = attemptproxy.ResultExecutionRejected
	AttemptTransportUnavailable = attemptproxy.ResultTransportUnavailable
	AttemptProxyRejected        = attemptproxy.ResultProxyRejected
	AttemptCommitUncertain      = attemptproxy.ResultCommitUncertain
	AttemptCanceled             = attemptproxy.ResultCanceled
)

type AttemptOutcome struct {
	Kind                AttemptOutcomeKind
	Result              state.AttemptResult
	ExecutionAgentID    string
	Path                app.RoutePath
	Commit              tunnel.CommitState
	ProviderResultKnown bool
	ProviderDispatched  bool
	PlanAdvanceAllowed  bool
	ResponseStarted     bool
	ReasonCode          string
	AgentPaths          []models.AgentPathRecord
	Trace               *trace.TraceRecord
	Dispatches          int
	DurationMs          int
}

type AttemptAction string

const (
	ActionTryRelay      AttemptAction = "try_relay"
	ActionTryNextTarget AttemptAction = "try_next_target"
	ActionExecuteLocal  AttemptAction = "execute_local"
	ActionAdvancePlan   AttemptAction = "advance_plan"
	ActionComplete      AttemptAction = "complete"
	ActionStop          AttemptAction = "stop"
)

type AttemptDecisionInput struct {
	Route          AttemptRoute
	CurrentPath    app.RoutePath
	HasNextTarget  bool
	HasLocalTarget bool
	HasNextAttempt bool
	Outcome        AttemptOutcome
}

func nextAttemptAction(input AttemptDecisionInput) AttemptAction {
	if input.Outcome.Kind == AttemptSucceeded && input.Outcome.ProviderResultKnown {
		return ActionComplete
	}
	if mustStopBeforeReplay(input.Outcome) {
		return ActionStop
	}
	if isExplicitPreCommitUnavailable(input.Outcome) {
		return preCommitTransportAction(input)
	}

	switch input.Outcome.Kind {
	case AttemptProviderFailed, AttemptExecutionRejected:
		if input.Outcome.PlanAdvanceAllowed && input.HasNextAttempt {
			return ActionAdvancePlan
		}
	}
	return ActionStop
}

func mustStopBeforeReplay(outcome AttemptOutcome) bool {
	if outcome.Kind == AttemptCanceled ||
		errors.Is(outcome.Result.Err, context.Canceled) ||
		errors.Is(outcome.Result.Err, context.DeadlineExceeded) {
		return true
	}
	if outcome.ResponseStarted || outcome.Result.Written {
		return true
	}
	if outcome.Kind == AttemptCommitUncertain || outcome.Commit == tunnel.CommitUncertain {
		return true
	}
	if !outcome.ProviderResultKnown && !isExplicitPreCommitUnavailable(outcome) {
		return true
	}
	return outcome.Kind == AttemptProxyRejected
}

func isExplicitPreCommitUnavailable(outcome AttemptOutcome) bool {
	return outcome.Kind == AttemptTransportUnavailable && outcome.Commit == tunnel.PreCommit
}

func preCommitTransportAction(input AttemptDecisionInput) AttemptAction {
	switch input.CurrentPath {
	case app.RoutePathDirect:
		return ActionTryRelay
	case app.RoutePathRelay:
		if input.HasNextTarget {
			return ActionTryNextTarget
		}
		if !input.Route.Hard && input.HasLocalTarget {
			return ActionExecuteLocal
		}
	}
	return ActionStop
}
