package enrollment

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"go.uber.org/zap"
)

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "creds.json")

	// Write test credentials
	creds := Credentials{AgentID: "test-agent", Secret: "test-secret"}
	data, _ := json.Marshal(creds)
	os.WriteFile(path, data, 0o600)

	loaded, err := loadFromFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.AgentID != "test-agent" {
		t.Errorf("agent_id = %s, want test-agent", loaded.AgentID)
	}
}

func TestRegister(t *testing.T) {
	// Mock master enrollment endpoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agents/enroll" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(Credentials{AgentID: "new-agent", Secret: "new-secret"})
	}))
	defer srv.Close()

	logger, _ := zap.NewDevelopment()
	dir := t.TempDir()
	cfg := &config.AgentConfig{
		MasterURL:       "http://" + srv.Listener.Addr().String(),
		EnrollmentToken: "test-token",
		CredentialsFile: filepath.Join(dir, "creds.json"),
	}

	creds, err := LoadOrRegister(cfg, logger)
	if err != nil {
		t.Fatal(err)
	}
	if creds.AgentID != "new-agent" {
		t.Errorf("agent_id = %s, want new-agent", creds.AgentID)
	}

	// Verify saved to file
	loaded, err := loadFromFile(cfg.CredentialsFile)
	if err != nil {
		t.Fatal("credentials not saved:", err)
	}
	if loaded.AgentID != "new-agent" {
		t.Error("saved credentials mismatch")
	}
}

func TestLoadOrRegister_UsesExistingCredentialsWithoutEnrollmentToken(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "creds.json")

	data, _ := json.Marshal(Credentials{AgentID: "saved-agent", Secret: "saved-secret"})
	if err := os.WriteFile(credsPath, data, 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	cfg := &config.AgentConfig{
		MasterURL:       "http://127.0.0.1:8140",
		CredentialsFile: credsPath,
	}

	creds, err := LoadOrRegister(cfg, logger)
	if err != nil {
		t.Fatalf("LoadOrRegister: %v", err)
	}
	if creds.AgentID != "saved-agent" {
		t.Fatalf("agent_id = %s, want saved-agent", creds.AgentID)
	}
}
