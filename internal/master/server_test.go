package master

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

func TestMasterMetricEndpointUsesInjectedRegistry(t *testing.T) {
	registry := prometheus.NewRegistry()
	relayMetrics := pkgmetrics.NewAgentRelayMetrics(registry, registry)
	server := &Server{Router: gin.New(), RelayMetrics: relayMetrics}
	server.setupMetricsRoute()
	relayMetrics.IncRouteTelemetryDropped()

	response := httptest.NewRecorder()
	server.Router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "agent_route_telemetry_dropped_total")
}

type serverSigningKeySource struct {
	key agentauth.PublicKey
}

func (s serverSigningKeySource) LookupKey(keyID string) (ed25519.PublicKey, bool) {
	if keyID != s.key.KeyID {
		return nil, false
	}
	return ed25519.PublicKey(s.key.Key), true
}

func waitForConnectedAgents(t *testing.T, srv *Server, want int) {
	t.Helper()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(5 * time.Millisecond)
	defer poll.Stop()
	for {
		if srv.Hub.ConnectedAgents() == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf("connected agents = %d, want %d", srv.Hub.ConnectedAgents(), want)
		case <-poll.C:
		}
	}
}

func setupConnectedEmbeddedAgent(t *testing.T) *Server {
	t.Helper()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
			DBPath:    ":memory:",
			JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{
			CredentialsFile: filepath.Join(t.TempDir(), "embedded-agent.json"),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	ts := httptest.NewServer(srv.Router)
	t.Cleanup(ts.Close)
	parsed, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	if err := srv.SetupEmbeddedAgentForTest(parsed.Host); err != nil {
		t.Fatalf("setup embedded agent: %v", err)
	}
	waitForConnectedAgents(t, srv, 1)
	return srv
}

type masterShutdownOutcome struct {
	err        error
	panicValue any
}

func beginMasterShutdown(srv *Server, start <-chan struct{}) <-chan masterShutdownOutcome {
	done := make(chan masterShutdownOutcome, 1)
	go func() {
		<-start
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		outcome := masterShutdownOutcome{}
		defer func() {
			cancel()
			outcome.panicValue = recover()
			done <- outcome
		}()
		outcome.err = srv.Shutdown(ctx)
	}()
	return done
}

func awaitMasterShutdown(t *testing.T, done <-chan masterShutdownOutcome) masterShutdownOutcome {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case outcome := <-done:
		return outcome
	case <-deadline.C:
		t.Fatal("master Shutdown did not return within its bounded deadline")
		return masterShutdownOutcome{}
	}
}

func shutdownMasterOnce(t *testing.T, srv *Server) masterShutdownOutcome {
	t.Helper()
	start := make(chan struct{})
	done := beginMasterShutdown(srv, start)
	close(start)
	return awaitMasterShutdown(t, done)
}

func requireMasterShutdownOutcome(t *testing.T, outcome masterShutdownOutcome) {
	t.Helper()
	if outcome.panicValue != nil {
		t.Fatalf("master Shutdown panicked: %v", outcome.panicValue)
	}
	if outcome.err != nil {
		t.Fatalf("master Shutdown returned error: %v", outcome.err)
	}
}

func TestEmbeddedAgentShutdownClosesControlSession(t *testing.T) {
	srv := setupConnectedEmbeddedAgent(t)
	if !filepath.IsAbs(srv.Cfg.Agent.CredentialsFile) {
		t.Fatalf("test credentials file = %q, want isolated absolute path", srv.Cfg.Agent.CredentialsFile)
	}

	requireMasterShutdownOutcome(t, shutdownMasterOnce(t, srv))
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Fatalf("Shutdown returned with %d connected agents, want 0", got)
	}
}

func TestEmbeddedAgentShutdownIsSequentiallyIdempotent(t *testing.T) {
	srv := setupConnectedEmbeddedAgent(t)

	requireMasterShutdownOutcome(t, shutdownMasterOnce(t, srv))
	requireMasterShutdownOutcome(t, shutdownMasterOnce(t, srv))
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Fatalf("repeated Shutdown returned with %d connected agents, want 0", got)
	}
}

func TestEmbeddedAgentShutdownIsConcurrentSafe(t *testing.T) {
	srv := setupConnectedEmbeddedAgent(t)
	start := make(chan struct{})
	first := beginMasterShutdown(srv, start)
	second := beginMasterShutdown(srv, start)
	close(start)

	outcomes := []masterShutdownOutcome{
		awaitMasterShutdown(t, first),
		awaitMasterShutdown(t, second),
	}
	for _, outcome := range outcomes {
		requireMasterShutdownOutcome(t, outcome)
	}
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Fatalf("concurrent Shutdown returned with %d connected agents, want 0", got)
	}
}

func TestInstanceIDIsNonEmptyStableAndUsedAsConnectionSnapshotEpoch(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
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
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown master: %v", err)
		}
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close sql db: %v", err)
		}
	})
	if srv.InstanceID == "" {
		t.Fatal("InstanceID must not be empty")
	}
	if srv.Connections == nil {
		t.Fatal("Connections service must be wired during New")
	}
	if srv.Signer == nil {
		t.Fatal("Signer must be wired during New")
	}
	publicKey := srv.Signer.PublicKey()
	if publicKey.KeyID == "" || publicKey.Algorithm != "EdDSA" || len(publicKey.Key) != ed25519.PublicKeySize {
		t.Fatalf("invalid signer public identity: key_id_len=%d algorithm=%q key_len=%d", len(publicKey.KeyID), publicKey.Algorithm, len(publicKey.Key))
	}
	ticket, _, err := srv.Signer.SignRelay("agent-a", 0)
	if err != nil {
		t.Fatalf("sign relay ticket: %v", err)
	}
	verifier := agentauth.NewVerifier(serverSigningKeySource{key: publicKey})
	claims, err := verifier.VerifyRelay(ticket, "agent-a", srv.InstanceID, 0)
	if err != nil {
		t.Fatalf("verify relay ticket: %v", err)
	}
	if claims.MasterInstanceID != srv.InstanceID {
		t.Fatalf("relay master_instance_id = %q, want Server.InstanceID %q", claims.MasterInstanceID, srv.InstanceID)
	}
	welcomeClaims := agentauth.WelcomeProofClaims{
		AgentID:           "agent-a",
		Nonce:             "nonce-a",
		MasterInstanceID:  srv.InstanceID,
		SessionGeneration: 0,
		DesiredGeneration: 0,
	}
	welcomeRaw, err := srv.Signer.SignWelcome(welcomeClaims)
	if err != nil {
		t.Fatalf("sign welcome proof: %v", err)
	}
	if err := verifier.VerifyWelcome(string(welcomeRaw), welcomeClaims); err != nil {
		t.Fatalf("verify welcome proof: %v", err)
	}
	wrongMasterClaims := welcomeClaims
	wrongMasterClaims.MasterInstanceID = "different-master"
	welcomeRaw, err = srv.Signer.SignWelcome(wrongMasterClaims)
	if err == nil {
		t.Fatal("wrong-master welcome proof must fail")
	}
	if welcomeRaw != nil {
		t.Fatal("wrong-master welcome proof must not return token bytes")
	}
	if strings.Contains(err.Error(), srv.InstanceID) || strings.Contains(err.Error(), wrongMasterClaims.MasterInstanceID) {
		t.Fatalf("wrong-master error leaked an instance ID: %v", err)
	}

	first := srv.Connections.Build(models.Agent{AgentID: "agent-a", Status: 1})
	second := srv.Connections.Build(models.Agent{AgentID: "agent-a", Status: 1})
	if first.SnapshotEpoch != srv.InstanceID || second.SnapshotEpoch != srv.InstanceID {
		t.Fatalf("snapshot epochs = %q, %q; want stable server InstanceID %q", first.SnapshotEpoch, second.SnapshotEpoch, srv.InstanceID)
	}
}

func TestMasterRegistersAgentRelayRoute(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master:  config.MasterConfig{Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32)},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown master: %v", err)
		}
		select {
		case <-srv.Operations.Done():
		default:
			t.Error("operation service Done remained open after master shutdown")
		}
	})

	found := false
	for _, route := range srv.Router.Routes() {
		if route.Method == "GET" && route.Path == "/ws/agent-relay" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("GET /ws/agent-relay route is not registered")
	}
}

func TestMasterRegistersAgentOperationRouteAndService(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master:  config.MasterConfig{Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32)},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown master: %v", err)
		}
	})
	if srv.Operations == nil {
		t.Fatal("master operation service is nil")
	}
	want := map[string]bool{
		http.MethodGet + " /api/admin/agents":                             false,
		http.MethodGet + " /api/admin/agents/:id/detail":                  false,
		http.MethodGet + " /api/admin/agents/online":                      false,
		http.MethodGet + " /api/admin/agents/:id/connectivity":            false,
		http.MethodPost + " /api/admin/agents/:id/connectivity":           false,
		http.MethodGet + " /api/admin/agents/:id/connections/targets":     false,
		http.MethodGet + " /api/admin/agents/:id/connections/diagnostics": false,
		http.MethodPost + " /api/admin/agents/:id/operations/:operation":  false,
	}
	for _, route := range srv.Router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := want[key]; ok {
			want[key] = true
		}
	}
	for route, found := range want {
		if !found {
			t.Errorf("route %s is not registered", route)
		}
	}
}

func TestNewForwardTicketBootstrapEnablesSourceCache(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("new master: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Errorf("shutdown master: %v", err)
		}
	})
	require.NoError(t, srv.DB.Create(&models.Agent{
		AgentID: "source-agent", Secret: "source-secret", Name: "source-agent", Status: consts.StatusEnabled,
	}).Error)

	httpServer := httptest.NewServer(srv.Router)
	t.Cleanup(httpServer.Close)
	headers := http.Header{}
	headers.Set(consts.HeaderXAgentID, "source-agent")
	headers.Set(consts.HeaderXAgentSecret, "source-secret")
	client, err := ws.Dial(
		t.Context(),
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/agent",
		zap.NewNop(),
		headers,
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	cache := agentauthcache.NewCache(client, agentauthcache.CacheOptions{})
	require.NoError(t, cache.Run(t.Context()))
	t.Cleanup(func() {
		cache.Close()
		<-cache.Done()
	})
	require.Equal(t, []string{
		protocol.AgentCapabilityForwardV1,
		protocol.AgentCapabilityTunnelV1,
	}, cache.Bootstrap().Capabilities)
	var ticket agentauth.ForwardTicket
	require.Eventually(t, func() bool {
		var cachedErr error
		ticket, cachedErr = cache.CachedForwardTicket()
		return cachedErr == nil
	}, time.Second, time.Millisecond)
	claims, err := agentauth.NewVerifier(serverSigningKeySource{key: srv.Signer.PublicKey()}).VerifyForward(ticket)
	require.NoError(t, err)
	require.Equal(t, "source-agent", claims.SourceAgentID)
}

func TestNewFailsClosedOnCorruptSigningIdentityWithoutLeakingPrivateKey(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "corrupt-signing-key.db")
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatalf("migrate seed db: %v", err)
	}
	privateMarker := bytes.Repeat([]byte("private-marker-"), 5)
	privateMarker = privateMarker[:ed25519.PrivateKeySize]
	one := uint8(1)
	corrupt := models.MasterSigningKey{
		KeyID:      strings.Repeat("a", 64),
		PublicKey:  bytes.Repeat([]byte{1}, ed25519.PublicKeySize-1),
		PrivateKey: privateMarker,
		ActiveSlot: &one,
	}
	if err := db.Create(&corrupt).Error; err != nil {
		t.Fatalf("seed corrupt signing key: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get seed sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close seed sql db: %v", err)
	}

	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
			DBPath:    dbPath,
			JWTSecret: strings.Repeat("x", 32),
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, newErr := New(cfg, zap.NewNop())
	if newErr == nil {
		t.Fatal("New must fail when the persisted active signing identity is corrupt")
	}
	if srv != nil {
		t.Fatal("New must not return a partially initialized Server")
	}
	for _, forbidden := range []string{
		string(privateMarker),
		base64.StdEncoding.EncodeToString(privateMarker),
	} {
		if strings.Contains(newErr.Error(), forbidden) {
			t.Fatalf("New error leaked private signing material: %v", newErr)
		}
	}

	checkDB, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		t.Fatalf("reopen db after failed New: %v", err)
	}
	checkSQLDB, err := checkDB.DB()
	if err != nil {
		t.Fatalf("get reopened sql db: %v", err)
	}
	defer checkSQLDB.Close()
	var rows []models.MasterSigningKey
	if err := checkDB.Find(&rows).Error; err != nil {
		t.Fatalf("load signing rows after failed New: %v", err)
	}
	if len(rows) != 1 || !bytes.Equal(rows[0].PrivateKey, privateMarker) {
		t.Fatalf("failed New replaced corrupt identity: rows=%d", len(rows))
	}
}

func TestGenerateAgentSecret_RandomAndLongEnough(t *testing.T) {
	s1, err := generateAgentSecret()
	if err != nil {
		t.Fatalf("generateAgentSecret: %v", err)
	}
	if len(s1) < 40 {
		t.Errorf("secret too short: len=%d", len(s1))
	}
	s2, err := generateAgentSecret()
	if err != nil {
		t.Fatalf("generateAgentSecret 2: %v", err)
	}
	if s1 == s2 {
		t.Errorf("two consecutive secrets should not be equal")
	}
}

func TestEnsureEmbeddedAgent_FirstStartGeneratesSecret(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	agent, err := ensureEmbeddedAgent(db)
	if err != nil {
		t.Fatalf("ensureEmbeddedAgent: %v", err)
	}
	if agent.AgentID != "embedded" {
		t.Errorf("agent_id = %q, want \"embedded\"", agent.AgentID)
	}
	if len(agent.Secret) < 40 {
		t.Errorf("secret too short: %d", len(agent.Secret))
	}
	if agent.Secret == "embedded-local-secret" {
		t.Errorf("must not use hardcoded secret")
	}
}

func TestEnsureEmbeddedAgent_SecondStartReusesSecret(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}

	a1, _ := ensureEmbeddedAgent(db)
	a2, _ := ensureEmbeddedAgent(db)
	if a1.Secret != a2.Secret {
		t.Errorf("secret changed between calls: %q vs %q", a1.Secret, a2.Secret)
	}
}

func TestSaveVersion_NoOpWhenVersionUnchanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// settings.version 行预先存在（模拟 loadVersion 已经跑过）
	if err := db.Create(&models.Setting{Key: "version", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(5)
	srv.lastSavedVersion.Store(5)

	// 用 GORM session callback 统计 UPDATE 次数
	updates := 0
	if err := db.Callback().Update().Register("test:count", func(tx *gorm.DB) {
		updates++
	}); err != nil {
		t.Fatal(err)
	}

	srv.saveVersion(context.Background())
	srv.saveVersion(context.Background())
	srv.saveVersion(context.Background())

	if updates != 0 {
		t.Errorf("expected 0 UPDATE calls when Version unchanged, got %d", updates)
	}
}

func TestSaveVersion_WritesWhenVersionChanged(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Setting{Key: "version", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(42)
	srv.lastSavedVersion.Store(0)

	srv.saveVersion(context.Background())

	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("read settings.version: %v", err)
	}
	if got.Value != "42" {
		t.Errorf("settings.version = %q, want \"42\"", got.Value)
	}
	if v := srv.lastSavedVersion.Load(); v != 42 {
		t.Errorf("lastSavedVersion = %d, want 42", v)
	}
}

func TestSaveVersion_FailureDoesNotAdvanceLastSaved(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// 故意把 settings 表删掉，让 UPDATE 失败
	if err := db.Migrator().DropTable(&models.Setting{}); err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.Version.Store(10)
	srv.lastSavedVersion.Store(5)

	srv.saveVersion(context.Background())

	if v := srv.lastSavedVersion.Load(); v != 5 {
		t.Errorf("lastSavedVersion advanced to %d on failure, want stay at 5", v)
	}
}

func TestLoadVersion_EnsuresPlaceholderRowAndAligns(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// settings.version 行**不**预先存在 — 测试首次启动场景

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.loadVersion()

	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("settings.version row missing after loadVersion: %v", err)
	}
	if got.Value != "0" {
		t.Errorf("placeholder value = %q, want \"0\"", got.Value)
	}
	if v := srv.lastSavedVersion.Load(); v != srv.Version.Load() {
		t.Errorf("lastSavedVersion = %d, Version = %d, want equal after loadVersion", v, srv.Version.Load())
	}
}

func TestLoadVersion_PreservesExistingValue(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := models.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	// 预先插入非默认 value（模拟之前已经持久化过 Version=42）
	if err := db.Create(&models.Setting{Key: "version", Value: "42"}).Error; err != nil {
		t.Fatal(err)
	}

	srv := &Server{DB: db, Logger: zap.NewNop()}
	srv.loadVersion()

	// row 仍然是 42，不能被 placeholder 覆写
	var got models.Setting
	if err := db.Where("key = ?", "version").First(&got).Error; err != nil {
		t.Fatalf("read settings.version: %v", err)
	}
	if got.Value != "42" {
		t.Errorf("settings.version clobbered to %q, want \"42\"", got.Value)
	}
	if v := srv.Version.Load(); v != 42 {
		t.Errorf("Version = %d, want 42", v)
	}
	if v := srv.lastSavedVersion.Load(); v != 42 {
		t.Errorf("lastSavedVersion = %d, want 42", v)
	}
}
