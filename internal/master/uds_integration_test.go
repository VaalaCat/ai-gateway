package master

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	"go.uber.org/zap"
)

func runMasterOverUnix(t *testing.T, listen string) (*Server, func()) {
	t.Helper()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    listen,
			DBPath:    ":memory:",
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = srv.Run() }()
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
	return srv, cleanup
}

func waitEmbeddedAgent(t *testing.T, srv *Server) {
	t.Helper()
	for i := 0; i < 100; i++ { // ~10s max
		if srv.Hub != nil && srv.Hub.ConnectedAgents() >= 1 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("embedded agent did not connect over unix socket within timeout (ConnectedAgents=%d)", srv.Hub.ConnectedAgents())
}

func pingOverUnix(t *testing.T, listen string) {
	t.Helper()
	client, base := netaddr.SelfClient(listen)
	resp, err := client.Get(base + "/ping")
	if err != nil {
		t.Fatalf("ping over unix: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("ping status = %d", resp.StatusCode)
	}
}

func TestMasterOverUnixSocket_Pathname(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "master.sock")
	listen := "unix:" + sock
	srv, cleanup := runMasterOverUnix(t, listen)
	waitEmbeddedAgent(t, srv)
	pingOverUnix(t, listen)
	cleanup()
	// socket file removed after shutdown
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sock); os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Errorf("socket file not cleaned up after shutdown: %v", err)
	}
}

func TestMasterOverUnixSocket_Abstract(t *testing.T) {
	listen := fmt.Sprintf("unix:@aigw-uds-test-%d", os.Getpid())
	srv, cleanup := runMasterOverUnix(t, listen)
	defer cleanup()
	waitEmbeddedAgent(t, srv)
	pingOverUnix(t, listen)
}
