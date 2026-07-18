package rpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
)

const (
	directProbeBodyLimit      = 64 << 10
	defaultDirectProbeTimeout = 10 * time.Second
)

var errDirectProbeTimeout = errors.New("direct probe: overall timeout")

type DirectProberOptions struct {
	Transport *http.Transport
	Now       func() time.Time
	Timeout   time.Duration
	Metrics   DirectProbeMetricRecorder
}

type DirectProbeMetricRecorder interface {
	IncDirectProbe(pkgmetrics.ProbeResult)
}

type DirectProber struct {
	transport *http.Transport
	now       func() time.Time
	timeout   time.Duration
	metrics   DirectProbeMetricRecorder
}

type DirectProbeGate interface {
	BindProbeTarget(protocol.DirectProbeTarget)
	MarkChecking(targetAgentID, addressFingerprint string)
	ApplyProbeResult(protocol.DirectProbeResult)
}

func NewDirectProber(opts DirectProberOptions) *DirectProber {
	transport := opts.Transport
	if transport == nil {
		transport = http.DefaultTransport.(*http.Transport)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = defaultDirectProbeTimeout
	}
	return &DirectProber{transport: transport.Clone(), now: now, timeout: timeout, metrics: opts.Metrics}
}

func (p *DirectProber) Probe(ctx context.Context, target protocol.DirectProbeTarget) (result protocol.DirectProbeResult) {
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
	base := protocol.DirectProbeResult{
		TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
		Network: "unreachable", Identity: "unknown", CheckedAt: startedAt.Unix(),
	}
	if ctx == nil {
		base.ReasonCode = "invalid_context"
		return base
	}
	probeCtx, cancel := context.WithTimeoutCause(ctx, p.timeout, errDirectProbeTimeout)
	defer cancel()
	if target.TargetAgentID == "" || len(target.Addresses) == 0 {
		base.ReasonCode = "direct_invalid_target"
		return base
	}
	transport, err := p.targetTransport(target.EffectiveProxy)
	if err != nil {
		base.ReasonCode = "direct_proxy_invalid"
		return base
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	best := base
	for _, address := range target.Addresses {
		candidate := p.probeAddress(probeCtx, client, target, address, startedAt)
		if candidate.Eligible {
			return candidate
		}
		if best.Network != "reachable" && candidate.Network == "reachable" {
			best = candidate
		} else if best.ReasonCode == "" {
			best = candidate
		}
		if context.Cause(probeCtx) != nil {
			return candidate
		}
	}
	return best
}

func directProbeMetricResult(result protocol.DirectProbeResult) pkgmetrics.ProbeResult {
	if result.Eligible && result.Identity == "verified" {
		return pkgmetrics.ProbeVerified
	}
	if result.ReasonCode == "cancelled" || result.ReasonCode == "request_cancelled" {
		return pkgmetrics.ProbeCancelled
	}
	if result.Network == "unreachable" && result.ReasonCode != "invalid_context" && result.ReasonCode != "direct_invalid_target" {
		return pkgmetrics.ProbeUnreachable
	}
	return pkgmetrics.ProbeInvalid
}

func (p *DirectProber) probeAddress(
	ctx context.Context,
	client *http.Client,
	target protocol.DirectProbeTarget,
	address protocol.Address,
	startedAt time.Time,
) protocol.DirectProbeResult {
	result := protocol.DirectProbeResult{
		TargetAgentID: target.TargetAgentID, AddressFingerprint: target.AddressFingerprint,
		Network: "unreachable", Identity: "unknown", CheckedAt: startedAt.Unix(),
	}
	endpoint, err := directProbeURL(address.URL, target.TargetAgentID)
	if err != nil {
		result.ReasonCode = "direct_invalid_address"
		return result
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		result.ReasonCode = "direct_invalid_address"
		return result
	}
	request.Header = make(http.Header)
	response, err := client.Do(request)
	result.LatencyMS = p.now().Sub(startedAt).Milliseconds()
	if err != nil {
		result.ReasonCode = directProbeNetworkReason(ctx, err)
		return result
	}
	defer response.Body.Close()
	result.Network = "reachable"
	if response.StatusCode != http.StatusOK {
		result.Identity = "unverified"
		result.ReasonCode = "http_status"
		return result
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, directProbeBodyLimit+1))
	if err != nil {
		if errors.Is(context.Cause(ctx), errDirectProbeTimeout) {
			result.Identity = "invalid"
			result.ReasonCode = "identity_interrupted"
			return result
		}
		if context.Cause(ctx) != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.ReasonCode = "cancelled"
			return result
		}
		result.Identity = "invalid"
		result.ReasonCode = "identity_interrupted"
		return result
	}
	if len(body) > directProbeBodyLimit {
		result.Identity = "invalid"
		result.ReasonCode = "identity_too_large"
		return result
	}
	var identity protocol.DirectIngressIdentity
	if err := json.Unmarshal(body, &identity); err != nil {
		result.Identity = "invalid"
		result.ReasonCode = "identity_malformed"
		return result
	}
	if identity.Contract != protocol.DirectIngressContractV1 {
		result.Identity = "mismatch"
		result.ReasonCode = "identity_contract_mismatch"
		return result
	}
	if identity.Role != "agent" {
		result.Identity = "mismatch"
		result.ReasonCode = "identity_role_mismatch"
		return result
	}
	if identity.AgentID != target.TargetAgentID {
		result.Identity = "mismatch"
		result.ReasonCode = "identity_agent_mismatch"
		return result
	}
	result.Identity = "verified"
	result.Eligible = true
	return result
}

func (p *DirectProber) targetTransport(proxyRaw string) (*http.Transport, error) {
	transport := p.transport.Clone()
	if strings.TrimSpace(proxyRaw) == "" {
		transport.Proxy = nil
		return transport, nil
	}
	proxyURL, err := url.Parse(proxyRaw)
	if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
		return nil, fmt.Errorf("invalid direct probe proxy")
	}
	transport.Proxy = http.ProxyURL(proxyURL)
	return transport, nil
}

func directProbeURL(raw, targetAgentID string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("invalid direct probe address")
	}
	// behavior change: the ingress identity route is listener-relative, matching
	// DirectForwarder, which also treats a configured address path as non-authoritative.
	parsed.Path = protocol.DirectIngressIdentityPath
	parsed.RawPath = ""
	query := url.Values{}
	query.Set("target_agent_id", targetAgentID)
	parsed.RawQuery = query.Encode()
	parsed.Fragment = ""
	return parsed.String(), nil
}

func directProbeNetworkReason(ctx context.Context, err error) string {
	if errors.Is(context.Cause(ctx), errDirectProbeTimeout) {
		return "direct_connect"
	}
	if context.Cause(ctx) != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled"
	}
	var dnsError *net.DNSError
	lower := strings.ToLower(err.Error())
	if errors.As(err, &dnsError) || strings.Contains(lower, "no such host") || strings.Contains(lower, "server misbehaving") {
		return "direct_dns"
	}
	var recordError tls.RecordHeaderError
	var authorityError x509.UnknownAuthorityError
	if errors.As(err, &recordError) || errors.As(err, &authorityError) || strings.Contains(lower, "tls") || strings.Contains(lower, "certificate") {
		return "direct_tls"
	}
	return "direct_connect"
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
