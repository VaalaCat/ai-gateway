package attemptproxy

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

func TestResultFromProviderClassifiesSuccessFailureAndCancellation(t *testing.T) {
	tests := []struct {
		name     string
		provider attemptexec.ProviderResult
		wantKind attemptwire.ResultKind
		wantPlan bool
	}{
		{name: "success", provider: attemptexec.ProviderResult{}, wantKind: attemptwire.ResultSucceeded},
		{
			name: "retryable provider failure",
			provider: attemptexec.ProviderResult{
				Outcome:    state.AttemptResult{Err: &common.UpstreamError{Status: http.StatusBadGateway}},
				Dispatches: 2, ProviderDispatched: true,
			},
			wantKind: attemptwire.ResultProviderFailed, wantPlan: true,
		},
		{
			name:     "canceled",
			provider: attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: context.Canceled}},
			wantKind: attemptwire.ResultCanceled,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rctx, _ := newResponseRelayContext(trace.CaptureOff)
			writer := newAttemptResponseWriter(rctx.Writer)
			got := resultFromProvider(rctx, test.provider, writer)
			require.Equal(t, test.wantKind, got.Kind)
			require.Equal(t, test.wantPlan, got.PlanAdvanceAllowed)
			require.Equal(t, test.provider.Dispatches, got.Dispatches)
			require.Equal(t, test.provider.ProviderDispatched || test.provider.Dispatches > 0, got.ProviderDispatched)
			require.True(t, got.ProviderResultKnown)
		})
	}
}

func TestAttemptResponseWriterKeepsProviderResponseSeparateFromExplicitResult(t *testing.T) {
	rctx, recorder := newResponseRelayContext(trace.CaptureOff)
	writer := newAttemptResponseWriter(rctx.Writer)
	writer.Header().Set("X-Provider", "kept")
	writer.WriteHeader(http.StatusCreated)
	_, err := writer.WriteString(`{"ok":true}`)
	require.NoError(t, err)
	result := writer.FinishResponse(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "kept", recorder.Header().Get("X-Provider"))
	require.Equal(t, attemptwire.ModeResponse, recorder.Header().Get(attemptwire.HeaderMode))
	require.Empty(t, recorder.Header().Values("Trailer"))
	require.Equal(t, `{"ok":true}`, recorder.Body.String())
	require.True(t, result.ResponseStarted)
	require.False(t, result.PlanAdvanceAllowed)
}

func TestResponseExecutorResultWriterFailureDoesNotWriteSecondBody(t *testing.T) {
	results := &capturingAttemptResultWriter{err: errors.New("result frame failed")}
	rctx, recorder := newResponseRelayContext(trace.CaptureOff)
	rctx.Request = rctx.Request.WithContext(attemptwire.WithAttemptResultWriter(rctx.Request.Context(), results))
	provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		_, err := rctx.Writer.WriteString("provider-body")
		require.NoError(t, err)
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}}
	})

	require.NotPanics(t, func() { NewResponseExecutor().Execute(rctx, state.Attempt{}, provider) })
	require.Equal(t, "provider-body", recorder.Body.String())
	require.Equal(t, attemptwire.ModeResponse, recorder.Header().Get(attemptwire.HeaderMode))
	require.Empty(t, recorder.Header().Values("Trailer"))
}

func TestFinalizeAttemptTraceCaptureModes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		mode     trace.CaptureMode
		fail     bool
		wantWire bool
		wantBody bool
	}{
		{name: "off success", mode: trace.CaptureOff},
		{name: "headers success", mode: trace.CaptureHeaders, wantWire: true},
		{name: "headers failure", mode: trace.CaptureHeaders, fail: true, wantWire: true, wantBody: true},
		{name: "full success", mode: trace.CaptureFull, wantWire: true, wantBody: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rctx, _ := newResponseRelayContext(tc.mode)
			rec := rctx.State.Recorder
			inbound := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			inbound.Header.Set("X-Inbound", "kept")
			rec.WithInbound(inbound, []byte(`{"model":"public"}`))
			outbound := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
			outbound.Header.Set("X-Outbound", "kept")
			rec.WithOutbound(outbound, []byte(`{"model":"upstream"}`), &models.Channel{})
			rec.WithUpstreamStatus(&http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Upstream": {"kept"}}})
			rec.SetUpstreamBody([]byte(`{"response":"upstream"}`))
			rec.WithPassthrough()
			if tc.fail {
				rec.WithFail(trace.StageUpstreamStatus, errors.New("upstream failed"))
			}

			wire := finalizeAttemptTrace(rctx)

			if !tc.wantWire {
				require.Nil(t, wire)
				return
			}
			require.NotNil(t, wire)
			require.Equal(t, "/v1/chat/completions", wire.InboundPath)
			var inboundHeaders http.Header
			require.NoError(t, json.Unmarshal([]byte(wire.InboundHeaders), &inboundHeaders))
			require.Equal(t, "kept", inboundHeaders.Get("X-Inbound"))
			if tc.wantBody {
				require.NotEmpty(t, wire.InboundBody)
				require.NotEmpty(t, wire.OutboundBody)
				require.NotEmpty(t, wire.ResponseBody)
				require.NotEmpty(t, wire.ClientResponseBody)
				return
			}
			require.Nil(t, wire.FailureFallback, "plain trace finalization must not build a remote fallback")
			require.Empty(t, wire.InboundBody)
			require.Empty(t, wire.OutboundBody)
			require.Empty(t, wire.ResponseBody)
			require.Empty(t, wire.ClientResponseBody)
		})
	}
}

func TestResultFromProviderHeaderSuccessBuildsRemoteFailureFallback(t *testing.T) {
	rctx, _ := newResponseRelayContext(trace.CaptureHeaders)
	rec := rctx.State.Recorder
	inbound := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	rec.WithInbound(inbound, []byte(`{"model":"public"}`))
	outbound := httptest.NewRequest(http.MethodPost, "https://upstream.example/v1/chat/completions", nil)
	rec.WithOutbound(outbound, []byte(`{"key":"provider-secret"}`), &models.Channel{Key: "provider-secret"})
	rec.WithUpstreamStatus(&http.Response{StatusCode: http.StatusOK})
	rec.SetUpstreamBody([]byte(`{"provider":"raw"}`))
	rec.WithPassthrough()

	result := resultFromProvider(rctx, attemptexec.ProviderResult{}, nil)

	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.NotNil(t, result.Trace)
	require.Empty(t, result.Trace.InboundBody)
	require.Empty(t, result.Trace.OutboundBody)
	require.Empty(t, result.Trace.ResponseBody)
	require.Empty(t, result.Trace.ClientResponseBody)
	require.Equal(t, &attemptwire.AttemptTraceBodyWire{
		InboundBody: `{"model":"public"}`, OutboundBody: `{"key":"***************"}`,
		ResponseBody: `{"provider":"raw"}`, ClientResponseBody: `{"provider":"raw"}`,
	}, result.Trace.FailureFallback)
	raw, err := attemptwire.EncodeResultJSON(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "provider-secret")
}

func newResponseRelayContext(mode trace.CaptureMode) (*state.RelayContext, *httptest.ResponseRecorder) {
	c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public"}`))
	c.Request = c.Request.WithContext(agentproxy.WithIngressMeta(c.Request.Context(), agentproxy.IngressMeta{Kind: agentproxy.IngressKindDirectTunnel}))
	return &state.RelayContext{
		Context: c,
		Input:   state.RelayInput{Model: "real"},
		State:   &state.RelayState{Recorder: trace.NewRecorder(mode, 256*1024)},
	}, recorder
}

type providerFunc func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult

func (f providerFunc) Execute(rctx *state.RelayContext, attempt state.Attempt) attemptexec.ProviderResult {
	return f(rctx, attempt)
}
