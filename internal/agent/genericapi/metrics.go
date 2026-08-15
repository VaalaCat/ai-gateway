package genericapi

import "github.com/prometheus/client_golang/prometheus"

// APIMetrics exposes only bounded protocol, outcome, and transport dimensions.
// Per-service, route, token, and request identifiers belong in request logs,
// never Prometheus labels.
type APIMetrics struct {
	Requests     *prometheus.CounterVec
	Dispatches   *prometheus.CounterVec
	Active       *prometheus.GaugeVec
	UsageDropped prometheus.Counter
	TraceSlimmed prometheus.Counter
}

func NewAPIMetrics(registerer prometheus.Registerer) *APIMetrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	metrics := &APIMetrics{
		Requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "generic_api_requests_total", Help: "Generic API requests by protocol and outcome.",
		}, []string{"protocol", "outcome"}),
		Dispatches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "generic_api_dispatches_total", Help: "Generic API provider dispatches by transport and outcome.",
		}, []string{"transport", "outcome"}),
		Active: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "generic_api_active", Help: "Active Generic API requests by protocol.",
		}, []string{"protocol"}),
		UsageDropped: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "generic_api_usage_dropped_total", Help: "Generic API usage records dropped before settlement.",
		}),
		TraceSlimmed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "generic_api_trace_slimmed_total", Help: "Generic API traces slimmed to stay within a transport budget.",
		}),
	}
	registerer.MustRegister(
		metrics.Requests, metrics.Dispatches, metrics.Active,
		metrics.UsageDropped, metrics.TraceSlimmed,
	)
	return metrics
}

func (metrics *APIMetrics) beginRequest(protocol string) func(string) {
	if metrics == nil {
		return func(string) {}
	}
	protocol = boundedAPIProtocol(protocol)
	metrics.Active.WithLabelValues(protocol).Inc()
	return func(outcome string) {
		metrics.Active.WithLabelValues(protocol).Dec()
		metrics.Requests.WithLabelValues(protocol, boundedAPIOutcome(outcome)).Inc()
	}
}

func (metrics *APIMetrics) observeDispatch(transport, outcome string) {
	if metrics != nil {
		metrics.Dispatches.WithLabelValues(boundedAPITransport(transport), boundedAPIOutcome(outcome)).Inc()
	}
}

func (metrics *APIMetrics) AddUsageDropped(count uint64) {
	if metrics != nil && count > 0 {
		metrics.UsageDropped.Add(float64(count))
	}
}

func (metrics *APIMetrics) AddTraceSlimmed(count uint64) {
	if metrics != nil && count > 0 {
		metrics.TraceSlimmed.Add(float64(count))
	}
}

func boundedAPIProtocol(protocol string) string {
	if protocol == ProtocolHTTP || protocol == ProtocolWebSocket {
		return protocol
	}
	return "unknown"
}

func boundedAPIOutcome(outcome string) string {
	if outcome == "success" {
		return outcome
	}
	return "error"
}

func boundedAPITransport(transport string) string {
	switch transport {
	case "local", "direct", "relay":
		return transport
	default:
		return "unknown"
	}
}
