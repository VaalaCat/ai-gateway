package metrics

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestLogDeliveryMetricsExposeQueueAndFailureState(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewLogDeliveryMetrics(registry)
	metrics.Observe(deliveryqueue.Stats{Pending: 2, Retry: 3, Inflight: 1, Bytes: 42, OldestAge: 5 * time.Second, Dropped: 4}, true)
	metrics.WriteFailure()
	metrics.SnapshotFailure()

	families, err := registry.Gather()
	require.NoError(t, err)
	wants := map[string]float64{
		"log_delivery_pending": 2, "log_delivery_retry": 3, "log_delivery_inflight": 1,
		"log_delivery_pending_bytes": 42, "log_delivery_oldest_age_seconds": 5,
		"log_delivery_dropped_total": 4, "log_delivery_write_failures_total": 1,
		"log_delivery_snapshot_failures_total": 1, "log_delivery_schema_ready": 1,
	}
	for _, family := range families {
		if want, ok := wants[family.GetName()]; ok {
			require.Equal(t, want, logDeliveryMetricValue(family), family.GetName())
			delete(wants, family.GetName())
		}
	}
	require.Empty(t, wants)
}

func TestLogDeliveryMetricsClassifiesManualBacklogDrops(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewLogDeliveryMetrics(registry)
	metrics.Observe(deliveryqueue.Stats{Dropped: 2}, false)
	metrics.BacklogCleared(5, 3)
	metrics.Observe(deliveryqueue.Stats{Dropped: 5}, false)

	families, err := registry.Gather()
	require.NoError(t, err)
	var reasons = map[string]float64{}
	for _, family := range families {
		if family.GetName() != "log_delivery_dropped_total" {
			continue
		}
		for _, metric := range family.Metric {
			reasons[metric.Label[0].GetValue()] = metric.Counter.GetValue()
		}
	}
	require.Equal(t, float64(2), reasons["capacity"])
	require.Equal(t, float64(3), reasons["manual_clear"])
}

func logDeliveryMetricValue(family *dto.MetricFamily) float64 {
	metric := family.Metric[0]
	if metric.Gauge != nil {
		return metric.Gauge.GetValue()
	}
	return metric.Counter.GetValue()
}
