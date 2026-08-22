package native

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/script"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/config"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/pkg/llmkit"
)

type httpDoerScriptProvider struct {
	scripts []*script.Compiled
}

func (provider httpDoerScriptProvider) MatchScripts(script.MatchInput) []*script.Compiled {
	return provider.scripts
}

type httpDoerScriptCache struct {
	app.AgentCache
	engine *script.Engine
}

func (cache httpDoerScriptCache) ScriptEngine() *script.Engine {
	return cache.engine
}

type httpDoerTestAgent struct {
	app.AgentApplication
	cache app.AgentCache
	pool  app.TransportPool
}

func (agent httpDoerTestAgent) GetCache() app.AgentCache            { return agent.cache }
func (httpDoerTestAgent) GetBodyStore() app.BodyStore               { return nil }
func (httpDoerTestAgent) GetLogger() *zap.Logger                    { return zap.NewNop() }
func (httpDoerTestAgent) GetConfig() *config.AgentRuntimeConfig     { return nil }
func (agent httpDoerTestAgent) GetTransportPool() app.TransportPool { return agent.pool }
func (httpDoerTestAgent) RelayTimeout() time.Duration               { return 0 }

type httpDoerTransportPool struct {
	transport *http.Transport
	got       *models.Channel
}

func (pool *httpDoerTransportPool) Get(channel *models.Channel) *http.Transport {
	pool.got = channel
	return pool.transport
}

func (*httpDoerTransportPool) Invalidate(uint, string) {}
func (*httpDoerTransportPool) CloseIdleConnections()   {}

type httpDoerRoundTripFunc func(*http.Request) (*http.Response, error)

func (do httpDoerRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return do(request)
}

func httpDoerTestTransport(scheme string, do httpDoerRoundTripFunc) *http.Transport {
	transport := &http.Transport{}
	transport.RegisterProtocol(scheme, do)
	return transport
}

type httpDoerTrackingBody struct {
	*bytes.Reader
	closed bool
}

func (body *httpDoerTrackingBody) Close() error {
	body.closed = true
	return nil
}

func httpDoerAgentWithScript(t *testing.T, code string, pool app.TransportPool) app.AgentApplication {
	t.Helper()
	compiled, err := script.Compile(models.AdminScript{Name: "http-doer-test", Code: code})
	require.NoError(t, err)
	engine := script.NewEngine(httpDoerScriptProvider{scripts: []*script.Compiled{compiled}}, zap.NewNop(), time.Second)
	return httpDoerTestAgent{cache: httpDoerScriptCache{engine: engine}, pool: pool}
}

func httpDoerRequest(t *testing.T, rawURL, body string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(body))
	require.NoError(t, err)
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestAttemptHTTPDoerAppliesOverridesBeforeScript(t *testing.T) {
	var gotBody map[string]any
	var gotHeader string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.NoError(t, json.NewDecoder(request.Body).Decode(&gotBody))
		gotHeader = request.Header.Get("X-Override")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	channel := &models.Channel{
		ChannelCore: models.ChannelCore{
			ID:            7,
			ParamOverride: `{"temperature":0.5}`,
		},
		HeaderOverride: `{"X-Override":"channel"}`,
	}
	rctx, _ := newNativeTestCtx(t, nil, llmkit.ProtocolOpenAIChat, false)
	agent := httpDoerAgentWithScript(t, `function onUpstreamRequest(ctx) {
		ctx.body.script_saw_temperature = ctx.body.temperature;
		ctx.body.script_saw_header = ctx.headers["X-Override"];
		ctx.setHeader("X-Override", "script");
	}`, nil)
	attempt := state.Attempt{Channel: channel, RealModel: "provider-model", Source: state.SourceAdmin, SourceID: 7}
	doer := &attemptHTTPDoer{
		agent: agent, gin: rctx.Context, relay: rctx, attempt: attempt,
		protocol: llmkit.ProtocolOpenAIChat, recorder: rctx.State.Recorder,
	}

	response, err := doer.Do(httpDoerRequest(t, server.URL, `{"model":"provider-model","temperature":0}`))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, 0.5, gotBody["temperature"])
	require.Equal(t, 0.5, gotBody["script_saw_temperature"], "script must observe the overridden body")
	require.Equal(t, "channel", gotBody["script_saw_header"], "script must observe the overridden upstream header")
	require.Equal(t, "script", gotHeader, "script header operation must run after channel override")
}

func TestAttemptHTTPDoerKeepsFinalBodyAndGetBodyInSync(t *testing.T) {
	var actualBody, replayBody []byte
	transport := httpDoerTestTransport("replaytest", func(request *http.Request) (*http.Response, error) {
		var err error
		actualBody, err = io.ReadAll(request.Body)
		require.NoError(t, err)
		require.NotNil(t, request.GetBody)
		replay, err := request.GetBody()
		require.NoError(t, err)
		defer replay.Close()
		replayBody, err = io.ReadAll(replay)
		require.NoError(t, err)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       http.NoBody,
			Request:    request,
		}, nil
	})
	channel := &models.Channel{ChannelCore: models.ChannelCore{
		ID: 14, ParamOverride: `{"temperature":0.5}`,
	}}
	doer := &attemptHTTPDoer{
		agent:    httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
		attempt:  state.Attempt{Channel: channel},
		protocol: llmkit.ProtocolOpenAIChat,
	}

	response, err := doer.Do(httpDoerRequest(t, "replaytest://provider/v1", `{"model":"m","temperature":0}`))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.JSONEq(t, `{"model":"m","temperature":0.5}`, string(actualBody))
	require.Equal(t, actualBody, replayBody)
}

func TestAttemptHTTPDoerPreservesFinalBodyAcross307Redirect(t *testing.T) {
	redirectedBody := make(chan []byte, 1)
	redirectedHeader := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/start":
			writer.Header().Set("Location", "/final")
			writer.WriteHeader(http.StatusTemporaryRedirect)
		case "/final":
			body, err := io.ReadAll(request.Body)
			require.NoError(t, err)
			redirectedBody <- body
			redirectedHeader <- request.Header.Get("X-Custom-Auth")
			writer.WriteHeader(http.StatusOK)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	channel := &models.Channel{ChannelCore: models.ChannelCore{
		ID: 15, ParamOverride: `{"temperature":0.5}`,
	}}
	doer := &attemptHTTPDoer{attempt: state.Attempt{Channel: channel}, protocol: llmkit.ProtocolOpenAIChat}
	request := httpDoerRequest(t, server.URL+"/start", `{"model":"m","temperature":0}`)
	request.Header.Set("X-Custom-Auth", "same-origin-secret")

	response, err := doer.Do(request)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.JSONEq(t, `{"model":"m","temperature":0.5}`, string(<-redirectedBody))
	require.Equal(t, "same-origin-secret", <-redirectedHeader)
}

func TestAttemptHTTPDoerRejectsCrossOriginRedirectAndClosesBody(t *testing.T) {
	tests := []struct {
		name     string
		location string
	}{
		{name: "different host", location: "redirecttest://other.example/final"},
		{name: "different scheme", location: "otherredirect://provider.example/final"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := &httpDoerTrackingBody{Reader: bytes.NewReader([]byte("redirect body"))}
			var requests atomic.Int32
			transport := httpDoerTestTransport("redirecttest", func(request *http.Request) (*http.Response, error) {
				if requests.Add(1) > 1 {
					t.Fatalf("cross-origin second hop was dispatched with X-API-Key=%q X-Custom-Auth=%q",
						request.Header.Get("X-API-Key"), request.Header.Get("X-Custom-Auth"))
				}
				return &http.Response{
					StatusCode: http.StatusTemporaryRedirect,
					Header:     http.Header{"Location": []string{test.location}},
					Body:       body,
					Request:    request,
				}, nil
			})
			doer := &attemptHTTPDoer{
				agent:    httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
				attempt:  state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 16}}},
				protocol: llmkit.ProtocolOpenAIChat,
			}
			client := llmkit.NewClient(llmkit.ClientOptions{HTTPClient: doer})

			_, err := client.Call(t.Context(), llmkit.Request{}, llmkit.Target{
				Protocol:     llmkit.ProtocolOpenAIChat,
				BaseURL:      "redirecttest://provider.example",
				EndpointPath: "/start",
				Headers: map[string][]string{
					"X-API-Key":     {"claude-secret"},
					"X-Custom-Auth": {"custom-secret"},
				},
			}, llmkit.CallOptions{})

			var clientErr *llmkit.Error
			require.ErrorAs(t, err, &clientErr)
			require.Equal(t, llmkit.ErrorStageConnect, clientErr.Stage)
			require.True(t, clientErr.Retryable)
			require.EqualValues(t, 1, requests.Load())
			require.True(t, body.closed, "redirect error response body must be closed")
		})
	}
}

func TestAttemptHTTPDoerUsesChannelTransport(t *testing.T) {
	channel := &models.Channel{ChannelCore: models.ChannelCore{ID: 9}}
	called := false
	transport := httpDoerTestTransport("pooltest", func(request *http.Request) (*http.Response, error) {
		called = true
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    request,
		}, nil
	})
	pool := &httpDoerTransportPool{transport: transport}
	doer := &attemptHTTPDoer{
		agent: httpDoerTestAgent{pool: pool}, attempt: state.Attempt{Channel: channel},
		protocol: llmkit.ProtocolOpenAIChat,
	}

	response, err := doer.Do(httpDoerRequest(t, "pooltest://provider/v1", `{}`))
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.True(t, called)
	require.Same(t, channel, pool.got)
}

func TestAttemptHTTPDoerScriptRejectionReturnsTypedError(t *testing.T) {
	rctx, writer := newNativeTestCtx(t, nil, llmkit.ProtocolOpenAIChat, false)
	channel := &models.Channel{ChannelCore: models.ChannelCore{ID: 11}, Key: "channel-key"}
	agent := httpDoerAgentWithScript(t, `function onUpstreamRequest(ctx) { ctx.reject(451, "blocked") }`, nil)
	attempt := state.Attempt{Channel: channel, RealModel: "provider-model", Source: state.SourceAdmin, SourceID: 11}
	doer := &attemptHTTPDoer{
		agent: agent, gin: rctx.Context, relay: rctx, attempt: attempt,
		protocol: llmkit.ProtocolOpenAIChat, recorder: rctx.State.Recorder,
	}
	client := llmkit.NewClient(llmkit.ClientOptions{HTTPClient: doer})

	_, err := client.Call(context.Background(), llmkit.Request{
		Messages: []llmkit.Message{{
			Role:    llmkit.RoleUser,
			Content: []llmkit.ContentBlock{{Type: llmkit.ContentTypeText, Text: "hello"}},
		}},
	}, llmkit.Target{
		Protocol: llmkit.ProtocolOpenAIChat,
		BaseURL:  "http://provider.invalid",
		APIKey:   channel.Key,
		Model:    "provider-model",
	}, llmkit.CallOptions{})
	require.Error(t, err)
	var rejected *scriptRejectedError
	require.ErrorAs(t, err, &rejected)
	require.True(t, rejected.result.Written)
	require.Error(t, rejected.result.Err)
	require.Equal(t, http.StatusUnavailableForLegalReasons, writer.Code)
}

func TestAttemptHTTPDoerWrapsResponseForTrace(t *testing.T) {
	const upstreamBody = `{"answer":"captured while reading"}`
	body := &httpDoerTrackingBody{Reader: bytes.NewReader([]byte(upstreamBody))}
	transport := httpDoerTestTransport("tracetest", func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
			Request:    request,
		}, nil
	})
	channel := &models.Channel{ChannelCore: models.ChannelCore{ID: 12}, Key: "channel-key"}
	recorder := trace.NewRecorder(trace.CaptureFull, 1024)
	doer := &attemptHTTPDoer{
		agent:    httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
		attempt:  state.Attempt{Channel: channel},
		protocol: llmkit.ProtocolOpenAIChat,
		recorder: recorder,
	}

	response, err := doer.Do(httpDoerRequest(t, "tracetest://provider/v1", `{}`))
	require.NoError(t, err)
	require.Empty(t, recorder.UpstreamBodyBytes(), "trace must not eagerly consume a successful response")
	got, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, upstreamBody, string(got))
	require.Equal(t, upstreamBody, string(recorder.UpstreamBodyBytes()))
	require.False(t, body.closed, "caller owns a successful response body")
	require.NoError(t, response.Body.Close())
	require.True(t, body.closed, "trace wrapper must preserve the transport body's Close")
}

func TestAttemptHTTPDoerNonSuccessReturnsBoundedTypedErrorAndClosesBody(t *testing.T) {
	prefix := `{"error":{"type":"invalid_request_error"},"detail":"`
	rawBody := prefix + strings.Repeat("x", common.DefaultErrorBodyMaxRead+128)
	body := &httpDoerTrackingBody{Reader: bytes.NewReader([]byte(rawBody))}
	transport := httpDoerTestTransport("errortest", func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       body,
			Request:    request,
		}, nil
	})
	recorder := trace.NewRecorder(trace.CaptureOff, 1024)
	doer := &attemptHTTPDoer{
		agent: httpDoerTestAgent{pool: &httpDoerTransportPool{transport: transport}},
		attempt: state.Attempt{Channel: &models.Channel{
			ChannelCore: models.ChannelCore{ID: 13}, Key: "channel-key",
		}},
		protocol: llmkit.ProtocolOpenAIChat,
		recorder: recorder,
	}

	response, err := doer.Do(httpDoerRequest(t, "errortest://provider/v1", `{}`))
	require.Nil(t, response)
	require.Error(t, err)
	require.True(t, body.closed)
	var clientErr *llmkit.Error
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, llmkit.ErrorStageUpstream, clientErr.Stage)
	require.Equal(t, http.StatusBadRequest, clientErr.StatusCode)
	var upstreamErr *common.UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusBadRequest, upstreamErr.Status)
	require.Equal(t, "invalid_request_error", upstreamErr.ProviderErrorType)
	require.LessOrEqual(t, len(upstreamErr.Body), common.DefaultErrorBodyHeadLimit+len(common.TruncatedBodyMarker))
	require.Contains(t, string(upstreamErr.Body), common.TruncatedBodyMarker)
	require.NotEmpty(t, recorder.UpstreamBodyBytes())
	record := recorder.Finalize()
	require.Equal(t, trace.StageUpstreamStatus, record.FailStage)
	require.Equal(t, http.StatusBadRequest, record.UpstreamStatus)
}

func TestAttemptHTTPDoerNilDependenciesAndEmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(request.Body)
		require.NoError(t, err)
		require.Empty(t, body)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	doer := &attemptHTTPDoer{}
	response, err := doer.Do(httpDoerRequest(t, server.URL, ""))
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.NoError(t, response.Body.Close())
}
