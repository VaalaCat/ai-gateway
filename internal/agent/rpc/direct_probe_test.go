package rpc

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type directProbePoolStub struct {
	targets  []agentproxy.DirectSessionTarget
	requests []app.ProbeStreamRequest
	stream   app.ProbeStream
	err      error
}

func (p *directProbePoolStub) OpenProbeStream(
	_ context.Context,
	target agentproxy.DirectSessionTarget,
	request app.ProbeStreamRequest,
) (app.ProbeStream, error) {
	p.targets = append(p.targets, target)
	p.requests = append(p.requests, request)
	return p.stream, p.err
}

type directProbeStreamStub struct {
	status      int
	body        string
	commitState wire.CommitState
	commitErr   error
	uploadErr   error
	responseErr error
	ops         []string
	cancelled   bool
}

func (s *directProbeStreamStub) Commit(context.Context) error {
	s.ops = append(s.ops, "commit")
	return s.commitErr
}

func (s *directProbeStreamStub) Upload(_ context.Context, src io.Reader) error {
	s.ops = append(s.ops, "upload")
	body, _ := io.ReadAll(src)
	if len(body) != 0 {
		return errors.New("probe upload must be empty")
	}
	return s.uploadErr
}

func (s *directProbeStreamStub) CopyResponse(_ context.Context, dst http.ResponseWriter) error {
	s.ops = append(s.ops, "response")
	if s.responseErr != nil {
		return s.responseErr
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	_, err := io.WriteString(dst, s.body)
	return err
}

func (s *directProbeStreamStub) CommitState() wire.CommitState { return s.commitState }
func (s *directProbeStreamStub) Cancel(error) {
	s.cancelled = true
	s.ops = append(s.ops, "cancel")
}
func (s *directProbeStreamStub) Close() error {
	s.ops = append(s.ops, "close")
	return nil
}

type directProbeOpenFailure struct {
	stage  string
	reason string
}

func (e directProbeOpenFailure) Error() string          { return "typed direct open failure" }
func (e directProbeOpenFailure) Stage() string          { return e.stage }
func (e directProbeOpenFailure) ReasonCode() string     { return e.reason }
func (e directProbeOpenFailure) CountsForCircuit() bool { return true }

type directProbeResetError struct{ code string }

func (e directProbeResetError) Error() string     { return "direct reset" }
func (e directProbeResetError) ResetCode() string { return e.code }

func TestDirectProbeUsesP2PSessionAndPassesPolicy(t *testing.T) {
	stream := &directProbeStreamStub{commitState: wire.Committed, body: `{"status":"ok"}`}
	pool := &directProbePoolStub{stream: stream}
	now := time.Unix(100, 0)
	prober := NewDirectProber(DirectProberOptions{
		Pool: pool,
		Now: func() time.Time {
			now = now.Add(7 * time.Millisecond)
			return now
		},
		Timeout: time.Second,
	})

	result := prober.Probe(t.Context(), protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", AddressFingerprint: "probe-fp", TargetGeneration: 12,
		Addresses:      []protocol.Address{{URL: "https://agent-b.example/base", Tag: "wan"}},
		EffectiveProxy: "http://proxy.example:8080",
		Policy:         protocol.ProbeRespectBusinessPolicy,
	})

	require.Equal(t, "reachable", result.Network)
	require.Equal(t, "verified", result.Identity)
	require.True(t, result.Eligible)
	require.Equal(t, protocol.ProbeRespectBusinessPolicy, result.Policy)
	require.False(t, result.PolicyDisabled)
	require.Empty(t, result.PolicyReasonCode)
	require.Equal(t, []string{"commit", "upload", "response", "close"}, stream.ops)
	require.Len(t, pool.targets, 1)
	require.Equal(t, "agent-b", pool.targets[0].TargetAgentID)
	require.Equal(t, "probe-fp", pool.targets[0].AddressFingerprint)
	require.Equal(t, "https://agent-b.example/base", pool.targets[0].WebSocketURL.String())
	require.Equal(t, "http://proxy.example:8080", pool.targets[0].ProxyURL.String())
	require.Equal(t, []app.ProbeStreamRequest{{
		TargetAgentID: "agent-b", RequestID: "direct-connectivity-probe",
		Remaining: pool.requests[0].Remaining, Policy: app.ProbeRespectBusinessPolicy,
	}}, pool.requests)
	require.Positive(t, pool.requests[0].Remaining)
}

func TestDirectProbePassesManualBypassPolicy(t *testing.T) {
	pool := &directProbePoolStub{stream: &directProbeStreamStub{commitState: wire.Committed, body: `{"status":"ok"}`}}
	result := NewDirectProber(DirectProberOptions{Pool: pool}).Probe(t.Context(), directProbeTargetForTest(protocol.ProbeBypassBusinessPolicy))
	require.True(t, result.Eligible)
	require.Equal(t, app.ProbeBypassBusinessPolicy, pool.requests[0].Policy)
	require.Equal(t, protocol.ProbeBypassBusinessPolicy, result.Policy)
}

func TestDirectProbeClassifiesTypedOpenFailure(t *testing.T) {
	pool := &directProbePoolStub{err: directProbeOpenFailure{stage: "credentials", reason: "invalid"}}
	result := NewDirectProber(DirectProberOptions{Pool: pool}).Probe(t.Context(), directProbeTargetForTest(protocol.ProbeRespectBusinessPolicy))
	require.Equal(t, "unreachable", result.Network)
	require.Equal(t, "unknown", result.Identity)
	require.False(t, result.Eligible)
	require.Equal(t, "credentials", result.Stage)
	require.Equal(t, consts.RouteErrorDirectAuthUnavailable, result.ReasonCode)
}

func TestDirectProbeFailsClosedAtPoolBoundary(t *testing.T) {
	tests := []struct {
		name string
		pool agentproxy.DirectProbeStreamOpener
	}{
		{name: "nil pool"},
		{name: "nil stream", pool: &directProbePoolStub{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var result protocol.DirectProbeResult
			require.NotPanics(t, func() {
				result = NewDirectProber(DirectProberOptions{Pool: test.pool}).Probe(
					t.Context(), directProbeTargetForTest(protocol.ProbeRespectBusinessPolicy),
				)
			})
			require.Equal(t, "unreachable", result.Network)
			require.Equal(t, "unknown", result.Identity)
			require.False(t, result.Eligible)
			require.Equal(t, "pool", result.Stage)
			require.Equal(t, consts.RouteErrorDirectDisabled, result.ReasonCode)
		})
	}
}

func TestDirectProbeProjectsRespectTargetPolicyReset(t *testing.T) {
	tests := []struct {
		name           string
		policy         protocol.ProbePolicy
		pool           *directProbePoolStub
		wantDisabled   bool
		wantNetwork    string
		wantIdentity   string
		wantReasonCode string
	}{
		{
			name: "open reset", policy: protocol.ProbeRespectBusinessPolicy,
			pool:         &directProbePoolStub{err: directProbeResetError{code: consts.RouteErrorTargetDirectInboundDisabled}},
			wantDisabled: true, wantNetwork: "reachable", wantIdentity: "verified",
		},
		{
			name: "commit reset", policy: protocol.ProbeRespectBusinessPolicy,
			pool: &directProbePoolStub{stream: &directProbeStreamStub{
				commitState: wire.PreCommit, commitErr: directProbeResetError{code: consts.RouteErrorTargetDirectInboundDisabled},
			}},
			wantDisabled: true, wantNetwork: "reachable", wantIdentity: "verified",
		},
		{
			name: "bypass does not project policy", policy: protocol.ProbeBypassBusinessPolicy,
			pool: &directProbePoolStub{stream: &directProbeStreamStub{
				commitState: wire.PreCommit, commitErr: directProbeResetError{code: consts.RouteErrorTargetDirectInboundDisabled},
			}},
			wantNetwork: "unreachable", wantIdentity: "unknown",
			wantReasonCode: consts.RouteErrorTargetDirectInboundDisabled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := NewDirectProber(DirectProberOptions{Pool: test.pool}).Probe(
				t.Context(), directProbeTargetForTest(test.policy),
			)
			require.Equal(t, test.wantNetwork, result.Network)
			require.Equal(t, test.wantIdentity, result.Identity)
			require.False(t, result.Eligible)
			require.Equal(t, test.wantDisabled, result.PolicyDisabled)
			if test.wantDisabled {
				require.Equal(t, consts.RouteErrorTargetDirectInboundDisabled, result.PolicyReasonCode)
				require.Empty(t, result.ReasonCode)
			} else {
				require.Empty(t, result.PolicyReasonCode)
				require.Equal(t, test.wantReasonCode, result.ReasonCode)
			}
		})
	}
}

func TestDirectProbeMetricUsesPhysicalVerificationResult(t *testing.T) {
	tests := []struct {
		name   string
		result protocol.DirectProbeResult
		want   pkgmetrics.ProbeResult
	}{
		{name: "eligible verified", result: protocol.DirectProbeResult{Network: "reachable", Identity: "verified", Eligible: true}, want: pkgmetrics.ProbeVerified},
		{name: "policy disabled verified", result: protocol.DirectProbeResult{
			Network: "reachable", Identity: "verified", PolicyDisabled: true,
			PolicyReasonCode: consts.RouteErrorTargetDirectInboundDisabled,
		}, want: pkgmetrics.ProbeVerified},
		{name: "cancelled", result: protocol.DirectProbeResult{ReasonCode: consts.RouteErrorRequestCancelled}, want: pkgmetrics.ProbeCancelled},
		{name: "unreachable", result: protocol.DirectProbeResult{Network: "unreachable", ReasonCode: consts.RouteErrorDirectConnect}, want: pkgmetrics.ProbeUnreachable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, directProbeMetricResult(test.result))
		})
	}
}

func TestDirectProbeBoundsAndValidatesPingResponse(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{name: "http status", status: http.StatusBadGateway, body: `{"status":"ok"}`, reason: consts.RouteErrorDirectProbeHTTPStatus},
		{name: "malformed", body: `{`, reason: consts.RouteErrorDirectProbeInvalidResponse},
		{name: "wrong status", body: `{"status":"warming"}`, reason: consts.RouteErrorDirectProbeInvalidResponse},
		{name: "too large", body: strings.Repeat("x", directProbeBodyLimit+1), reason: consts.RouteErrorDirectProbeBodyTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			pool := &directProbePoolStub{stream: &directProbeStreamStub{
				commitState: wire.Committed, status: test.status, body: test.body,
			}}
			result := NewDirectProber(DirectProberOptions{Pool: pool}).Probe(t.Context(), directProbeTargetForTest(protocol.ProbeRespectBusinessPolicy))
			require.Equal(t, "reachable", result.Network)
			require.Equal(t, "verified", result.Identity)
			require.False(t, result.Eligible)
			require.Equal(t, test.reason, result.ReasonCode)
		})
	}
}

func TestDirectProbeRejectsInvalidInputsWithoutOpeningPool(t *testing.T) {
	pool := &directProbePoolStub{}
	prober := NewDirectProber(DirectProberOptions{Pool: pool})
	tests := []struct {
		name   string
		ctx    context.Context
		target protocol.DirectProbeTarget
	}{
		{name: "nil context", target: directProbeTargetForTest(protocol.ProbeRespectBusinessPolicy)},
		{name: "empty target", ctx: t.Context(), target: protocol.DirectProbeTarget{Policy: protocol.ProbeRespectBusinessPolicy}},
		{name: "invalid policy", ctx: t.Context(), target: directProbeTargetForTest(protocol.ProbePolicy("invalid"))},
		{name: "invalid address", ctx: t.Context(), target: protocol.DirectProbeTarget{
			TargetAgentID: "agent-b", AddressFingerprint: "fp", Policy: protocol.ProbeRespectBusinessPolicy,
			Addresses: []protocol.Address{{URL: "file:///tmp/socket"}},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := prober.Probe(test.ctx, test.target)
			require.False(t, result.Eligible)
			require.NotEmpty(t, result.ReasonCode)
		})
	}
	require.Empty(t, pool.requests)
}

func directProbeTargetForTest(policy protocol.ProbePolicy) protocol.DirectProbeTarget {
	return protocol.DirectProbeTarget{
		TargetAgentID: "agent-b", AddressFingerprint: "fp", TargetGeneration: 12,
		Addresses: []protocol.Address{{URL: "http://agent-b.example"}}, Policy: policy,
	}
}
