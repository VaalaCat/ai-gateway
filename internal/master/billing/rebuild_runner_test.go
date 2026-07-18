package billing

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func newRunnerForTest(t *testing.T) *RebuildRunner {
	t.Helper()
	// retain 100ms so gc tests can observe cleanup
	r := NewRebuildRunner(nil, zap.NewNop(), 100*time.Millisecond)
	r.Start(context.Background())
	return r
}

func TestRebuildRunner_SubmitComputesSlices(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())

	// success: 单日窗口 → 24 分片
	job, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.NoError(t, err)
	require.NotEmpty(t, job.ID)
	require.Equal(t, int64(24), job.TotalSlices)

	// 单端给 end 不给 start → 视为 start=end
	job2, err := r.Submit(dao.BillingRebuildFilter{EndDate: "2026-05-02"})
	require.NoError(t, err)
	require.Equal(t, int64(24), job2.TotalSlices)
}

func TestRebuildRunner_SubmitRejectsBadRange(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())

	// failure: start > end
	_, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-02", EndDate: "2026-05-01"})
	require.Error(t, err)

	// failure: 全空
	_, err = r.Submit(dao.BillingRebuildFilter{})
	require.Error(t, err)

	// failure: 日期不可解析
	_, err = r.Submit(dao.BillingRebuildFilter{StartDate: "bogus", EndDate: "2026-05-01"})
	require.Error(t, err)
}

func TestRebuildRunner_GetReturnsKnownJobs(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())

	job, _ := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	got, ok := r.Get(job.ID)
	require.True(t, ok)
	require.Equal(t, job.ID, got.ID)

	// failure: 未知 ID
	_, ok = r.Get("nonexistent-id")
	require.False(t, ok)
}

func TestRebuildRunner_ListReturnsAll(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())

	// boundary: 空列表
	require.Empty(t, r.List())

	// success: 多 job
	_, _ = r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	_, _ = r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-02", EndDate: "2026-05-02"})
	require.Len(t, r.List(), 2)
}

type fakeSliceRunner struct {
	mu    sync.Mutex
	calls []sliceCall
	errOn func(date string, hour int) error
	delay time.Duration
}

type sliceCall struct {
	Date       string
	Hour       int
	ResetDaily bool
}

func (f *fakeSliceRunner) RebuildHourSlice(date string, hour int, targets []string, resetDaily bool) (*dao.BillingRebuildResult, error) {
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	f.calls = append(f.calls, sliceCall{date, hour, resetDaily})
	f.mu.Unlock()
	if f.errOn != nil {
		if err := f.errOn(date, hour); err != nil {
			return nil, err
		}
	}
	return &dao.BillingRebuildResult{ReplayedLogs: 1}, nil
}

func (f *fakeSliceRunner) snapshotCalls() []sliceCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := make([]sliceCall, len(f.calls))
	copy(cp, f.calls)
	return cp
}

func TestRebuildRunner_RunSucceeds(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())
	fake := &fakeSliceRunner{}
	r.SetSliceFn(fake.RebuildHourSlice)

	job, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		got, _ := r.Get(job.ID)
		return got.Snapshot().Status == JobStatusSucceeded
	}, 2*time.Second, 10*time.Millisecond)

	got, _ := r.Get(job.ID)
	snap := got.Snapshot()
	require.Equal(t, int64(24), snap.DoneSlices)
	require.Equal(t, int64(24), snap.ReplayedLogs)
	calls := fake.snapshotCalls()
	require.Len(t, calls, 24)
	require.True(t, calls[0].ResetDaily, "hour=0 应 ResetDaily=true")
	require.False(t, calls[1].ResetDaily)
}

func TestRebuildRunner_RunFailsOnSliceError(t *testing.T) {
	r := newRunnerForTest(t)
	defer r.Stop(context.Background())
	fake := &fakeSliceRunner{
		errOn: func(_ string, hour int) error {
			if hour == 5 {
				return fmt.Errorf("boom")
			}
			return nil
		},
	}
	r.SetSliceFn(fake.RebuildHourSlice)

	job, _ := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.Eventually(t, func() bool {
		got, _ := r.Get(job.ID)
		return got.Snapshot().Status == JobStatusFailed
	}, 2*time.Second, 10*time.Millisecond)

	got, _ := r.Get(job.ID)
	snap := got.Snapshot()
	require.Equal(t, int64(5), snap.DoneSlices, "failure 前完成 0..4 共 5 个分片")
	require.Contains(t, snap.Error, "boom")
}

func TestRebuildRunner_StopMarksRunningAsCanceled(t *testing.T) {
	r := newRunnerForTest(t)
	fake := &fakeSliceRunner{delay: 20 * time.Millisecond}
	r.SetSliceFn(fake.RebuildHourSlice)

	job, _ := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-07"})
	time.Sleep(30 * time.Millisecond) // 让它跑几个分片
	_ = r.Stop(context.Background())

	// 给 cancel 一点点时间传播
	require.Eventually(t, func() bool {
		got, _ := r.Get(job.ID)
		return got.Snapshot().Status == JobStatusCanceled
	}, time.Second, 5*time.Millisecond)
}

func TestRebuildRunnerCloseCancelsBlockedSliceAndRejectsRestart(t *testing.T) {
	r := NewRebuildRunner(nil, zap.NewNop(), time.Hour)
	if got := r.ResourceCounts(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources before Start = %+v", got)
	}
	r.Start(context.Background())
	entered := make(chan struct{})
	canceled := make(chan error, 1)
	r.SetSliceContextFn(func(ctx context.Context, _ string, _ int, _ []string, _ bool) (*dao.BillingRebuildResult, error) {
		close(entered)
		<-ctx.Done()
		cause := context.Cause(ctx)
		canceled <- cause
		return nil, cause
	})
	if _, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"}); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cause := <-canceled; cause == nil {
		t.Fatal("slice did not observe cancellation")
	}
	if got := r.ResourceCounts(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Close = %+v", got)
	}
	if _, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"}); err == nil {
		t.Fatal("Submit after Close succeeded")
	}
	r.Start(context.Background())
	if got := r.ResourceCounts(); got != (app.ResourceCounts{}) {
		t.Fatalf("Start after Close created resources: %+v", got)
	}
}
