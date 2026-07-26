package logqueue

import (
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/deliveryqueue"
	"github.com/stretchr/testify/require"
)

func TestLogDeliveryQueueHonorsByteLimitAndSnapshotBoundary(t *testing.T) {
	batch := completeTestBatch("byte-limit-a")
	batchBytes := BatchSize(batch)
	queue := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10, MaxBytes: batchBytes}, BatchSize, nil)
	require.True(t, queue.Enqueue(batch).Accepted)
	batch.Request.RequestID = "byte-limit-b"
	batch.Traces[0].RequestID = batch.Request.RequestID
	require.True(t, queue.Enqueue(batch).Accepted)
	require.EqualValues(t, 1, queue.Stats().Dropped)
	require.EqualValues(t, batchBytes, queue.Stats().Bytes)

	path := filepath.Join(t.TempDir(), "boundary.snapshot.gz")
	snapshotter := deliveryqueue.Snapshotter[LogBatch]{Queue: queue, Path: path}
	require.NoError(t, snapshotter.WriteNow())
	restored := deliveryqueue.New(deliveryqueue.Limits{MaxEntries: 10, MaxBytes: batchBytes}, BatchSize, nil)
	restore := deliveryqueue.Snapshotter[LogBatch]{Queue: restored, Path: path}
	require.NoError(t, restore.Restore())
	require.Equal(t, queue.Stats().Pending, restored.Stats().Pending)
	require.Equal(t, queue.Stats().Bytes, restored.Stats().Bytes)
	require.Zero(t, restored.Stats().Dropped, "process-local drop counter is not snapshot state")
	require.Equal(t, "byte-limit-b", restored.Items()[0].Value.Request.RequestID)
}
