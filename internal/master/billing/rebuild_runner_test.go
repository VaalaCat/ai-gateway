package billing

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
)

func TestRebuildRunnerRejectsNonDailyTargets(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	r := startBackfillRunner(t, application)

	_, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateA,
		EndDate:   backfillDateA,
		Targets:   []string{"hourly_bucket"},
	})
	require.ErrorIs(t, err, dao.ErrInvalidRebuildTarget)
}

func TestRebuildRunnerRejectsBadDailyRange(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	r := startBackfillRunner(t, application)
	targets := []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily}

	_, err := r.Submit(dao.BillingRebuildFilter{StartDate: backfillDateB, EndDate: backfillDateA, Targets: targets})
	require.Error(t, err)
	_, err = r.Submit(dao.BillingRebuildFilter{Targets: targets})
	require.Error(t, err)
}

func TestRebuildRunnerRejectsSecondConcurrentDailyJob(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	r := startBackfillRunner(t, application)
	r.SetDailyRebuilder(&fakeLogDailyRebuilder{
		bounds:         dao.RequestLogDateBounds{StartDate: backfillDateA, EndDate: backfillDateA},
		rebuildEntered: entered, rebuildRelease: release,
	})

	first, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateA, EndDate: backfillDateA,
		Targets: []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily},
	})
	require.NoError(t, err)
	<-entered
	_, err = r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateB, EndDate: backfillDateB,
		Targets: []string{dao.RebuildTargetTokenDaily},
	})
	require.ErrorIs(t, err, ErrDailyBillingRebuildRunning)
	close(release)
	require.Eventually(t, func() bool { return first.Snapshot().Status == JobStatusSucceeded }, time.Second, 5*time.Millisecond)
}

func TestRebuildRunnerDailyProgressCountsOnlyDatesWithRequests(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	r := startBackfillRunner(t, application)
	r.SetDailyRebuilder(&fakeLogDailyRebuilder{})

	job, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateA, EndDate: backfillDateB,
		Targets: []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return job.Snapshot().Status == JobStatusSucceeded }, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(2), job.Snapshot().DoneSlices)
	require.Equal(t, int64(2), job.Snapshot().TotalSlices)
	require.NotEmpty(t, r.List())
	_, ok := r.Get(job.ID)
	require.True(t, ok)
}

func TestRebuildRunnerFixesTotalSlicesBeforeWorkerProgress(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	rebuildEntered := make(chan struct{}, 2)
	rebuildRelease := make(chan struct{})
	r := startBackfillRunner(t, application)
	r.SetSliceSleep(200 * time.Millisecond)
	r.SetDailyRebuilder(&fakeLogDailyRebuilder{
		rebuildEntered: rebuildEntered,
		rebuildRelease: rebuildRelease,
	})

	job, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateA, EndDate: backfillDateB,
		Targets: []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily},
	})
	require.NoError(t, err)
	initial := job.Snapshot()
	require.Equal(t, int64(2), initial.TotalSlices)
	require.Equal(t, JobStatusRunning, initial.Status)
	require.Zero(t, initial.DoneSlices)
	require.Zero(t, initial.ReplayedLogs)

	<-rebuildEntered
	blocked := job.Snapshot()
	require.Equal(t, initial.Status, blocked.Status)
	require.Equal(t, initial.DoneSlices, blocked.DoneSlices)
	require.Equal(t, initial.TotalSlices, blocked.TotalSlices)
	require.Equal(t, initial.ReplayedLogs, blocked.ReplayedLogs)
	close(rebuildRelease)
	require.Eventually(t, func() bool {
		snapshot := job.Snapshot()
		return snapshot.Status == JobStatusRunning && snapshot.DoneSlices == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(2), job.Snapshot().TotalSlices, "running progress denominator must not retreat")
	require.Eventually(t, func() bool {
		return job.Snapshot().Status == JobStatusSucceeded
	}, time.Second, 5*time.Millisecond)
	require.Equal(t, int64(2), job.Snapshot().DoneSlices)
	require.Equal(t, int64(2), job.Snapshot().TotalSlices)
}

func TestRebuildRunnerManualDailyJobUsesLogRebuilderWithoutChangingAutomaticMarker(t *testing.T) {
	_, logDB, application := openSplitBillingDBs(t)
	seedBackfillRequests(t, logDB, backfillRequest("manual", backfillDateB, 13))
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: models.DailyBillingBackfillVersion, State: models.DailyBillingBackfillCompleted,
		StartDate: backfillDateA, EndDate: backfillDateA, LastCompletedDate: backfillDateA,
		StartedAtUnix: 1, CompletedAtUnix: 2, UpdatedAtUnix: 2,
	}).Error)
	r := startBackfillRunner(t, application)

	job, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateB, EndDate: backfillDateB,
		Targets: []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily},
	})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return job.Snapshot().Status != JobStatusRunning }, time.Second, 5*time.Millisecond)
	require.Equal(t, JobStatusSucceeded, job.Snapshot().Status)
	var marker models.DailyBillingBackfill
	require.NoError(t, logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error)
	require.Equal(t, backfillDateA, marker.LastCompletedDate)
	require.Equal(t, int64(1), countRows(t, logDB, &models.TokenDailyBilling{}))
	require.Equal(t, int64(1), countRows(t, logDB, &models.ChannelDailyBilling{}))
}

func TestRebuildRunnerAppliesManualDailyTargets(t *testing.T) {
	tests := []struct {
		name                string
		targets             []string
		wantTokenRequests   int64
		wantChannelRequests int64
	}{
		{name: "token only preserves channel", targets: []string{dao.RebuildTargetTokenDaily}, wantTokenRequests: 1, wantChannelRequests: 77},
		{name: "channel only preserves token", targets: []string{dao.RebuildTargetChannelDaily}, wantTokenRequests: 55, wantChannelRequests: 1},
		{name: "empty means both", targets: nil, wantTokenRequests: 1, wantChannelRequests: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, logDB, application := openSplitBillingDBs(t)
			seedBackfillRequests(t, logDB, backfillRequest("manual-target", backfillDateA, 13))
			require.NoError(t, logDB.Create(&models.TokenDailyBilling{
				Date: backfillDateA, UserID: 99, TokenID: 99, RequestCount: 55,
			}).Error)
			require.NoError(t, logDB.Create(&models.ChannelDailyBilling{
				Date: backfillDateA, ChannelID: 99, RequestCount: 77,
			}).Error)
			r := startBackfillRunner(t, application)

			job, err := r.Submit(dao.BillingRebuildFilter{
				StartDate: backfillDateA, EndDate: backfillDateA, Targets: tt.targets,
			})
			require.NoError(t, err)
			require.Eventually(t, func() bool {
				return job.Snapshot().Status != JobStatusRunning
			}, time.Second, 5*time.Millisecond)
			require.Equal(t, JobStatusSucceeded, job.Snapshot().Status)

			var tokenRequests int64
			require.NoError(t, logDB.Model(&models.TokenDailyBilling{}).Select("COALESCE(SUM(request_count), 0)").Scan(&tokenRequests).Error)
			require.Equal(t, tt.wantTokenRequests, tokenRequests)
			var channelRequests int64
			require.NoError(t, logDB.Model(&models.ChannelDailyBilling{}).Select("COALESCE(SUM(request_count), 0)").Scan(&channelRequests).Error)
			require.Equal(t, tt.wantChannelRequests, channelRequests)
		})
	}
}

func TestRebuildRunnerCloseCancelsDailyJob(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	entered := make(chan struct{}, 1)
	r := startBackfillRunner(t, application)
	r.SetDailyRebuilder(&fakeLogDailyRebuilder{
		bounds:         dao.RequestLogDateBounds{StartDate: backfillDateA, EndDate: backfillDateA},
		rebuildEntered: entered, rebuildRelease: make(chan struct{}),
	})
	job, err := r.Submit(dao.BillingRebuildFilter{
		StartDate: backfillDateA, EndDate: backfillDateA,
		Targets: []string{dao.RebuildTargetTokenDaily},
	})
	require.NoError(t, err)
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, r.Close(ctx))
	require.Eventually(t, func() bool { return job.Snapshot().Status == JobStatusCanceled }, time.Second, 5*time.Millisecond)
}
