package master

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	masterdatabase "github.com/VaalaCat/ai-gateway/internal/master/database"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestNewKeepsCoreReadyWhenLogOpenFails(t *testing.T) {
	originalOpenCore := openCoreDatabase
	originalOpenLog := openLogDatabase
	t.Cleanup(func() {
		openCoreDatabase = originalOpenCore
		openLogDatabase = originalOpenLog
	})

	openCoreDatabase = func(connector *masterdatabase.Connector, path string) (*gorm.DB, error) {
		return originalOpenCore(connector, path)
	}
	openLogDatabase = func(*masterdatabase.Connector, string) (*gorm.DB, error) {
		return nil, errors.New("forced log open failure")
	}

	dir := t.TempDir()
	srv, err := New(testDatabaseRuntimeConfig(dir), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if srv.App.GetCoreDB() == nil || srv.App.GetLogDB() != nil {
		t.Fatalf("databases = core:%p log:%p, want degraded log", srv.App.GetCoreDB(), srv.App.GetLogDB())
	}
	require.NoError(t, srv.Shutdown(context.Background()))
}

func TestServerShutdownClosesBothDatabasesAndIsIdempotent(t *testing.T) {
	srv, err := New(testDatabaseRuntimeConfig(t.TempDir()), zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	coreSQL, err := srv.App.GetCoreDB().DB()
	if err != nil {
		t.Fatalf("core sql db: %v", err)
	}
	logSQL, err := srv.App.GetLogDB().DB()
	if err != nil {
		t.Fatalf("log sql db: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	firstErr := srv.Shutdown(ctx)
	if firstErr != nil {
		t.Fatalf("first Shutdown: %v", firstErr)
	}
	select {
	case <-srv.Done():
	case <-ctx.Done():
		t.Fatalf("wait Done: %v", context.Cause(ctx))
	}
	secondErr := srv.Shutdown(ctx)
	if !errors.Is(secondErr, firstErr) || secondErr != firstErr {
		t.Fatalf("second Shutdown error = %v, want first result %v", secondErr, firstErr)
	}
	assertSQLDBClosed(t, coreSQL, "core after Shutdown")
	assertSQLDBClosed(t, logSQL, "log after Shutdown")
}

func testDatabaseRuntimeConfig(dir string) *config.MasterRuntimeConfig {
	return &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:       "127.0.0.1:0",
			DBPath:       filepath.Join(dir, "core.db"),
			LogDBPath:    filepath.Join(dir, "log.db"),
			LegacyDBPath: filepath.Join(dir, "master.db"),
			JWTSecret:    strings.Repeat("x", 32),
		},
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(dir, "embedded-agent.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
}

func assertSQLDBClosed(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	if db == nil {
		t.Fatalf("%s was not opened", name)
	}
	if err := db.Ping(); err == nil {
		t.Fatalf("%s Ping succeeded, want closed database", name)
	}
}

func TestNewConnectsDistinctCoreAndLogDatabases(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    "127.0.0.1:0",
			DBPath:    filepath.Join(dir, "core.db"),
			LogDBPath: filepath.Join(dir, "log.db"),
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	closeServerDatabases(t, srv)

	coreDB := srv.App.GetCoreDB()
	logDB := srv.App.GetLogDB()
	if coreDB == nil || logDB == nil {
		t.Fatalf("application databases = core:%p log:%p, want both configured", coreDB, logDB)
	}
	if coreDB == logDB {
		t.Fatal("application core and log databases must be distinct")
	}
	if !coreDB.Migrator().HasTable(&models.User{}) {
		t.Fatal("core database was not migrated")
	}
	if logDB.Migrator().HasTable(&models.User{}) {
		t.Fatal("log database must not receive the legacy schema during Task 1")
	}
}

func TestNewRejectsSameCoreAndLogDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.db")
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			DBPath:    path + "?_pragma=journal_mode(WAL)",
			LogDBPath: filepath.Join(filepath.Dir(path), ".", filepath.Base(path)),
			JWTSecret: strings.Repeat("x", 32),
		},
	}

	_, err := New(cfg, zap.NewNop())
	if err == nil || !strings.Contains(err.Error(), "same SQLite file") {
		t.Fatalf("New error = %v, want same SQLite file", err)
	}
}

func TestNewReturnsCoreAndLogOpenErrors(t *testing.T) {
	tests := []struct {
		name    string
		core    func(string) string
		log     func(string) string
		wantErr string
	}{
		{
			name:    "invalid core",
			core:    func(dir string) string { return dir },
			log:     func(dir string) string { return filepath.Join(dir, "log.db") },
			wantErr: "core",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfg := &config.MasterRuntimeConfig{Master: config.MasterConfig{
				DBPath: tt.core(dir), LogDBPath: tt.log(dir), JWTSecret: strings.Repeat("x", 32),
			}}
			_, err := New(cfg, zap.NewNop())
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.wantErr) {
				t.Fatalf("New error = %v, want field %q", err, tt.wantErr)
			}
		})
	}
}

func closeServerDatabases(t *testing.T, srv *Server) {
	t.Helper()
	for _, db := range []*gorm.DB{srv.App.GetCoreDB(), srv.App.GetLogDB()} {
		if db == nil {
			continue
		}
		sqlDB, err := db.DB()
		if err != nil {
			t.Fatalf("get sql db: %v", err)
		}
		t.Cleanup(func() { _ = sqlDB.Close() })
	}
}

func TestNew_EnablesWALForFileSQLite(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    "127.0.0.1:0",
			DBPath:    filepath.Join(t.TempDir(), "core.db"),
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}

	sqlDB, err := srv.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	defer sqlDB.Close()

	var mode string
	if err := srv.DB.Raw("PRAGMA journal_mode;").Scan(&mode).Error; err != nil {
		t.Fatalf("query journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
}

// TestNew_SetsBusyTimeoutForFileSQLite 验证 New() 组装的 DSN 真的让 sqlite 生效
// busy_timeout(30000):回填 rebuild 等长写事务持锁时,给并发 settle 30s 等锁窗口,
// 而不是 5s 就 SQLITE_BUSY。
func TestNew_SetsBusyTimeoutForFileSQLite(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    "127.0.0.1:0",
			DBPath:    filepath.Join(t.TempDir(), "core.db"),
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}

	sqlDB, err := srv.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	defer sqlDB.Close()

	var timeoutMs int
	if err := srv.DB.Raw("PRAGMA busy_timeout;").Scan(&timeoutMs).Error; err != nil {
		t.Fatalf("query busy_timeout: %v", err)
	}
	if timeoutMs != 30000 {
		t.Fatalf("busy_timeout = %d, want 30000", timeoutMs)
	}
}

func TestNew_SQLiteMemoryUsesSingleConnection(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    "127.0.0.1:0",
			DBPath:    ":memory:",
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}

	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}

	sqlDB, err := srv.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	defer sqlDB.Close()

	if got := sqlDB.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("sqlite memory max open conns = %d, want 1", got)
	}
}
