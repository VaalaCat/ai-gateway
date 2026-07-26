package tunnel

import (
	"net/url"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"go.uber.org/zap"
)

type directPathDisabledRecorder struct {
	metrics *pkgmetrics.AgentRelayMetrics
	logs    *directLogs
}

func NewDirectPathDisabledRecorder(
	metrics *pkgmetrics.AgentRelayMetrics, logger *zap.Logger, suppressor *diagnostics.Suppressor,
) agentproxy.DirectPathDisabledRecorder {
	return &directPathDisabledRecorder{metrics: metrics, logs: newDirectLogs(logger, suppressor)}
}

func (r *directPathDisabledRecorder) RecordDirectPathDisabled(event agentproxy.DirectPathDisabledEvent) {
	if r == nil {
		return
	}
	reason := pkgmetrics.DirectReasonOther
	switch event.Reason {
	case agentproxy.DirectPathDisabledSourceOutbound:
		reason = pkgmetrics.DirectReasonSourceOutboundDisabled
	case agentproxy.DirectPathDisabledTargetInbound:
		reason = pkgmetrics.DirectReasonTargetInboundDisabled
	default:
		return
	}
	r.metrics.IncPathDisabled(pkgmetrics.PathDirect, reason)
	r.logs.PolicyDisabled(directLogEvent{
		SourceAgentID: event.SourceAgentID, TargetAgentID: event.TargetAgentID,
		Stage: "policy", ReasonCode: string(event.Reason),
		SourceOutbound: event.Reason == agentproxy.DirectPathDisabledSourceOutbound,
		TargetInbound:  event.Reason == agentproxy.DirectPathDisabledTargetInbound,
	})
}

// directLogEvent is the complete bounded field set for Direct lifecycle logs.
// Payloads, headers, tickets, traces, and arbitrary result JSON have no slot.
type directLogEvent struct {
	SourceAgentID          string
	TargetAgentID          string
	Stage                  string
	ReasonCode             string
	SessionGeneration      uint64
	StreamID               string
	RequestID              string
	CommitState            string
	ResponseStarted        bool
	SourceOutbound         bool
	TargetInbound          bool
	ResultKind             string
	ResultBytes            int
	Duration               time.Duration
	DiscardedResponseBytes int64
	ResultReceived         bool
	EndReceived            bool
}

type directLogs struct {
	logger     *zap.Logger
	suppressor *diagnostics.Suppressor
	now        func() time.Time
}

type directLifecycleAction uint8

const (
	directLifecycleReplaced directLifecycleAction = iota
	directLifecycleEvicted
	directLifecycleDraining
	directLifecycleClosed
)

type directLifecycleNotice struct {
	action   directLifecycleAction
	event    directLogEvent
	endpoint string
	cause    error
}

func emitDirectLifecycleNotices(logs *directLogs, notices []directLifecycleNotice) {
	for _, notice := range notices {
		switch notice.action {
		case directLifecycleReplaced:
			logs.Replaced(notice.event)
		case directLifecycleEvicted:
			logs.Evicted(notice.event)
		case directLifecycleDraining:
			logs.Draining(notice.event)
		case directLifecycleClosed:
			logs.Closed(notice.event, notice.endpoint, notice.cause)
		}
	}
}

func newDirectLogs(logger *zap.Logger, suppressor *diagnostics.Suppressor) *directLogs {
	if logger == nil {
		logger = zap.NewNop()
	}
	if suppressor == nil {
		suppressor = diagnostics.NewSuppressor(diagnostics.SuppressorOptions{})
	}
	return &directLogs{logger: logger, suppressor: suppressor, now: time.Now}
}

func (l *directLogs) DialStarted(event directLogEvent, endpoint string) {
	l.info("direct dial started", event, endpoint, nil)
}

func (l *directLogs) Ready(event directLogEvent, endpoint string) {
	for _, reason := range directRecoveryReasons {
		recovery := event
		recovery.ReasonCode = reason
		l.recover("direct dial recovered", recovery)
	}
	l.info("direct session ready", event, endpoint, nil)
}

func (l *directLogs) DialFailed(event directLogEvent, endpoint string, cause error) {
	l.failure("direct dial failed", event, endpoint, cause)
}

func (l *directLogs) IngressAccepted(event directLogEvent, endpoint string) {
	l.info("direct ingress accepted", event, endpoint, nil)
}

func (l *directLogs) IngressRejected(event directLogEvent, endpoint string, cause error) {
	l.failure("direct ingress rejected", event, endpoint, cause)
}

func (l *directLogs) Replaced(event directLogEvent) {
	l.info("direct session replaced", event, "", nil)
}

func (l *directLogs) Evicted(event directLogEvent) {
	l.info("direct session evicted", event, "", nil)
}

func (l *directLogs) Draining(event directLogEvent) {
	l.info("direct session draining", event, "", nil)
}

func (l *directLogs) Closed(event directLogEvent, endpoint string, cause error) {
	l.info("direct session closed", event, endpoint, cause)
}

func (l *directLogs) ResultProtocolFailed(event directLogEvent, endpoint string, cause error) {
	l.failure("direct result protocol failed", event, endpoint, cause)
}

func (l *directLogs) ResultProtocolRecovered(event directLogEvent) {
	for _, reason := range []string{"invalid", "too_large"} {
		recovery := event
		recovery.ReasonCode = reason
		l.recover("direct result protocol recovered", recovery)
	}
}

func (l *directLogs) ResultDrainFinished(event directLogEvent) {
	if l == nil {
		return
	}
	fields := l.fields(event, "", nil)
	fields = append(fields,
		zap.String("route_path", "direct"),
		zap.String("session_direction", "direct_outgoing"),
		zap.Int64("drain_duration_ms", event.Duration.Milliseconds()),
		zap.Int64("discarded_response_bytes", event.DiscardedResponseBytes),
		zap.Bool("result_received", event.ResultReceived),
		zap.Bool("end_received", event.EndReceived),
	)
	l.logger.Info("direct attempt result drain finished", fields...)
}

var directRecoveryReasons = []string{
	"protocol", "policy", "capacity", "auth", "unavailable", "canceled", "timeout", "closed", "other",
}

func (l *directLogs) PolicyDisabled(event directLogEvent) {
	l.info("direct path disabled", event, "", nil)
}

func (l *directLogs) failure(message string, event directLogEvent, endpoint string, cause error) {
	if l == nil {
		return
	}
	decision := l.suppressor.Observe(event.suppressionKey(), l.now())
	if decision.Summary != nil {
		fields := l.fields(event, endpoint, nil)
		fields = append(fields,
			zap.String("summary_kind", decision.Summary.Kind),
			zap.Uint64("suppressed_count", decision.Summary.SuppressedCount),
		)
		l.logger.Warn("direct failures suppressed", fields...)
		return
	}
	if decision.Allow {
		l.logger.Warn(message, l.fields(event, endpoint, cause)...)
	}
}

func (l *directLogs) recover(message string, event directLogEvent) {
	if l == nil {
		return
	}
	summary := l.suppressor.Recover(event.suppressionKey(), l.now())
	if summary == nil {
		return
	}
	fields := l.fields(event, "", nil)
	fields = append(fields, zap.String("summary_kind", summary.Kind), zap.Uint64("suppressed_count", summary.SuppressedCount))
	l.logger.Info(message, fields...)
}

func (l *directLogs) info(message string, event directLogEvent, endpoint string, cause error) {
	if l != nil {
		l.logger.Info(message, l.fields(event, endpoint, cause)...)
	}
}

func (l *directLogs) fields(event directLogEvent, endpoint string, cause error) []zap.Field {
	event = normalizeDirectLogEvent(event)
	fields := []zap.Field{
		zap.String("source_agent_id", event.SourceAgentID),
		zap.String("target_agent_id", event.TargetAgentID),
		zap.String("path_kind", "direct"),
		zap.String("stage", event.Stage),
		zap.String("reason_code", event.ReasonCode),
		zap.Uint64("session_generation", event.SessionGeneration),
		zap.String("stream_id", event.StreamID),
		zap.String("request_id", event.RequestID),
		zap.String("commit_state", event.CommitState),
		zap.Bool("response_started", event.ResponseStarted),
		zap.Bool("source_outbound", event.SourceOutbound),
		zap.Bool("target_inbound", event.TargetInbound),
		zap.String("result_kind", event.ResultKind),
		zap.Int("result_bytes", event.ResultBytes),
		zap.Duration("duration", event.Duration),
	}
	if scheme, host, ok := directLogEndpoint(endpoint); ok {
		fields = append(fields,
			zap.String("endpoint_scheme", scheme),
			zap.String("endpoint_host", host),
			zap.String("endpoint_path", DirectTunnelPath),
		)
	}
	if cause != nil {
		fields = append(fields, zap.String("error", diagnostics.SanitizeText(cause.Error())))
	}
	return fields
}

func (event directLogEvent) suppressionKey() diagnostics.SuppressionKey {
	event = normalizeDirectLogEvent(event)
	return diagnostics.SuppressionKey{
		Source: event.SourceAgentID, Target: event.TargetAgentID, PathKind: "direct",
		Stage: event.Stage, ReasonCode: event.ReasonCode,
	}
}

func normalizeDirectLogEvent(event directLogEvent) directLogEvent {
	switch event.Stage {
	case "dial", "handshake", "ingress", "pool", "session", "result", "policy", "replace", "evict", "drain", "close":
	default:
		event.Stage = "other"
	}
	event.ReasonCode = normalizeDirectLogReason(event.ReasonCode)
	return event
}

func normalizeDirectLogReason(reason string) string {
	switch reason {
	case "none", "protocol", "policy", "capacity", "auth", "unavailable", "canceled", "timeout", "closed", "invalid", "too_large", "other":
		return reason
	case directReasonCapacity:
		return "capacity"
	case "direct_ticket_invalid":
		return "auth"
	case "direct_ingress_draining", "direct_closed":
		return "closed"
	case "direct_source_unavailable", "direct_target_unavailable", "failed":
		return "unavailable"
	case "source_direct_outbound_disabled", "target_direct_inbound_disabled":
		return reason
	case "direct_target_mismatch", "policy_disabled":
		return "policy"
	default:
		return "other"
	}
}

func directLogEndpoint(raw string) (string, string, bool) {
	redacted := diagnostics.RedactURI(raw)
	parsed, err := url.Parse(redacted)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", "", false
	}
	return parsed.Scheme, parsed.Host, true
}
