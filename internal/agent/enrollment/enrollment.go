package enrollment

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"go.uber.org/zap"
)

type Credentials struct {
	AgentID string `json:"agent_id"`
	Secret  string `json:"secret"`
}

// LoadOrRegister tries to load credentials from file.
// If not found, registers with master using enrollment token.
func LoadOrRegister(cfg *config.AgentConfig, logger *zap.Logger) (*Credentials, error) {
	// Try loading from file
	creds, err := loadFromFile(cfg.CredentialsFile)
	if err == nil {
		logger.Info("loaded agent credentials from file", zap.String("agent_id", creds.AgentID))
		return creds, nil
	}

	// Register with master
	if cfg.EnrollmentToken == "" {
		return nil, fmt.Errorf("no credentials file and no enrollment token configured")
	}

	logger.Info("registering with master", zap.String("master_url", cfg.MasterURL))
	creds, err = register(cfg)
	if err != nil {
		return nil, fmt.Errorf("enrollment failed: %w", err)
	}

	// Save to file
	if err := saveToFile(cfg.CredentialsFile, creds); err != nil {
		logger.Warn("failed to save credentials", zap.Error(err))
	}

	logger.Info("enrolled successfully", zap.String("agent_id", creds.AgentID))
	return creds, nil
}

func loadFromFile(path string) (*Credentials, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, err
	}
	if creds.AgentID == "" || creds.Secret == "" {
		return nil, fmt.Errorf("invalid credentials")
	}
	return &creds, nil
}

func saveToFile(path string, creds *Credentials) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func register(cfg *config.AgentConfig) (*Credentials, error) {
	// Convert ws:// URL to http:// for enrollment API
	enrollURL := cfg.MasterURL
	enrollURL = strings.Replace(enrollURL, "ws://", "http://", 1)
	enrollURL = strings.Replace(enrollURL, "wss://", "https://", 1)
	// Strip path and use /api/agents/enroll
	u, err := url.Parse(enrollURL)
	if err != nil {
		return nil, err
	}
	u.Path = "/api/agents/enroll"

	body, _ := json.Marshal(map[string]string{
		"enrollment_token": cfg.EnrollmentToken,
	})

	resp, err := http.Post(u.String(), consts.ContentTypeJSON, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("enrollment returned %d", resp.StatusCode)
	}

	var creds Credentials
	if err := json.NewDecoder(resp.Body).Decode(&creds); err != nil {
		return nil, err
	}
	return &creds, nil
}
