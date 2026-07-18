package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	agentattemptproxy "github.com/VaalaCat/ai-gateway/internal/agent/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	bodypkg "github.com/VaalaCat/ai-gateway/internal/agent/body"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agentrelay "github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	agentrelaylegacy "github.com/VaalaCat/ai-gateway/internal/agent/relay/legacy"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	relayexec "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
	agentroute "github.com/VaalaCat/ai-gateway/internal/agent/route"
	"github.com/VaalaCat/ai-gateway/internal/agent/rpc"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/diagnostics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	pkgtunnel "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sourcegraph/conc"
	"github.com/sourcegraph/conc/pool"
	"go.uber.org/zap"
)

var _ app.AgentServer = (*Server)(nil)

var (
	errAgentServerClosing = errors.New("agent server: shutting down")
	ErrAlreadyRunning     = errors.New("agent server: already running")
)

type startupState uint8

const (
	startupIdle startupState = iota
	startupPreparing
	startupRunning
	startupClosing
)

type Server struct {
	Cfg              *config.AgentRuntimeConfig
	Logger           *zap.Logger
	Bus              app.EventBus
	Router           *gin.Engine
	Creds            *enrollment.Credentials
	Store            *cache.Store
	BodyStore        *bodypkg.Store
	Reporter         *reporter.Reporter
	RouteObserver    *agentroute.Observer
	RouteReporter    *agentroute.Reporter
	Syncer           *cache.Syncer
	Listener         net.Listener
	httpSrv          *http.Server
	MetricsRegistry  *prometheus.Registry
	RelayMetrics     *pkgmetrics.AgentRelayMetrics
	RouteSuppressor  *diagnostics.Suppressor
	ownsHTTPListener bool

	Inflight     *inflight.Registry
	Breakers     *resilience.Registry
	LimiterStore *limiter.MemStore
	stopWatchdog func()

	clientMu sync.RWMutex
	client   *ws.Client

	agentAuthCacheMu sync.RWMutex
	agentAuthCache   *agentauthcache.Cache

	TunnelManager *agenttunnel.Manager
	tunnelStateMu sync.RWMutex
	tunnelState   tunnelRuntimeState

	lifecycleOnce        sync.Once
	lifecycleMu          sync.Mutex
	rootCtx              context.Context
	rootCancel           context.CancelCauseFunc
	done                 chan struct{}
	closing              bool
	startupState         startupState
	startupGeneration    uint64
	startupLease         *startupLease
	shutdownErr          error
	shutdownOnce         sync.Once
	workers              conc.WaitGroup
	transportPool        app.TransportPool
	directForwarder      *agentproxy.DirectForwarder
	directGate           *agentroute.DirectGate
	directProber         *rpc.DirectProber
	relayProber          *rpc.RelayProber
	legacyTransportOwner *agentrelaylegacy.TransportOwner
	activeWorkers        atomic.Int64
	httpHandlers         atomic.Int64
	acceptedSockets      atomic.Int64
	watchdogActive       atomic.Int64

	beforeHTTPRegister         func()
	beforeConnectLoop          func()
	beforeStartupFailureReturn func()
	afterRuntimePhaseAReady    func(context.Context)
	afterShutdownSnapshot      func()
	runBackgroundReady         func()
}

type tunnelRuntimeState struct {
	support     string
	config      string
	desired     agenttunnel.Desired
	fingerprint string
}

func New(cfg config.AgentRuntimeProvider, logger *zap.Logger) (*Server, error) {
	runtimeCfg := cfg.ToAgentRuntimeConfig()
	if runtimeCfg == nil {
		return nil, fmt.Errorf("agent runtime config is required")
	}

	creds, err := enrollment.LoadOrRegister(&runtimeCfg.Agent, logger)
	if err != nil {
		return nil, fmt.Errorf("enrollment: %w", err)
	}

	bus := eventbus.NewMemoryBus()
	metricsRegistry := prometheus.NewRegistry()

	s := &Server{
		Cfg:                  runtimeCfg,
		Logger:               logger,
		Bus:                  bus,
		Router:               gin.New(),
		Creds:                creds,
		MetricsRegistry:      metricsRegistry,
		RelayMetrics:         pkgmetrics.NewAgentRelayMetrics(metricsRegistry, metricsRegistry),
		RouteSuppressor:      diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}),
		legacyTransportOwner: agentrelaylegacy.NewTransportOwner(),
		ownsHTTPListener:     true,
	}
	s.initLifecycle()
	if err := s.initRouteObservation(); err != nil {
		return nil, err
	}
	s.directForwarder = agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		ResponseHeaderTimeout: time.Duration(runtimeCfg.Runtime.RelayTimeout) * time.Second,
		OnCircuitTransition:   s.logDirectCircuitTransition,
	})
	s.directGate = agentroute.NewDirectGate(agentroute.DirectGateOptions{})
	s.directProber = rpc.NewDirectProber(rpc.DirectProberOptions{Metrics: s.RelayMetrics})
	// lazyWSClient 将 LRU Loader 的 RPC 调用委托给运行时实际连接，
	// 避免 Store 在连接建立前因持有 nil client 而 Loader 不可用。
	s.Store = cache.NewStore(&lazyWSClient{getClient: s.getClient}, runtimeCfg.Agent.Cache)
	s.Store.SetLogger(s.Logger)
	s.BodyStore, err = newRequestBodyStore(runtimeCfg, s.Logger)
	if err != nil {
		return nil, err
	}
	s.TunnelManager = s.newTunnelManager()
	s.relayProber = s.newRelayProber()

	warnAge := time.Duration(s.Cfg.Runtime.RelayTimeout) * 2 * time.Second
	if warnAge < 60*time.Second {
		warnAge = 60 * time.Second
	}
	s.Inflight = inflight.NewRegistry(s.Logger.Named("inflight"), warnAge)
	s.stopWatchdog = s.Inflight.StartWatchdog(10 * time.Second)
	s.watchdogActive.Store(1)

	s.Router.Use(gin.Recovery(), ginutil.AbortHandlerRecovery())
	s.setupRoutes()

	return s, nil
}

func (s *Server) setupRoutes() {
	s.Router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{
			"status":          "ok",
			"role":            "agent",
			"agent_id":        s.Creds.AgentID,
			"cached_tokens":   s.Store.TokenCount(),
			"cached_channels": s.Store.ChannelCount(),
			"cached_models":   s.Store.ModelConfigCount(),
			"version":         s.Store.Version(),
		})
	})
	s.registerDirectIngressIdentityRoute(s.Router)
	s.setupMetricsRoute()

	runtime := s.buildRelayHandler(time.Duration(s.Cfg.Runtime.RelayTimeout) * time.Second)
	s.registerAttemptProxyRoute(s.Router, runtime.attemptHandler)

	v1 := s.Router.Group("/v1")
	v1.Use(auth.TokenAuth(s.Store))
	v1.GET("/models", agentrelay.ListModels(s.Store))
	v1.POST("/chat/completions", runtime.relayHandler.Relay)
	v1.POST("/completions", runtime.relayHandler.Relay)
	v1.POST("/responses", runtime.relayHandler.Relay)
	v1.POST("/responses/:id", runtime.relayHandler.Relay)
	v1.POST("/messages", runtime.relayHandler.Relay)
	v1.POST("/embeddings", runtime.relayHandler.Relay)
	v1.POST("/images/generations", runtime.relayHandler.Relay)
	v1.POST("/audio/transcriptions", runtime.relayHandler.Relay)
	v1.POST("/audio/translations", runtime.relayHandler.Relay)
	v1.POST("/audio/speech", runtime.relayHandler.Relay)
}

func (s *Server) setupMetricsRoute() {
	if s != nil && s.Router != nil && s.RelayMetrics != nil {
		s.Router.GET("/metrics", gin.WrapH(s.RelayMetrics.Handler()))
	}
}

// NewTunnelTargetHandler binds committed tunnel requests to the same router
// used by direct agent traffic. Session ownership is added by the tunnel
// manager; this factory only supplies target identity, admission, and routing.
func (s *Server) NewTunnelTargetHandler(router http.Handler) *agenttunnel.TargetHandler {
	if router == nil {
		router = s.Router
	}
	agentID := ""
	if s.Creds != nil {
		agentID = s.Creds.AgentID
	}
	return agenttunnel.NewTargetHandler(agentID, func() bool {
		return s.Store != nil && s.Store.Settings().RelayFallbackEnabled == 1
	}, router)
}

func (s *Server) GetRelayLink() agentproxy.RelayLink {
	if s == nil || s.TunnelManager == nil {
		return nil
	}
	return s.TunnelManager
}

func (s *Server) Run() error {
	ctx := s.lifecycleContext()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	startup, err := s.beginStartup()
	if err != nil {
		return err
	}
	defer startup.Abort()

	ln, err := netaddr.Listen(s.Cfg.Agent.Listen)
	if err != nil {
		return err
	}
	resourcesRegistered := false
	defer func() {
		if !resourcesRegistered {
			_ = ln.Close()
		}
	}()

	httpSrv := &http.Server{
		Handler:           s.countHTTPHandlers(s.Router),
		ReadHeaderTimeout: 30 * time.Second, // guard against inbound slowloris
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ConnState:         s.countAcceptedSockets,
	}
	if s.beforeHTTPRegister != nil {
		s.beforeHTTPRegister()
	}
	runtime, err := s.prepareRuntime(ctx, startup, func() {
		s.Listener = ln
		s.httpSrv = httpSrv
	})
	if err != nil {
		return err
	}
	resourcesRegistered = true
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	runtime.commit()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}

	s.Logger.Info("agent listening",
		zap.String("addr", ln.Addr().String()),
		zap.String("agent_id", s.Creds.AgentID),
	)
	return httpSrv.Serve(ln)
}

// NewEmbedded creates an agent server for embedding inside master.
// It skips enrollment and accepts pre-configured credentials.
// The returned server has no Router — use MountRoutes() to attach
// relay routes to an external router.
type EmbeddedOptions struct {
	MetricsRegistry *prometheus.Registry
	RelayMetrics    *pkgmetrics.AgentRelayMetrics
}

func NewEmbedded(cfg config.AgentRuntimeProvider, logger *zap.Logger, creds *enrollment.Credentials, options ...EmbeddedOptions) (*Server, error) {
	runtimeCfg := cfg.ToAgentRuntimeConfig()
	if runtimeCfg == nil {
		return nil, fmt.Errorf("agent runtime config is required")
	}

	bus := eventbus.NewMemoryBus()
	var embedded EmbeddedOptions
	if len(options) > 0 && options[0].MetricsRegistry != nil && options[0].RelayMetrics != nil {
		embedded = options[0]
	}

	s := &Server{
		Cfg:                  runtimeCfg,
		Logger:               logger,
		Bus:                  bus,
		Router:               nil, // embedded agent does not own a router
		Creds:                creds,
		MetricsRegistry:      embedded.MetricsRegistry,
		RelayMetrics:         embedded.RelayMetrics,
		RouteSuppressor:      diagnostics.NewSuppressor(diagnostics.SuppressorOptions{}),
		legacyTransportOwner: agentrelaylegacy.NewTransportOwner(),
	}
	s.initLifecycle()
	if err := s.initRouteObservation(); err != nil {
		return nil, err
	}
	s.directForwarder = agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		ResponseHeaderTimeout: time.Duration(runtimeCfg.Runtime.RelayTimeout) * time.Second,
		OnCircuitTransition:   s.logDirectCircuitTransition,
	})
	s.directGate = agentroute.NewDirectGate(agentroute.DirectGateOptions{})
	s.directProber = rpc.NewDirectProber(rpc.DirectProberOptions{Metrics: s.RelayMetrics})
	// lazyWSClient 将 LRU Loader 的 RPC 调用委托给运行时实际连接。
	s.Store = cache.NewStore(&lazyWSClient{getClient: s.getClient}, runtimeCfg.Agent.Cache)
	s.Store.SetLogger(s.Logger)
	var err error
	s.BodyStore, err = newRequestBodyStore(runtimeCfg, s.Logger)
	if err != nil {
		return nil, err
	}

	warnAge := time.Duration(s.Cfg.Runtime.RelayTimeout) * 2 * time.Second
	if warnAge < 60*time.Second {
		warnAge = 60 * time.Second
	}
	s.Inflight = inflight.NewRegistry(s.Logger.Named("inflight"), warnAge)
	s.stopWatchdog = s.Inflight.StartWatchdog(10 * time.Second)
	s.watchdogActive.Store(1)

	return s, nil
}

func (s *Server) logDirectCircuitTransition(transition agentproxy.DirectCircuitTransition) {
	if s == nil || s.Logger == nil {
		return
	}
	source := ""
	if s.Creds != nil {
		source = s.Creds.AgentID
	}
	s.Logger.Info("direct circuit state changed",
		zap.String("source", source), zap.String("target", transition.TargetAgentID), zap.String("path_kind", "direct"),
		zap.String("stage", "circuit"), zap.String("state", transition.State))
}

// MountRoutes registers /v1/* relay routes on the given router.
// This is used by the embedded agent to share the master's router.
func (s *Server) MountRoutes(router *gin.Engine) {
	s.registerDirectIngressIdentityRoute(router)
	if s.TunnelManager == nil {
		s.TunnelManager = s.newTunnelManagerWithRouter(router)
	}
	if s.relayProber == nil {
		s.relayProber = s.newRelayProber()
	}
	relayTimeout := time.Duration(s.Cfg.Runtime.RelayTimeout) * time.Second
	if relayTimeout == 0 {
		relayTimeout = 300 * time.Second
	}

	runtime := s.buildRelayHandler(relayTimeout)
	s.registerAttemptProxyRoute(router, runtime.attemptHandler)

	v1 := router.Group("/v1")
	v1.Use(auth.TokenAuth(s.Store))
	v1.GET("/models", agentrelay.ListModels(s.Store))
	v1.POST("/chat/completions", runtime.relayHandler.Relay)
	v1.POST("/completions", runtime.relayHandler.Relay)
	v1.POST("/responses", runtime.relayHandler.Relay)
	v1.POST("/responses/:id", runtime.relayHandler.Relay)
	v1.POST("/messages", runtime.relayHandler.Relay)
	v1.POST("/embeddings", runtime.relayHandler.Relay)
	v1.POST("/images/generations", runtime.relayHandler.Relay)
	v1.POST("/audio/transcriptions", runtime.relayHandler.Relay)
	v1.POST("/audio/translations", runtime.relayHandler.Relay)
	v1.POST("/audio/speech", runtime.relayHandler.Relay)
}

func (s *Server) registerAttemptProxyRoute(router *gin.Engine, handler *agentattemptproxy.Handler) {
	if s == nil || router == nil || handler == nil || s.Store == nil {
		return
	}
	router.POST(
		attemptwire.EndpointPath,
		agentattemptproxy.IngressMiddleware(agentattemptproxy.IngressConfig{
			FindAgentByID:    s.Store.GetAgent,
			LoadAuthSnapshot: s.currentForwardAuthSnapshot,
		}),
		auth.TokenAuth(s.Store),
		handler.Serve,
	)
}

func (s *Server) peerRouteMode() string {
	if s == nil || s.Store == nil || s.Creds == nil {
		return consts.PeerRouteModeDirectFirst
	}
	self := s.Store.GetAgent(s.Creds.AgentID)
	if self != nil && self.PeerRouteMode == consts.PeerRouteModeRelayOnly {
		return consts.PeerRouteModeRelayOnly
	}
	return consts.PeerRouteModeDirectFirst
}

func (s *Server) registerDirectIngressIdentityRoute(router *gin.Engine) {
	if s == nil || router == nil || s.Creds == nil || s.Creds.AgentID == "" {
		return
	}
	agentID := s.Creds.AgentID
	router.GET(protocol.DirectIngressIdentityPath, func(c *gin.Context) {
		if c.Query("target_agent_id") != agentID {
			c.Status(http.StatusNotFound)
			return
		}
		c.JSON(http.StatusOK, protocol.DirectIngressIdentity{
			Contract: protocol.DirectIngressContractV1,
			Role:     "agent",
			AgentID:  agentID,
		})
	})
}

// PreparedBackground owns a phase-A-ready embedded runtime. Commit only opens
// the phase-B gate; Cancel and Wait let the embedding server own its lifetime.
type PreparedBackground struct {
	ctx        context.Context
	cancel     context.CancelCauseFunc
	stopParent func() bool
	startup    *preparedRuntime
	commitOnce sync.Once
	finishOnce sync.Once
}

func (p *PreparedBackground) Commit() {
	if p == nil {
		return
	}
	p.commitOnce.Do(func() { p.startup.commit() })
}

func (p *PreparedBackground) Cancel(cause error) {
	if p == nil {
		return
	}
	if cause == nil {
		cause = context.Canceled
	}
	p.cancel(cause)
	p.finish()
}

func (p *PreparedBackground) Wait() {
	if p == nil {
		return
	}
	<-p.ctx.Done()
	p.finish()
}

func (p *PreparedBackground) finish() {
	p.finishOnce.Do(func() {
		p.stopParent()
		p.cancel(context.Canceled)
	})
}

// PrepareBackground completes all fallible subscriptions but keeps tunnel,
// control, full-sync, periodic, and heartbeat workers behind the phase-B gate.
func (s *Server) PrepareBackground(parentCtx context.Context) (*PreparedBackground, error) {
	if parentCtx == nil {
		return nil, errors.New("agent server: nil background context")
	}
	rootCtx := s.lifecycleContext()
	if cause := context.Cause(parentCtx); cause != nil {
		return nil, cause
	}
	if cause := context.Cause(rootCtx); cause != nil {
		return nil, cause
	}
	startupLease, err := s.beginStartup()
	if err != nil {
		return nil, err
	}
	defer startupLease.Abort()
	ctx, cancel := context.WithCancelCause(rootCtx)
	stopParent := context.AfterFunc(parentCtx, func() { cancel(context.Cause(parentCtx)) })
	startup, err := s.prepareRuntime(ctx, startupLease, nil)
	if err != nil {
		stopParent()
		cancel(err)
		return nil, err
	}
	return &PreparedBackground{
		ctx: ctx, cancel: cancel, stopParent: stopParent, startup: startup,
	}, nil
}

// RunBackground prepares and immediately commits an embedded runtime, then
// blocks until its parent context is cancelled.
func (s *Server) RunBackground(ctx context.Context) {
	background, err := s.PrepareBackground(ctx)
	if err != nil {
		if ctx != nil && context.Cause(ctx) == nil {
			s.Logger.Error("prepare background agent failed", zap.Error(err))
		}
		return
	}
	background.Commit()
	if s.runBackgroundReady != nil {
		s.runBackgroundReady()
	}
	background.Wait()
}

func (s *Server) startRequestedFullSyncWorker(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Syncer.RunRequestedFullSyncs(ctx)
	}()
	return done
}

func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("agent server: nil shutdown context")
	}
	s.beginShutdown(ctx)
	select {
	case <-s.Done():
		s.lifecycleMu.Lock()
		err := s.shutdownErr
		s.lifecycleMu.Unlock()
		return err
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (s *Server) Done() <-chan struct{} {
	s.initLifecycle()
	return s.done
}

func (s *Server) initLifecycle() {
	s.lifecycleOnce.Do(func() {
		s.rootCtx, s.rootCancel = context.WithCancelCause(context.Background())
		s.done = make(chan struct{})
	})
}

func (s *Server) lifecycleContext() context.Context {
	s.initLifecycle()
	return s.rootCtx
}

func (s *Server) startLifecycleWorker(run func()) bool {
	s.initLifecycle()
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing {
		return false
	}
	s.startLifecycleWorkerLocked(run)
	return true
}

func (s *Server) startLifecycleWorkerLocked(run func()) {
	s.activeWorkers.Add(1)
	s.workers.Go(func() {
		defer s.activeWorkers.Add(-1)
		run()
	})
}

type startupLease struct {
	server     *Server
	generation uint64
	done       chan struct{}
	abortOnce  sync.Once
	finishOnce sync.Once
}

func (s *Server) beginStartup() (*startupLease, error) {
	s.initLifecycle()
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing || s.startupState == startupClosing {
		return nil, errAgentServerClosing
	}
	if s.startupState != startupIdle {
		return nil, ErrAlreadyRunning
	}
	s.startupGeneration++
	lease := &startupLease{server: s, generation: s.startupGeneration, done: make(chan struct{})}
	s.startupState = startupPreparing
	s.startupLease = lease
	return lease, nil
}

func (l *startupLease) Commit() {
	if l != nil {
		l.finishOnce.Do(func() {
			if l.done != nil {
				close(l.done)
			}
		})
	}
}

func (l *startupLease) commitLocked(server *Server) bool {
	if l == nil || server == nil || l.server != server {
		return false
	}
	if server.closing || server.startupState != startupPreparing ||
		server.startupLease != l || server.startupGeneration != l.generation {
		return false
	}
	server.startupState = startupRunning
	server.startupLease = nil
	return true
}

func (l *startupLease) Abort() {
	if l == nil || l.server == nil {
		return
	}
	l.abortOnce.Do(func() {
		server := l.server
		server.lifecycleMu.Lock()
		if server.startupLease == l && server.startupGeneration == l.generation {
			if server.startupState == startupPreparing {
				server.startupState = startupIdle
			}
			server.startupLease = nil
		}
		server.lifecycleMu.Unlock()
	})
	l.Commit()
}

type preparedRuntime struct {
	ctx         context.Context
	phaseBReady chan struct{}
	commitOnce  sync.Once
}

func (p *preparedRuntime) commit() {
	if p == nil || context.Cause(p.ctx) != nil {
		return
	}
	p.commitOnce.Do(func() { close(p.phaseBReady) })
}

// prepareRuntime is the shared standalone/embedded startup boundary. The
// optional publishLocked callback may only publish already-built owner fields.
func (s *Server) prepareRuntime(ctx context.Context, startup *startupLease, publishLocked func()) (*preparedRuntime, error) {
	syncer := cache.NewSyncer(
		s.Store,
		nil, // client set on connect
		s.Bus,
		s.Logger,
		time.Duration(s.Cfg.Runtime.FullSyncInterval)*time.Second,
	)
	rep, err := s.buildReporter()
	if err != nil {
		return nil, err
	}
	phaseAReady := make(chan struct{})
	phaseAResults := make(chan error, 2)
	phaseBReady := make(chan struct{})
	s.lifecycleMu.Lock()
	if !startup.commitLocked(s) {
		commitErr := ErrAlreadyRunning
		if s.closing || s.startupState == startupClosing {
			commitErr = errAgentServerClosing
		}
		s.lifecycleMu.Unlock()
		_ = rep.Close(ctx)
		<-rep.Done()
		if s.beforeStartupFailureReturn != nil {
			s.beforeStartupFailureReturn()
		}
		return nil, commitErr
	}
	s.Syncer = syncer
	s.Reporter = rep
	if publishLocked != nil {
		publishLocked()
	}
	start := func(ready <-chan struct{}, run func()) {
		s.startLifecycleWorkerLocked(func() { runAfterCommit(ctx, ready, run) })
	}
	start(phaseAReady, func() { phaseAResults <- syncer.SubscribeEvents() })
	start(phaseAReady, func() { phaseAResults <- rep.Start(ctx) })
	start(phaseBReady, func() { <-s.startTunnelRuntime(ctx) })
	start(phaseBReady, func() { <-s.startRequestedFullSyncWorker(ctx) })
	start(phaseBReady, func() { s.connectLoop(ctx) })
	start(phaseBReady, func() { _ = s.RouteReporter.Run(ctx) })
	start(phaseBReady, func() { syncer.RunPeriodicCheck(ctx) })
	start(phaseBReady, func() { s.heartbeatLoop(ctx) })
	s.lifecycleMu.Unlock()
	startup.Commit()
	close(phaseAReady)
	if err := waitForRunStartup(ctx, phaseAResults, 2); err != nil {
		s.closeCommittedRuntimeAfterStartupError(err)
		return nil, err
	}
	if s.afterRuntimePhaseAReady != nil {
		s.afterRuntimePhaseAReady(ctx)
	}
	if cause := context.Cause(ctx); cause != nil {
		s.closeCommittedRuntimeAfterStartupError(cause)
		return nil, cause
	}
	return &preparedRuntime{ctx: ctx, phaseBReady: phaseBReady}, nil
}

func (s *Server) closeCommittedRuntimeAfterStartupError(err error) {
	if err != nil && context.Cause(s.lifecycleContext()) == nil {
		s.abortRunStartup(err)
	}
}

func waitForRunStartup(ctx context.Context, results <-chan error, count int) error {
	for range count {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case err := <-results:
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			if err != nil {
				return err
			}
		}
	}
	return context.Cause(ctx)
}

func (s *Server) abortRunStartup(err error) {
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(err)
	s.beginShutdown(ctx)
	<-s.Done()
}

func runAfterCommit(ctx context.Context, ready <-chan struct{}, run func()) {
	select {
	case <-ctx.Done():
		return
	case <-ready:
	}
	if context.Cause(ctx) != nil {
		return
	}
	run()
}

func (s *Server) beginShutdown(ctx context.Context) {
	s.initLifecycle()
	s.shutdownOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.closing = true
		s.startupState = startupClosing
		var startupDone <-chan struct{}
		if s.startupLease != nil {
			startupDone = s.startupLease.done
		}
		s.lifecycleMu.Unlock()
		s.CancelDirectForwarding()
		go s.finalizeShutdown(ctx, startupDone)
	})
}

// CancelDirectForwarding stops new direct admission and cancels active direct
// requests without waiting for handlers to drain.
func (s *Server) CancelDirectForwarding() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	directForwarder := s.directForwarder
	s.lifecycleMu.Unlock()
	if directForwarder != nil {
		directForwarder.Cancel()
	}
}

func (s *Server) finalizeShutdown(ctx context.Context, startupDone <-chan struct{}) {
	s.lifecycleMu.Lock()
	httpSrv := s.httpSrv
	tunnelManager := s.TunnelManager
	directForwarder := s.directForwarder
	s.lifecycleMu.Unlock()
	if s.afterShutdownSnapshot != nil {
		s.afterShutdownSnapshot()
	}
	if startupDone != nil {
		<-startupDone
	}

	drains := pool.New().WithContext(ctx)
	if httpSrv != nil {
		drains.Go(func(ctx context.Context) error { return httpSrv.Shutdown(ctx) })
	}
	if tunnelManager != nil {
		drains.Go(func(ctx context.Context) error { return tunnelManager.Drain(ctx) })
	}
	drainErr := drains.Wait()
	shutdownCause := context.Cause(ctx)
	if shutdownCause == nil {
		shutdownCause = errors.New("agent server: shutdown")
	}
	s.rootCancel(shutdownCause)
	if directForwarder != nil {
		directForwarder.Cancel()
	}
	if s.Bus != nil {
		_ = s.Bus.Close()
	}

	if s.stopWatchdog != nil {
		s.stopWatchdog()
		s.watchdogActive.Store(0)
	}
	if s.Store != nil {
		s.Store.Close()
		<-s.Store.Done()
	}
	if httpSrv != nil {
		_ = httpSrv.Close()
	}
	if tunnelManager != nil {
		_ = tunnelManager.Close(ctx)
		<-tunnelManager.Done()
	}
	if client := s.getClient(); client != nil {
		_ = client.Close()
		<-client.Done()
	}
	if authCache := s.borrowAgentAuthCache(); authCache != nil {
		s.stopAgentAuthSession(authCache)
	}
	if s.Reporter != nil {
		_ = s.Reporter.Close(ctx)
		<-s.Reporter.Done()
	}
	if s.BodyStore != nil {
		_ = s.BodyStore.Close(ctx)
		<-s.BodyStore.Done()
	}
	if s.transportPool != nil {
		s.transportPool.CloseIdleConnections()
	}
	if directForwarder != nil {
		_ = directForwarder.Close(ctx)
		<-directForwarder.Done()
	}
	if s.legacyTransportOwner != nil {
		s.legacyTransportOwner.CloseIdleConnections()
	}
	s.workers.Wait()
	s.lifecycleMu.Lock()
	s.shutdownErr = drainErr
	s.lifecycleMu.Unlock()
	close(s.done)
}

func (s *Server) countHTTPHandlers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.httpHandlers.Add(1)
		defer s.httpHandlers.Add(-1)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) countAcceptedSockets(_ net.Conn, state http.ConnState) {
	switch state {
	case http.StateNew:
		s.acceptedSockets.Add(1)
	case http.StateClosed, http.StateHijacked:
		s.acceptedSockets.Add(-1)
	}
}

func (s *Server) ResourceCountsForTest() app.ResourceCounts {
	counts := app.ResourceCounts{LifecycleWorkers: s.activeWorkers.Load(), HTTPHandlers: s.httpHandlers.Load(), AcceptedSockets: s.acceptedSockets.Load(), Timers: s.watchdogActive.Load()}
	if client := s.getClient(); client != nil {
		select {
		case <-client.Done():
		default:
			counts.ControlSessions = 1
		}
	}
	if s.TunnelManager != nil {
		snapshot := s.TunnelManager.Snapshot()
		if snapshot.Availability == "available" {
			counts.RelayActive = 1
		}
		counts.RelayCandidates = int64(snapshot.Candidates)
		counts.RelayDraining = int64(snapshot.Draining)
		counts.RelayStreams = int64(snapshot.Streams)
	}
	if s.Store != nil {
		counts.CacheLoads, counts.CacheRefreshes = s.Store.ResourceCounts()
	}
	if s.Reporter != nil {
		reporterCounts := s.Reporter.ResourceCounts()
		counts.ReporterWorkers += reporterCounts.ReporterWorkers
		counts.Inflight += reporterCounts.Inflight
	}
	if s.Inflight != nil {
		counts.Inflight += int64(len(s.Inflight.Snapshot()))
	}
	if pool, ok := s.transportPool.(interface{ ResourceCount() int }); ok {
		counts.Transports += int64(pool.ResourceCount())
	}
	if s.directForwarder != nil {
		counts.Transports += int64(s.directForwarder.ResourceCount())
	}
	if s.legacyTransportOwner != nil {
		counts.Transports += int64(s.legacyTransportOwner.ResourceCount())
	}
	return counts
}

func (s *Server) connectLoop(ctx context.Context) {
	if s.beforeConnectLoop != nil {
		s.beforeConnectLoop()
	}
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		client, err := s.dial(ctx)
		if err != nil {
			s.Logger.Warn("connect to master failed, retrying",
				zap.Error(err),
				zap.Duration("backoff", backoff),
			)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff = time.Duration(math.Min(float64(backoff*2), float64(maxBackoff)))
			continue
		}

		// Reset backoff on successful connect
		backoff = time.Second
		s.Logger.Info("connected to master")
		if !s.runControlSession(ctx, client) {
			return
		}
		s.Logger.Warn("disconnected from master, will reconnect")
	}
}

func (s *Server) runControlSession(ctx context.Context, client *ws.Client) bool {
	client.BindNotificationContext(ctx)
	controlSession := s.setClient(client)
	defer s.endClient(controlSession, client)
	authCache, err := s.startAgentAuthSession(ctx, client, controlSession)
	if err != nil {
		_ = client.Close()
		<-client.Conn.Done()
		return ctx.Err() == nil
	}
	return s.waitForAgentAuthSession(ctx, client.Conn, authCache, controlSession)
}

func (s *Server) startAgentAuthSession(
	ctx context.Context,
	client app.WSClient,
	sessions ...*cache.ControlSession,
) (*agentauthcache.Cache, error) {
	controlSession := firstControlSession(s.Syncer, sessions)
	if controlSession == nil {
		return nil, cache.ErrControlSessionChanged
	}
	authCache := agentauthcache.NewCache(client, agentauthcache.CacheOptions{})
	if previous := s.replaceAgentAuthCache(authCache); previous != nil {
		previous.Close()
		<-previous.Done()
	}

	bridge := cache.NewWSBridge(client, s.Store, s.Bus, s.Logger)
	bridge.Syncer = s.Syncer
	bridge.ControlSession = controlSession
	bridge.SetAgentCapabilities = func(agentID string, capabilities []string) {
		s.setAgentCapabilitiesForAuthCache(authCache, agentID, capabilities)
	}
	bridge.ApplyDirectAddresses = func(update protocol.AgentDirectAddressesUpdate) bool {
		return s.applyDirectAddressesForAuthCache(authCache, update)
	}
	bridge.Start()
	s.registerControlHandlers(client, controlSession)

	if err := authCache.Run(ctx); err != nil {
		s.stopAgentAuthSession(authCache)
		return nil, err
	}
	if !s.beginDirectAddressSessionForAuthCache(authCache) {
		s.stopAgentAuthSession(authCache)
		return nil, errors.New("activate direct address session")
	}
	if err := client.Notify(consts.RPCSyncAgentCapabilities, protocol.AgentCapabilitiesUpdate{
		AgentID:      s.Creds.AgentID,
		Capabilities: agentRuntimeCapabilities(),
	}); err != nil {
		s.stopAgentAuthSession(authCache)
		return nil, fmt.Errorf("report agent capabilities: %w", err)
	}
	if err := s.Syncer.FullSyncForSession(ctx, controlSession); err != nil &&
		!errors.Is(err, cache.ErrControlSessionChanged) && s.Logger != nil {
		s.Logger.Error("full sync after connect failed", zap.Error(err))
	}
	return authCache, nil
}

func (s *Server) waitForAgentAuthSession(
	ctx context.Context,
	conn controlSessionConn,
	authCache *agentauthcache.Cache,
	sessions ...*cache.ControlSession,
) bool {
	reconnect := waitForControlSession(ctx, conn)
	if controlSession := firstControlSession(s.Syncer, sessions); controlSession != nil {
		s.Syncer.EndControlSession(controlSession)
	}
	s.stopAgentAuthSession(authCache)
	return reconnect
}

func (s *Server) stopAgentAuthSession(authCache *agentauthcache.Cache) {
	if authCache == nil {
		return
	}
	s.clearAgentAuthCache(authCache)
	authCache.Close()
	<-authCache.Done()
}

func (s *Server) replaceAgentAuthCache(next *agentauthcache.Cache) *agentauthcache.Cache {
	s.agentAuthCacheMu.Lock()
	previous := s.agentAuthCache
	if s.Store != nil {
		s.Store.ClearAgentCapabilities()
	}
	s.agentAuthCache = next
	s.agentAuthCacheMu.Unlock()
	return previous
}

func (s *Server) clearAgentAuthCache(expected *agentauthcache.Cache) bool {
	s.agentAuthCacheMu.Lock()
	defer s.agentAuthCacheMu.Unlock()
	if s.agentAuthCache != expected {
		return false
	}
	s.agentAuthCache = nil
	if s.Store != nil {
		s.Store.ClearAgentCapabilities()
	}
	return true
}

func (s *Server) borrowAgentAuthCache() *agentauthcache.Cache {
	s.agentAuthCacheMu.RLock()
	defer s.agentAuthCacheMu.RUnlock()
	return s.agentAuthCache
}

func (s *Server) setAgentCapabilitiesForAuthCache(
	expected *agentauthcache.Cache,
	agentID string,
	capabilities []string,
) bool {
	s.agentAuthCacheMu.Lock()
	defer s.agentAuthCacheMu.Unlock()
	if s.agentAuthCache != expected || s.Store == nil {
		return false
	}
	s.Store.SetAgentCapabilities(agentID, capabilities)
	return true
}

func (s *Server) beginDirectAddressSessionForAuthCache(expected *agentauthcache.Cache) bool {
	s.agentAuthCacheMu.Lock()
	defer s.agentAuthCacheMu.Unlock()
	if s.agentAuthCache != expected || s.Store == nil {
		return false
	}
	masterInstanceID := strings.TrimSpace(expected.Bootstrap().MasterInstanceID)
	if masterInstanceID == "" {
		return false
	}
	s.Store.BeginDirectAddressSession(masterInstanceID)
	return true
}

func (s *Server) applyDirectAddressesForAuthCache(
	expected *agentauthcache.Cache,
	update protocol.AgentDirectAddressesUpdate,
) bool {
	s.agentAuthCacheMu.Lock()
	defer s.agentAuthCacheMu.Unlock()
	if s.agentAuthCache != expected || s.Store == nil {
		return false
	}
	masterInstanceID := strings.TrimSpace(expected.Bootstrap().MasterInstanceID)
	if masterInstanceID == "" || masterInstanceID != strings.TrimSpace(update.MasterInstanceID) {
		return false
	}
	return s.Store.ApplyDirectAddressesUpdate(update)
}

func agentRuntimeCapabilities() []string {
	return []string{
		protocol.AgentCapabilityTunnelV1,
		protocol.AgentCapabilityForwardV1,
		protocol.AgentCapabilityDirectIngressV1,
		protocol.AgentCapabilityRelayHTTPPingV1,
		protocol.AgentCapabilityTokenRoutingV1,
	}
}

func deriveTunnelDesired(agent *models.Agent, defaultURI, masterURL string) (agenttunnel.Desired, string) {
	mode := consts.RelayModeInherit
	configuredURI := ""
	if agent == nil {
		return agenttunnel.Desired{Mode: mode}, "not_configured"
	}
	if agent.RelayMode != "" {
		mode = agent.RelayMode
	}
	configuredURI = pkgtunnel.TrimRelayURIWhitespace(agent.RelayURI)
	desired := agenttunnel.Desired{Mode: mode, ConfiguredURI: configuredURI}
	switch mode {
	case consts.RelayModeDisabled:
		return desired, "disabled"
	case consts.RelayModeCustom:
		desired.EffectiveURI = configuredURI
	default:
		desired.Mode = consts.RelayModeInherit
		desired.EffectiveURI = pkgtunnel.TrimRelayURIWhitespace(defaultURI)
		if desired.EffectiveURI == "" {
			desired.EffectiveURI, _ = netaddr.AgentRelayURIFromMasterURL(masterURL)
		}
	}
	if desired.EffectiveURI == "" {
		return desired, "not_configured"
	}
	return desired, "configured"
}

func tunnelBootstrapSupported(bootstrap agentauthcache.BootstrapSnapshot) bool {
	for _, capability := range bootstrap.Capabilities {
		if capability == protocol.AgentCapabilityTunnelV1 {
			return true
		}
	}
	return false
}

type serverTunnelTickets struct{ server *Server }

func (p serverTunnelTickets) RelayTicket(ctx context.Context, desiredGeneration uint64) (agentauth.RelayTicket, error) {
	cache := p.server.borrowAgentAuthCache()
	if cache == nil {
		return "", errors.New("agent tunnel: control bootstrap unavailable")
	}
	return cache.RelayTicket(ctx, desiredGeneration)
}

func (s *Server) currentTunnelBootstrap() agentauthcache.BootstrapSnapshot {
	cache := s.borrowAgentAuthCache()
	if cache == nil {
		return agentauthcache.BootstrapSnapshot{}
	}
	return cache.Bootstrap()
}

func (s *Server) currentForwardAuthSnapshot() agentproxy.ForwardAuthSnapshot {
	bootstrap := s.currentTunnelBootstrap()
	return agentproxy.ForwardAuthSnapshot{
		Capabilities: bootstrap.Capabilities,
		SigningKeys:  bootstrap.SigningKeys,
	}
}

func (s *Server) targetSupportsForwardTickets(agentID string) bool {
	return s != nil && s.Store != nil && slices.Contains(
		s.Store.GetAgentCapabilities(agentID),
		protocol.AgentCapabilityForwardV1,
	)
}

func (s *Server) targetSupportsDirectIngress(agentID string) bool {
	return s != nil && s.Store != nil && slices.Contains(
		s.Store.GetAgentCapabilities(agentID),
		protocol.AgentCapabilityDirectIngressV1,
	)
}

func (s *Server) cachedForwardTicket(_ context.Context) (agentauth.ForwardTicket, error) {
	cache := s.borrowAgentAuthCache()
	if cache == nil {
		return "", errors.New("agent forward auth bootstrap unavailable")
	}
	return cache.CachedForwardTicket()
}

func (s *Server) newTunnelManager() *agenttunnel.Manager {
	return s.newTunnelManagerWithRouter(nil)
}

func (s *Server) newRelayProber() *rpc.RelayProber {
	if s == nil || s.TunnelManager == nil {
		return nil
	}
	return rpc.NewRelayProber(rpc.RelayProberOptions{
		Link: s.TunnelManager, Metrics: s.RelayMetrics,
		RelayGeneration: func() uint64 {
			if s.TunnelManager == nil {
				return 0
			}
			return s.TunnelManager.Snapshot().SessionGeneration
		},
	})
}

func (s *Server) newTunnelManagerWithRouter(router http.Handler) *agenttunnel.Manager {
	if s == nil || s.Creds == nil {
		return nil
	}
	logger := s.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	limits := s.currentTunnelLimits()
	drainTimeout := s.currentTunnelDrainTimeout()
	dialer := agenttunnel.NewClientDialer(agenttunnel.ClientDialerOptions{
		AgentID: s.Creds.AgentID, Bootstrap: s.currentTunnelBootstrap, Limits: s.currentTunnelLimits,
		DrainTimeout:  s.currentTunnelDrainTimeout,
		TargetHandler: s.NewTunnelTargetHandler(router), Logger: logger.Named("relay-tunnel-client"),
	})
	return agenttunnel.NewManager(agenttunnel.ManagerOptions{
		SourceID: s.Creds.AgentID, Dialer: dialer, Tickets: serverTunnelTickets{server: s}, Limits: limits,
		DrainTimeout: drainTimeout, Logger: logger.Named("relay-tunnel-manager"),
	})
}

func (s *Server) currentTunnelDrainTimeout() time.Duration {
	if s == nil || s.Store == nil {
		return 300 * time.Second
	}
	return time.Duration(s.Store.Settings().TunnelDrainTimeoutSec) * time.Second
}

func (s *Server) currentTunnelLimits() pkgtunnel.Limits {
	limits := pkgtunnel.Limits{
		MaxMetadataBytes: 64 * 1024, MaxDataBytes: 64 * 1024,
		InitialStreamWindow: 512 * 1024, MaxQueuedSessionBytes: 8 * 1024 * 1024, MaxConcurrentStreams: 256,
	}
	if s == nil || s.Store == nil {
		return limits
	}
	settings := s.Store.Settings()
	return pkgtunnel.Limits{
		MaxMetadataBytes: settings.TunnelMaxMetadataBytes, MaxDataBytes: settings.TunnelMaxDataBytes,
		InitialStreamWindow:   settings.TunnelInitialWindowBytes,
		MaxQueuedSessionBytes: settings.TunnelMaxSessionQueueBytes, MaxConcurrentStreams: settings.TunnelMaxStreams,
	}
}

func (s *Server) startTunnelRuntime(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if s.TunnelManager == nil {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		reconcileDone := make(chan struct{})
		go func() {
			defer close(reconcileDone)
			s.runTunnelDesiredLoop(ctx)
		}()
		_ = s.TunnelManager.Run(ctx)
		<-reconcileDone
	}()
	return done
}

func (s *Server) runTunnelDesiredLoop(ctx context.Context) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		s.reconcileTunnelDesired()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) reconcileTunnelDesired() {
	if s.TunnelManager == nil || s.Creds == nil || s.Store == nil {
		return
	}
	cache := s.borrowAgentAuthCache()
	bootstrap := agentauthcache.BootstrapSnapshot{}
	if cache != nil {
		bootstrap = cache.Bootstrap()
	}
	agent := s.Store.GetAgent(s.Creds.AgentID)
	settings := s.Store.Settings()
	masterURL := ""
	if s.Cfg != nil {
		masterURL = s.Cfg.Agent.MasterURL
	}
	desired, configState := deriveTunnelDesired(agent, settings.RelayDefaultURI, masterURL)
	supportState := "unsupported"
	managerDesired := agenttunnel.Desired{Mode: consts.RelayModeDisabled}
	if tunnelBootstrapSupported(bootstrap) {
		supportState = "supported"
		managerDesired = desired
	}
	fingerprint := fmt.Sprintf("%p|%s|%s|%t|%s|%s|%s|%d|%d|%d|%d|%d|%d", cache, bootstrap.MasterInstanceID,
		supportState, agent != nil, desired.Mode, desired.ConfiguredURI, desired.EffectiveURI,
		settings.TunnelMaxMetadataBytes, settings.TunnelMaxDataBytes, settings.TunnelInitialWindowBytes,
		settings.TunnelMaxSessionQueueBytes, settings.TunnelMaxStreams, settings.TunnelDrainTimeoutSec)
	s.tunnelStateMu.Lock()
	if s.tunnelState.fingerprint == fingerprint {
		s.tunnelStateMu.Unlock()
		return
	}
	s.tunnelState = tunnelRuntimeState{support: supportState, config: configState, desired: desired, fingerprint: fingerprint}
	s.tunnelStateMu.Unlock()
	s.TunnelManager.Apply(managerDesired)
}

func (s *Server) tunnelHeartbeatRuntime() *protocol.RelayRuntime {
	if s == nil || s.TunnelManager == nil {
		return nil
	}
	snapshot := s.TunnelManager.Snapshot()
	s.tunnelStateMu.RLock()
	state := s.tunnelState
	s.tunnelStateMu.RUnlock()
	desired := state.desired
	if desired.Mode == "" {
		desired = snapshot.Desired
	}
	return &protocol.RelayRuntime{
		Support: state.support, Config: state.config, Availability: snapshot.Availability,
		AcceptingNewStreams: snapshot.AcceptingNewStreams, Convergence: snapshot.Convergence,
		Desired: protocol.RelayDesiredRuntime{
			Mode: desired.Mode, ConfiguredURI: sanitizeTunnelSnapshotURI(desired.ConfiguredURI),
			EffectiveURI: sanitizeTunnelSnapshotURI(desired.EffectiveURI), DesiredGeneration: snapshot.DesiredGeneration,
		},
		Active: protocol.RelayActiveRuntime{
			URI: sanitizeTunnelSnapshotURI(snapshot.ActiveURI), ActiveGeneration: snapshot.ActiveGeneration,
			SessionGeneration: snapshot.SessionGeneration, ConnectedAt: snapshot.ConnectedAt,
			Streams: snapshot.Streams, RetryAt: snapshot.RetryAt,
		},
		LastError:    snapshot.LastError,
		RecentErrors: relayRecentErrorsForHeartbeat(snapshot.RecentErrors),
	}
}

func relayRecentErrorsForHeartbeat(events []diagnostics.Event) []protocol.RelayRecentError {
	if len(events) > diagnostics.DefaultRingCapacity {
		events = events[len(events)-diagnostics.DefaultRingCapacity:]
	}
	result := make([]protocol.RelayRecentError, 0, len(events))
	for _, event := range events {
		result = append(result, protocol.RelayRecentError{
			Code: event.Code, Stage: event.Stage, Message: diagnostics.SanitizeText(event.Message), OccurredAt: event.At.Unix(), Count: 1,
		})
	}
	return result
}

func sanitizeTunnelSnapshotURI(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	parsed, err := pkgtunnel.ParseRelayURI(raw)
	if err != nil {
		return ""
	}
	return parsed.Sanitized
}

type controlSessionFullSync struct {
	syncer  *cache.Syncer
	session *cache.ControlSession
}

func (o controlSessionFullSync) FullSync(ctx context.Context) error {
	if o.syncer == nil || o.session == nil {
		return cache.ErrControlSessionChanged
	}
	return o.syncer.FullSyncForSession(ctx, o.session)
}

func firstControlSession(syncer *cache.Syncer, sessions []*cache.ControlSession) *cache.ControlSession {
	if len(sessions) > 0 && sessions[0] != nil {
		return sessions[0]
	}
	if syncer == nil {
		return nil
	}
	return syncer.CurrentControlSession()
}

func (s *Server) registerControlHandlers(client app.WSClient, sessions ...*cache.ControlSession) {
	controlSession := firstControlSession(s.Syncer, sessions)
	operationHandler := rpc.NewAgentOperationHandler(rpc.AgentOperationSources{
		Syncer:   controlSessionFullSync{syncer: s.Syncer, session: controlSession},
		Tunnel:   s.TunnelManager,
		Circuits: s.directForwarder,
		Inflight: s.Inflight,
	})
	client.OnNotification(consts.RPCAgentOperation, operationHandler.Handle)
	client.OnNotification(consts.RPCChannelTest, func(ctx context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleChannelTest(ctx, params, s.Store, s.Cfg.Agent.Listen, s.Logger)
	})
	client.OnNotification(consts.RPCAgentCheckConnectivity, func(ctx context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleCheckConnectivity(ctx, params, s.Logger)
	})
	client.OnNotification(consts.RPCAgentDirectProbe, func(ctx context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleDirectProbe(ctx, params, s.directProber, s.directGate)
	})
	client.OnNotification(consts.RPCAgentRelayProbe, func(ctx context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleRelayProbe(ctx, params, s.relayProber)
	})
	client.OnNotification(consts.RPCAgentInflight, func(context.Context, json.RawMessage) (any, error) {
		return rpc.HandleInflight(s.Inflight)
	})
	client.OnNotification(consts.RPCAgentGoroutines, func(context.Context, json.RawMessage) (any, error) {
		return rpc.HandleGoroutines()
	})
	client.OnNotification(consts.RPCAgentInterrupt, func(_ context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleInterrupt(s.Inflight, params)
	})
	client.OnNotification(consts.RPCAgentBreakers, func(context.Context, json.RawMessage) (any, error) {
		return rpc.HandleBreakers(s.Breakers)
	})
	client.OnNotification(consts.RPCAgentUsageQueue, func(context.Context, json.RawMessage) (any, error) {
		return rpc.HandleUsageQueue(s.Reporter)
	})
	client.OnNotification(consts.RPCAgentUsageQueueOp, func(_ context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleUsageQueueOp(s.Reporter, params)
	})
	client.OnNotification(consts.RPCAgentLimiterUsage, func(context.Context, json.RawMessage) (any, error) {
		var idx *cache.LimiterIndex
		if s.Store != nil {
			idx = s.Store.LimiterIndex
		}
		return rpc.HandleLimiterUsage(s.LimiterStore, idx)
	})
	client.OnNotification(consts.RPCChannelFetchModels, func(ctx context.Context, params json.RawMessage) (any, error) {
		return rpc.HandleFetchModels(ctx, params)
	})
}

type controlSessionConn interface {
	Done() <-chan struct{}
	Close() error
}

func waitForControlSession(ctx context.Context, conn controlSessionConn) bool {
	select {
	case <-conn.Done():
		return true
	case <-ctx.Done():
		_ = conn.Close()
		<-conn.Done()
		return false
	}
}

func (s *Server) dial(ctx context.Context) (*ws.Client, error) {
	headers := http.Header{}
	headers.Set(consts.HeaderXAgentID, s.Creds.AgentID)
	headers.Set(consts.HeaderXAgentSecret, s.Creds.Secret)
	return netaddr.WSDial(ctx, s.Cfg.Agent.MasterURL, s.Logger, headers)
}

func (s *Server) setClient(client *ws.Client) *cache.ControlSession {
	controlSession := s.Syncer.BeginControlSession(client)
	s.clientMu.Lock()
	s.client = client
	s.clientMu.Unlock()
	s.Reporter.SetClient(client)
	s.RouteReporter.SetClient(client)
	return controlSession
}

func (s *Server) endClient(controlSession *cache.ControlSession, client *ws.Client) {
	if s.Syncer != nil {
		s.Syncer.EndControlSession(controlSession)
	}
	cleared := false
	s.clientMu.Lock()
	if s.client == client {
		s.client = nil
		cleared = true
	}
	s.clientMu.Unlock()
	if cleared && s.Reporter != nil {
		s.Reporter.SetClient(nil)
	}
	if cleared && s.RouteReporter != nil {
		s.RouteReporter.SetClient(nil)
	}
}

func (s *Server) initRouteObservation() error {
	observer, err := agentroute.NewObserver(agentroute.ObserverOptions{Metrics: s.RelayMetrics})
	if err != nil {
		return fmt.Errorf("create route observer: %w", err)
	}
	s.RouteObserver = observer
	s.RouteReporter = agentroute.NewReporter(observer, agentroute.ReporterOptions{})
	return nil
}

func (s *Server) getClient() *ws.Client {
	s.clientMu.RLock()
	defer s.clientMu.RUnlock()
	return s.client
}

func extractPort(listen string) int {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(portStr)
	return port
}

// nonzero 返回 v 本身；若 v<=0 则返回 def。用于将配置零值替换为合理默认。
// 注意：keepalive 默认值同时也在 config.normalizeRelayConfig 兜底；此处是
// defense-in-depth（万一某条 config 路径未归一化也不至于让保活退化为零值），
// 两处默认值需保持一致（15/15/3）。
func nonzero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

type relayRuntime struct {
	relayHandler   *agentrelay.Handler
	attemptHandler *agentattemptproxy.Handler
	provider       attemptexec.ProviderAttemptExecutor
}

func (s *Server) SnapshotRemoteTarget(agentID string) (relayexec.RemoteTargetSnapshot, bool) {
	if s == nil || s.Store == nil {
		return relayexec.RemoteTargetSnapshot{}, false
	}
	target := s.Store.GetAgent(agentID)
	if target == nil {
		return relayexec.RemoteTargetSnapshot{}, false
	}
	globalProxyURL := ""
	preferredTag := ""
	if s.Cfg != nil {
		globalProxyURL = s.Cfg.Agent.ProxyURL
		preferredTag = s.Cfg.Agent.PreferredAddrTag
	}
	return relayexec.RemoteTargetSnapshot{
		Enabled:        target.Status == consts.StatusEnabled,
		HTTPAddresses:  target.HTTPAddresses,
		ProxyURL:       target.ProxyURL,
		GlobalProxyURL: globalProxyURL,
		PreferredTag:   preferredTag,
	}, true
}

func (s *Server) observeAttemptPath(record models.AgentPathRecord) {
	if s != nil && s.RouteObserver != nil {
		result := "error"
		if record.Result == models.AgentPathSelected {
			result = "success"
		}
		s.RouteObserver.Record(protocol.RouteEvent{
			TargetAgentID: record.AgentID,
			PathKind:      string(record.Path),
			Result:        result,
			Stage:         string(record.Stage),
			ReasonCode:    record.ReasonCode,
			CommitState:   string(record.CommitState),
			DurationMS:    int64(record.DurationMs),
		})
	}
}

// buildRelayHandler 装配共享 relay runtime，并把 transport pool invalidate-on-change
// 钩子挂到 Store 上：channel 的 ProxyURL 变更时让缓存的 *http.Transport 失效，
// 否则连接会一直走旧代理。钩子由 server.go 在装配阶段注入，避免 Handler 反向依赖 Store。
func (s *Server) buildRelayHandler(relayTimeout time.Duration) relayRuntime {
	pool := upstream.NewTransportPool(
		s.Cfg.Relay.MaxIdleConns,
		s.Cfg.Relay.MaxIdleConnsPerHost,
		relayTimeout,
		upstream.KeepaliveConfig{
			Idle:     time.Duration(nonzero(s.Cfg.Relay.KeepaliveIdle, 15)) * time.Second,
			Interval: time.Duration(nonzero(s.Cfg.Relay.KeepaliveInterval, 15)) * time.Second,
			Count:    nonzero(s.Cfg.Relay.KeepaliveCount, 3),
		},
	)
	s.lifecycleMu.Lock()
	if s.closing {
		pool.CloseIdleConnections()
	} else {
		s.transportPool = pool
	}
	s.lifecycleMu.Unlock()
	if s.Store != nil {
		s.Store.OnChannelChange(func(old, new *models.Channel) {
			if old == nil || new == nil {
				return // 新增/删除无旧 transport
			}
			if old.ProxyURL == new.ProxyURL {
				return // ProxyURL 没变，transport 不需 invalidate
			}
			pool.Invalidate(old.ID, old.ProxyURL)
		})
	}

	runtimeCfg := &config.AgentRuntimeConfig{
		Runtime: s.Cfg.Runtime,
		Relay:   s.Cfg.Relay,
		Agent:   s.Cfg.Agent,
	}
	if runtimeCfg.Relay.Timeout == 0 {
		runtimeCfg.Relay.Timeout = int(relayTimeout.Seconds())
	}
	agentApp := agentappkg.NewDefaultAgentApplication(s.Store, s.BodyStore, s.Logger, runtimeCfg, pool, s.legacyTransportOwner)

	dispatcher := backend.NewDispatcher(agentApp)
	s.LimiterStore = limiter.NewMemStore()
	s.Breakers = resilience.NewRegistry()
	remote := relayexec.NewRemoteAttemptExecutor(relayexec.RemoteAttemptExecutorOptions{
		SourceAgentID: s.Creds.AgentID,
		Direct:        s.directForwarder,
		Relay:         s.TunnelManager,
		Targets:       s,
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) {
			return s.cachedForwardTicket(context.Background())
		},
		RelayEnabled: func() bool {
			return s.Store != nil && s.Store.Settings().RelayFallbackEnabled == 1
		},
		PeerRouteMode: s.peerRouteMode,
		Observer:      s.observeAttemptPath,
	})
	relayHandler := agentrelay.NewHandler(
		s.Bus, agentApp, dispatcher, s.Inflight, s.LimiterStore, s.Breakers,
		agentrelay.WithAttemptRouting(
			s.Creds.AgentID,
			relayexec.NewAttemptRouteBuilder(agentApp.GetCache()),
			nil,
			remote,
		),
	)
	provider := relayHandler.ProviderAttemptExecutor()
	return relayRuntime{
		relayHandler: relayHandler,
		attemptHandler: agentattemptproxy.NewHandler(
			agentattemptproxy.NewContextBuilder(agentApp),
			agentattemptproxy.NewBoundChannelFinder(agentApp.GetCache()),
			provider,
			agentattemptproxy.NewResponseExecutor(),
		),
		provider: provider,
	}
}

func newRequestBodyStore(cfg *config.AgentRuntimeConfig, logger *zap.Logger) (*bodypkg.Store, error) {
	directory := filepath.Join(filepath.Dir(cfg.Agent.CredentialsFile), "request-bodies")
	store, err := bodypkg.NewStore(bodypkg.StoreOptions{Directory: directory})
	if err != nil {
		return nil, fmt.Errorf("create request body store: %w", err)
	}
	if err := store.Scavenge(context.Background()); err != nil && logger != nil {
		logger.Warn("request body stale cleanup failed", zap.Error(err))
	}
	return store, nil
}

// lazyWSClient 是 app.WSClient 的延迟代理。
// Store / Loader 在 agent 启动时以 nil client 初始化，
// 连接建立后 setClient 写入 *ws.Client；lazyWSClient 在每次 Call
// 时通过 getClient() 获取最新实际 client，避免 Loader 持有 nil。
type lazyWSClient struct {
	getClient func() *ws.Client
}

func (l *lazyWSClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c := l.getClient()
	if c == nil {
		// 预连接窗口语义上等同于"master 不可达",返回 ws.ErrConnClosed 使其能被
		// classifyResolveErr 归类为 master_unreachable,而非 unknown。
		return nil, ws.ErrConnClosed
	}
	return c.Call(ctx, method, params)
}

func (l *lazyWSClient) OnNotification(method string, handler app.NotificationHandler) {
	c := l.getClient()
	if c != nil {
		c.OnNotification(method, handler)
	}
}

func (l *lazyWSClient) Notify(method string, params any) error {
	c := l.getClient()
	if c == nil {
		return fmt.Errorf("ws client not connected")
	}
	return c.Notify(method, params)
}

func (l *lazyWSClient) Close() error {
	c := l.getClient()
	if c == nil {
		return nil
	}
	return c.Close()
}

func (l *lazyWSClient) ReadLoop() {
	c := l.getClient()
	if c != nil {
		c.ReadLoop()
	}
}

var _ app.WSClient = (*lazyWSClient)(nil)

func (s *Server) buildReporter() (*reporter.Reporter, error) {
	store := reporter.NewMemPendingUsageStore(s.Cfg.Runtime.ReportBufferSize*10, s.Logger)
	uploader, err := reporter.NewUsageUploader(reporter.UploaderConfig{
		Store:                    store,
		MasterURL:                s.Cfg.Agent.MasterURL,
		AgentID:                  s.Creds.AgentID,
		Secret:                   s.Creds.Secret,
		FlushInterval:            time.Duration(s.Cfg.Runtime.ReportFlushInterval) * time.Second,
		BatchMax:                 s.Cfg.Runtime.ReportBufferSize,
		RetryLimit:               s.Cfg.Runtime.ReportBufferSize * 10,
		BackoffMaxSec:            func() int { return s.Store.Settings().UsageUploadBackoffMaxSec },
		Concurrency:              func() int { return s.Store.Settings().UsageUploadConcurrency },
		SlimBodyAfterAttempts:    func() int { return s.Store.Settings().UsageSlimBodyAfterAttempts },
		StripTraceAfterAttempts:  func() int { return s.Store.Settings().UsageStripTraceAfterAttempts },
		BillingOnlyAfterAttempts: func() int { return s.Store.Settings().UsageBillingOnlyAfterAttempts },
		Logger:                   s.Logger,
	})
	if err != nil {
		return nil, err
	}
	snapPath := filepath.Join(filepath.Dir(s.Cfg.Agent.CredentialsFile), "usage_backlog.snapshot.gz")
	snap := &reporter.Snapshotter{Store: store, Uploader: uploader, Path: snapPath, Logger: s.Logger}
	return reporter.New(s.Bus, s.Logger, store, uploader, snap), nil
}

// heartbeatCaller 是 heartbeatTick 的最小依赖;*ws.Client 满足。
type heartbeatCaller interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
}

// heartbeatTick 发送一次心跳(仅上报 runtime 统计)。失败只告警并累计计数,
// 永不主动断连——判活交给 ws 传输层 Ping/Pong(30s/240s)。behavior change: 旧逻辑
// 连续 3 次失败会 Close 触发重连,在跨区高延迟下造成 flapping(spec §1.2/§5.1)。
func (s *Server) heartbeatTick(
	ctx context.Context,
	client heartbeatCaller,
	params protocol.HeartbeatParams,
	timeout time.Duration,
	failures int,
) int {
	if client == nil {
		return failures
	}
	// behavior change: session shutdown now interrupts an in-flight heartbeat immediately.
	hbCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if _, err := client.Call(hbCtx, consts.RPCAgentHeartbeat, params); err != nil {
		failures++
		s.Logger.Warn("heartbeat failed (non-fatal, liveness handled by ws ping/pong)",
			zap.Error(err), zap.Int("consecutive_failures", failures))
		return failures
	}
	return 0
}

// connCloser 是 maybeForceReconnect 的最小依赖;*ws.Client 满足。
type connCloser interface {
	Close() error
}

// maybeForceReconnect 是心跳的应用层恢复兜底:连续失败达到阈值就主动断开,
// 让 connectLoop 立即重拨,而不是干等 ws Ping/Pong 的 240s 假死窗口
// (pongTimeout,ws/server.go)——期间控制面 Call(token 鉴权/实体拉取)全在超时。
// behavior change: d27d98a 曾完全移除心跳断连;生产实证纯 Ping/Pong 判活的恢复
// 窗口太长(token auth control_timeout),这里以"可调阈值 + 0 值禁用"折中找回。
func (s *Server) maybeForceReconnect(client connCloser, failures, threshold int) int {
	if threshold <= 0 || client == nil || failures < threshold {
		return failures
	}
	s.Logger.Error("consecutive heartbeat failures reached threshold, forcing reconnect",
		zap.Int("failures", failures), zap.Int("threshold", threshold))
	_ = client.Close()
	return 0
}

func (s *Server) directListenPort() int {
	if s == nil || !s.ownsHTTPListener || s.Cfg == nil {
		return 0
	}
	return extractPort(s.Cfg.Agent.Listen)
}

func (s *Server) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.Cfg.Runtime.HeartbeatInterval) * time.Second)
	defer ticker.Stop()
	startTime := time.Now()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			client := s.getClient()
			if client == nil {
				continue
			}
			addrJSON, _ := json.Marshal(s.Cfg.Agent.HTTPAddresses)
			params := protocol.HeartbeatParams{
				Uptime:               int64(time.Since(startTime).Seconds()),
				CachedTokens:         s.Store.TokenCount(),
				CachedChannels:       s.Store.ChannelCount(),
				CachedModels:         s.Store.ModelConfigCount(),
				CachedGlobalRoutings: s.Store.GlobalRoutingCount(),
				CachedUserRoutings:   s.Store.UserRoutingsCount(),
				Version:              s.Store.Version(),
				PendingUsage:         s.Reporter.PendingCount(),
				Capabilities:         agentRuntimeCapabilities(),
				Relay:                s.tunnelHeartbeatRuntime(),
				HTTPAddresses:        addrJSON,
				Tags:                 s.Cfg.Agent.Tags,
				ProxyURL:             s.Cfg.Agent.ProxyURL,
				ListenPort:           s.directListenPort(),
				CacheStats:           s.Store.CacheSnapshot(),
			}
			timeout := time.Duration(s.Store.Settings().HeartbeatCallTimeoutSec) * time.Second
			failures = s.heartbeatTick(ctx, client, params, timeout, failures)
			failures = s.maybeForceReconnect(client, failures, s.Store.Settings().HeartbeatReconnectFailures)
		}
	}
}
