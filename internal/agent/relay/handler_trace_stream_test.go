package relay

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type traceStreamLimiterResolver struct {
	request []*models.RequestLimiter
}

func (r traceStreamLimiterResolver) EffectiveRequestLimiters(uint, uint) []*models.RequestLimiter {
	return r.request
}

func (traceStreamLimiterResolver) EffectiveAttemptLimiters(uint, uint, string, uint) []*models.RequestLimiter {
	return nil
}

func TestRelayStreamWaitProviderFailureCapturesFinalSSEError(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{
		{name: "provider 4xx", status: http.StatusTooManyRequests},
		{name: "provider 5xx", status: http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			providerCalls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				providerCalls++
				w.Header().Set(consts.HeaderContentType, consts.ContentTypeJSON)
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`{"error":{"message":"provider failed","type":"server_error"}}`))
			}))
			defer upstream.Close()

			handler, store, bus := setupTestHandler([]*models.Channel{{
				ChannelCore: models.ChannelCore{
					ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL,
					Status: consts.StatusEnabled, Weight: 1,
				},
				Key: "provider-key", Models: "gpt-4o",
			}})
			store.LoadSettings([]models.Setting{
				{Key: "retry_max_channels", Value: "1"},
				{Key: "max_retries_per_channel", Value: "0"},
			})
			logs := collectUsageLogs(bus)
			rule := &models.RequestLimiter{
				ID: 1, Metric: models.LimiterMetricConcurrency, Capacity: 1,
				KeyBy: "shared", Action: models.LimiterActionWait,
				QueueSize: 1, QueueTimeMs: 500, Enabled: true,
			}
			gate := limiter.NewGate(traceStreamLimiterResolver{request: []*models.RequestLimiter{rule}}, limiter.NewMemStore())
			held, err := gate.AcquireRequest(&state.RelayContext{
				Input: state.RelayInput{UserInfo: &app.UserInfo{UserID: 1}}, State: &state.RelayState{},
			})
			require.NoError(t, err)
			handler.executor.RequestGate = gate
			time.AfterFunc(50*time.Millisecond, held.Release)

			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.POST("/v1/chat/completions", func(c *gin.Context) {
				c.Set(consts.CtxKeyUserInfo, &app.UserInfo{
					UserID: 1, TokenID: 1, TraceEnabled: true, TraceMode: models.TokenTraceModeHeaders,
				})
				handler.Relay(c)
			})
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(
				context.Background(), http.MethodPost, "/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`),
			)
			request.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)

			router.ServeHTTP(response, request)

			require.Equal(t, http.StatusOK, response.Code)
			require.Equal(t, 1, providerCalls)
			body := response.Body.String()
			require.Contains(t, body, ": keepalive\n\n")
			require.Equal(t, 1, strings.Count(body, `data: {"error"`))
			require.Equal(t, 1, strings.Count(body, "data: [DONE]"))
			errorOffset := strings.Index(body, `data: {"error"`)
			require.GreaterOrEqual(t, errorOffset, 0)
			actualErrorSSE := body[errorOffset:]

			require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, 5*time.Millisecond)
			entry := logs.Get(0)
			require.Len(t, entry.AttemptTraces, 1)
			attemptTrace := entry.AttemptTraces[0]
			require.Equal(t, string(trace.StageUpstreamStatus), attemptTrace.ErrorStage)
			require.Equal(t, actualErrorSSE, attemptTrace.ClientResponseBody)
			require.NotEmpty(t, attemptTrace.InboundBody, "failed headers attempt must remain full")
			require.NotEmpty(t, attemptTrace.OutboundBody, "failed headers attempt must remain full")
			require.NotEmpty(t, attemptTrace.ResponseBody, "failed headers attempt must remain full")
		})
	}
}

func TestRelayTraceStreamKeepsTailPastCaptureHardLimit(t *testing.T) {
	const traceLimit = 4096
	const finalEvent = "data: [DONE]\n\n"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(consts.HeaderContentType, consts.ContentTypeSSE)
		_, _ = w.Write([]byte(":" + strings.Repeat("x", traceLimit*consts.TraceBufferHardLimitMultiple+1024) + "\n\n"))
		_, _ = w.Write([]byte(finalEvent))
	}))
	defer upstream.Close()

	handler, store, bus := setupTestHandler([]*models.Channel{{
		ChannelCore: models.ChannelCore{
			ID: 1, Type: consts.ChannelTypeOpenAI, BaseURL: upstream.URL,
			Status: consts.StatusEnabled, Weight: 1, PassthroughEnabled: true,
		},
		Key: "provider-key", Models: "gpt-4o",
	}})
	store.LoadSettings([]models.Setting{{Key: "trace_max_body_size", Value: "4096"}})
	logs := collectUsageLogs(bus)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		c.Set(consts.CtxKeyUserInfo, &app.UserInfo{
			UserID: 1, TokenID: 1, TraceEnabled: true, TraceMode: models.TokenTraceModeFull,
		})
		handler.Relay(c)
	})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(
		`{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hi"}]}`,
	))
	request.Header.Set(consts.HeaderContentType, consts.ContentTypeJSON)
	router.ServeHTTP(response, request)

	require.Equal(t, http.StatusOK, response.Code)
	require.Eventually(t, func() bool { return logs.Len() == 1 }, time.Second, 5*time.Millisecond)
	entry := logs.Get(0)
	require.Len(t, entry.AttemptTraces, 1)
	got := entry.AttemptTraces[0]
	for name, body := range map[string]string{"upstream": got.ResponseBody, "client": got.ClientResponseBody} {
		require.True(t, strings.HasPrefix(body, "...(truncated)"), name)
		require.True(t, strings.HasSuffix(body, finalEvent), name)
		require.LessOrEqual(t, len(strings.TrimPrefix(body, "...(truncated)")), traceLimit, name)
	}
}
