package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	mbilling "github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type rebuildHandlerDailyRebuilder struct{}

func (rebuildHandlerDailyRebuilder) FindRequestLogDateBounds(context.Context) (dao.RequestLogDateBounds, error) {
	return dao.RequestLogDateBounds{StartDate: "2026-05-01", EndDate: "2026-05-01"}, nil
}

func (rebuildHandlerDailyRebuilder) FindNextRequestLogDate(_ context.Context, after, end string) (string, bool, error) {
	const date = "2026-05-01"
	if after < date && date <= end {
		return date, true, nil
	}
	return "", false, nil
}

func (rebuildHandlerDailyRebuilder) RebuildLogDailyDate(context.Context, string, uint, dao.DailyBillingRebuildTargets) (*dao.BillingRebuildResult, error) {
	return &dao.BillingRebuildResult{ReplayedLogs: 1}, nil
}

func TestRebuildHandler_SubmitAndGet(t *testing.T) {
	r := mbilling.NewRebuildRunner(nil, zap.NewNop(), time.Minute)
	r.Start(context.Background())
	defer r.Stop(context.Background())
	r.SetDailyRebuilder(rebuildHandlerDailyRebuilder{})
	h := &Handler{Runner: r}

	// Submit returns a job ID; daily rebuild creates one slice per date with requests.
	resp, err := h.Rebuild(nil, RebuildRequest{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.JobID)
	require.Equal(t, int64(1), resp.TotalSlices)

	// success: GET 能拿到刚提交的 job
	got, err := h.GetRebuildJob(nil, GetRebuildJobRequest{ID: resp.JobID})
	require.NoError(t, err)
	require.Equal(t, resp.JobID, got.ID)
	require.Contains(t, []string{"running", "succeeded"}, got.Status)
	require.Eventually(t, func() bool {
		job, ok := r.Get(resp.JobID)
		return ok && job.Snapshot().Status == mbilling.JobStatusSucceeded
	}, time.Second, 5*time.Millisecond)
	got, err = h.GetRebuildJob(nil, GetRebuildJobRequest{ID: resp.JobID})
	require.NoError(t, err)
	require.Equal(t, int64(1), got.TotalSlices)

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
	r.Start(context.Background())
	defer r.Stop(context.Background())
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
