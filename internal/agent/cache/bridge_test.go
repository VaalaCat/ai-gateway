package cache

import (
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"go.uber.org/zap"
)

func TestWSBridgeHandleAutoAddrUpdate(t *testing.T) {
	s := NewStore(nil, config.AgentCacheConfig{})
	s.SetAgent(&models.Agent{
		AgentID:       "agent-c",
		HTTPAddresses: `[{"url":"http://10.0.0.1:8139","tag":"auto-detected"}]`,
	})

	logger, _ := zap.NewDevelopment()
	b := &WSBridge{Store: s, Logger: logger}

	err := b.handleAutoAddrUpdate([]byte(`{"agent_id":"agent-c","http_addresses":[{"url":"http://10.0.0.9:8139","tag":"auto-detected"}]}`))
	if err != nil {
		t.Fatalf("handleAutoAddrUpdate failed: %v", err)
	}

	agent := s.GetAgent("agent-c")
	if agent == nil {
		t.Fatal("expected agent to exist")
	}
	if agent.HTTPAddresses != `[{"url":"http://10.0.0.9:8139","tag":"auto-detected"}]` {
		t.Fatalf("unexpected updated addresses: %s", agent.HTTPAddresses)
	}
}
