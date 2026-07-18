package tunnel

import (
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"go.uber.org/zap"
	"strings"
)

func candidateFailureCode(err error) string {
	if err == nil {
		return "relay_not_ready"
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "auth") || strings.Contains(message, "ticket") || strings.Contains(message, "unauthorized") {
		return "relay_auth"
	}
	if strings.Contains(message, "protocol") || strings.Contains(message, "limits") {
		return "relay_protocol"
	}
	return "relay_not_ready"
}

func (m *Manager) logRelayFailure(stage, code string, cause error, errorLevel bool) {
	key := diagnostics.SuppressionKey{
		Source: m.opts.SourceID, Target: "master", PathKind: "relay", Stage: stage, ReasonCode: code,
	}
	decision := m.opts.Suppressor.Observe(key, m.opts.Now())
	if decision.Summary != nil {
		m.writeRelayFailureLog("relay failures suppressed", key, nil, errorLevel,
			zap.Uint64("suppressed_count", decision.Summary.SuppressedCount))
		return
	}
	if decision.Allow {
		m.writeRelayFailureLog("relay operation failed", key, cause, errorLevel)
	}
}

func (m *Manager) recoverRelayFailure(stage, code string) {
	key := diagnostics.SuppressionKey{
		Source: m.opts.SourceID, Target: "master", PathKind: "relay", Stage: stage, ReasonCode: code,
	}
	summary := m.opts.Suppressor.Recover(key, m.opts.Now())
	if summary == nil {
		return
	}
	m.opts.Logger.Info("relay operation recovered",
		zap.String("source", key.Source), zap.String("target", key.Target), zap.String("path_kind", key.PathKind),
		zap.String("stage", key.Stage), zap.String("reason_code", key.ReasonCode),
		zap.Uint64("suppressed_count", summary.SuppressedCount))
}

func (m *Manager) writeRelayFailureLog(message string, key diagnostics.SuppressionKey, cause error, errorLevel bool, extra ...zap.Field) {
	fields := []zap.Field{
		zap.String("source", key.Source), zap.String("target", key.Target), zap.String("path_kind", key.PathKind),
		zap.String("stage", key.Stage), zap.String("reason_code", key.ReasonCode),
	}
	if cause != nil {
		fields = append(fields, zap.String("error", diagnostics.SanitizeText(cause.Error())))
	}
	fields = append(fields, extra...)
	if errorLevel {
		m.opts.Logger.Error(message, fields...)
		return
	}
	m.opts.Logger.Warn(message, fields...)
}

func (m *Manager) logRelayState(message, stage string, fields ...zap.Field) {
	base := []zap.Field{
		zap.String("source", m.opts.SourceID), zap.String("target", "master"), zap.String("path_kind", "relay"),
		zap.String("stage", stage),
	}
	m.opts.Logger.Info(message, append(base, fields...)...)
}
