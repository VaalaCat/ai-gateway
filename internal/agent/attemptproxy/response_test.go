package attemptproxy

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
)

func TestProxyResponseControlDoesNotLeakProviderState(t *testing.T) {
	tests := []struct {
		name       string
		provider   attemptexec.ProviderResult
		wantKind   attemptwire.ResultKind
		wantReason string
		wantPlan   bool
	}{
		{
			name: "provider 500",
			provider: attemptexec.ProviderResult{
				Outcome: state.AttemptResult{Err: &common.UpstreamError{
					Status: 500, ProviderErrorType: "server_error", Body: []byte(`{"error":{"message":"secret"}}`),
				}},
				Dispatches: 1, ProviderDispatched: true,
			},
			wantKind: attemptwire.ResultProviderFailed, wantReason: "provider_http_error", wantPlan: true,
		},
		{
			name: "attempt limiter reject",
			provider: attemptexec.ProviderResult{
				Outcome: state.AttemptResult{Err: state.ErrRateLimited},
			},
			wantKind: attemptwire.ResultExecutionRejected, wantReason: "attempt_rate_limited", wantPlan: true,
		},
		{
			name: "invalid provider request",
			provider: attemptexec.ProviderResult{
				Outcome:    state.AttemptResult{Err: &common.UpstreamError{Status: 400, ProviderErrorType: "invalid_request_error"}},
				Dispatches: 1, ProviderDispatched: true,
			},
			wantKind: attemptwire.ResultProviderFailed, wantReason: "provider_http_error", wantPlan: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(true)
			provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				rctx.Writer.Header().Set("X-Provider-Secret", "must-not-leak")
				rctx.Writer.WriteHeader(http.StatusInsufficientStorage)
				return tt.provider
			})

			NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
			require.Equal(t, attemptwire.ModeControl, recorder.Header().Get(attemptwire.HeaderMode))
			require.Empty(t, recorder.Header().Get("X-Provider-Secret"))
			require.Empty(t, recorder.Header().Get("Trailer"))
			var got attemptwire.AttemptProxyResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
			require.NoError(t, got.Validate())
			require.Equal(t, tt.wantKind, got.Kind)
			require.Equal(t, tt.wantReason, got.ReasonCode)
			require.Equal(t, tt.wantPlan, got.PlanAdvanceAllowed)
			require.True(t, got.ProviderResultKnown)
			require.False(t, got.ResponseStarted)
			if tt.name == "provider 500" {
				require.Equal(t, 500, got.HTTPStatus)
				require.Equal(t, "server_error", got.ErrorType)
				require.NotContains(t, got.ErrorMessage, "secret")
			}
		})
	}
}

func TestProxyResponseCarriesProviderDispatchCount(t *testing.T) {
	rctx, recorder := newResponseRelayContext(false)
	provider := providerFunc(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
		return attemptexec.ProviderResult{
			Outcome:    state.AttemptResult{Err: &common.UpstreamError{Status: http.StatusBadGateway}},
			Dispatches: 3, ProviderDispatched: true,
		}
	})

	NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

	var got attemptwire.AttemptProxyResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	require.Equal(t, 3, got.Dispatches)
	require.True(t, got.ProviderDispatched)
}

func TestAttemptResultFallbacksPreserveDispatchCount(t *testing.T) {
	result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, Dispatches: 3, ProviderDispatched: true}

	require.Equal(t, 3, minimalCommitUncertainResult(result, "write_failed").Dispatches)
	require.Equal(t, 3, unsupportedTrailerControlResult(result).Dispatches)
}

func TestWriteControlOversizedScalarsUsesMinimalResult(t *testing.T) {
	c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, nil)
	writeControl(c.Writer, attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, Dispatches: 3, ProviderDispatched: true,
		ProviderResultKnown: true, PlanAdvanceAllowed: true,
		ErrorMessage: strings.Repeat("e", attemptwire.MaxResultWireBytes),
	})

	require.LessOrEqual(t, recorder.Body.Len(), attemptwire.MaxResultWireBytes)
	var got attemptwire.AttemptProxyResult
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	require.Equal(t, attemptwire.ResultCommitUncertain, got.Kind)
	require.Equal(t, 3, got.Dispatches)
	require.True(t, got.ProviderDispatched)
	require.True(t, got.ProviderResultKnown)
	require.False(t, got.PlanAdvanceAllowed)
	require.False(t, got.ResponseStarted)
}

func TestProxyResponseCommitsProviderJSONAndResultTrailer(t *testing.T) {
	rctx, recorder := newResponseRelayContext(true)
	provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("Content-Type", "application/provider+json")
		rctx.Writer.Header().Set("X-Provider", "kept")
		rctx.Writer.WriteHeader(http.StatusCreated)
		_, err := rctx.Writer.Write([]byte(`{"ok":true}`))
		require.NoError(t, err)
		return attemptexec.ProviderResult{
			Outcome:    state.AttemptResult{Written: true, PromptTokens: 13, CompletionTokens: 5, UpstreamModel: "real"},
			Dispatches: 1, ProviderDispatched: true,
		}
	})

	NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

	response := recorder.Result()
	require.Equal(t, http.StatusCreated, response.StatusCode)
	require.Equal(t, "application/provider+json", response.Header.Get("Content-Type"))
	require.Equal(t, "kept", response.Header.Get("X-Provider"))
	require.Equal(t, attemptwire.ModeResponse, response.Header.Get(attemptwire.HeaderMode))
	require.Equal(t, attemptwire.TrailerResult, response.Header.Get("Trailer"))
	require.Equal(t, `{"ok":true}`, recorder.Body.String())
	result := decodeResponseTrailer(t, response)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.Equal(t, 13, result.PromptTokens)
	require.Equal(t, 5, result.CompletionTokens)
	require.True(t, result.Written)
	require.True(t, result.ResponseStarted)
	require.False(t, result.PlanAdvanceAllowed)
}

func TestProxyResponseSSEFlushAndWriteStringStreamWithoutBuffering(t *testing.T) {
	tests := []struct {
		name  string
		write func(gin.ResponseWriter)
		want  string
	}{
		{
			name: "first flush commits response",
			write: func(w gin.ResponseWriter) {
				w.Flush()
				_, _ = w.WriteString("data: one\n\n")
				_, _ = w.WriteString("data: two\n\n")
			},
			want: "data: one\n\ndata: two\n\n",
		},
		{
			name: "first write string commits response",
			write: func(w gin.ResponseWriter) {
				_, _ = w.WriteString("data: one\n\n")
				w.Flush()
				_, _ = w.WriteString("data: two\n\n")
			},
			want: "data: one\n\ndata: two\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(false)
			provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				rctx.Writer.Header().Set("Content-Type", "text/event-stream")
				rctx.Writer.WriteHeader(http.StatusAccepted)
				tt.write(rctx.Writer)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}, ProviderDispatched: true}
			})

			NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

			response := recorder.Result()
			require.Equal(t, http.StatusAccepted, response.StatusCode)
			require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
			require.Equal(t, attemptwire.ModeResponse, response.Header.Get(attemptwire.HeaderMode))
			require.Equal(t, tt.want, recorder.Body.String())
			require.NoError(t, decodeResponseTrailer(t, response).Validate())
		})
	}
}

func TestProxyResponseBufferedSuccessCommitsHeadersWithoutBody(t *testing.T) {
	rctx, recorder := newResponseRelayContext(false)
	provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("X-Provider", "empty-success")
		rctx.Writer.WriteHeader(http.StatusOK)
		return attemptexec.ProviderResult{ProviderDispatched: true}
	})

	NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

	response := recorder.Result()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, "empty-success", response.Header.Get("X-Provider"))
	require.Empty(t, recorder.Body.String())
	require.Equal(t, attemptwire.ModeResponse, response.Header.Get(attemptwire.HeaderMode))
	result := decodeResponseTrailer(t, response)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.True(t, result.ResponseStarted)
}

func TestProxyResponseSocketJSONTrailerIsVisible(t *testing.T) {
	payload := `{"ok":true}`
	response, body := executeResponseOverSocket(t, providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("Content-Type", "application/provider+json")
		rctx.Writer.WriteHeader(http.StatusOK)
		_, _ = rctx.Writer.WriteString(payload)
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}, ProviderDispatched: true}
	}))

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, payload, string(body))
	require.Equal(t, attemptwire.ModeResponse, response.Header.Get(attemptwire.HeaderMode))
	require.Equal(t, attemptwire.ResultSucceeded, decodeResponseTrailer(t, response).Kind)
}

func TestProxyResponseSocketContentLengthIsRemovedForTrailer(t *testing.T) {
	payload := `{"provider":"body"}`
	response, body := executeResponseOverSocket(t, providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		rctx.Writer.Header().Set("X-Provider", "kept")
		rctx.Writer.WriteHeader(http.StatusOK)
		_, _ = rctx.Writer.WriteString(payload)
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}, ProviderDispatched: true}
	}))

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, payload, string(body))
	require.Equal(t, "kept", response.Header.Get("X-Provider"))
	require.Equal(t, int64(-1), response.ContentLength)
	require.True(t, slices.Contains(response.TransferEncoding, "chunked"), response.TransferEncoding)
	require.Equal(t, attemptwire.ResultSucceeded, decodeResponseTrailer(t, response).Kind)
}

func TestProxyResponseSocketSSEFlushTrailerIsVisible(t *testing.T) {
	payload := "data: one\n\ndata: two\n\n"
	response, body := executeResponseOverSocket(t, providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("Content-Type", "text/event-stream")
		_, _ = rctx.Writer.WriteString("data: one\n\n")
		rctx.Writer.Flush()
		_, _ = rctx.Writer.WriteString("data: two\n\n")
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}, ProviderDispatched: true}
	}))

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Equal(t, payload, string(body))
	require.Equal(t, "text/event-stream", response.Header.Get("Content-Type"))
	require.Equal(t, attemptwire.ResultSucceeded, decodeResponseTrailer(t, response).Kind)
}

func TestProxyResponseSocketBuffered200TrailerIsVisible(t *testing.T) {
	response, body := executeResponseOverSocket(t, providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		rctx.Writer.Header().Set("X-Provider", "empty-success")
		rctx.Writer.WriteHeader(http.StatusOK)
		return attemptexec.ProviderResult{ProviderDispatched: true}
	}))

	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Empty(t, body)
	require.Equal(t, "empty-success", response.Header.Get("X-Provider"))
	result := decodeResponseTrailer(t, response)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.True(t, result.ResponseStarted)
}

func TestProxyResponseSocketStatusWithoutTrailerSupportUsesControl(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusNotModified} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			response, body := executeResponseOverSocket(t, providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				rctx.Writer.Header().Set("X-Provider-Secret", "must-not-leak")
				rctx.Writer.Header().Set("Content-Type", "application/provider+json")
				rctx.Writer.WriteHeader(status)
				return attemptexec.ProviderResult{ProviderDispatched: true}
			}))

			require.Equal(t, http.StatusOK, response.StatusCode)
			require.Equal(t, "application/json", response.Header.Get("Content-Type"))
			require.Equal(t, attemptwire.ModeControl, response.Header.Get(attemptwire.HeaderMode))
			require.Empty(t, response.Header.Get("X-Provider-Secret"))
			require.Empty(t, response.Trailer)
			var result attemptwire.AttemptProxyResult
			require.NoError(t, json.Unmarshal(body, &result))
			require.Equal(t, attemptwire.ResultCommitUncertain, result.Kind)
			require.True(t, result.ProviderResultKnown)
			require.True(t, result.ProviderDispatched)
			require.False(t, result.Written)
			require.False(t, result.ResponseStarted)
			require.False(t, result.PlanAdvanceAllowed)
		})
	}
}

func TestProxyResponseWrittenOutcomeOrStartedWriterNeverFallsBackToControl(t *testing.T) {
	tests := []struct {
		name     string
		started  bool
		written  bool
		provider func(gin.ResponseWriter)
	}{
		{
			name:     "outcome written",
			written:  true,
			provider: func(w gin.ResponseWriter) { _, _ = w.Write([]byte("partial")) },
		},
		{
			name:     "writer already started",
			started:  true,
			provider: func(w gin.ResponseWriter) {},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(false)
			if tt.started {
				rctx.Writer.WriteHeaderNow()
			}
			provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				tt.provider(rctx.Writer)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: tt.written, Err: errors.New("failed")}}
			})
			NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)
			require.NotEqual(t, attemptwire.ModeControl, recorder.Header().Get(attemptwire.HeaderMode))
			require.NotContains(t, recorder.Body.String(), `"kind":"provider_failed"`)
		})
	}
}

func TestProxyResponseVerboseTraceIsReducedBelow48KBAndDecodable(t *testing.T) {
	rctx, recorder := newResponseRelayContext(true)
	huge := strings.Repeat("trace-body-", 12_000)
	rctx.State.Recorder.WithInbound(rctx.Request, []byte(huge))
	provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
		_, _ = rctx.Writer.WriteString("provider-body")
		return attemptexec.ProviderResult{Outcome: state.AttemptResult{Written: true}, ProviderDispatched: true}
	})

	NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)

	response := recorder.Result()
	raw := response.Trailer.Get(attemptwire.TrailerResult)
	require.NotEmpty(t, raw)
	require.LessOrEqual(t, len(raw), attemptwire.MaxResultWireBytes)
	result, err := attemptwire.DecodeResult(raw)
	require.NoError(t, err)
	require.NotNil(t, result.Trace)
	require.Empty(t, result.Trace.InboundBody)
}

func TestProxyResponseExact48KBoundaryAndOversizeFallback(t *testing.T) {
	base := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ResponseStarted: true, ErrorMessage: "x"}
	raw, err := json.Marshal(base)
	require.NoError(t, err)
	messageLen := attemptwire.MaxResultWireBytes*3/4 - len(raw) + 1
	exact := base
	exact.ErrorMessage = strings.Repeat("x", messageLen)
	encoded, err := attemptwire.EncodeResult(exact)
	require.NoError(t, err)
	require.Len(t, encoded, attemptwire.MaxResultWireBytes)

	for _, tt := range []struct {
		name   string
		result attemptwire.AttemptProxyResult
		kind   attemptwire.ResultKind
	}{
		{name: "exact limit", result: exact, kind: attemptwire.ResultSucceeded},
		{name: "one byte beyond JSON limit", result: func() attemptwire.AttemptProxyResult {
			tooLarge := exact
			tooLarge.ErrorMessage += "x"
			return tooLarge
		}(), kind: attemptwire.ResultCommitUncertain},
		{name: "invalid result cannot encode", result: attemptwire.AttemptProxyResult{Kind: "invalid"}, kind: attemptwire.ResultCommitUncertain},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, nil)
			writer := newAttemptResponseWriter(c.Writer)
			c.Writer = writer
			_, writeErr := writer.WriteString("provider-body")
			require.NoError(t, writeErr)
			writer.FinishResponse(tt.result)
			require.Equal(t, "provider-body", recorder.Body.String())
			response := recorder.Result()
			got := decodeResponseTrailer(t, response)
			require.Equal(t, tt.kind, got.Kind)
			require.LessOrEqual(t, len(response.Trailer.Get(attemptwire.TrailerResult)), attemptwire.MaxResultWireBytes)
		})
	}
}

func TestProxyResponseWriterFailuresBecomeCommitUncertainWithoutSecondBody(t *testing.T) {
	tests := []struct {
		name string
		wrap func(gin.ResponseWriter) gin.ResponseWriter
		act  func(gin.ResponseWriter)
	}{
		{
			name: "write returns error",
			wrap: func(base gin.ResponseWriter) gin.ResponseWriter {
				return &responseFailWriter{ResponseWriter: base, writeErr: errors.New("broken pipe")}
			},
			act: func(w gin.ResponseWriter) { _, _ = w.Write([]byte("provider-body")) },
		},
		{
			name: "write string returns error",
			wrap: func(base gin.ResponseWriter) gin.ResponseWriter {
				return &responseFailWriter{ResponseWriter: base, stringErr: errors.New("broken pipe")}
			},
			act: func(w gin.ResponseWriter) { _, _ = w.WriteString("provider-body") },
		},
		{
			name: "flush panics during commit",
			wrap: func(base gin.ResponseWriter) gin.ResponseWriter {
				return &responseFailWriter{ResponseWriter: base, panicFlush: true}
			},
			act: func(w gin.ResponseWriter) { w.Flush() },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(false)
			failing := tt.wrap(rctx.Writer)
			rctx.Writer = failing
			provider := providerFunc(func(rctx *state.RelayContext, _ state.Attempt) attemptexec.ProviderResult {
				tt.act(rctx.Writer)
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: errors.New("client write failed")}, ProviderDispatched: true}
			})
			require.NotPanics(t, func() { NewResponseExecutor().Execute(rctx, state.Attempt{}, provider) })
			require.Equal(t, attemptwire.ModeResponse, recorder.Header().Get(attemptwire.HeaderMode))
			require.NotContains(t, recorder.Body.String(), `"kind"`)
			result := decodeResponseTrailer(t, recorder.Result())
			require.Equal(t, attemptwire.ResultCommitUncertain, result.Kind)
			require.True(t, result.ResponseStarted)
			require.False(t, result.PlanAdvanceAllowed)
		})
	}
}

func TestProxyResponseCanceledRequestDoesNotAllowReplay(t *testing.T) {
	for _, err := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(err.Error(), func(t *testing.T) {
			rctx, recorder := newResponseRelayContext(false)
			provider := providerFunc(func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult {
				return attemptexec.ProviderResult{Outcome: state.AttemptResult{Err: err}}
			})
			NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)
			var result attemptwire.AttemptProxyResult
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &result))
			require.Equal(t, attemptwire.ResultCanceled, result.Kind)
			require.False(t, result.PlanAdvanceAllowed)
		})
	}
}

func newResponseRelayContext(traceEnabled bool) (*state.RelayContext, *httptest.ResponseRecorder) {
	c, recorder := newAttemptTestContext(http.MethodPost, attemptwire.EndpointPath, []byte(`{"model":"public"}`))
	return &state.RelayContext{
		Context: c,
		Input:   state.RelayInput{Model: "real"},
		State:   &state.RelayState{Recorder: trace.NewRecorder(traceEnabled, 256*1024)},
	}, recorder
}

func executeResponseOverSocket(t *testing.T, provider attemptexec.ProviderAttemptExecutor) (*http.Response, []byte) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		c, _ := gin.CreateTestContext(writer)
		c.Request = request
		rctx := &state.RelayContext{
			Context: c,
			Input:   state.RelayInput{Model: "real"},
			State:   &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
		}
		NewResponseExecutor().Execute(rctx, state.Attempt{}, provider)
	}))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(http.MethodPost, server.URL, strings.NewReader(`{"model":"public"}`))
	require.NoError(t, err)
	response, err := server.Client().Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response, body
}

func decodeResponseTrailer(t *testing.T, response *http.Response) attemptwire.AttemptProxyResult {
	t.Helper()
	raw := response.Trailer.Get(attemptwire.TrailerResult)
	require.NotEmpty(t, raw, "response trailer is missing")
	result, err := attemptwire.DecodeResult(raw)
	require.NoError(t, err)
	require.NoError(t, result.Validate())
	return result
}

type providerFunc func(*state.RelayContext, state.Attempt) attemptexec.ProviderResult

func (f providerFunc) Execute(rctx *state.RelayContext, attempt state.Attempt) attemptexec.ProviderResult {
	return f(rctx, attempt)
}

type responseFailWriter struct {
	gin.ResponseWriter
	writeErr   error
	stringErr  error
	panicFlush bool
	writes     atomic.Int32
}

func (w *responseFailWriter) Write(p []byte) (int, error) {
	w.writes.Add(1)
	if w.writeErr != nil {
		return 0, w.writeErr
	}
	return w.ResponseWriter.Write(p)
}

func (w *responseFailWriter) WriteString(s string) (int, error) {
	w.writes.Add(1)
	if w.stringErr != nil {
		return 0, w.stringErr
	}
	return w.ResponseWriter.WriteString(s)
}

func (w *responseFailWriter) Flush() {
	if w.panicFlush {
		panic("flush interrupted")
	}
	w.ResponseWriter.Flush()
}

func (w *responseFailWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, fmt.Errorf("hijack unsupported")
}
