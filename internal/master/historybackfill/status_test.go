package historybackfill

import (
	"encoding/json"
	"math"
	"testing"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStatusHasStableJSONShape(t *testing.T) {
	encoded, err := json.Marshal(Status{
		State: StateCaughtUp, Billing: CursorStatus{LastSourceID: 3},
		Requests: CursorStatus{Skipped: true}, Traces: CursorStatus{ProcessedRows: 4},
	})
	require.NoError(t, err)
	require.JSONEq(t, `{
		"state":"caught_up","source_kind":"","source_path":"","source_size_bytes":0,
		"billing":{"last_source_id":3,"processed_rows":0,"skipped":false},
		"requests":{"last_source_id":0,"processed_rows":0,"skipped":true},
		"traces":{"last_source_id":0,"processed_rows":4,"skipped":false},
		"rows_per_second":0,"last_error":"","last_successful_at_unix":0,
		"can_complete":false,"can_delete_source":false
	}`, string(encoded))
}

func TestStatusDegradesWhenLogCursorDatabaseIsUnavailable(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.worker.backfiller.options.LogDBFinder = func() *gorm.DB { return nil }
	status := f.worker.Status()
	require.Equal(t, StateDegraded, status.State)
	require.Contains(t, status.LastError, "log database is unavailable")
	require.Equal(t, string(masterdatabase.LegacyLayoutMonolith), status.SourceKind)
}

func TestStatusRowsPerSecondIsFinite(t *testing.T) {
	f := newOnlineBackfillFixture(t)
	f.worker.mu.Lock()
	f.worker.rowsPerSecond = math.Inf(1)
	f.worker.mu.Unlock()
	require.Zero(t, f.worker.Status().RowsPerSecond)
}

func TestWorkerBatchRunningReportsProtectedState(t *testing.T) {
	worker := &Worker{}
	require.False(t, worker.BatchRunning())
	worker.setBatchRunning(true)
	require.True(t, worker.BatchRunning())
	worker.setBatchRunning(false)
	require.False(t, worker.BatchRunning())
}
