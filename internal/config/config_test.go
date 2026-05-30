package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	if cfg.Master.DBPath != "./data/master.db?_pragma=journal_mode(WAL)" {
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
