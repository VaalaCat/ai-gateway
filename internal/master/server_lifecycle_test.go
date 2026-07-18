package master

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/sourcegraph/conc"
	"go.uber.org/goleak"
	"go.uber.org/zap"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type blockingEmbeddedStartupBus struct {
	app.EventBus
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	topics  map[string]int
}

func TestMasterShutdownCancelsEmbeddedInfiniteDirectSSEBeforeHTTPDrain(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{
			CredentialsFile: filepath.Join(t.TempDir(), "embedded.json"), PreferredAddrTag: "local",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	upstreamStarted := make(chan struct{})
	var upstreamOnce sync.Once
	providerRouter := gin.New()
	providerRouter.POST("/v1/chat/completions", func(c *gin.Context) {
		w, r := c.Writer, c.Request
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: ready\n\n")
		w.(http.Flusher).Flush()
		upstreamOnce.Do(func() { close(upstreamStarted) })
		<-r.Context().Done()
	})
	provider := httptest.NewServer(providerRouter)
	t.Cleanup(provider.Close)
	targetCfg := &config.AgentRuntimeConfig{
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "peer.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30}, Relay: config.RelayConfig{Timeout: 30},
	}
	target, err := agent.NewEmbedded(
		targetCfg, zap.NewNop(), &enrollment.Credentials{AgentID: "peer", Secret: "secret"},
	)
	if err != nil {
		t.Fatal(err)
	}
	target.Store.SetAgent(&models.Agent{AgentID: "peer", Status: consts.StatusEnabled})
	target.Store.SetToken(&models.Token{ID: 1, Key: "lifecycle-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	target.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: provider.URL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	})
	target.Store.RebuildModelIndex()
	target.Store.LoadSettings([]models.Setting{
		{Key: "retry_max_channels", Value: "1"},
		{Key: "max_retries_per_channel", Value: "0"},
		{Key: "breaker_enabled", Value: "0"},
	})
	targetRouter := gin.New()
	targetTransport := mountManagedAttemptTarget(target, targetRouter, func() agentproxy.ForwardAuthSnapshot {
		return agentproxy.ForwardAuthSnapshot{
			Capabilities: []string{protocol.AgentCapabilityForwardV1},
			SigningKeys:  []agentauth.PublicKey{srv.Signer.PublicKey()},
		}
	})
	targetHTTP := httptest.NewServer(targetRouter)
	t.Cleanup(func() {
		targetHTTP.Close()
		targetTransport.CloseIdleConnections()
		shutdownAgentRouteServer(t, target, "lifecycle direct target")
	})
	if err := srv.DB.Create(&models.Agent{
		AgentID: "peer", Name: "peer", Status: consts.StatusEnabled,
		HTTPAddresses: fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, targetHTTP.URL),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := srv.DB.Create(&models.AdminScript{
		Name: "full-sync-barrier", Enabled: true, Code: "function onRequest(c){}",
		Scope: datatypes.NewJSONType(models.ScriptScope{}),
	}).Error; err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: srv.countHTTPHandlers(srv.Router)}
	srv.Listener = listener
	srv.httpSrv = httpServer
	var workers conc.WaitGroup
	workers.Go(func() { _ = httpServer.Serve(listener) })
	if err := srv.SetupEmbeddedAgentForTest(listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	waitForConnectedAgents(t, srv, 1)
	embedded := srv.embeddedAgent
	target.Store.SetAgent(&models.Agent{AgentID: embedded.Creds.AgentID, Status: consts.StatusEnabled})
	cacheDeadline := time.Now().Add(time.Second)
	for (embedded.Store.GetAgent("peer") == nil || embedded.Store.ScriptCount() != 1) && time.Now().Before(cacheDeadline) {
		time.Sleep(time.Millisecond)
	}
	if embedded.Store.GetAgent("peer") == nil || embedded.Store.ScriptCount() != 1 {
		t.Fatal("embedded agent did not complete the initial full sync")
	}
	embedded.Store.SetAgentCapabilities("peer", []string{
		protocol.AgentCapabilityForwardV1,
		protocol.AgentCapabilityDirectIngressV1,
	})
	embedded.Store.SetToken(&models.Token{ID: 1, Key: "lifecycle-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	embedded.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: "http://unused.invalid",
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "unused", Models: "gpt-4o",
	})
	embedded.Store.RebuildModelIndex()
	embedded.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 1, SourceType: "token", SourceID: 1, Model: "gpt-4o",
		AgentID: "peer", Priority: 100,
	})
	if peer := embedded.Store.GetAgent("peer"); peer == nil || peer.Status != consts.StatusEnabled {
		t.Fatalf("peer missing before request: %#v", peer)
	}
	if route := embedded.Store.RouteIndex.FindTokenRoute(1, "gpt-4o"); route == nil || route.AgentID != "peer" {
		t.Fatalf("agent route missing before request: %#v", route)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+listener.Addr().String()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":true}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"lifecycle-token")
	request.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if line, err := bufio.NewReader(response.Body).ReadString('\n'); err != nil {
		t.Fatalf("read first SSE line: status=%d line=%q err=%v", response.StatusCode, line, err)
	}
	<-upstreamStarted
	shutdownResult := make(chan error, 1)
	workers.Go(func() { shutdownResult <- srv.Shutdown(context.Background()) })
	select {
	case err := <-shutdownResult:
		if err != nil {
			t.Fatalf("Shutdown: %v", err)
		}
	case <-time.After(250 * time.Millisecond):
		embedded.CancelDirectForwarding()
		_ = httpServer.Close()
		<-shutdownResult
		t.Fatal("master Shutdown waited for shared HTTP drain before cancelling embedded direct forwarding")
	}
	workers.Wait()
}

func (b *blockingEmbeddedStartupBus) Publish(ctx context.Context, event eventbus.Event) error {
	b.mu.Lock()
	b.topics[event.Topic]++
	b.mu.Unlock()
	return b.EventBus.Publish(ctx, event)
}

func (b *blockingEmbeddedStartupBus) topicCount(topic string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.topics[topic]
}

type failingEmbeddedStartupBus struct {
	app.EventBus
	err     error
	entered chan struct{}
	once    sync.Once
}

type blockingEmbeddedReporterBus struct {
	app.EventBus
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingEmbeddedReporterBus) Subscribe(topic string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	if topic == events.UsageCompletedTopic.Value() {
		b.once.Do(func() { close(b.entered) })
		<-b.release
	}
	return b.EventBus.Subscribe(topic, handler)
}

func (b *failingEmbeddedStartupBus) SubscribePattern(string, eventbus.EventHandler) (eventbus.Subscription, error) {
	b.once.Do(func() { close(b.entered) })
	return nil, b.err
}

func (b *blockingEmbeddedStartupBus) SubscribePattern(pattern string, handler eventbus.EventHandler) (eventbus.Subscription, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return b.EventBus.SubscribePattern(pattern, handler)
}

func TestLifecycleShutdownBeforeRunClosesDone(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := &Server{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown before Run: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
}

func TestLifecycleConcurrentShutdownIsIdempotent(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := &Server{}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var mu sync.Mutex
	var errs []error
	var workers conc.WaitGroup
	for range 8 {
		workers.Go(func() {
			err := srv.Shutdown(ctx)
			mu.Lock()
			errs = append(errs, err)
			mu.Unlock()
		})
	}
	workers.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Shutdown: %v", err)
		}
	}
	requireServerLifecycleDone(t, srv.Done())
}

func TestLifecycleShutdownRejectsNilContext(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := &Server{}
	if err := srv.Shutdown(nil); err == nil {
		t.Fatal("Shutdown(nil) = nil, want error")
	}
}

func TestRegistrationLeaseContextRequiresExplicitContext(t *testing.T) {
	t.Run("nil receiver", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("nil registrationLease.Context did not panic")
			}
		}()
		var lease *registrationLease
		_ = lease.Context()
	})
	t.Run("nil context", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("registrationLease.Context with nil ctx did not panic")
			}
		}()
		_ = (&registrationLease{}).Context()
	})
	t.Run("preserves cancellation cause", func(t *testing.T) {
		cause := errors.New("registration canceled")
		ctx, cancel := context.WithCancelCause(context.Background())
		cancel(cause)
		lease := &registrationLease{ctx: ctx}
		if got := context.Cause(lease.Context()); !errors.Is(got, cause) {
			t.Fatalf("registration context cause = %v, want %v", got, cause)
		}
	})
}

func TestRunAdmissionRejectsConcurrentEmbeddedSetupBeforeMount(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := newLifecycleMasterServer(t)
	routesBefore := len(srv.Router.Routes())
	registrationEntered := make(chan struct{})
	releaseRegistration := make(chan struct{})
	srv.afterRunRegistration = func() {
		close(registrationEntered)
		<-releaseRegistration
	}
	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-registrationEntered:
	case <-time.After(time.Second):
		t.Fatal("Run did not acquire startup admission")
	}

	setupErr := srv.SetupEmbeddedAgentForTest("127.0.0.1:1")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	close(releaseRegistration)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	<-runDone

	if !errors.Is(setupErr, ErrAlreadyRunning) {
		t.Fatalf("SetupEmbeddedAgentForTest during Run = %v, want %v", setupErr, ErrAlreadyRunning)
	}
	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Fatalf("rejected setup changed routes: before=%d after=%d", routesBefore, routesAfter)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestEmbeddedSetupAdmissionRejectsConcurrentSetupBeforeConstruction(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := newLifecycleMasterServer(t)
	routesBefore := len(srv.Router.Routes())
	setupEntered := make(chan struct{})
	releaseSetup := make(chan struct{})
	var setupCalls atomic.Int32
	srv.beforeSetupEmbedded = func() {
		if setupCalls.Add(1) == 1 {
			close(setupEntered)
			<-releaseSetup
		}
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- srv.SetupEmbeddedAgentForTest("127.0.0.1:1") }()
	select {
	case <-setupEntered:
	case <-time.After(time.Second):
		t.Fatal("first embedded setup did not reach construction barrier")
	}
	secondErr := srv.SetupEmbeddedAgentForTest("127.0.0.1:1")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	close(releaseSetup)
	<-firstDone
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if srv.startupLease != nil {
		t.Fatal("joined startup lease remained referenced after Server.Done")
	}
	if !errors.Is(secondErr, ErrAlreadyRunning) {
		t.Fatalf("second embedded setup = %v, want %v", secondErr, ErrAlreadyRunning)
	}
	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Fatalf("concurrent setup changed routes before commit: before=%d after=%d", routesBefore, routesAfter)
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestStartupAdmissionAbortCannotReleaseNewGeneration(t *testing.T) {
	srv := &Server{}
	first, err := srv.beginRegistration()
	if err != nil {
		t.Fatalf("first beginRegistration: %v", err)
	}
	first.Abort()
	second, err := srv.beginRegistration()
	if err != nil {
		t.Fatalf("second beginRegistration: %v", err)
	}
	first.Abort()
	if third, err := srv.beginRegistration(); third != nil || !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("beginRegistration after stale Abort = (%v, %v), want %v", third, err, ErrAlreadyRunning)
	}
	second.Abort()
	select {
	case <-second.done:
	default:
		t.Fatal("registration Abort did not release its completion signal")
	}
}

func TestStartupAdmissionListenFailureAllowsRetry(t *testing.T) {
	srv := newLifecycleMasterServer(t)
	srv.Cfg.Master.Listen = "127.0.0.1:not-a-port"
	if err := srv.Run(); err == nil {
		t.Fatal("Run with invalid listen address returned nil")
	}
	lease, err := srv.beginRegistration()
	if err != nil {
		t.Fatalf("beginRegistration after precommit failure: %v", err)
	}
	lease.Abort()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestCommittedEmbeddedSetupRejectsRestart(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	srv := newLifecycleMasterServer(t)
	httpServer := httptest.NewServer(srv.Router)
	parsed, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	if err := srv.SetupEmbeddedAgentForTest(parsed.Host); err != nil {
		t.Fatalf("first SetupEmbeddedAgentForTest: %v", err)
	}
	if err := srv.SetupEmbeddedAgentForTest(parsed.Host); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second SetupEmbeddedAgentForTest = %v, want %v", err, ErrAlreadyRunning)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	httpServer.Close()
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
}

func TestLifecycleShutdownDrainsHTTPBeforeCancelingRoot(t *testing.T) {
	srv := &Server{}
	root := srv.lifecycleContext()
	entered := make(chan struct{})
	handlerCanceled := make(chan error, 1)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{
		BaseContext: func(net.Listener) context.Context { return root },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(entered)
			<-r.Context().Done()
			handlerCanceled <- context.Cause(r.Context())
		}),
	}
	srv.httpSrv = httpSrv
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); _ = httpSrv.Serve(ln) }()
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String())
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("request did not enter handler")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err = srv.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	select {
	case cancelErr := <-handlerCanceled:
		if !errors.Is(cancelErr, context.DeadlineExceeded) && !errors.Is(cancelErr, context.Canceled) {
			t.Fatalf("handler cancel cause = %v", cancelErr)
		}
	case <-time.After(time.Second):
		t.Fatal("handler remained active after graceful deadline")
	}
	requireServerLifecycleDone(t, srv.Done())
	<-serveDone
	<-requestDone
}

func TestLifecycleShutdownWaitsForRunRegistrationLeaseAndRunAborts(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	leaseEntered := make(chan struct{})
	releaseLease := make(chan struct{})
	shutdownClosedAdmission := make(chan struct{})
	setupCalled := make(chan struct{}, 1)
	srv.afterRunRegistration = func() {
		close(leaseEntered)
		<-releaseLease
	}
	srv.afterShutdownAdmission = func() { close(shutdownClosedAdmission) }
	srv.beforeSetupEmbedded = func() { setupCalled <- struct{}{} }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	<-leaseEntered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	<-shutdownClosedAdmission
	select {
	case <-srv.Done():
		t.Fatal("Server.Done closed while Run registration lease was held")
	default:
	}
	close(releaseLease)
	if err := <-runDone; err == nil {
		t.Fatal("Run committed resources after shutdown admission closed")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case <-setupCalled:
		t.Fatal("setupEmbeddedAgent ran after shutdown admission closed")
	default:
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Fatal("aborted Run left registered resources")
	}
}

func TestRunEmbeddedSetupFailureRollsBackStartupResources(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listenAddr := probe.Addr().String()
	if err := probe.Close(); err != nil {
		t.Fatal(err)
	}
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: listenAddr, DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	defer func() { _ = srv.Shutdown(shutdownCtx) }()

	setupErr := errors.New("embedded agent setup failed")
	if err := srv.DB.Callback().Query().Before("gorm:query").Register("test:fail_embedded_agent_setup", func(tx *gorm.DB) {
		if tx.Statement.Table == "agents" {
			_ = tx.AddError(setupErr)
		}
	}); err != nil {
		t.Fatal(err)
	}

	if err := srv.Run(); !errors.Is(err, setupErr) {
		t.Fatalf("Run = %v, want %v", err, setupErr)
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Fatal("failed Run retained startup resources")
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after failed Run = %+v", got)
	}
	rebound, err := net.Listen("tcp", listenAddr)
	if err != nil {
		t.Fatalf("startup listener remained open: %v", err)
	}
	_ = rebound.Close()
	var one int
	if err := srv.DB.Raw("SELECT 1").Scan(&one).Error; err != nil || one != 1 {
		t.Fatalf("database unavailable after startup rollback: value=%d err=%v", one, err)
	}
	select {
	case <-srv.Done():
		t.Fatal("startup rollback closed Server.Done before explicit Shutdown")
	default:
	}
}

func TestRunDoesNotServeBeforeEmbeddedSyncSubscriptionsReady(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	startupBus := &blockingEmbeddedStartupBus{
		entered: make(chan struct{}), release: make(chan struct{}), topics: make(map[string]int),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(startupBus.release) }) }
	t.Cleanup(release)
	var embedded *agent.Server
	srv.afterEmbeddedConstruct = func(candidate *agent.Server) {
		embedded = candidate
		candidate.Store.SetChannel(&models.Channel{ChannelCore: models.ChannelCore{ID: 987, Name: "stale-before-first-push"}})
		startupBus.EventBus = candidate.Bus
		candidate.Bus = startupBus
	}

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-startupBus.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("embedded Syncer did not reach subscription barrier")
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Error("Master published startup resources before embedded subscriptions became ready")
	}
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Errorf("embedded control connected before phase A readiness: %d", got)
	}

	servedBeforeReady := false
	if srv.Listener != nil {
		client := &http.Client{Timeout: 200 * time.Millisecond}
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			resp, requestErr := client.Get("http://" + srv.Listener.Addr().String() + "/api/system/public-config")
			if resp != nil {
				_ = resp.Body.Close()
			}
			if requestErr == nil {
				servedBeforeReady = true
				break
			}
		}
	}

	release()
	capabilityDeadline := time.Now().Add(5 * time.Second)
	for len(srv.Hub.Capabilities(embeddedAgentID)) == 0 && time.Now().Before(capabilityDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := srv.Hub.Capabilities(embeddedAgentID); len(got) == 0 {
		t.Fatal("embedded authenticated control did not report capabilities")
	}
	push := protocol.SyncPushParams{
		Entity: events.EntityChannel, Action: events.ActionUpdate,
		Data: []byte(`{"id":987,"name":"first-control-push"}`), Version: 910,
	}
	srv.Hub.NotifyAgent(embeddedAgentID, consts.RPCSyncPush, push)
	pushDeadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(pushDeadline) {
		if got := embedded.Store.GetChannel(987); got != nil && got.Name == "first-control-push" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := embedded.Store.GetChannel(987); got == nil || got.Name != "first-control-push" {
		t.Errorf("first authenticated control push was not applied: %+v", got)
	}
	pushTopic := events.SyncPushTopic(events.EntityChannel, events.ActionUpdate).Value()
	if got := startupBus.topicCount(pushTopic); got != 1 {
		t.Errorf("first authenticated control push published %d times, want 1", got)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after Shutdown")
	}
	if servedBeforeReady {
		t.Error("Master served HTTP before embedded Syncer subscriptions became ready")
	}
}

func TestRunEmbeddedSubscriptionErrorDoesNotMountRoutes(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	routesBefore := len(srv.Router.Routes())
	startupErr := errors.New("embedded sync subscription failed")
	startupBus := &failingEmbeddedStartupBus{err: startupErr, entered: make(chan struct{})}
	srv.afterEmbeddedConstruct = func(embedded *agent.Server) {
		startupBus.EventBus = embedded.Bus
		embedded.Bus = startupBus
	}

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-startupBus.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("embedded Syncer did not attempt subscription")
	}

	var runErr error
	runReturned := false
	select {
	case runErr = <-runDone:
		runReturned = true
	case <-time.After(200 * time.Millisecond):
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !runReturned {
		runErr = <-runDone
	}
	if !runReturned || !errors.Is(runErr, startupErr) {
		t.Errorf("Run after embedded subscription error: returned=%v err=%v, want %v", runReturned, runErr, startupErr)
	}
	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Errorf("embedded subscription error mounted routes: before=%d after=%d", routesBefore, routesAfter)
	}
}

func TestRunDoesNotServeBeforeEmbeddedReporterSubscriptionReady(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	startupBus := &blockingEmbeddedReporterBus{entered: make(chan struct{}), release: make(chan struct{})}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(startupBus.release) }) }
	t.Cleanup(release)
	srv.afterEmbeddedConstruct = func(candidate *agent.Server) {
		startupBus.EventBus = candidate.Bus
		candidate.Bus = startupBus
	}
	commitPublished := make(chan struct{})
	srv.beforeRunRelease = func() { close(commitPublished) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-startupBus.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("embedded Reporter did not reach usage subscription barrier")
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Error("Master published startup resources before embedded Reporter subscription became ready")
	}
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Errorf("embedded control connected before Reporter readiness: %d", got)
	}

	release()
	select {
	case <-commitPublished:
	case <-time.After(3 * time.Second):
		t.Fatal("Master did not commit after Reporter became ready")
	}
	if srv.Listener == nil {
		t.Fatal("Master did not publish listener after Reporter became ready")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after Shutdown")
	}
}

func TestRunEmbeddedSubscriptionCancellationCleansCandidateBeforeMount(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	routesBefore := len(srv.Router.Routes())
	startupBus := &blockingEmbeddedStartupBus{
		entered: make(chan struct{}), release: make(chan struct{}), topics: make(map[string]int),
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(startupBus.release) }) }
	t.Cleanup(release)
	srv.afterEmbeddedConstruct = func(candidate *agent.Server) {
		startupBus.EventBus = candidate.Bus
		candidate.Bus = startupBus
	}
	shutdownAdmitted := make(chan struct{})
	srv.afterShutdownAdmission = func() { close(shutdownAdmitted) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-startupBus.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("embedded Syncer did not reach subscription barrier")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	select {
	case <-shutdownAdmitted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not cancel startup admission")
	}
	select {
	case <-srv.Done():
		t.Fatal("Master Done closed while phase A subscription still owned work")
	default:
	}
	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Errorf("canceled phase A mounted routes: before=%d after=%d", routesBefore, routesAfter)
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Error("canceled phase A published Master startup resources")
	}

	release()
	if err := <-runDone; !errors.Is(err, errMasterServerClosing) {
		t.Errorf("Run after canceled embedded phase A = %v, want %v", err, errMasterServerClosing)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Errorf("resources after canceled embedded phase A = %+v", got)
	}
}

func TestRunEmbeddedCommitFailureLeavesRouterAndHandlerUnchanged(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer shutdownCancel()
	defer func() { _ = srv.Shutdown(shutdownCtx) }()

	routesBefore := len(srv.Router.Routes())
	agentStoreBefore := srv.channelHandler.AgentStore
	srv.beforeSetupEmbedded = func() {
		srv.lifecycleMu.Lock()
		srv.closing = true
		srv.lifecycleMu.Unlock()
	}
	if err := srv.Run(); !errors.Is(err, errMasterServerClosing) {
		t.Fatalf("Run = %v, want %v", err, errMasterServerClosing)
	}
	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Fatalf("failed embedded commit mounted routes: before=%d after=%d", routesBefore, routesAfter)
	}
	if srv.channelHandler.AgentStore != agentStoreBefore {
		t.Fatal("failed embedded commit replaced channel handler agent store")
	}
	if srv.Listener != nil || srv.httpSrv != nil || srv.embeddedAgent != nil {
		t.Fatal("failed embedded commit published startup resources")
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after failed embedded commit = %+v", got)
	}
	select {
	case <-srv.Done():
		t.Fatal("startup rollback closed Server.Done before explicit Shutdown")
	default:
	}
}

func TestRunStartupCommitIsAtomicAgainstShutdown(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	routesBefore := len(srv.Router.Routes())
	agentStoreBefore := srv.channelHandler.AgentStore
	beforeRunCommit := make(chan struct{})
	releaseRun := make(chan struct{})
	shutdownAdmitted := make(chan struct{})
	srv.beforeRunCommit = func() {
		close(beforeRunCommit)
		<-releaseRun
	}
	srv.afterShutdownAdmission = func() { close(shutdownAdmitted) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-beforeRunCommit:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not reach startup commit barrier")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	select {
	case <-shutdownAdmitted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not close startup admission")
	}
	close(releaseRun)
	if err := <-runDone; !errors.Is(err, errMasterServerClosing) {
		t.Fatalf("Run error = %v, want %v", err, errMasterServerClosing)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())

	if routesAfter := len(srv.Router.Routes()); routesAfter != routesBefore {
		t.Errorf("failed startup changed router routes: before=%d after=%d", routesBefore, routesAfter)
	}
	if srv.channelHandler.AgentStore != agentStoreBefore {
		t.Error("failed startup changed channel handler AgentStore")
	}
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Errorf("resources after failed startup = %+v", got)
	}
}

func TestRunStartupCommitCancellationBeforeWorkerReleaseConverges(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	commitPublished := make(chan struct{})
	releaseWorkers := make(chan struct{})
	shutdownAdmitted := make(chan struct{})
	srv.beforeRunRelease = func() {
		close(commitPublished)
		<-releaseWorkers
	}
	srv.afterShutdownAdmission = func() { close(shutdownAdmitted) }

	runDone := make(chan error, 1)
	go func() { runDone <- srv.Run() }()
	select {
	case <-commitPublished:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not publish atomic startup commit")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(shutdownCtx) }()
	select {
	case <-shutdownAdmitted:
	case <-time.After(time.Second):
		t.Fatal("Shutdown did not close startup admission")
	}
	close(releaseWorkers)
	if err := <-runDone; err == nil {
		t.Fatal("Run returned nil after concurrent Shutdown")
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after commit cancellation = %+v", got)
	}
}

func TestLifecycleShutdownDeadlineCancelsMasterWorkersBeforeClosingDB(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Setting{}, &models.Channel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Setting{Key: "version", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}

	versionEntered := make(chan struct{}, 1)
	versionCanceled := make(chan error, 1)
	releaseVersion := make(chan struct{})
	defer close(releaseVersion)
	if err := db.Callback().Update().Before("gorm:update").Register("test:block_version_update", func(tx *gorm.DB) {
		if tx.Statement.Table != "settings" {
			return
		}
		select {
		case versionEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			select {
			case versionCanceled <- context.Cause(tx.Statement.Context):
			default:
			}
		case <-releaseVersion:
		}
	}); err != nil {
		t.Fatal(err)
	}

	aggregatorCanceled := make(chan error, 1)
	aggregator := billing.NewAggregator(nil, zap.NewNop(), billing.AggregatorOptions{FlushEvery: time.Hour})
	aggregator.SetFlushContextFns(func(ctx context.Context, _ []dao.TokenDailyRow) error {
		<-ctx.Done()
		cause := context.Cause(ctx)
		aggregatorCanceled <- cause
		return cause
	}, nil, nil, nil)
	aggregator.Submit(&models.UsageLog{UserID: 1, TokenID: 1, OwnerType: "admin", Status: 1, CreatedAt: 1})

	heartbeatCanceled := make(chan error, 1)
	heartbeat := msync.NewHeartbeatTracker(nil, zap.NewNop(), time.Hour)
	heartbeat.SetLastSeenPersistContextFn(func(ctx context.Context, _ map[string]int64) error {
		cause := context.Cause(ctx)
		heartbeatCanceled <- cause
		return cause
	})
	heartbeat.Touch("agent-a", 1)

	rebuildEntered := make(chan struct{})
	rebuildCanceled := make(chan error, 1)
	rebuild := billing.NewRebuildRunner(nil, zap.NewNop(), time.Hour)
	rebuild.SetSliceContextFn(func(ctx context.Context, _ string, _ int, _ []string, _ bool) (*dao.BillingRebuildResult, error) {
		close(rebuildEntered)
		<-ctx.Done()
		cause := context.Cause(ctx)
		rebuildCanceled <- cause
		return nil, cause
	})

	limitEntered := make(chan struct{}, 1)
	limitCanceled := make(chan error, 1)
	releaseLimit := make(chan struct{})
	defer close(releaseLimit)
	if err := db.Callback().Query().Before("gorm:query").Register("test:block_limit_query", func(tx *gorm.DB) {
		if tx.Statement.Table != "channels" {
			return
		}
		select {
		case limitEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			select {
			case limitCanceled <- context.Cause(tx.Statement.Context):
			default:
			}
		case <-releaseLimit:
		}
	}); err != nil {
		t.Fatal(err)
	}
	application := app.NewApplication()
	application.SetDB(db)
	limit := billing.NewLimitEvaluator(application, nil, zap.NewNop(), time.Nanosecond)

	srv := &Server{
		DB: db, Logger: zap.NewNop(), Heartbeat: heartbeat, Aggregator: aggregator,
		RebuildRunner: rebuild, LimitEvaluator: limit,
	}
	root := srv.lifecycleContext()
	srv.Version.Store(1)
	if !srv.startVersionPersistence(root) {
		t.Fatal("startVersionPersistence rejected before shutdown")
	}
	heartbeat.Start(root)
	aggregator.Start(root)
	rebuild.Start(root)
	if _, err := rebuild.Submit(dao.BillingRebuildFilter{StartDate: "2026-01-01", EndDate: "2026-01-01"}); err != nil {
		t.Fatal(err)
	}
	limit.Start()
	<-rebuildEntered
	<-limitEntered

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	for name, canceled := range map[string]<-chan error{
		"aggregator": aggregatorCanceled,
		"heartbeat":  heartbeatCanceled,
		"rebuild":    rebuildCanceled,
		"limit":      limitCanceled,
		"version":    versionCanceled,
	} {
		select {
		case cause := <-canceled:
			if cause == nil {
				t.Fatalf("%s work received nil cancellation cause", name)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s work did not observe shutdown cancellation", name)
		}
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
	if err := sqlDB.Ping(); err == nil {
		t.Fatal("database remained open after Server.Done")
	}
	select {
	case <-versionEntered:
	default:
		t.Fatal("final version update was not attempted before DB close")
	}
}

func TestLifecycleShutdownClosesControlSocketBeforeJoiningHTTPHandlers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Agent{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Agent{AgentID: "agent-a", Secret: "secret", Name: "agent-a", Status: 1}).Error; err != nil {
		t.Fatal(err)
	}
	application := app.NewApplication()
	application.SetDB(db)
	heartbeat := msync.NewHeartbeatTracker(application, zap.NewNop(), 0)
	hub := msync.NewHub(application, zap.NewNop(), nil, func() int64 { return 0 }, nil, msync.HubOptions{})
	hub.Heartbeat = heartbeat
	srv := &Server{DB: db, Hub: hub, Heartbeat: heartbeat}
	root := srv.lifecycleContext()
	router := gin.New()
	router.GET("/ws/agent", hub.HandleWS)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{
		Handler:     srv.countHTTPHandlers(router),
		BaseContext: func(net.Listener) context.Context { return root },
		ConnState:   srv.countAcceptedSockets,
	}
	srv.Listener = ln
	srv.httpSrv = httpSrv
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = httpSrv.Serve(ln)
	}()

	header := http.Header{}
	header.Set(consts.HeaderXAgentID, "agent-a")
	header.Set(consts.HeaderXAgentSecret, "secret")
	conn, _, err := websocket.DefaultDialer.Dial("ws://"+ln.Addr().String()+"/ws/agent", header)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	deadline := time.Now().Add(time.Second)
	for hub.ConnectedAgents() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if hub.ConnectedAgents() != 1 {
		t.Fatal("control session was not installed")
	}
	counts := srv.ResourceCountsForTest()
	if counts.HTTPHandlers != 1 || counts.ControlHandlers != 1 || counts.ControlSockets != 1 || counts.ControlSessions != 1 {
		t.Fatalf("resources with installed control session = %+v", counts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- srv.Shutdown(ctx) }()
	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	_, _, readErr := conn.ReadMessage()
	timedOut := false
	if timeout, ok := readErr.(net.Error); ok && timeout.Timeout() {
		timedOut = true
		_ = conn.Close()
	}
	if readErr == nil {
		_ = conn.Close()
	}
	shutdownErr := <-shutdownDone
	if timedOut {
		t.Fatal("Shutdown waited for HTTP handler before closing its Hub-owned socket")
	}
	if readErr == nil {
		t.Fatal("control socket remained readable during Shutdown")
	}
	if shutdownErr != nil {
		t.Fatalf("Shutdown: %v", shutdownErr)
	}
	requireServerLifecycleDone(t, hub.Done())
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
	<-serveDone
}

func TestLifecycleShutdownDeadlineCancelsHTTPAPIDAOBeforeClosingDB(t *testing.T) {
	cfg := &config.MasterRuntimeConfig{
		Master:  config.MasterConfig{Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32)},
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 30},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := srv.DB.DB()
	if err != nil {
		t.Fatal(err)
	}
	queryEntered := make(chan struct{}, 1)
	queryCanceled := make(chan error, 1)
	releaseQuery := make(chan struct{})
	defer close(releaseQuery)
	if err := srv.DB.Callback().Query().Before("gorm:query").Register("test:block_http_api_query", func(tx *gorm.DB) {
		if tx.Statement.Table != "settings" {
			return
		}
		select {
		case queryEntered <- struct{}{}:
		default:
		}
		select {
		case <-tx.Statement.Context.Done():
			cause := context.Cause(tx.Statement.Context)
			select {
			case queryCanceled <- cause:
			default:
			}
			_ = tx.AddError(cause)
		case <-releaseQuery:
		}
	}); err != nil {
		t.Fatal(err)
	}
	root := srv.lifecycleContext()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpSrv := &http.Server{
		Handler:     srv.countHTTPHandlers(srv.Router),
		BaseContext: func(net.Listener) context.Context { return root },
		ConnState:   srv.countAcceptedSockets,
	}
	srv.Listener = ln
	srv.httpSrv = httpSrv
	serveDone := make(chan struct{})
	go func() {
		defer close(serveDone)
		_ = httpSrv.Serve(ln)
	}()
	requestDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/api/system/public-config")
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()
	<-queryEntered
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown = %v, want deadline exceeded", err)
	}
	select {
	case cause := <-queryCanceled:
		if cause == nil {
			t.Fatal("HTTP API query received nil cancellation cause")
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP API query did not observe shutdown cancellation")
	}
	requireServerLifecycleDone(t, srv.Done())
	if err := sqlDB.Ping(); err == nil {
		t.Fatal("database remained open after Server.Done")
	}
	<-serveDone
	<-requestDone
}

func TestServerShutdownClosesControlAndTunnel(t *testing.T) {
	defer goleak.VerifyNone(t, masterServerLifecycleGoleakOptions()...)
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: ":0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	httpServer := httptest.NewServer(srv.Router)
	parsed, err := url.Parse(httpServer.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	if err := srv.SetupEmbeddedAgentForTest(parsed.Host); err != nil {
		t.Fatalf("setup embedded agent: %v", err)
	}
	waitForConnectedAgents(t, srv, 1)

	ticket, _, err := srv.Signer.SignRelay(embeddedAgentID, 0)
	if err != nil {
		t.Fatalf("sign relay ticket: %v", err)
	}
	header := http.Header{"Authorization": []string{"Bearer " + string(ticket)}}
	relayConn, _, err := websocket.DefaultDialer.Dial(
		"ws"+strings.TrimPrefix(httpServer.URL, "http")+"/ws/agent-relay", header,
	)
	if err != nil {
		t.Fatalf("dial relay websocket: %v", err)
	}
	if err := relayConn.WriteJSON(wire.Hello{Nonce: "lifecycle", DesiredGeneration: 0}); err != nil {
		t.Fatalf("write relay HELLO: %v", err)
	}
	var welcome wire.Welcome
	if err := relayConn.ReadJSON(&welcome); err != nil {
		t.Fatalf("read relay WELCOME: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	requireServerLifecycleDone(t, srv.Done())
	if got := srv.ResourceCountsForTest(); got != (app.ResourceCounts{}) {
		t.Fatalf("resources after Shutdown = %+v", got)
	}
	requireServerLifecycleDone(t, srv.Hub.Done())
	requireServerLifecycleDone(t, srv.RelayHub.Done())
	if got := srv.Hub.ConnectedAgents(); got != 0 {
		t.Fatalf("connected controls after Shutdown = %d", got)
	}
	_ = relayConn.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := relayConn.ReadMessage(); err == nil {
		t.Fatal("relay peer socket remained open after Shutdown")
	}
	_ = relayConn.Close()
	httpServer.Close()
}

func masterServerLifecycleGoleakOptions() []goleak.Option {
	return []goleak.Option{
		goleak.IgnoreTopFunction("github.com/bytedance/gopkg/cache/asynccache.(*sharedTicker).tick"),
	}
}

func newLifecycleMasterServer(t *testing.T) *Server {
	t.Helper()
	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen: "127.0.0.1:0", DBPath: ":memory:", JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "embedded.json")},
		Runtime: config.RuntimeConfig{
			RelayTimeout: 30, FullSyncInterval: 3600, HeartbeatInterval: 3600,
			ReportBufferSize: 8, ReportFlushInterval: 3600,
		},
	}
	srv, err := New(cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return srv
}

func requireServerLifecycleDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Server.Done did not close")
	}
}
