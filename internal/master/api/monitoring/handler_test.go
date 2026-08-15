package monitoring

import (
	"testing"
	"time"

	masterlogqueue "github.com/VaalaCat/ai-gateway/internal/master/logqueue"
	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/stretchr/testify/require"
)

func TestLogBacklogFromDeliveryStatus(t *testing.T) {
	status := masterlogqueue.DeliveryStatus{
		Queue: deliveryqueue.Stats{
			Pending: 2, Retry: 3, Inflight: 1, Bytes: 42, Dropped: 4,
			OldestAge: 5 * time.Second,
		},
		SchemaReady: true,
		LastError:   "last write failed",
	}

	got := LogBacklogFrom(status)
	require.Equal(t, LogBacklog{
		Pending: 2, Retry: 3, Inflight: 1, Bytes: 42, Dropped: 4,
		OldestSeconds: 5, SchemaReady: true, LastError: "last write failed",
	}, got)

	require.Equal(t, LogBacklog{}, LogBacklogFrom(masterlogqueue.DeliveryStatus{}), "zero status boundary must remain stable")
}
