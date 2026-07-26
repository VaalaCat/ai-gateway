package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

const (
	directProbeBodyLimit      = 64 << 10
	defaultDirectProbeTimeout = 10 * time.Second
)

var (
	errDirectProbeTimeout      = errors.New("direct probe: overall timeout")
	errDirectProbeBodyTooLarge = errors.New("direct probe: response body too large")
)

type DirectProberOptions struct {
	Pool    agentproxy.DirectProbeStreamOpener
	Now     func() time.Time
	Timeout time.Duration
	Metrics DirectProbeMetricRecorder
}

type DirectProbeMetricRecorder interface {
	IncDirectProbe(pkgmetrics.ProbeResult)
}

type DirectProber struct {
	pool    agentproxy.DirectProbeStreamOpener
	now     func() time.Time
	timeout time.Duration
	metrics DirectProbeMetricRecorder
}

type DirectProbeGate interface {
	BindProbeTarget(protocol.DirectProbeTarget)
	MarkChecking(targetAgentID, addressFingerprint string)
	ApplyProbeResult(protocol.DirectProbeResult)
}

func NewDirectProber(opts DirectProberOptions) *DirectProber {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDirectProbeTimeout
	}
	return &DirectProber{pool: opts.Pool, now: now, timeout: timeout, metrics: opts.Metrics}
}

func (p *DirectProber) Probe(ctx context.Context, target protocol.DirectProbeTarget) (result protocol.DirectProbeResult) {
	if p == nil {
		return directProbeBase(target, time.Now(), "direct_invalid_target")
	}
	defer func() {
		if p.metrics != nil {
			metricResult := directProbeMetricResult(result)
			p.metrics.IncDirectProbe(metricResult)
			if recorder, ok := p.metrics.(interface {
				IncConnectivityProbe(pkgmetrics.PathKind, pkgmetrics.ProbeResult)
			}); ok {
				recorder.IncConnectivityProbe(pkgmetrics.PathDirect, metricResult)
			}
		}
	}()

	startedAt := p.now()
	result = directProbeBase(target, startedAt, "")
	if ctx == nil {
		result.ReasonCode = "invalid_context"
		return result
	}
	if target.TargetAgentID == "" || target.AddressFingerprint == "" || len(target.Addresses) == 0 || !validProbePolicy(target.Policy) {
		result.ReasonCode = "direct_invalid_target"
		return result
	}
	if p.pool == nil {
		result.Stage = "pool"
		result.ReasonCode = consts.RouteErrorDirectDisabled
		return result
	}
	proxyURL, err := directProbeProxyURL(target.EffectiveProxy)
	if err != nil {
		result.Stage = "proxy"
		result.ReasonCode = consts.RouteErrorDirectDisabled
		return result
	}

	probeCtx, cancel := context.WithTimeoutCause(ctx, p.timeout, errDirectProbeTimeout)
	defer cancel()
	best := result
	for _, address := range target.Addresses {
		frozen, err := directProbeSessionTarget(target, address, proxyURL)
		if err != nil {
			best.Stage = "url"
			best.ReasonCode = consts.RouteErrorDirectDisabled
			continue
		}
		candidate := p.probeSession(probeCtx, target, frozen, startedAt)
		if candidate.Eligible {
			return candidate
		}
		best = candidate
		if context.Cause(probeCtx) != nil || candidate.Network == "reachable" {
			return candidate
		}
	}
	return best
}

func (p *DirectProber) probeSession(
	ctx context.Context,
	target protocol.DirectProbeTarget,
	frozen agentproxy.DirectSessionTarget,
	startedAt time.Time,
) protocol.DirectProbeResult {
	result := directProbeBase(target, startedAt, "")
	stream, err := p.pool.OpenProbeStream(ctx, frozen, app.ProbeStreamRequest{
		TargetAgentID: target.TargetAgentID,
		RequestID:     "direct-connectivity-probe",
		Remaining:     probeRemaining(ctx, p.timeout),
		Policy:        target.Policy,
	})
	if err != nil {
		if isTargetPolicyReset(err, target.Policy, consts.RouteErrorTargetDirectInboundDisabled) {
			result.Stage = "open"
			applyDirectPolicyDisabled(&result, consts.RouteErrorTargetDirectInboundDisabled)
			return result
		}
		result.Stage, result.ReasonCode = directProbeOpenFailureCode(ctx, err)
		return result
	}
	if stream == nil {
		result.Stage = "pool"
		result.ReasonCode = consts.RouteErrorDirectDisabled
		return result
	}
	defer stream.Close()

	response := newDirectProbeResponse()
	stage, err := commitUploadAndCopyProbe(ctx, stream, response)
	result.LatencyMS = p.now().Sub(startedAt).Milliseconds()
	result.Stage = string(stage)
	if err != nil {
		stream.Cancel(err)
		if isTargetPolicyReset(err, target.Policy, consts.RouteErrorTargetDirectInboundDisabled) {
			applyDirectPolicyDisabled(&result, consts.RouteErrorTargetDirectInboundDisabled)
			return result
		}
		if response.status != 0 {
			result.Network = "reachable"
			result.Identity = "verified"
		}
		result.ReasonCode = directProbeStreamFailureCode(ctx, stream, stage, err)
		return result
	}

	result.Network = "reachable"
	result.Identity = "verified"
	if response.status != http.StatusOK {
		result.ReasonCode = consts.RouteErrorDirectProbeHTTPStatus
		return result
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.body.Bytes(), &body); err != nil || body.Status != "ok" {
		result.ReasonCode = consts.RouteErrorDirectProbeInvalidResponse
		return result
	}
	result.Eligible = true
	result.Stage = ""
	return result
}

func directProbeBase(target protocol.DirectProbeTarget, checkedAt time.Time, reason string) protocol.DirectProbeResult {
	return protocol.DirectProbeResult{
		TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
		Network: "unreachable", Identity: "unknown", CheckedAt: checkedAt.Unix(),
		ReasonCode: reason, Policy: target.Policy,
	}
}

func directProbeMetricResult(result protocol.DirectProbeResult) pkgmetrics.ProbeResult {
	if result.Network == "reachable" && result.Identity == "verified" && result.ReasonCode == "" {
		return pkgmetrics.ProbeVerified
	}
	if result.ReasonCode == consts.RouteErrorRequestCancelled {
		return pkgmetrics.ProbeCancelled
	}
	if result.Network == "unreachable" && result.ReasonCode != "invalid_context" && result.ReasonCode != "direct_invalid_target" {
		return pkgmetrics.ProbeUnreachable
	}
	return pkgmetrics.ProbeInvalid
}

func applyDirectPolicyDisabled(result *protocol.DirectProbeResult, reason string) {
	result.Network = "reachable"
	result.Identity = "verified"
	result.Eligible = false
	result.ReasonCode = ""
	result.PolicyDisabled = true
	result.PolicyReasonCode = reason
}

func isTargetPolicyReset(err error, policy protocol.ProbePolicy, reason string) bool {
	if policy != protocol.ProbeRespectBusinessPolicy {
		return false
	}
	var reset interface{ ResetCode() string }
	return errors.As(err, &reset) && reset.ResetCode() == reason
}

func validProbePolicy(policy protocol.ProbePolicy) bool {
	return policy == protocol.ProbeRespectBusinessPolicy || policy == protocol.ProbeBypassBusinessPolicy
}

func directProbeSessionTarget(
	target protocol.DirectProbeTarget,
	address protocol.Address,
	proxyURL *url.URL,
) (agentproxy.DirectSessionTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(address.URL))
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return agentproxy.DirectSessionTarget{}, errors.New("invalid direct probe address")
	}
	return agentproxy.DirectSessionTarget{
		TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
		WebSocketURL: parsed, ProxyURL: proxyURL,
	}, nil
}

func directProbeProxyURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed == nil || !parsed.IsAbs() || parsed.Opaque != "" || parsed.Host == "" || parsed.Hostname() == "" {
		return nil, errors.New("invalid direct probe proxy")
	}
	return parsed, nil
}

func directProbeOpenFailureCode(ctx context.Context, err error) (string, string) {
	if reason := probeContextFailureCode(ctx, err); reason != "" {
		return "open", reason
	}
	stage, reason := "open", consts.RouteErrorDirectConnect
	var failure interface {
		Stage() string
		ReasonCode() string
	}
	if !errors.As(err, &failure) {
		return stage, reason
	}
	stage = failure.Stage()
	switch stage {
	case "credentials":
		reason = consts.RouteErrorDirectAuthUnavailable
	case "policy":
		if consts.IsPublicRouteErrorCode(failure.ReasonCode()) {
			reason = failure.ReasonCode()
		}
	case "target", "url", "pool", "proxy":
		reason = consts.RouteErrorDirectDisabled
	}
	return stage, reason
}

func directProbeStreamFailureCode(
	ctx context.Context,
	stream app.ProbeStream,
	stage protocol.RelayProbeStage,
	err error,
) string {
	if errors.Is(err, errDirectProbeBodyTooLarge) {
		return consts.RouteErrorDirectProbeBodyTooLarge
	}
	if reason := probeContextFailureCode(ctx, err); reason != "" {
		return reason
	}
	var reset interface{ ResetCode() string }
	if errors.As(err, &reset) && consts.IsPublicRouteErrorCode(reset.ResetCode()) {
		return reset.ResetCode()
	}
	if stage == protocol.RelayProbeStageCommit {
		if stream.CommitState() == wire.PreCommit {
			return consts.RouteErrorDirectConnect
		}
		return consts.RouteErrorDirectCommitUncertain
	}
	return consts.RouteErrorDirectResponseInterrupted
}

func probeContextFailureCode(ctx context.Context, err error) string {
	cause := context.Cause(ctx)
	if errors.Is(cause, errDirectProbeTimeout) || errors.Is(cause, errRelayProbeTimeout) ||
		errors.Is(cause, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return consts.RouteErrorRequestDeadline
	}
	if cause != nil || errors.Is(err, context.Canceled) {
		return consts.RouteErrorRequestCancelled
	}
	return ""
}

func probeRemaining(ctx context.Context, fallback time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return max(time.Until(deadline), 0)
	}
	return fallback
}

type directProbeResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newDirectProbeResponse() *directProbeResponse {
	return &directProbeResponse{header: make(http.Header)}
}

func (w *directProbeResponse) Header() http.Header { return w.header }

func (w *directProbeResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *directProbeResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	remaining := directProbeBodyLimit - w.body.Len()
	if len(data) > remaining {
		if remaining > 0 {
			_, _ = w.body.Write(data[:remaining])
		}
		return remaining, errDirectProbeBodyTooLarge
	}
	return w.body.Write(data)
}

func commitUploadAndCopyProbe(
	ctx context.Context,
	stream app.ProbeStream,
	dst http.ResponseWriter,
) (protocol.RelayProbeStage, error) {
	if err := stream.Commit(ctx); err != nil {
		return protocol.RelayProbeStageCommit, err
	}
	if err := stream.Upload(ctx, bytes.NewReader(nil)); err != nil {
		return protocol.RelayProbeStageCommit, err
	}
	if err := stream.CopyResponse(ctx, dst); err != nil {
		return protocol.RelayProbeStageResponse, err
	}
	return protocol.RelayProbeStageResponse, nil
}

func HandleDirectProbe(ctx context.Context, params json.RawMessage, prober *DirectProber, gate DirectProbeGate) (any, error) {
	if ctx == nil {
		return nil, errors.New("direct probe: nil context")
	}
	if prober == nil {
		return nil, errors.New("direct probe: prober is required")
	}
	var target protocol.DirectProbeTarget
	if err := json.Unmarshal(params, &target); err != nil {
		return nil, fmt.Errorf("invalid direct probe params: %w", err)
	}
	if gate != nil {
		gate.BindProbeTarget(target)
		gate.MarkChecking(target.TargetAgentID, target.AddressFingerprint)
	}
	result := prober.Probe(ctx, target)
	if gate != nil {
		gate.ApplyProbeResult(result)
	}
	return result, nil
}

var _ http.ResponseWriter = (*directProbeResponse)(nil)
var _ io.Writer = (*directProbeResponse)(nil)
