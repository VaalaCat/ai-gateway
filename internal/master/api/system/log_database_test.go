package system

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	masterhistorybackfill "github.com/VaalaCat/ai-gateway/internal/master/historybackfill"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSystemStatsReportsBothDatabasesAndQueue(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "master.db")
	logPath := filepath.Join(dir, "log.db")
	core := openStatusTestDB(t, corePath)
	logDB := openStatusTestDB(t, logPath)
	require.NoError(t, core.Exec("PRAGMA user_version = 7").Error)
	require.NoError(t, logDB.Exec("PRAGMA user_version = 9").Error)

	h := &Handler{
		CoreDatabasePath: corePath + "?_pragma=busy_timeout(5000)&token=core-secret",
		LogDatabasePath:  "file:" + logPath + "?_pragma=journal_mode(WAL)&token=log-secret",
		CoreDatabase:     func() *gorm.DB { return core },
		LogDatabase:      func() *gorm.DB { return logDB },
		LogDatabaseReady: func() bool { return true },
		LogQueueSnapshot: func() LogQueueStatus {
			return LogQueueStatus{Pending: 2, Retry: 3, Inflight: 1, Bytes: 42, OldestSeconds: 5, Dropped: 4, LastError: "last write failed"}
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, "available", resp.Storage.CoreDB.Status)
	require.Equal(t, "available", resp.Storage.LogDB.Status)
	require.Equal(t, corePath, resp.Storage.CoreDB.Path)
	require.Equal(t, logPath, resp.Storage.LogDB.Path)
	require.NotContains(t, resp.Storage.CoreDB.Path, "token")
	require.NotContains(t, resp.Storage.LogDB.Path, "token")
	require.Equal(t, "7", resp.Storage.CoreDB.SchemaVersion)
	require.Equal(t, "9", resp.Storage.LogDB.SchemaVersion)
	require.Greater(t, resp.Storage.CoreDB.SizeBytes, int64(0))
	require.Greater(t, resp.Storage.LogDB.SizeBytes, int64(0))
	require.GreaterOrEqual(t, resp.Storage.CoreDB.OpenConnections, 0)
	require.Equal(t, 2, resp.Storage.LogQueue.Pending)
	require.Equal(t, 3, resp.Storage.LogQueue.Retry)
	require.Equal(t, 1, resp.Storage.LogQueue.Inflight)
	require.Equal(t, int64(42), resp.Storage.LogQueue.Bytes)
	require.Equal(t, uint64(4), resp.Storage.LogQueue.Dropped)
	require.Equal(t, "last write failed", resp.Storage.LogQueue.LastError)
}

func TestSystemStatsListsAllDatabaseTables(t *testing.T) {
	t.Run("split databases include every application table", func(t *testing.T) {
		core := openArtifactTargetDB(t, filepath.Join(t.TempDir(), "master.db"), models.DatabaseRoleCore)
		logDB := openArtifactTargetDB(t, filepath.Join(t.TempDir(), "log.db"), models.DatabaseRoleLog)
		require.NoError(t, core.Create(&models.User{Username: "stats-user"}).Error)
		require.NoError(t, logDB.Create(&models.RequestLog{RequestID: "stats-request"}).Error)

		resp := collectSplitDatabaseStats(t, core, logDB, true)
		tables := decodeTableStats(t, resp)
		wantCore := listUserTables(t, core)
		wantLog := listUserTables(t, logDB)

		require.Len(t, tables, len(wantCore)+len(wantLog))
		for _, name := range wantCore {
			require.Contains(t, tables, "core:"+name)
		}
		for _, name := range wantLog {
			require.Contains(t, tables, "log:"+name)
		}
		require.Equal(t, int64(1), tables["core:users"])
		require.Equal(t, int64(1), tables["log:request_logs"])
	})

	t.Run("same table name remains distinguishable by database", func(t *testing.T) {
		core := openArtifactTargetDB(t, filepath.Join(t.TempDir(), "master.db"), models.DatabaseRoleCore)
		logDB := openArtifactTargetDB(t, filepath.Join(t.TempDir(), "log.db"), models.DatabaseRoleLog)
		require.NoError(t, core.Create(&models.HistoryCursor{Key: "core"}).Error)
		require.NoError(t, logDB.Create(&models.HistoryCursor{Key: "log"}).Error)

		tables := decodeTableStats(t, collectSplitDatabaseStats(t, core, logDB, true))

		require.Equal(t, int64(1), tables["core:history_cursors"])
		require.Equal(t, int64(1), tables["log:history_cursors"])
	})

	t.Run("unavailable log database keeps complete core statistics", func(t *testing.T) {
		core := openArtifactTargetDB(t, filepath.Join(t.TempDir(), "master.db"), models.DatabaseRoleCore)

		resp := collectSplitDatabaseStats(t, core, nil, false)
		tables := decodeTableStats(t, resp)
		wantCore := listUserTables(t, core)

		require.Len(t, tables, len(wantCore))
		for _, name := range wantCore {
			require.Contains(t, tables, "core:"+name)
		}
		for key := range tables {
			require.NotContains(t, key, "log:")
		}
	})
}

func collectSplitDatabaseStats(t *testing.T, core, logDB *gorm.DB, logReady bool) StatsResponse {
	t.Helper()
	h := &Handler{
		CoreDatabase:     func() *gorm.DB { return core },
		LogDatabase:      func() *gorm.DB { return logDB },
		LogDatabaseReady: func() bool { return logReady },
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	return resp
}

func decodeTableStats(t *testing.T, response StatsResponse) map[string]int64 {
	t.Helper()
	payload, err := json.Marshal(response)
	require.NoError(t, err)
	var decoded struct {
		Tables []struct {
			Database string `json:"database"`
			Name     string `json:"name"`
			Count    int64  `json:"count"`
		} `json:"tables"`
	}
	require.NoError(t, json.Unmarshal(payload, &decoded))
	result := make(map[string]int64, len(decoded.Tables))
	for _, table := range decoded.Tables {
		result[table.Database+":"+table.Name] = table.Count
	}
	return result
}

func listUserTables(t *testing.T, db *gorm.DB) []string {
	t.Helper()
	var names []string
	err := db.Raw(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`).Scan(&names).Error
	require.NoError(t, err)
	return names
}

func TestSystemStatsReportsUnavailableLogDatabaseWithoutOpeningIt(t *testing.T) {
	corePath := filepath.Join(t.TempDir(), "master.db")
	core := openStatusTestDB(t, corePath)
	logFinderCalls := 0
	h := &Handler{
		CoreDatabasePath: corePath,
		LogDatabasePath:  filepath.Join(t.TempDir(), "missing-log.db") + "?token=secret",
		CoreDatabase:     func() *gorm.DB { return core },
		LogDatabase: func() *gorm.DB {
			logFinderCalls++
			return nil
		},
		LogDatabaseReady: func() bool { return false },
		LogDatabaseError: func() string { return "open log database: permission denied" },
	}
	application := app.NewApplication()
	application.SetCoreDB(core)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, "unavailable", resp.Storage.LogDB.Status)
	require.Equal(t, "open log database: permission denied", resp.Storage.LogDB.LastError)
	require.Equal(t, 1, logFinderCalls, "status may inspect the current pointer once but must not open a database")
}

func TestBuildDatabaseStatusMarksClosedDatabaseUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "closed.db")
	db := openStatusTestDB(t, path)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	status := buildDatabaseStatus(t.Context(), path+"?token=secret", db, true, "")
	require.Equal(t, databaseUnavailable, status.Status)
	require.NotEmpty(t, status.LastError)
	require.Equal(t, path, status.Path)
	require.NotContains(t, status.Path, "token")
}

func TestBuildDatabaseStatusMarksSchemaQueryFailureUnavailable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-layout-marker.db")
	db := openStatusTestDB(t, path)
	require.NoError(t, db.Exec(`CREATE TABLE database_layouts (
		id integer PRIMARY KEY,
		role text NOT NULL,
		version integer NOT NULL
	)`).Error)

	status := buildDatabaseStatus(t.Context(), path, db, true, "")
	require.Equal(t, databaseUnavailable, status.Status)
	require.Contains(t, status.LastError, "record not found")
	require.Greater(t, status.SizeBytes, int64(0))
}

func TestBuildDatabaseStatusKeepsQueryableDatabaseAvailableWhenSizeStatFails(t *testing.T) {
	db := openStatusTestDB(t, filepath.Join(t.TempDir(), "healthy.db"))
	missingPath := filepath.Join(t.TempDir(), "missing.db")

	status := buildDatabaseStatus(t.Context(), missingPath, db, true, "")
	require.Equal(t, databaseAvailable, status.Status)
	require.Contains(t, status.LastError, "stat database file")
}

func TestRetryLogQueueNowRequiresAdminAndWakesWorker(t *testing.T) {
	wakes := 0
	h := &Handler{RetryLogQueue: func() { wakes++ }}
	application := app.NewApplication()

	_, err := h.RetryLogQueueNow(newSystemOperationContext(t, application, false), RetryLogQueueRequest{})
	requireAPIStatus(t, err, http.StatusForbidden)
	require.Zero(t, wakes)

	resp, err := h.RetryLogQueueNow(newSystemOperationContext(t, application, true), RetryLogQueueRequest{})
	require.NoError(t, err)
	require.True(t, resp.Woken)
	require.Equal(t, 1, wakes)
}

func TestClearLogBacklogKeepsInflightAndReturnsDroppedCounts(t *testing.T) {
	h := &Handler{ClearLogQueueBacklog: func() (ClearLogBacklogResponse, error) {
		return ClearLogBacklogResponse{Pending: 2, Retry: 3, Bytes: 55}, nil
	}}
	application := app.NewApplication()

	_, err := h.ClearLogBacklog(newSystemOperationContext(t, application, true), ClearLogBacklogRequest{})
	requireAPIStatus(t, err, http.StatusBadRequest)

	resp, err := h.ClearLogBacklog(newSystemOperationContext(t, application, true), ClearLogBacklogRequest{Confirm: true})
	require.NoError(t, err)
	require.Equal(t, 2, resp.Pending)
	require.Equal(t, 3, resp.Retry)
	require.Equal(t, 5, resp.Dropped)
	require.Equal(t, int64(55), resp.Bytes)
}

func TestRetryLogQueueConnectorFailureKeepsBacklog(t *testing.T) {
	queue := LogQueueStatus{Retry: 2, Bytes: 50, LastError: "connector unavailable"}
	h := &Handler{
		RetryLogQueue:    func() {},
		LogQueueSnapshot: func() LogQueueStatus { return queue },
	}
	application := app.NewApplication()

	resp, err := h.RetryLogQueueNow(newSystemOperationContext(t, application, true), RetryLogQueueRequest{})
	require.NoError(t, err)
	require.True(t, resp.Woken)
	require.Equal(t, 2, h.LogQueueSnapshot().Retry)
	require.Equal(t, int64(50), h.LogQueueSnapshot().Bytes)
}

func TestSystemStatsUsesHistoryBackfillStatusAndLegacyArtifact(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "master.db")
	require.NoError(t, os.WriteFile(legacyPath, []byte("legacy"), 0o600))
	wantHistory := masterhistorybackfill.Status{
		State: masterhistorybackfill.StateCaughtUp, SourcePath: legacyPath,
		Billing: masterhistorybackfill.CursorStatus{LastSourceID: 9, ProcessedRows: 10}, CanComplete: true,
	}
	wantArtifact := masterdatabase.LegacyArtifact{
		Path: filepath.Join(dir, "master.db.pre-split.bak"), Available: true, Exists: true, SizeBytes: 20,
	}
	require.NoError(t, os.WriteFile(wantArtifact.Path, make([]byte, wantArtifact.SizeBytes), 0o600))
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	h := &Handler{
		CoreDatabasePath:        corePath,
		CoreDatabase:            func() *gorm.DB { return core },
		LogDatabasePath:         logPath,
		LogDatabase:             func() *gorm.DB { return logDB },
		LogDatabaseReady:        func() bool { return true },
		LegacyDatabasePath:      legacyPath,
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status { return wantHistory },
		LegacyArtifactSnapshot:  func() (masterdatabase.LegacyArtifact, error) { return wantArtifact, nil },
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, wantHistory, resp.Storage.HistoryBackfill)
	wantArtifact.CanDelete = true
	require.Equal(t, wantArtifact, resp.Storage.LegacyArtifact)
	require.Equal(t, databaseAvailable, resp.Storage.LegacyDB.Status)
	require.Equal(t, legacyPath, resp.Storage.LegacyDB.Path)
}

func TestSystemStatsMarksLegacyArtifactActiveIdentityInUse(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	require.NoError(t, os.Link(corePath, artifactPath))
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		LogDatabasePath: logPath, LogDatabase: func() *gorm.DB { return logDB },
		LogDatabaseReady: func() bool { return true },
		LegacyArtifactSnapshot: func() (masterdatabase.LegacyArtifact, error) {
			return masterdatabase.LegacyArtifact{Path: artifactPath, Available: true, Exists: true}, nil
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})

	require.NoError(t, err)
	require.True(t, resp.Storage.LegacyArtifact.InUse)
	require.False(t, resp.Storage.LegacyArtifact.CanDelete)
	require.Contains(t, resp.Storage.LegacyArtifact.DeleteError, "active core database")
}

func TestSystemStatsDoesNotTreatNoSourceHistoryAsActive(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	legacyPath := filepath.Join(dir, "legacy.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	require.NoError(t, os.WriteFile(legacyPath, []byte("legacy"), 0o600))
	require.NoError(t, os.Link(legacyPath, artifactPath))
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		LogDatabasePath: logPath, LogDatabase: func() *gorm.DB { return logDB },
		LogDatabaseReady: func() bool { return true },
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status {
			return masterhistorybackfill.Status{
				State: masterhistorybackfill.StateCompleted, SourceKind: string(masterdatabase.LegacyLayoutNone), SourcePath: legacyPath,
			}
		},
		LegacyArtifactSnapshot: func() (masterdatabase.LegacyArtifact, error) {
			return masterdatabase.LegacyArtifact{Path: artifactPath, Available: true, Exists: true}, nil
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})

	require.NoError(t, err)
	require.False(t, resp.Storage.LegacyArtifact.InUse)
	require.True(t, resp.Storage.LegacyArtifact.CanDelete)
}

func TestActiveLegacyDatabaseSourcesFollowsMigrationOwnership(t *testing.T) {
	configuredPath := "/data/configured.db"
	sourcePath := "/data/history.db"
	tests := []struct {
		name       string
		state      masterhistorybackfill.State
		sourceKind masterdatabase.LegacyLayout
		want       []string
	}{
		{
			name: "pending monolith", state: masterhistorybackfill.StatePending,
			sourceKind: masterdatabase.LegacyLayoutMonolith, want: []string{configuredPath, sourcePath},
		},
		{
			name: "completed monolith", state: masterhistorybackfill.StateCompleted,
			sourceKind: masterdatabase.LegacyLayoutMonolith, want: []string{configuredPath, sourcePath},
		},
		{
			name: "completed v5", state: masterhistorybackfill.StateCompleted,
			sourceKind: masterdatabase.LegacyLayoutV5Core, want: []string{configuredPath, sourcePath},
		},
		{
			name: "completed none", state: masterhistorybackfill.StateCompleted,
			sourceKind: masterdatabase.LegacyLayoutNone,
		},
		{
			name: "source deleted", state: masterhistorybackfill.StateSourceDeleted,
			sourceKind: masterdatabase.LegacyLayoutMonolith,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status := masterhistorybackfill.Status{State: tt.state, SourceKind: string(tt.sourceKind), SourcePath: sourcePath}
			require.Equal(t, tt.want, ActiveLegacyDatabaseSources(configuredPath, status))
		})
	}
}

func TestSourceDeletedLegacyDatabaseDoesNotBlockArtifactStatsOrDelete(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	configuredPath := filepath.Join(dir, "master.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	require.NoFileExists(t, configuredPath)
	require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
	require.NoError(t, os.WriteFile(masterdatabase.SplitManifestPath(configuredPath), []byte(
		`{"paths":{"backup_core":"`+artifactPath+`"}}`,
	), 0o600))
	status := masterhistorybackfill.Status{
		State: masterhistorybackfill.StateSourceDeleted, SourceKind: string(masterdatabase.LegacyLayoutMonolith), SourcePath: configuredPath,
	}
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		LogDatabasePath: logPath, LogDatabase: func() *gorm.DB { return logDB },
		LogDatabaseReady:        func() bool { return true },
		LegacyDatabasePath:      configuredPath,
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status { return status },
		LegacyArtifactSnapshot: func() (masterdatabase.LegacyArtifact, error) {
			return masterdatabase.FindLegacyArtifact(configuredPath)
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	application.SetLogDB(logDB)
	application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.True(t, resp.Storage.LegacyArtifact.CanDelete)
	require.False(t, resp.Storage.LegacyArtifact.InUse)

	command := masterdatabase.LegacyArtifactDeletionCommand{
		FindArtifact: func() (masterdatabase.LegacyArtifact, error) {
			return masterdatabase.FindLegacyArtifact(configuredPath)
		},
		BuildValidator: func() masterdatabase.LegacyArtifactDeletionValidator {
			return masterdatabase.LegacyArtifactDeletionValidator{
				CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
				CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
				ActiveLegacySources: ActiveLegacyDatabaseSources(configuredPath, status),
			}
		},
	}
	require.NoError(t, command.Delete("DELETE"))
	require.NoFileExists(t, artifactPath)
}

func TestCompletedLegacySourceIdentityRemainsProtectedFromArtifactDelete(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	configuredPath := filepath.Join(dir, "master.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	require.NoError(t, os.WriteFile(configuredPath, []byte("legacy"), 0o600))
	require.NoError(t, os.Link(configuredPath, artifactPath))
	status := masterhistorybackfill.Status{
		State: masterhistorybackfill.StateCompleted, SourceKind: string(masterdatabase.LegacyLayoutMonolith), SourcePath: configuredPath,
	}
	validator := masterdatabase.LegacyArtifactDeletionValidator{
		CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
		CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
		ActiveLegacySources: ActiveLegacyDatabaseSources(configuredPath, status),
	}

	checked := validator.Check(masterdatabase.LegacyArtifact{Path: artifactPath, Exists: true, Available: true})

	require.True(t, checked.InUse)
	require.False(t, checked.CanDelete)
	require.Contains(t, checked.DeleteError, "active legacy database")
}

func TestNonTerminalMissingLegacySourceFailsArtifactEligibilityClosed(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	logPath := filepath.Join(dir, "log.db")
	configuredPath := filepath.Join(dir, "missing-master.db")
	artifactPath := filepath.Join(dir, "master.db.pre-split.bak")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	logDB := openArtifactTargetDB(t, logPath, models.DatabaseRoleLog)
	require.NoError(t, os.WriteFile(artifactPath, []byte("artifact"), 0o600))
	status := masterhistorybackfill.Status{
		State: masterhistorybackfill.StateCopying, SourceKind: string(masterdatabase.LegacyLayoutMonolith), SourcePath: configuredPath,
	}
	validator := masterdatabase.LegacyArtifactDeletionValidator{
		CoreDB: core, LogDB: logDB, LogDatabaseReady: true,
		CoreDatabaseDSN: corePath, LogDatabaseDSN: logPath,
		ActiveLegacySources: ActiveLegacyDatabaseSources(configuredPath, status),
	}

	checked := validator.Check(masterdatabase.LegacyArtifact{Path: artifactPath, Exists: true, Available: true})

	require.False(t, checked.CanDelete)
	require.Contains(t, checked.DeleteError, "inspect active legacy database")
}

func TestSystemStatsExplainsWhyLegacyArtifactCannotBeDeleted(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	appLogPath := filepath.Join(dir, "app-log.db")
	core := openArtifactTargetDB(t, corePath, models.DatabaseRoleCore)
	appLogDB := openArtifactTargetDB(t, appLogPath, models.DatabaseRoleLog)
	artifact := masterdatabase.LegacyArtifact{
		Path: filepath.Join(dir, "master.db.pre-split.bak"), Available: true, Exists: true,
	}
	require.NoError(t, os.WriteFile(artifact.Path, []byte("artifact"), 0o600))
	tests := []struct {
		name      string
		logDB     func() *gorm.DB
		logPath   string
		logReady  bool
		wantError string
	}{
		{
			name: "log unavailable", logDB: func() *gorm.DB { return nil }, logPath: appLogPath,
			wantError: "log database is unavailable",
		},
		{
			name: "invalid log layout",
			logDB: func() *gorm.DB {
				return openArtifactTargetDB(t, filepath.Join(dir, "wrong-role.db"), models.DatabaseRoleCore)
			},
			logPath: filepath.Join(dir, "wrong-role.db"), logReady: true, wantError: "validate active log database",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &Handler{
				CoreDatabasePath: corePath, LogDatabasePath: tt.logPath,
				CoreDatabase: func() *gorm.DB { return core }, LogDatabase: tt.logDB,
				LogDatabaseReady:       func() bool { return tt.logReady },
				LegacyArtifactSnapshot: func() (masterdatabase.LegacyArtifact, error) { return artifact, nil },
			}
			application := app.NewApplication()
			application.SetCoreDB(core)
			application.SetLogDB(appLogDB)
			application.SetDatabaseLayoutMode(app.DatabaseLayoutSplit)

			resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
			require.NoError(t, err)
			require.False(t, resp.Storage.LegacyArtifact.CanDelete)
			require.Contains(t, resp.Storage.LegacyArtifact.DeleteError, tt.wantError)
			require.Empty(t, resp.Storage.LegacyArtifact.LastError)
		})
	}
}

func TestSystemStatsRepairsCompletedStateWhenSourceIsMissing(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	core := openStatusTestDB(t, corePath)
	status := masterhistorybackfill.Status{State: masterhistorybackfill.StateCompleted, SourcePath: filepath.Join(dir, "missing.db")}
	marked := 0
	h := &Handler{
		CoreDatabasePath:        corePath,
		CoreDatabase:            func() *gorm.DB { return core },
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status { return status },
		ReconcileHistorySourceDeleted: func(context.Context, string) error {
			marked++
			status.State = masterhistorybackfill.StateSourceDeleted
			return nil
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, 1, marked)
	require.Equal(t, masterhistorybackfill.StateSourceDeleted, resp.Storage.HistoryBackfill.State)
}

func TestSystemStatsKeepsUnavailableLegacyArtifactWithoutFailing(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	core := openStatusTestDB(t, corePath)
	want := masterdatabase.LegacyArtifact{Available: false, LastError: "decode legacy split manifest: invalid json"}
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		LegacyArtifactSnapshot: func() (masterdatabase.LegacyArtifact, error) { return want, errors.New(want.LastError) },
	}
	application := app.NewApplication()
	application.SetCoreDB(core)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, want, resp.Storage.LegacyArtifact)
	encoded, err := json.Marshal(resp.Storage.LegacyArtifact)
	require.NoError(t, err)
	require.JSONEq(t, `{"path":"","size_bytes":0,"exists":false,"in_use":false,"available":false,"last_error":"decode legacy split manifest: invalid json","can_delete":false,"delete_error":""}`, string(encoded))
}

func TestFailedLegacySourceDeleteDoesNotLetStatsMarkSourceDeleted(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	legacyPath := filepath.Join(dir, "master.db")
	core := openStatusTestDB(t, corePath)
	require.NoError(t, os.WriteFile(legacyPath, []byte("legacy"), 0o600))
	status := masterhistorybackfill.Status{State: masterhistorybackfill.StateCompleted, SourcePath: legacyPath}
	marked := 0
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status { return status },
		DeleteLegacySource:      func(string) error { return errors.New("remove denied; source restored") },
		ReconcileHistorySourceDeleted: func(_ context.Context, path string) error {
			_, err := os.Lstat(path)
			return err
		},
		MarkHistorySourceDeleted: func(context.Context) error {
			marked++
			return nil
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)
	ctx := newSystemOperationContext(t, application, true)

	_, err := h.DeleteHistoryBackfillSource(ctx, DeleteLegacyFileRequest{Confirmation: "DELETE"})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	resp, err := h.Stats(ctx, StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, masterhistorybackfill.StateCompleted, resp.Storage.HistoryBackfill.State)
	require.Zero(t, marked)
}

func TestSystemStatsDoesNotMarkSourceDeletedWhileCrossProcessMarkerExists(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	legacyPath := filepath.Join(dir, "master.db")
	core := openStatusTestDB(t, corePath)
	require.NoError(t, os.WriteFile(masterdatabase.LegacyDeleteMarkerPath(legacyPath), []byte("pending"), 0o600))
	status := masterhistorybackfill.Status{State: masterhistorybackfill.StateCompleted, SourcePath: legacyPath}
	marked := 0
	h := &Handler{
		CoreDatabasePath: corePath, CoreDatabase: func() *gorm.DB { return core },
		HistoryBackfillSnapshot: func() masterhistorybackfill.Status { return status },
		ReconcileHistorySourceDeleted: func(ctx context.Context, path string) error {
			return ReconcileHistorySourceDeletion(ctx, path, func(context.Context) error { marked++; return nil })
		},
	}
	application := app.NewApplication()
	application.SetCoreDB(core)

	resp, err := h.Stats(newSystemOperationContext(t, application, true), StatsRequest{})
	require.NoError(t, err)
	require.Equal(t, masterhistorybackfill.StateCompleted, resp.Storage.HistoryBackfill.State)
	require.Zero(t, marked)
}

func TestReconcileHistorySourceDeletionDoesNotMarkOnMarkerReadError(t *testing.T) {
	marked := 0
	err := ReconcileHistorySourceDeletion(t.Context(), filepath.Join(t.TempDir(), "master.db")+"\x00", func(context.Context) error {
		marked++
		return nil
	})
	require.ErrorContains(t, err, "legacy delete coordinator")
	require.Zero(t, marked)
}

func TestRetryHistoryBackfillRequiresAdminAndWakesWorker(t *testing.T) {
	wakes := 0
	h := &Handler{RetryHistoryBackfill: func() { wakes++ }}
	application := app.NewApplication()

	_, err := h.RetryHistoryBackfillNow(newSystemOperationContext(t, application, false), RetryHistoryBackfillRequest{})
	requireAPIStatus(t, err, http.StatusForbidden)
	resp, err := h.RetryHistoryBackfillNow(newSystemOperationContext(t, application, true), RetryHistoryBackfillRequest{})
	require.NoError(t, err)
	require.True(t, resp.Woken)
	require.Equal(t, 1, wakes)
}

func TestSkipHistoryBackfillRequiresConfirmation(t *testing.T) {
	skips := 0
	h := &Handler{SkipHistoryBackfill: func(context.Context) error { skips++; return nil }}
	application := app.NewApplication()

	_, err := h.SkipHistoryBackfillRemaining(newSystemOperationContext(t, application, true), SkipHistoryBackfillRequest{})
	requireAPIStatus(t, err, http.StatusBadRequest)
	resp, err := h.SkipHistoryBackfillRemaining(newSystemOperationContext(t, application, true), SkipHistoryBackfillRequest{Confirm: true})
	require.NoError(t, err)
	require.True(t, resp.Skipped)
	require.Equal(t, 1, skips)
}

func TestCompleteHistoryBackfillRequiresAdminConfirmationAndCaughtUp(t *testing.T) {
	application := app.NewApplication()
	calls := 0
	h := &Handler{CompleteHistoryBackfill: func(context.Context) error {
		calls++
		return errors.New("billing is not caught up")
	}}

	_, err := h.CompleteHistoryBackfillNow(newSystemOperationContext(t, application, false), CompleteHistoryBackfillRequest{Confirm: true})
	requireAPIStatus(t, err, http.StatusForbidden)
	_, err = h.CompleteHistoryBackfillNow(newSystemOperationContext(t, application, true), CompleteHistoryBackfillRequest{})
	requireAPIStatus(t, err, http.StatusBadRequest)
	_, err = h.CompleteHistoryBackfillNow(newSystemOperationContext(t, application, true), CompleteHistoryBackfillRequest{Confirm: true})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	require.ErrorContains(t, err, "billing is not caught up")
	require.Equal(t, 1, calls)
}

func TestCompleteHistoryBackfillRejectsMissingCallback(t *testing.T) {
	_, err := (&Handler{}).CompleteHistoryBackfillNow(
		newSystemOperationContext(t, app.NewApplication(), true), CompleteHistoryBackfillRequest{Confirm: true})
	requireAPIStatus(t, err, http.StatusInternalServerError)
}

func TestDeleteLegacySourceRequiresAdminAndMarksSourceDeleted(t *testing.T) {
	application := app.NewApplication()
	deleted, marked := 0, 0
	h := &Handler{
		DeleteLegacySource:       func(confirmation string) error { deleted++; require.Equal(t, "DELETE", confirmation); return nil },
		MarkHistorySourceDeleted: func(context.Context) error { marked++; return nil },
	}

	_, err := h.DeleteHistoryBackfillSource(newSystemOperationContext(t, application, false), DeleteLegacyFileRequest{Confirmation: "DELETE"})
	requireAPIStatus(t, err, http.StatusForbidden)
	resp, err := h.DeleteHistoryBackfillSource(newSystemOperationContext(t, application, true), DeleteLegacyFileRequest{Confirmation: "DELETE"})
	require.NoError(t, err)
	require.True(t, resp.Deleted)
	require.Equal(t, 1, deleted)
	require.Equal(t, 1, marked)
}

func TestDeleteLegacySourceRequiresExactConfirmation(t *testing.T) {
	deleted := 0
	h := &Handler{
		DeleteLegacySource:       func(string) error { deleted++; return nil },
		MarkHistorySourceDeleted: func(context.Context) error { return nil },
	}

	_, err := h.DeleteHistoryBackfillSource(
		newSystemOperationContext(t, app.NewApplication(), true), DeleteLegacyFileRequest{Confirmation: "delete"})
	requireAPIStatus(t, err, http.StatusBadRequest)
	require.Zero(t, deleted)
}

func TestDeleteLegacySourceMismatchDoesNotMarkDeleted(t *testing.T) {
	marked := 0
	h := &Handler{
		DeleteLegacySource: func(string) error { return errors.New("configured source does not match persisted source") },
		MarkHistorySourceDeleted: func(context.Context) error {
			marked++
			return nil
		},
	}

	_, err := h.DeleteHistoryBackfillSource(
		newSystemOperationContext(t, app.NewApplication(), true), DeleteLegacyFileRequest{Confirmation: "DELETE"})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	require.Zero(t, marked)
}

func TestDeleteLegacyArtifactDoesNotUpdateMigrationState(t *testing.T) {
	deleted, marked := 0, 0
	h := &Handler{
		DeleteLegacyArtifact: func(confirmation string) error { deleted++; return nil },
		MarkHistorySourceDeleted: func(context.Context) error {
			marked++
			return nil
		},
	}
	resp, err := h.DeleteHistoryBackfillLegacyArtifact(
		newSystemOperationContext(t, app.NewApplication(), true), DeleteLegacyFileRequest{Confirmation: "DELETE"})
	require.NoError(t, err)
	require.True(t, resp.Deleted)
	require.Equal(t, 1, deleted)
	require.Zero(t, marked)
}

func TestDeleteLegacyFilesRejectMissingCallbacks(t *testing.T) {
	ctx := newSystemOperationContext(t, app.NewApplication(), true)
	_, err := (&Handler{}).DeleteHistoryBackfillSource(ctx, DeleteLegacyFileRequest{Confirmation: "DELETE"})
	requireAPIStatus(t, err, http.StatusInternalServerError)
	_, err = (&Handler{}).DeleteHistoryBackfillLegacyArtifact(ctx, DeleteLegacyFileRequest{Confirmation: "DELETE"})
	requireAPIStatus(t, err, http.StatusInternalServerError)
}

func TestMarkHistorySourceDeletedRequiresTerminalMigration(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		wantError bool
	}{
		{name: "completed", state: string(masterhistorybackfill.StateCompleted)},
		{name: "idempotent", state: string(masterhistorybackfill.StateSourceDeleted)},
		{name: "copying", state: string(masterhistorybackfill.StateCopying), wantError: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openStatusTestDB(t, filepath.Join(t.TempDir(), "core.db"))
			require.NoError(t, db.AutoMigrate(&models.HistoryMigration{}))
			require.NoError(t, db.Create(&models.HistoryMigration{
				ID: models.HistoryMigrationSingletonID, SourceKind: "monolith", SourcePath: "/data/master.db", State: tt.state,
			}).Error)

			err := MarkHistorySourceDeleted(t.Context(), db, 1_800_000_000)
			if tt.wantError {
				require.ErrorContains(t, err, "not completed")
				return
			}
			require.NoError(t, err)
			var migration models.HistoryMigration
			require.NoError(t, db.First(&migration, models.HistoryMigrationSingletonID).Error)
			require.Equal(t, string(masterhistorybackfill.StateSourceDeleted), migration.State)
			require.EqualValues(t, 1_800_000_000, migration.SourceDeletedAtUnix)
		})
	}
}

func openStatusTestDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.Exec("CREATE TABLE status_probe (id INTEGER PRIMARY KEY)").Error)
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func openArtifactTargetDB(t *testing.T, path, role string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	require.NoError(t, err)
	if role == models.DatabaseRoleCore {
		require.NoError(t, models.MigrateCoreDB(db))
	} else {
		require.NoError(t, models.MigrateLogDB(db))
	}
	t.Cleanup(func() {
		sqlDB, sqlErr := db.DB()
		if sqlErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func newSystemOperationContext(t *testing.T, application app.Application, admin bool) *app.Context {
	t.Helper()
	w := httptest.NewRecorder()
	ginCtx, _ := gin.CreateTestContext(w)
	ginCtx.Set(consts.CtxKeyRequestScope, &middleware.RequestScope{IsAdmin: admin, UserID: 1})
	return &app.Context{Context: ginCtx, App: application, UserInfo: &app.UserInfo{UserID: 1}, OwnerContext: t.Context()}
}

func requireAPIStatus(t *testing.T, err error, status int) {
	t.Helper()
	require.Error(t, err)
	var apiErr *api.APIError
	require.True(t, errors.As(err, &apiErr), "err=%v", err)
	require.Equal(t, status, apiErr.Status)
}
