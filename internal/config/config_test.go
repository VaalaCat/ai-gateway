package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func writeTempYAML(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp yaml: %v", err)
	}
	return path
}

func writeValidCredsJSON(t *testing.T, raw string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "agent_credentials.json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("invalid credentials json fixture: %v", err)
	}
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write credentials json: %v", err)
	}
	return path
}

func TestLoad_Defaults(t *testing.T) {
	cfg, err := Load("nonexistent-file-that-does-not-exist.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":8140" {
		t.Errorf("expected default listen :8140, got %s", cfg.Listen)
	}
	if cfg.Master.JWTSecret != "" {
		t.Errorf("expected empty jwt_secret without explicit config, got %q", cfg.Master.JWTSecret)
	}
	if cfg.Agent.Listen != ":8139" {
		t.Errorf("expected default agent listen :8139, got %s", cfg.Agent.Listen)
	}
	if cfg.Relay.Timeout != 300 {
		t.Errorf("expected default timeout 300, got %d", cfg.Relay.Timeout)
	}
	if cfg.Master.DBPath != "./data/core.db?_pragma=journal_mode(WAL)" {
		t.Errorf("expected default db_path with WAL pragma, got %s", cfg.Master.DBPath)
	}
	if cfg.Agent.Cache.TokenCapacity != 20000 {
		t.Errorf("expected default token_capacity 20000, got %d", cfg.Agent.Cache.TokenCapacity)
	}
	if cfg.Agent.Cache.UserCapacity != 20000 {
		t.Errorf("expected default user_capacity 20000, got %d", cfg.Agent.Cache.UserCapacity)
	}
	if cfg.Agent.Cache.NegativeTTLSeconds != 600 {
		t.Errorf("expected default negative_ttl_seconds 600, got %d", cfg.Agent.Cache.NegativeTTLSeconds)
	}
	if cfg.Agent.Cache.UserRoutingsCapacity != 5000 {
		t.Errorf("expected default user_routings_capacity 5000, got %d", cfg.Agent.Cache.UserRoutingsCapacity)
	}
}

func TestLoad_AgentCacheOverride(t *testing.T) {
	path := writeTempYAML(t, `role: agent
agent:
  cache:
    token_capacity: 5
    user_capacity: 6
    negative_ttl_seconds: 7
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.Cache.TokenCapacity != 5 || cfg.Agent.Cache.UserCapacity != 6 || cfg.Agent.Cache.NegativeTTLSeconds != 7 {
		t.Fatalf("override mismatch: %+v", cfg.Agent.Cache)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.yaml")
	content := []byte("listen: ':9999'\nmaster:\n  db_path: ':memory:'\n  jwt_secret: 'my-secret'\n")
	os.WriteFile(path, content, 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Listen != ":9999" {
		t.Errorf("expected :9999, got %s", cfg.Listen)
	}
	if cfg.Master.JWTSecret != "my-secret" {
		t.Errorf("expected my-secret, got %s", cfg.Master.JWTSecret)
	}
	// Defaults should still apply for unset values
	if cfg.Agent.Listen != ":8139" {
		t.Errorf("expected default agent listen :8139, got %s", cfg.Agent.Listen)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	os.WriteFile(path, []byte("{{invalid yaml content"), 0o644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestLoadMaster_MinimalConfig(t *testing.T) {
	validSecret := strings.Repeat("x", 32)
	path := writeTempYAML(t, fmt.Sprintf(`
master:
  jwt_secret: %s
  admin_password: secure-password-123
  public_base_urls:
    - "http://localhost:8140"
`, validSecret))

	cfg, err := LoadMaster(path)
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	if cfg.Master.Listen != ":8140" {
		t.Fatalf("master.listen = %q, want :8140", cfg.Master.Listen)
	}
	if cfg.Master.JWTSecret != validSecret {
		t.Fatalf("master.jwt_secret = %q, want %q", cfg.Master.JWTSecret, validSecret)
	}
	if cfg.Runtime.RelayTimeout != 300 {
		t.Fatalf("runtime.relay_timeout = %d, want 300", cfg.Runtime.RelayTimeout)
	}
	if cfg.Master.DBPath != "./data/core.db" {
		t.Fatalf("master.db_path = %q, want ./data/core.db", cfg.Master.DBPath)
	}
	if cfg.Master.LogDBPath != "data/log.db" {
		t.Fatalf("master.log_db_path = %q, want data/log.db", cfg.Master.LogDBPath)
	}
	if cfg.Master.LegacyDBPath != "data/master.db" {
		t.Fatalf("master.legacy_db_path = %q, want data/master.db", cfg.Master.LegacyDBPath)
	}
}

func TestLoadMaster_DeprecatedDBPathBecomesLegacyAndDerivesSplitPaths(t *testing.T) {
	validSecret := strings.Repeat("x", 32)
	path := writeTempYAML(t, fmt.Sprintf(`
master:
  db_path: "/data/master.db?cache=shared&_pragma=journal_mode(WAL)"
  jwt_secret: %s
  admin_password: secure-password-123
`, validSecret))

	cfg, err := LoadMaster(path)
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	wantQuery := "?cache=shared&_pragma=journal_mode(WAL)"
	if cfg.Master.DBPath != "/data/core.db"+wantQuery {
		t.Fatalf("master core path = %q, want %q", cfg.Master.DBPath, "/data/core.db"+wantQuery)
	}
	if cfg.Master.LogDBPath != "/data/log.db"+wantQuery {
		t.Fatalf("master.log_db_path = %q, want %q", cfg.Master.LogDBPath, "/data/log.db"+wantQuery)
	}
	if cfg.Master.LegacyDBPath != "/data/master.db"+wantQuery {
		t.Fatalf("master legacy path = %q, want %q", cfg.Master.LegacyDBPath, "/data/master.db"+wantQuery)
	}
}

func TestLoadMaster_ExplicitSplitDatabaseKeysOverrideDeprecatedAlias(t *testing.T) {
	validSecret := strings.Repeat("x", 32)
	path := writeTempYAML(t, fmt.Sprintf(`
master:
  core_db_path: "/core/live.db?cache=private"
  log_db_path: "/logs/events.db?cache=private"
  legacy_db_path: "/archive/master.db?cache=private"
  jwt_secret: %s
  admin_password: secure-password-123
`, validSecret))

	cfg, err := LoadMaster(path)
	if err != nil {
		t.Fatalf("LoadMaster: %v", err)
	}
	if cfg.Master.DBPath != "/core/live.db?cache=private" {
		t.Fatalf("master core path = %q", cfg.Master.DBPath)
	}
	if cfg.Master.LogDBPath != "/logs/events.db?cache=private" {
		t.Fatalf("master log path = %q", cfg.Master.LogDBPath)
	}
	if cfg.Master.LegacyDBPath != "/archive/master.db?cache=private" {
		t.Fatalf("master legacy path = %q", cfg.Master.LegacyDBPath)
	}
}

func TestLoadMaster_RejectsConflictingDeprecatedAndLegacyPaths(t *testing.T) {
	validSecret := strings.Repeat("x", 32)
	path := writeTempYAML(t, fmt.Sprintf(`
master:
  db_path: "/data/old.db"
  legacy_db_path: "/archive/master.db"
  jwt_secret: %s
  admin_password: secure-password-123
`, validSecret))

	_, err := LoadMaster(path)
	if err == nil || !strings.Contains(err.Error(), "master.db_path and master.legacy_db_path") {
		t.Fatalf("LoadMaster error = %v, want deprecated/legacy path conflict", err)
	}
}

func TestMasterDatabasePathsDefaultsFromCorePath(t *testing.T) {
	cfg := MasterConfig{DBPath: "/data/core.db"}

	if err := prepareMasterDatabaseConfig(&cfg); err != nil {
		t.Fatalf("prepareMasterDatabaseConfig: %v", err)
	}
	if cfg.DBPath != "/data/core.db" {
		t.Fatalf("master.db_path = %q, want /data/core.db", cfg.DBPath)
	}
	if cfg.LogDBPath != "/data/log.db" {
		t.Fatalf("master.log_db_path = %q, want /data/log.db", cfg.LogDBPath)
	}
	if cfg.LegacyDBPath != "/data/master.db" {
		t.Fatalf("master.legacy_db_path = %q, want /data/master.db", cfg.LegacyDBPath)
	}
}

func TestMasterDatabasePathsRejectDuplicateFiles(t *testing.T) {
	tests := []struct {
		name string
		cfg  MasterConfig
	}{
		{
			name: "core and log",
			cfg:  MasterConfig{DBPath: "/data/core.db", LogDBPath: "/data/./core.db", LegacyDBPath: "/data/master.db"},
		},
		{
			name: "core and legacy",
			cfg:  MasterConfig{DBPath: "file:/data/core.db?cache=shared", LogDBPath: "/data/log.db", LegacyDBPath: "/data/core.db"},
		},
		{
			name: "log and legacy",
			cfg:  MasterConfig{DBPath: "/data/core.db", LogDBPath: "/data/log.db", LegacyDBPath: "file:/data/log.db?mode=rw"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := prepareMasterDatabaseConfig(&tt.cfg)
			if err == nil || !strings.Contains(err.Error(), "different SQLite files") {
				t.Fatalf("prepareMasterDatabaseConfig error = %v, want different SQLite files", err)
			}
		})
	}
}

func TestMasterDatabasePathsAcceptIndependentMemoryConnections(t *testing.T) {
	cfg := MasterConfig{DBPath: ":memory:", LogDBPath: ":memory:", LegacyDBPath: ":memory:"}

	if err := prepareMasterDatabaseConfig(&cfg); err != nil {
		t.Fatalf("prepareMasterDatabaseConfig: %v", err)
	}
	if cfg.DBPath != ":memory:" || cfg.LogDBPath != ":memory:" || cfg.LegacyDBPath != ":memory:" {
		t.Fatalf("memory paths changed: %+v", cfg)
	}
}

func TestMasterDatabasePathsRejectFileAliases(t *testing.T) {
	dir := t.TempDir()
	corePath := filepath.Join(dir, "core.db")
	if err := os.WriteFile(corePath, []byte("sqlite fixture"), 0o600); err != nil {
		t.Fatalf("write core fixture: %v", err)
	}

	tests := []struct {
		name      string
		makeAlias func(string) error
	}{
		{
			name: "symbolic link",
			makeAlias: func(aliasPath string) error {
				return os.Symlink(corePath, aliasPath)
			},
		},
		{
			name: "hard link",
			makeAlias: func(aliasPath string) error {
				return os.Link(corePath, aliasPath)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			aliasPath := filepath.Join(t.TempDir(), "core-alias.db")
			if err := tt.makeAlias(aliasPath); err != nil {
				t.Skipf("file alias is not supported: %v", err)
			}
			cfg := MasterConfig{
				DBPath:       corePath,
				LogDBPath:    aliasPath,
				LegacyDBPath: filepath.Join(dir, "master.db"),
			}

			err := prepareMasterDatabaseConfig(&cfg)
			if err == nil || !strings.Contains(err.Error(), "different SQLite files") {
				t.Fatalf("prepareMasterDatabaseConfig error = %v, want different SQLite files", err)
			}
		})
	}
}

func TestMasterDatabasePathsRuntimeDefaults(t *testing.T) {
	tests := []struct {
		name       string
		load       func() MasterConfig
		wantCore   string
		wantLog    string
		wantLegacy string
	}{
		{
			name: "combined runtime config",
			load: func() MasterConfig {
				return (&Config{}).ToMasterRuntimeConfig().Master
			},
			wantCore:   "./data/core.db?_pragma=journal_mode(WAL)",
			wantLog:    "data/log.db?_pragma=journal_mode(WAL)",
			wantLegacy: "data/master.db?_pragma=journal_mode(WAL)",
		},
		{
			name: "master runtime config",
			load: func() MasterConfig {
				return (&MasterRuntimeConfig{}).ToMasterRuntimeConfig().Master
			},
			wantCore:   "./data/core.db",
			wantLog:    "data/log.db",
			wantLegacy: "data/master.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.load()
			if got.DBPath != tt.wantCore || got.LogDBPath != tt.wantLog || got.LegacyDBPath != tt.wantLegacy {
				t.Fatalf("database paths = (%q, %q, %q), want (%q, %q, %q)", got.DBPath, got.LogDBPath, got.LegacyDBPath, tt.wantCore, tt.wantLog, tt.wantLegacy)
			}
		})
	}
}

func TestLoadMaster_LogDatabaseConfig(t *testing.T) {
	validSecret := strings.Repeat("x", 32)

	tests := []struct {
		name           string
		databaseYAML   string
		wantLogPath    string
		wantLegacyPath string
	}{
		{
			name:           "default sibling paths preserve core DSN query",
			databaseYAML:   "  core_db_path: ./state/core.db?cache=shared&_pragma=journal_mode(WAL)\n",
			wantLogPath:    "state/log.db?cache=shared&_pragma=journal_mode(WAL)",
			wantLegacyPath: "state/master.db?cache=shared&_pragma=journal_mode(WAL)",
		},
		{
			name:           "explicit independent paths",
			databaseYAML:   "  core_db_path: ./state/core.db\n  log_db_path: ./logs/history.db\n  legacy_db_path: ./legacy/master.db\n",
			wantLogPath:    "./logs/history.db",
			wantLegacyPath: "./legacy/master.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempYAML(t, fmt.Sprintf("master:\n  jwt_secret: %s\n  admin_password: secure-password-123\n%s", validSecret, tt.databaseYAML))
			cfg, err := LoadMaster(path)
			if err != nil {
				t.Fatalf("LoadMaster: %v", err)
			}
			if cfg.Master.LogDBPath != tt.wantLogPath {
				t.Fatalf("master.log_db_path = %q, want %q", cfg.Master.LogDBPath, tt.wantLogPath)
			}
			if cfg.Master.LegacyDBPath != tt.wantLegacyPath {
				t.Fatalf("master.legacy_db_path = %q, want %q", cfg.Master.LegacyDBPath, tt.wantLegacyPath)
			}
		})
	}
}

func TestDefaultLogDBPathMemoryDSNs(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{name: "memory sentinel", dsn: ":memory:", want: ":memory:"},
		{name: "memory URI sentinel", dsn: "file::memory:?cache=shared", want: ":memory:"},
		{name: "named memory URI", dsn: "file:core?mode=memory", want: ":memory:"},
		{name: "mode substring in another value", dsn: "./state/core.db?label=mode=memory", want: "state/log.db?label=mode=memory"},
		{name: "mode value prefix", dsn: "./state/core.db?mode=memory_backup", want: "state/log.db?mode=memory_backup"},
		{name: "percent encoded file URI", dsn: "file:/tmp/state%20dir/core.db?cache=shared", want: "file:/tmp/state%20dir/log.db?cache=shared"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := defaultLogDBPath(tt.dsn)
			if err != nil {
				t.Fatalf("defaultLogDBPath(%q): %v", tt.dsn, err)
			}
			if got != tt.want {
				t.Fatalf("defaultLogDBPath(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

func TestDefaultLogDBPathRejectsEmptySQLiteFileURI(t *testing.T) {
	for _, dsn := range []string{"file:", "file:?cache=shared", "?cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			_, err := defaultLogDBPath(dsn)
			if err == nil || !strings.Contains(err.Error(), "file path is empty") {
				t.Fatalf("defaultLogDBPath(%q) error = %v, want file path is empty", dsn, err)
			}
		})
	}
}

func TestLoadMasterRejectsEmptySQLiteFileURI(t *testing.T) {
	for _, dsn := range []string{"file:", "file:?cache=shared", "?cache=shared"} {
		t.Run(dsn, func(t *testing.T) {
			path := writeTempYAML(t, fmt.Sprintf(`
master:
  core_db_path: %q
  jwt_secret: %s
  admin_password: secure-password-123
`, dsn, strings.Repeat("x", 32)))
			_, err := LoadMaster(path)
			if err == nil {
				t.Fatalf("LoadMaster accepted core_db_path %q", dsn)
			}
			if !strings.Contains(err.Error(), "master.core_db_path") || !strings.Contains(err.Error(), "file path is empty") {
				t.Fatalf("LoadMaster error = %v, want actionable master.core_db_path file path error", err)
			}
		})
	}
}

func TestConfigLoadersRejectExplicitEmptyLogDatabasePaths(t *testing.T) {
	loaders := map[string]func(string) error{
		"Load": func(path string) error {
			_, err := Load(path)
			return err
		},
		"LoadMaster": func(path string) error {
			_, err := LoadMaster(path)
			return err
		},
	}

	for loaderName, load := range loaders {
		for _, dsn := range []string{"file:", "file:?cache=shared", "?cache=shared"} {
			t.Run(loaderName+"/"+dsn, func(t *testing.T) {
				viper.Reset()
				t.Cleanup(viper.Reset)
				path := writeTempYAML(t, fmt.Sprintf(`
master:
  core_db_path: ./data/core.db
  log_db_path: %q
  jwt_secret: %s
  admin_password: secure-password-123
`, dsn, strings.Repeat("x", 32)))
				err := load(path)
				if err == nil {
					t.Fatalf("%s accepted log_db_path %q", loaderName, dsn)
				}
				if !strings.Contains(err.Error(), "master.log_db_path") || !strings.Contains(err.Error(), "SQLite DSN file path is empty") {
					t.Fatalf("%s error = %v, want actionable master.log_db_path error", loaderName, err)
				}
			})
		}
	}
}

func TestConfigLoadersAcceptExplicitMemoryLogDatabasePaths(t *testing.T) {
	loaders := map[string]func(string) (string, error){
		"Load": func(path string) (string, error) {
			cfg, err := Load(path)
			if err != nil {
				return "", err
			}
			return cfg.Master.LogDBPath, nil
		},
		"LoadMaster": func(path string) (string, error) {
			cfg, err := LoadMaster(path)
			if err != nil {
				return "", err
			}
			return cfg.Master.LogDBPath, nil
		},
	}

	for loaderName, load := range loaders {
		for _, dsn := range []string{":memory:", "file::memory:?cache=shared", "file:log?mode=memory"} {
			t.Run(loaderName+"/"+dsn, func(t *testing.T) {
				viper.Reset()
				t.Cleanup(viper.Reset)
				path := writeTempYAML(t, fmt.Sprintf(`
master:
  core_db_path: ./data/core.db
  log_db_path: %q
  jwt_secret: %s
  admin_password: secure-password-123
`, dsn, strings.Repeat("x", 32)))
				got, err := load(path)
				if err != nil {
					t.Fatalf("%s: %v", loaderName, err)
				}
				if got != dsn {
					t.Fatalf("%s log_db_path = %q, want %q", loaderName, got, dsn)
				}
			})
		}
	}
}

func TestLoadMaster_RejectsRemovedTopLevelFields(t *testing.T) {
	path := writeTempYAML(t, `
role: master
listen: ":9000"
master:
  jwt_secret: test-secret
`)

	_, err := LoadMaster(path)
	if err == nil {
		t.Fatal("expected error for removed top-level fields")
	}
	if !strings.Contains(err.Error(), `unknown field "role"`) && !strings.Contains(err.Error(), `unknown field "listen"`) {
		t.Fatalf("expected migration-friendly removed top-level field error, got %v", err)
	}
}

func TestLoadAgent_MinimalEnrollmentConfig(t *testing.T) {
	path := writeTempYAML(t, `
agent:
  master_url: http://127.0.0.1:8140
  enrollment_token: test-token
`)

	cfg, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("LoadAgent: %v", err)
	}
	if cfg.Agent.Listen != ":8139" {
		t.Fatalf("agent.listen = %q, want :8139", cfg.Agent.Listen)
	}
	if cfg.Agent.MasterURL != "http://127.0.0.1:8140" {
		t.Fatalf("agent.master_url = %q, want http://127.0.0.1:8140", cfg.Agent.MasterURL)
	}
}

func TestLoadMaster_RequiresJWTSecret(t *testing.T) {
	path := writeTempYAML(t, `
master:
`)

	_, err := LoadMaster(path)
	if err == nil {
		t.Fatal("expected error when master.jwt_secret is missing")
	}
	if !strings.Contains(err.Error(), "jwt_secret") {
		t.Fatalf("expected jwt_secret error, got %v", err)
	}
}

func TestLoadAgent_RequiresMasterURL(t *testing.T) {
	path := writeTempYAML(t, `
agent:
  enrollment_token: test-token
`)

	_, err := LoadAgent(path)
	if err == nil {
		t.Fatal("expected error when agent.master_url is missing")
	}
	if !strings.Contains(err.Error(), "master_url") {
		t.Fatalf("expected master_url error, got %v", err)
	}
}

func TestLoadAgent_RejectsRemovedSections(t *testing.T) {
	path := writeTempYAML(t, `
agent:
  master_url: http://127.0.0.1:8140
eventbus:
  type: memory
`)

	_, err := LoadAgent(path)
	if err == nil {
		t.Fatal("expected error for removed section")
	}
	if !strings.Contains(err.Error(), `unknown section "eventbus"`) {
		t.Fatalf("expected eventbus rejection, got %v", err)
	}
}

func TestLoadAgent_RequiresEnrollmentTokenWhenCredentialsMissing(t *testing.T) {
	path := writeTempYAML(t, `
agent:
  master_url: http://127.0.0.1:8140
  credentials_file: ./missing-creds.json
`)

	_, err := LoadAgent(path)
	if err == nil {
		t.Fatal("expected error when enrollment_token is missing and credentials are unavailable")
	}
	if !strings.Contains(err.Error(), "enrollment_token") {
		t.Fatalf("expected enrollment_token error, got %v", err)
	}
}

func TestLoadAgent_AllowsMissingEnrollmentTokenWhenCredentialsValid(t *testing.T) {
	credsPath := writeValidCredsJSON(t, `{"agent_id":"a1","secret":"s1"}`)
	path := writeTempYAML(t, fmt.Sprintf(`
agent:
  master_url: http://127.0.0.1:8140
  credentials_file: %q
`, credsPath))

	cfg, err := LoadAgent(path)
	if err != nil {
		t.Fatalf("expected valid agent config, got %v", err)
	}
	if cfg.Agent.CredentialsFile != credsPath {
		t.Fatalf("credentials_file = %q, want %q", cfg.Agent.CredentialsFile, credsPath)
	}
}

func TestMetricsRuntimeConfigPropagation(t *testing.T) {
	want := MetricsConfig{
		Listen: ":9091",
		Token:  strings.Repeat("a", 31) + "=",
	}

	tests := []struct {
		name string
		load func() MetricsConfig
	}{
		{
			name: "master runtime config",
			load: func() MetricsConfig {
				return (&MasterRuntimeConfig{Metrics: want}).ToMasterRuntimeConfig().Metrics
			},
		},
		{
			name: "combined config to master",
			load: func() MetricsConfig {
				return (&Config{Metrics: want}).ToMasterRuntimeConfig().Metrics
			},
		},
		{
			name: "agent runtime config",
			load: func() MetricsConfig {
				return (&AgentRuntimeConfig{Metrics: want}).ToAgentRuntimeConfig().Metrics
			},
		},
		{
			name: "combined config to agent",
			load: func() MetricsConfig {
				return (&Config{Metrics: want}).ToAgentRuntimeConfig().Metrics
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.load(); got != want {
				t.Fatalf("metrics = %+v, want %+v", got, want)
			}
		})
	}
}

func TestLoadMaster_MetricsModes(t *testing.T) {
	validToken := strings.Repeat("A", 30) + "=="
	tests := []struct {
		name        string
		metricsYAML string
		want        MetricsConfig
	}{
		{name: "disabled", want: MetricsConfig{}},
		{
			name:        "shared",
			metricsYAML: fmt.Sprintf("metrics:\n  token: %q\n", validToken),
			want:        MetricsConfig{Token: validToken},
		},
		{
			name:        "independent",
			metricsYAML: fmt.Sprintf("metrics:\n  listen: %q\n  token: %q\n", ":9091", validToken),
			want:        MetricsConfig{Listen: ":9091", Token: validToken},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempYAML(t, fmt.Sprintf(`
master:
  listen: ":8140"
  jwt_secret: %s
  admin_password: secure-password-123
%s`, strings.Repeat("x", 32), tt.metricsYAML))

			cfg, err := LoadMaster(path)
			if err != nil {
				t.Fatalf("LoadMaster: %v", err)
			}
			if cfg.Metrics != tt.want {
				t.Fatalf("metrics = %+v, want %+v", cfg.Metrics, tt.want)
			}
		})
	}
}

func TestLoadAgent_MetricsModes(t *testing.T) {
	validToken := strings.Repeat("z", 32)
	tests := []struct {
		name        string
		metricsYAML string
		want        MetricsConfig
	}{
		{name: "disabled", want: MetricsConfig{}},
		{
			name:        "shared",
			metricsYAML: fmt.Sprintf("metrics:\n  token: %q\n", validToken),
			want:        MetricsConfig{Token: validToken},
		},
		{
			name:        "independent",
			metricsYAML: fmt.Sprintf("metrics:\n  listen: %q\n  token: %q\n", "127.0.0.1:9091", validToken),
			want:        MetricsConfig{Listen: "127.0.0.1:9091", Token: validToken},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeTempYAML(t, fmt.Sprintf(`
agent:
  listen: ":8139"
  master_url: http://127.0.0.1:8140
  enrollment_token: test-token
%s`, tt.metricsYAML))

			cfg, err := LoadAgent(path)
			if err != nil {
				t.Fatalf("LoadAgent: %v", err)
			}
			if cfg.Metrics != tt.want {
				t.Fatalf("metrics = %+v, want %+v", cfg.Metrics, tt.want)
			}
		})
	}
}

func TestValidateMetrics(t *testing.T) {
	validBoundaryToken := "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef"
	tests := []struct {
		name           string
		cfg            MetricsConfig
		businessListen string
		wantField      string
	}{
		{name: "disabled"},
		{name: "shared", cfg: MetricsConfig{Token: validBoundaryToken}},
		{name: "independent", cfg: MetricsConfig{Listen: ":9091", Token: strings.Repeat("a", 30) + "=="}, businessListen: ":8140"},
		{name: "listen without token", cfg: MetricsConfig{Listen: ":9091"}, wantField: "metrics.token"},
		{name: "31 characters", cfg: MetricsConfig{Token: strings.Repeat("a", 31)}, wantField: "metrics.token"},
		{name: "whitespace", cfg: MetricsConfig{Token: strings.Repeat("a", 31) + " "}, wantField: "metrics.token"},
		{name: "control character", cfg: MetricsConfig{Token: strings.Repeat("a", 31) + "\n"}, wantField: "metrics.token"},
		{name: "non ASCII", cfg: MetricsConfig{Token: strings.Repeat("a", 31) + "界"}, wantField: "metrics.token"},
		{name: "padding without body", cfg: MetricsConfig{Token: strings.Repeat("=", 32)}, wantField: "metrics.token"},
		{name: "invalid padding", cfg: MetricsConfig{Token: strings.Repeat("a", 30) + "=a"}, wantField: "metrics.token"},
		{name: "invalid symbol", cfg: MetricsConfig{Token: strings.Repeat("a", 31) + "?"}, wantField: "metrics.token"},
		{name: "same listen", cfg: MetricsConfig{Listen: ":8140", Token: validBoundaryToken}, businessListen: ":8140", wantField: "metrics.listen"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMetrics(tt.cfg, tt.businessListen)
			if tt.wantField == "" {
				if err != nil {
					t.Fatalf("validateMetrics: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantField) {
				t.Fatalf("validateMetrics error = %v, want field %s", err, tt.wantField)
			}
			if tt.cfg.Token != "" && strings.Contains(err.Error(), tt.cfg.Token) {
				t.Fatalf("validateMetrics error leaked token: %v", err)
			}
		})
	}
}

func TestRuntimeValidationRejectsMetricsListenConflict(t *testing.T) {
	validToken := strings.Repeat("a", 32)

	masterErr := validateMaster(&MasterRuntimeConfig{
		Master: MasterConfig{
			Listen:         ":8140",
			JWTSecret:      strings.Repeat("x", 32),
			AdminPassword:  "secure-password",
			PublicBaseURLs: []string{"http://localhost:8140"},
		},
		Metrics: MetricsConfig{Listen: ":8140", Token: validToken},
	})
	if masterErr == nil || !strings.Contains(masterErr.Error(), "metrics.listen") {
		t.Fatalf("validateMaster error = %v, want metrics.listen", masterErr)
	}

	agentErr := validateAgent(&AgentRuntimeConfig{
		Agent: AgentConfig{
			Listen:          ":8139",
			MasterURL:       "http://127.0.0.1:8140",
			EnrollmentToken: "test-token",
		},
		Metrics: MetricsConfig{Listen: ":8139", Token: validToken},
	})
	if agentErr == nil || !strings.Contains(agentErr.Error(), "metrics.listen") {
		t.Fatalf("validateAgent error = %v, want metrics.listen", agentErr)
	}
}

func TestValidateMaster(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if err := validateMaster(nil); err == nil {
			t.Fatal("expected error for nil master runtime config")
		}
	})

	t.Run("missing jwt secret", func(t *testing.T) {
		err := validateMaster(&MasterRuntimeConfig{Master: MasterConfig{}})
		if err == nil {
			t.Fatal("expected error for missing master.jwt_secret")
		}
		if !strings.Contains(err.Error(), "jwt_secret") {
			t.Fatalf("expected jwt_secret error, got %v", err)
		}
	})

	t.Run("valid", func(t *testing.T) {
		err := validateMaster(&MasterRuntimeConfig{Master: MasterConfig{JWTSecret: strings.Repeat("x", 32), AdminPassword: "secure-password", PublicBaseURLs: []string{"http://localhost:8140"}}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateAgent(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		if err := validateAgent(nil); err == nil {
			t.Fatal("expected error for nil agent runtime config")
		}
	})

	t.Run("missing master url", func(t *testing.T) {
		err := validateAgent(&AgentRuntimeConfig{Agent: AgentConfig{EnrollmentToken: "test-token"}})
		if err == nil {
			t.Fatal("expected error for missing agent.master_url")
		}
		if !strings.Contains(err.Error(), "master_url") {
			t.Fatalf("expected master_url error, got %v", err)
		}
	})

	t.Run("missing enrollment token and credentials", func(t *testing.T) {
		err := validateAgent(&AgentRuntimeConfig{Agent: AgentConfig{MasterURL: "http://127.0.0.1:8140"}})
		if err == nil {
			t.Fatal("expected error for missing agent.enrollment_token")
		}
		if !strings.Contains(err.Error(), "enrollment_token") {
			t.Fatalf("expected enrollment_token error, got %v", err)
		}
	})

	t.Run("valid credentials file bypasses enrollment token", func(t *testing.T) {
		credsPath := writeValidCredsJSON(t, `{"agent_id":"a1","secret":"s1"}`)
		err := validateAgent(&AgentRuntimeConfig{Agent: AgentConfig{
			MasterURL:       "http://127.0.0.1:8140",
			CredentialsFile: credsPath,
		}})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestValidateMaster_RejectsInsecureDefaultJWTSecret(t *testing.T) {
	err := validateMaster(&MasterRuntimeConfig{
		Master: MasterConfig{
			JWTSecret:      "change-me", // 故意用 sentinel
			PublicBaseURLs: []string{"http://localhost:8140"},
		},
	})
	if err == nil {
		t.Fatal("expected error rejecting insecure default jwt_secret, got nil")
	}
	if !strings.Contains(err.Error(), "insecure default") {
		t.Errorf("error should mention 'insecure default', got: %v", err)
	}
}

func TestValidateMaster_RejectsShortJWTSecret(t *testing.T) {
	err := validateMaster(&MasterRuntimeConfig{
		Master: MasterConfig{
			JWTSecret:      "shortie", // 7 字节，< 32
			PublicBaseURLs: []string{"http://localhost:8140"},
		},
	})
	if err == nil {
		t.Fatal("expected error rejecting short jwt_secret, got nil")
	}
	if !strings.Contains(err.Error(), "too short") {
		t.Errorf("error should mention 'too short', got: %v", err)
	}
}

func TestValidateMaster_RejectsInsecureDefaultAdminPassword(t *testing.T) {
	err := validateMaster(&MasterRuntimeConfig{
		Master: MasterConfig{
			JWTSecret:      strings.Repeat("x", 32), // 通过 jwt_secret 检查
			AdminPassword:  "admin123",              // sentinel
			PublicBaseURLs: []string{"http://localhost:8140"},
		},
	})
	if err == nil {
		t.Fatal("expected error rejecting insecure default admin_password, got nil")
	}
	if !strings.Contains(err.Error(), "admin_password") {
		t.Errorf("error should mention 'admin_password', got: %v", err)
	}
}

func TestValidateMaster_RejectsEmptyAdminPassword(t *testing.T) {
	err := validateMaster(&MasterRuntimeConfig{
		Master: MasterConfig{
			JWTSecret:      strings.Repeat("x", 32),
			AdminPassword:  "", // 空
			PublicBaseURLs: []string{"http://localhost:8140"},
		},
	})
	if err == nil {
		t.Fatal("expected error rejecting empty admin_password, got nil")
	}
	if !strings.Contains(err.Error(), "admin_password") {
		t.Errorf("error should mention 'admin_password', got: %v", err)
	}
}
