package database

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestParseSQLiteDSN(t *testing.T) {
	tests := []struct {
		name     string
		dsn      string
		memory   bool
		filePath string
	}{
		{name: "memory sentinel", dsn: ":memory:", memory: true},
		{name: "memory URI sentinel", dsn: "file::memory:?cache=shared", memory: true},
		{name: "named memory URI", dsn: "file:core?mode=memory&cache=shared", memory: true},
		{name: "mode substring in another value", dsn: "file:disk.db?label=mode=memory", filePath: "disk.db"},
		{name: "mode value prefix", dsn: "file:disk.db?mode=memory_backup", filePath: "disk.db"},
		{name: "absolute file URI", dsn: "file:/tmp/core.db?cache=shared", filePath: "/tmp/core.db"},
		{name: "percent encoded file URI", dsn: "file:/tmp/state%20dir/core.db", filePath: "/tmp/state dir/core.db"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed, err := ParseSQLiteDSN(tt.dsn)
			if err != nil {
				t.Fatalf("ParseSQLiteDSN: %v", err)
			}
			if parsed.Memory != tt.memory {
				t.Fatalf("Memory = %v, want %v", parsed.Memory, tt.memory)
			}
			if parsed.FilesystemPath != tt.filePath {
				t.Fatalf("FilesystemPath = %q, want %q", parsed.FilesystemPath, tt.filePath)
			}
		})
	}
}

func TestParseSQLiteDSNRejectsEmpty(t *testing.T) {
	for _, dsn := range []string{"   ", "file:", "file:?cache=shared", "?cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			_, err := ParseSQLiteDSN(dsn)
			if err == nil {
				t.Fatalf("ParseSQLiteDSN(%q) accepted an empty file path", dsn)
			}
			if !strings.Contains(err.Error(), "SQLite DSN") || !strings.Contains(err.Error(), "file path is empty") {
				t.Fatalf("ParseSQLiteDSN(%q) error = %v, want actionable SQLite DSN file path error", dsn, err)
			}
		})
	}
}

func TestConnectorRejectsEmptyFilePaths(t *testing.T) {
	connector := NewConnector()
	openers := map[string]func(string) (*gorm.DB, error){
		"core": connector.OpenCore,
		"log":  connector.OpenLog,
	}
	for role, open := range openers {
		for _, dsn := range []string{"file:", "file:?cache=shared", "?cache=shared"} {
			t.Run(role+"/"+dsn, func(t *testing.T) {
				_, err := open(dsn)
				if err == nil {
					t.Fatalf("%s opener accepted %q", role, dsn)
				}
				if !strings.Contains(err.Error(), "SQLite DSN") || !strings.Contains(err.Error(), "file path is empty") {
					t.Fatalf("%s opener error = %v, want actionable SQLite DSN file path error", role, err)
				}
			})
		}
	}
}

func TestValidatePaths(t *testing.T) {
	t.Run("different normalized paths", func(t *testing.T) {
		dir := t.TempDir()
		err := ValidatePaths(
			filepath.Join(dir, "core.db")+"?_pragma=journal_mode(WAL)",
			filepath.Join(dir, ".", "log.db")+"?cache=shared",
		)
		if err != nil {
			t.Fatalf("ValidatePaths: %v", err)
		}
	})

	t.Run("same normalized path", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		err := ValidatePaths(corePath+"?cache=shared", filepath.Join(dir, ".", "core.db"))
		if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
			t.Fatalf("error = %v, want same SQLite file", err)
		}
	})

	t.Run("symlinks resolve to same existing file", func(t *testing.T) {
		dir := t.TempDir()
		corePath := filepath.Join(dir, "core.db")
		if err := os.WriteFile(corePath, nil, 0o600); err != nil {
			t.Fatalf("write core db: %v", err)
		}
		aliasPath := filepath.Join(dir, "alias.db")
		if err := os.Symlink(corePath, aliasPath); err != nil {
			t.Fatalf("create symlink: %v", err)
		}
		err := ValidatePaths(corePath, aliasPath)
		if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
			t.Fatalf("error = %v, want same SQLite file", err)
		}
	})

	t.Run("file URI and plain path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "shared.db")
		err := ValidatePaths("file:"+path+"?cache=shared", path)
		if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
			t.Fatalf("error = %v, want same SQLite file", err)
		}
	})

	t.Run("percent encoded URI and plain path", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "shared db.sqlite")
		uri := (&url.URL{Scheme: "file", Path: path}).String()
		err := ValidatePaths(uri, path)
		if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
			t.Fatalf("error = %v, want same SQLite file", err)
		}
	})

	t.Run("nonexistent files under symlinked parent", func(t *testing.T) {
		realDir := t.TempDir()
		aliasRoot := t.TempDir()
		aliasDir := filepath.Join(aliasRoot, "state")
		if err := os.Symlink(realDir, aliasDir); err != nil {
			t.Fatalf("create parent symlink: %v", err)
		}
		err := ValidatePaths(filepath.Join(realDir, "new.db"), filepath.Join(aliasDir, "new.db"))
		if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
			t.Fatalf("error = %v, want same SQLite file", err)
		}
	})
}

func TestReadHistoryMigrationMarkerReadsUncheckpointedWAL(t *testing.T) {
	t.Run("marker exists only in WAL", func(t *testing.T) {
		path, writer := openWALMarkerWriter(t)
		require.NoError(t, writer.Create(&models.HistoryMigration{
			ID: models.HistoryMigrationSingletonID, SourceKind: string(LegacyLayoutMonolith),
			SourcePath: filepath.Join(filepath.Dir(path), "master.db"), State: "completed",
		}).Error)
		mainBefore, err := os.ReadFile(path)
		require.NoError(t, err)
		before := requireNonemptyWAL(t, path)

		migration, found, err := NewConnector().ReadHistoryMigrationMarker(path)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "completed", migration.State)
		after, err := os.ReadFile(path + "-wal")
		require.NoError(t, err)
		require.True(t, bytes.Equal(before, after))
		mainAfter, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, bytes.Equal(mainBefore, mainAfter))
	})

	t.Run("WAL update supersedes main marker", func(t *testing.T) {
		path, writer := openWALMarkerWriter(t)
		require.NoError(t, writer.Create(&models.HistoryMigration{
			ID: models.HistoryMigrationSingletonID, SourceKind: string(LegacyLayoutMonolith),
			SourcePath: path, State: "pending",
		}).Error)
		checkpointWAL(t, writer)
		require.NoError(t, writer.Model(&models.HistoryMigration{}).
			Where("id = ?", models.HistoryMigrationSingletonID).Update("state", "completed").Error)
		mainBefore, err := os.ReadFile(path)
		require.NoError(t, err)
		before := requireNonemptyWAL(t, path)

		migration, found, err := NewConnector().ReadHistoryMigrationMarker(path)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "completed", migration.State)
		after, err := os.ReadFile(path + "-wal")
		require.NoError(t, err)
		require.True(t, bytes.Equal(before, after))
		mainAfter, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, bytes.Equal(mainBefore, mainAfter))
	})
}

func TestConnectorFileURIUsesFilesystemDirectory(t *testing.T) {
	workingDir := t.TempDir()
	t.Chdir(workingDir)
	target := filepath.Join(t.TempDir(), "nested", "core db.sqlite")
	dsn := (&url.URL{Scheme: "file", Path: target}).String()

	db, err := NewConnector().OpenCore(dsn)
	if err != nil {
		t.Fatalf("OpenCore: %v", err)
	}
	closeDatabase(t, db)
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Fatalf("target directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workingDir, "file:")); !os.IsNotExist(err) {
		t.Fatalf("connector created a literal file: directory: %v", err)
	}
}

func TestConnectorPathOpenersPreserveReservedCharacters(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core db?#%.sqlite")
	db, err := NewConnector().OpenCorePath(path)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE path_identity (value TEXT)").Error)
	require.NoError(t, db.Exec("INSERT INTO path_identity(value) VALUES (?)", "exact").Error)
	closeDatabase(t, db)
	require.FileExists(t, path)
	require.NoFileExists(t, strings.Split(path, "?")[0])

	db, err = NewConnector().OpenExistingCorePath(path)
	require.NoError(t, err)
	defer closeDatabase(t, db)
	var value string
	require.NoError(t, db.Raw("SELECT value FROM path_identity").Scan(&value).Error)
	require.Equal(t, "exact", value)
}

func TestConnectorDSNAndFilesystemPathOpenersRemainDistinct(t *testing.T) {
	path := filepath.Join(t.TempDir(), "configured core.sqlite")
	dsn := (&url.URL{Scheme: "file", Path: path, RawQuery: "mode=rwc&cache=private"}).String()
	db, err := NewConnector().OpenCore(dsn)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE configured_dsn (value INTEGER)").Error)
	closeDatabase(t, db)

	db, err = NewConnector().OpenExistingCorePath(path)
	require.NoError(t, err)
	defer closeDatabase(t, db)
	require.True(t, db.Migrator().HasTable("configured_dsn"))
}

func TestConnectorDoesNotTreatMemorySubstringsAsMemory(t *testing.T) {
	for _, query := range []string{"label=mode=memory"} {
		t.Run(query, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "disk.db")
			db, err := NewConnector().OpenCore("file:" + path + "?" + query)
			if err != nil {
				t.Fatalf("OpenCore: %v", err)
			}
			closeDatabase(t, db)
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("disk database was not created: %v", err)
			}
		})
	}
}

func TestConnectorOverridesConflictingPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "core.db")
	dsn := fmt.Sprintf("%s?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(1)&_pragma=foreign_keys(0)", path)
	db, err := NewConnector().OpenCore(dsn)
	if err != nil {
		t.Fatalf("OpenCore: %v", err)
	}
	closeDatabase(t, db)

	var journalMode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
		t.Fatalf("journal mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Fatalf("journal mode = %q, want wal", journalMode)
	}
	var busyTimeout int
	if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
		t.Fatalf("busy timeout: %v", err)
	}
	if busyTimeout != 30000 {
		t.Fatalf("busy timeout = %d, want 30000", busyTimeout)
	}
	var foreignKeys int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
		t.Fatalf("foreign keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign keys = %d, want 1", foreignKeys)
	}
}

func TestConnectorOpensIndependentDatabases(t *testing.T) {
	connector := NewConnector()
	dir := t.TempDir()
	coreDB, err := connector.OpenCore(filepath.Join(dir, "core.db"))
	if err != nil {
		t.Fatalf("OpenCore: %v", err)
	}
	closeDatabase(t, coreDB)
	logDB, err := connector.OpenLog(filepath.Join(dir, "log.db"))
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	closeDatabase(t, logDB)

	if err := coreDB.Exec("CREATE TABLE core_only (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create core table: %v", err)
	}
	if logDB.Migrator().HasTable("core_only") {
		t.Fatal("log database unexpectedly sees core database table")
	}
	for name, db := range map[string]*gorm.DB{"core": coreDB, "log": logDB} {
		var journalMode string
		if err := db.Raw("PRAGMA journal_mode").Scan(&journalMode).Error; err != nil {
			t.Fatalf("%s journal mode: %v", name, err)
		}
		if strings.ToLower(journalMode) != "wal" {
			t.Fatalf("%s journal mode = %q, want wal", name, journalMode)
		}
		var busyTimeout int
		if err := db.Raw("PRAGMA busy_timeout").Scan(&busyTimeout).Error; err != nil {
			t.Fatalf("%s busy timeout: %v", name, err)
		}
		if busyTimeout != 30000 {
			t.Fatalf("%s busy timeout = %d, want 30000", name, busyTimeout)
		}
		var foreignKeys int
		if err := db.Raw("PRAGMA foreign_keys").Scan(&foreignKeys).Error; err != nil {
			t.Fatalf("%s foreign keys: %v", name, err)
		}
		if foreignKeys != 1 {
			t.Fatalf("%s foreign keys = %d, want 1", name, foreignKeys)
		}
	}
	if err := CheckIntegrity(coreDB); err != nil {
		t.Fatalf("core CheckIntegrity: %v", err)
	}
	if err := CheckIntegrity(logDB); err != nil {
		t.Fatalf("log CheckIntegrity: %v", err)
	}
}

func TestConnectorMemoryDatabasesAreIsolated(t *testing.T) {
	connector := NewConnector()
	coreDB, err := connector.OpenCore(":memory:")
	if err != nil {
		t.Fatalf("OpenCore: %v", err)
	}
	closeDatabase(t, coreDB)
	logDB, err := connector.OpenLog(":memory:")
	if err != nil {
		t.Fatalf("OpenLog: %v", err)
	}
	closeDatabase(t, logDB)

	if err := coreDB.Exec("CREATE TABLE memory_core_only (id INTEGER PRIMARY KEY)").Error; err != nil {
		t.Fatalf("create core memory table: %v", err)
	}
	if logDB.Migrator().HasTable("memory_core_only") {
		t.Fatal("log memory database unexpectedly sees core table")
	}

	coreSQL, err := coreDB.DB()
	if err != nil {
		t.Fatalf("core sql db: %v", err)
	}
	logSQL, err := logDB.DB()
	if err != nil {
		t.Fatalf("log sql db: %v", err)
	}
	if coreSQL.Stats().MaxOpenConnections != 1 || logSQL.Stats().MaxOpenConnections != 1 {
		t.Fatalf("memory max open connections = core:%d log:%d, want both 1", coreSQL.Stats().MaxOpenConnections, logSQL.Stats().MaxOpenConnections)
	}
}

func TestConnectorRejectsInvalidCorePath(t *testing.T) {
	connector := NewConnector()
	_, err := connector.OpenCore(t.TempDir())
	if err == nil {
		t.Fatal("expected directory path to fail as a SQLite database")
	}
}

func TestSQLiteDSNOnlyAddsMissingPragmas(t *testing.T) {
	parsed, err := ParseSQLiteDSN("./core.db?cache=shared&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("ParseSQLiteDSN: %v", err)
	}
	if parsed.Memory {
		t.Fatal("file database was classified as memory")
	}
	dsn := parsed.connectionDSN("core")
	reparsed, err := ParseSQLiteDSN(dsn)
	if err != nil {
		t.Fatalf("reparse connection DSN: %v", err)
	}
	if got := countPragma(reparsed.Query, "journal_mode"); got != 1 {
		t.Fatalf("journal_mode pragma count = %d, want 1 in %q", got, dsn)
	}
	if got := countPragma(reparsed.Query, "busy_timeout"); got != 1 {
		t.Fatalf("missing busy_timeout pragma in %q", dsn)
	}
	if got := countPragma(reparsed.Query, "foreign_keys"); got != 1 {
		t.Fatalf("missing foreign_keys pragma in %q", dsn)
	}
}

func countPragma(query url.Values, name string) int {
	count := 0
	for key, values := range query {
		if !strings.EqualFold(key, "_pragma") {
			continue
		}
		for _, value := range values {
			if strings.HasPrefix(strings.ToLower(value), name+"(") {
				count++
			}
		}
	}
	return count
}

func closeDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
}

func openWALMarkerWriter(t *testing.T) (string, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "core.db")
	db, err := NewConnector().OpenCore(path)
	require.NoError(t, err)
	closeDatabase(t, db)
	require.NoError(t, models.MigrateCoreDB(db))
	require.NoError(t, db.Exec("PRAGMA wal_autocheckpoint = 0").Error)
	checkpointWAL(t, db)
	return path, db
}

func checkpointWAL(t *testing.T, db *gorm.DB) {
	t.Helper()
	require.NoError(t, db.Exec("PRAGMA wal_checkpoint(TRUNCATE)").Error)
}

func requireNonemptyWAL(t *testing.T, path string) []byte {
	t.Helper()
	wal, err := os.ReadFile(path + "-wal")
	require.NoError(t, err)
	require.NotEmpty(t, wal)
	return wal
}
