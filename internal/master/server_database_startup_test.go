package master

import (
	"bytes"
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestRollingBootstrapIsReadyBeforeHistoryCopy(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	require.Equal(t, app.DatabaseLayoutSplit, srv.App.GetDatabaseLayoutMode())
	require.NotNil(t, srv.HistoryBackfillWorker)
	var users, billingRows, requestRows int64
	require.NoError(t, srv.DB.Model(&models.User{}).Count(&users).Error)
	require.EqualValues(t, 1, users)
	require.NoError(t, srv.DB.Model(&models.BillingLog{}).Count(&billingRows).Error)
	require.Zero(t, billingRows)
	require.NoError(t, srv.App.GetLogDB().Model(&models.RequestLog{}).Count(&requestRows).Error)
	require.Zero(t, requestRows)

	serveStarted := make(chan struct{})
	release := make(chan struct{})
	srv.afterHTTPServeStarted = func() {
		close(serveStarted)
		<-release
	}
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not become ready")
	}
	response, err := http.Get("http://" + srv.Listener.Addr().String() + "/ping")
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, masterhistorybackfill.StatePending, srv.HistoryBackfillWorker.Status().State)
	close(release)
	require.Eventually(t, func() bool {
		return srv.DB.Model(&models.BillingLog{}).Count(&billingRows).Error == nil && billingRows == 1 &&
			srv.App.GetLogDB().Model(&models.RequestLog{}).Count(&requestRows).Error == nil && requestRows == 1
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, srv.Shutdown(context.Background()))
	require.ErrorIs(t, <-runDone, http.ErrServerClosed)
}

func TestCurrentV5BootstrapsCoreAndReusesExistingLog(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	legacy := openMigratedCore(t, cfg.Master.LegacyDBPath)
	require.NoError(t, legacy.Create(&models.User{ID: 7, Username: "v5", Quota: 100}).Error)
	require.NoError(t, legacy.Create(&models.BillingLog{RequestID: "v5-billing", CreatedAt: 1}).Error)
	closeGormDatabase(legacy)
	logDB, err := masterdatabase.NewConnector().OpenLog(cfg.Master.LogDBPath)
	require.NoError(t, err)
	require.NoError(t, models.MigrateLogDB(logDB))
	require.NoError(t, logDB.Create(&models.RequestLog{RequestID: "keep-log", CreatedAt: 1}).Error)
	closeGormDatabase(logDB)

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	var users, billingRows, requestRows int64
	require.NoError(t, srv.DB.Model(&models.User{}).Count(&users).Error)
	require.EqualValues(t, 1, users)
	require.NoError(t, srv.DB.Model(&models.BillingLog{}).Count(&billingRows).Error)
	require.Zero(t, billingRows)
	require.NoError(t, srv.App.GetLogDB().Model(&models.RequestLog{}).Count(&requestRows).Error)
	require.EqualValues(t, 1, requestRows)
	require.Equal(t, string(masterdatabase.LegacyLayoutV5Core), srv.HistoryBackfillWorker.Status().SourceKind)
}

func TestFreshInstallCreatesCompletedNoSourceMarker(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	var migration models.HistoryMigration
	require.NoError(t, srv.DB.First(&migration, models.HistoryMigrationSingletonID).Error)
	require.Equal(t, string(masterhistorybackfill.StateCompleted), migration.State)
	require.Equal(t, string(masterdatabase.LegacyLayoutNone), migration.SourceKind)
	require.Empty(t, migration.SourcePath)
	require.NoFileExists(t, cfg.Master.LegacyDBPath)
}

func TestSourceDeletedRestartDoesNotRecreateLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	core := openMigratedCore(t, cfg.Master.DBPath)
	require.NoError(t, core.Create(&models.HistoryMigration{ID: 1, SourceKind: string(masterdatabase.LegacyLayoutMonolith), SourcePath: cfg.Master.LegacyDBPath, State: string(masterhistorybackfill.StateSourceDeleted)}).Error)
	closeGormDatabase(core)

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	require.NoFileExists(t, cfg.Master.LegacyDBPath)
	require.Equal(t, masterhistorybackfill.StateSourceDeleted, srv.HistoryBackfillWorker.Status().State)
}

func TestTerminalHistoryMarkerSkipsCorruptConfiguredLegacySource(t *testing.T) {
	for _, test := range []struct {
		name       string
		state      masterhistorybackfill.State
		sourceKind masterdatabase.LegacyLayout
	}{
		{name: "completed", state: masterhistorybackfill.StateCompleted, sourceKind: masterdatabase.LegacyLayoutMonolith},
		{name: "source deleted", state: masterhistorybackfill.StateSourceDeleted, sourceKind: masterdatabase.LegacyLayoutMonolith},
		{name: "no source", state: masterhistorybackfill.StateCompleted, sourceKind: masterdatabase.LegacyLayoutNone},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := testDatabaseRuntimeConfig(dir)
			core := openMigratedCore(t, cfg.Master.DBPath)
			sourcePath := cfg.Master.LegacyDBPath
			if test.sourceKind == masterdatabase.LegacyLayoutNone {
				sourcePath = ""
			}
			require.NoError(t, core.Create(&models.HistoryMigration{
				ID: models.HistoryMigrationSingletonID, SourceKind: string(test.sourceKind),
				SourcePath: sourcePath, State: string(test.state),
			}).Error)
			closeGormDatabase(core)
			require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("not a sqlite database"), 0o600))

			srv, err := New(cfg, zap.NewNop())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
			require.Equal(t, test.state, srv.HistoryBackfillWorker.Status().State)
		})
	}
}

func TestTerminalHistoryMarkerSkipsUnresolvableConfiguredLegacyMetadata(t *testing.T) {
	for _, state := range []masterhistorybackfill.State{
		masterhistorybackfill.StateCompleted,
		masterhistorybackfill.StateSourceDeleted,
	} {
		t.Run(string(state), func(t *testing.T) {
			dir := t.TempDir()
			cfg := testDatabaseRuntimeConfig(dir)
			core := openMigratedCore(t, cfg.Master.DBPath)
			require.NoError(t, core.Create(&models.HistoryMigration{
				ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutMonolith),
				SourcePath: filepath.Join(dir, "persisted-source.db"), State: string(state),
			}).Error)
			closeGormDatabase(core)
			loopA := filepath.Join(dir, "legacy-loop-a")
			loopB := filepath.Join(dir, "legacy-loop-b")
			require.NoError(t, os.Symlink(loopB, loopA))
			require.NoError(t, os.Symlink(loopA, loopB))
			cfg.Master.LegacyDBPath = loopA

			srv, err := New(cfg, zap.NewNop())
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
			require.Equal(t, state, srv.HistoryBackfillWorker.Status().State)
		})
	}
}

func TestFreshBootstrapRejectsUnresolvableLegacyMetadataBeforeCreatingDatabases(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	loopA := filepath.Join(dir, "legacy-loop-a")
	loopB := filepath.Join(dir, "legacy-loop-b")
	require.NoError(t, os.Symlink(loopB, loopA))
	require.NoError(t, os.Symlink(loopA, loopB))
	cfg.Master.LegacyDBPath = loopA

	srv, err := New(cfg, zap.NewNop())
	require.Nil(t, srv)
	require.Error(t, err)
	require.NoFileExists(t, cfg.Master.DBPath)
	require.NoFileExists(t, cfg.Master.LogDBPath)
}

func TestTerminalMarkerOnlyInWALSkipsCorruptLegacy(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	writer := openWALCoreWriter(t, cfg.Master.DBPath)
	require.NoError(t, writer.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutMonolith),
		SourcePath: cfg.Master.LegacyDBPath, State: string(masterhistorybackfill.StateCompleted),
	}).Error)
	require.NotEmpty(t, readWALFile(t, cfg.Master.DBPath))
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("not a sqlite database"), 0o600))

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	require.Equal(t, masterhistorybackfill.StateCompleted, srv.HistoryBackfillWorker.Status().State)
}

func TestPendingMarkerOnlyInWALRejectsActiveSourceAliasBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	writer := openWALCoreWriter(t, cfg.Master.DBPath)
	require.NoError(t, writer.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutV5Core),
		SourcePath: cfg.Master.DBPath, State: string(masterhistorybackfill.StatePending),
	}).Error)
	before := readWALFile(t, cfg.Master.DBPath)
	require.NotEmpty(t, before)

	srv, err := New(cfg, zap.NewNop())
	require.Nil(t, srv)
	require.ErrorContains(t, err, "same SQLite file")
	after := readWALFile(t, cfg.Master.DBPath)
	require.True(t, bytes.Equal(before, after))
}

func TestCompletedWALUpdateSupersedesPendingMainMarker(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	writer := openWALCoreWriter(t, cfg.Master.DBPath)
	require.NoError(t, writer.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutV5Core),
		SourcePath: cfg.Master.DBPath, State: string(masterhistorybackfill.StatePending),
	}).Error)
	checkpointCoreWAL(t, writer)
	require.NoError(t, writer.Model(&models.HistoryMigration{}).
		Where("id = ?", models.HistoryMigrationSingletonID).
		Update("state", string(masterhistorybackfill.StateCompleted)).Error)
	require.NotEmpty(t, readWALFile(t, cfg.Master.DBPath))
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("not a sqlite database"), 0o600))

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	require.Equal(t, masterhistorybackfill.StateCompleted, srv.HistoryBackfillWorker.Status().State)
}

func TestPendingHistoryLayoutMismatchDegradesWithoutReadingSource(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)
	core := openMigratedCore(t, cfg.Master.DBPath)
	require.NoError(t, core.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutV5Core),
		SourcePath: cfg.Master.LegacyDBPath, State: string(masterhistorybackfill.StatePending),
	}).Error)
	closeGormDatabase(core)

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	serveStarted := make(chan struct{})
	srv.afterHTTPServeStarted = func() { close(serveStarted) }
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not become ready")
	}
	require.Eventually(t, func() bool {
		status := srv.HistoryBackfillWorker.Status()
		return status.State == masterhistorybackfill.StateDegraded && status.LastError != ""
	}, time.Second, time.Millisecond)
	var rows int64
	require.NoError(t, srv.DB.Model(&models.BillingLog{}).Count(&rows).Error)
	require.Zero(t, rows)
	require.NoError(t, srv.Shutdown(context.Background()))
	require.ErrorIs(t, <-runDone, http.ErrServerClosed)
}

func TestPendingHistoryUsesMarkerSourceAndIgnoresConfiguredLegacyMismatch(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	markerSource := filepath.Join(dir, "marker-source.db")
	createLegacyDatabase(t, markerSource)
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("wrong configured source"), 0o600))
	core := openMigratedCore(t, cfg.Master.DBPath)
	require.NoError(t, core.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutMonolith),
		SourcePath: markerSource, State: string(masterhistorybackfill.StatePending),
	}).Error)
	closeGormDatabase(core)

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	serveStarted := make(chan struct{})
	srv.afterHTTPServeStarted = func() { close(serveStarted) }
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not become ready")
	}
	require.Eventually(t, func() bool {
		var rows int64
		return srv.DB.Model(&models.BillingLog{}).Where("request_id = ?", "legacy-request").Count(&rows).Error == nil && rows == 1
	}, 3*time.Second, 10*time.Millisecond)
	require.Equal(t, markerSource, srv.HistoryBackfillWorker.Status().SourcePath)
	require.NoError(t, srv.Shutdown(context.Background()))
	require.ErrorIs(t, <-runDone, http.ErrServerClosed)
}

func TestPendingHistoryCorruptSourceDegradesThenRecovers(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	core := openMigratedCore(t, cfg.Master.DBPath)
	require.NoError(t, core.Create(&models.HistoryMigration{
		ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutMonolith),
		SourcePath: cfg.Master.LegacyDBPath, State: string(masterhistorybackfill.StatePending),
	}).Error)
	closeGormDatabase(core)
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("not a sqlite database"), 0o600))

	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	serveStarted := make(chan struct{})
	srv.afterHTTPServeStarted = func() { close(serveStarted) }
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not become ready")
	}
	require.Eventually(t, func() bool {
		return srv.HistoryBackfillWorker.Status().State == masterhistorybackfill.StateDegraded
	}, time.Second, time.Millisecond)
	require.NoError(t, os.Remove(cfg.Master.LegacyDBPath))
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)
	srv.HistoryBackfillWorker.RetryNow()
	require.Eventually(t, func() bool {
		var rows int64
		return srv.DB.Model(&models.BillingLog{}).Where("request_id = ?", "legacy-request").Count(&rows).Error == nil && rows == 1
	}, 3*time.Second, 10*time.Millisecond)
	require.NoError(t, srv.Shutdown(context.Background()))
	require.ErrorIs(t, <-runDone, http.ErrServerClosed)
}

func TestFirstBootstrapRejectsCorruptLegacySource(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	require.NoError(t, os.WriteFile(cfg.Master.LegacyDBPath, []byte("not a sqlite database"), 0o600))

	srv, err := New(cfg, zap.NewNop())
	require.Nil(t, srv)
	require.ErrorContains(t, err, "legacy database")
}

func TestDatabaseIdentityAliasesRejectBeforeWritableOpen(t *testing.T) {
	for _, pair := range []string{"core-log", "core-legacy", "log-legacy"} {
		for _, aliasKind := range []string{"lexical", "symlink", "hardlink"} {
			t.Run(pair+"/"+aliasKind, func(t *testing.T) {
				dir := t.TempDir()
				cfg := testDatabaseRuntimeConfig(dir)
				var protectedPath string
				switch pair {
				case "core-log", "core-legacy":
					protectedPath = cfg.Master.DBPath
					closeGormDatabase(openMigratedCore(t, protectedPath))
				case "log-legacy":
					protectedPath = cfg.Master.LogDBPath
					logDB, err := masterdatabase.NewConnector().OpenLog(protectedPath)
					require.NoError(t, err)
					require.NoError(t, models.MigrateLogDB(logDB))
					closeGormDatabase(logDB)
				}
				aliasPath := createDatabaseAlias(t, protectedPath, aliasKind)
				switch pair {
				case "core-log":
					cfg.Master.LogDBPath = aliasPath
				case "core-legacy":
					cfg.Master.LegacyDBPath = aliasPath
				case "log-legacy":
					cfg.Master.LegacyDBPath = aliasPath
				}
				before, err := os.ReadFile(protectedPath)
				require.NoError(t, err)
				beforeInfo, err := os.Stat(protectedPath)
				require.NoError(t, err)

				srv, err := New(cfg, zap.NewNop())
				require.Nil(t, srv)
				require.ErrorContains(t, err, "same SQLite file")
				after, readErr := os.ReadFile(protectedPath)
				require.NoError(t, readErr)
				require.True(t, bytes.Equal(before, after))
				afterInfo, statErr := os.Stat(protectedPath)
				require.NoError(t, statErr)
				require.Equal(t, beforeInfo.ModTime(), afterInfo.ModTime())
				for _, path := range []string{cfg.Master.DBPath, cfg.Master.LogDBPath, cfg.Master.LegacyDBPath, protectedPath} {
					require.NoFileExists(t, path+"-wal")
					require.NoFileExists(t, path+"-shm")
				}
				if pair == "log-legacy" {
					require.NoFileExists(t, cfg.Master.DBPath)
				}
			})
		}
	}
}

func TestNonterminalMarkerSourceAliasRejectsBeforeWritableOpen(t *testing.T) {
	for _, source := range []string{"core", "log"} {
		t.Run(source, func(t *testing.T) {
			dir := t.TempDir()
			cfg := testDatabaseRuntimeConfig(dir)
			core := openMigratedCore(t, cfg.Master.DBPath)
			if source == "log" {
				logDB, err := masterdatabase.NewConnector().OpenLog(cfg.Master.LogDBPath)
				require.NoError(t, err)
				require.NoError(t, models.MigrateLogDB(logDB))
				closeGormDatabase(logDB)
			}
			sourcePath := cfg.Master.DBPath
			if source == "log" {
				sourcePath = cfg.Master.LogDBPath
			}
			require.NoError(t, core.Create(&models.HistoryMigration{
				ID: models.HistoryMigrationSingletonID, SourceKind: string(masterdatabase.LegacyLayoutV5Core),
				SourcePath: sourcePath, State: string(masterhistorybackfill.StatePending),
			}).Error)
			closeGormDatabase(core)
			coreBefore, err := os.ReadFile(cfg.Master.DBPath)
			require.NoError(t, err)
			var logBefore []byte
			if source == "log" {
				logBefore, err = os.ReadFile(cfg.Master.LogDBPath)
				require.NoError(t, err)
			}

			srv, err := New(cfg, zap.NewNop())
			require.Nil(t, srv)
			require.ErrorContains(t, err, "same SQLite file")
			coreAfter, readErr := os.ReadFile(cfg.Master.DBPath)
			require.NoError(t, readErr)
			require.True(t, bytes.Equal(coreBefore, coreAfter))
			if source == "log" {
				logAfter, logErr := os.ReadFile(cfg.Master.LogDBPath)
				require.NoError(t, logErr)
				require.True(t, bytes.Equal(logBefore, logAfter))
			}
		})
	}
}

func TestBootstrapRejectsPartialCoreWithoutHistoryMarker(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)
	core := openMigratedCore(t, cfg.Master.DBPath)
	require.NoError(t, core.Create(&models.User{ID: 99, Username: "partial"}).Error)
	closeGormDatabase(core)

	srv, err := New(cfg, zap.NewNop())
	require.Nil(t, srv)
	require.ErrorContains(t, err, "refuse to overwrite")
}

func TestLogDatabaseUnavailableDegradesAndRecovers(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	require.NoError(t, os.Mkdir(cfg.Master.LogDBPath, 0o755))
	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, srv.Shutdown(context.Background())) })
	require.Nil(t, srv.App.GetLogDB())
	require.NoError(t, srv.DB.Create(&models.User{ID: 42, Username: "degraded", Quota: 1_000_000}).Error)
	require.NoError(t, srv.DB.Create(&models.ModelConfig{ModelName: "priced", InputPrice: 10}).Error)
	entry := protocol.UsageLogEntry{RequestID: "degraded-request", UserID: 42, ModelName: "priced", PromptTokens: 1000, Timestamp: time.Now().Unix(), Status: 1}
	require.NoError(t, srv.Hub.SettleUsage(t.Context(), "agent", []protocol.UsageLogEntry{entry}))
	require.NoError(t, os.Remove(cfg.Master.LogDBPath))
	require.Eventually(t, func() bool {
		db := srv.App.GetLogDB()
		if db == nil {
			return false
		}
		var rows int64
		return db.Model(&models.RequestLog{}).Where("request_id = ?", entry.RequestID).Count(&rows).Error == nil && rows == 1
	}, 5*time.Second, 20*time.Millisecond)
}

func TestDatabaseLayoutRoleConflictsRemainFatal(t *testing.T) {
	t.Run("core has log role", func(t *testing.T) {
		dir := t.TempDir()
		cfg := testDatabaseRuntimeConfig(dir)
		db, err := masterdatabase.NewConnector().OpenLog(cfg.Master.DBPath)
		require.NoError(t, err)
		require.NoError(t, models.MigrateLogDB(db))
		closeGormDatabase(db)
		_, err = New(cfg, zap.NewNop())
		require.ErrorContains(t, err, "layout role")
	})
	t.Run("log has core role", func(t *testing.T) {
		dir := t.TempDir()
		cfg := testDatabaseRuntimeConfig(dir)
		db := openMigratedCore(t, cfg.Master.LogDBPath)
		closeGormDatabase(db)
		_, err := New(cfg, zap.NewNop())
		require.ErrorContains(t, err, "layout role")
	})
}

func TestShutdownStopsHistoryWorkerWithinContext(t *testing.T) {
	dir := t.TempDir()
	cfg := testDatabaseRuntimeConfig(dir)
	createLegacyDatabase(t, cfg.Master.LegacyDBPath)
	srv, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	serveStarted := make(chan struct{})
	srv.afterHTTPServeStarted = func() { close(serveStarted) }
	go func() { _ = srv.Run() }()
	select {
	case <-serveStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not become ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	require.NoError(t, srv.Shutdown(ctx))
}

func createLegacyDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := masterdatabase.NewConnector().OpenCore(path)
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.Create(&models.User{ID: 1, Username: "legacy", Quota: 1000}).Error)
	require.NoError(t, db.Create(&models.UsageLog{RequestID: "legacy-request", UserID: 1, ModelName: "legacy-model", PromptTokens: 2, CompletionTokens: 3, TotalCost: 7, CreatedAt: time.Now().Unix(), Status: 1}).Error)
	closeGormDatabase(db)
}

func openMigratedCore(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := masterdatabase.NewConnector().OpenCore(path)
	require.NoError(t, err)
	require.NoError(t, models.MigrateCoreDB(db))
	return db
}

func createDatabaseAlias(t *testing.T, target, kind string) string {
	t.Helper()
	if kind == "lexical" {
		return target
	}
	alias := filepath.Join(filepath.Dir(target), kind+"-alias.db")
	var err error
	if kind == "symlink" {
		err = os.Symlink(target, alias)
	} else {
		err = os.Link(target, alias)
	}
	require.NoError(t, err)
	return alias
}

func openWALCoreWriter(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db := openMigratedCore(t, path)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.Exec("PRAGMA wal_autocheckpoint = 0").Error)
	checkpointCoreWAL(t, db)
	return db
}

func checkpointCoreWAL(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error)
}

func readWALFile(t *testing.T, path string) []byte {
	t.Helper()
	wal, err := os.ReadFile(path + "-wal")
	require.NoError(t, err)
	return wal
}
