package logqueue

import "github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"

type DeliveryMetrics interface {
	Observe(deliveryqueue.Stats, bool)
	BacklogCleared(uint64, uint64)
	WriteFailure()
	SnapshotFailure()
}

type noopDeliveryMetrics struct{}

func (noopDeliveryMetrics) Observe(deliveryqueue.Stats, bool) {}
func (noopDeliveryMetrics) BacklogCleared(uint64, uint64)     {}
func (noopDeliveryMetrics) WriteFailure()                     {}
func (noopDeliveryMetrics) SnapshotFailure()                  {}
