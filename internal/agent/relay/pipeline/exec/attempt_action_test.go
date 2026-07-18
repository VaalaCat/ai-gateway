package exec

import (
	"context"
	"errors"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestNextAttemptActionTransportPreCommit(t *testing.T) {
	tests := []struct {
		name string
		in   AttemptDecisionInput
		want AttemptAction
	}{
		{
			name: "soft direct tries relay",
			in:   preCommitUnavailableInput(app.RoutePathDirect, false),
			want: ActionTryRelay,
		},
		{
			name: "hard direct still tries relay for same target",
			in:   preCommitUnavailableInput(app.RoutePathDirect, true),
			want: ActionTryRelay,
		},
		{
			name: "soft relay tries next remote target first",
			in: withNextTarget(
				preCommitUnavailableInput(app.RoutePathRelay, false),
			),
			want: ActionTryNextTarget,
		},
		{
			name: "hard relay can try next remote target before selector freezes",
			in: withNextTarget(
				preCommitUnavailableInput(app.RoutePathRelay, true),
			),
			want: ActionTryNextTarget,
		},
		{
			name: "soft relay exhausts remote targets then executes local",
			in: withLocalTarget(
				preCommitUnavailableInput(app.RoutePathRelay, false),
			),
			want: ActionExecuteLocal,
		},
		{
			name: "hard relay never executes local",
			in: withLocalTarget(
				preCommitUnavailableInput(app.RoutePathRelay, true),
			),
			want: ActionStop,
		},
		{
			name: "soft relay without local target stops",
			in:   preCommitUnavailableInput(app.RoutePathRelay, false),
			want: ActionStop,
		},
		{
			name: "local transport failure stops",
			in:   preCommitUnavailableInput(app.RoutePathLocal, false),
			want: ActionStop,
		},
		{
			name: "unknown transport path stops",
			in:   preCommitUnavailableInput(app.RoutePath("unknown"), false),
			want: ActionStop,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, nextAttemptAction(tt.in))
		})
	}
}

func TestNextAttemptActionProviderResult(t *testing.T) {
	tests := []struct {
		name string
		in   AttemptDecisionInput
		want AttemptAction
	}{
		{
			name: "known success completes",
			in:   knownProviderInput(AttemptSucceeded, false, false),
			want: ActionComplete,
		},
		{
			name: "known written success completes",
			in: withWrittenDecision(knownProviderInput(
				AttemptSucceeded, true, false,
			)),
			want: ActionComplete,
		},
		{
			name: "provider 429 advances plan",
			in: withNextAttempt(knownProviderInput(
				AttemptProviderFailed, true, true,
			), "provider_429"),
			want: ActionAdvancePlan,
		},
		{
			name: "provider 500 advances plan",
			in: withNextAttempt(knownProviderInput(
				AttemptProviderFailed, true, true,
			), "provider_500"),
			want: ActionAdvancePlan,
		},
		{
			name: "attempt limiter rejection advances plan",
			in: withNextAttempt(knownProviderInput(
				AttemptExecutionRejected, false, true,
			), "attempt_limiter_rejected"),
			want: ActionAdvancePlan,
		},
		{
			name: "attempt breaker rejection advances plan",
			in: withNextAttempt(knownProviderInput(
				AttemptExecutionRejected, false, true,
			), "attempt_breaker_open"),
			want: ActionAdvancePlan,
		},
		{
			name: "terminal provider failure stops",
			in: withNextAttempt(knownProviderInput(
				AttemptProviderFailed, true, false,
			), "provider_terminal"),
			want: ActionStop,
		},
		{
			name: "exhausted plan stops",
			in:   knownProviderInput(AttemptProviderFailed, true, true),
			want: ActionStop,
		},
		{
			name: "committed retryable provider result still advances",
			in: withCommit(
				withNextAttempt(knownProviderInput(AttemptProviderFailed, true, true), "provider_500"),
				tunnel.Committed,
			),
			want: ActionAdvancePlan,
		},
		{
			name: "committed known success completes",
			in: withCommit(
				knownProviderInput(AttemptSucceeded, true, false),
				tunnel.Committed,
			),
			want: ActionComplete,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, nextAttemptAction(tt.in))
		})
	}
}

func TestNextAttemptActionNoReplayGuards(t *testing.T) {
	retryable := withNextAttempt(
		knownProviderInput(AttemptProviderFailed, true, true),
		"provider_500",
	)
	preCommit := withNextTarget(preCommitUnavailableInput(app.RoutePathRelay, false))

	tests := []struct {
		name    string
		outcome AttemptOutcome
	}{
		{name: "canceled kind", outcome: AttemptOutcome{Kind: AttemptCanceled}},
		{name: "wrapped context canceled", outcome: withResultError(retryable.Outcome, errors.Join(errors.New("request stopped"), context.Canceled))},
		{name: "deadline exceeded", outcome: withResultError(retryable.Outcome, context.DeadlineExceeded)},
		{name: "response started", outcome: withResponseStarted(retryable.Outcome)},
		{name: "result written", outcome: withWrittenResult(retryable.Outcome)},
		{name: "uncertain outcome kind", outcome: AttemptOutcome{Kind: AttemptCommitUncertain, ProviderResultKnown: true}},
		{name: "uncertain commit state", outcome: withOutcomeCommit(retryable.Outcome, tunnel.CommitUncertain)},
		{name: "provider result unknown", outcome: AttemptOutcome{Kind: AttemptProviderFailed}},
		{name: "success missing result", outcome: AttemptOutcome{Kind: AttemptSucceeded}},
		{name: "committed result missing", outcome: AttemptOutcome{Kind: AttemptProviderFailed, Commit: tunnel.Committed}},
		{name: "damaged trailer leaves result unknown", outcome: AttemptOutcome{Kind: AttemptProviderFailed, Commit: tunnel.Committed, ReasonCode: "invalid_result_trailer"}},
		{name: "proxy rejection", outcome: AttemptOutcome{Kind: AttemptProxyRejected, ProviderResultKnown: true, PlanAdvanceAllowed: true}},
		{name: "unknown result kind", outcome: AttemptOutcome{Kind: AttemptOutcomeKind("unknown"), ProviderResultKnown: true, PlanAdvanceAllowed: true}},
		{name: "precommit unavailable but canceled", outcome: withResultError(preCommit.Outcome, context.Canceled)},
		{name: "precommit unavailable but deadline exceeded", outcome: withResultError(preCommit.Outcome, context.DeadlineExceeded)},
		{name: "precommit unavailable but response started", outcome: withResponseStarted(preCommit.Outcome)},
		{name: "precommit unavailable but written", outcome: withWrittenResult(preCommit.Outcome)},
		{name: "precommit unavailable but uncertain kind", outcome: withOutcomeKind(preCommit.Outcome, AttemptCommitUncertain)},
		{name: "precommit unavailable but uncertain commit", outcome: withOutcomeCommit(preCommit.Outcome, tunnel.CommitUncertain)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := AttemptDecisionInput{
				Route:          AttemptRoute{},
				CurrentPath:    app.RoutePathRelay,
				HasNextTarget:  true,
				HasLocalTarget: true,
				HasNextAttempt: true,
				Outcome:        tt.outcome,
			}
			require.Equal(t, ActionStop, nextAttemptAction(in))
		})
	}
}

func TestNextAttemptActionNeverReplaysUncertainOrStartedResponse(t *testing.T) {
	for _, outcome := range []AttemptOutcome{
		{Kind: AttemptCommitUncertain, Commit: tunnel.CommitUncertain},
		{Kind: AttemptProviderFailed, ProviderResultKnown: false},
		{Kind: AttemptProviderFailed, ProviderResultKnown: true, ResponseStarted: true},
		{Kind: AttemptProviderFailed, ProviderResultKnown: true, Result: state.AttemptResult{Written: true}},
		{Kind: AttemptCanceled, ProviderResultKnown: false},
	} {
		action := nextAttemptAction(AttemptDecisionInput{HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome})
		require.Equal(t, ActionStop, action)
	}
}

func preCommitUnavailableInput(path app.RoutePath, hard bool) AttemptDecisionInput {
	return AttemptDecisionInput{
		Route:       AttemptRoute{Hard: hard},
		CurrentPath: path,
		Outcome: AttemptOutcome{
			Kind:   AttemptTransportUnavailable,
			Path:   path,
			Commit: tunnel.PreCommit,
		},
	}
}

func knownProviderInput(kind AttemptOutcomeKind, dispatched, advance bool) AttemptDecisionInput {
	return AttemptDecisionInput{
		Outcome: AttemptOutcome{
			Kind:                kind,
			Commit:              tunnel.PreCommit,
			ProviderResultKnown: true,
			ProviderDispatched:  dispatched,
			PlanAdvanceAllowed:  advance,
		},
	}
}

func withNextTarget(in AttemptDecisionInput) AttemptDecisionInput {
	in.HasNextTarget = true
	return in
}

func withLocalTarget(in AttemptDecisionInput) AttemptDecisionInput {
	in.HasLocalTarget = true
	return in
}

func withNextAttempt(in AttemptDecisionInput, reasonCode string) AttemptDecisionInput {
	in.HasNextAttempt = true
	in.Outcome.ReasonCode = reasonCode
	return in
}

func withCommit(in AttemptDecisionInput, commit tunnel.CommitState) AttemptDecisionInput {
	in.Outcome.Commit = commit
	return in
}

func withResultError(outcome AttemptOutcome, err error) AttemptOutcome {
	outcome.Result.Err = err
	return outcome
}

func withResponseStarted(outcome AttemptOutcome) AttemptOutcome {
	outcome.ResponseStarted = true
	return outcome
}

func withWrittenResult(outcome AttemptOutcome) AttemptOutcome {
	outcome.Result.Written = true
	return outcome
}

func withWrittenDecision(input AttemptDecisionInput) AttemptDecisionInput {
	input.Outcome.Result.Written = true
	return input
}

func withOutcomeKind(outcome AttemptOutcome, kind AttemptOutcomeKind) AttemptOutcome {
	outcome.Kind = kind
	return outcome
}

func withOutcomeCommit(outcome AttemptOutcome, commit tunnel.CommitState) AttemptOutcome {
	outcome.Commit = commit
	return outcome
}
