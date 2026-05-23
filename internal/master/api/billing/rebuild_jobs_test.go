package billing

import (
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mbilling "github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestRebuildHandler_SubmitAndGet(t *testing.T) {
	r := mbilling.NewRebuildRunner(nil, zap.NewNop(), time.Minute)
	defer r.Stop()
	r.SetSliceFn(func(date string, hour int, targets []string, reset bool) (*dao.BillingRebuildResult, error) {
		return &dao.BillingRebuildResult{ReplayedLogs: 1}, nil
	})
	h := &Handler{Runner: r}

	// success: Submit 返回 job_id 和 24 个 slice
	resp, err := h.Rebuild(nil, RebuildRequest{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.JobID)
	require.Equal(t, int64(24), resp.TotalSlices)

	// success: GET 能拿到刚提交的 job
	got, err := h.GetRebuildJob(nil, GetRebuildJobRequest{ID: resp.JobID})
	require.NoError(t, err)
	require.Equal(t, resp.JobID, got.ID)
	require.Contains(t, []string{"running", "succeeded"}, got.Status)
	require.Equal(t, int64(24), got.TotalSlices)

	// failure: 未知 ID → 404
	_, err = h.GetRebuildJob(nil, GetRebuildJobRequest{ID: "no-such-job"})
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 404, apiErr.Status)

	// boundary: List 至少包含刚才提交的 job
	list, err := h.ListRebuildJobs(nil, api.EmptyRequest{})
	require.NoError(t, err)
	require.NotEmpty(t, list.Jobs)
	found := false
	for _, j := range list.Jobs {
		if j.ID == resp.JobID {
			found = true
			break
		}
	}
	require.True(t, found, "submitted job missing from List")
}

func TestRebuildHandler_NilRunner(t *testing.T) {
	h := &Handler{Runner: nil}

	// failure: 三个 endpoint 在 Runner 未注入时均返回 InternalError
	_, err := h.Rebuild(nil, RebuildRequest{StartDate: "2026-05-01"})
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 500, apiErr.Status)

	_, err = h.GetRebuildJob(nil, GetRebuildJobRequest{ID: "x"})
	require.Error(t, err)
	apiErr, ok = err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 500, apiErr.Status)

	_, err = h.ListRebuildJobs(nil, api.EmptyRequest{})
	require.Error(t, err)
	apiErr, ok = err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 500, apiErr.Status)
}

func TestRebuildHandler_RejectsBadRange(t *testing.T) {
	r := mbilling.NewRebuildRunner(nil, zap.NewNop(), time.Minute)
	defer r.Stop()
	h := &Handler{Runner: r}

	// failure: 全空 → Runner.Submit 拒绝 → 400
	_, err := h.Rebuild(nil, RebuildRequest{})
	require.Error(t, err)
	apiErr, ok := err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 400, apiErr.Status)

	// failure: start > end → 400
	_, err = h.Rebuild(nil, RebuildRequest{StartDate: "2026-05-02", EndDate: "2026-05-01"})
	require.Error(t, err)
	apiErr, ok = err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 400, apiErr.Status)

	// boundary: 日期不可解析 → 400
	_, err = h.Rebuild(nil, RebuildRequest{StartDate: "bogus", EndDate: "2026-05-01"})
	require.Error(t, err)
	apiErr, ok = err.(*api.APIError)
	require.True(t, ok)
	require.Equal(t, 400, apiErr.Status)
}
