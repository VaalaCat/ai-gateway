package agentproxy

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type resetCodeError string

func (e resetCodeError) Error() string     { return string(e) }
func (e resetCodeError) ResetCode() string { return string(e) }

func TestRelayFailureCodeUsesStablePublicCodes(t *testing.T) {
	deadlineCtx, deadlineCancel := context.WithCancelCause(context.Background())
	deadlineCancel(context.DeadlineExceeded)
	cancelledCtx, cancelledCancel := context.WithCancelCause(context.Background())
	cancelledCancel(context.Canceled)

	tests := []struct {
		name     string
		ctx      context.Context
		err      error
		fallback string
		want     string
	}{
		{name: "context deadline wins", ctx: deadlineCtx, err: context.Canceled, fallback: CodeRelayNotReady, want: "request_deadline"},
		{name: "error deadline", ctx: context.Background(), err: context.DeadlineExceeded, fallback: CodeRelayNotReady, want: "request_deadline"},
		{name: "context cancelled", ctx: cancelledCtx, err: context.Canceled, fallback: CodeRelayNotReady, want: CodeRequestCancelled},
		{name: "known reset code", ctx: context.Background(), err: resetCodeError("relay_overloaded"), fallback: CodeRelayNotReady, want: "relay_overloaded"},
		{name: "unknown reset code fails closed", ctx: context.Background(), err: resetCodeError("internal_stack"), fallback: CodeRelayNotReady, want: "relay_protocol"},
		{name: "ordinary error uses stable fallback", ctx: context.Background(), err: errors.New("connection refused"), fallback: CodeRelayNotReady, want: CodeRelayNotReady},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, relayFailureCode(test.ctx, test.err, test.fallback))
		})
	}
}

func TestDirectCircuitDeniedOutcomeUsesStablePublicCodes(t *testing.T) {
	tests := []struct {
		name   string
		reason directCircuitDenyReason
		want   string
	}{
		{name: "open", reason: directCircuitDeniedOpen, want: CodeDirectCircuitOpen},
		{name: "half open", reason: directCircuitDeniedHalfOpen, want: CodeDirectCircuitOpen},
		{name: "capacity", reason: directCircuitDeniedCapacity, want: CodeDirectCircuitOpen},
		{name: "closed", reason: directCircuitDeniedClosed, want: CodeDirectDisabled},
		{name: "unknown fails closed", reason: directCircuitDenyReason(255), want: "relay_protocol"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			outcome := directCircuitDeniedOutcome(test.reason)
			require.Equal(t, test.want, outcome.Code)
			require.Error(t, outcome.Err)
		})
	}
}
