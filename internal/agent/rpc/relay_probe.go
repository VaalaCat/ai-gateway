package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

const (
	relayProbeBodyLimit      = 64 << 10
	defaultRelayProbeTimeout = 10 * time.Second
)

var (
	errRelayProbeTimeout      = errors.New("relay probe: overall timeout")
	errRelayProbeBodyTooLarge = errors.New("relay probe: response body too large")
)

type RelayProberOptions struct {
	Link            app.RelayLink
	RelayGeneration func() uint64
	Now             func() time.Time
	Timeout         time.Duration
	Metrics         RelayProbeMetricRecorder
}

type RelayProbeMetricRecorder interface {
	IncConnectivityProbe(pkgmetrics.PathKind, pkgmetrics.ProbeResult)
}

type RelayProber struct {
	link            app.RelayLink
	relayGeneration func() uint64
	now             func() time.Time
	timeout         time.Duration
	metrics         RelayProbeMetricRecorder
}

func NewRelayProber(opts RelayProberOptions) *RelayProber {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultRelayProbeTimeout
	}
	return &RelayProber{
		link: opts.Link, relayGeneration: opts.RelayGeneration, now: now, timeout: timeout, metrics: opts.Metrics,
	}
}

func (p *RelayProber) Probe(ctx context.Context, target protocol.RelayProbeTarget) (result protocol.RelayProbeResult) {
	if p == nil {
		return protocol.RelayProbeResult{
			TargetAgentID: target.TargetAgentID,
			State:         protocol.RelayProbeUnknown,
			CheckedAt:     time.Now().Unix(),
			ReasonCode:    consts.RouteErrorRelayProbeInvalidResult,
		}
	}
	defer func() {
		if p.metrics != nil {
			p.metrics.IncConnectivityProbe(pkgmetrics.PathRelay, relayProbeMetricResult(result))
		}
	}()
	startedAt := p.now()
	result = protocol.RelayProbeResult{
		TargetAgentID: target.TargetAgentID,
		State:         protocol.RelayProbeUnknown,
		CheckedAt:     startedAt.Unix(),
	}
	if ctx == nil || target.TargetAgentID == "" || target.SourceRelayGeneration == 0 || target.TargetRelayGeneration == 0 {
		result.ReasonCode = consts.RouteErrorRelayProbeInvalidResult
		return result
	}
	if !p.generationMatches(target.SourceRelayGeneration) {
		return result
	}
	probeCtx, cancel := context.WithTimeoutCause(ctx, p.timeout, errRelayProbeTimeout)
	defer cancel()
	if p.link == nil {
		result.State = protocol.RelayProbeUnavailable
		result.Stage = protocol.RelayProbeStageOpen
		result.ReasonCode = consts.RouteErrorRelayNotReady
		return result
	}

	stream, err := p.link.OpenStream(probeCtx, app.RelayRequest{
		Purpose:       wire.StreamPurposeConnectivityProbe,
		TargetAgentID: target.TargetAgentID,
		RequestID:     "relay-connectivity-probe",
		Method:        http.MethodGet,
		Path:          "/ping",
		Header:        make(http.Header),
		BodyLength:    0,
		Remaining:     relayProbeRemaining(probeCtx, p.timeout),
	})
	if err != nil {
		return p.failed(result, startedAt, protocol.RelayProbeUnavailable, protocol.RelayProbeStageOpen, probeCtx, err, consts.RouteErrorRelayNotReady)
	}
	defer stream.Close()
	if err := stream.Commit(probeCtx); err != nil {
		stream.Cancel(err)
		fallback := consts.RouteErrorRelayCommitUncertain
		if stream.CommitState() == wire.PreCommit {
			fallback = consts.RouteErrorRelayNotReady
		}
		return p.failed(result, startedAt, protocol.RelayProbeUnavailable, protocol.RelayProbeStageCommit, probeCtx, err, fallback)
	}
	if err := stream.Upload(probeCtx, bytes.NewReader(nil)); err != nil {
		stream.Cancel(err)
		return p.failed(result, startedAt, protocol.RelayProbeUnreachable, protocol.RelayProbeStageCommit, probeCtx, err, consts.RouteErrorRelayCommitUncertain)
	}

	response := newRelayProbeResponse()
	if err := stream.CopyResponse(probeCtx, response); err != nil {
		stream.Cancel(err)
		reason := consts.RouteErrorRelayResponseInterrupted
		if errors.Is(err, errRelayProbeBodyTooLarge) {
			reason = consts.RouteErrorRelayProbeBodyTooLarge
		}
		return p.failed(result, startedAt, protocol.RelayProbeUnreachable, protocol.RelayProbeStageResponse, probeCtx, err, reason)
	}
	result.LatencyMS = p.now().Sub(startedAt).Milliseconds()
	result.Stage = protocol.RelayProbeStageResponse
	if !p.generationMatches(target.SourceRelayGeneration) {
		result.State = protocol.RelayProbeUnknown
		return result
	}
	if response.status != http.StatusOK {
		result.State = protocol.RelayProbeUnreachable
		result.ReasonCode = consts.RouteErrorRelayProbeHTTPStatus
		return result
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(response.body.Bytes(), &body); err != nil || body.Status != "ok" {
		result.State = protocol.RelayProbeUnreachable
		result.ReasonCode = consts.RouteErrorRelayProbeInvalidResponse
		return result
	}
	result.State = protocol.RelayProbeReachable
	return result
}

func relayProbeMetricResult(result protocol.RelayProbeResult) pkgmetrics.ProbeResult {
	if result.ReasonCode == consts.RouteErrorRequestCancelled {
		return pkgmetrics.ProbeCancelled
	}
	if result.ReasonCode == consts.RouteErrorRelayProbeInvalidResult ||
		result.ReasonCode == consts.RouteErrorRelayProbeInvalidResponse {
		return pkgmetrics.ProbeInvalid
	}
	switch result.State {
	case protocol.RelayProbeReachable:
		return pkgmetrics.ProbeReachable
	case protocol.RelayProbeUnreachable:
		return pkgmetrics.ProbeUnreachable
	case protocol.RelayProbeUnavailable:
		return pkgmetrics.ProbeUnavailable
	default:
		return pkgmetrics.ProbeUnknown
	}
}

func (p *RelayProber) generationMatches(expected uint64) bool {
	return p != nil && p.relayGeneration != nil && p.relayGeneration() == expected
}

func (p *RelayProber) failed(
	result protocol.RelayProbeResult,
	startedAt time.Time,
	state protocol.RelayProbeState,
	stage protocol.RelayProbeStage,
	ctx context.Context,
	err error,
	fallback string,
) protocol.RelayProbeResult {
	result.LatencyMS = p.now().Sub(startedAt).Milliseconds()
	result.State = state
	result.Stage = stage
	result.ReasonCode = relayProbeFailureCode(ctx, err, fallback)
	if result.ReasonCode == consts.RouteErrorRequestCancelled {
		result.State = protocol.RelayProbeUnknown
	}
	return result
}

func relayProbeFailureCode(ctx context.Context, err error, fallback string) string {
	cause := context.Cause(ctx)
	if errors.Is(cause, errRelayProbeTimeout) || errors.Is(cause, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return consts.RouteErrorRequestDeadline
	}
	if cause != nil || errors.Is(err, context.Canceled) {
		return consts.RouteErrorRequestCancelled
	}
	var reset interface{ ResetCode() string }
	if errors.As(err, &reset) {
		if code := reset.ResetCode(); consts.IsPublicRouteErrorCode(code) {
			return code
		}
		return consts.RouteErrorRelayProtocol
	}
	if consts.IsConnectivityProbeErrorCode(fallback) {
		return fallback
	}
	return consts.RouteErrorRelayProtocol
}

func relayProbeRemaining(ctx context.Context, fallback time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
		return 0
	}
	return fallback
}

type relayProbeResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newRelayProbeResponse() *relayProbeResponse {
	return &relayProbeResponse{header: make(http.Header)}
}

func (w *relayProbeResponse) Header() http.Header { return w.header }

func (w *relayProbeResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *relayProbeResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	remaining := relayProbeBodyLimit - w.body.Len()
	if len(data) > remaining {
		if remaining > 0 {
			_, _ = w.body.Write(data[:remaining])
		}
		return remaining, errRelayProbeBodyTooLarge
	}
	return w.body.Write(data)
}

func HandleRelayProbe(ctx context.Context, params json.RawMessage, prober *RelayProber) (any, error) {
	if ctx == nil {
		return nil, errors.New("relay probe: nil context")
	}
	if prober == nil {
		return nil, errors.New("relay probe: prober is required")
	}
	var target protocol.RelayProbeTarget
	if err := json.Unmarshal(params, &target); err != nil {
		return nil, fmt.Errorf("invalid relay probe params: %w", err)
	}
	return prober.Probe(ctx, target), nil
}

var _ http.ResponseWriter = (*relayProbeResponse)(nil)
var _ io.Writer = (*relayProbeResponse)(nil)
