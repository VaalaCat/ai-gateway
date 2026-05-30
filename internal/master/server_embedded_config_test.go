package master

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
)

func TestBuildEmbeddedAgentConfig_PropagatesCache(t *testing.T) {
	mc := &config.MasterRuntimeConfig{
		LogLevel: "info",
		Runtime:  config.RuntimeConfig{RelayTimeout: 300},
		Relay:    config.RelayConfig{Timeout: 300},
		Agent:    config.AgentConfig{Cache: config.AgentCacheConfig{NegativeTTLSeconds: 120}},
	}
	got := buildEmbeddedAgentConfig(mc, ":8140", "127.0.0.1:8140")
	if got.Agent.Cache.NegativeTTLSeconds != 120 {
		t.Fatalf("embedded agent cache not propagated: %d", got.Agent.Cache.NegativeTTLSeconds)
	}
	if got.Agent.MasterURL == "" || got.Agent.Listen == "" {
		t.Fatal("bootstrap fields must be set")
	}
}

func TestBuildEmbeddedAgentConfig_UnixMasterURL(t *testing.T) {
	mc := &config.MasterRuntimeConfig{LogLevel: "info"}
	got := buildEmbeddedAgentConfig(mc, "unix:/tmp/m.sock", "unix:/tmp/m.sock")
	if got.Agent.MasterURL != "unix:/tmp/m.sock" {
		t.Errorf("unix MasterURL = %q, want unix:/tmp/m.sock", got.Agent.MasterURL)
	}
	if got.Agent.Listen != "unix:/tmp/m.sock" {
		t.Errorf("unix Listen = %q", got.Agent.Listen)
	}
}
