package billing

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/dao"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	backfillDateA = "2026-05-01"
	backfillDateB = "2026-05-03"
)

type splitBillingApp struct {
	core *gorm.DB
	log  *gorm.DB
}

func (a *splitBillingApp) GetCoreDB() *gorm.DB { return a.core }
func (a *splitBillingApp) GetLogDB() *gorm.DB  { return a.log }
func (a *splitBillingApp) GetDatabaseLayoutMode() app.DatabaseLayoutMode {
	return app.DatabaseLayoutSplit
}

func TestDailyBillingBackfillEmptyLogCompletesWithoutJob(t *testing.T) {
	core, logDB, application := openSplitBillingDBs(t)
	seedRetiredCoreBillingTables(t, core)
	runner := startBackfillRunner(t, application)
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), retireCoreBillingAfterBackfill(application))
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillCompleted && retiredCoreBillingTablesAbsent(core)
	}, time.Second, 5*time.Millisecond)
	require.Empty(t, runner.List())
}

func TestDailyBillingBackfillFirstStartupSubmitsDailyOnlyJob(t *testing.T) {
	core, logDB, application := openSplitBillingDBs(t)
	seedRetiredCoreBillingTables(t, core)
	seedBackfillRequests(t, logDB, backfillRequest("first-a", backfillDateA, 11), backfillRequest("first-b", backfillDateB, 13))
	runner := startBackfillRunner(t, application)
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), retireCoreBillingAfterBackfill(application))
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool { return len(runner.List()) == 1 }, time.Second, 5*time.Millisecond)
	job := runner.List()[0].Snapshot()
	require.Equal(t, []string{dao.RebuildTargetTokenDaily, dao.RebuildTargetChannelDaily}, job.Filter.Targets)
	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillCompleted && marker.LastCompletedDate == backfillDateB &&
			retiredCoreBillingTablesAbsent(core)
	}, time.Second, 5*time.Millisecond)
}

func TestDailyBillingBackfillResumeAtEndRetiresCoreWithoutRestart(t *testing.T) {
	core, logDB, application := openSplitBillingDBs(t)
	seedRetiredCoreBillingTables(t, core)
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: models.DailyBillingBackfillVersion, State: models.DailyBillingBackfillRunning,
		StartDate: backfillDateA, EndDate: backfillDateA, LastCompletedDate: backfillDateA,
	}).Error)
	runner := startBackfillRunner(t, application)
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), retireCoreBillingAfterBackfill(application))
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillCompleted && retiredCoreBillingTablesAbsent(core)
	}, time.Second, 5*time.Millisecond)
	require.Empty(t, runner.List())
}

func TestDailyBillingBackfillCompletionFailureFailsJobAfterCompletedMarker(t *testing.T) {
	_, logDB, application := openSplitBillingDBs(t)
	seedBackfillRequests(t, logDB, backfillRequest("completion-failure", backfillDateA, 11))
	runner := startBackfillRunner(t, application)
	completionFailure := errors.New("forced projection retirement failure")
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), func(context.Context) error {
		return completionFailure
	})
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool {
		return len(runner.List()) == 1 && runner.List()[0].Snapshot().Status == JobStatusFailed
	}, time.Second, 5*time.Millisecond)
	job := runner.List()[0].Snapshot()
	require.ErrorContains(t, errors.New(job.Error), completionFailure.Error())
	var marker models.DailyBillingBackfill
	require.NoError(t, logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error)
	require.Equal(t, models.DailyBillingBackfillCompleted, marker.State)
}

func TestDailyBillingBackfillFailureIsRecordedAndRetriedOnRestart(t *testing.T) {
	_, logDB, application := openSplitBillingDBs(t)
	seedBackfillRequests(t, logDB, backfillRequest("retry-a", backfillDateA, 11))
	runner1 := startBackfillRunner(t, application)
	coordinator1 := NewDailyBillingBackfill(application, runner1, zap.NewNop(), nil)
	coordinator1.SetRebuilder(&fakeLogDailyRebuilder{boundsErr: errors.New("forced bounds failure")})
	coordinator1.Start(t.Context())
	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillFailed && marker.LastError == "forced bounds failure"
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, coordinator1.Close(context.Background()))
	require.NoError(t, runner1.Close(context.Background()))

	runner2 := startBackfillRunner(t, application)
	coordinator2 := NewDailyBillingBackfill(application, runner2, zap.NewNop(), nil)
	coordinator2.Start(t.Context())
	t.Cleanup(func() { _ = coordinator2.Close(context.Background()) })
	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillCompleted && marker.LastCompletedDate == backfillDateA
	}, time.Second, 5*time.Millisecond)
}

func TestDailyBillingBackfillRebuildFailureRecordsError(t *testing.T) {
	_, logDB, application := openSplitBillingDBs(t)
	runner := startBackfillRunner(t, application)
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), nil)
	rebuilder := &fakeLogDailyRebuilder{
		bounds:     dao.RequestLogDateBounds{StartDate: backfillDateA, EndDate: backfillDateA},
		rebuildErr: errors.New("forced daily rebuild failure"),
	}
	coordinator.SetRebuilder(rebuilder)
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool {
		var marker models.DailyBillingBackfill
		return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
			marker.State == models.DailyBillingBackfillFailed && marker.LastError == "forced daily rebuild failure"
	}, time.Second, 5*time.Millisecond)
	rebuilder.mu.Lock()
	require.Equal(t, []dao.DailyBillingRebuildTargets{{TokenDaily: true, ChannelDaily: true}}, rebuilder.targets)
	rebuilder.mu.Unlock()
}

func TestDailyBillingBackfillRestartResumesAfterCheckpoint(t *testing.T) {
	for _, state := range []models.DailyBillingBackfillState{
		models.DailyBillingBackfillPending,
		models.DailyBillingBackfillRunning,
	} {
		t.Run(string(state), func(t *testing.T) {
			_, logDB, application := openSplitBillingDBs(t)
			seedBackfillRequests(t, logDB, backfillRequest("resume-a", backfillDateA, 11), backfillRequest("resume-b", backfillDateB, 13))
			require.NoError(t, logDB.Create(&models.TokenDailyBilling{
				Date: backfillDateA, UserID: 1, TokenID: 2, RequestCount: 77,
			}).Error)
			require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
				Version: models.DailyBillingBackfillVersion, State: state,
				StartDate: backfillDateA, EndDate: backfillDateB, LastCompletedDate: backfillDateA,
			}).Error)
			runner := startBackfillRunner(t, application)
			coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), nil)
			coordinator.Start(t.Context())
			t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

			require.Eventually(t, func() bool {
				var marker models.DailyBillingBackfill
				return logDB.First(&marker, "version = ?", models.DailyBillingBackfillVersion).Error == nil &&
					marker.State == models.DailyBillingBackfillCompleted && marker.LastCompletedDate == backfillDateB
			}, time.Second, 5*time.Millisecond)
			var preserved models.TokenDailyBilling
			require.NoError(t, logDB.First(&preserved, "date = ?", backfillDateA).Error)
			require.Equal(t, int64(77), preserved.RequestCount)
		})
	}
}

func TestDailyBillingBackfillCompletedDoesNotSubmitAgain(t *testing.T) {
	_, logDB, application := openSplitBillingDBs(t)
	seedBackfillRequests(t, logDB, backfillRequest("completed", backfillDateA, 11))
	require.NoError(t, logDB.Create(&models.DailyBillingBackfill{
		Version: models.DailyBillingBackfillVersion, State: models.DailyBillingBackfillCompleted,
		StartDate: backfillDateA, EndDate: backfillDateA, LastCompletedDate: backfillDateA,
	}).Error)
	runner := startBackfillRunner(t, application)
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), nil)
	coordinator.Start(t.Context())
	t.Cleanup(func() { _ = coordinator.Close(context.Background()) })

	require.Eventually(t, func() bool {
		select {
		case <-coordinator.Done():
			return true
		default:
			return false
		}
	}, time.Second, 5*time.Millisecond)
	require.Empty(t, runner.List())
}

func TestDailyBillingBackfillCloseCancelsAndWaitsForStartup(t *testing.T) {
	_, _, application := openSplitBillingDBs(t)
	runner := startBackfillRunner(t, application)
	entered := make(chan struct{})
	fake := &fakeLogDailyRebuilder{boundsEntered: entered, blockBounds: true}
	coordinator := NewDailyBillingBackfill(application, runner, zap.NewNop(), nil)
	coordinator.SetRebuilder(fake)
	coordinator.Start(context.Background())
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, coordinator.Close(ctx))
	select {
	case <-coordinator.Done():
	case <-time.After(time.Second):
		t.Fatal("daily backfill coordinator did not stop")
	}
}

type fakeLogDailyRebuilder struct {
	mu             sync.Mutex
	bounds         dao.RequestLogDateBounds
	boundsErr      error
	boundsEntered  chan struct{}
	blockBounds    bool
	rebuildEntered chan struct{}
	rebuildRelease chan struct{}
	rebuildErr     error
	rebuilt        []string
	targets        []dao.DailyBillingRebuildTargets
}

func (f *fakeLogDailyRebuilder) FindRequestLogDateBounds(ctx context.Context) (dao.RequestLogDateBounds, error) {
	if f.boundsEntered != nil {
		close(f.boundsEntered)
	}
	if f.blockBounds {
		<-ctx.Done()
		return dao.RequestLogDateBounds{}, context.Cause(ctx)
	}
	return f.bounds, f.boundsErr
}

func (f *fakeLogDailyRebuilder) FindNextRequestLogDate(_ context.Context, after, end string) (string, bool, error) {
	if after < backfillDateA && backfillDateA <= end {
		return backfillDateA, true, nil
	}
	if after < backfillDateB && backfillDateB <= end {
		return backfillDateB, true, nil
	}
	return "", false, nil
}

func (f *fakeLogDailyRebuilder) RebuildLogDailyDate(ctx context.Context, date string, _ uint, targets dao.DailyBillingRebuildTargets) (*dao.BillingRebuildResult, error) {
	f.mu.Lock()
	f.targets = append(f.targets, targets)
	f.mu.Unlock()
	if f.rebuildEntered != nil {
		select {
		case f.rebuildEntered <- struct{}{}:
		default:
		}
	}
	if f.rebuildRelease != nil {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		case <-f.rebuildRelease:
		}
	}
	if f.rebuildErr != nil {
		return nil, f.rebuildErr
	}
	f.mu.Lock()
	f.rebuilt = append(f.rebuilt, date)
	f.mu.Unlock()
	return &dao.BillingRebuildResult{ReplayedLogs: 1}, nil
}

func openSplitBillingDBs(t *testing.T) (*gorm.DB, *gorm.DB, *splitBillingApp) {
	t.Helper()
	open := func(path string, migrate func(*gorm.DB) error) *gorm.DB {
		db, err := gorm.Open(sqlite.Open(path+"?_pragma=busy_timeout(5000)"), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := db.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(4)
		t.Cleanup(func() { _ = sqlDB.Close() })
		require.NoError(t, migrate(db))
		return db
	}
	core := open(filepath.Join(t.TempDir(), "core.db"), models.MigrateCoreDB)
	logDB := open(filepath.Join(t.TempDir(), "log.db"), models.MigrateLogDB)
	return core, logDB, &splitBillingApp{core: core, log: logDB}
}

func startBackfillRunner(t *testing.T, application dao.AppProvider) *RebuildRunner {
	t.Helper()
	runner := NewRebuildRunner(application, zap.NewNop(), time.Hour)
	runner.Start(t.Context())
	t.Cleanup(func() { _ = runner.Close(context.Background()) })
	return runner
}

func backfillRequest(id, date string, prompt int) models.RequestLog {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		panic(err)
	}
	return models.RequestLog{
		RequestID: id, UserID: 1, TokenID: 2, TokenName: "token",
		ChannelID: 3, OwnerType: "admin", ChannelName: "channel",
		PromptTokens: prompt, CompletionTokens: prompt * 2, TotalCost: int64(prompt * 3),
		Status: 1, CreatedAt: parsed.Add(time.Duration(prompt) * time.Second).Unix(),
	}
}

func seedBackfillRequests(t *testing.T, db *gorm.DB, requests ...models.RequestLog) {
	t.Helper()
	require.NoError(t, db.Create(&requests).Error)
}

func seedRetiredCoreBillingTables(t *testing.T, core *gorm.DB) {
	t.Helper()
	require.NoError(t, core.AutoMigrate(
		&models.BillingProjectionReceipt{},
		&models.BillingProjectionBaseline{},
		&models.BillingHourlyBucket{},
		&models.TokenDailyBilling{},
		&models.ChannelDailyBilling{},
	))
}

func retiredCoreBillingTablesAbsent(core *gorm.DB) bool {
	return !core.Migrator().HasTable(&models.BillingProjectionReceipt{}) &&
		!core.Migrator().HasTable(&models.BillingProjectionBaseline{}) &&
		!core.Migrator().HasTable(&models.BillingHourlyBucket{}) &&
		!core.Migrator().HasTable(&models.TokenDailyBilling{}) &&
		!core.Migrator().HasTable(&models.ChannelDailyBilling{})
}

func retireCoreBillingAfterBackfill(application *splitBillingApp) func(context.Context) error {
	return func(ctx context.Context) error {
		_, err := masterdatabase.RetireCoreBillingProjectionTables(ctx, application.GetCoreDB(), application.GetLogDB())
		return err
	}
}
