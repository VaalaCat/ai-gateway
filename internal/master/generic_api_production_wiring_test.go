package master

import (
	"bytes"
	"context"
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

	"github.com/VaalaCat/ai-gateway/internal/agent/genericapi"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"gorm.io/datatypes"
)

const genericAPIProductionTokenKey = "sk-generic-api-production-wiring"

type genericAPIProductionCase struct {
	grantInvoke         bool
	userQuota           int64
	initialPrice        int64
	currentPrice        int64
	limiter             bool
	providerUnavailable bool
}

// Production break caught: transport failures must keep the client envelope
// opaque while persisting a useful, sanitized error for administrators.
func TestGenericAPIProductionWiringPersistsSafeTransportError(t *testing.T) {
	fixture := newGenericAPIProductionWiringFixture(t, genericAPIProductionCase{
		grantInvoke: true, userQuota: 50_000, initialPrice: 100, providerUnavailable: true,
	})
	response := fixture.invokeRaw(t, "generic-api-production-connection-refused")
	defer response.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)

	requestID := internalRequestID(t, response, "generic-api-production-connection-refused")
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.JSONEq(t, fmt.Sprintf(`{"error":{"code":"api_unavailable","request_id":%q}}`, requestID), string(body))

	var log models.APIRequestLog
	require.Eventually(t, func() bool {
		logDB := fixture.server.App.GetLogDB()
		return logDB != nil && logDB.Where("request_id = ?", requestID).Take(&log).Error == nil
	}, 8*time.Second, 20*time.Millisecond)
	require.Equal(t, "transport", log.ErrorStage)
	require.Equal(t, genericapi.CodeUnavailable, log.ErrorCode)
	require.Contains(t, log.ErrorMessage, "connection refused")
	require.NotContains(t, log.ErrorMessage, "fixture-secret")
	require.NotContains(t, log.ErrorMessage, "api_key")
}

type genericAPIProductionWiringFixture struct {
	server          *Server
	baseURL         string
	providerCalls   atomic.Int32
	providerEntered chan struct{}
	providerRelease chan struct{}
	providerOnce    sync.Once
	releaseOnce     sync.Once
	user            models.User
	token           models.Token
	service         models.APIService
	route           models.APIRoute
	currentPrice    int64
	limiterID       uint
}

// Production break caught: a successful Generic API request must traverse
// real auth/cache/gates and the production Reporter -> Master worker chain.
func TestGenericAPIProductionWiringRBACQuotaLimiterAndSettlement(t *testing.T) {
	tests := []struct {
		name             string
		requestID        string
		setup            genericAPIProductionCase
		wantStatus       int
		wantErrorCode    string
		wantProviderCall int32
	}{
		{
			name:       "permission gate rejects",
			requestID:  "generic-api-production-permission-rejected",
			setup:      genericAPIProductionCase{userQuota: 50_000, initialPrice: 100},
			wantStatus: http.StatusForbidden, wantErrorCode: genericapi.CodeAPIForbidden,
		},
		{
			name:       "quota gate rejects",
			requestID:  "generic-api-production-quota-rejected",
			setup:      genericAPIProductionCase{grantInvoke: true, userQuota: 0, initialPrice: 100},
			wantStatus: http.StatusPaymentRequired, wantErrorCode: genericapi.CodeInsufficientQuota,
		},
		{
			name:       "limiter gate rejects",
			requestID:  "generic-api-production-limiter-rejected",
			setup:      genericAPIProductionCase{grantInvoke: true, userQuota: 50_000, initialPrice: 100, limiter: true},
			wantStatus: http.StatusTooManyRequests, wantErrorCode: genericapi.CodeRateLimited,
		},
		{
			name:       "settles successful request at current price",
			requestID:  "generic-api-production-settled",
			setup:      genericAPIProductionCase{grantInvoke: true, userQuota: 50_000, initialPrice: 100, currentPrice: 12_345},
			wantStatus: http.StatusOK, wantProviderCall: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newGenericAPIProductionWiringFixture(t, test.setup)
			if test.setup.limiter {
				var rejected *http.Response
				holder := fixture.invokeAfterProviderDispatch(t, "generic-api-production-limiter-holder", func(t *testing.T) {
					fixture.providerCalls.Store(0)
					rejected = fixture.invoke(t, test.requestID)
					require.Equal(t, test.wantStatus, rejected.StatusCode)
					require.Equal(t, test.wantProviderCall, fixture.providerCalls.Load())
					fixture.requireRejected(t, internalRequestID(t, rejected, test.requestID), test.wantStatus, test.wantErrorCode, test.setup.userQuota)
				})
				require.Equal(t, http.StatusOK, holder.StatusCode)
				return
			}
			if test.wantStatus != http.StatusOK {
				response := fixture.invoke(t, test.requestID)
				require.Equal(t, test.wantStatus, response.StatusCode)
				require.Equal(t, test.wantProviderCall, fixture.providerCalls.Load())
				fixture.requireRejected(t, internalRequestID(t, response, test.requestID), test.wantStatus, test.wantErrorCode, test.setup.userQuota)
				return
			}

			response := fixture.invokeAfterProviderDispatch(t, test.requestID, fixture.applyCurrentPrice)
			require.Equal(t, test.wantStatus, response.StatusCode)
			require.Equal(t, test.wantProviderCall, fixture.providerCalls.Load())
			fixture.requireSettled(t, internalRequestID(t, response, test.requestID), test.setup.userQuota-test.setup.currentPrice, test.setup.currentPrice)
		})
	}
}

func internalRequestID(t *testing.T, response *http.Response, clientRequestID string) string {
	t.Helper()
	requestID := response.Header.Get(consts.HeaderXRequestID)
	require.NotEmpty(t, requestID)
	require.NotEqual(t, clientRequestID, requestID)
	return requestID
}

// Production break caught: the embedded Agent must publish Generic API
// observations through the registry served by its owning Master.
func TestMasterEmbeddedAgentGenericAPIMetricsUseServedRegistry(t *testing.T) {
	fixture := newGenericAPIProductionWiringFixture(t, genericAPIProductionCase{
		userQuota: 50_000, initialPrice: 100,
	})
	response := fixture.invoke(t, "generic-api-production-metrics")
	require.Equal(t, http.StatusForbidden, response.StatusCode)

	families, err := fixture.server.MetricsRegistry.Gather()
	require.NoError(t, err)
	var observed float64
	for _, family := range families {
		if family.GetName() != "generic_api_requests_total" {
			continue
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["protocol"] == genericapi.ProtocolHTTP && labels["outcome"] == "error" {
				observed += metric.GetCounter().GetValue()
			}
		}
	}
	require.Equal(t, float64(1), observed)
}

func newGenericAPIProductionWiringFixture(t *testing.T, testCase genericAPIProductionCase) *genericAPIProductionWiringFixture {
	t.Helper()
	dir := t.TempDir()
	fixture := &genericAPIProductionWiringFixture{
		providerEntered: make(chan struct{}),
		providerRelease: make(chan struct{}),
		currentPrice:    testCase.currentPrice,
	}
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		fixture.providerCalls.Add(1)
		_, _ = io.Copy(io.Discard, request.Body)
		fixture.providerOnce.Do(func() { close(fixture.providerEntered) })
		<-fixture.providerRelease
		response.WriteHeader(http.StatusOK)
	}))
	providerURL := provider.URL
	if testCase.providerUnavailable {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		providerURL = "http://" + listener.Addr().String() + "?api_key=fixture-secret"
		require.NoError(t, listener.Close())
	}

	cfg := &config.MasterRuntimeConfig{
		Master: config.MasterConfig{
			Listen:    ":0",
			DBPath:    filepath.Join(dir, "core.db"),
			LogDBPath: filepath.Join(dir, "log.db"),
			JWTSecret: strings.Repeat("x", 32),
		},
		Agent: config.AgentConfig{
			CredentialsFile:  filepath.Join(dir, "embedded-agent.json"),
			PreferredAddrTag: "local",
		},
		Runtime: config.RuntimeConfig{
			RelayTimeout:        30,
			FullSyncInterval:    3600,
			HeartbeatInterval:   3600,
			ReportBufferSize:    8,
			ReportFlushInterval: 1,
		},
	}
	server, err := New(cfg, zap.NewNop())
	require.NoError(t, err)
	fixture.server = server

	fixture.user = models.User{
		Username: "generic-api-production-user",
		Status:   consts.StatusEnabled,
		GroupID:  1,
		Quota:    testCase.userQuota,
	}
	require.NoError(t, server.DB.Create(&fixture.user).Error)
	fixture.token = models.Token{
		UserID:      fixture.user.ID,
		Key:         genericAPIProductionTokenKey,
		Name:        "generic-api-production-token",
		Status:      consts.StatusEnabled,
		ExpiredAt:   -1,
		APIRoleMode: models.APIRoleModeExplicit,
	}
	require.NoError(t, server.DB.Create(&fixture.token).Error)
	fixture.service = models.APIService{
		Slug:         "weather",
		Name:         "Weather",
		PricePerCall: testCase.initialPrice,
		Status:       consts.StatusEnabled,
	}
	require.NoError(t, server.DB.Create(&fixture.service).Error)
	backend := models.APIBackend{APIServiceID: fixture.service.ID, Name: "primary"}
	require.NoError(t, server.DB.Create(&backend).Error)
	fixture.route = models.APIRoute{
		APIServiceID:   fixture.service.ID,
		BackendID:      backend.ID,
		Slug:           "current",
		Protocols:      datatypes.JSONSlice[models.APIProtocol]{models.APIProtocolHTTP},
		AllowedMethods: datatypes.JSONSlice[string]{http.MethodPost},
		UpstreamPath:   "/provider",
		Status:         consts.StatusEnabled,
	}
	require.NoError(t, server.DB.Create(&fixture.route).Error)
	upstream := models.APIUpstream{
		BackendID: backend.ID,
		Name:      "primary",
		BaseURL:   providerURL,
		Weight:    1,
		AuthType:  models.APIUpstreamAuthNone,
		Status:    consts.StatusEnabled,
	}
	require.NoError(t, server.DB.Create(&upstream).Error)
	if testCase.grantInvoke {
		role := models.Role{Key: "generic_api_production_invoke", Name: "Generic API production invoke", Status: consts.StatusEnabled}
		require.NoError(t, server.DB.Create(&role).Error)
		permission := models.Permission{
			Resource:   models.APIResourceService,
			ResourceID: fixture.service.ID,
			Action:     models.APIPermissionInvoke,
		}
		require.NoError(t, server.DB.Create(&permission).Error)
		require.NoError(t, server.DB.Create(&models.RolePermission{RoleID: role.ID, PermissionID: permission.ID}).Error)
		require.NoError(t, server.DB.Create(&models.RoleBinding{
			PrincipalType: models.APIPrincipalToken,
			PrincipalID:   fixture.token.ID,
			RoleID:        role.ID,
		}).Error)
	}
	if testCase.limiter {
		limiter := models.RequestLimiter{
			Name: "generic-api-production-concurrency", Enabled: true,
			Metric: models.LimiterMetricConcurrency, Capacity: 1,
			KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
		}
		require.NoError(t, server.DB.Create(&limiter).Error)
		fixture.limiterID = limiter.ID
		require.NoError(t, server.DB.Create(&models.LimiterBinding{
			LimiterID: limiter.ID, TargetType: models.LimiterTargetAPIService,
			TargetID: fixture.service.ID, Enabled: true,
		}).Error)
	}

	masterHTTP := httptest.NewServer(server.Router)
	fixture.baseURL = masterHTTP.URL
	parsed, err := url.Parse(masterHTTP.URL)
	require.NoError(t, err)
	require.NoError(t, server.SetupEmbeddedAgentForTest(parsed.Host))
	waitForConnectedAgents(t, server, 1)
	require.Eventually(t, func() bool {
		store := server.GetEmbeddedAgentStore()
		if store == nil || store.APIIndex.RequireReady() != nil {
			return false
		}
		return fixture.limiterID == 0 || len(store.LimiterIndex.EffectiveSourceAPILimiters(
			fixture.user.ID, fixture.user.GroupID, fixture.service.ID, fixture.route.ID,
		)) == 1
	}, 5*time.Second, 10*time.Millisecond)

	t.Cleanup(func() {
		fixture.releaseProvider()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			t.Errorf("shutdown production wiring fixture: %v", err)
		}
		masterHTTP.CloseClientConnections()
		masterHTTP.Close()
		provider.Close()
	})
	return fixture
}

func (f *genericAPIProductionWiringFixture) invoke(t *testing.T, requestID string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, f.baseURL+"/v1/api/weather/current", bytes.NewBufferString("production-wiring-body"))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+genericAPIProductionTokenKey)
	request.Header.Set(consts.HeaderXRequestID, requestID)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response
}

func (f *genericAPIProductionWiringFixture) invokeRaw(t *testing.T, requestID string) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.baseURL+"/v1/api/weather/current", bytes.NewBufferString("production-wiring-body"))
	require.NoError(t, err)
	request.Header.Set("Authorization", "Bearer "+genericAPIProductionTokenKey)
	request.Header.Set(consts.HeaderXRequestID, requestID)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	return response
}

func (f *genericAPIProductionWiringFixture) invokeAfterProviderDispatch(t *testing.T, requestID string, afterDispatch func(*testing.T)) *http.Response {
	t.Helper()
	responseCh := make(chan *http.Response, 1)
	errorCh := make(chan error, 1)
	go func() {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.baseURL+"/v1/api/weather/current", bytes.NewBufferString("production-wiring-body"))
		if err != nil {
			errorCh <- err
			return
		}
		request.Header.Set("Authorization", "Bearer "+genericAPIProductionTokenKey)
		request.Header.Set(consts.HeaderXRequestID, requestID)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			errorCh <- err
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		responseCh <- response
	}()

	select {
	case <-f.providerEntered:
	case err := <-errorCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("provider was not dispatched")
	}
	afterDispatch(t)
	f.releaseProvider()
	select {
	case response := <-responseCh:
		return response
	case err := <-errorCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("gateway response timed out")
	}
	return nil
}

func (f *genericAPIProductionWiringFixture) applyCurrentPrice(t *testing.T) {
	t.Helper()
	require.NoError(t, f.server.DB.Model(&models.APIService{}).
		Where("id = ?", f.service.ID).
		Update("price_per_call", f.currentPrice).Error)
}

func (f *genericAPIProductionWiringFixture) requireSettled(t *testing.T, requestID string, wantQuota, wantPrice int64) {
	t.Helper()
	type settledLog struct {
		UnitPrice          int64
		TotalCost          int64
		ProviderDispatched bool
		StatusCode         int
	}
	require.Eventually(t, func() bool {
		var user models.User
		if err := f.server.DB.First(&user, f.user.ID).Error; err != nil || user.Quota != wantQuota {
			return false
		}
		var log settledLog
		if err := f.server.LogDB.Model(&models.APIRequestLog{}).
			Where("request_id = ?", requestID).
			Take(&log).Error; err != nil {
			return false
		}
		return log.UnitPrice == wantPrice && log.TotalCost == wantPrice && log.ProviderDispatched && log.StatusCode == http.StatusOK
	}, 8*time.Second, 20*time.Millisecond)
}

func (f *genericAPIProductionWiringFixture) requireRejected(t *testing.T, requestID string, wantStatus int, wantErrorCode string, wantQuota int64) {
	t.Helper()
	require.Eventually(t, func() bool {
		var user models.User
		if err := f.server.DB.First(&user, f.user.ID).Error; err != nil || user.Quota != wantQuota {
			return false
		}
		var log models.APIRequestLog
		if err := f.server.LogDB.Where("request_id = ?", requestID).Take(&log).Error; err != nil {
			return false
		}
		return log.StatusCode == wantStatus && log.ErrorCode == wantErrorCode &&
			!log.ProviderDispatched && log.TotalCost == 0
	}, 8*time.Second, 20*time.Millisecond)
}

func (f *genericAPIProductionWiringFixture) releaseProvider() {
	f.releaseOnce.Do(func() { close(f.providerRelease) })
}
