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
