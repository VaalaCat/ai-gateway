package master

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"go.uber.org/zap"
)

func TestWithSQLiteWAL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "append pragma to plain file path",
			in:   "./data/master.db",
			want: "./data/master.db?_pragma=journal_mode(WAL)",
		},
		{
			name: "append pragma to dsn with query",
			in:   "./data/master.db?cache=shared",
			want: "./data/master.db?cache=shared&_pragma=journal_mode(WAL)",
		},
		{
			name: "keep memory dsn unchanged",
			in:   ":memory:",
			want: ":memory:",
		},
		{
			name: "keep memory mode dsn unchanged",
			in:   "file:master.db?mode=memory&cache=shared",
			want: "file:master.db?mode=memory&cache=shared",
		},
		{
			name: "do not append duplicate wal pragma",
			in:   "./data/master.db?_pragma=journal_mode(WAL)",
			want: "./data/master.db?_pragma=journal_mode(WAL)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withSQLiteWAL(tt.in)
			if got != tt.want {
				t.Fatalf("withSQLiteWAL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestWithSQLiteBusyTimeout(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "append pragma to plain file path",
			in:   "./data/master.db",
			want: "./data/master.db?_pragma=busy_timeout(30000)",
		},
		{
			name: "append pragma to dsn with query",
			in:   "./data/master.db?cache=shared",
			want: "./data/master.db?cache=shared&_pragma=busy_timeout(30000)",
		},
		{
			name: "keep memory dsn unchanged",
			in:   ":memory:",
			want: ":memory:",
		},
		{
			name: "keep memory mode dsn unchanged",
			in:   "file:master.db?mode=memory&cache=shared",
			want: "file:master.db?mode=memory&cache=shared",
		},
		{
			name: "do not append duplicate busy_timeout pragma",
			in:   "./data/master.db?_pragma=busy_timeout(30000)",
			want: "./data/master.db?_pragma=busy_timeout(30000)",
		},
		{
			name: "composes after an existing WAL pragma",
			in:   "./data/master.db?_pragma=journal_mode(WAL)",
			want: "./data/master.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(30000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := withSQLiteBusyTimeout(tt.in)
			if got != tt.want {
				t.Fatalf("withSQLiteBusyTimeout(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNew_EnablesWALForFileSQLite(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    "127.0.0.1:0",
			DBPath:    filepath.Join(t.TempDir(), "master.db"),
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
			DBPath:    filepath.Join(t.TempDir(), "master.db"),
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
