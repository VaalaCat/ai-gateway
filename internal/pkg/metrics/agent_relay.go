package metrics

import (
	"net/http"
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type PathKind string

const (
	PathDirect PathKind = "direct"
	PathRelay  PathKind = "relay"
	PathLocal  PathKind = "local"
)

type AttemptResult string

const (
	ResultSuccess  AttemptResult = "success"
	ResultFailure  AttemptResult = "failure"
	ResultFallback AttemptResult = "fallback"
)

type Stage string

const (
	StageSelect   Stage = "select"
	StageDial     Stage = "dial"
	StageAuth     Stage = "auth"
	StageProtocol Stage = "protocol"
	StageResponse Stage = "response"
	StageCommit   Stage = "commit"
)

type ReasonClass string

const (
	ReasonAvailability ReasonClass = "availability"
	ReasonCapacity     ReasonClass = "capacity"
	ReasonAuth         ReasonClass = "auth"
	ReasonProtocol     ReasonClass = "protocol"
	ReasonPolicy       ReasonClass = "policy"
)

type SessionAvailability string

const (
	SessionAvailable   SessionAvailability = "available"
	SessionUnavailable SessionAvailability = "unavailable"
	SessionDraining    SessionAvailability = "draining"
)

type SessionConvergence string

const (
	ConvergenceConverged  SessionConvergence = "converged"
	ConvergenceConverging SessionConvergence = "converging"
)

type Direction string

const (
	DirectionInbound  Direction = "inbound"
	DirectionOutbound Direction = "outbound"
)

type ProbeResult string

const (
	ProbeVerified    ProbeResult = "verified"
	ProbeReachable   ProbeResult = "reachable"
	ProbeInvalid     ProbeResult = "invalid"
	ProbeUnreachable ProbeResult = "unreachable"
	ProbeUnavailable ProbeResult = "unavailable"
	ProbeUnknown     ProbeResult = "unknown"
	ProbeCancelled   ProbeResult = "cancelled"
)

type RouteAttempt struct {
	PathKind PathKind
	Result   AttemptResult
	Stage    Stage
}

type RouteFallback struct {
	From        PathKind
	To          PathKind
	ReasonClass ReasonClass
}

type AgentRelayMetrics struct {
	gatherer prometheus.Gatherer

	routeAttempts      *prometheus.CounterVec
	routeFallbacks     *prometheus.CounterVec
	tunnelSessions     *prometheus.GaugeVec
	tunnelStreams      prometheus.Gauge
	tunnelBytes        *prometheus.CounterVec
	tunnelResets       *prometheus.CounterVec
	directProbes       *prometheus.CounterVec
	connectivityProbes *prometheus.CounterVec
	telemetryDropped   prometheus.Counter
}

func NewAgentRelayMetrics(registerer prometheus.Registerer, gatherer prometheus.Gatherer) *AgentRelayMetrics {
	if registerer == nil {
		registry := prometheus.NewRegistry()
		registerer = registry
		gatherer = registry
	}
	if gatherer == nil {
		if candidate, ok := registerer.(prometheus.Gatherer); ok {
			gatherer = candidate
		} else {
			gatherer = prometheus.NewRegistry()
		}
	}
	metrics := &AgentRelayMetrics{
		gatherer: gatherer,
		routeAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_route_attempt_total", Help: "Agent route attempts by bounded route outcome.",
		}, []string{"path_kind", "result", "stage"}),
		routeFallbacks: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_route_fallback_total", Help: "Agent route fallbacks by bounded reason class.",
		}, []string{"from", "to", "reason_class"}),
		tunnelSessions: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "agent_tunnel_sessions", Help: "Agent tunnel sessions by availability and convergence.",
		}, []string{"availability", "convergence"}),
		tunnelStreams: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "agent_tunnel_streams", Help: "Current agent tunnel streams.",
		}),
		tunnelBytes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_tunnel_bytes_total", Help: "Agent tunnel bytes by direction.",
		}, []string{"direction"}),
		tunnelResets: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_tunnel_resets_total", Help: "Agent tunnel resets by stage and commit state.",
		}, []string{"stage", "committed"}),
		directProbes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_direct_probe_total", Help: "Agent direct probes by bounded result.",
		}, []string{"result"}),
		connectivityProbes: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_connectivity_probe_total", Help: "Agent connectivity probes by bounded path and result.",
		}, []string{"path_kind", "result"}),
		telemetryDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "agent_route_telemetry_dropped_total", Help: "Dropped agent route telemetry events.",
		}),
	}
	registerer.MustRegister(
		metrics.routeAttempts, metrics.routeFallbacks, metrics.tunnelSessions, metrics.tunnelStreams,
		metrics.tunnelBytes, metrics.tunnelResets, metrics.directProbes, metrics.connectivityProbes, metrics.telemetryDropped,
	)
	return metrics
}

func (m *AgentRelayMetrics) ObserveRouteAttempt(attempt RouteAttempt) {
	if m != nil {
		m.routeAttempts.WithLabelValues(string(attempt.PathKind), string(attempt.Result), string(attempt.Stage)).Inc()
	}
}

func (m *AgentRelayMetrics) ObserveFallback(fallback RouteFallback) {
	if m != nil {
		m.routeFallbacks.WithLabelValues(string(fallback.From), string(fallback.To), string(fallback.ReasonClass)).Inc()
	}
}

func (m *AgentRelayMetrics) SetTunnelSession(availability SessionAvailability, convergence SessionConvergence, value float64) {
	if m != nil {
		m.tunnelSessions.WithLabelValues(string(availability), string(convergence)).Set(value)
	}
}

func (m *AgentRelayMetrics) SetTunnelStreams(value float64) {
	if m != nil {
		m.tunnelStreams.Set(value)
	}
}

func (m *AgentRelayMetrics) AddTunnelBytes(direction Direction, value float64) {
	if m != nil && value >= 0 {
		m.tunnelBytes.WithLabelValues(string(direction)).Add(value)
	}
}

func (m *AgentRelayMetrics) IncTunnelReset(stage Stage, committed bool) {
	if m != nil {
		m.tunnelResets.WithLabelValues(string(stage), strconv.FormatBool(committed)).Inc()
	}
}

func (m *AgentRelayMetrics) IncDirectProbe(result ProbeResult) {
	if m != nil {
		m.directProbes.WithLabelValues(string(result)).Inc()
	}
}

func (m *AgentRelayMetrics) IncConnectivityProbe(path PathKind, result ProbeResult) {
	if m != nil {
		m.connectivityProbes.WithLabelValues(string(path), string(result)).Inc()
	}
}

func (m *AgentRelayMetrics) IncRouteTelemetryDropped() {
	if m != nil {
		m.telemetryDropped.Inc()
	}
}

func (m *AgentRelayMetrics) Handler() http.Handler {
	if m == nil {
		return http.NotFoundHandler()
	}
	return promhttp.HandlerFor(m.gatherer, promhttp.HandlerOpts{})
}
