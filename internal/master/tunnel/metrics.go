package tunnel

import (
	"strings"

	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type TunnelMetricRecorder interface {
	SetTunnelSession(pkgmetrics.SessionAvailability, pkgmetrics.SessionConvergence, float64)
	SetTunnelStreams(float64)
	AddTunnelBytes(pkgmetrics.Direction, float64)
	IncTunnelReset(pkgmetrics.Stage, bool)
}

type sessionMetricState uint8

const (
	sessionMetricNone sessionMetricState = iota
	sessionMetricActive
	sessionMetricCandidate
	sessionMetricDraining
)

type sessionMetricSnapshot struct {
	version                     uint64
	active, candidate, draining int
}

func (h *Hub) transitionSessionMetricLocked(session *Session, next sessionMetricState) bool {
	if session == nil || session.metricState == next {
		return false
	}
	switch session.metricState {
	case sessionMetricActive:
		if h.metricActive > 0 {
			h.metricActive--
		}
	case sessionMetricCandidate:
		if h.metricCandidate > 0 {
			h.metricCandidate--
		}
	case sessionMetricDraining:
		if h.metricDraining > 0 {
			h.metricDraining--
		}
	}
	switch next {
	case sessionMetricActive:
		h.metricActive++
		session.availability.Store("active")
	case sessionMetricCandidate:
		h.metricCandidate++
		session.availability.Store("candidate")
	case sessionMetricDraining:
		h.metricDraining++
		session.availability.Store("draining")
	}
	session.metricState = next
	h.metricVersion++
	return true
}

func (h *Hub) sessionMetricSnapshotLocked() sessionMetricSnapshot {
	return sessionMetricSnapshot{
		version: h.metricVersion, active: h.metricActive,
		candidate: h.metricCandidate, draining: h.metricDraining,
	}
}

func (h *Hub) syncSessionMetrics(snapshot sessionMetricSnapshot) {
	if h == nil || h.opts.Metrics == nil {
		return
	}
	h.metricsMu.Lock()
	if snapshot.version < h.publishedSessionMetricVersion {
		h.metricsMu.Unlock()
		return
	}

	for _, availability := range []pkgmetrics.SessionAvailability{
		pkgmetrics.SessionAvailable, pkgmetrics.SessionUnavailable, pkgmetrics.SessionDraining,
	} {
		for _, convergence := range []pkgmetrics.SessionConvergence{
			pkgmetrics.ConvergenceConverged, pkgmetrics.ConvergenceConverging,
		} {
			h.opts.Metrics.SetTunnelSession(availability, convergence, 0)
		}
	}
	h.opts.Metrics.SetTunnelSession(pkgmetrics.SessionAvailable, pkgmetrics.ConvergenceConverged, float64(snapshot.active))
	h.opts.Metrics.SetTunnelSession(pkgmetrics.SessionUnavailable, pkgmetrics.ConvergenceConverging, float64(snapshot.candidate))
	h.opts.Metrics.SetTunnelSession(pkgmetrics.SessionDraining, pkgmetrics.ConvergenceConverging, float64(snapshot.draining))
	h.publishedSessionMetricVersion = snapshot.version
	h.metricsMu.Unlock()
}

func (h *Hub) syncStreamMetrics() {
	if h == nil || h.opts.Metrics == nil {
		return
	}
	h.metricsMu.Lock()
	h.mu.RLock()
	streams := h.metricStreams
	h.mu.RUnlock()
	h.opts.Metrics.SetTunnelStreams(float64(streams))
	h.metricsMu.Unlock()
}

func (s *Switch) recordFrameMetrics(from *Session, frame wire.Frame) {
	if s == nil || s.hub == nil || s.hub.opts.Metrics == nil {
		return
	}
	direction := pkgmetrics.DirectionInbound
	if from == s.source {
		direction = pkgmetrics.DirectionOutbound
	}
	s.hub.opts.Metrics.AddTunnelBytes(direction, float64(wire.HeaderSize+len(frame.Payload)))
	if frame.Type != wire.FrameReset {
		return
	}
	var reset wire.Reset
	if err := wire.DecodeMetadata(frame.Payload, &reset, s.limits.MaxMetadataBytes); err != nil {
		s.hub.opts.Metrics.IncTunnelReset(pkgmetrics.StageProtocol, false)
		return
	}
	s.hub.opts.Metrics.IncTunnelReset(tunnelMetricStage(reset.Stage), reset.Committed)
}

func tunnelMetricStage(stage string) pkgmetrics.Stage {
	switch {
	case strings.Contains(stage, "select"), strings.Contains(stage, "target"), strings.Contains(stage, "admission"):
		return pkgmetrics.StageSelect
	case strings.Contains(stage, "dial"), strings.Contains(stage, "open"):
		return pkgmetrics.StageDial
	case strings.Contains(stage, "auth"):
		return pkgmetrics.StageAuth
	case strings.Contains(stage, "response"):
		return pkgmetrics.StageResponse
	case strings.Contains(stage, "commit"), strings.Contains(stage, "peer"):
		return pkgmetrics.StageCommit
	default:
		return pkgmetrics.StageProtocol
	}
}
