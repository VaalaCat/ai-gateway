package master

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent"
	agentauthcache "github.com/VaalaCat/ai-gateway/internal/agent/agentauth"
	agentappkg "github.com/VaalaCat/ai-gateway/internal/agent/app"
	agentattemptproxy "github.com/VaalaCat/ai-gateway/internal/agent/attemptproxy"
	agenttokenauth "github.com/VaalaCat/ai-gateway/internal/agent/auth"
	agentcache "github.com/VaalaCat/ai-gateway/internal/agent/cache"
	"github.com/VaalaCat/ai-gateway/internal/agent/enrollment"
	agentrelay "github.com/VaalaCat/ai-gateway/internal/agent/relay"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	relayexec "github.com/VaalaCat/ai-gateway/internal/agent/relay/pipeline/exec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/upstream"
	agentrpc "github.com/VaalaCat/ai-gateway/internal/agent/rpc"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	masteragentauth "github.com/VaalaCat/ai-gateway/internal/master/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/master/billing"
	mastertunnel "github.com/VaalaCat/ai-gateway/internal/master/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/events"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type agentRouteSocketCase struct {
	name                string
	routeTarget         string
	hardTarget          string
	targetAddress       func(*agentRouteSocketFixture) string
	targetProviderCode  int
	wantHTTPCode        int
	wantSourceProviders int32
	wantTargetProviders int32
	wantRows            int64
	wantAgentID         string
	wantRouteSource     string
	wantRouteID         uint
	wantPath            string
}

func TestAgentRelayRouteUsageRealSocketNoReplayMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := []agentRouteSocketCase{
		{
			name: "ordinary local", wantHTTPCode: http.StatusOK, wantSourceProviders: 1,
			wantRows: 1, wantAgentID: "source", wantRouteSource: "source", wantPath: "local",
		},
		{
			name: "selected local self", routeTarget: "source", wantHTTPCode: http.StatusOK,
			wantSourceProviders: 1, wantRows: 1, wantAgentID: "source", wantRouteSource: "source", wantRouteID: 42, wantPath: "local",
		},
		{
			name: "soft local fallback after DNS and relay READY failure", routeTarget: "target",
			targetAddress: func(*agentRouteSocketFixture) string {
				return "http://" + strings.Repeat("missing", 10) + ".invalid"
			},
			wantHTTPCode: http.StatusOK, wantSourceProviders: 1, wantRows: 1,
			wantAgentID: "source", wantRouteSource: "source", wantRouteID: 42, wantPath: "local",
		},
		{
			name: "soft local fallback after TCP and relay READY failure", routeTarget: "target",
			targetAddress: func(f *agentRouteSocketFixture) string { return f.closedTCPAddress() },
			wantHTTPCode:  http.StatusOK, wantSourceProviders: 1, wantRows: 1,
			wantAgentID: "source", wantRouteSource: "source", wantRouteID: 42, wantPath: "local",
		},
		{
			name: "soft local fallback after TLS and relay READY failure", routeTarget: "target",
			targetAddress: func(f *agentRouteSocketFixture) string {
				return "https" + strings.TrimPrefix(f.plainTarget.URL, "http")
			},
			wantHTTPCode: http.StatusOK, wantSourceProviders: 1, wantRows: 1,
			wantAgentID: "source", wantRouteSource: "source", wantRouteID: 42, wantPath: "local",
		},
		{
			name: "soft local fallback when relay is not READY", routeTarget: "target",
			targetAddress: func(*agentRouteSocketFixture) string { return "" },
			wantHTTPCode:  http.StatusOK, wantSourceProviders: 1, wantRows: 1,
			wantAgentID: "source", wantRouteSource: "source", wantRouteID: 42, wantPath: "local",
		},
		{
			name: "direct target", routeTarget: "target",
			targetAddress: func(f *agentRouteSocketFixture) string { return f.targetAgent.URL },
			wantHTTPCode:  http.StatusOK, wantTargetProviders: 1, wantRows: 1,
			wantAgentID: "target", wantRouteSource: "source", wantRouteID: 42, wantPath: "direct",
		},
		{
			name: "hard direct target", hardTarget: "target",
			targetAddress: func(f *agentRouteSocketFixture) string { return f.targetAgent.URL },
			wantHTTPCode:  http.StatusOK, wantTargetProviders: 1, wantRows: 1,
			wantAgentID: "target", wantRouteSource: "source", wantPath: "direct",
		},
		{
			name: "target 429 is final", routeTarget: "target", targetProviderCode: http.StatusTooManyRequests,
			targetAddress: func(f *agentRouteSocketFixture) string { return f.targetAgent.URL },
			wantHTTPCode:  http.StatusTooManyRequests, wantTargetProviders: 1, wantRows: 1,
			wantAgentID: "target", wantRouteSource: "source", wantRouteID: 42, wantPath: "direct",
		},
		{
			name: "target 500 is final", routeTarget: "target", targetProviderCode: http.StatusInternalServerError,
			targetAddress: func(f *agentRouteSocketFixture) string { return f.targetAgent.URL },
			wantHTTPCode:  http.StatusBadGateway, wantTargetProviders: 1, wantRows: 1,
			wantAgentID: "target", wantRouteSource: "source", wantRouteID: 42, wantPath: "direct",
		},
		{
			name: "direct uncertain has no replay", routeTarget: "target",
			targetAddress: func(f *agentRouteSocketFixture) string { return f.uncertainAddress() },
			wantHTTPCode:  http.StatusBadGateway, wantRows: 1,
			wantRouteSource: "source", wantRouteID: 42, wantPath: "direct",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newAgentRouteSocketFixture(t, tc.targetProviderCode)
			address := ""
			if tc.targetAddress != nil {
				address = tc.targetAddress(fixture)
			}
			fixture.configureSourceRoute(tc.routeTarget, tc.hardTarget, address)

			status, body := fixture.request(tc.hardTarget)

			require.Equal(t, tc.wantHTTPCode, status, "response body: %s", body)
			require.Equal(t, tc.wantTargetProviders, fixture.targetProviderCalls.Load())
			require.Equal(t, tc.wantSourceProviders, fixture.sourceProviderCalls.Load())
			var count int64
			require.NoError(t, fixture.db.Model(&models.UsageLog{}).Count(&count).Error)
			require.Equal(t, tc.wantRows, count)
			if tc.wantRows == 0 {
				return
			}
			var usage models.UsageLog
			require.NoError(t, fixture.db.First(&usage).Error)
			require.Equal(t, tc.wantAgentID, usage.AgentID)
			require.Equal(t, tc.wantRouteSource, usage.RouteSourceAgentID)
			require.Equal(t, tc.wantRouteID, usage.AgentRouteID)
			require.Equal(t, tc.wantPath, usage.AgentRoutePath)
		})
	}
}

func TestAgentRelayRemoteProviderErrorsUseSanitizedJSONEnvelope(t *testing.T) {
	const secret = "secret-provider-token"
	provider429 := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
		w.WriteHeader(http.StatusTooManyRequests)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]string{"message": secret, "type": "rate_limit_error"},
		}))
	})
	assertResponse := func(t *testing.T, status int, header http.Header, body []byte) {
		t.Helper()
		require.Equal(t, http.StatusTooManyRequests, status, "response body: %s", body)
		require.Equal(t, consts.ContentTypeJSON, header.Get(consts.HeaderContentType))
		require.True(t, json.Valid(body), "response body: %s", body)
		var envelope struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		require.NoError(t, json.Unmarshal(body, &envelope))
		require.Equal(t, "provider returned HTTP 429", envelope.Error.Message)
		require.Equal(t, "rate_limit_error", envelope.Error.Type)
		require.NotContains(t, string(body), secret)
	}

	t.Run("direct", func(t *testing.T) {
		f := newAgentRouteSocketFixture(t, http.StatusOK)
		var providerCalls atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			providerCalls.Add(1)
			provider429.ServeHTTP(w, r)
		}))
		t.Cleanup(provider.Close)
		f.target.Store.SetChannel(&models.Channel{
			ChannelCore: models.ChannelCore{
				ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: provider.URL,
				Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
			},
			Key: "provider-key", Models: "gpt-4o",
		})
		f.target.Store.RebuildModelIndex()
		f.configureSourceRoute("target", "", f.targetAgent.URL)

		status, header, body := requestAgentRelayHTTP(t, f.sourceAgent.URL)

		assertResponse(t, status, header, body)
		require.EqualValues(t, 1, providerCalls.Load())
		require.Equal(t, "direct", f.singleUsage().AgentRoutePath)
	})

	t.Run("relay", func(t *testing.T) {
		f := newOwnedRoutedRelayFixture(t, provider429, true)

		status, header, body := requestAgentRelayHTTP(t, f.sourceHTTP.URL)

		assertResponse(t, status, header, body)
		require.EqualValues(t, 1, f.targetProviderCalls.Load())
		require.Zero(t, f.sourceProviderCalls.Load())
		require.Equal(t, "relay", f.singleUsage().AgentRoutePath)
	})
}

func requestAgentRelayHTTP(t *testing.T, baseURL string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(
		http.MethodPost,
		baseURL+"/v1/chat/completions",
		strings.NewReader(relayRequestBody(false)),
	)
	require.NoError(t, err)
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
	req.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, resp.Header.Clone(), body
}

type agentRouteSocketFixture struct {
	t                    *testing.T
	db                   *gorm.DB
	settler              *billing.Settler
	source               *agent.Server
	target               *agent.Server
	sourceAgent          *httptest.Server
	targetAgent          *httptest.Server
	plainTarget          *httptest.Server
	sourceProvider       *httptest.Server
	targetProvider       *httptest.Server
	sourceProviderCalls  atomic.Int32
	targetProviderCalls  atomic.Int32
	uncertainTargetCalls atomic.Int32
	forwardAuth          agentproxy.ForwardAuthSnapshot
	issueForwardTickets  atomic.Bool
	signer               *masteragentauth.Signer
	sourceDirect         *agentproxy.DirectForwarder
	sourceTransport      app.TransportPool
	targetTransport      app.TransportPool
	agentHTTPActive      atomic.Int32
	providerActive       atomic.Int32
	sourceCalls          agentOwnershipCalls
	targetCalls          agentOwnershipCalls
	targetAttempt        atomic.Pointer[attemptwire.AttemptProxyMeta]
	targetCacheClient    app.WSClient
}

type agentOwnershipCalls struct {
	script         atomic.Int32
	planner        atomic.Int32
	requestLimiter atomic.Int32
	attemptLimiter atomic.Int32
	publisher      atomic.Int32
}

type visiblePrivateChannelClient struct {
	visibleUserID uint
	set           protocol.VisiblePrivateChannelSet
	loads         atomic.Int32
}

func (c *visiblePrivateChannelClient) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	request, ok := params.(protocol.FetchEntityRequest)
	if method != consts.RPCSyncFetchEntity || !ok {
		return nil, errors.New("unexpected private channel cache RPC")
	}
	response := protocol.FetchEntityResponse{}
	switch request.Entity {
	case events.EntityUser:
		response.Found = true
		response.Data, _ = json.Marshal(protocol.SyncedUser{ID: c.visibleUserID, GroupID: 1, Quota: 1000})
	case events.EntityPrivateChannel:
		c.loads.Add(1)
		if request.Key == fmt.Sprint(c.visibleUserID) {
			response.Found = true
			response.Data, _ = json.Marshal(c.set)
		}
	}
	return json.Marshal(response)
}

func (*visiblePrivateChannelClient) OnNotification(string, app.NotificationHandler) {}
func (*visiblePrivateChannelClient) Notify(string, any) error                       { return nil }
func (*visiblePrivateChannelClient) Close() error                                   { return nil }
func (*visiblePrivateChannelClient) ReadLoop()                                      {}

type agentRouteProviderProbe struct {
	calls         atomic.Int32
	active        atomic.Int32
	authorization atomic.Value
}

func newAgentRouteProviderProbe(t *testing.T) (*agentRouteProviderProbe, *httptest.Server) {
	t.Helper()
	probe := &agentRouteProviderProbe{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe.active.Add(1)
		defer probe.active.Add(-1)
		probe.calls.Add(1)
		probe.authorization.Store(r.Header.Get(consts.HeaderAuthorization))
		relayProviderSuccess(nil).ServeHTTP(w, r)
	}))
	t.Cleanup(func() {
		requireAgentRouteActiveZero(t, &probe.active, "BYOK provider probe")
		server.Close()
	})
	return probe, server
}

type agentOwnershipCache struct {
	app.AgentCache
	store       *agentcache.Store
	calls       *agentOwnershipCalls
	plannerSeen sync.Map
	scripts     *script.Engine
}

func (c *agentOwnershipCache) ResolveRouting(
	ctx context.Context,
	name string,
	owner protocol.RoutingOwner,
) *protocol.SyncedRouting {
	if _, loaded := c.plannerSeen.LoadOrStore(ctx, struct{}{}); !loaded {
		c.calls.planner.Add(1)
	}
	return c.store.ResolveRouting(ctx, name, owner)
}

func (c *agentOwnershipCache) GetGlobalRouting(ctx context.Context, name string) *protocol.SyncedRouting {
	return c.store.GetGlobalRouting(ctx, name)
}

func (c *agentOwnershipCache) HasRealModel(name string) bool {
	return c.store.HasRealModel(name)
}

func (c *agentOwnershipCache) ScriptEngine() *script.Engine {
	return c.scripts
}

func (c *agentOwnershipCache) EffectiveRequestLimiters(userID, groupID uint) []*models.RequestLimiter {
	c.calls.requestLimiter.Add(1)
	return c.AgentCache.EffectiveRequestLimiters(userID, groupID)
}

func (c *agentOwnershipCache) EffectiveAttemptLimiters(
	userID, groupID uint,
	source string,
	channelID uint,
) []*models.RequestLimiter {
	c.calls.attemptLimiter.Add(1)
	return c.AgentCache.EffectiveAttemptLimiters(userID, groupID, source, channelID)
}

type agentOwnershipScriptProvider struct {
	store *agentcache.Store
	calls *agentOwnershipCalls
}

func (p agentOwnershipScriptProvider) MatchScripts(channelID uint, model string) []*script.Compiled {
	if channelID == 0 {
		p.calls.script.Add(1)
	}
	return p.store.MatchScripts(channelID, model)
}

type agentOwnershipApplication struct {
	app.AgentApplication
	cache *agentOwnershipCache
}

func (a *agentOwnershipApplication) GetCache() app.AgentCache {
	return a.cache
}

func newAgentOwnershipApplication(
	srv *agent.Server,
	transport app.TransportPool,
	calls *agentOwnershipCalls,
) app.AgentApplication {
	base := agentappkg.NewDefaultAgentApplication(
		srv.Store, srv.BodyStore, srv.Logger, srv.Cfg, transport,
	)
	ownedCache := &agentOwnershipCache{
		AgentCache: base.GetCache(), store: srv.Store, calls: calls,
	}
	ownedCache.scripts = script.NewEngine(
		agentOwnershipScriptProvider{store: srv.Store, calls: calls}, srv.Logger, time.Second,
	)
	return &agentOwnershipApplication{AgentApplication: base, cache: ownedCache}
}

func newAgentRouteSocketFixture(t *testing.T, targetStatus int) *agentRouteSocketFixture {
	return newAgentRouteSocketFixtureWithTargetClient(t, targetStatus, nil)
}

func newAgentRouteSocketFixtureWithTargetClient(
	t *testing.T,
	targetStatus int,
	targetClient app.WSClient,
) *agentRouteSocketFixture {
	t.Helper()
	f := &agentRouteSocketFixture{t: t, targetCacheClient: targetClient}
	f.db = newAgentRouteUsageDB(t)
	f.settler = billing.NewSettler(&agentRouteDBProvider{db: f.db}, nil, zap.NewNop())
	f.signer = newAgentRouteSigner(t)
	f.forwardAuth = agentproxy.ForwardAuthSnapshot{
		Capabilities: []string{protocol.AgentCapabilityForwardV1},
		SigningKeys:  []agentauth.PublicKey{f.signer.PublicKey()},
	}
	f.issueForwardTickets.Store(true)
	f.sourceProvider = newAgentRouteProvider(t, &f.sourceProviderCalls, &f.providerActive, http.StatusOK)
	if targetStatus == 0 {
		targetStatus = http.StatusOK
	}
	f.targetProvider = newAgentRouteProvider(t, &f.targetProviderCalls, &f.providerActive, targetStatus)
	f.plainTarget = httptest.NewServer(trackAgentRouteActive(&f.agentHTTPActive, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))
	t.Cleanup(func() {
		requireAgentRouteActiveZero(t, &f.agentHTTPActive, "plain target")
		f.plainTarget.Close()
	})

	f.target, f.targetAgent = f.newAgent("target", f.targetProvider.URL, true)
	f.source, f.sourceAgent = f.newAgent("source", f.sourceProvider.URL, false)
	f.target.Store.SetAgent(&models.Agent{AgentID: "source", Status: consts.StatusEnabled})
	return f
}

type agentRouteDBProvider struct{ db *gorm.DB }

func (p *agentRouteDBProvider) GetDB() *gorm.DB { return p.db }

type agentRouteSigningStore struct{ key *models.MasterSigningKey }

func (s agentRouteSigningStore) LoadOrCreateActive(context.Context) (*models.MasterSigningKey, error) {
	return s.key, nil
}

func newAgentRouteSigner(t *testing.T) *masteragentauth.Signer {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	digest := sha256.Sum256(publicKey)
	active := uint8(1)
	signer, err := masteragentauth.NewSigner(t.Context(), agentRouteSigningStore{key: &models.MasterSigningKey{
		KeyID: hex.EncodeToString(digest[:]), PublicKey: publicKey, PrivateKey: privateKey, ActiveSlot: &active,
	}}, "master-route-fixture", masteragentauth.SignerOptions{})
	require.NoError(t, err)
	return signer
}

func newAgentRouteUsageDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, models.AutoMigrate(db))
	require.NoError(t, db.Create(&models.ModelConfig{ModelName: "gpt-4o", Status: consts.StatusEnabled}).Error)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	return db
}

func newAgentRouteProvider(t *testing.T, calls, active *atomic.Int32, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		calls.Add(1)
		w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
		w.WriteHeader(status)
		if status >= http.StatusBadRequest {
			_, _ = fmt.Fprintf(w, `{"error":{"message":"provider %d"}}`, status)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-route", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	}))
	t.Cleanup(func() {
		requireAgentRouteActiveZero(t, active, "provider")
		server.Close()
	})
	return server
}

func (f *agentRouteSocketFixture) newAgent(id, providerURL string, trustedIngress bool) (*agent.Server, *httptest.Server) {
	f.t.Helper()
	cfg := &config.AgentRuntimeConfig{
		Agent: config.AgentConfig{
			CredentialsFile: filepath.Join(f.t.TempDir(), id+".json"), PreferredAddrTag: "local",
		},
		Runtime: config.RuntimeConfig{RelayTimeout: 1}, Relay: config.RelayConfig{Timeout: 1},
	}
	srv, err := agent.NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: id, Secret: "secret"})
	require.NoError(f.t, err)
	if id == "target" && f.targetCacheClient != nil {
		previous := srv.Store
		srv.Store = agentcache.NewStore(f.targetCacheClient, cfg.Agent.Cache)
		srv.Store.SetLogger(srv.Logger)
		previous.Close()
		requireAgentRouteDone(f.t, previous.Done(), "replaced target cache")
	}
	srv.Store.SetAgent(&models.Agent{AgentID: id, Status: consts.StatusEnabled})
	srv.Store.SetToken(&models.Token{ID: 1, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	srv.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: providerURL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	})
	srv.Store.RebuildModelIndex()
	srv.Store.LoadSettings([]models.Setting{
		{Key: consts.SettingAgentRelayFallbackEnabled, Value: "1"},
		{Key: "retry_max_channels", Value: "1"},
		{Key: "max_retries_per_channel", Value: "0"},
		{Key: "breaker_enabled", Value: "0"},
	})
	calls := &f.sourceCalls
	if id == "target" {
		calls = &f.targetCalls
	}
	_, err = events.SubscribeUsageCompleted(srv.Bus, func(ctx context.Context, entry protocol.UsageLogEntry) error {
		calls.publisher.Add(1)
		f.settler.Settle(ctx, id, []protocol.UsageLogEntry{entry})
		return nil
	})
	require.NoError(f.t, err)

	router := gin.New()
	if trustedIngress {
		f.mountManagedTargetRoutes(srv, router)
	} else {
		f.mountManagedSourceRoutes(srv, router)
	}
	httpServer := httptest.NewServer(trackAgentRouteActive(&f.agentHTTPActive, router))
	f.t.Cleanup(func() {
		requireAgentRouteActiveZero(f.t, &f.agentHTTPActive, id+" agent HTTP")
		httpServer.Close()
		if id == "source" {
			f.closeManagedSourceResources()
		}
		if id == "target" && f.targetTransport != nil {
			f.targetTransport.CloseIdleConnections()
		}
		shutdownAgentRouteServer(f.t, srv, id)
	})
	return srv, httpServer
}

func (f *agentRouteSocketFixture) mountManagedTargetRoutes(srv *agent.Server, router *gin.Engine) {
	f.targetTransport = mountManagedAttemptTarget(srv, router, func() agentproxy.ForwardAuthSnapshot {
		return f.forwardAuth
	}, managedAttemptTargetOption{ownership: &f.targetCalls, capture: &f.targetAttempt})
}

type managedAttemptTargetOption struct {
	ownership *agentOwnershipCalls
	capture   *atomic.Pointer[attemptwire.AttemptProxyMeta]
}

func mountManagedAttemptTarget(
	srv *agent.Server,
	router *gin.Engine,
	loadAuthSnapshot func() agentproxy.ForwardAuthSnapshot,
	options ...managedAttemptTargetOption,
) app.TransportPool {
	transport := upstream.NewTransportPool(32, 8, time.Second, upstream.KeepaliveConfig{
		Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3,
	})
	agentApp := agentappkg.NewDefaultAgentApplication(srv.Store, srv.BodyStore, srv.Logger, srv.Cfg, transport)
	option := managedAttemptTargetOption{}
	if len(options) > 0 {
		option = options[0]
	}
	if option.ownership != nil {
		agentApp = newAgentOwnershipApplication(srv, transport, option.ownership)
	}
	relayHandler := agentrelay.NewHandler(
		srv.Bus, agentApp, backend.NewDispatcher(agentApp), srv.Inflight, nil, nil,
	)
	attemptHandler := agentattemptproxy.NewHandler(
		agentattemptproxy.NewContextBuilder(agentApp),
		agentattemptproxy.NewBoundChannelFinder(agentApp.GetCache()),
		relayHandler.ProviderAttemptExecutor(),
		agentattemptproxy.NewResponseExecutor(),
	)
	handlers := []gin.HandlerFunc{
		agentattemptproxy.IngressMiddleware(agentattemptproxy.IngressConfig{
			FindAgentByID:    srv.Store.GetAgent,
			LoadAuthSnapshot: loadAuthSnapshot,
		}),
	}
	if option.capture != nil {
		handlers = append(handlers, func(c *gin.Context) {
			if meta, ok := attemptwire.MetaFromContext(c.Request.Context()); ok {
				captured := meta
				option.capture.Store(&captured)
			}
		})
	}
	handlers = append(handlers, agenttokenauth.TokenAuth(srv.Store), attemptHandler.Serve)
	router.POST(attemptwire.EndpointPath, handlers...)
	return transport
}

func (f *agentRouteSocketFixture) closeManagedSourceResources() {
	if f.sourceDirect != nil {
		ctx, cancel := agentRouteCleanupContext(f.t)
		require.NoError(f.t, f.sourceDirect.Close(ctx))
		cancel()
		requireAgentRouteDone(f.t, f.sourceDirect.Done(), "source direct forwarder")
	}
	if f.sourceTransport != nil {
		f.sourceTransport.CloseIdleConnections()
	}
}

func (f *agentRouteSocketFixture) mountManagedSourceRoutes(srv *agent.Server, router *gin.Engine) {
	f.sourceDirect = agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		ResponseHeaderTimeout: time.Second,
	})
	f.sourceTransport = upstream.NewTransportPool(32, 8, time.Second, upstream.KeepaliveConfig{
		Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3,
	})
	agentApp := newAgentOwnershipApplication(srv, f.sourceTransport, &f.sourceCalls)
	remote := relayexec.NewRemoteAttemptExecutor(relayexec.RemoteAttemptExecutorOptions{
		SourceAgentID: "source",
		Direct:        f.sourceDirect,
		Targets:       srv,
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) {
			if !f.issueForwardTickets.Load() {
				return "", errors.New("managed forward ticket unavailable")
			}
			ticket, _, err := f.signer.SignForward("source")
			return ticket, err
		},
		RelayEnabled: func() bool {
			return srv.Store.Settings().RelayFallbackEnabled == 1
		},
	})
	handler := agentrelay.NewHandler(
		srv.Bus, agentApp, backend.NewDispatcher(agentApp), srv.Inflight, nil, nil,
		agentrelay.WithAttemptRouting(
			"source",
			relayexec.NewAttemptRouteBuilder(agentApp.GetCache()),
			nil,
			remote,
		),
	)
	v1 := router.Group("/v1")
	v1.Use(agenttokenauth.TokenAuth(srv.Store))
	v1.POST("/chat/completions", handler.Relay)
}

func TestAgentRelayDirectRequiresManagedSourceHeaders(t *testing.T) {
	f := newAgentRouteSocketFixture(t, http.StatusOK)
	f.issueForwardTickets.Store(false)
	f.configureSourceRoute("", "target", f.targetAgent.URL)

	status, body := f.request("target")

	require.Equal(t, http.StatusBadGateway, status, body)
	require.Zero(t, f.targetProviderCalls.Load())
	require.Zero(t, f.sourceProviderCalls.Load())
}

func TestAgentRelayDirectBoundAttemptPreservesSourceModelAndMode(t *testing.T) {
	f := newAgentRouteSocketFixture(t, http.StatusOK)
	f.configureSourceRoute("target", "", f.targetAgent.URL)

	status, body := f.request("")

	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, int32(1), f.targetProviderCalls.Load())
	require.Zero(t, f.sourceProviderCalls.Load())
	require.Equal(t, &attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o",
			Mode:      attemptwire.ModePassthrough,
		},
		RequestPath: "/v1/chat/completions",
	}, f.targetAttempt.Load())
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1,
		targetAttemptLimiter: 1, sourcePublisher: 1,
	})
}

// behavior change: the source owns the complete attempt plan; a route on B
// cannot preempt A, and the target executes only the bound B attempt.
func TestAgentRelayCurrentAttemptChannelRouteRunsSourceThenTarget(t *testing.T) {
	f := newAgentRouteSocketFixture(t, http.StatusOK)
	failingA := newAgentRouteProvider(t, &f.sourceProviderCalls, &f.providerActive, http.StatusInternalServerError)
	f.setTwoAttemptChannels(failingA.URL)
	f.source.Store.SetAgent(&models.Agent{
		AgentID: "target", Status: consts.StatusEnabled,
		HTTPAddresses: fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, f.targetAgent.URL),
	})
	f.source.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 42, SourceType: "channel", SourceID: 8, Model: "gpt-4o", AgentID: "target", Priority: 100,
	})

	status, body := f.request("")

	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, int32(1), f.sourceProviderCalls.Load())
	require.Equal(t, int32(1), f.targetProviderCalls.Load())
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1, sourceAttemptLimiter: 1,
		targetAttemptLimiter: 1, sourcePublisher: 1,
	})
	usage := f.singleUsage()
	require.Equal(t, "target", usage.AgentID)
	require.Equal(t, "source", usage.RouteSourceAgentID)
	require.Equal(t, uint(42), usage.AgentRouteID)
	require.Equal(t, "direct", usage.AgentRoutePath)
	require.Len(t, usage.FallbackChain, 2)
	require.Equal(t, uint(7), usage.FallbackChain[0].ChannelID)
	require.Equal(t, uint(8), usage.FallbackChain[1].ChannelID)
}

// behavior change: a known remote provider failure advances the source-owned
// plan; it never retries the same bound attempt through another transport.
func TestAgentRelayTargetProviderFailureAdvancesNextSourceAttempt(t *testing.T) {
	f := newAgentRouteSocketFixture(t, http.StatusInternalServerError)
	f.setTwoAttemptChannels(f.sourceProvider.URL)
	f.source.Store.SetAgent(&models.Agent{
		AgentID: "target", Status: consts.StatusEnabled,
		HTTPAddresses: fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, f.targetAgent.URL),
	})
	f.source.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 43, SourceType: "channel", SourceID: 7, Model: "gpt-4o", AgentID: "target", Priority: 100,
	})

	status, body := f.request("")

	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, int32(1), f.targetProviderCalls.Load())
	require.Equal(t, int32(1), f.sourceProviderCalls.Load())
	require.Equal(t, uint(7), f.targetAttempt.Load().Attempt.Channel.ID)
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1, sourceAttemptLimiter: 1,
		targetAttemptLimiter: 1, sourcePublisher: 1,
	})
	usage := f.singleUsage()
	require.Equal(t, "source", usage.AgentID)
	require.Zero(t, usage.AgentRouteID)
	require.Equal(t, "local", usage.AgentRoutePath)
	require.Len(t, usage.FallbackChain, 2)
	require.Equal(t, uint(43), usage.FallbackChain[0].AgentRouteID)
	require.Equal(t, http.StatusInternalServerError, usage.FallbackChain[0].HTTPStatus)
	require.Equal(t, "none", usage.FallbackChain[1].AgentRouteKind)
}

func TestAgentRelayPrivateAndAdminIDCollisionUsesBoundSourceAndPrivateCache(t *testing.T) {
	privateProbe, privateProvider := newAgentRouteProviderProbe(t)
	adminProbe, adminProvider := newAgentRouteProviderProbe(t)
	privateChannel := syncedPrivateChannel(7, 11, privateProvider.URL, "private-key")
	client := &visiblePrivateChannelClient{
		visibleUserID: 11,
		set:           protocol.VisiblePrivateChannelSet{UserID: 11, Channels: []protocol.SyncedPrivateChannel{privateChannel}},
	}
	f := newAgentRouteSocketFixtureWithTargetClient(t, http.StatusOK, client)
	f.configurePrivateCollision(privateChannel, adminProvider.URL)

	for index := 1; index <= 2; index++ {
		status, body := f.requestWithOptions("", 0, fmt.Sprintf("req-private-%d", index))
		require.Equal(t, http.StatusOK, status, body)
		require.Equal(t, attemptwire.SourcePrivate, f.targetAttempt.Load().Attempt.Channel.Source)
	}
	require.Equal(t, int32(2), privateProbe.calls.Load())
	require.Equal(t, consts.BearerPrefix+"private-key", privateProbe.authorization.Load())
	require.Zero(t, adminProbe.calls.Load())
	require.Equal(t, int32(1), client.loads.Load())

	status, body := f.requestWithOptions("", 7, "req-admin-collision")
	require.Equal(t, http.StatusOK, status, body)
	require.Equal(t, attemptwire.SourceAdmin, f.targetAttempt.Load().Attempt.Channel.Source)
	require.Equal(t, int32(1), adminProbe.calls.Load())
	require.Equal(t, consts.BearerPrefix+"admin-key", adminProbe.authorization.Load())
	require.Equal(t, int32(2), privateProbe.calls.Load())
	require.Equal(t, int32(1), client.loads.Load())
	require.Equal(t, int32(3), f.sourceCalls.publisher.Load())
	require.Zero(t, f.targetCalls.publisher.Load())
}

func TestAgentRelayAttemptEndpointRejectsPrivateAuthBoundaries(t *testing.T) {
	tests := []struct {
		name          string
		tokenUserID   uint
		authorization string
		channels      func(string) []protocol.SyncedPrivateChannel
		wantStatus    int
	}{
		{
			name: "wrong token", tokenUserID: 11, authorization: consts.BearerPrefix + "wrong-token",
			channels: func(url string) []protocol.SyncedPrivateChannel {
				return []protocol.SyncedPrivateChannel{syncedPrivateChannel(7, 11, url, "private-key")}
			},
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong owner", tokenUserID: 12, authorization: consts.BearerPrefix + "route-token",
			channels: func(url string) []protocol.SyncedPrivateChannel {
				return []protocol.SyncedPrivateChannel{syncedPrivateChannel(7, 11, url, "private-key")}
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "bound channel is not visible", tokenUserID: 11, authorization: consts.BearerPrefix + "route-token",
			channels: func(url string) []protocol.SyncedPrivateChannel {
				return []protocol.SyncedPrivateChannel{syncedPrivateChannel(8, 11, url, "other-key")}
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			probe, provider := newAgentRouteProviderProbe(t)
			client := &visiblePrivateChannelClient{
				visibleUserID: 11,
				set:           protocol.VisiblePrivateChannelSet{UserID: 11, Channels: test.channels(provider.URL)},
			}
			f := newAgentRouteSocketFixtureWithTargetClient(t, http.StatusOK, client)
			f.target.Store.SetToken(&models.Token{
				ID: 1, UserID: test.tokenUserID, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1,
			})

			response := f.directBoundAttempt(test.authorization, attemptwire.BoundAttempt{
				Channel:   attemptwire.ChannelRef{Source: attemptwire.SourcePrivate, ID: 7},
				RealModel: "gpt-4o", Mode: attemptwire.ModePassthrough,
			})

			require.Equal(t, test.wantStatus, response.StatusCode)
			require.Zero(t, probe.calls.Load())
			require.Zero(t, f.targetProviderCalls.Load())
			require.Zero(t, f.targetCalls.attemptLimiter.Load())
			require.Zero(t, f.targetCalls.script.Load())
			require.Zero(t, f.targetCalls.planner.Load())
			require.Zero(t, f.targetCalls.requestLimiter.Load())
			require.Zero(t, f.targetCalls.publisher.Load())
		})
	}
}

func TestAgentRelayAttemptEndpointFailsClosedBeforeOrdinaryPlanner(t *testing.T) {
	t.Run("dedicated endpoint missing", func(t *testing.T) {
		f := newAgentRouteSocketFixture(t, http.StatusOK)
		var ordinaryPlanner atomic.Int32
		missingEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/chat/completions" {
				ordinaryPlanner.Add(1)
			}
			http.NotFound(w, r)
		}))
		t.Cleanup(missingEndpoint.Close)
		f.configureSourceRoute("", "target", missingEndpoint.URL)

		status, body := f.request("target")

		require.Equal(t, http.StatusBadGateway, status, body)
		require.Zero(t, ordinaryPlanner.Load())
		require.Zero(t, f.sourceProviderCalls.Load())
		require.Zero(t, f.targetProviderCalls.Load())
	})

	t.Run("untrusted public ingress", func(t *testing.T) {
		f := newAgentRouteSocketFixture(t, http.StatusOK)
		request, err := http.NewRequest(
			http.MethodPost,
			f.targetAgent.URL+attemptwire.EndpointPath,
			strings.NewReader(relayRequestBody(false)),
		)
		require.NoError(t, err)
		request.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
		request.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()

		require.Equal(t, http.StatusUnauthorized, response.StatusCode)
		require.Zero(t, f.targetProviderCalls.Load())
		require.Zero(t, f.targetCalls.attemptLimiter.Load())
		require.Zero(t, f.targetCalls.planner.Load())
	})

	t.Run("corrupt bound attempt", func(t *testing.T) {
		f := newAgentRouteSocketFixture(t, http.StatusOK)
		ticket, _, err := f.signer.SignForward("source")
		require.NoError(t, err)
		request, err := http.NewRequest(
			http.MethodPost,
			f.targetAgent.URL+attemptwire.EndpointPath,
			strings.NewReader(relayRequestBody(false)),
		)
		require.NoError(t, err)
		request.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
		request.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
		request.Header.Set(consts.HeaderXAgentForwardTicket, string(ticket))
		request.Header.Set(consts.HeaderXAgentRouteID, "0")
		request.Header.Set(consts.HeaderXAgentHop, "1")
		request.Header.Set(attemptwire.HeaderMeta, `{}`)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer response.Body.Close()

		require.Equal(t, http.StatusBadRequest, response.StatusCode)
		require.Zero(t, f.targetProviderCalls.Load())
		require.Zero(t, f.targetCalls.attemptLimiter.Load())
		require.Zero(t, f.targetCalls.planner.Load())
	})
}

// behavior change: a present-but-empty execution agent is not a legacy entry
// and must not fall back to the reporting source agent.
func TestAgentRelayUsageExecutionAgentPointerPresence(t *testing.T) {
	db := newAgentRouteUsageDB(t)
	settler := billing.NewSettler(&agentRouteDBProvider{db: db}, nil, zap.NewNop())
	empty := ""
	settler.Settle(t.Context(), "reporter", []protocol.UsageLogEntry{
		{RequestID: "req-legacy-agent", Timestamp: time.Now().Unix(), Status: 1},
		{RequestID: "req-explicit-empty-agent", Timestamp: time.Now().Unix(), Status: 0, ExecutionAgentID: &empty},
	})

	var rows []models.UsageLog
	require.NoError(t, db.Order("request_id").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Empty(t, rows[0].AgentID)
	require.Equal(t, "reporter", rows[1].AgentID)
}

func (f *agentRouteSocketFixture) directBoundAttempt(
	authorization string,
	attempt attemptwire.BoundAttempt,
) *http.Response {
	f.t.Helper()
	meta, err := attemptwire.EncodeMeta(attemptwire.AttemptProxyMeta{
		Attempt: attempt, RequestPath: "/v1/chat/completions",
	})
	require.NoError(f.t, err)
	ticket, _, err := f.signer.SignForward("source")
	require.NoError(f.t, err)
	req, err := http.NewRequest(
		http.MethodPost,
		f.targetAgent.URL+attemptwire.EndpointPath,
		strings.NewReader(relayRequestBody(false)),
	)
	require.NoError(f.t, err)
	req.Header.Set(consts.HeaderAuthorization, authorization)
	req.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	req.Header.Set(consts.HeaderXAgentForwardTicket, string(ticket))
	req.Header.Set(consts.HeaderXAgentRouteID, "0")
	req.Header.Set(consts.HeaderXAgentHop, "1")
	req.Header.Set(attemptwire.HeaderMeta, meta)
	response, err := http.DefaultClient.Do(req)
	require.NoError(f.t, err)
	f.t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func syncedPrivateChannel(id, ownerID uint, providerURL, key string) protocol.SyncedPrivateChannel {
	return protocol.SyncedPrivateChannel{
		ChannelCore: models.ChannelCore{
			ID: id, Type: consts.ChannelTypeOpenAI, BaseURL: providerURL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		OwnerID: ownerID, KeyPlaintext: key, Models: []string{"gpt-4o"},
	}
}

func (f *agentRouteSocketFixture) configurePrivateCollision(
	private protocol.SyncedPrivateChannel,
	adminProviderURL string,
) {
	f.t.Helper()
	f.source.Store.SetToken(&models.Token{
		ID: 1, UserID: 11, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1,
	})
	f.target.Store.SetToken(&models.Token{
		ID: 1, UserID: 11, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1,
	})
	f.source.Store.OverrideVisiblePrivateChannels(11, []protocol.SyncedPrivateChannel{private})
	f.source.Store.SetChannel(agentRouteChannel(7, adminProviderURL, 100))
	f.target.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: adminProviderURL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "admin-key", Models: "gpt-4o",
	})
	f.source.Store.RebuildModelIndex()
	f.target.Store.RebuildModelIndex()
	f.source.Store.SetAgent(&models.Agent{
		AgentID: "target", Status: consts.StatusEnabled,
		HTTPAddresses: fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, f.targetAgent.URL),
	})
	f.source.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 44, SourceType: "token", SourceID: 1, Model: "gpt-4o", AgentID: "target", Priority: 100,
	})
}

type agentOwnershipWant struct {
	sourceScript         int32
	sourcePlanner        int32
	sourceRequestLimiter int32
	sourceAttemptLimiter int32
	targetAttemptLimiter int32
	sourcePublisher      int32
}

func (f *agentRouteSocketFixture) requireOwnership(want agentOwnershipWant) {
	f.t.Helper()
	requireAgentOwnership(f.t, &f.sourceCalls, &f.targetCalls, want)
}

func (f *agentRouteSocketFixture) setTwoAttemptChannels(sourceAURL string) {
	f.t.Helper()
	configureTwoAttemptAgents(
		f.source, f.target, sourceAURL, f.sourceProvider.URL, f.targetProvider.URL, f.targetProvider.URL,
	)
}

func agentRouteChannel(id uint, providerURL string, priority int) *models.Channel {
	return &models.Channel{
		ChannelCore: models.ChannelCore{
			ID: id, Type: consts.ChannelTypeOpenAI, BaseURL: providerURL,
			Status: consts.StatusEnabled, Weight: 1, Priority: priority, PassthroughEnabled: true,
		},
		Key: fmt.Sprintf("provider-key-%d", id), Models: "gpt-4o",
	}
}

func (f *agentRouteSocketFixture) singleUsage() models.UsageLog {
	f.t.Helper()
	require.Eventually(f.t, func() bool {
		var count int64
		return f.db.Model(&models.UsageLog{}).Count(&count).Error == nil && count == 1
	}, time.Second, 5*time.Millisecond)
	var usage models.UsageLog
	require.NoError(f.t, f.db.First(&usage).Error)
	return usage
}

func (f *agentRouteSocketFixture) configureSourceRoute(routeTarget, hardTarget, address string) {
	f.t.Helper()
	targetID := routeTarget
	if hardTarget != "" {
		targetID = hardTarget
	}
	if targetID == "" {
		return
	}
	httpAddresses := ""
	if address != "" {
		httpAddresses = fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, address)
	}
	f.source.Store.SetAgent(&models.Agent{
		AgentID: targetID, Status: consts.StatusEnabled, HTTPAddresses: httpAddresses,
	})
	if routeTarget != "" {
		f.source.Store.RouteIndex.Put(&models.AgentRoute{
			ID: 42, SourceType: "token", SourceID: 1, Model: "gpt-4o", AgentID: routeTarget, Priority: 100,
		})
	}
}

func (f *agentRouteSocketFixture) request(hardTarget string) (int, string) {
	return f.requestWithOptions(hardTarget, 0, "")
}

func (f *agentRouteSocketFixture) requestWithOptions(hardTarget string, channelID uint, requestID string) (int, string) {
	f.t.Helper()
	body := `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}]}`
	req, err := http.NewRequest(http.MethodPost, f.sourceAgent.URL+"/v1/chat/completions", strings.NewReader(body))
	require.NoError(f.t, err)
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
	req.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	req.Header.Set(consts.HeaderXRequestID, "req-"+strings.NewReplacer(" ", "-", "/", "-").Replace(f.t.Name()))
	if hardTarget != "" {
		req.Header.Set(consts.HeaderXAgentID, hardTarget)
	}
	if channelID != 0 {
		req.Header.Set(consts.HeaderXChannelID, fmt.Sprint(channelID))
	}
	if requestID != "" {
		req.Header.Set(consts.HeaderXRequestID, requestID)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(f.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(f.t, err)
	return resp.StatusCode, string(data)
}

func (f *agentRouteSocketFixture) closedTCPAddress() string {
	f.t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(f.t, err)
	address := listener.Addr().String()
	require.NoError(f.t, listener.Close())
	return "http://" + address
}

func (f *agentRouteSocketFixture) uncertainAddress() string {
	f.t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(f.t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		f.uncertainTargetCalls.Add(1)
		reader := bufio.NewReader(conn)
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil || line == "\r\n" {
				break
			}
		}
		_ = conn.Close()
	}()
	f.t.Cleanup(func() {
		_ = listener.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			f.t.Error("uncertain target socket did not stop")
		}
	})
	return "http://" + listener.Addr().String()
}

func TestAgentRelayRealSourceHandlerUsageAndNoReplayMatrix(t *testing.T) {
	t.Run("relay target provider", func(t *testing.T) {
		f := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
		status, body, err := f.request(t.Context(), false)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, `"content":"ok"`)
		f.requireProviderAndUsage(0, 1, 1, "target", "relay")
	})

	t.Run("pre COMMIT target unavailable falls back local", func(t *testing.T) {
		f := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, false)
		status, body, err := f.request(t.Context(), false)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		f.requireProviderAndUsage(1, 0, 1, "source", "local")
	})

	t.Run("COMMIT then websocket drop never replays local provider", func(t *testing.T) {
		barrier := newRelayTargetBarrier()
		f := newRoutedRelayFixture(t, relayProviderSuccess(nil), barrier, true)
		result := make(chan routedRelayResponse, 1)
		go func() {
			status, body, err := f.request(t.Context(), false)
			result <- routedRelayResponse{status: status, body: body, err: err}
		}()
		barrier.wait(t)
		f.closeHub()
		barrier.waitCanceled(t)
		select {
		case <-result:
		case <-time.After(2 * time.Second):
			t.Fatal("source request did not finish after relay websocket drop")
		}
		f.requireProviderAndUsage(0, 0, 1, "", "relay")
		require.Equal(t, int32(1), f.sourceCalls.publisher.Load())
		require.Zero(t, f.targetCalls.publisher.Load())
	})

	t.Run("SSE interruption never replays local provider", func(t *testing.T) {
		f := newRoutedRelayFixture(t, relayProviderInterruptedSSE(), nil, true)
		status, body, err := f.request(t.Context(), true)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, status, body)
		require.Contains(t, body, "data:")
		f.requireProviderAndUsage(0, 1, 1, "target", "relay")
	})

	t.Run("client cancel after COMMIT before provider never replays local provider", func(t *testing.T) {
		barrier := newRelayTargetBarrier()
		f := newRoutedRelayFixture(t, relayProviderSuccess(nil), barrier, true)
		requestCtx, cancelRequest := context.WithCancel(t.Context())
		result := make(chan error, 1)
		go func() {
			_, _, err := f.request(requestCtx, false)
			result <- err
		}()
		barrier.wait(t)
		cancelRequest()
		barrier.waitCanceled(t)
		select {
		case err := <-result:
			require.ErrorIs(t, err, context.Canceled)
		case <-time.After(2 * time.Second):
			t.Fatal("canceled source request did not finish")
		}
		f.requireProviderAndUsage(0, 0, 1, "", "relay")
		require.Equal(t, int32(1), f.sourceCalls.publisher.Load())
		require.Zero(t, f.targetCalls.publisher.Load())
	})
}

func TestAgentRelayMissingForwardCapabilityFallsBackToRelay(t *testing.T) {
	f := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
	var directCalls atomic.Int32
	directTarget := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		directCalls.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(directTarget.Close)
	f.source.Store.SetAgent(&models.Agent{
		AgentID: "target", Status: consts.StatusEnabled,
		HTTPAddresses: fmt.Sprintf(`[{"url":%q,"tag":"local"}]`, directTarget.URL),
	})
	f.source.Store.SetAgentCapabilities("target", []string{protocol.AgentCapabilityTunnelV1})

	status, body, err := f.request(t.Context(), false)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, body)
	require.Zero(t, directCalls.Load())
	f.requireProviderAndUsage(0, 1, 1, "target", "relay")
}

func TestAgentRelayDirectUnavailableUsesRealRelayAttemptEndpoint(t *testing.T) {
	f := newOwnedRoutedRelayFixture(t, relayProviderSuccess(nil), true)

	status, body, err := f.request(t.Context(), false)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, body)
	require.Contains(t, body, `"content":"ok"`)
	require.Zero(t, f.sourceProviderCalls.Load())
	require.Equal(t, int32(1), f.targetProviderCalls.Load())
	require.Equal(t, uint(7), f.targetAttempt.Load().Attempt.Channel.ID)
	require.Equal(t, attemptwire.SourceAdmin, f.targetAttempt.Load().Attempt.Channel.Source)
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1,
		targetAttemptLimiter: 1, sourcePublisher: 1,
	})
	usage := f.singleUsage()
	require.Equal(t, "target", usage.AgentID)
	require.Equal(t, "source", usage.RouteSourceAgentID)
	require.Equal(t, uint(77), usage.AgentRouteID)
	require.Equal(t, "relay", usage.AgentRoutePath)
	require.Len(t, usage.FallbackChain, 1)
	require.Len(t, usage.FallbackChain[0].AgentPaths, 2)
	require.Equal(t, models.AgentPathDirect, usage.FallbackChain[0].AgentPaths[0].Path)
	require.Equal(t, models.AgentPathUnavailable, usage.FallbackChain[0].AgentPaths[0].Result)
	require.Equal(t, models.AgentPathRelay, usage.FallbackChain[0].AgentPaths[1].Path)
	require.Equal(t, models.AgentPathSelected, usage.FallbackChain[0].AgentPaths[1].Result)
}

func TestAgentRelayHardTargetStaysFrozenAcrossProviderFailure(t *testing.T) {
	var providerAttempts atomic.Int32
	targetProvider := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if providerAttempts.Add(1) == 1 {
			w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"retry next bound attempt"}}`)
			return
		}
		relayProviderSuccess(nil).ServeHTTP(w, r)
	})
	f := newOwnedRoutedRelayFixture(t, targetProvider, true)
	f.setTwoAttemptChannels()

	status, body, err := f.requestWithSelector(t.Context(), false, "target", "", "req-hard-frozen")

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, body)
	require.Zero(t, f.sourceProviderCalls.Load())
	require.Equal(t, int32(2), f.targetProviderCalls.Load())
	require.Equal(t, uint(8), f.targetAttempt.Load().Attempt.Channel.ID)
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1,
		targetAttemptLimiter: 2, sourcePublisher: 1,
	})
	usage := f.singleUsage()
	require.Equal(t, "target", usage.AgentID)
	require.Len(t, usage.FallbackChain, 2)
	for _, attempt := range usage.FallbackChain {
		require.Equal(t, "hard", attempt.AgentRouteKind)
		require.Zero(t, attempt.AgentRouteID)
		require.Equal(t, models.AgentPathRelay, attempt.AgentPaths[len(attempt.AgentPaths)-1].Path)
	}
}

func TestAgentRelayHardUnavailableFailsWithoutSourceProvider(t *testing.T) {
	f := newOwnedRoutedRelayFixture(t, relayProviderSuccess(nil), false)

	status, body, err := f.requestWithSelector(t.Context(), false, "target", "", "req-hard-unavailable")

	require.NoError(t, err)
	require.Equal(t, http.StatusBadGateway, status, body)
	require.Zero(t, f.sourceProviderCalls.Load())
	require.Zero(t, f.targetProviderCalls.Load())
	f.requireOwnership(agentOwnershipWant{
		sourceScript: 1, sourcePlanner: 1, sourceRequestLimiter: 1, sourcePublisher: 1,
	})
	usage := f.singleUsage()
	require.Empty(t, usage.AgentID)
	require.Equal(t, "source", usage.RouteSourceAgentID)
	require.Equal(t, "relay", usage.AgentRoutePath)
	require.Len(t, usage.FallbackChain, 1)
	require.Len(t, usage.FallbackChain[0].AgentPaths, 2)
}

func TestAgentRelayHardTagFailsOverThenFreezesReachedMember(t *testing.T) {
	var f *routedRelayFixture
	var providerAttempts atomic.Int32
	targetProvider := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if providerAttempts.Add(1) == 1 {
			f.source.Store.SetAgent(&models.Agent{AgentID: "target", Status: consts.StatusEnabled})
			f.source.Store.SetAgent(&models.Agent{AgentID: "unreachable", Status: consts.StatusEnabled})
			w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(w, `{"error":{"message":"freeze reached member"}}`)
			return
		}
		relayProviderSuccess(nil).ServeHTTP(w, r)
	})
	f = newOwnedRoutedRelayFixture(t, targetProvider, true)
	f.setTwoAttemptChannels()
	f.source.Store.SetAgent(&models.Agent{AgentID: "target", Status: consts.StatusEnabled, Tags: "hard-pool"})
	f.source.Store.SetAgent(&models.Agent{AgentID: "unreachable", Status: consts.StatusEnabled, Tags: "hard-pool"})
	requestID := requestIDForAgentOrder(t, "hard-pool", "unreachable", "target")

	status, body, err := f.requestWithSelector(t.Context(), false, "", "hard-pool", requestID)

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status, body)
	require.Zero(t, f.sourceProviderCalls.Load())
	require.Equal(t, int32(2), f.targetProviderCalls.Load())
	usage := f.singleUsage()
	require.Len(t, usage.FallbackChain, 2)
	firstPaths := usage.FallbackChain[0].AgentPaths
	require.Len(t, firstPaths, 4)
	require.Equal(t, "unreachable", firstPaths[0].AgentID)
	require.Equal(t, models.AgentPathDirect, firstPaths[0].Path)
	require.Equal(t, models.AgentPathUnavailable, firstPaths[0].Result)
	require.Equal(t, models.AgentPathNotCommitted, firstPaths[0].CommitState)
	require.Equal(t, "unreachable", firstPaths[1].AgentID)
	require.Equal(t, models.AgentPathRelay, firstPaths[1].Path)
	require.Equal(t, models.AgentPathUnavailable, firstPaths[1].Result)
	require.Equal(t, models.AgentPathNotCommitted, firstPaths[1].CommitState)
	for _, path := range firstPaths[2:] {
		require.Equal(t, "target", path.AgentID)
	}
	secondPaths := usage.FallbackChain[1].AgentPaths
	require.NotEmpty(t, secondPaths)
	for _, path := range secondPaths {
		require.Equal(t, "target", path.AgentID)
	}
}

func requestIDForAgentOrder(t *testing.T, tag string, want ...string) string {
	t.Helper()
	for index := 0; index < 1000; index++ {
		requestID := fmt.Sprintf("req-hard-tag-%d", index)
		if equalAgentOrder(want, agentproxy.StableAgentRing(requestID, 0, tag, want)) {
			return requestID
		}
	}
	t.Fatal("could not find stable request ID for agent order")
	return ""
}

func equalAgentOrder(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func (f *routedRelayFixture) requireOwnership(want agentOwnershipWant) {
	f.t.Helper()
	requireAgentOwnership(f.t, &f.sourceCalls, &f.targetCalls, want)
}

func (f *routedRelayFixture) singleUsage() models.UsageLog {
	f.t.Helper()
	require.Eventually(f.t, func() bool { return f.usageRowCount() == 1 }, time.Second, 5*time.Millisecond)
	var usage models.UsageLog
	require.NoError(f.t, f.db.First(&usage).Error)
	return usage
}

func (f *routedRelayFixture) setTwoAttemptChannels() {
	f.t.Helper()
	configureTwoAttemptAgents(
		f.source, f.target, f.sourceProvider.URL, f.sourceProvider.URL, f.targetProvider.URL, f.targetProvider.URL,
	)
}

func requireAgentOwnership(
	t *testing.T,
	source, target *agentOwnershipCalls,
	want agentOwnershipWant,
) {
	t.Helper()
	require.Equal(t, want.sourceScript, source.script.Load())
	require.Equal(t, want.sourcePlanner, source.planner.Load())
	require.Equal(t, want.sourceRequestLimiter, source.requestLimiter.Load())
	require.Equal(t, want.sourceAttemptLimiter, source.attemptLimiter.Load())
	require.Equal(t, want.targetAttemptLimiter, target.attemptLimiter.Load())
	require.Equal(t, want.sourcePublisher, source.publisher.Load())
	require.Zero(t, target.script.Load())
	require.Zero(t, target.planner.Load())
	require.Zero(t, target.requestLimiter.Load())
	require.Zero(t, target.publisher.Load())
}

func configureTwoAttemptAgents(
	source, target *agent.Server,
	sourceAURL, sourceBURL, targetAURL, targetBURL string,
) {
	for _, srv := range []*agent.Server{source, target} {
		srv.Store.LoadSettings([]models.Setting{
			{Key: consts.SettingAgentRelayFallbackEnabled, Value: "1"},
			{Key: "retry_max_channels", Value: "2"},
			{Key: "max_retries_per_channel", Value: "0"},
			{Key: "breaker_enabled", Value: "0"},
		})
	}
	source.Store.SetChannel(agentRouteChannel(7, sourceAURL, 200))
	source.Store.SetChannel(agentRouteChannel(8, sourceBURL, 100))
	target.Store.SetChannel(agentRouteChannel(7, targetAURL, 200))
	target.Store.SetChannel(agentRouteChannel(8, targetBURL, 100))
	source.Store.RebuildModelIndex()
	target.Store.RebuildModelIndex()
}

type routedRelayResponse struct {
	status int
	body   string
	err    error
}

type relayTargetBarrier struct {
	entered     chan struct{}
	canceled    chan struct{}
	releaseGate chan struct{}
	enterOnce   sync.Once
	cancelOnce  sync.Once
	releaseOnce sync.Once
}

func newRelayTargetBarrier() *relayTargetBarrier {
	return &relayTargetBarrier{entered: make(chan struct{}), canceled: make(chan struct{}), releaseGate: make(chan struct{})}
}

func (b *relayTargetBarrier) handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b.enterOnce.Do(func() { close(b.entered) })
		select {
		case <-b.releaseGate:
			next.ServeHTTP(w, r)
		case <-r.Context().Done():
			b.cancelOnce.Do(func() { close(b.canceled) })
		}
	})
}

func (b *relayTargetBarrier) wait(t *testing.T) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("target committed handler did not start")
	}
}

func (b *relayTargetBarrier) waitCanceled(t *testing.T) {
	t.Helper()
	select {
	case <-b.canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("target committed request context was not canceled")
	}
}

func (b *relayTargetBarrier) release() {
	b.releaseOnce.Do(func() { close(b.releaseGate) })
}

type routedRelayAgentLookup struct {
	mu           sync.RWMutex
	capabilities map[string][]string
}

func newRoutedRelayAgentLookup() *routedRelayAgentLookup {
	return &routedRelayAgentLookup{capabilities: map[string][]string{
		"source": {protocol.AgentCapabilityTunnelV1},
		"target": {protocol.AgentCapabilityTunnelV1},
	}}
}

func (*routedRelayAgentLookup) GetByAgentID(_ context.Context, id string) (*models.Agent, error) {
	if id != "source" && id != "target" {
		return nil, gorm.ErrRecordNotFound
	}
	return &models.Agent{AgentID: id, Status: consts.StatusEnabled}, nil
}

func (l *routedRelayAgentLookup) Capabilities(agentID string) []string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return append([]string(nil), l.capabilities[agentID]...)
}

func (l *routedRelayAgentLookup) SetCapabilities(agentID string, capabilities []string) {
	l.mu.Lock()
	l.capabilities[agentID] = append([]string(nil), capabilities...)
	l.mu.Unlock()
}

type routedRelayTicketProvider struct {
	agentID string
	signer  *masteragentauth.Signer
}

func (p routedRelayTicketProvider) RelayTicket(_ context.Context, generation uint64) (agentauth.RelayTicket, error) {
	ticket, _, err := p.signer.SignRelay(p.agentID, generation)
	return ticket, err
}

type routedRelayManagerRun struct {
	cancel context.CancelFunc
	done   chan error
}

type routedRelayFixture struct {
	t                   *testing.T
	db                  *gorm.DB
	admission           *mastertunnel.AdmissionGate
	lookup              *routedRelayAgentLookup
	signer              *masteragentauth.Signer
	limits              wire.Limits
	hub                 *mastertunnel.Hub
	hubServer           *httptest.Server
	source              *agent.Server
	target              *agent.Server
	sourceHTTP          *httptest.Server
	sourceProvider      *httptest.Server
	targetProvider      *httptest.Server
	sourceDirect        *agentproxy.DirectForwarder
	sourceTransport     app.TransportPool
	targetTransport     app.TransportPool
	barrier             *relayTargetBarrier
	managerRuns         []routedRelayManagerRun
	closeOnce           sync.Once
	hubCloseOnce        sync.Once
	sourceProviderCalls atomic.Int32
	targetProviderCalls atomic.Int32
	providerActive      atomic.Int32
	httpActive          atomic.Int32
	hubHTTPActive       atomic.Int32
	sourceCalls         agentOwnershipCalls
	targetCalls         agentOwnershipCalls
	targetAttempt       atomic.Pointer[attemptwire.AttemptProxyMeta]
}

func newRoutedRelayFixture(t *testing.T, targetProviderHandler http.Handler, barrier *relayTargetBarrier, connectTarget bool) *routedRelayFixture {
	return newRoutedRelayFixtureWithOwnership(t, targetProviderHandler, barrier, connectTarget, false)
}

func newOwnedRoutedRelayFixture(t *testing.T, targetProviderHandler http.Handler, connectTarget bool) *routedRelayFixture {
	return newRoutedRelayFixtureWithOwnership(t, targetProviderHandler, nil, connectTarget, true)
}

func newRoutedRelayFixtureWithOwnership(
	t *testing.T,
	targetProviderHandler http.Handler,
	barrier *relayTargetBarrier,
	connectTarget bool,
	ownership bool,
) *routedRelayFixture {
	t.Helper()
	f := &routedRelayFixture{t: t, db: newAgentRouteUsageDB(t), barrier: barrier}
	f.signer = newAgentRouteSigner(t)
	f.limits = wire.Limits{
		MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
		MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
	}
	f.admission = &mastertunnel.AdmissionGate{}
	f.admission.Set(true)
	f.lookup = newRoutedRelayAgentLookup()
	f.hub = mastertunnel.NewHub(mastertunnel.HubOptions{
		InstanceID: "master-route-fixture", Signer: f.signer, Agents: f.lookup,
		Admission: f.admission, Limits: f.limits, Logger: zap.NewNop(),
	})
	hubRouter := gin.New()
	hubRouter.GET("/ws/agent-relay", f.hub.HandleWS)
	f.hubServer = httptest.NewServer(trackAgentRouteActive(&f.hubHTTPActive, hubRouter))
	relayURI := "ws" + strings.TrimPrefix(f.hubServer.URL, "http") + "/ws/agent-relay"

	f.sourceProvider = f.newProvider(&f.sourceProviderCalls, relayProviderSuccess(nil))
	f.targetProvider = f.newProvider(&f.targetProviderCalls, targetProviderHandler)
	f.source = f.newAgent("source", f.sourceProvider.URL)
	f.target = f.newAgent("target", f.targetProvider.URL)

	sourceRouter := f.setupRoutedRelayRouters(relayURI, connectTarget, ownership)
	f.source.Store.SetAgent(&models.Agent{AgentID: "target", Status: consts.StatusEnabled})
	f.target.Store.SetAgent(&models.Agent{AgentID: "source", Status: consts.StatusEnabled})
	f.source.Store.RouteIndex.Put(&models.AgentRoute{
		ID: 77, SourceType: "token", SourceID: 1, Model: "gpt-4o", AgentID: "target", Priority: 100,
	})
	f.sourceHTTP = httptest.NewServer(trackAgentRouteActive(&f.httpActive, sourceRouter))
	t.Cleanup(f.close)
	return f
}

func (f *routedRelayFixture) setupRoutedRelayRouters(
	relayURI string,
	connectTarget, ownership bool,
) *gin.Engine {
	sourceRouter, targetRouter := gin.New(), gin.New()
	targetRouter.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "role": "master"})
	})
	targetHandler := http.Handler(targetRouter)
	if f.barrier != nil {
		targetHandler = f.barrier.handler(targetRouter)
	}
	f.installManager(f.source, "source", f.signer, f.limits, f.source.NewTunnelTargetHandler(sourceRouter))
	if connectTarget {
		f.installManager(f.target, "target", f.signer, f.limits, f.target.NewTunnelTargetHandler(targetHandler))
	} else if previous := f.target.TunnelManager; previous != nil {
		closeAgentRouteManager(f.t, previous, "unused target manager")
		f.target.TunnelManager = nil
	}
	if ownership {
		f.mountRoutedSourceRoutes(sourceRouter)
	} else {
		f.source.MountRoutes(sourceRouter)
	}
	if connectTarget && ownership {
		f.mountRoutedTargetRoutes(targetRouter)
	} else if connectTarget {
		f.target.MountRoutes(targetRouter)
	}
	f.startManagers(relayURI)
	return sourceRouter
}

func TestRelayProbeReachesEmbeddedAgentThroughSharedMasterPing(t *testing.T) {
	fixture := newRoutedRelayFixture(t, relayProviderSuccess(nil), nil, true)
	sourceGeneration := fixture.source.TunnelManager.Snapshot().SessionGeneration
	targetGeneration := fixture.target.TunnelManager.Snapshot().SessionGeneration
	prober := agentrpc.NewRelayProber(agentrpc.RelayProberOptions{
		Link: fixture.source.TunnelManager,
		RelayGeneration: func() uint64 {
			return fixture.source.TunnelManager.Snapshot().SessionGeneration
		},
	})

	result := prober.Probe(t.Context(), protocol.RelayProbeTarget{
		TargetAgentID: "target", SourceRelayGeneration: sourceGeneration, TargetRelayGeneration: targetGeneration,
	})
	require.Equal(t, protocol.RelayProbeReachable, result.State, "%+v", result)
	require.Equal(t, protocol.RelayProbeStageResponse, result.Stage)
	require.Empty(t, result.ReasonCode)
	require.Zero(t, fixture.sourceProviderCalls.Load())
	require.Zero(t, fixture.targetProviderCalls.Load())
}

func (f *routedRelayFixture) newProvider(calls *atomic.Int32, handler http.Handler) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.providerActive.Add(1)
		defer f.providerActive.Add(-1)
		calls.Add(1)
		handler.ServeHTTP(w, r)
	}))
}

func (f *routedRelayFixture) newAgent(id, providerURL string) *agent.Server {
	f.t.Helper()
	cfg := &config.AgentRuntimeConfig{
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(f.t.TempDir(), id+"-relay.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 2}, Relay: config.RelayConfig{Timeout: 2},
	}
	srv, err := agent.NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: id, Secret: "secret"})
	require.NoError(f.t, err)
	srv.Store.SetAgent(&models.Agent{AgentID: id, Status: consts.StatusEnabled})
	srv.Store.SetToken(&models.Token{ID: 1, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	srv.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: providerURL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	})
	srv.Store.RebuildModelIndex()
	srv.Store.LoadSettings([]models.Setting{
		{Key: consts.SettingAgentRelayFallbackEnabled, Value: "1"},
		{Key: "retry_max_channels", Value: "1"},
		{Key: "max_retries_per_channel", Value: "0"},
		{Key: "breaker_enabled", Value: "0"},
	})
	settler := billing.NewSettler(&agentRouteDBProvider{db: f.db}, nil, zap.NewNop())
	calls := &f.sourceCalls
	if id == "target" {
		calls = &f.targetCalls
	}
	_, err = events.SubscribeUsageCompleted(srv.Bus, func(ctx context.Context, entry protocol.UsageLogEntry) error {
		calls.publisher.Add(1)
		settler.Settle(ctx, id, []protocol.UsageLogEntry{entry})
		return nil
	})
	require.NoError(f.t, err)
	return srv
}

func (f *routedRelayFixture) mountRoutedSourceRoutes(router *gin.Engine) {
	f.sourceDirect = agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{ResponseHeaderTimeout: time.Second})
	f.sourceTransport = upstream.NewTransportPool(32, 8, time.Second, upstream.KeepaliveConfig{
		Idle: 15 * time.Second, Interval: 15 * time.Second, Count: 3,
	})
	agentApp := newAgentOwnershipApplication(f.source, f.sourceTransport, &f.sourceCalls)
	remote := relayexec.NewRemoteAttemptExecutor(relayexec.RemoteAttemptExecutorOptions{
		SourceAgentID: "source", Direct: f.sourceDirect, Relay: f.source.TunnelManager, Targets: f.source,
		CachedForwardTicket: func() (agentauth.ForwardTicket, error) {
			ticket, _, err := f.signer.SignForward("source")
			return ticket, err
		},
		RelayEnabled: func() bool { return f.source.Store.Settings().RelayFallbackEnabled == 1 },
	})
	handler := agentrelay.NewHandler(
		f.source.Bus, agentApp, backend.NewDispatcher(agentApp), f.source.Inflight, nil, nil,
		agentrelay.WithAttemptRouting(
			"source", relayexec.NewAttemptRouteBuilder(agentApp.GetCache()), nil, remote,
		),
	)
	v1 := router.Group("/v1")
	v1.Use(agenttokenauth.TokenAuth(f.source.Store))
	v1.POST("/chat/completions", handler.Relay)
}

func (f *routedRelayFixture) mountRoutedTargetRoutes(router *gin.Engine) {
	f.targetTransport = mountManagedAttemptTarget(
		f.target,
		router,
		func() agentproxy.ForwardAuthSnapshot {
			return agentproxy.ForwardAuthSnapshot{
				Capabilities: []string{protocol.AgentCapabilityForwardV1},
				SigningKeys:  []agentauth.PublicKey{f.signer.PublicKey()},
			}
		},
		managedAttemptTargetOption{ownership: &f.targetCalls, capture: &f.targetAttempt},
	)
}

func (f *routedRelayFixture) installManager(
	srv *agent.Server,
	agentID string,
	signer *masteragentauth.Signer,
	limits wire.Limits,
	targetHandler *agenttunnel.TargetHandler,
) {
	f.t.Helper()
	if previous := srv.TunnelManager; previous != nil {
		closeAgentRouteManager(f.t, previous, agentID+" previous manager")
	}
	bootstrap := agentauthcache.BootstrapSnapshot{
		MasterInstanceID: "master-route-fixture",
		Capabilities:     []string{protocol.AgentCapabilityTunnelV1},
		SigningKeys:      []agentauth.PublicKey{signer.PublicKey()},
	}
	dialer := agenttunnel.NewClientDialer(agenttunnel.ClientDialerOptions{
		AgentID: agentID, Bootstrap: func() agentauthcache.BootstrapSnapshot { return bootstrap },
		Limits: func() wire.Limits { return limits }, DrainTimeout: func() time.Duration { return time.Second },
		TargetHandler: targetHandler, Logger: zap.NewNop(),
	})
	srv.TunnelManager = agenttunnel.NewManager(agenttunnel.ManagerOptions{
		Dialer: dialer, Tickets: routedRelayTicketProvider{agentID: agentID, signer: signer}, Limits: limits,
		DrainTimeout: time.Second, BackoffMin: time.Millisecond, BackoffMax: 10 * time.Millisecond, Logger: zap.NewNop(),
	})
}

func (f *routedRelayFixture) startManagers(relayURI string) {
	f.t.Helper()
	for _, srv := range []*agent.Server{f.source, f.target} {
		if srv.TunnelManager == nil {
			continue
		}
		runCtx, cancel := context.WithCancel(context.WithoutCancel(f.t.Context()))
		done := make(chan error, 1)
		go func(manager *agenttunnel.Manager) { done <- manager.Run(runCtx) }(srv.TunnelManager)
		srv.TunnelManager.Apply(agenttunnel.Desired{Mode: "custom", ConfiguredURI: relayURI, EffectiveURI: relayURI})
		f.managerRuns = append(f.managerRuns, routedRelayManagerRun{cancel: cancel, done: done})
	}
	require.Eventually(f.t, func() bool {
		return f.source.TunnelManager.Snapshot().AcceptingNewStreams
	}, 2*time.Second, 5*time.Millisecond)
	if f.target.TunnelManager != nil {
		require.Eventually(f.t, func() bool {
			return f.target.TunnelManager.Snapshot().AcceptingNewStreams
		}, 2*time.Second, 5*time.Millisecond)
	}
}

func (f *routedRelayFixture) request(ctx context.Context, stream bool) (int, string, error) {
	return f.requestWithSelector(ctx, stream, "", "", "")
}

func (f *routedRelayFixture) requestWithSelector(
	ctx context.Context,
	stream bool,
	agentID, agentTag, requestID string,
) (int, string, error) {
	body := relayRequestBody(stream)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.sourceHTTP.URL+"/v1/chat/completions", strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set(consts.HeaderAuthorization, consts.BearerPrefix+"route-token")
	req.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	if agentID != "" {
		req.Header.Set(consts.HeaderXAgentID, agentID)
	}
	if agentTag != "" {
		req.Header.Set(consts.HeaderXAgentTag, agentTag)
	}
	if requestID != "" {
		req.Header.Set(consts.HeaderXRequestID, requestID)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(data), err
}

func (f *routedRelayFixture) requireProviderAndUsage(source, target int32, rows int64, agentID, path string) {
	f.t.Helper()
	require.Eventually(f.t, func() bool {
		return f.sourceProviderCalls.Load() == source && f.targetProviderCalls.Load() == target && f.usageRowCount() == rows
	}, time.Second, 5*time.Millisecond)
	require.Equal(f.t, source, f.sourceProviderCalls.Load())
	require.Equal(f.t, target, f.targetProviderCalls.Load())
	if rows == 0 {
		require.Never(f.t, func() bool {
			return f.sourceProviderCalls.Load() != 0 || f.targetProviderCalls.Load() != 0 || f.usageRowCount() != 0
		}, 50*time.Millisecond, 5*time.Millisecond)
		return
	}
	var usage models.UsageLog
	require.NoError(f.t, f.db.First(&usage).Error)
	// behavior change: remote attempt usage belongs to the target provider
	// agent even when an older rollout caller still passes the source ID.
	expectedAgentID := agentID
	if target > 0 {
		expectedAgentID = "target"
	}
	require.Equal(f.t, expectedAgentID, usage.AgentID)
	require.Equal(f.t, "source", usage.RouteSourceAgentID)
	require.Equal(f.t, uint(77), usage.AgentRouteID)
	require.Equal(f.t, path, usage.AgentRoutePath)
}

func (f *routedRelayFixture) usageRowCount() int64 {
	var count int64
	if err := f.db.Model(&models.UsageLog{}).Count(&count).Error; err != nil {
		return -1
	}
	return count
}

func (f *routedRelayFixture) closeHub() {
	f.hubCloseOnce.Do(func() {
		ctx, cancel := agentRouteCleanupContext(f.t)
		defer cancel()
		require.NoError(f.t, f.hub.Close(ctx))
		requireAgentRouteActiveZero(f.t, &f.hubHTTPActive, "relay hub HTTP")
	})
}

func (f *routedRelayFixture) close() {
	f.closeOnce.Do(func() {
		if f.barrier != nil {
			f.barrier.release()
		}
		requireAgentRouteActiveZero(f.t, &f.httpActive, "relay source HTTP")
		f.sourceHTTP.Close()
		if f.sourceDirect != nil {
			ctx, cancel := agentRouteCleanupContext(f.t)
			require.NoError(f.t, f.sourceDirect.Close(ctx))
			cancel()
			requireAgentRouteDone(f.t, f.sourceDirect.Done(), "relay source direct forwarder")
		}
		if f.sourceTransport != nil {
			f.sourceTransport.CloseIdleConnections()
		}
		if f.targetTransport != nil {
			f.targetTransport.CloseIdleConnections()
		}
		shutdownAgentRouteServer(f.t, f.source, "relay source")
		shutdownAgentRouteServer(f.t, f.target, "relay target")
		for _, run := range f.managerRuns {
			run.cancel()
			select {
			case err := <-run.done:
				require.Error(f.t, err)
			case <-time.After(2 * time.Second):
				f.t.Error("relay manager Run did not stop")
			}
		}
		f.closeHub()
		f.hubServer.Close()
		requireAgentRouteActiveZero(f.t, &f.providerActive, "relay provider")
		f.sourceProvider.Close()
		f.targetProvider.Close()
	})
}

func TestAgentRelayRealSocketBoundAttemptAndNoReplayMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("relay target executes embedded provider once", func(t *testing.T) {
		f := newAgentRelaySocketFixture(t, relayProviderSuccess(nil))
		body := relayRequestBody(false)
		id := wire.StreamID{71}
		f.open(id, body)
		f.commit(id)
		f.sendRequest(id, body)
		status, response, terminal, result := f.readResponse(id)

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, response, `"content":"ok"`)
		require.Equal(t, wire.FrameEnd, terminal)
		require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
		require.True(t, result.ProviderDispatched)
		require.Equal(t, 1, result.Dispatches)
		f.requireProviderAndUsage(0, 1, 0, "", 0)
	})

	t.Run("pre COMMIT RESET executes no provider and writes no usage", func(t *testing.T) {
		f := newAgentRelaySocketFixture(t, relayProviderSuccess(nil))
		body := relayRequestBody(false)
		id := wire.StreamID{72}
		f.open(id, body)
		f.reset(id, false)

		require.Never(t, func() bool { return f.targetProviderCalls.Load() != 0 }, 50*time.Millisecond, 5*time.Millisecond)
		f.requireProviderAndUsage(0, 0, 0, "", 0)
	})

	t.Run("COMMIT then websocket drop never replays provider", func(t *testing.T) {
		f := newAgentRelaySocketFixture(t, relayProviderSuccess(nil))
		body := relayRequestBody(false)
		id := wire.StreamID{73}
		f.open(id, body)
		f.commit(id)
		require.NoError(t, f.peer.Close())
		f.waitSession()

		require.Zero(t, f.targetProviderCalls.Load())
		f.requireNoProviderOrUsage()
	})

	t.Run("SSE interruption preserves partial response without replay", func(t *testing.T) {
		f := newAgentRelaySocketFixture(t, relayProviderInterruptedSSE())
		body := relayRequestBody(true)
		id := wire.StreamID{74}
		f.open(id, body)
		f.commit(id)
		f.sendRequest(id, body)
		status, response, terminal, _ := f.readResponse(id)

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, response, "data:")
		require.Contains(t, []wire.Type{wire.FrameEnd, wire.FrameReset}, terminal)
		f.requireProviderAndUsage(0, 1, 0, "", 0)
	})

	t.Run("client cancel after COMMIT before provider dispatch never replays", func(t *testing.T) {
		f := newAgentRelaySocketFixture(t, relayProviderSuccess(nil))
		body := relayRequestBody(false)
		id := wire.StreamID{75}
		f.open(id, body)
		f.commit(id)
		f.cancel(id)

		require.Never(t, func() bool { return f.targetProviderCalls.Load() != 0 }, 50*time.Millisecond, 5*time.Millisecond)
		f.requireNoProviderOrUsage()
	})
}

type agentRelaySocketFixture struct {
	t                   *testing.T
	db                  *gorm.DB
	target              *agent.Server
	limits              wire.Limits
	session             *agenttunnel.Session
	wsClient            *websocket.Conn
	peer                *websocket.Conn
	wsServer            *httptest.Server
	provider            *httptest.Server
	runDone             chan struct{}
	runErr              error
	closeOnce           sync.Once
	targetProviderCalls atomic.Int32
	sourceProviderCalls atomic.Int32
	providerActive      atomic.Int32
}

func newAgentRelaySocketFixture(t *testing.T, providerHandler http.Handler) *agentRelaySocketFixture {
	t.Helper()
	f := &agentRelaySocketFixture{
		t: t, db: newAgentRouteUsageDB(t),
		limits: wire.Limits{
			MaxMetadataBytes: 64 << 10, MaxDataBytes: 64 << 10, InitialStreamWindow: 256 << 10,
			MaxQueuedSessionBytes: 1 << 20, MaxConcurrentStreams: 4,
		},
	}
	f.provider = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.providerActive.Add(1)
		defer f.providerActive.Add(-1)
		f.targetProviderCalls.Add(1)
		providerHandler.ServeHTTP(w, r)
	}))

	cfg := &config.AgentRuntimeConfig{
		Agent:   config.AgentConfig{CredentialsFile: filepath.Join(t.TempDir(), "relay-target.json")},
		Runtime: config.RuntimeConfig{RelayTimeout: 2}, Relay: config.RelayConfig{Timeout: 2},
	}
	var err error
	f.target, err = agent.NewEmbedded(cfg, zap.NewNop(), &enrollment.Credentials{AgentID: "target", Secret: "secret"})
	require.NoError(t, err)
	f.target.Store.SetAgent(&models.Agent{AgentID: "target", Status: consts.StatusEnabled})
	f.target.Store.SetToken(&models.Token{ID: 1, Key: "route-token", Status: consts.StatusEnabled, ExpiredAt: -1})
	f.target.Store.SetChannel(&models.Channel{
		ChannelCore: models.ChannelCore{
			ID: 7, Type: consts.ChannelTypeOpenAI, BaseURL: f.provider.URL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	})
	f.target.Store.RebuildModelIndex()
	f.target.Store.LoadSettings([]models.Setting{
		{Key: consts.SettingAgentRelayFallbackEnabled, Value: "1"},
		{Key: "retry_max_channels", Value: "1"},
		{Key: "max_retries_per_channel", Value: "0"},
		{Key: "breaker_enabled", Value: "0"},
	})
	settler := billing.NewSettler(&agentRouteDBProvider{db: f.db}, nil, zap.NewNop())
	_, err = events.SubscribeUsageCompleted(f.target.Bus, func(ctx context.Context, entry protocol.UsageLogEntry) error {
		settler.Settle(ctx, "target", []protocol.UsageLogEntry{entry})
		return nil
	})
	require.NoError(t, err)
	router := gin.New()
	f.target.MountRoutes(router)

	f.wsClient, f.peer, f.wsServer = agentRelayWebSocketPair(t)
	f.session = agenttunnel.NewSession(f.wsClient, 1, f.limits, agenttunnel.SessionOptions{
		TargetHandler: f.target.NewTunnelTargetHandler(router),
	})
	f.runDone = make(chan struct{})
	go func() {
		defer close(f.runDone)
		f.runErr = f.session.Run(t.Context())
	}()
	t.Cleanup(f.close)
	return f
}

func relayProviderSuccess(started chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if started != nil {
			close(started)
		}
		w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "chatcmpl-relay", "object": "chat.completion",
			"choices": []map[string]any{{"message": map[string]string{"content": "ok"}, "index": 0, "finish_reason": "stop"}},
			"usage":   map[string]int{"prompt_tokens": 2, "completion_tokens": 1, "total_tokens": 3},
		})
	})
}

func relayProviderInterruptedSSE() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(consts.HeaderContentType, "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-partial\",\"choices\":[{\"delta\":{\"content\":\"part\"},\"index\":0}]}\n\n")
		w.(http.Flusher).Flush()
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			return
		}
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	})
}

func relayRequestBody(stream bool) string {
	return fmt.Sprintf(`{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"stream":%t}`, stream)
}

func (f *agentRelaySocketFixture) open(id wire.StreamID, body string) {
	f.t.Helper()
	attempt := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModePassthrough,
		},
		RequestPath: "/v1/chat/completions",
	}
	f.write(wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameOpen, StreamID: id, Sequence: 1,
		Payload: f.metadata(wire.Open{
			Method: http.MethodPost, Path: attemptwire.EndpointPath, BodyLength: int64(len(body)),
			RequestID: "req-" + f.t.Name(), SourceAgentID: "source", TargetAgentID: "target",
			RouteID: 77, Hop: 1, ResponseWindow: f.limits.InitialStreamWindow, Attempt: &attempt,
			Header: map[string][]string{
				consts.HeaderAuthorization: {consts.BearerPrefix + "route-token"},
				consts.HeaderContentType:   {consts.ContentTypeJSON},
			},
		}),
	})
	require.Equal(f.t, wire.FrameReady, f.read().Type)
	require.Zero(f.t, f.targetProviderCalls.Load(), "READY must not execute provider")
	require.Zero(f.t, f.usageRowCount(), "READY must not persist usage")
}

func (f *agentRelaySocketFixture) commit(id wire.StreamID) {
	f.t.Helper()
	f.write(wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameCommit, StreamID: id, Sequence: 2})
	require.Equal(f.t, wire.FrameCommitted, f.read().Type)
}

func (f *agentRelaySocketFixture) sendRequest(id wire.StreamID, body string) {
	f.t.Helper()
	f.write(wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: id, Sequence: 3, Payload: []byte(body)})
	f.write(wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestEnd, StreamID: id, Sequence: 4})
}

func (f *agentRelaySocketFixture) reset(id wire.StreamID, committed bool) {
	f.t.Helper()
	f.write(wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameReset, StreamID: id, Sequence: 5,
		Payload: f.metadata(wire.Reset{Code: "client_cancel", Stage: "request", Committed: committed}),
	})
}

func (f *agentRelaySocketFixture) cancel(id wire.StreamID) {
	f.t.Helper()
	f.write(wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameCancel, StreamID: id, Sequence: 5,
	})
}

func (f *agentRelaySocketFixture) readResponse(id wire.StreamID) (int, string, wire.Type, attemptwire.AttemptProxyResult) {
	f.t.Helper()
	status := 0
	var body strings.Builder
	for {
		frame := f.read()
		require.Equal(f.t, id, frame.StreamID)
		switch frame.Type {
		case wire.FrameWindowUpdate:
		case wire.FrameHeaders:
			var headers wire.Headers
			require.NoError(f.t, wire.DecodeMetadata(frame.Payload, &headers, f.limits.MaxMetadataBytes))
			status = headers.StatusCode
		case wire.FrameResponseData:
			body.Write(frame.Payload)
		case wire.FrameEnd:
			var trailers wire.Trailers
			require.NoError(f.t, wire.DecodeMetadata(frame.Payload, &trailers, f.limits.MaxMetadataBytes))
			result, err := attemptwire.DecodeResult(http.Header(trailers.Header).Get(attemptwire.TrailerResult))
			require.NoError(f.t, err)
			return status, body.String(), frame.Type, result
		case wire.FrameReset:
			return status, body.String(), frame.Type, attemptwire.AttemptProxyResult{}
		default:
			f.t.Fatalf("unexpected relay response frame %d", frame.Type)
		}
	}
}

func (f *agentRelaySocketFixture) requireProviderAndUsage(source, target int32, rows int64, path string, routeID uint) {
	f.t.Helper()
	require.Equal(f.t, source, f.sourceProviderCalls.Load())
	require.Equal(f.t, target, f.targetProviderCalls.Load())
	require.Eventually(f.t, func() bool { return f.usageRowCount() == rows }, time.Second, 5*time.Millisecond)
	if rows == 0 {
		require.Never(f.t, func() bool { return f.usageRowCount() != 0 }, 50*time.Millisecond, 5*time.Millisecond)
		return
	}
	var usage models.UsageLog
	require.NoError(f.t, f.db.First(&usage).Error)
	require.Equal(f.t, "target", usage.AgentID)
	require.Equal(f.t, "source", usage.RouteSourceAgentID)
	require.Equal(f.t, routeID, usage.AgentRouteID)
	require.Equal(f.t, path, usage.AgentRoutePath)
}

func (f *agentRelaySocketFixture) requireNoProviderOrUsage() {
	f.t.Helper()
	require.Never(f.t, func() bool {
		return f.sourceProviderCalls.Load() != 0 || f.targetProviderCalls.Load() != 0 || f.usageRowCount() != 0
	}, 100*time.Millisecond, 5*time.Millisecond)
}

func (f *agentRelaySocketFixture) usageRowCount() int64 {
	var count int64
	if err := f.db.Model(&models.UsageLog{}).Count(&count).Error; err != nil {
		return -1
	}
	return count
}

func (f *agentRelaySocketFixture) waitSession() {
	f.t.Helper()
	select {
	case <-f.runDone:
		require.Error(f.t, f.runErr)
	case <-time.After(2 * time.Second):
		f.t.Fatal("relay target session did not stop")
	}
}

func (f *agentRelaySocketFixture) close() {
	f.closeOnce.Do(func() {
		f.session.Cancel(context.Canceled)
		_ = f.peer.Close()
		f.waitSession()
		_ = f.wsClient.Close()

		shutdownAgentRouteServer(f.t, f.target, "manual relay target")
		requireAgentRouteActiveZero(f.t, &f.providerActive, "manual relay provider")
		f.provider.Close()
		f.wsServer.Close()
	})
}

func (f *agentRelaySocketFixture) write(frame wire.Frame) {
	f.t.Helper()
	raw, err := wire.Encode(frame, f.limits)
	require.NoError(f.t, err)
	require.NoError(f.t, f.peer.WriteMessage(websocket.BinaryMessage, raw))
}

func (f *agentRelaySocketFixture) read() wire.Frame {
	f.t.Helper()
	require.NoError(f.t, f.peer.SetReadDeadline(time.Now().Add(2*time.Second)))
	messageType, raw, err := f.peer.ReadMessage()
	require.NoError(f.t, err)
	require.Equal(f.t, websocket.BinaryMessage, messageType)
	frame, err := wire.Decode(raw, f.limits)
	require.NoError(f.t, err)
	return frame
}

func (f *agentRelaySocketFixture) metadata(value any) []byte {
	f.t.Helper()
	payload, err := wire.EncodeMetadata(value, f.limits.MaxMetadataBytes)
	require.NoError(f.t, err)
	return payload
}

func agentRelayWebSocketPair(t *testing.T) (*websocket.Conn, *websocket.Conn, *httptest.Server) {
	t.Helper()
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan *websocket.Conn, 1)
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(handlerDone)
		conn, err := upgrader.Upgrade(w, r, nil)
		if err == nil {
			accepted <- conn
		}
	}))
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	var peer *websocket.Conn
	select {
	case peer = <-accepted:
	case <-time.After(2 * time.Second):
		server.Close()
		t.Fatal("relay websocket server did not accept client")
	}
	requireAgentRouteDone(t, handlerDone, "relay websocket HTTP handler")
	return client, peer, server
}

func requireAgentRouteServerDone(t *testing.T, srv *agent.Server) {
	t.Helper()
	requireAgentRouteDone(t, srv.Done(), "agent server")
	require.Equal(t, (app.ResourceCounts{}), srv.ResourceCountsForTest())
}

func shutdownAgentRouteServer(t *testing.T, srv *agent.Server, name string) {
	t.Helper()
	ctx, cancel := agentRouteCleanupContext(t)
	require.NoError(t, srv.Shutdown(ctx), name)
	cancel()
	requireAgentRouteServerDone(t, srv)
}

func closeAgentRouteManager(t *testing.T, manager *agenttunnel.Manager, name string) {
	t.Helper()
	ctx, cancel := agentRouteCleanupContext(t)
	require.NoError(t, manager.Close(ctx), name)
	cancel()
	requireAgentRouteDone(t, manager.Done(), name)
}

func agentRouteCleanupContext(t *testing.T) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.WithoutCancel(t.Context()), 2*time.Second)
}

func requireAgentRouteDone(t *testing.T, done <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("%s did not close Done", name)
	}
}

func trackAgentRouteActive(active *atomic.Int32, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		next.ServeHTTP(w, r)
	})
}

func requireAgentRouteActiveZero(t *testing.T, active *atomic.Int32, name string) {
	t.Helper()
	require.Eventually(t, func() bool { return active.Load() == 0 }, 2*time.Second, 5*time.Millisecond, name)
}
