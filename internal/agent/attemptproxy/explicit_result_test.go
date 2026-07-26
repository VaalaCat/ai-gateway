package attemptproxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/stretchr/testify/require"
)

func TestResponseExecutorWritesExplicitResultWithoutCarrierMetadata(t *testing.T) {
	tests := []struct {
		name     string
		provider providerFunc
		wantMode string
		wantKind attemptwire.ResultKind
		wantBody string
		wantCode int
	}{
		{
			name: "provider response", wantMode: attemptwire.ModeResponse, wantKind: attemptwire.ResultSucceeded,
			provider: func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				_, err := rctx.Writer.WriteString(`{"ok":true}`)
				require.NoError(t, err)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}}
			},
			wantBody: `{"ok":true}`,
			wantCode: http.StatusOK,
		},
		{
			name: "zero body", wantMode: attemptwire.ModeResponse, wantKind: attemptwire.ResultSucceeded,
			provider: func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
				return attemptexec.ProviderResult{}
			},
			wantCode: http.StatusOK,
		},
		{
			name: "204", wantMode: attemptwire.ModeResponse, wantKind: attemptwire.ResultSucceeded,
			provider: func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				rctx.Writer.WriteHeader(http.StatusNoContent)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}}
			},
			wantCode: http.StatusNoContent,
		},
		{
			name: "304", wantMode: attemptwire.ModeResponse, wantKind: attemptwire.ResultSucceeded,
			provider: func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				rctx.Writer.WriteHeader(http.StatusNotModified)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}}
			},
			wantCode: http.StatusNotModified,
		},
		{
			name: "provider pre-response failure", wantMode: attemptwire.ModeControl, wantKind: attemptwire.ResultProviderFailed,
			provider: func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
				return attemptexec.ProviderResult{
					Outcome:            state.AttemptResult{Err: errors.New("provider failed")},
					ProviderDispatched: true,
				}
			},
			wantCode: http.StatusOK,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := &capturingAttemptResultWriter{}
			rctx, recorder := newResponseRelayContext(trace.CaptureOff)
			rctx.Request = rctx.Request.WithContext(attemptwire.WithAttemptResultWriter(rctx.Request.Context(), results))

			NewResponseExecutor().Execute(rctx, state.Attempt{}, test.provider)

			require.Equal(t, test.wantMode, recorder.Header().Get(attemptwire.HeaderMode))
			require.Equal(t, test.wantCode, recorder.Code)
			require.Equal(t, test.wantBody, recorder.Body.String())
			require.Empty(t, recorder.Header().Values("Trailer"))
			got := results.single(t)
			require.Equal(t, test.wantKind, got.Kind)
			require.True(t, got.ProviderResultKnown)
			require.Equal(t, test.wantMode == attemptwire.ModeResponse, got.ResponseStarted)
		})
	}
}

func TestResponseExecutorSSEFlushesBeforeExplicitResult(t *testing.T) {
	results := &capturingAttemptResultWriter{}
	rctx, recorder := newResponseRelayContext(trace.CaptureOff)
	rctx.Request = rctx.Request.WithContext(attemptwire.WithAttemptResultWriter(rctx.Request.Context(), results))
	provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("Content-Type", "text/event-stream")
		_, err := rctx.Writer.WriteString("data: first\n\n")
		require.NoError(t, err)
		rctx.Writer.Flush()
		require.True(t, recorder.Flushed, "SSE first chunk must flush before Result is written")
		require.Empty(t, results.results)
		_, err = rctx.Writer.WriteString("data: second\n\n")
		require.NoError(t, err)
		rctx.Writer.Flush()
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}}
	})

	NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

	require.True(t, recorder.Flushed)
	require.Equal(t, "data: first\n\ndata: second\n\n", recorder.Body.String())
	require.Equal(t, attemptwire.ResultSucceeded, results.single(t).Kind)
}

func TestHandlerProxyRejectionWritesControlHasNoBodyAndExplicitResult(t *testing.T) {
	results := &capturingAttemptResultWriter{}
	c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, nil)
	ctx := attemptwire.WithAttemptResultWriter(c.Request.Context(), results)
	ctx = agentproxy.WithIngressMeta(ctx, agentproxy.IngressMeta{Kind: agentproxy.IngressKindRelayTunnel})
	c.Request = c.Request.WithContext(ctx)

	(*Handler)(nil).Serve(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, attemptwire.ModeControl, recorder.Header().Get(attemptwire.HeaderMode))
	require.Empty(t, recorder.Body.String())
	result := results.single(t)
	require.Equal(t, attemptwire.ResultProxyRejected, result.Kind)
	require.Equal(t, http.StatusInternalServerError, result.HTTPStatus)
	require.True(t, result.ProviderResultKnown)
}

func TestResponseExecutorMissingWriterNeverUsesLegacyBridge(t *testing.T) {
	for _, test := range []struct {
		name string
		kind string
	}{
		{name: "relay tunnel", kind: agentproxy.IngressKindRelayTunnel},
		{name: "direct tunnel", kind: agentproxy.IngressKindDirectTunnel},
		{name: "unknown ingress"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(trace.CaptureOff)
			ctx := agentproxy.WithIngressMeta(context.Background(), agentproxy.IngressMeta{Kind: test.kind})
			rctx.Request = rctx.Request.WithContext(ctx)
			providerCalled := false
			provider := providerFunc(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
				providerCalled = true
				return attemptexec.ProviderResult{}
			})

			NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

			require.False(t, providerCalled)
			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, attemptwire.ModeControl, recorder.Header().Get(attemptwire.HeaderMode))
			require.Empty(t, recorder.Body.String())
			require.Empty(t, recorder.Header().Values("Trailer"))
		})
	}
}

type capturingAttemptResultWriter struct {
	mu      sync.Mutex
	results []attemptwire.AttemptProxyResult
	err     error
}

func (w *capturingAttemptResultWriter) WriteAttemptResult(result attemptwire.AttemptProxyResult) error {
	if w == nil {
		return errors.New("nil result writer")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.results = append(w.results, result)
	return w.err
}

func (w *capturingAttemptResultWriter) single(t *testing.T) attemptwire.AttemptProxyResult {
	t.Helper()
	w.mu.Lock()
	defer w.mu.Unlock()
	require.Len(t, w.results, 1)
	return w.results[0]
}
