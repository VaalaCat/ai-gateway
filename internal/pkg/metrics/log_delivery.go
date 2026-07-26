package metrics

import (
	"sync/atomic"

	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/prometheus/client_golang/prometheus"
)

type LogDeliveryMetrics struct {
	pending, retry, inflight        prometheus.Gauge
	bytes, oldest, schemaReady      prometheus.Gauge
	dropped                         *prometheus.CounterVec
	writeFailures, snapshotFailures prometheus.Counter
	lastDropped                     atomic.Uint64
}

func NewLogDeliveryMetrics(registerer prometheus.Registerer) *LogDeliveryMetrics {
	if registerer == nil {
		registerer = prometheus.DefaultRegisterer
	}
	m := &LogDeliveryMetrics{
		pending:          prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_pending", Help: "Pending log delivery batches."}),
		retry:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_retry", Help: "Retrying log delivery batches."}),
		inflight:         prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_inflight", Help: "Inflight log delivery batches."}),
		bytes:            prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_pending_bytes", Help: "Bytes retained by the log delivery queue."}),
		oldest:           prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_oldest_age_seconds", Help: "Age of the oldest queued log batch."}),
		schemaReady:      prometheus.NewGauge(prometheus.GaugeOpts{Name: "log_delivery_schema_ready", Help: "Whether the log database schema is ready."}),
		dropped:          prometheus.NewCounterVec(prometheus.CounterOpts{Name: "log_delivery_dropped_total", Help: "Log batches dropped by reason."}, []string{"reason"}),
		writeFailures:    prometheus.NewCounter(prometheus.CounterOpts{Name: "log_delivery_write_failures_total", Help: "Failed log database writes."}),
		snapshotFailures: prometheus.NewCounter(prometheus.CounterOpts{Name: "log_delivery_snapshot_failures_total", Help: "Failed log queue snapshots."}),
	}
	registerer.MustRegister(m.pending, m.retry, m.inflight, m.bytes, m.oldest, m.schemaReady, m.dropped, m.writeFailures, m.snapshotFailures)
	return m
}

func (m *LogDeliveryMetrics) Observe(stats deliveryqueue.Stats, schemaReady bool) {
	if m == nil {
		return
	}
	m.pending.Set(float64(stats.Pending))
	m.retry.Set(float64(stats.Retry))
	m.inflight.Set(float64(stats.Inflight))
	m.bytes.Set(float64(stats.Bytes))
	m.oldest.Set(stats.OldestAge.Seconds())
	m.observeDropped(stats.Dropped)
	if schemaReady {
		m.schemaReady.Set(1)
	} else {
		m.schemaReady.Set(0)
	}
}

func (m *LogDeliveryMetrics) observeDropped(total uint64) {
	for {
		previous := m.lastDropped.Load()
		if total <= previous {
			return
		}
		if m.lastDropped.CompareAndSwap(previous, total) {
			m.dropped.WithLabelValues("capacity").Add(float64(total - previous))
			return
		}
	}
}

func (m *LogDeliveryMetrics) BacklogCleared(total, cleared uint64) {
	if m == nil || cleared == 0 {
		return
	}
	for {
		previous := m.lastDropped.Load()
		if total <= previous {
			return
		}
		delta := total - previous
		manual := min(cleared, delta)
		if m.lastDropped.CompareAndSwap(previous, total) {
			m.dropped.WithLabelValues("manual_clear").Add(float64(manual))
			if capacity := delta - manual; capacity > 0 {
				m.dropped.WithLabelValues("capacity").Add(float64(capacity))
			}
			return
		}
	}
}

func (m *LogDeliveryMetrics) WriteFailure() {
	if m != nil {
		m.writeFailures.Inc()
	}
}
func (m *LogDeliveryMetrics) SnapshotFailure() {
	if m != nil {
		m.snapshotFailures.Inc()
	}
}
