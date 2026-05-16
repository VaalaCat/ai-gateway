package test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/master"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func TestSync_FullSync(t *testing.T) {
	env := setupFullEnv(t, "sync-full-agent", 1)
	defer env.Close()

	// Create test data via admin API
	userID := env.CreateUserWithQuota("syncuser", 1000)
	env.CreateToken(userID, "sync-token")
	env.CreateChannel("sync-ch", 1, "sk-test", "http://localhost", "gpt-4o")
	env.CreateModelConfig("gpt-4o")

	// Sync from master to agent
	env.SyncFromMaster()

	// LRU 实体（token / user）不参与 FullSync——按需 RPC 加载
	if cc := env.Store.ChannelCount(); cc < 1 {
		t.Errorf("expected at least 1 channel in cache, got %d", cc)
	}

	if mc := env.Store.ModelConfigCount(); mc < 1 {
		t.Errorf("expected at least 1 model config in cache, got %d", mc)
	}
}

func TestSync_IncrementalSync(t *testing.T) {
	env := setupFullEnv(t, "sync-incr-agent", 1)
	defer env.Close()

	// Create initial data and sync
	userID := env.CreateUserWithQuota("incruser", 1000)
	env.CreateToken(userID, "incr-token")
	env.CreateChannel("incr-ch-1", 1, "sk-test", "http://localhost", "gpt-4o")
	env.CreateModelConfig("gpt-4o")
	env.SyncFromMaster()

	initialChannels := env.Store.ChannelCount()

	// Create a new channel via API
	env.CreateChannel("incr-ch-2", 1, "sk-test2", "http://localhost", "gpt-4o-mini")

	// Wait for event propagation
	time.Sleep(500 * time.Millisecond)

	// Re-sync and verify channel count increased
	env.SyncFromMaster()

	newChannels := env.Store.ChannelCount()
	if newChannels <= initialChannels {
		t.Errorf("expected channel count to increase: initial=%d, after=%d", initialChannels, newChannels)
	}
}

func TestSync_EnrollmentFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	logger, _ := zap.NewDevelopment()
	masterCfg := newTestMasterRuntimeConfig(":0")
	srv, err := master.New(masterCfg, logger)
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	masterTS := httptest.NewServer(srv.Router)
	defer masterTS.Close()

	srv.InitAdminUser("admin", "admin123")
	jwt := login(t, masterTS.URL, "admin", "admin123")

	// Generate enrollment token via admin API
	tokenBody, _ := json.Marshal(map[string]any{"ttl": 300})
	tokenReq, _ := http.NewRequest("POST", masterTS.URL+"/api/admin/agents/enrollment-token", bytes.NewReader(tokenBody))
	tokenReq.Header.Set("Content-Type", "application/json")
	tokenReq.Header.Set("Authorization", "Bearer "+jwt)
	tokenResp, err := http.DefaultClient.Do(tokenReq)
	if err != nil {
		t.Fatalf("generate enrollment token: %v", err)
	}
	defer tokenResp.Body.Close()

	if tokenResp.StatusCode != 200 {
		body, _ := io.ReadAll(tokenResp.Body)
		t.Fatalf("enrollment token: expected 200, got %d: %s", tokenResp.StatusCode, body)
	}

	var enrollTokenResp map[string]any
	json.NewDecoder(tokenResp.Body).Decode(&enrollTokenResp)
	enrollToken := enrollTokenResp["enrollment_token"].(string)

	// Enroll agent using that token → 201
	enrollBody, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "enrolled-agent-1"})
	enrollReq, _ := http.NewRequest("POST", masterTS.URL+"/api/agents/enroll", bytes.NewReader(enrollBody))
	enrollReq.Header.Set("Content-Type", "application/json")
	enrollResp, err := http.DefaultClient.Do(enrollReq)
	if err != nil {
		t.Fatalf("enroll agent: %v", err)
	}
	defer enrollResp.Body.Close()

	if enrollResp.StatusCode != 201 {
		body, _ := io.ReadAll(enrollResp.Body)
		t.Fatalf("enroll: expected 201, got %d: %s", enrollResp.StatusCode, body)
	}

	var enrollResult map[string]any
	json.NewDecoder(enrollResp.Body).Decode(&enrollResult)
	agentID := enrollResult["agent_id"].(string)
	agentSecret := enrollResult["secret"].(string)

	// Verify enrolled agent can connect via WebSocket
	wsURL := "ws" + strings.TrimPrefix(masterTS.URL, "http") + "/ws/agent"
	wsHeaders := http.Header{}
	wsHeaders.Set(consts.HeaderXAgentID, agentID)
	wsHeaders.Set(consts.HeaderXAgentSecret, agentSecret)
	client, err := ws.Dial(context.Background(), wsURL, logger, wsHeaders)
	if err != nil {
		t.Fatalf("enrolled agent WebSocket dial: %v", err)
	}

	// Verify we can sync with the connected agent
	agentBus := eventbus.NewMemoryBus()
	store := cache.NewStore(nil, config.AgentCacheConfig{})
	bridge := cache.NewWSBridge(client, store, agentBus, logger)
	bridge.Start()
	syncer := cache.NewSyncer(store, client, agentBus, logger, 5*time.Minute)
	syncer.SubscribeEvents()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := syncer.FullSync(ctx); err != nil {
		t.Fatalf("full sync with enrolled agent: %v", err)
	}
	client.Close()

	// Reuse token → still 201 (multi-use)
	enrollBody2, _ := json.Marshal(map[string]any{"enrollment_token": enrollToken, "name": "enrolled-agent-2"})
	enrollReq2, _ := http.NewRequest("POST", masterTS.URL+"/api/agents/enroll", bytes.NewReader(enrollBody2))
	enrollReq2.Header.Set("Content-Type", "application/json")
	enrollResp2, err := http.DefaultClient.Do(enrollReq2)
	if err != nil {
		t.Fatalf("reuse enrollment token: %v", err)
	}
	enrollResp2.Body.Close()
	if enrollResp2.StatusCode != 201 {
		t.Fatalf("reuse enrollment token: expected 201, got %d", enrollResp2.StatusCode)
	}

	// Invalid token → 401
	invalidBody, _ := json.Marshal(map[string]any{"enrollment_token": "invalid-token", "name": "bad-agent"})
	invalidReq, _ := http.NewRequest("POST", masterTS.URL+"/api/agents/enroll", bytes.NewReader(invalidBody))
	invalidReq.Header.Set("Content-Type", "application/json")
	invalidResp, err := http.DefaultClient.Do(invalidReq)
	if err != nil {
		t.Fatalf("invalid enrollment token: %v", err)
	}
	invalidResp.Body.Close()
	if invalidResp.StatusCode != 401 {
		t.Fatalf("invalid enrollment token: expected 401, got %d", invalidResp.StatusCode)
	}
}
