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
	"sync/atomic"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/pkg/byokcrypto"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/dao"
	"github.com/VaalaCat/ai-gateway/internal/master/api"
	apiagent "github.com/VaalaCat/ai-gateway/internal/master/api/agent"
	"github.com/VaalaCat/ai-gateway/internal/master/api/agent_route"
	apibilling "github.com/VaalaCat/ai-gateway/internal/master/api/billing"
	apicache "github.com/VaalaCat/ai-gateway/internal/master/api/cache"
	"github.com/VaalaCat/ai-gateway/internal/master/api/channel"
	apiinsights "github.com/VaalaCat/ai-gateway/internal/master/api/insights"
	apilog "github.com/VaalaCat/ai-gateway/internal/master/api/log"
	"github.com/VaalaCat/ai-gateway/internal/master/api/middleware"
	"github.com/VaalaCat/ai-gateway/internal/master/api/model"
	apimodelrouting "github.com/VaalaCat/ai-gateway/internal/master/api/model_routing"
	apimonitoring "github.com/VaalaCat/ai-gateway/internal/master/api/monitoring"
	"github.com/VaalaCat/ai-gateway/internal/master/api/private_channel"
	apioauth "github.com/VaalaCat/ai-gateway/internal/master/api/oauth"
	apioap "github.com/VaalaCat/ai-gateway/internal/master/api/oauth_provider_admin"
	"github.com/VaalaCat/ai-gateway/internal/master/api/stats"
	apisystem "github.com/VaalaCat/ai-gateway/internal/master/api/system"
	"github.com/VaalaCat/ai-gateway/internal/master/api/token"
	"github.com/VaalaCat/ai-gateway/internal/master/api/token_template"
	"github.com/VaalaCat/ai-gateway/internal/master/api/user"
	"github.com/VaalaCat/ai-gateway/internal/master/api/user_group"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	msync "github.com/VaalaCat/ai-gateway/internal/master/sync"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/eventbus"
	"github.com/VaalaCat/ai-gateway/internal/pkg/ginutil"
	webassets "github.com/VaalaCat/ai-gateway/web"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

var _ app.MasterServer = (*Server)(nil)

type Server struct {
	Cfg              *config.MasterRuntimeConfig
	Logger           *zap.Logger
	DB               *gorm.DB
	Bus              app.EventBus
	Router           *gin.Engine
	Version          atomic.Int64
	lastSavedVersion atomic.Int64
	Hub              *msync.Hub
	Listener         net.Listener
	httpSrv          *http.Server
	App              app.Application

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

	// BYOKProvider 是 BYOK cipher 的注入点。private_channel.Handler
	// 通过它获取 *Cipher，避免污染 app.Application 顶层接口。
	BYOKProvider byokcrypto.Provider

	channelHandler *channel.Handler
	embeddedAgent  *agent.Server
	oauthAllowlist *apioauth.Allowlist
	oauthHandler   *apioauth.Handler
}

const sqliteWALPragma = "_pragma=journal_mode(WAL)"

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

	sqliteDSN := withSQLiteWAL(runtimeCfg.Master.DBPath)

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
	if isSQLiteMemoryDSN(sqliteDSN) {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, fmt.Errorf("get sql db: %w", err)
		}
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

	application := app.NewApplication()
	application.SetDB(db)
	application.SetEventBus(bus)

	s := &Server{
		Cfg:          runtimeCfg,
		Logger:       logger,
		DB:           db,
		Bus:          bus,
		Router:       gin.New(),
		App:          application,
		BYOKProvider: byokProvider,
	}

	allowlist, err := apioauth.NewAllowlist(runtimeCfg.Master.PublicBaseURLs)
	if err != nil {
		return nil, fmt.Errorf("oauth allowlist: %w", err)
	}
	s.oauthAllowlist = allowlist

	// Load persisted version from DB
	s.loadVersion()

	s.Hub = msync.NewHub(application, logger, bus, func() int64 { return s.Version.Load() }, byokProvider.GetCipher())

	// Heartbeat tracker — memory-first last_seen + config fingerprint.
	// Flush interval is admin-configurable; falls back to 300s if unset.
	flushSec := dao.NewAdminQuery(dao.NewContext(application)).Setting().LookupInt(
		"agent.heartbeat_flush_interval_seconds", 300,
	)
	s.Heartbeat = msync.NewHeartbeatTracker(application, logger, time.Duration(flushSec)*time.Second)
	s.Heartbeat.SetLastSeenPersistFn(func(updates map[string]int64) error {
		return dao.NewAdminMutation(dao.NewContext(application)).Agent().BatchUpdateLastSeen(updates)
	})
	s.Hub.Heartbeat = s.Heartbeat

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
	aggregator.SetFlushFns(
		func(rows []dao.TokenDailyRow) error {
			return dao.NewAdminMutation(dao.NewContext(application)).Billing().BatchUpsertTokenDaily(rows)
		},
		func(rows []dao.ChannelDailyRow) error {
			return dao.NewAdminMutation(dao.NewContext(application)).Billing().BatchUpsertChannelDaily(rows)
		},
		func(rows []dao.HourlyBucketRow) error {
			return dao.NewAdminMutation(dao.NewContext(application)).Billing().BatchUpsertHourlyBucket(rows)
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
	checker := billing.NewQuotaChecker(application, bus, logger)
	checker.Start()

	s.setupMiddleware()
	s.setupRoutes()

	return s, nil
}

func (s *Server) setupMiddleware() {
	s.Router.Use(gin.Recovery(), ginutil.AbortHandlerRecovery())
}

func (s *Server) setupRoutes() {
	s.Router.GET("/ping", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok", "role": "master"})
	})

	adapter := api.NewAdapter(s.Cfg, s.Logger, s.App)
	userH := &user.Handler{Bus: s.Bus}
	tokenH := &token.Handler{}
	s.channelHandler = &channel.Handler{Hub: s.Hub, MasterListen: s.Cfg.Master.Listen}
	channelH := s.channelHandler
	modelH := &model.Handler{}
	agentH := &apiagent.Handler{
		GetOnlineAgentIDs: s.Hub.GetOnlineAgentIDs,
		GetRuntime:        s.Hub.GetRuntime,
		HubCall:           s.Hub.Call,
		Hub:               s.Hub,
	}
	cacheH := &apicache.Handler{
		GetOnlineAgentIDs: s.Hub.GetOnlineAgentIDs,
		GetRuntime:        s.Hub.GetRuntime,
		Tracker:           s.Heartbeat,
	}

	// Public endpoints
	s.Router.POST("/api/login", api.Adapt(adapter, api.BindJSON, userH.Login))
	s.Router.POST("/api/register", api.Adapt(adapter, api.BindJSON, userH.Register))
	s.Router.GET("/api/system/registration-status", api.Adapt(adapter, api.BindNone, userH.RegistrationStatus))
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
	pcH := private_channel.NewHandler(s.App, s.BYOKProvider)

	// User-level authenticated routes (no admin required)
	userAuth := s.Router.Group("/api")
	userAuth.Use(middleware.AuthMiddleware(s.Cfg.Master.JWTSecret))
	userAuth.Use(middleware.ScopeMiddleware())
	userAuth.GET("/profile", api.Adapt(adapter, api.BindNone, userH.GetProfile))
	userAuth.PUT("/profile", api.Adapt(adapter, api.BindJSON, userH.UpdateProfile))
	userAuth.PUT("/profile/password", api.Adapt(adapter, api.BindJSON, userH.ChangePassword))
	userAuth.POST("/oauth/link-ticket", api.Adapt(adapter, api.BindNone, oauthH.IssueLinkTicket))
	userAuth.GET("/oauth/identities", api.Adapt(adapter, api.BindNone, oauthH.ListMyIdentities))
	userAuth.DELETE("/oauth/identities/:id", api.Adapt(adapter, api.BindURI, oauthH.DeleteIdentity))
	tplH := &token_template.Handler{}
	ugH := &user_group.Handler{Bus: s.Bus}
	oapH := &apioap.Handler{Bus: s.Bus}
	userAuth.GET("/token-templates", api.Adapt(adapter, api.BindQuery, tplH.ListEnabled))

	// Portal model-routings (user-owned, scope forced to user)
	userAuth.GET("/model-routings", api.Adapt(adapter, api.BindQuery, mrH.PortalList))
	userAuth.POST("/model-routings", api.Adapt(adapter, api.BindJSON, mrH.PortalCreate))
	userAuth.GET("/model-routings/global-routing-names", api.Adapt(adapter, api.BindNone, mrH.PortalGlobalRoutingNames))
	userAuth.POST("/model-routings/preview", api.Adapt(adapter, api.BindJSON, mrH.Preview))
	userAuth.GET("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.PortalGet))
	userAuth.PUT("/model-routings/:id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.PortalUpdate))
	userAuth.DELETE("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.PortalDelete))

	// Portal private-channels (BYOK)
	userAuth.GET("/private-channels", api.Adapt(adapter, api.BindQuery, pcH.PortalList))
	userAuth.POST("/private-channels", api.Adapt(adapter, api.BindJSON, pcH.Create))
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
	auth.PUT("/users/:id/quota", api.Adapt(adapter, api.BindURIAndBodyMap, userH.UpdateQuota))

	auth.GET("/token-templates", api.Adapt(adapter, api.BindQuery, tplH.List))
	auth.POST("/token-templates", api.Adapt(adapter, api.BindJSON, tplH.Create))
	auth.PUT("/token-templates/:id", api.Adapt(adapter, api.BindURIAndBodyMap, tplH.Update))
	auth.DELETE("/token-templates/:id", api.Adapt(adapter, api.BindURI, tplH.Delete))
	auth.POST("/token-templates/:id/sync-preview", api.Adapt(adapter, api.BindURI, tplH.SyncPreview))
	auth.POST("/token-templates/:id/sync", api.Adapt(adapter, api.BindURI, tplH.Sync))

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
	auth.GET("/channels/types", api.Adapt(adapter, api.BindNone, channelH.Types))
	auth.GET("/channels/:id", api.Adapt(adapter, api.BindURI, channelH.Get))
	auth.PUT("/channels/:id", api.Adapt(adapter, api.BindURIAndBodyMap, channelH.Update))
	auth.DELETE("/channels/:id", api.Adapt(adapter, api.BindURI, channelH.Delete))
	auth.POST("/channels/:id/test", api.Adapt(adapter, api.BindURIAndOptionalJSON, channelH.Test))
	auth.POST("/channels/fetch-models", api.Adapt(adapter, api.BindJSON, channelH.FetchModels))

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
	auth.GET("/agents/:id", api.Adapt(adapter, api.BindURI, agentH.Get))
	auth.PUT("/agents/:id", api.Adapt(adapter, api.BindURIAndBodyMap, agentH.Update))
	auth.DELETE("/agents/:id", api.Adapt(adapter, api.BindURI, agentH.Delete))
	auth.POST("/agents/enrollment-token", api.Adapt(adapter, api.BindOptionalJSON, agentH.GenerateEnrollmentToken))
	auth.GET("/agents/online", api.Adapt(adapter, api.BindNone, agentH.Online))
	auth.GET("/agents/:id/detail", api.Adapt(adapter, api.BindURI, agentH.Detail))
	auth.POST("/agents/:id/connectivity", api.Adapt(adapter, api.BindURI, agentH.CheckConnectivity))
	auth.GET("/agents/:id/connectivity", api.Adapt(adapter, api.BindURI, agentH.GetConnectivity))

	agentRouteH := &agent_route.Handler{}
	auth.GET("/agent-routes", api.Adapt(adapter, api.BindQuery, agentRouteH.List))
	auth.POST("/agent-routes", api.Adapt(adapter, api.BindJSON, agentRouteH.Create))
	auth.GET("/agent-routes/overview", api.Adapt(adapter, api.BindQuery, agentRouteH.Overview))
	auth.GET("/agent-routes/:id", api.Adapt(adapter, api.BindURI, agentRouteH.Get))
	auth.PUT("/agent-routes/:id", api.Adapt(adapter, api.BindURIAndBodyMap, agentRouteH.Update))
	auth.DELETE("/agent-routes/:id", api.Adapt(adapter, api.BindURI, agentRouteH.Delete))

	auth.GET("/model-routings", api.Adapt(adapter, api.BindQuery, mrH.List))
	auth.POST("/model-routings", api.Adapt(adapter, api.BindJSON, mrH.Create))
	auth.GET("/model-routings/candidates", api.Adapt(adapter, api.BindNone, mrH.Candidates))
	auth.POST("/model-routings/preview", api.Adapt(adapter, api.BindJSON, mrH.Preview))
	auth.GET("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.Get))
	auth.PUT("/model-routings/:id", api.Adapt(adapter, api.BindURIAndBodyMap, mrH.Update))
	auth.DELETE("/model-routings/:id", api.Adapt(adapter, api.BindURI, mrH.Delete))

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

	systemH := &apisystem.Handler{ConnectedCount: s.Hub.ConnectedAgents}
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

	s.setupStaticRoutes()
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
	return s.setupEmbeddedAgent(listenAddr)
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

func (s *Server) saveVersion() {
	current := s.Version.Load()
	if current == s.lastSavedVersion.Load() {
		return
	}
	v := strconv.FormatInt(current, 10)
	if err := s.DB.Model(&models.Setting{}).
		Where("key = ?", "version").
		Update("value", v).Error; err != nil {
		s.Logger.Warn("saveVersion failed", zap.Error(err))
		return
	}
	s.lastSavedVersion.Store(current)
}

func (s *Server) startVersionPersistence(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				s.saveVersion()
				return
			case <-ticker.C:
				s.saveVersion()
			}
		}
	}()
}

// setupEmbeddedAgent creates a full agent instance embedded in the master
// process. The agent connects back to master via WebSocket on localhost,
// ensuring full feature parity (usage logging, cache sync, etc.).
func (s *Server) setupEmbeddedAgent(listenAddr string) error {
	agt, err := ensureEmbeddedAgent(s.DB)
	if err != nil {
		return err
	}

	s.Logger.Info("embedded agent ready", zap.String("agent_id", agt.AgentID))

	// Build agent config pointing at this master
	agentCfg := &config.AgentRuntimeConfig{
		LogLevel: s.Cfg.LogLevel,
		Agent: config.AgentConfig{
			Listen:    s.Cfg.Master.Listen,
			MasterURL: "http://" + listenAddr,
		},
		Runtime: s.Cfg.Runtime,
		Relay:   s.Cfg.Relay,
	}

	creds := &enrollment.Credentials{
		AgentID: agt.AgentID,
		Secret:  agt.Secret,
	}

	embeddedAgent, err := agent.NewEmbedded(agentCfg, s.Logger.Named("embedded-agent"), creds)
	if err != nil {
		return fmt.Errorf("create embedded agent: %w", err)
	}
	s.embeddedAgent = embeddedAgent

	// Wire embedded agent store into channel handler so that the local channel
	// test path can warm the __system_test__ token via SetToken (apply-if-present
	// push semantics never warm new tokens, so we need the direct write path).
	if s.channelHandler != nil {
		s.channelHandler.AgentStore = embeddedAgent.Store
	}

	// Mount relay routes on master's router
	embeddedAgent.MountRoutes(s.Router)

	// Start background goroutines (connectLoop, Reporter, Syncer, heartbeat)
	go embeddedAgent.RunBackground(context.Background())

	s.Logger.Info("embedded agent started",
		zap.String("agent_id", agt.AgentID),
		zap.String("master_url", agentCfg.Agent.MasterURL),
	)
	return nil
}

func (s *Server) Run() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.startVersionPersistence(ctx)
	s.Heartbeat.Start(ctx)
	if s.Aggregator != nil {
		s.Aggregator.Start(ctx)
	}
	go s.runStateSweeper(ctx, s.oauthHandler.StateStore)

	ln, err := net.Listen("tcp", s.Cfg.Master.Listen)
	if err != nil {
		return err
	}
	s.Listener = ln

	// Channel test handler was constructed with the configured listen string
	// (e.g. ":0" in tests); now that the OS has assigned a real port, point
	// the handler at it so its loopback URL resolves.
	s.SetChannelMasterListen(ln.Addr().String())

	// Start embedded agent (needs actual listen address)
	if err := s.setupEmbeddedAgent(ln.Addr().String()); err != nil {
		return fmt.Errorf("embedded agent: %w", err)
	}

	s.httpSrv = &http.Server{Handler: s.Router}
	s.Logger.Info("master listening", zap.String("addr", ln.Addr().String()))
	return s.httpSrv.Serve(ln)
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
	var httpErr error
	if s.httpSrv != nil {
		httpErr = s.httpSrv.Shutdown(ctx)
	}
	// Shutdown order (spec §9): HTTP → Aggregator → RebuildRunner → Heartbeat.
	// Aggregator.Stop drains the final billing rollup batch; running it
	// AFTER the HTTP server has stopped accepting new requests guarantees
	// the in-memory deltas reflect every committed UsageLog.
	if s.Aggregator != nil {
		s.Aggregator.Stop()
	}
	// Cancel in-flight rebuild jobs; in-progress slice transactions finish on
	// their own goroutine then exit at the next ctx check. Idempotent.
	if s.RebuildRunner != nil {
		s.RebuildRunner.Stop()
	}
	// Force-flush in-memory last_seen before exit. Idempotent + safe even
	// if Start was never called (e.g. in tests).
	if s.Heartbeat != nil {
		s.Heartbeat.Stop()
	}
	return httpErr
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
	var agent models.Agent
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
