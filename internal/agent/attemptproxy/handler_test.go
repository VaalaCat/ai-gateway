package attemptproxy

import (
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/codec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

func TestHandlerRunsOnlyBoundAttemptPipeline(t *testing.T) {
	var calls []string
	meta := validAttemptMeta("/v1/chat/completions")
	c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public"}`))
	c.Request = c.Request.WithContext(attemptwire.WithMeta(c.Request.Context(), meta))
	user := &app.UserInfo{UserID: 3, TokenID: 4}
	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			UserInfo: user, Model: meta.Attempt.RealModel, InboundProto: codec.ProtocolOpenAIChat,
			Body: []byte(`{"model":"public"}`),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	builder := &handlerBuilder{build: func(got *gin.Context, gotMeta attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error) {
		calls = append(calls, "build")
		require.Same(t, c, got)
		require.Equal(t, meta, gotMeta)
		got.Request.URL.Path = gotMeta.RequestPath
		return rctx, func() { calls = append(calls, "release") }, nil
	}}
	wantAttempt := state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 11}}, RealModel: meta.Attempt.RealModel, Mode: state.ModeNative, Source: state.SourceAdmin, SourceID: 11}
	finder := &handlerFinder{find: func(input BoundChannelInput) (state.Attempt, error) {
		calls = append(calls, "find")
		require.Same(t, user, input.User)
		require.Equal(t, meta.Attempt, input.Attempt)
		require.Equal(t, codec.ProtocolOpenAIChat, input.InboundProtocol)
		return wantAttempt, nil
	}}
	gate := &handlerAttemptGate{onAttempt: func(got *state.RelayContext, attempt state.Attempt) {
		calls = append(calls, "attempt_limiter")
		require.Same(t, rctx, got)
		require.Equal(t, wantAttempt, attempt)
	}}
	dispatcher := &handlerDispatcher{dispatch: func(got *state.RelayContext, attempt state.Attempt) state.AttemptResult {
		calls = append(calls, "dispatcher")
		require.Equal(t, meta.RequestPath, got.Request.URL.Path)
		require.Equal(t, wantAttempt, attempt)
		_, err := got.Writer.WriteString(`{"ok":true}`)
		require.NoError(t, err)
		return state.AttemptResult{Written: true, UpstreamModel: "provider-real-model"}
	}}
	provider := attemptexec.NewProviderExecutor(dispatcher, nil, gate)
	handler := NewHandler(builder, finder, provider, NewResponseExecutor())

	handler.Serve(c)

	require.Equal(t, []string{"build", "find", "attempt_limiter", "dispatcher", "release"}, calls)
	require.Equal(t, int32(0), gate.requestCalls.Load(), "request limiter must not run")
	require.Equal(t, int32(1), gate.attemptCalls.Load())
	require.Equal(t, int32(1), dispatcher.calls.Load())
	require.Equal(t, `{"ok":true}`, recorder.Body.String())
	require.Equal(t, attemptwire.ModeResponse, recorder.Header().Get(attemptwire.HeaderMode))
}

func TestHandlerPassesOriginalProviderPathForLegacyAndAudio(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		mode        attemptwire.ExecutionMode
		contentType string
		body        []byte
	}{
		{name: "legacy", path: "/v1/completions", mode: attemptwire.ModeLegacy, body: []byte(`{"model":"public"}`)},
		{name: "response action", path: "/v1/responses/resp_123", mode: attemptwire.ModeNative, body: []byte(`{"model":"public"}`)},
		{name: "audio", path: "/v1/audio/transcriptions", mode: attemptwire.ModeNative, contentType: "multipart/form-data; boundary=handler-audio", body: multipartContextBody(t, "handler-audio", "public")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := validAttemptMeta(tt.path)
			meta.Attempt.Mode = tt.mode
			c, _ := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, tt.body)
			if tt.contentType != "" {
				c.Request.Header.Set("Content-Type", tt.contentType)
			}
			c.Set(consts.CtxKeyUserInfo, &app.UserInfo{UserID: 1, TokenID: 2})
			c.Request = c.Request.WithContext(attemptwire.WithMeta(c.Request.Context(), meta))
			finder := &handlerFinder{find: func(input BoundChannelInput) (state.Attempt, error) {
				return state.Attempt{Channel: &models.Channel{ChannelCore: models.ChannelCore{ID: 11}}, RealModel: meta.Attempt.RealModel, Mode: meta.Attempt.Mode}, nil
			}}
			provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				require.Equal(t, tt.path, rctx.Request.URL.Path)
				require.Equal(t, codec.PathToProtocol(tt.path), rctx.Input.InboundProto)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: state.ErrRateLimited}}
			})
			handler := NewHandler(NewContextBuilder(&contextAgent{bodyStore: &contextMemoryStore{}}), finder, provider, NewResponseExecutor())
			handler.Serve(c)
		})
	}
}

func TestHandlerPreProviderRejectionsAreStableProxyRejectedJSON(t *testing.T) {
	meta := validAttemptMeta("/v1/chat/completions")
	tests := []struct {
		name       string
		withMeta   bool
		meta       attemptwire.AttemptProxyMeta
		buildErr   error
		channelErr error
		wantStatus int
		wantReason string
	}{
		{name: "missing meta", wantStatus: http.StatusBadRequest, wantReason: "attempt_meta_missing"},
		{name: "invalid meta", withMeta: true, meta: validAttemptMeta(""), wantStatus: http.StatusBadRequest, wantReason: "attempt_meta_invalid"},
		{name: "disallowed provider path", withMeta: true, meta: validAttemptMeta("/internal/agent/attempt"), wantStatus: http.StatusBadRequest, wantReason: "attempt_path_invalid"},
		{name: "context build error", withMeta: true, meta: meta, buildErr: errors.New("body capture failed"), wantStatus: http.StatusInternalServerError, wantReason: "context_build_failed"},
		{name: "channel forbidden", withMeta: true, meta: meta, channelErr: &BoundChannelError{Code: boundChannelForbidden}, wantStatus: http.StatusForbidden, wantReason: boundChannelForbidden},
		{name: "channel missing", withMeta: true, meta: meta, channelErr: &BoundChannelError{Code: boundChannelNotFound}, wantStatus: http.StatusNotFound, wantReason: boundChannelNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public"}`))
			if tt.withMeta {
				c.Request = c.Request.WithContext(attemptwire.WithMeta(c.Request.Context(), tt.meta))
			}
			builder := &handlerBuilder{build: func(c *gin.Context, _ attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error) {
				if tt.buildErr != nil {
					return nil, nil, tt.buildErr
				}
				return &state.RelayContext{Context: c, State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)}}, func() {}, nil
			}}
			finder := &handlerFinder{find: func(BoundChannelInput) (state.Attempt, error) {
				if tt.channelErr != nil {
					return state.Attempt{}, tt.channelErr
				}
				return state.Attempt{}, nil
			}}
			var providerCalls atomic.Int32
			provider := providerFunc(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
				providerCalls.Add(1)
				return attemptexec.ProviderResult{}
			})

			NewHandler(builder, finder, provider, NewResponseExecutor()).Serve(c)

			require.Equal(t, tt.wantStatus, recorder.Code)
			require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			require.Empty(t, recorder.Header().Get(attemptwire.HeaderMode))
			var result attemptwire.AttemptProxyResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Equal(t, attemptwire.ResultProxyRejected, result.Kind)
			require.Equal(t, tt.wantReason, result.ReasonCode)
			require.False(t, result.ProviderResultKnown)
			require.Equal(t, int32(0), providerCalls.Load())
		})
	}
}

func TestHandlerNilAndTypedNilDependenciesFailClosed(t *testing.T) {
	meta := validAttemptMeta("/v1/chat/completions")
	var typedNilDispatcher *backend.Dispatcher
	validBuilder := &handlerBuilder{build: func(c *gin.Context, _ attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error) {
		return &state.RelayContext{Context: c, State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)}}, func() {}, nil
	}}
	validFinder := &handlerFinder{find: func(BoundChannelInput) (state.Attempt, error) { return state.Attempt{}, nil }}
	validProvider := providerFunc(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
		return attemptexec.ProviderResult{}
	})
	tests := []struct {
		name string
		h    *Handler
	}{
		{name: "nil handler"},
		{name: "nil contexts", h: NewHandler(nil, validFinder, validProvider, NewResponseExecutor())},
		{name: "typed nil contexts", h: NewHandler((*handlerBuilder)(nil), validFinder, validProvider, NewResponseExecutor())},
		{name: "nil channels", h: NewHandler(validBuilder, nil, validProvider, NewResponseExecutor())},
		{name: "typed nil channels", h: NewHandler(validBuilder, (*handlerFinder)(nil), validProvider, NewResponseExecutor())},
		{name: "nil provider", h: NewHandler(validBuilder, validFinder, nil, NewResponseExecutor())},
		{name: "typed nil provider", h: NewHandler(validBuilder, validFinder, (*handlerProvider)(nil), NewResponseExecutor())},
		{name: "provider executor without dispatcher", h: NewHandler(validBuilder, validFinder, attemptexec.NewProviderExecutor(nil, nil, nil), NewResponseExecutor())},
		{name: "provider executor with typed nil dispatcher", h: NewHandler(validBuilder, validFinder, attemptexec.NewProviderExecutor(typedNilDispatcher, nil, nil), NewResponseExecutor())},
		{name: "nil responses", h: NewHandler(validBuilder, validFinder, validProvider, nil)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public"}`))
			c.Request = c.Request.WithContext(attemptwire.WithMeta(c.Request.Context(), meta))
			require.NotPanics(t, func() { tt.h.Serve(c) })
			require.Equal(t, http.StatusInternalServerError, recorder.Code)
			var result attemptwire.AttemptProxyResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Equal(t, attemptwire.ResultProxyRejected, result.Kind)
			require.Equal(t, "proxy_dependencies_unavailable", result.ReasonCode)
		})
	}
}

type handlerBuilder struct {
	build func(*gin.Context, attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error)
}

func (b *handlerBuilder) Build(c *gin.Context, meta attemptwire.AttemptProxyMeta) (*state.RelayContext, func(), error) {
	return b.build(c, meta)
}

type handlerFinder struct {
	find func(BoundChannelInput) (state.Attempt, error)
}

func (f *handlerFinder) Find(input BoundChannelInput) (state.Attempt, error) { return f.find(input) }

type handlerProvider struct{}

func (*handlerProvider) Execute(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
	panic("typed nil provider must not execute")
}

type handlerAttemptGate struct {
	requestCalls atomic.Int32
	attemptCalls atomic.Int32
	onAttempt    func(*state.RelayContext, state.Attempt)
}

func (g *handlerAttemptGate) AcquireRequest(*state.RelayContext) (state.RateLease, error) {
	g.requestCalls.Add(1)
	return nil, errors.New("request limiter must not run")
}

func (g *handlerAttemptGate) AcquireAttempt(rctx *state.RelayContext, attempt state.Attempt) (state.RateLease, error) {
	g.attemptCalls.Add(1)
	if g.onAttempt != nil {
		g.onAttempt(rctx, attempt)
	}
	return nil, nil
}

type handlerDispatcher struct {
	calls    atomic.Int32
	dispatch func(*state.RelayContext, state.Attempt) state.AttemptResult
}

func (d *handlerDispatcher) Dispatch(rctx *state.RelayContext, attempt state.Attempt) state.AttemptResult {
	d.calls.Add(1)
	return d.dispatch(rctx, attempt)
}
