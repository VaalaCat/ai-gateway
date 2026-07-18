package agentproxy

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

func TestCommitClassifiesOnlyBeforeWriteFailuresAsPreCommit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage string
		code  string
	}{
		{name: "dns", stage: "dns", code: "direct_dns"},
		{name: "tcp", stage: "connect", code: "direct_connect"},
		{name: "tls", stage: "tls", code: "direct_tls"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := classifyRoundTripFailure(&BeforeWriteError{Stage: tc.stage, Code: tc.code, Err: errors.New("failed")})
			require.Equal(t, tunnel.PreCommit, out.Commit)
			require.Equal(t, tc.stage, out.Stage)
			require.Equal(t, tc.code, out.Code)
		})
	}
}

func TestCommitConservativelyClassifiesPostConnectAndCancellation(t *testing.T) {
	for _, err := range []error{
		errors.New("write headers failed"),
		&net.OpError{Op: "read", Net: "tcp", Err: errors.New("timeout")},
		context.Canceled,
	} {
		out := classifyRoundTripFailure(err)
		require.Equal(t, tunnel.CommitUncertain, out.Commit)
		require.Equal(t, "round_trip", out.Stage)
	}
}

func TestCommitTreatsEveryHTTPResponseAsCommitted(t *testing.T) {
	for _, status := range []int{401, 429, 500, 502} {
		require.Equal(t, tunnel.Committed, classifyHTTPResponse(status).Commit)
	}
}
