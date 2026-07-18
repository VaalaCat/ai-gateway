package master

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	masteragentauth "github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apiagent "github.com/VaalaCat/ai-gateway/internal/master/api/agent"
	"github.com/VaalaCat/ai-gateway/internal/master/api/agent_route"
	apibilling "github.com/VaalaCat/ai-gateway/internal/master/api/billing"
	apicache "github.com/VaalaCat/ai-gateway/internal/master/api/cache"
	apicapability "github.com/VaalaCat/ai-gateway/internal/master/api/capability"
	"github.com/VaalaCat/ai-gateway/internal/master/api/channel"
	apiinsights "github.com/VaalaCat/ai-gateway/internal/master/api/insights"
	apiinvite "github.com/VaalaCat/ai-gateway/internal/master/api/invite"
	apilog "github.com/VaalaCat/ai-gateway/internal/master/api/log"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/master/api/model"
	apimodelrouting "github.com/VaalaCat/ai-gateway/internal/master/api/model_routing"
	apimonitoring "github.com/VaalaCat/ai-gateway/internal/master/api/monitoring"
	apioauth "github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	apioap "github.com/VaalaCat/ai-gateway/internal/master/api/oauth_provider_admin"
	apiobservability "github.com/VaalaCat/ai-gateway/internal/master/api/observability"
	"github.com/VaalaCat/ai-gateway/internal/master/api/private_channel"
	apiratelimiter "github.com/VaalaCat/ai-gateway/internal/master/api/request_limiter"
	apiscript "github.com/VaalaCat/ai-gateway/internal/master/api/script"
	"github.com/VaalaCat/ai-gateway/internal/master/api/stats"
	apisystem "github.com/VaalaCat/ai-gateway/internal/master/api/system"
	"github.com/VaalaCat/ai-gateway/internal/master/api/token"
	"github.com/VaalaCat/ai-gateway/internal/master/api/token_template"
	"github.com/VaalaCat/ai-gateway/internal/master/api/user"
	"github.com/VaalaCat/ai-gateway/internal/master/api/user_group"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	"github.com/VaalaCat/ai-gateway/internal/master/connectivity"
	masteroperations "github.com/VaalaCat/ai-gateway/internal/master/operations"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	mastertunnel "github.com/VaalaCat/ai-gateway/internal/master/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/netaddr"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	pkgtunnel "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ws"
	webassets "github.com/VaalaCat/ai-gateway/web"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/sourcegraph/conc"
	"github.com/sourcegraph/conc/pool"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var _ app.MasterServer = (*Server)(nil)

var (
	errMasterServerClosing = errors.New("master server: shutting down")
	ErrAlreadyRunning      = errors.New("master server: already running")
)

type startupState uint8

const (
	startupIdle startupState = iota
	startupPreparing
	startupRunning
	startupClosing
)

type Server struct {
	Cfg              *config.MasterRuntimeConfig
	Logger           *zap.Logger
	DB               *gorm.DB
	Bus              app.EventBus
	Router           *gin.Engine
	Version          atomic.Int64
	lastSavedVersion atomic.Int64
	Hub              *msync.Hub
	RelayHub         *mastertunnel.Hub
	RelayAdmission   *mastertunnel.AdmissionGate
	InstanceID       string
	Signer           *masteragentauth.Signer
	Connections      *connectivity.Service
	ProbeScheduler   *connectivity.Scheduler
	Operations       *masteroperations.Service
	Listener         net.Listener
	httpSrv          *http.Server
	App              app.Application
	MetricsRegistry  *prometheus.Registry
	RelayMetrics     *pkgmetrics.AgentRelayMetrics

	// Heartbeat captures agent last_seen in memory and periodically flushes
	// to DB; also serves freshness reads for API enrichment. Started in Run
	// and stopped (force-flushed) in Shutdown.
	Heartbeat *msync.HeartbeatTracker

	// Aggregator buffers per-key billing rollup deltas (token_daily /
	// channel_daily / hourly_bucket) in memory and flushes them in
	// batched UPSERTs. Settler hands off each committed UsageLog to it
	// via the UsageAggregator interface. Started in Run; Stop() in
	// Shutdown drains the final batch before Heartbeat.Stop.
	Aggregator *billing.Aggregator

	// RebuildRunner schedules async per-hour billing rollup rebuilds.
	// Submitted jobs run as background goroutines (one per Submit);
	// the gc loop spawns inside NewRebuildRunner. Stopped in Shutdown
	// between Aggregator and Heartbeat (spec §9).
	RebuildRunner *billing.RebuildRunner

	// LimitEvaluator periodically evaluates per-channel usage limits,
	// toggling Status + LimitState. Stopped in Shutdown.
	LimitEvaluator *billing.LimitEvaluator

	// BYOKProvider 是 BYOK cipher 的注入点。private_channel.Handler
	// 通过它获取 *Cipher，避免污染 app.Application 顶层接口。
	BYOKProvider byokcrypto.Provider

	channelHandler     *channel.Handler
	embeddedAgent      *agent.Server
	embeddedBackground *agent.PreparedBackground
	oauthAllowlist     *apioauth.Allowlist
	oauthHandler       *apioauth.Handler

	lifecycleOnce      sync.Once
	lifecycleMu        sync.Mutex
	rootCtx            context.Context
	rootCancel         context.CancelCauseFunc
	registrationCtx    context.Context
	registrationCancel context.CancelCauseFunc
	done               chan struct{}
	closing            bool
	startupState       startupState
	startupGeneration  uint64
	startupLease       *registrationLease
	shutdownErr        error
	shutdownOnce       sync.Once
	workers            conc.WaitGroup
	activeWorkers      atomic.Int64
	httpHandlers       atomic.Int64
	acceptedSockets    atomic.Int64
	httpHandlerChanged chan struct{}

	afterRunRegistration   func()
	afterShutdownAdmission func()
	beforeSetupEmbedded    func()
	afterEmbeddedConstruct func(*agent.Server)
	beforeRunCommit        func()
	beforeRunRelease       func()
}

type relayAgentLookup struct {
	application dao.AppProvider
	control     *msync.Hub
}

type masterOperationAgentFinder struct{ application dao.AppProvider }

func (f masterOperationAgentFinder) FindAgent(ctx context.Context, agentID string) (models.Agent, error) {
	agent, err := dao.NewAdminQuery(dao.NewContextWithContext(f.application, ctx)).Agent().GetByAgentID(agentID)
	if err != nil {
		return models.Agent{}, err
	}
	return *agent, nil
}

func (l relayAgentLookup) GetByAgentID(ctx context.Context, agentID string) (*models.Agent, error) {
	return dao.NewAdminQuery(dao.NewContextWithContext(l.application, ctx)).Agent().GetByAgentID(agentID)
}

func (l relayAgentLookup) Capabilities(agentID string) []string {
	if l.control == nil {
		return nil
	}
	return l.control.Capabilities(agentID)
}

func (l relayAgentLookup) GetRelayRuntime(agentID string) (connectivity.RelayRuntimeFact, bool) {
	if l.control == nil {
		return connectivity.RelayRuntimeFact{}, false
	}
	fact, ok := l.control.GetControlSession(agentID)
	if !ok || fact.Runtime == nil || fact.Runtime.Relay == nil {
		return connectivity.RelayRuntimeFact{}, false
	}
	return *fact.Runtime.Relay, true
}

const sqliteWALPragma = "_pragma=journal_mode(WAL)"

// sqliteBusyTimeoutPragma: 回填 rebuild 等长写事务持锁时,给并发 settle 30s 等锁
// 窗口,而不是 5s 就 SQLITE_BUSY。
const sqliteBusyTimeoutPragma = "_pragma=busy_timeout(30000)"

func isSQLiteMemoryDSN(dsn string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dsn))
	return normalized == ":memory:" ||
		strings.Contains(normalized, "mode=memory") ||
		strings.Contains(normalized, "::memory:")
}

func withSQLiteWAL(dsn string) string {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" || isSQLiteMemoryDSN(trimmed) {
		return trimmed
	}

	normalized := strings.ToLower(trimmed)
	if strings.Contains(normalized, "journal_mode(wal)") {
		return trimmed
	}
	if strings.Contains(trimmed, "?") {
		return trimmed + "&" + sqliteWALPragma
	}
	return trimmed + "?" + sqliteWALPragma
}

// withSQLiteBusyTimeout ensures a busy_timeout pragma is present in the sqlite
// DSN so concurrent writers (settle) wait up to 30s for the write lock
// instead of failing immediately with SQLITE_BUSY when a long rebuild
// transaction is holding it. Mirrors withSQLiteWAL: idempotent, and leaves
// non-file/memory DSNs untouched the same way withSQLiteWAL does.
func withSQLiteBusyTimeout(dsn string) string {
	trimmed := strings.TrimSpace(dsn)
	if trimmed == "" || isSQLiteMemoryDSN(trimmed) {
		return trimmed
	}

	normalized := strings.ToLower(trimmed)
	if strings.Contains(normalized, "busy_timeout(") {
		return trimmed
	}
	if strings.Contains(trimmed, "?") {
		return trimmed + "&" + sqliteBusyTimeoutPragma
	}
	return trimmed + "?" + sqliteBusyTimeoutPragma
}

func sqliteDirFromDSN(dsn string) string {
	base := dsn
	if i := strings.Index(base, "?"); i >= 0 {
		base = base[:i]
	}
	return filepath.Dir(base)
}

func New(cfg config.MasterRuntimeProvider, logger *zap.Logger) (*Server, error) {
	runtimeCfg := cfg.ToMasterRuntimeConfig()
	if runtimeCfg == nil {
		return nil, fmt.Errorf("master runtime config is required")
	}

	sqliteDSN := withSQLiteBusyTimeout(withSQLiteWAL(runtimeCfg.Master.DBPath))

	// Skip directory creation for in-memory databases
	if !isSQLiteMemoryDSN(sqliteDSN) {
		dir := sqliteDirFromDSN(sqliteDSN)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create data dir: %w", err)
		}
	}

	db, err := gorm.Open(sqlite.Open(sqliteDSN), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql db: %w", err)
	}
	dbOwned := true
	defer func() {
		if dbOwned {
			_ = sqlDB.Close()
		}
	}()
	if isSQLiteMemoryDSN(sqliteDSN) {
		// SQLite :memory: is per connection. Keep a single connection so all
		// requests and goroutines see the same in-memory schema/data.
		sqlDB.SetMaxOpenConns(1)
		sqlDB.SetMaxIdleConns(1)
	}

	if err := models.AutoMigrate(db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	if err := models.SeedDefaultUserGroup(db); err != nil {
		return nil, fmt.Errorf("seed default user group: %w", err)
	}

	if err := models.SeedBYOKSettings(db); err != nil {
		return nil, fmt.Errorf("seed byok settings: %w", err)
	}

	byokCipher, err := byokcrypto.NewFromConfig(runtimeCfg.Master.BYOKKEK, runtimeCfg.Master.JWTSecret)
	if err != nil {
		return nil, fmt.Errorf("init byok cipher: %w", err)
	}
	byokProvider := byokcrypto.NewStaticProvider(byokCipher)

	bus := eventbus.NewMemoryBus()
	metricsRegistry := prometheus.NewRegistry()

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(bus)

	s := &Server{
		Cfg:             runtimeCfg,
		Logger:          logger,
		DB:              db,
		Bus:             bus,
		Router:          gin.New(),
		App:             application,
		BYOKProvider:    byokProvider,
		InstanceID:      uuid.NewString(),
		MetricsRegistry: metricsRegistry,
		RelayMetrics:    pkgmetrics.NewAgentRelayMetrics(metricsRegistry, metricsRegistry),
	}
	s.initLifecycle()
	signer, err := masteragentauth.NewSigner(
		context.Background(),
		dao.NewMasterSigningKeyStore(db),
		s.InstanceID,
		masteragentauth.SignerOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("init agent ticket signer: %w", err)
	}
	s.Signer = signer

	allowlist, err := apioauth.NewAllowlist(runtimeCfg.Master.PublicBaseURLs)
	if err != nil {
		return nil, fmt.Errorf("oauth allowlist: %w", err)
	}
	s.oauthAllowlist = allowlist

	// Load persisted version from DB
	s.loadVersion()

	s.Hub = msync.NewHub(
		application,
		logger,
		bus,
		func() int64 { return s.Version.Load() },
		byokProvider.GetCipher(),
		msync.HubOptions{
			MasterInstanceID: s.InstanceID,
			Capabilities: []string{
				protocol.AgentCapabilityForwardV1,
				protocol.AgentCapabilityTunnelV1,
			},
			AgentTicketSigner: s.Signer,
		},
	)

	// Heartbeat tracker — memory-first last_seen + config fingerprint.
	// Flush interval is admin-configurable; falls back to 300s if unset.
	flushSec := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"agent.heartbeat_flush_interval_seconds", 300,
	)
	s.Heartbeat = msync.NewHeartbeatTracker(application, logger, time.Duration(flushSec)*time.Second)
	s.Heartbeat.SetLastSeenPersistContextFn(func(ctx context.Context, updates map[string]int64) error {
		return dao.NewAdminMutation(dao.NewContextWithContext(application, ctx)).Agent().BatchUpdateLastSeen(updates)
	})
	s.Hub.Heartbeat = s.Heartbeat
	settingQuery := dao.NewAdminQuery(dao.NewContext(application)).Setting()
	controlHeartbeatDegradedSec := lookupIntInRange(
		settingQuery,
		"agent.control_heartbeat_degraded_seconds",
		90,
		10,
		3600,
	)
	controlHealthRecoverySamples := lookupIntInRange(
		settingQuery,
		"agent.control_health_recovery_samples",
		2,
		1,
		10,
	)
	s.RelayAdmission = &mastertunnel.AdmissionGate{}
	s.RelayAdmission.Set(settingQuery.LookupInt(consts.SettingAgentRelayFallbackEnabled, 0) == 1)
	s.RelayHub = mastertunnel.NewHub(mastertunnel.HubOptions{
		InstanceID: s.InstanceID,
		Signer:     s.Signer,
		Agents: relayAgentLookup{
			application: s.App,
			control:     s.Hub,
		},
		Admission: s.RelayAdmission,
		Limits: pkgtunnel.Limits{
			MaxMetadataBytes:      pkgtunnel.MaxV1PayloadBytes,
			MaxDataBytes:          pkgtunnel.MaxV1PayloadBytes,
			InitialStreamWindow:   256 * 1024,
			MaxQueuedSessionBytes: 4 * 1024 * 1024,
			MaxConcurrentStreams:  128,
		},
		Logger:  logger.Named("relay-tunnel"),
		Metrics: s.RelayMetrics,
		RuntimeSettings: func() mastertunnel.RuntimeSettings {
			return mastertunnel.RuntimeSettings{
				Limits: pkgtunnel.Limits{
					MaxMetadataBytes:      int64(settingQuery.LookupInt("agent.tunnel_max_metadata_bytes", 65536)),
					MaxDataBytes:          int64(settingQuery.LookupInt("agent.tunnel_max_data_bytes", 65536)),
					InitialStreamWindow:   int64(settingQuery.LookupInt("agent.tunnel_initial_window_bytes", 524288)),
					MaxQueuedSessionBytes: int64(settingQuery.LookupInt("agent.tunnel_max_session_queue_bytes", 8388608)),
					MaxConcurrentStreams:  settingQuery.LookupInt("agent.tunnel_max_streams", 256),
				},
				DrainTimeout: time.Duration(settingQuery.LookupInt("agent.tunnel_drain_timeout_seconds", 300)) * time.Second,
			}
		},
	})
	s.Connections = connectivity.NewService(s.InstanceID, connectivity.Sources{Control: s.Hub, Relay: s.RelayHub}, connectivity.Options{
		HeartbeatDegradedAfter: time.Duration(controlHeartbeatDegradedSec) * time.Second,
		RecoverySamples:        controlHealthRecoverySamples,
		Logger:                 logger.Named("connectivity"),
	})
	s.Hub.RouteObservations = s.Connections
	s.Hub.SetControlSessionRemoved(s.Connections.Forget)
	s.ProbeScheduler = connectivity.NewScheduler(masterProbeCaller{control: s.Hub}, s.Connections, connectivity.SchedulerOptions{
		ProbeTargetFinder: masterProbeTargetFinder{application: s.App, control: s.Hub, globalProxy: runtimeCfg.Master.ProxyURL},
		SuccessTTL:        time.Duration(settingQuery.LookupInt(consts.SettingAgentConnectivityProbeSuccessTTLSeconds, 300)) * time.Second,
		FailureRetryMin:   time.Duration(settingQuery.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMinSeconds, 30)) * time.Second,
		FailureRetryMax:   time.Duration(settingQuery.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds, 300)) * time.Second,
	})
	s.Operations = masteroperations.NewService(s.lifecycleContext(), masterOperationAgentFinder{application: s.App}, masteroperations.Sources{
		Connections: s.Connections,
		Control:     s.Hub,
		Relay:       s.RelayHub,
		Probes:      s.ProbeScheduler,
	})

	// ws 出站消息分级(anti-flapping 单元②):队列大小/背压超时 settings 可覆盖,
	// 默认与 internal/pkg/ws 包内 var 初值一致。
	ws.SendQueueSize = dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"agent.ws_send_queue_size", 1024,
	)
	ws.WriteDeadline = time.Duration(dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"agent.ws_write_deadline_seconds", 10,
	)) * time.Second

	warnIfPlaintextAgentChannel(logger, runtimeCfg.Master.PublicBaseURLs)

	publisher := msync.NewPublisher(s.Hub, bus, &s.Version, logger)
	publisher.Start()

	// Aggregator: buffer rollup writes in memory; flush in batches.
	// Settings provide override; defaults match spec (30s tick / 5000 row cap).
	aggFlushSec := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"billing.aggregator_flush_interval_seconds", 30,
	)
	aggMaxRows := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"billing.aggregator_max_buffered_rows", 5000,
	)
	aggregator := billing.NewAggregator(application, logger, billing.AggregatorOptions{
		FlushEvery: time.Duration(aggFlushSec) * time.Second,
		MaxRows:    aggMaxRows,
	})
	aggregator.SetFlushContextFns(
		func(ctx context.Context, rows []dao.TokenDailyRow) error {
			return dao.NewAdminMutation(dao.NewContextWithContext(application, ctx)).Billing().BatchUpsertTokenDaily(rows)
		},
		func(ctx context.Context, rows []dao.ChannelDailyRow) error {
			return dao.NewAdminMutation(dao.NewContextWithContext(application, ctx)).Billing().BatchUpsertChannelDaily(rows)
		},
		func(ctx context.Context, rows []dao.HourlyBucketRow) error {
			return dao.NewAdminMutation(dao.NewContextWithContext(application, ctx)).Billing().BatchUpsertHourlyBucket(rows)
		},
		func(ctx context.Context, rows []dao.DurationHistogramRow) error {
			return dao.NewAdminMutation(dao.NewContextWithContext(application, ctx)).Billing().BatchUpsertDurationHistogram(rows)
		},
	)
	s.Aggregator = aggregator

	// RebuildRunner: per-hour async rebuild scheduler. Terminal-job retention
	// is admin-configurable; default 24h. Production sliceFn falls back to
	// dao.RebuildHourSlice (set inside run() when SliceFn is nil + app non-nil).
	retainSec := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"billing.rebuild_job_retain_seconds", 86400,
	)
	s.RebuildRunner = billing.NewRebuildRunner(application, logger, time.Duration(retainSec)*time.Second)

	settler := billing.NewSettlerWithAggregator(application, bus, logger, aggregator)
	settler.Start()
	// 数据面同步结算入口:HTTP 摄取(POST /api/agents/usage)只在 SettleBatch 落库
	// 成功后才 200,把 ack 语义从"进了内存 bus"收紧到"已持久化"。ws 路径(usage.reported
	// 订阅)不受影响,仍走上面 settler.Start() 的异步 Settle。
	s.Hub.SettleUsage = settler.SettleBatch
	checker := billing.NewQuotaChecker(application, bus, logger)
	checker.Start()

	limitEvalSec := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"channel.limit_eval_interval_seconds", 30,
	)
	s.LimitEvaluator = billing.NewLimitEvaluator(application, bus, logger, time.Duration(limitEvalSec)*time.Second)

	s.setupMiddleware()
	s.setupRoutes()

	dbOwned = false
	return s, nil
}

func lookupIntInRange(query dao.AdminSettingQuery, key string, fallback, minimum, maximum int) int {
	value := query.LookupInt(key, fallback)
	if value < minimum || value > maximum {
		return fallback
	}
	return value
}

func (s *Server) setupMiddleware() {
	s.Router.Use(gin.Recovery(), ginutil.AbortHandlerRecovery())
}

func (s *Server) setupRoutes() {
	s.Router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "role": "master"})
	})
	s.setupMetricsRoute()

	adapter := api.NewAdapter(s.Cfg, s.Logger, s.App)
	userH := &user.Handler{Bus: s.Bus}
	tokenH := &token.Handler{}
	s.channelHandler = &channel.Handler{Hub: s.Hub, MasterListen: s.Cfg.Master.Listen}
	channelH := s.channelHandler
	modelH := &model.Handler{}
	agentH := &apiagent.Handler{
		GetOnlineAgentIDs:    s.Hub.GetOnlineAgentIDs,
		GetRuntime:           s.Hub.GetRuntime,
		RevokeControlSession: s.Hub.RevokeControlSession,
		GetProbeProgress:     s.ProbeScheduler.FindProgressForSource,
		Connections:          s.Connections,
		ControlSessions:      s.Hub,
		Operations:           s.Operations,
		HubCallSession:       s.Hub.CallSession,
		Hub:                  s.Hub,
	}
	obsH := &apiobservability.Handler{
		HubCall:           s.Hub.Call,
		GetOnlineAgentIDs: s.Hub.GetOnlineAgentIDs,
	}
	cacheH := &apicache.Handler{
		GetOnlineAgentIDs: s.Hub.GetOnlineAgentIDs,
		GetRuntime:        s.Hub.GetRuntime,
		Tracker:           s.Heartbeat,
	}

	systemH := &apisystem.Handler{
		ConnectedCount: s.Hub.ConnectedAgents,
		RelayAdmission: s.RelayAdmission,
		RefreshProbeTimings: func(ctx context.Context) {
			if s.ProbeScheduler == nil {
				return
			}
			settings := dao.NewAdminQuery(dao.NewContextWithContext(s.App, ctx)).Setting()
			s.ProbeScheduler.SetTimings(
				time.Duration(settings.LookupInt(consts.SettingAgentConnectivityProbeSuccessTTLSeconds, 300))*time.Second,
				time.Duration(settings.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMinSeconds, 30))*time.Second,
				time.Duration(settings.LookupInt(consts.SettingAgentConnectivityProbeFailureRetryMaxSeconds, 300))*time.Second,
			)
		},
	}

	// Public endpoints
	s.Router.POST("/api/login", api.Adapt(adapter, api.BindJSON, userH.Login))
	s.Router.POST("/api/register", api.Adapt(adapter, api.BindJSON, userH.Register))
	s.Router.GET("/api/system/public-config", api.Adapt(adapter, api.BindNone, systemH.PublicConfig))
	s.Router.POST("/api/agents/enroll", api.Adapt(adapter, api.BindJSON, agentH.Enroll))

	s.oauthHandler = apioauth.NewHandler(s.App, s.Bus, s.Cfg.Master.JWTSecret, s.oauthAllowlist)
	oauthH := s.oauthHandler
	s.Router.GET("/api/oauth/providers", api.Adapt(adapter, api.BindNone, oauthH.ListPublicProviders))
	s.Router.GET("/api/oauth/:provider/authorize", oauthH.HandleAuthorize)
	s.Router.GET("/api/oauth/:provider/callback", oauthH.HandleCallback)
	s.Router.POST("/api/oauth/bind", api.Adapt(adapter, api.BindJSON, oauthH.Bind))
	s.Router.POST("/api/oauth/register", api.Adapt(adapter, api.BindJSON, oauthH.Register))
	s.Router.GET("/api/oauth/:provider/link", oauthH.HandleLink)

	mrH := &apimodelrouting.Handler{Bus: s.Bus}
	capabilityH := &apicapability.Handler{}
	pcH := private_channel.NewHandler(s.App, s.BYOKProvider)

	// User-level authenticated routes (no admin required)
	userAuth := s.Router.Group("/api")
	userAuth.Use(middleware.AuthMiddleware(s.Cfg.Master.JWTSecret))
	userAuth.Use(middleware.ScopeMiddleware())
	userAuth.GET("/profile", api.Adapt(adapter, api.BindNone, userH.GetProfile))
	userAuth.GET("/capabilities", api.Adapt(adapter, api.BindNone, capabilityH.Get))
	userAuth.PUT("/profile", api.Adapt(adapter, api.BindJSON, userH.UpdateProfile))
	userAuth.PUT("/profile/password", api.Adapt(adapter, api.BindJSON, userH.ChangePassword))
	userAuth.POST("/oauth/link-ticket", api.Adapt(adapter, api.BindNone, oauthH.IssueLinkTicket))
	userAuth.GET("/oauth/identities", api.Adapt(adapter, api.BindNone, oauthH.ListMyIdentities))
	userAuth.DELETE("/oauth/identities/:id", api.Adapt(adapter, api.BindURI, oauthH.DeleteIdentity))
	tplH := &token_template.Handler{}
	inviteH := &apiinvite.Handler{}
	ugH := &user_group.Handler{Bus: s.Bus}
	oapH := &apioap.Handler{Bus: s.Bus}
	userAuth.GET("/token-templates", api.Adapt(adapter, api.BindQuery, tplH.ListEnabled))
	userAuth.GET("/invite-codes", api.Adapt(adapter, api.BindQuery, inviteH.ListMine))
	userAuth.POST("/invite-codes", api.Adapt(adapter, api.BindJSON, inviteH.Create))
	userAuth.DELETE("/invite-codes/:id", api.Adapt(adapter, api.BindURI, inviteH.DeleteMine))

	// Portal model-routings (user-owned, scope forced to user)
	userAuth.GET("/model-routings", api.Adapt(adapter, api.BindQuery, mrH.PortalList))
	userAuth.POST("/model-routings", api.Adapt(adapter, api.BindJSON, mrH.PortalCreate))
	userAuth.GET("/model-routings/global-routing-names", api.Adapt(adapter, api.BindNone, mrH.PortalGlobalRoutingNames))
	userAuth.POST("/model-routings/preview", api.Adapt(adapter, api.BindJSON, mrH.Preview))
	userAuth.GET("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.PortalGet))
	userAuth.PUT("/model-routings/:id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.PortalUpdate))
	userAuth.DELETE("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.PortalDelete))
	userAuth.GET("/tokens/:id/model-routings", api.Adapt(adapter, api.BindURIAndQuery, mrH.TokenList))
	userAuth.POST("/tokens/:id/model-routings", api.Adapt(adapter, api.BindURIAndJSON, mrH.TokenCreate))
	userAuth.POST("/tokens/:id/model-routings/preview", api.Adapt(adapter, api.BindURIAndJSON, mrH.TokenPreview))
	userAuth.GET("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURI, mrH.TokenGet))
	userAuth.PUT("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.TokenUpdate))
	userAuth.DELETE("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURI, mrH.TokenDelete))

	// Portal private-channels (BYOK)
	userAuth.GET("/private-channels", api.Adapt(adapter, api.BindQuery, pcH.PortalList))
	userAuth.POST("/private-channels", api.Adapt(adapter, api.BindJSON, pcH.Create))
	userAuth.POST("/private-channels/export", pcH.ExportHTTP(adapter))
	userAuth.POST("/private-channels/import", pcH.ImportHTTP(adapter))
	userAuth.GET("/private-channels/available-models", api.Adapt(adapter, api.BindNone, pcH.PortalAvailableModels))
	userAuth.GET("/private-channels/types", api.Adapt(adapter, api.BindNone, pcH.PortalSupportedTypes))
	userAuth.GET("/private-channels/:id", api.Adapt(adapter, api.BindURI, pcH.PortalGet))
	userAuth.PUT("/private-channels/:id", api.Adapt(adapter, api.BindURIAndBodyMap, pcH.PortalUpdate))
	userAuth.PUT("/private-channels/:id/key", api.Adapt(adapter, api.BindURIAndJSON, pcH.PortalUpdateKey))
	userAuth.DELETE("/private-channels/:id", api.Adapt(adapter, api.BindURI, pcH.PortalDelete))
	userAuth.POST("/private-channels/:id/test", api.Adapt(adapter, api.BindURIAndOptionalJSON, pcH.PortalTest))

	// Portal BYOK billing breakdowns (current-user scoped; spec §4.3 / Task 21)
	userAuth.GET("/private-channels/billing/overview", api.Adapt(adapter, api.BindQuery, pcH.BillingOverview))
	userAuth.GET("/private-channels/billing/by-channel", api.Adapt(adapter, api.BindQuery, pcH.BillingByChannel))
	userAuth.GET("/private-channels/billing/by-model", api.Adapt(adapter, api.BindQuery, pcH.BillingByModel))

	// Protected endpoints
	auth := s.Router.Group("/api/admin")
	auth.Use(middleware.AuthMiddleware(s.Cfg.Master.JWTSecret))
	auth.Use(middleware.AdminOnly())
	auth.Use(middleware.ScopeMiddleware())

	auth.GET("/users", api.Adapt(adapter, api.BindQuery, userH.List))
	auth.POST("/users", api.Adapt(adapter, api.BindJSON, userH.Create))
	auth.GET("/users/:id", api.Adapt(adapter, api.BindURI, userH.Get))
	auth.PUT("/users/:id", api.Adapt(adapter, api.BindURIAndBodyMap, userH.Update))
	auth.DELETE("/users/:id", api.Adapt(adapter, api.BindURI, userH.Delete))
	auth.PUT("/users/:id/quota", api.Adapt(adapter, api.BindURIAndJSON, userH.UpdateQuota))

	auth.GET("/token-templates", api.Adapt(adapter, api.BindQuery, tplH.List))
	auth.POST("/token-templates", api.Adapt(adapter, api.BindJSON, tplH.Create))
	auth.PUT("/token-templates/:id", api.Adapt(adapter, api.BindURIAndBodyMap, tplH.Update))
	auth.DELETE("/token-templates/:id", api.Adapt(adapter, api.BindURI, tplH.Delete))
	auth.POST("/token-templates/:id/sync-preview", api.Adapt(adapter, api.BindURIAndOptionalJSON, tplH.SyncPreview))
	auth.POST("/token-templates/:id/sync", api.Adapt(adapter, api.BindURIAndOptionalJSON, tplH.Sync))

	auth.GET("/invite-codes", api.Adapt(adapter, api.BindQuery, inviteH.AdminList))
	auth.DELETE("/invite-codes/:id", api.Adapt(adapter, api.BindURI, inviteH.AdminDelete))

	auth.GET("/user-groups", api.Adapt(adapter, api.BindQuery, ugH.List))
	auth.POST("/user-groups", api.Adapt(adapter, api.BindJSON, ugH.Create))
	auth.GET("/user-groups/:id", api.Adapt(adapter, api.BindURI, ugH.Get))
	auth.PUT("/user-groups/:id", api.Adapt(adapter, api.BindURIAndBodyMap, ugH.Update))
	auth.DELETE("/user-groups/:id", api.Adapt(adapter, api.BindURI, ugH.Delete))

	auth.GET("/oauth-providers", api.Adapt(adapter, api.BindNone, oapH.List))
	auth.POST("/oauth-providers", api.Adapt(adapter, api.BindJSON, oapH.Create))
	auth.GET("/oauth-providers/:id", api.Adapt(adapter, api.BindURI, oapH.Get))
	auth.PUT("/oauth-providers/:id", api.Adapt(adapter, api.BindURIAndBodyMap, oapH.Update))
	auth.DELETE("/oauth-providers/:id", api.Adapt(adapter, api.BindURI, oapH.Delete))

	auth.GET("/channels", api.Adapt(adapter, api.BindQuery, channelH.List))
	auth.POST("/channels", api.Adapt(adapter, api.BindJSON, channelH.Create))
	auth.POST("/channels/export", channelH.ExportHTTP(adapter))
	auth.POST("/channels/import", channelH.ImportHTTP(adapter))
	auth.GET("/channels/types", api.Adapt(adapter, api.BindNone, channelH.Types))
	auth.GET("/channels/:id", api.Adapt(adapter, api.BindURI, channelH.Get))
	auth.GET("/channels/:id/dataflow", api.Adapt(adapter, api.BindURI, channelH.DataFlow))
	auth.PUT("/channels/:id", api.Adapt(adapter, api.BindURIAndBodyMap, channelH.Update))
	auth.DELETE("/channels/:id", api.Adapt(adapter, api.BindURI, channelH.Delete))
	auth.POST("/channels/:id/test", api.Adapt(adapter, api.BindURIAndOptionalJSON, channelH.Test))
	auth.POST("/channels/fetch-models", api.Adapt(adapter, api.BindJSON, channelH.FetchModels))

	scriptH := &apiscript.Handler{}
	auth.GET("/scripts", api.Adapt(adapter, api.BindQuery, scriptH.List))
	auth.POST("/scripts", api.Adapt(adapter, api.BindJSON, scriptH.Create))
	auth.GET("/scripts/:id", api.Adapt(adapter, api.BindURI, scriptH.Get))
	auth.PUT("/scripts/:id", api.Adapt(adapter, api.BindURIAndBodyMap, scriptH.Update))
	auth.DELETE("/scripts/:id", api.Adapt(adapter, api.BindURI, scriptH.Delete))

	// Admin private-channels (BYOK cross-user view + kill switch)
	auth.GET("/private-channels", api.Adapt(adapter, api.BindQuery, pcH.AdminList))
	auth.GET("/private-channels/baseurl/usage", api.Adapt(adapter, api.BindQuery, pcH.AdminBaseURLUsage))
	auth.GET("/private-channels/:id", api.Adapt(adapter, api.BindURI, pcH.AdminGet))
	auth.POST("/private-channels/:id/disable", api.Adapt(adapter, api.BindURI, pcH.AdminDisable))

	auth.GET("/models", api.Adapt(adapter, api.BindQuery, modelH.List))
	auth.POST("/models", api.Adapt(adapter, api.BindJSON, modelH.Create))
	auth.GET("/models/:id", api.Adapt(adapter, api.BindURI, modelH.Get))
	auth.PUT("/models/:id", api.Adapt(adapter, api.BindURIAndBodyMap, modelH.Update))
	auth.DELETE("/models/:id", api.Adapt(adapter, api.BindURI, modelH.Delete))
	auth.POST("/models/sync", api.Adapt(adapter, api.BindNone, modelH.Sync))
	auth.POST("/models/fetch-pricing", api.Adapt(adapter, api.BindQuery, modelH.FetchPricing))
	auth.POST("/models/apply-pricing", api.Adapt(adapter, api.BindJSON, modelH.ApplyPricing))

	auth.GET("/agents", api.Adapt(adapter, api.BindQuery, agentH.List))
	auth.POST("/agents", api.Adapt(adapter, api.BindJSON, agentH.Create))
	auth.POST("/agents/full-sync", api.Adapt(adapter, api.BindJSON, agentH.FullSync))
	auth.POST("/agents/:id/operations/:operation", api.Adapt(adapter, api.BindURIAndJSON, agentH.Operation))
	auth.GET("/agents/:id", api.Adapt(adapter, api.BindURI, agentH.Get))
	auth.PUT("/agents/:id", api.Adapt(adapter, api.BindURIAndJSON, agentH.Update))
	auth.DELETE("/agents/:id", api.Adapt(adapter, api.BindURI, agentH.Delete))
	auth.POST("/agents/enrollment-token", api.Adapt(adapter, api.BindOptionalJSON, agentH.GenerateEnrollmentToken))
	auth.GET("/agents/online", api.Adapt(adapter, api.BindNone, agentH.Online))
	auth.GET("/agents/:id/detail", api.Adapt(adapter, api.BindURI, agentH.Detail))
	auth.GET("/agents/:id/connections/targets", api.Adapt(adapter, api.BindURIAndQuery, agentH.RouteTargets))
	auth.GET("/agents/:id/connections/diagnostics", api.Adapt(adapter, api.BindURI, agentH.ConnectionDiagnostics))
	auth.POST("/agents/:id/connectivity", api.Adapt(adapter, api.BindURIAndOptionalJSON, agentH.CheckConnectivity))
	auth.GET("/agents/:id/connectivity", api.Adapt(adapter, api.BindURIAndQuery, agentH.GetConnectivity))
	auth.GET("/agents/inflight", api.Adapt(adapter, api.BindQuery, agentH.GetInflight))
	auth.GET("/agents/inflight/all", api.Adapt(adapter, api.BindNone, agentH.GetAllInflight))
	auth.POST("/agents/inflight/interrupt", api.Adapt(adapter, api.BindJSON, agentH.Interrupt))
	auth.GET("/agents/goroutines", api.Adapt(adapter, api.BindQuery, agentH.GetGoroutines))
	auth.GET("/observability/limiter-usage", api.Adapt(adapter, api.BindNone, obsH.GetLimiterUsage))
	auth.GET("/observability/breaker-board", api.Adapt(adapter, api.BindNone, obsH.GetBreakerBoard))
	auth.GET("/observability/recent-health", api.Adapt(adapter, api.BindNone, obsH.GetRecentHealth))
	auth.GET("/observability/delivery-board", api.Adapt(adapter, api.BindNone, obsH.GetDeliveryBoard))
	auth.POST("/observability/delivery-op", api.Adapt(adapter, api.BindJSON, obsH.PostDeliveryOp))

	agentRouteH := &agent_route.Handler{}
	auth.GET("/agent-routes", api.Adapt(adapter, api.BindQuery, agentRouteH.List))
	auth.POST("/agent-routes", api.Adapt(adapter, api.BindJSON, agentRouteH.Create))
	auth.GET("/agent-routes/overview", api.Adapt(adapter, api.BindQuery, agentRouteH.Overview))
	auth.GET("/agent-routes/:id", api.Adapt(adapter, api.BindURI, agentRouteH.Get))
	auth.PUT("/agent-routes/:id", api.Adapt(adapter, api.BindURIAndJSON, agentRouteH.Update))
	auth.DELETE("/agent-routes/:id", api.Adapt(adapter, api.BindURI, agentRouteH.Delete))

	rateLimiterH := &apiratelimiter.Handler{}
	auth.GET("/rate-limiters", api.Adapt(adapter, api.BindQuery, rateLimiterH.List))
	auth.POST("/rate-limiters", api.Adapt(adapter, api.BindJSON, rateLimiterH.Create))
	auth.PUT("/rate-limiters/:id", api.Adapt(adapter, api.BindURIAndBodyMap, rateLimiterH.Update))
	auth.DELETE("/rate-limiters/:id", api.Adapt(adapter, api.BindURI, rateLimiterH.Delete))
	auth.GET("/limiter-bindings", api.Adapt(adapter, api.BindQuery, rateLimiterH.ListBindings))
	auth.POST("/limiter-bindings", api.Adapt(adapter, api.BindJSON, rateLimiterH.CreateBinding))
	auth.DELETE("/limiter-bindings/:id", api.Adapt(adapter, api.BindURI, rateLimiterH.DeleteBinding))

	auth.GET("/model-routings", api.Adapt(adapter, api.BindQuery, mrH.List))
	auth.POST("/model-routings", api.Adapt(adapter, api.BindJSON, mrH.Create))
	auth.GET("/model-routings/candidates", api.Adapt(adapter, api.BindNone, mrH.Candidates))
	auth.POST("/model-routings/preview", api.Adapt(adapter, api.BindJSON, mrH.Preview))
	auth.GET("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.Get))
	auth.PUT("/model-routings/:id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.Update))
	auth.DELETE("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.Delete))
	auth.GET("/tokens/:id/model-routings", api.Adapt(adapter, api.BindURIAndQuery, mrH.TokenList))
	auth.POST("/tokens/:id/model-routings", api.Adapt(adapter, api.BindURIAndJSON, mrH.TokenCreate))
	auth.POST("/tokens/:id/model-routings/preview", api.Adapt(adapter, api.BindURIAndJSON, mrH.TokenPreview))
	auth.GET("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURI, mrH.TokenGet))
	auth.PUT("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.TokenUpdate))
	auth.DELETE("/tokens/:id/model-routings/:routing_id", api.Adapt(adapter, api.BindURI, mrH.TokenDelete))

	logH := &apilog.Handler{}
	billingH := &apibilling.Handler{Runner: s.RebuildRunner}
	statsH := &stats.Handler{ConnectedCount: s.Hub.ConnectedAgents}
	monitoringH := &apimonitoring.Handler{}
	insightsH := &apiinsights.Handler{Tracker: s.Heartbeat}

	// Token/Log/Stats routes on userAuth (accessible by all authenticated users)
	userAuth.GET("/tokens", api.Adapt(adapter, api.BindQuery, tokenH.List))
	userAuth.POST("/tokens", api.Adapt(adapter, api.BindJSON, tokenH.Create))
	userAuth.GET("/tokens/:id", api.Adapt(adapter, api.BindURI, tokenH.Get))
	userAuth.PUT("/tokens/:id", api.Adapt(adapter, api.BindURIAndBodyMap, tokenH.Update))
	userAuth.DELETE("/tokens/:id", api.Adapt(adapter, api.BindURI, tokenH.Delete))

	userAuth.GET("/logs", api.Adapt(adapter, api.BindQuery, logH.List))
	userAuth.GET("/logs/:request_id/trace", api.Adapt(adapter, api.BindURI, logH.GetTrace))
	userAuth.GET("/billing/tokens", api.Adapt(adapter, api.BindQuery, billingH.ListTokens))
	userAuth.GET("/billing/tokens/:token_id/daily", api.Adapt(adapter, api.BindURIAndQuery, billingH.TokenDaily))
	userAuth.GET("/billing/overview", api.Adapt(adapter, api.BindQuery, billingH.Overview))

	userAuth.GET("/stats/overview", api.Adapt(adapter, api.BindNone, statsH.Overview))
	userAuth.GET("/stats/trend", api.Adapt(adapter, api.BindQuery, statsH.Trend))
	userAuth.GET("/stats/byok-overview", api.Adapt(adapter, api.BindNone, statsH.BYOKOverview))

	// Observability v1: dashboard / billing insights / logs insights (all users; admin/user scope
	// 由各 handler 内部按 RequestScope 区分);monitoring insights / generic insights 仅 admin。
	userAuth.GET("/stats/dashboard", api.Adapt(adapter, api.BindQuery, statsH.Dashboard))
	userAuth.GET("/billing/insights", api.Adapt(adapter, api.BindQuery, billingH.Insights))
	userAuth.GET("/logs/insights", api.Adapt(adapter, api.BindQuery, logH.Insights))
	auth.GET("/monitoring/insights", api.Adapt(adapter, api.BindQuery, monitoringH.Insights))
	auth.GET("/insights", api.Adapt(adapter, api.BindQuery, insightsH.Get))

	// Backward-compatible aliases on admin group (deprecated)
	auth.GET("/tokens", api.Adapt(adapter, api.BindQuery, tokenH.List))
	auth.POST("/tokens", api.Adapt(adapter, api.BindJSON, tokenH.Create))
	auth.GET("/tokens/:id", api.Adapt(adapter, api.BindURI, tokenH.Get))
	auth.PUT("/tokens/:id", api.Adapt(adapter, api.BindURIAndBodyMap, tokenH.Update))
	auth.DELETE("/tokens/:id", api.Adapt(adapter, api.BindURI, tokenH.Delete))

	auth.GET("/logs", api.Adapt(adapter, api.BindQuery, logH.List))
	auth.GET("/logs/:request_id/trace", api.Adapt(adapter, api.BindURI, logH.GetTrace))
	auth.GET("/billing/channels", api.Adapt(adapter, api.BindQuery, billingH.ListChannels))
	auth.GET("/billing/channels/:channel_id/daily", api.Adapt(adapter, api.BindURIAndQuery, billingH.ChannelDaily))
	auth.POST("/billing/rebuild", api.Adapt(adapter, api.BindOptionalJSON, billingH.Rebuild))
	auth.GET("/billing/rebuild/jobs", api.Adapt(adapter, api.BindNone, billingH.ListRebuildJobs))
	auth.GET("/billing/rebuild/jobs/:id", api.Adapt(adapter, api.BindURI, billingH.GetRebuildJob))

	auth.GET("/stats", api.Adapt(adapter, api.BindNone, statsH.Overview))

	auth.GET("/system/stats", api.Adapt(adapter, api.BindNone, systemH.Stats))
	auth.GET("/system/cleanup/preview", api.Adapt(adapter, api.BindQuery, systemH.CleanupPreview))
	auth.POST("/system/cleanup", api.Adapt(adapter, api.BindJSON, systemH.Cleanup))
	auth.GET("/system/settings", api.Adapt(adapter, api.BindNone, systemH.GetSettings))
	auth.PUT("/system/settings", api.Adapt(adapter, api.BindJSON, systemH.UpdateSettings))
	auth.GET("/byok-system-baseurls", api.Adapt(adapter, api.BindNone, systemH.BYOKSystemBaseURLs))

	auth.GET("/cache/stats", api.Adapt(adapter, api.BindNone, cacheH.Stats))

	// WebSocket endpoint for agent sync
	s.Router.GET("/ws/agent", func(c *gin.Context) {
		s.Hub.HandleWS(c)
	})
	s.Router.GET("/ws/agent-relay", func(c *gin.Context) {
		s.RelayHub.HandleWS(c)
	})

	// HTTP usage ingest endpoint (acked alternative to the ws usage.reported notify)
	s.Router.POST("/api/agents/usage", func(c *gin.Context) {
		s.Hub.HandleUsageHTTP(c)
	})

	s.setupStaticRoutes()
}

func (s *Server) setupMetricsRoute() {
	if s != nil && s.Router != nil && s.RelayMetrics != nil {
		s.Router.GET("/metrics", gin.WrapH(s.RelayMetrics.Handler()))
	}
}

// SetChannelMasterListen overrides the channel handler's MasterListen
// after master.New() has run. Used by tests that bind a real listener
// (which yields the actual port) only after server construction.
func (s *Server) SetChannelMasterListen(addr string) {
	s.channelHandler.MasterListen = addr
}

// SetupEmbeddedAgentForTest mounts the embedded agent relay routes on the
// master router using the given listen address. This is the test-only escape
// hatch that replicates the production path in Run() without requiring a real
// net.Listener. Call it after httptest.NewServer so you have the actual port.
func (s *Server) SetupEmbeddedAgentForTest(listenAddr string) error {
	return s.setupEmbeddedAgent(context.Background(), listenAddr)
}

// GetEmbeddedAgentStore returns the embedded agent's cache store. Tests use
// this to wait for cache sync barriers (e.g. polling until __system_test__
// token is visible to the relay's auth middleware).
//
// Returns nil if embedded agent has not been set up yet.
func (s *Server) GetEmbeddedAgentStore() *cache.Store {
	if s.embeddedAgent == nil {
		return nil
	}
	return s.embeddedAgent.Store
}

func (s *Server) setupStaticRoutes() {
	assets, err := webassets.GetAssets()
	if err != nil {
		s.Logger.Warn("web assets unavailable, static routes disabled", zap.Error(err))
		return
	}
	if _, err := fs.Stat(assets, "index.html"); err != nil {
		s.Logger.Warn("web index.html not found, static routes disabled", zap.Error(err))
		return
	}

	s.setupStaticRoutesFromFS(assets)
}

func (s *Server) setupStaticRoutesFromFS(assets fs.FS) {
	fileServer := http.FileServer(http.FS(assets))
	indexHTML, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		s.Logger.Warn("failed to read web index.html, static routes disabled", zap.Error(err))
		return
	}

	s.Router.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if isAPIOrWSPath(path) {
			c.JSON(http.StatusNotFound, gin.H{"error": consts.ErrNotFound})
			return
		}

		// Next export outputs route HTML under <route>/index.html.
		if !strings.Contains(path, ".") {
			routePath := strings.Trim(path, "/")
			if routePath != "" && !strings.Contains(routePath, "..") {
				if routeHTML, err := fs.ReadFile(assets, routePath+"/index.html"); err == nil {
					c.Data(http.StatusOK, "text/html; charset=utf-8", routeHTML)
					return
				}
			}

			// Unknown app routes fallback to root index for client-side handling.
			c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)
			return
		}
		fileServer.ServeHTTP(c.Writer, c.Request)
	})
}

// warnIfPlaintextAgentChannel 检查 public_base_urls 是否含 http:// 项。
// 含有意味着外部（含 agent 回连 master）很可能走明文：BYOK API key 经
// master-agent WS 通道下发时会被中间人嗅探。生产请用 wss://。
// 不阻断启动，让运维自行权衡。
func warnIfPlaintextAgentChannel(logger *zap.Logger, publicBaseURLs []string) {
	if logger == nil {
		return
	}
	for _, raw := range publicBaseURLs {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			continue
		}
		if strings.EqualFold(parsed.Scheme, "http") || strings.EqualFold(parsed.Scheme, "ws") {
			logger.Warn(
				"master public_base_urls contains a plaintext entry; BYOK keys and agent credentials will traverse in plaintext over the master-agent WS channel. Use https:// (wss://) in production.",
				zap.String("entry", raw),
			)
		}
	}
}

func isAPIOrWSPath(path string) bool {
	return path == "/api" ||
		strings.HasPrefix(path, "/api/") ||
		path == "/ws" ||
		strings.HasPrefix(path, "/ws/")
}

func (s *Server) InitAdminUser(username, password string) error {
	var count int64
	s.DB.Model(&models.User{}).Where("role = 2").Count(&count)
	if count > 0 {
		return nil
	}
	hashed, _ := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return s.DB.Create(&models.User{
		Username: username,
		Password: string(hashed),
		Role:     2,
		Status:   1,
	}).Error
}

func (s *Server) loadVersion() {
	var setting models.Setting
	if err := s.DB.Where("key = ?", "version").First(&setting).Error; err == nil {
		if v, err := strconv.ParseInt(setting.Value, 10, 64); err == nil {
			s.Version.Store(v)
			s.Logger.Info("loaded version from DB", zap.Int64("version", v))
		}
	}
	// 不存在则插入 placeholder，让后续 saveVersion 走纯 UPDATE 不需要 INSERT
	s.DB.Where(models.Setting{Key: "version"}).
		Attrs(models.Setting{Value: "0"}).
		FirstOrCreate(&models.Setting{})
	s.lastSavedVersion.Store(s.Version.Load())
}

func (s *Server) saveVersion(ctx context.Context) {
	current := s.Version.Load()
	if current == s.lastSavedVersion.Load() {
		return
	}
	v := strconv.FormatInt(current, 10)
	if err := s.DB.WithContext(ctx).Model(&models.Setting{}).
		Where("key = ?", "version").
		Update("value", v).Error; err != nil {
		s.Logger.Warn("saveVersion failed", zap.Error(err))
		return
	}
	s.lastSavedVersion.Store(current)
}

func (s *Server) startVersionPersistence(ctx context.Context) bool {
	return s.startLifecycleWorker(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.saveVersion(ctx)
			}
		}
	})
}

// buildEmbeddedAgentConfig 由 master 配置派生内嵌 agent 的运行配置。
// 可下发字段(LogLevel/Relay/Runtime/Agent.Cache 等)从 master 配置透传;
// 引导/身份字段(Listen/MasterURL)由 master 现场决定。
func buildEmbeddedAgentConfig(mc *config.MasterRuntimeConfig, masterListen, listenAddr string) *config.AgentRuntimeConfig {
	masterURL := "http://" + listenAddr
	if strings.HasPrefix(listenAddr, "unix:") {
		masterURL = listenAddr // 已是 unix:/path 或 unix:@name，WSDial 直接识别
	}
	return &config.AgentRuntimeConfig{
		LogLevel: mc.LogLevel,
		Agent: config.AgentConfig{
			Listen:          masterListen,
			MasterURL:       masterURL,
			CredentialsFile: mc.Agent.CredentialsFile,
			Cache:           mc.Agent.Cache,
		},
		Runtime: mc.Runtime,
		Relay:   mc.Relay,
	}
}

type embeddedAgentCandidate struct {
	server     *agent.Server
	background *agent.PreparedBackground
}

func (s *Server) prepareEmbeddedAgent(ctx context.Context, listenAddr string) (*embeddedAgentCandidate, error) {
	if s.beforeSetupEmbedded != nil {
		s.beforeSetupEmbedded()
	}
	agt, err := ensureEmbeddedAgentContext(ctx, s.DB)
	if err != nil {
		return nil, err
	}

	s.Logger.Info("embedded agent ready", zap.String("agent_id", agt.AgentID))

	// Build agent config pointing at this master
	agentCfg := buildEmbeddedAgentConfig(s.Cfg, s.Cfg.Master.Listen, listenAddr)

	creds := &enrollment.Credentials{
		AgentID: agt.AgentID,
		Secret:  agt.Secret,
	}

	embeddedAgent, err := agent.NewEmbedded(agentCfg, s.Logger.Named("embedded-agent"), creds, agent.EmbeddedOptions{
		MetricsRegistry: s.MetricsRegistry,
		RelayMetrics:    s.RelayMetrics,
	})
	if err != nil {
		return nil, fmt.Errorf("create embedded agent: %w", err)
	}
	if s.afterEmbeddedConstruct != nil {
		s.afterEmbeddedConstruct(embeddedAgent)
	}
	if err := context.Cause(ctx); err != nil {
		_ = embeddedAgent.Shutdown(ctx)
		<-embeddedAgent.Done()
		return nil, err
	}
	background, err := embeddedAgent.PrepareBackground(ctx)
	if err != nil {
		_ = embeddedAgent.Shutdown(context.Background())
		<-embeddedAgent.Done()
		return nil, fmt.Errorf("prepare embedded agent background: %w", err)
	}
	return &embeddedAgentCandidate{server: embeddedAgent, background: background}, nil
}

func (s *Server) closeEmbeddedCandidate(candidate *embeddedAgentCandidate) {
	if candidate == nil {
		return
	}
	candidate.background.Cancel(context.Canceled)
	_ = candidate.server.Shutdown(context.Background())
	<-candidate.server.Done()
	candidate.background.Wait()
}

func (s *Server) commitEmbeddedAgent(candidate *embeddedAgentCandidate, startup *registrationLease) error {
	ready := make(chan struct{})
	// Wire embedded agent store into channel handler so that the local channel
	// test path can warm the __system_test__ token via SetToken (apply-if-present
	// push semantics never warm new tokens, so we need the direct write path).
	s.lifecycleMu.Lock()
	if !startup.commitLocked(s) {
		s.lifecycleMu.Unlock()
		s.closeEmbeddedCandidate(candidate)
		if s.isClosing() {
			return errMasterServerClosing
		}
		return ErrAlreadyRunning
	}
	if s.channelHandler != nil {
		s.channelHandler.AgentStore = candidate.server.Store
	}

	// Mount relay routes on master's router
	candidate.server.MountRoutes(s.Router)
	s.embeddedAgent = candidate.server
	s.embeddedBackground = candidate.background
	s.startLifecycleWorkerLocked(func() {
		runAfterMasterCommit(s.lifecycleContext(), ready, candidate.background.Wait)
	})
	s.lifecycleMu.Unlock()
	candidate.background.Commit()
	close(ready)
	return nil
}

// setupEmbeddedAgent creates a full agent instance embedded in the master
// process. The agent connects back to master via WebSocket on localhost,
// ensuring full feature parity (usage logging, cache sync, etc.).
func (s *Server) setupEmbeddedAgent(ctx context.Context, listenAddr string) error {
	startup, err := s.beginRegistration()
	if err != nil {
		return err
	}
	defer startup.Abort()
	candidate, err := s.prepareEmbeddedAgent(startup.Context(), listenAddr)
	if err != nil {
		return err
	}
	if err := context.Cause(ctx); err != nil {
		s.closeEmbeddedCandidate(candidate)
		return err
	}
	if err := s.commitEmbeddedAgent(candidate, startup); err != nil {
		return err
	}
	startup.Commit()

	s.Logger.Info("embedded agent started",
		zap.String("agent_id", candidate.server.Creds.AgentID),
		zap.String("master_url", candidate.server.Cfg.Agent.MasterURL),
	)
	return nil
}

type masterRunResources struct {
	listener   net.Listener
	httpServer *http.Server
	selfListen string
	embedded   *embeddedAgentCandidate
}

func (s *Server) commitRunResources(startupCtx, rootCtx context.Context, startup *registrationLease, resources masterRunResources) bool {
	ready := make(chan struct{})
	s.lifecycleMu.Lock()
	if !startup.commitLocked(s) {
		s.lifecycleMu.Unlock()
		return false
	}
	if s.channelHandler != nil {
		s.channelHandler.MasterListen = resources.selfListen
		s.channelHandler.AgentStore = resources.embedded.server.Store
	}
	resources.embedded.server.MountRoutes(s.Router)
	s.Listener = resources.listener
	s.httpSrv = resources.httpServer
	s.embeddedAgent = resources.embedded.server
	s.embeddedBackground = resources.embedded.background
	start := func(run func()) {
		s.startLifecycleWorkerLocked(func() { runAfterMasterCommit(startupCtx, ready, run) })
	}
	s.startLifecycleWorkerLocked(func() {
		runAfterMasterCommit(startupCtx, ready, resources.embedded.background.Wait)
	})
	start(func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-rootCtx.Done():
				return
			case <-ticker.C:
				s.saveVersion(rootCtx)
			}
		}
	})
	start(func() { s.runStateSweeper(rootCtx, s.oauthHandler.StateStore) })
	start(func() { s.Heartbeat.Start(rootCtx) })
	start(func() { s.Connections.Run(rootCtx) })
	start(func() { _ = s.ProbeScheduler.Run(rootCtx) })
	if s.Aggregator != nil {
		start(func() { s.Aggregator.Start(rootCtx) })
	}
	if s.RebuildRunner != nil {
		start(func() { s.RebuildRunner.Start(rootCtx) })
	}
	if s.LimitEvaluator != nil {
		start(s.LimitEvaluator.Start)
	}
	s.lifecycleMu.Unlock()
	if s.beforeRunRelease != nil {
		s.beforeRunRelease()
	}
	resources.embedded.background.Commit()
	close(ready)
	return true
}

func runAfterMasterCommit(ctx context.Context, ready <-chan struct{}, run func()) {
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

func (s *Server) Run() error {
	ctx := s.lifecycleContext()
	if err := context.Cause(ctx); err != nil {
		return err
	}
	registration, err := s.beginRegistration()
	if err != nil {
		return err
	}
	defer registration.Abort()
	if s.afterRunRegistration != nil {
		s.afterRunRegistration()
	}
	if s.isClosing() {
		return errMasterServerClosing
	}

	ln, err := netaddr.Listen(s.Cfg.Master.Listen)
	if err != nil {
		return err
	}
	resourcesCommitted := false
	var embedded *embeddedAgentCandidate
	defer func() {
		if !resourcesCommitted {
			_ = ln.Close()
			s.closeEmbeddedCandidate(embedded)
		}
	}()
	if s.isClosing() {
		return errMasterServerClosing
	}

	// Channel test handler was constructed with the configured listen string
	// (e.g. ":0" in tests); now that the OS has assigned a real port, point
	// the handler at it so its loopback URL resolves.
	// unix 监听时 ln.Addr().String() 是裸 socket 路径（会被 Parse 误判为 tcp），
	// 故 self-call / embedded 回连统一用配置里的 unix: 原串。
	selfListen := ln.Addr().String()
	if ln.Addr().Network() == "unix" {
		selfListen = s.Cfg.Master.Listen
	}
	// Prepare embedded agent (needs actual listen address).
	if s.isClosing() {
		return errMasterServerClosing
	}
	embedded, err = s.prepareEmbeddedAgent(registration.Context(), selfListen)
	if err != nil {
		return fmt.Errorf("embedded agent: %w", err)
	}
	httpSrv := &http.Server{
		Handler:           s.countHTTPHandlers(s.Router),
		ReadHeaderTimeout: 30 * time.Second, // guard against inbound slowloris
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ConnState:         s.countAcceptedSockets,
	}
	if s.beforeRunCommit != nil {
		s.beforeRunCommit()
	}
	if !s.commitRunResources(registration.Context(), ctx, registration, masterRunResources{
		listener:   ln,
		httpServer: httpSrv,
		selfListen: selfListen,
		embedded:   embedded,
	}) {
		return errMasterServerClosing
	}
	resourcesCommitted = true
	registration.Commit()
	if cause := context.Cause(ctx); cause != nil {
		return cause
	}
	s.Logger.Info("master listening", zap.String("addr", ln.Addr().String()))
	return httpSrv.Serve(ln)
}

func (s *Server) runStateSweeper(ctx context.Context, store *apioauth.StateStore) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			store.Sweep()
		}
	}
}

func (s *Server) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("master server: nil shutdown context")
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
		s.registrationCtx, s.registrationCancel = context.WithCancelCause(context.Background())
		s.done = make(chan struct{})
		s.httpHandlerChanged = make(chan struct{}, 1)
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

type registrationLease struct {
	done       chan struct{}
	once       sync.Once
	ctx        context.Context
	server     *Server
	generation uint64
}

func (l *registrationLease) Context() context.Context {
	if l == nil || l.ctx == nil {
		panic("master registration lease requires context")
	}
	return l.ctx
}

func (l *registrationLease) finish() {
	if l != nil {
		l.once.Do(func() { close(l.done) })
	}
}

func (l *registrationLease) commitLocked(server *Server) bool {
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

func (l *registrationLease) Commit() { l.finish() }

func (l *registrationLease) Abort() {
	if l == nil {
		return
	}
	if l.server != nil {
		l.server.lifecycleMu.Lock()
		if l.server.startupLease == l && l.server.startupGeneration == l.generation {
			if l.server.startupState == startupPreparing {
				l.server.startupState = startupIdle
			}
			l.server.startupLease = nil
		}
		l.server.lifecycleMu.Unlock()
	}
	l.finish()
}

func (s *Server) beginRegistration() (*registrationLease, error) {
	s.initLifecycle()
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing || s.startupState == startupClosing {
		return nil, errMasterServerClosing
	}
	if s.startupState != startupIdle {
		return nil, ErrAlreadyRunning
	}
	s.startupGeneration++
	lease := &registrationLease{
		done: make(chan struct{}), ctx: s.registrationCtx, server: s, generation: s.startupGeneration,
	}
	s.startupState = startupPreparing
	s.startupLease = lease
	return lease, nil
}

func (s *Server) isClosing() bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	return s.closing
}

func (s *Server) registerListener(ln net.Listener) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing {
		return false
	}
	s.Listener = ln
	return true
}

func (s *Server) registerHTTPServer(httpSrv *http.Server) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.closing {
		return false
	}
	s.httpSrv = httpSrv
	return true
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
		cancelRegistration := s.registrationCancel
		embeddedAgent := s.embeddedAgent
		operations := s.Operations
		s.lifecycleMu.Unlock()
		cancelRegistration(errMasterServerClosing)
		if operations != nil {
			operations.Cancel()
		}
		if embeddedAgent != nil {
			embeddedAgent.CancelDirectForwarding()
		}
		if s.afterShutdownAdmission != nil {
			s.afterShutdownAdmission()
		}
		go s.finalizeShutdown(ctx, startupDone)
	})
}

func (s *Server) finalizeShutdown(ctx context.Context, startupDone <-chan struct{}) {
	if startupDone != nil {
		<-startupDone
	}
	if s.RelayAdmission != nil {
		s.RelayAdmission.Set(false)
	}
	s.lifecycleMu.Lock()
	httpSrv := s.httpSrv
	listener := s.Listener
	relayHub := s.RelayHub
	s.lifecycleMu.Unlock()
	drains := pool.New().WithContext(ctx)
	if httpSrv != nil {
		drains.Go(func(ctx context.Context) error { return httpSrv.Shutdown(ctx) })
	}
	if relayHub != nil {
		drains.Go(func(ctx context.Context) error { return relayHub.DrainAll(ctx) })
	}
	drainErr := drains.Wait()
	shutdownCause := context.Cause(ctx)
	if shutdownCause == nil {
		shutdownCause = errors.New("master server: shutdown")
	}
	s.rootCancel(shutdownCause)
	if s.ProbeScheduler != nil {
		_ = s.ProbeScheduler.Close(ctx)
		<-s.ProbeScheduler.Done()
	}
	if s.Operations != nil {
		_ = s.Operations.Close(ctx)
		<-s.Operations.Done()
	}
	if httpSrv != nil {
		_ = httpSrv.Close()
	} else if listener != nil {
		_ = listener.Close()
	}
	if relayHub != nil {
		_ = relayHub.Close(ctx)
		<-relayHub.Done()
	}
	if s.Hub != nil {
		_ = s.Hub.Close(ctx)
		<-s.Hub.Done()
	}
	s.waitHTTPHandlers()
	if s.embeddedBackground != nil {
		s.embeddedBackground.Cancel(shutdownCause)
	}
	if s.embeddedAgent != nil {
		_ = s.embeddedAgent.Shutdown(ctx)
		<-s.embeddedAgent.Done()
	}
	if s.embeddedBackground != nil {
		s.embeddedBackground.Wait()
	}
	if s.Aggregator != nil {
		_ = s.Aggregator.Close(ctx)
		<-s.Aggregator.Done()
	}
	if s.RebuildRunner != nil {
		_ = s.RebuildRunner.Close(ctx)
		<-s.RebuildRunner.Done()
	}
	if s.LimitEvaluator != nil {
		_ = s.LimitEvaluator.Close(ctx)
		<-s.LimitEvaluator.Done()
	}
	if s.Heartbeat != nil {
		_ = s.Heartbeat.Close(ctx)
		<-s.Heartbeat.Done()
	}
	s.workers.Wait()
	if s.DB != nil {
		s.saveVersion(ctx)
	}
	if s.DB != nil {
		if sqlDB, err := s.DB.DB(); err == nil {
			_ = sqlDB.Close()
		}
	}
	s.lifecycleMu.Lock()
	s.shutdownErr = drainErr
	s.lifecycleMu.Unlock()
	close(s.done)
}

func (s *Server) countHTTPHandlers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.httpHandlers.Add(1)
		defer func() {
			s.httpHandlers.Add(-1)
			select {
			case s.httpHandlerChanged <- struct{}{}:
			default:
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) waitHTTPHandlers() {
	for s.httpHandlers.Load() > 0 {
		<-s.httpHandlerChanged
	}
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
	counts := app.ResourceCounts{LifecycleWorkers: s.activeWorkers.Load(), HTTPHandlers: s.httpHandlers.Load(), AcceptedSockets: s.acceptedSockets.Load()}
	if s.Hub != nil {
		control := s.Hub.ResourceCounts()
		counts.ControlSessions += control.ControlSessions
		counts.ControlHandlers += control.ControlHandlers
		counts.ControlSockets += control.ControlSockets
	}
	if s.RelayHub != nil {
		relay := s.RelayHub.ResourceCounts()
		counts.RelayCandidates += relay.RelayCandidates
		counts.RelayActive += relay.RelayActive
		counts.RelayDraining += relay.RelayDraining
		counts.RelayStreams += relay.RelayStreams
		counts.RelaySockets += relay.RelaySockets
	}
	owned := make([]app.ResourceCounts, 0, 4)
	if s.Heartbeat != nil {
		owned = append(owned, s.Heartbeat.ResourceCounts())
	}
	if s.Aggregator != nil {
		owned = append(owned, s.Aggregator.ResourceCounts())
	}
	if s.RebuildRunner != nil {
		owned = append(owned, s.RebuildRunner.ResourceCounts())
	}
	if s.LimitEvaluator != nil {
		owned = append(owned, s.LimitEvaluator.ResourceCounts())
	}
	for _, current := range owned {
		counts.LifecycleWorkers += current.LifecycleWorkers
		counts.Timers += current.Timers
		counts.Inflight += current.Inflight
	}
	if s.embeddedAgent != nil {
		embedded := s.embeddedAgent.ResourceCountsForTest()
		counts.LifecycleWorkers += embedded.LifecycleWorkers
		counts.HTTPHandlers += embedded.HTTPHandlers
		counts.AcceptedSockets += embedded.AcceptedSockets
		counts.ControlSessions += embedded.ControlSessions
		counts.RelayCandidates += embedded.RelayCandidates
		counts.RelayActive += embedded.RelayActive
		counts.RelayDraining += embedded.RelayDraining
		counts.RelayStreams += embedded.RelayStreams
		counts.CacheLoads += embedded.CacheLoads
		counts.CacheRefreshes += embedded.CacheRefreshes
		counts.ReporterWorkers += embedded.ReporterWorkers
		counts.Inflight += embedded.Inflight
		counts.Timers += embedded.Timers
		counts.Transports += embedded.Transports
		counts.RelaySockets += embedded.RelaySockets
	}
	return counts
}

func (s *Server) waitForEmbeddedControlSession(ctx context.Context) error {
	if s.embeddedAgent == nil || s.Hub == nil {
		return nil
	}
	poll := time.NewTicker(time.Millisecond)
	defer poll.Stop()
	for {
		if _, connected := s.Hub.GetControlSession(embeddedAgentID); !connected {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-poll.C:
		}
	}
}

// generateAgentSecret 用 crypto/rand 读 32 字节，base64 RawURL 编码（约 43 字符）。
// 用于 embedded agent 首次启动时生成持久化 secret。
func generateAgentSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

const embeddedAgentID = "embedded"

// ensureEmbeddedAgent 查找/创建 embedded agent。
// 首次启动随机生成 secret 并写入 DB；后续启动直接读已存的 secret。
// 不再用 Assign+FirstOrCreate（那会每次启动覆盖 Secret，是硬编码 secret 的根因）。
func ensureEmbeddedAgent(db *gorm.DB) (*models.Agent, error) {
	return ensureEmbeddedAgentContext(context.Background(), db)
}

func ensureEmbeddedAgentContext(ctx context.Context, db *gorm.DB) (*models.Agent, error) {
	var agent models.Agent
	db = db.WithContext(ctx)
	err := db.Where("agent_id = ?", embeddedAgentID).First(&agent).Error
	if err == nil {
		return &agent, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("lookup embedded agent: %w", err)
	}

	secret, err := generateAgentSecret()
	if err != nil {
		return nil, fmt.Errorf("generate embedded agent secret: %w", err)
	}
	agent = models.Agent{
		AgentID: embeddedAgentID,
		Secret:  secret,
		Name:    "Embedded Agent",
		Status:  1,
	}
	if err := db.Create(&agent).Error; err != nil {
		return nil, fmt.Errorf("create embedded agent: %w", err)
	}
	return &agent, nil
}
