package billing

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
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

func TestRebuildRunnerDefaultReplaysCoreBillingFromBillingLogs(t *testing.T) {
	db, appProv := setupTestDB(t)
	require.NoError(t, db.Migrator().DropTable(&models.UsageLog{}))
	require.NoError(t, db.Create(&models.BillingLog{
		RequestID: "rebuild-core", UserID: 1, TokenID: 2, ChannelID: 3,
		OwnerType: "admin", ModelName: "m", PromptTokens: 11, TotalCost: 7,
		Status: 1, CreatedAt: 1777611600,
	}).Error)
	agg := NewAggregator(appProv, zap.NewNop(), AggregatorOptions{})
	agg.SetCoreFlushContextFn(func(ctx context.Context, tokens []dao.TokenDailyRow, channels []dao.ChannelDailyRow, hourly []dao.BillingHourlyRow) error {
		return dao.NewAdminMutation(dao.NewContext(appProv)).Billing().UpsertCoreBillingRows(ctx, tokens, channels, hourly)
	})
	r := NewRebuildRunner(appProv, zap.NewNop(), time.Hour)
	r.SetCoreRebuildAdmission(agg)
	r.Start(t.Context())
	t.Cleanup(func() { _ = r.Stop(context.Background()) })

	job, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-01"})
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		got, ok := r.Get(job.ID)
		return ok && got.Snapshot().Status != JobStatusRunning
	}, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, JobStatusSucceeded, job.Snapshot().Status)
	require.Equal(t, int64(1), job.Snapshot().ReplayedLogs)
	require.Equal(t, int64(1), countRows(t, db, &models.TokenDailyBilling{}))
	require.Equal(t, int64(1), countRows(t, db, &models.ChannelDailyBilling{}))
	require.Equal(t, int64(1), countRows(t, db, &models.BillingHourlyBucket{}))
	var token models.TokenDailyBilling
	var channel models.ChannelDailyBilling
	var hourly models.BillingHourlyBucket
	require.NoError(t, db.First(&token).Error)
	require.NoError(t, db.First(&channel).Error)
	require.NoError(t, db.First(&hourly).Error)
	require.Equal(t, token.PromptTokens, hourly.PromptTokens)
	require.Equal(t, channel.PromptTokens, hourly.PromptTokens)
	require.Equal(t, token.TotalCost, hourly.TotalCost)
	require.Equal(t, channel.TotalCost, hourly.TotalCost)
}

func TestRebuildRunnerDailyTargetDoesNotReplaceHourlyProjection(t *testing.T) {
	db, appProv := setupTestDB(t)
	require.NoError(t, db.Create(&models.BillingLog{
		RequestID: "daily-only", UserID: 1, TokenID: 2, ChannelID: 3,
		OwnerType: "admin", ModelName: "m", PromptTokens: 11, TotalCost: 7,
		Status: 1, CreatedAt: 1777611600,
	}).Error)
	require.NoError(t, db.Create(&models.BillingHourlyBucket{
		Date: "2026-05-01", Hour: 5, UserID: 1, TokenID: 2, ChannelID: 3,
		OwnerType: "admin", ModelName: "m", PromptTokens: 99,
	}).Error)
	agg := NewAggregator(appProv, zap.NewNop(), AggregatorOptions{})
	agg.SetCoreFlushContextFn(func(ctx context.Context, tokens []dao.TokenDailyRow, channels []dao.ChannelDailyRow, hourly []dao.BillingHourlyRow) error {
		return dao.NewAdminMutation(dao.NewContext(appProv)).Billing().UpsertCoreBillingRows(ctx, tokens, channels, hourly)
	})
	r := NewRebuildRunner(appProv, zap.NewNop(), time.Hour)
	r.SetCoreRebuildAdmission(agg)
	r.Start(t.Context())
	t.Cleanup(func() { _ = r.Stop(context.Background()) })

	job, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: "2026-05-01", EndDate: "2026-05-01",
		Targets: []string{dao.RebuildTargetTokenDaily},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return job.Snapshot().Status != JobStatusRunning }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, JobStatusSucceeded, job.Snapshot().Status)
	require.Equal(t, int64(24), job.Snapshot().DoneSlices)
	require.Equal(t, int64(1), job.Snapshot().ReplayedLogs)
	var hourly models.BillingHourlyBucket
	require.NoError(t, db.First(&hourly).Error)
	require.Equal(t, int64(99), hourly.PromptTokens)
	require.Equal(t, int64(1), countRows(t, db, &models.TokenDailyBilling{}))
	require.Equal(t, int64(1), countRows(t, db, &models.ChannelDailyBilling{}))
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

// TestRun_InterSliceSleepCancelable 验证片间 sleep(SetSliceSleep)用
// ctx-cancelable select 实现:sleep 中途 ctx 被取消,job 立即标记 canceled
// 并停止调度后续分片,而不是傻等 sleep 到期或跑满全部分片。
func TestRun_InterSliceSleepCancelable(t *testing.T) {
	r := NewRebuildRunner(nil, zap.NewNop(), time.Hour)
	r.SetSliceSleep(50 * time.Millisecond)

	var mu sync.Mutex
	sliceCalls := 0
	r.SetSliceContextFn(func(ctx context.Context, d string, h int, targets []string, reset bool) (*dao.BillingRebuildResult, error) {
		mu.Lock()
		sliceCalls++
		mu.Unlock()
		return &dao.BillingRebuildResult{}, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	defer r.Stop(context.Background())

	// 2 天窗口 = 48 分片,足够大以便观测"远小于总数"就停下。
	job, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-05-01", EndDate: "2026-05-02"})
	require.NoError(t, err)

	// 第一片(几乎立即完成)之后进入 50ms sleep;30ms 时取消,应在 sleep 期间被打断。
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	require.Eventually(t, func() bool {
		got, _ := r.Get(job.ID)
		return got.Snapshot().Status == JobStatusCanceled
	}, 2*time.Second, 10*time.Millisecond)

	mu.Lock()
	calls := sliceCalls
	mu.Unlock()
	require.Less(t, calls, 48, "sleep 的 select 应被 ctx 打断,不会跑满全部分片")
}

func TestRebuildRunnerSliceSleepDoesNotHoldSubmitAdmission(t *testing.T) {
	_, application := setupTestDB(t)
	agg := NewAggregator(application, zap.NewNop(), AggregatorOptions{})
	agg.SetCoreFlushContextFn(func(ctx context.Context, tokens []dao.TokenDailyRow, channels []dao.ChannelDailyRow, hourly []dao.BillingHourlyRow) error {
		return dao.NewAdminMutation(dao.NewContext(application)).Billing().UpsertCoreBillingRows(ctx, tokens, channels, hourly)
	})
	r := NewRebuildRunner(application, zap.NewNop(), time.Hour)
	r.SetCoreRebuildAdmission(agg)
	r.SetSliceSleep(5 * time.Second)
	ctx, cancel := context.WithCancel(t.Context())
	r.Start(ctx)
	t.Cleanup(func() { _ = r.Stop(context.Background()) })

	job, err := r.Submit(dao.BillingRebuildFilter{StartDate: "2026-07-23", EndDate: "2026-07-23"})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return job.Snapshot().DoneSlices == 1 }, time.Second, 5*time.Millisecond)

	fact := coreFact("during-slice-sleep", time.Date(2026, 7, 23, 1, 10, 0, 0, time.UTC).Unix(), 3)
	done := make(chan struct{})
	go func() {
		agg.SubmitBilling(fact)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("SubmitBilling waited for RebuildRunner slice sleep")
	}
	cancel()
	require.Eventually(t, func() bool { return job.Snapshot().Status == JobStatusCanceled }, time.Second, 5*time.Millisecond)
}
