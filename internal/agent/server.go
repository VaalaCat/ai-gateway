package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	"github.com/VaalaCat/ai-gateway/internal/agent/auth"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agentrelay "github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/inflight"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	"github.com/VaalaCat/ai-gateway/internal/agent/reporter"
	"github.com/VaalaCat/ai-gateway/internal/agent/rpc"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var _ app.AgentServer = (*Server)(nil)

type Server struct {
	Cfg      *config.AgentRuntimeConfig
	Logger   *zap.Logger
	Bus      app.EventBus
	Router   *gin.Engine
	Creds    *enrollment.Credentials
	Store    *cache.Store
	Reporter *reporter.Reporter
	Syncer   *cache.Syncer
	Listener net.Listener
	httpSrv  *http.Server

	Inflight     *inflight.Registry
	stopWatchdog func()

	clientMu sync.RWMutex
	client   *ws.Client
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

	s := &Server{
		Cfg:    runtimeCfg,
		Logger: logger,
		Bus:    bus,
		Router: gin.New(),
		Creds:  creds,
	}
	// lazyWSClient 将 LRU Loader 的 RPC 调用委托给运行时实际连接，
	// 避免 Store 在连接建立前因持有 nil client 而 Loader 不可用。
	s.Store = cache.NewStore(&lazyWSClient{getClient: s.getClient}, runtimeCfg.Agent.Cache)
	s.Store.SetLogger(s.Logger)

	warnAge := time.Duration(s.Cfg.Runtime.RelayTimeout) * 2 * time.Second
	if warnAge < 60*time.Second {
		warnAge = 60 * time.Second
	}
	s.Inflight = inflight.NewRegistry(s.Logger.Named("inflight"), warnAge)
	s.stopWatchdog = s.Inflight.StartWatchdog(10 * time.Second)

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

	routeForwarder := &agentproxy.RouteForwarder{
		SelfID:         s.Creds.AgentID,
		GetAgent:       s.Store.GetAgent,
		GetAgentsByTag: s.Store.GetAgentsByTag,
		GlobalProxyURL: s.Cfg.Agent.ProxyURL,
		PreferredTag:   s.Cfg.Agent.PreferredAddrTag,
	}

	relayHandler := s.buildRelayHandler(routeForwarder,
		time.Duration(s.Cfg.Runtime.RelayTimeout)*time.Second)

	resolver := func(agentID, agentTag string) *models.Agent {
		if agentID != "" {
			return s.Store.GetAgent(agentID)
		}
		if agentTag != "" {
			agents := s.Store.GetAgentsByTag(agentTag)
			if len(agents) > 0 {
				return agents[0]
			}
		}
		return nil
	}
	addrFetcher := func(agentID string) []agentproxy.Address {
		agent := s.Store.GetAgent(agentID)
		if agent == nil {
			return nil
		}
		return agentproxy.ParseAddresses(agent.HTTPAddresses)
	}

	v1 := s.Router.Group("/v1")
	v1.Use(agentproxy.ForwardMiddleware(agentproxy.ForwardConfig{
		SelfID:           s.Creds.AgentID,
		Resolver:         resolver,
		AgentAddrFetcher: addrFetcher,
		PreferredAddrTag: s.Cfg.Agent.PreferredAddrTag,
		IsMaster:         false,
	}))
	v1.Use(auth.TokenAuth(s.Store))
	v1.GET("/models", agentrelay.ListModels(s.Store))
	v1.POST("/chat/completions", relayHandler.Relay)
	v1.POST("/completions", relayHandler.Relay)
	v1.POST("/responses", relayHandler.Relay)
	v1.POST("/responses/:id", relayHandler.Relay)
	v1.POST("/messages", relayHandler.Relay)
	v1.POST("/embeddings", relayHandler.Relay)
	v1.POST("/images/generations", relayHandler.Relay)
	v1.POST("/audio/transcriptions", relayHandler.Relay)
	v1.POST("/audio/translations", relayHandler.Relay)
	v1.POST("/audio/speech", relayHandler.Relay)
}

func (s *Server) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup Syncer (subscribes to EventBus, client set later)
	s.Syncer = cache.NewSyncer(
		s.Store,
		nil, // client set on connect
		s.Bus,
		s.Logger,
		time.Duration(s.Cfg.Runtime.FullSyncInterval)*time.Second,
	)
	s.Syncer.SubscribeEvents()

	// Setup reporter (client set later)
	s.Reporter = reporter.New(
		s.Bus,
		nil, // client set on connect
		s.Logger,
		s.Cfg.Runtime.ReportBufferSize,
		time.Duration(s.Cfg.Runtime.ReportFlushInterval)*time.Second,
		s.Creds.AgentID,
	)
	s.Reporter.Start(ctx)

	// Handle full sync requests from master
	events.SubscribeSyncFullSyncRequested(s.Bus, func(_ context.Context) error {
		s.Logger.Info("master requested full sync")
		go func() {
			if err := s.Syncer.FullSync(ctx); err != nil {
				s.Logger.Error("full sync (requested) failed", zap.Error(err))
			}
		}()
		return nil
	})

	// Connect and start reconnection loop in background
	go s.connectLoop(ctx)

	// Start periodic version check
	s.Syncer.StartPeriodicCheck(ctx)

	// Start heartbeat
	go s.heartbeatLoop(ctx)

	// Start HTTP server
	ln, err := netaddr.Listen(s.Cfg.Agent.Listen)
	if err != nil {
		return err
	}
	s.Listener = ln

	s.httpSrv = &http.Server{
		Handler:           s.Router,
		ReadHeaderTimeout: 30 * time.Second, // guard against inbound slowloris
	}
	s.Logger.Info("agent listening",
		zap.String("addr", ln.Addr().String()),
		zap.String("agent_id", s.Creds.AgentID),
	)
	return s.httpSrv.Serve(ln)
}

// NewEmbedded creates an agent server for embedding inside master.
// It skips enrollment and accepts pre-configured credentials.
// The returned server has no Router — use MountRoutes() to attach
// relay routes to an external router.
func NewEmbedded(cfg config.AgentRuntimeProvider, logger *zap.Logger, creds *enrollment.Credentials) (*Server, error) {
	runtimeCfg := cfg.ToAgentRuntimeConfig()
	if runtimeCfg == nil {
		return nil, fmt.Errorf("agent runtime config is required")
	}

	bus := eventbus.NewMemoryBus()

	s := &Server{
		Cfg:    runtimeCfg,
		Logger: logger,
		Bus:    bus,
		Router: nil, // embedded agent does not own a router
		Creds:  creds,
	}
	// lazyWSClient 将 LRU Loader 的 RPC 调用委托给运行时实际连接。
	s.Store = cache.NewStore(&lazyWSClient{getClient: s.getClient}, runtimeCfg.Agent.Cache)
	s.Store.SetLogger(s.Logger)

	warnAge := time.Duration(s.Cfg.Runtime.RelayTimeout) * 2 * time.Second
	if warnAge < 60*time.Second {
		warnAge = 60 * time.Second
	}
	s.Inflight = inflight.NewRegistry(s.Logger.Named("inflight"), warnAge)
	s.stopWatchdog = s.Inflight.StartWatchdog(10 * time.Second)

	return s, nil
}

// MountRoutes registers /v1/* relay routes on the given router.
// This is used by the embedded agent to share the master's router.
func (s *Server) MountRoutes(router *gin.Engine) {
	relayTimeout := time.Duration(s.Cfg.Runtime.RelayTimeout) * time.Second
	if relayTimeout == 0 {
		relayTimeout = 300 * time.Second
	}

	rf := &agentproxy.RouteForwarder{
		SelfID:         s.Creds.AgentID,
		GetAgent:       s.Store.GetAgent,
		GetAgentsByTag: s.Store.GetAgentsByTag,
		GlobalProxyURL: s.Cfg.Agent.ProxyURL,
		PreferredTag:   s.Cfg.Agent.PreferredAddrTag,
	}

	relayHandler := s.buildRelayHandler(rf, relayTimeout)

	resolver := func(agentID, agentTag string) *models.Agent {
		if agentID != "" {
			return s.Store.GetAgent(agentID)
		}
		if agentTag != "" {
			agents := s.Store.GetAgentsByTag(agentTag)
			if len(agents) > 0 {
				return agents[0]
			}
		}
		return nil
	}
	addrFetcher := func(agentID string) []agentproxy.Address {
		agent := s.Store.GetAgent(agentID)
		if agent == nil {
			return nil
		}
		return agentproxy.ParseAddresses(agent.HTTPAddresses)
	}

	v1 := router.Group("/v1")
	v1.Use(agentproxy.ForwardMiddleware(agentproxy.ForwardConfig{
		SelfID:           s.Creds.AgentID,
		Resolver:         resolver,
		AgentAddrFetcher: addrFetcher,
		PreferredAddrTag: s.Cfg.Agent.PreferredAddrTag,
		IsMaster:         false,
	}))
	v1.Use(auth.TokenAuth(s.Store))
	v1.GET("/models", agentrelay.ListModels(s.Store))
	v1.POST("/chat/completions", relayHandler.Relay)
	v1.POST("/completions", relayHandler.Relay)
	v1.POST("/responses", relayHandler.Relay)
	v1.POST("/responses/:id", relayHandler.Relay)
	v1.POST("/messages", relayHandler.Relay)
	v1.POST("/embeddings", relayHandler.Relay)
	v1.POST("/images/generations", relayHandler.Relay)
	v1.POST("/audio/transcriptions", relayHandler.Relay)
	v1.POST("/audio/translations", relayHandler.Relay)
	v1.POST("/audio/speech", relayHandler.Relay)
}

// RunBackground starts agent background goroutines (Reporter, Syncer,
// connectLoop, heartbeat) without starting an HTTP server.
// It blocks until ctx is cancelled.
func (s *Server) RunBackground(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Setup Syncer
	s.Syncer = cache.NewSyncer(
		s.Store,
		nil, // client set on connect
		s.Bus,
		s.Logger,
		time.Duration(s.Cfg.Runtime.FullSyncInterval)*time.Second,
	)
	s.Syncer.SubscribeEvents()

	// Setup Reporter
	s.Reporter = reporter.New(
		s.Bus,
		nil, // client set on connect
		s.Logger,
		s.Cfg.Runtime.ReportBufferSize,
		time.Duration(s.Cfg.Runtime.ReportFlushInterval)*time.Second,
		s.Creds.AgentID,
	)
	s.Reporter.Start(ctx)

	// Handle full sync requests from master
	events.SubscribeSyncFullSyncRequested(s.Bus, func(_ context.Context) error {
		s.Logger.Info("master requested full sync")
		go func() {
			if err := s.Syncer.FullSync(ctx); err != nil {
				s.Logger.Error("full sync (requested) failed", zap.Error(err))
			}
		}()
		return nil
	})

	// Connect and start reconnection loop in background
	go s.connectLoop(ctx)

	// Start periodic version check
	s.Syncer.StartPeriodicCheck(ctx)

	// Start heartbeat
	go s.heartbeatLoop(ctx)

	// Block until cancelled
	<-ctx.Done()
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.stopWatchdog != nil {
		s.stopWatchdog()
	}
	if s.httpSrv != nil {
		return s.httpSrv.Shutdown(ctx)
	}
	return nil
}

func (s *Server) connectLoop(ctx context.Context) {
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

		// Update client references
		s.setClient(client)

		// Setup bridge for this connection
		bridge := cache.NewWSBridge(client, s.Store, s.Bus, s.Logger)
		bridge.Syncer = s.Syncer
		bridge.Start()

		// Register RPC handlers for master-initiated calls
		client.OnNotification(consts.RPCChannelTest, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleChannelTest(ctx, params, s.Store, s.Cfg.Agent.Listen, s.Logger)
		})
		client.OnNotification(consts.RPCAgentCheckConnectivity, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleCheckConnectivity(ctx, params, s.Logger)
		})
		client.OnNotification(consts.RPCAgentInflight, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleInflight(s.Inflight)
		})
		client.OnNotification(consts.RPCAgentGoroutines, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleGoroutines()
		})
		client.OnNotification(consts.RPCAgentInterrupt, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleInterrupt(s.Inflight, params)
		})
		client.OnNotification(consts.RPCChannelFetchModels, func(ctx context.Context, params json.RawMessage) (any, error) {
			return rpc.HandleFetchModels(ctx, params)
		})

		// Full sync on (re)connect
		if err := s.Syncer.FullSync(ctx); err != nil {
			s.Logger.Error("full sync after connect failed", zap.Error(err))
		}

		// Wait for connection to close
		<-client.Conn.Done()
		s.Logger.Warn("disconnected from master, will reconnect")
	}
}

func (s *Server) dial(ctx context.Context) (*ws.Client, error) {
	headers := http.Header{}
	headers.Set(consts.HeaderXAgentID, s.Creds.AgentID)
	headers.Set(consts.HeaderXAgentSecret, s.Creds.Secret)
	return netaddr.WSDial(ctx, s.Cfg.Agent.MasterURL, s.Logger, headers)
}

func (s *Server) setClient(client *ws.Client) {
	s.clientMu.Lock()
	s.client = client
	s.clientMu.Unlock()
	s.Syncer.SetClient(client)
	s.Reporter.SetClient(client)
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

// buildRelayHandler 装配 *agentrelay.Handler 并把 transport pool invalidate-on-change
// 钩子挂到 Store 上：channel 的 ProxyURL 变更时让缓存的 *http.Transport 失效，
// 否则连接会一直走旧代理。钩子由 server.go 在装配阶段注入，避免 Handler 反向依赖 Store。
func (s *Server) buildRelayHandler(rf *agentproxy.RouteForwarder, relayTimeout time.Duration) *agentrelay.Handler {
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
	agentApp := agentappkg.NewDefaultAgentApplication(s.Store, rf, s.Logger, runtimeCfg, pool)

	dispatcher := backend.NewDispatcher(agentApp)
	return agentrelay.NewHandler(s.Bus, agentApp, dispatcher, s.Inflight)
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
		return nil, fmt.Errorf("ws client not connected")
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

func (s *Server) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(s.Cfg.Runtime.HeartbeatInterval) * time.Second)
	defer ticker.Stop()
	startTime := time.Now()
	consecutiveFailures := 0
	const maxFailures = 3

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
				Uptime:         int64(time.Since(startTime).Seconds()),
				CachedTokens:   s.Store.TokenCount(),
				CachedChannels: s.Store.ChannelCount(),
				CachedModels:   s.Store.ModelConfigCount(),
				CachedGlobalRoutings: s.Store.GlobalRoutingCount(),
				CachedUserRoutings:   s.Store.UserRoutingsCount(),
				Version:        s.Store.Version(),
				HTTPAddresses:  addrJSON,
				Tags:           s.Cfg.Agent.Tags,
				ProxyURL:       s.Cfg.Agent.ProxyURL,
				ListenPort:     extractPort(s.Cfg.Agent.Listen),
				CacheStats:     s.Store.CacheSnapshot(),
			}
			hbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, err := client.Call(hbCtx, consts.RPCAgentHeartbeat, params)
			cancel()
			if err != nil {
				consecutiveFailures++
				s.Logger.Warn("heartbeat failed",
					zap.Error(err),
					zap.Int("consecutive_failures", consecutiveFailures),
				)
				if consecutiveFailures >= maxFailures {
					s.Logger.Error("heartbeat failed too many times, closing connection to trigger reconnect",
						zap.Int("failures", consecutiveFailures),
					)
					client.Close()
					consecutiveFailures = 0
				}
			} else {
				consecutiveFailures = 0
			}
		}
	}
}
