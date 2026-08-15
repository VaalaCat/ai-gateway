package apiusage

import "github.com/prometheus/client_golang/prometheus"

type Metrics interface {
	Accepted()
	FullDropped()
	InvalidDropped()
	Duplicate()
	RetryExhausted()
	CapacityShrinkDropped()
	QueueDepth(int)
}

type noopMetrics struct{}

func (noopMetrics) Accepted()              {}
func (noopMetrics) FullDropped()           {}
func (noopMetrics) InvalidDropped()        {}
func (noopMetrics) Duplicate()             {}
func (noopMetrics) RetryExhausted()        {}
func (noopMetrics) CapacityShrinkDropped() {}
func (noopMetrics) QueueDepth(int)         {}

type prometheusMetrics struct {
	accepted, fullDropped, invalidDropped, duplicates, retryExhausted, shrinkDropped prometheus.Counter
	depth                                                                            prometheus.Gauge
}

func NewMetrics(registerer prometheus.Registerer) Metrics {
	if registerer == nil {
		return noopMetrics{}
	}
	newCounter := func(name, help string) prometheus.Counter {
		counter := prometheus.NewCounter(prometheus.CounterOpts{Namespace: "ai_gateway", Subsystem: "api_usage", Name: name, Help: help})
		registerer.MustRegister(counter)
		return counter
	}
	m := &prometheusMetrics{
		accepted:       newCounter("accepted_total", "Generic API usage records accepted for settlement."),
		fullDropped:    newCounter("full_dropped_total", "Generic API usage records dropped because the queue was full."),
		invalidDropped: newCounter("invalid_dropped_total", "Invalid generic API usage records dropped."),
		duplicates:     newCounter("duplicates_total", "Duplicate generic API usage records suppressed."),
		retryExhausted: newCounter("retry_exhausted_total", "Generic API settlements that exhausted bounded retries."),
		shrinkDropped:  newCounter("capacity_shrink_dropped_total", "Generic API usage records dropped by a startup capacity reduction."),
		depth:          prometheus.NewGauge(prometheus.GaugeOpts{Namespace: "ai_gateway", Subsystem: "api_usage", Name: "queue_depth", Help: "Current generic API usage admission queue depth."}),
	}
	registerer.MustRegister(m.depth)
	return m
}
func (m *prometheusMetrics) Accepted()              { m.accepted.Inc() }
func (m *prometheusMetrics) FullDropped()           { m.fullDropped.Inc() }
func (m *prometheusMetrics) InvalidDropped()        { m.invalidDropped.Inc() }
func (m *prometheusMetrics) Duplicate()             { m.duplicates.Inc() }
func (m *prometheusMetrics) RetryExhausted()        { m.retryExhausted.Inc() }
func (m *prometheusMetrics) CapacityShrinkDropped() { m.shrinkDropped.Inc() }
func (m *prometheusMetrics) QueueDepth(depth int)   { m.depth.Set(float64(depth)) }
