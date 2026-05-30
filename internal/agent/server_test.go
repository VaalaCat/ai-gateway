package agent

import (
	"context"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestNewEmbedded(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:    ":8139",
			MasterURL: "http://localhost:9999",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	if srv.Creds.AgentID != "test-embedded" {
		t.Errorf("expected agent_id test-embedded, got %s", srv.Creds.AgentID)
	}
	if srv.Store == nil {
		t.Fatal("store is nil")
	}
	if srv.Bus == nil {
		t.Fatal("bus is nil")
	}
	if srv.Router != nil {
		t.Fatal("embedded agent should not have its own router")
	}
}

func TestMountRoutes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:    ":8139",
			MasterURL: "http://localhost:9999",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}

	router := gin.New()
	srv.MountRoutes(router)

	routes := router.Routes()
	if len(routes) == 0 {
		t.Fatal("no routes mounted")
	}

	found := false
	for _, r := range routes {
		if r.Path == "/v1/chat/completions" && r.Method == "POST" {
			found = true
			break
		}
	}
	if !found {
		t.Error("/v1/chat/completions route not found")
	}
}

func TestRunBackground_Cancellation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()

	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			Listen:    ":8139",
			MasterURL: "http://localhost:9999",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    300,
			ReportBufferSize:    100,
			ReportFlushInterval: 5,
			HeartbeatInterval:   30,
		},
	}
	creds := &enrollment.Credentials{AgentID: "test-embedded", Secret: "test-secret"}

	srv, err := NewEmbedded(cfg, logger, creds)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		srv.RunBackground(ctx)
		close(done)
	}()

	time.Sleep(100 * time.Millisecond)

	cancel()
	select {
	case <-done:
		// ok
	case <-time.After(5 * time.Second):
		t.Fatal("RunBackground did not stop after cancel")
	}
}
