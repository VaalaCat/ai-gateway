package exec

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAttemptResponseReceiverBuffersControlWithoutWritingClient(t *testing.T) {
	client := httptest.NewRecorder()
	receiver := newAttemptResponseReceiver(client)
	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, HTTPStatus: http.StatusTooManyRequests,
		ProviderDispatched: true, ProviderResultKnown: true, PlanAdvanceAllowed: true,
		ReasonCode: "provider_http_error", ErrorMessage: "provider returned HTTP 429",
	}
	receiver.Header().Set("Content-Type", "application/json")
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	receiver.WriteHeader(http.StatusOK)
	require.NoError(t, json.NewEncoder(receiver).Encode(want))

	outcome := receiver.Finish("target-a", app.RoutePathDirect, tunnel.Committed, nil)

	require.Equal(t, AttemptProviderFailed, outcome.Kind)
	require.ErrorContains(t, outcome.Result.Err, "429")
	var upstream *common.UpstreamError
	require.ErrorAs(t, outcome.Result.Err, &upstream)
	require.Equal(t, http.StatusTooManyRequests, upstream.Status)
	require.True(t, outcome.ProviderDispatched)
	require.True(t, outcome.ProviderResultKnown)
	require.True(t, outcome.PlanAdvanceAllowed)
	require.False(t, outcome.ResponseStarted)
	require.Empty(t, client.Header())
	require.Empty(t, client.Body.String())
}

func TestAttemptResultErrorBuildsSafeProviderJSONEnvelope(t *testing.T) {
	const secret = "secret-provider-token"
	tests := []struct {
		name      string
		errorType string
	}{
		{name: "preserves provider error type", errorType: "rate_limit_error"},
		{name: "omits missing provider error type"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := attemptResultError(attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultProviderFailed, HTTPStatus: http.StatusTooManyRequests,
				ErrorType: test.errorType, ErrorMessage: "provider returned HTTP 429",
			})

			var upstream *common.UpstreamError
			require.ErrorAs(t, err, &upstream)
			require.Equal(t, http.StatusTooManyRequests, upstream.Status)
			require.Equal(t, test.errorType, upstream.ProviderErrorType)
			require.True(t, json.Valid(upstream.Body), "remote provider body: %s", upstream.Body)
			var envelope struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal(upstream.Body, &envelope))
			require.Equal(t, "provider returned HTTP 429", envelope.Error.Message)
			require.Equal(t, test.errorType, envelope.Error.Type)
			require.NotContains(t, string(upstream.Body), secret)
			if test.errorType == "" {
				var raw struct {
					Error map[string]any `json:"error"`
				}
				require.NoError(t, json.Unmarshal(upstream.Body, &raw))
				require.NotContains(t, raw.Error, "type")
			}
		})
	}
}

func TestAttemptResultErrorKeepsNonProviderAndCancellationSemantics(t *testing.T) {
	require.ErrorIs(t, attemptResultError(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultCanceled, ReasonCode: "request_deadline",
	}), context.DeadlineExceeded)
	require.ErrorIs(t, attemptResultError(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultCanceled, ReasonCode: "request_canceled",
	}), context.Canceled)

	err := attemptResultError(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultExecutionRejected, ErrorMessage: "attempt execution rejected",
	})
	require.EqualError(t, err, "attempt execution rejected")
	var upstream *common.UpstreamError
	require.False(t, errors.As(err, &upstream))
}

func TestOutcomeFromAttemptResultCarriesRemoteTrace(t *testing.T) {
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, ProviderDispatched: true, ProviderResultKnown: true,
		Trace: &attemptwire.AttemptTraceWire{
			InboundPath: "/v1/chat/completions", InboundHeaders: `{"X-Request":["one"]}`,
			OutboundPath: "/v1/messages", OutboundHeaders: `{"X-Upstream":["two"]}`,
			ResponseHeaders: `{"X-Response":["three"]}`, ResponseBody: "upstream-body",
			UpstreamStatus: http.StatusBadGateway, ErrorStage: "upstream_status",
		},
	}

	outcome := outcomeFromAttemptResult("target-a", app.RoutePathRelay, tunnel.Committed, result)

	require.NotNil(t, outcome.Trace)
	require.Equal(t, "/v1/chat/completions", outcome.Trace.InboundPath)
	require.Equal(t, "one", outcome.Trace.InboundHeaders.Get("X-Request"))
	require.Equal(t, "two", outcome.Trace.OutboundHeaders.Get("X-Upstream"))
	require.Equal(t, "three", outcome.Trace.ResponseHeaders.Get("X-Response"))
	require.Equal(t, "upstream-body", outcome.Trace.UpstreamBody)
	require.Equal(t, http.StatusBadGateway, outcome.Trace.UpstreamStatus)
	require.True(t, outcome.Trace.Verbose)
}

func TestOutcomeFromAttemptResultCarriesDispatchCount(t *testing.T) {
	outcome := outcomeFromAttemptResult("target-a", app.RoutePathDirect, tunnel.Committed, attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, Dispatches: 3, ProviderDispatched: true,
	})
	require.Equal(t, 3, outcome.Dispatches)
	require.True(t, outcome.ProviderDispatched)
}

func TestOutcomeFromAttemptResultNormalizesDispatchFlag(t *testing.T) {
	outcome := outcomeFromAttemptResult("target-a", app.RoutePathDirect, tunnel.Committed, attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, Dispatches: 1,
	})
	require.True(t, outcome.ProviderDispatched)
}

func TestOutcomeFromAttemptResultNilAndMalformedTraceHeaders(t *testing.T) {
	withoutTrace := outcomeFromAttemptResult("target-a", app.RoutePathDirect, tunnel.Committed, attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded})
	require.Nil(t, withoutTrace.Trace)

	malformed := outcomeFromAttemptResult("target-a", app.RoutePathDirect, tunnel.Committed, attemptwire.AttemptProxyResult{
		Kind:  attemptwire.ResultSucceeded,
		Trace: &attemptwire.AttemptTraceWire{InboundPath: "/v1/chat/completions", InboundHeaders: "not-json"},
	})
	require.NotNil(t, malformed.Trace)
	require.Empty(t, malformed.Trace.InboundHeaders)
}

func TestAttemptResponseReceiverForwardsProviderResponseAndIsolatesInternalMetadata(t *testing.T) {
	client := httptest.NewRecorder()
	receiver := newAttemptResponseReceiver(client)
	wireResult := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, PromptTokens: 7, CompletionTokens: 3,
		ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
	}
	encoded, err := attemptwire.EncodeResult(wireResult)
	require.NoError(t, err)

	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	receiver.Header().Set(attemptwire.TrailerResult, "stale-result-header")
	receiver.Header().Set("X-Provider", "upstream")
	receiver.Header().Add("Trailer", strings.ToLower(attemptwire.TrailerResult)+", "+strings.ToUpper(attemptwire.HeaderMode)+", X-Provider-Trailer")
	receiver.Header()[strings.ToLower(http.TrailerPrefix+attemptwire.HeaderMode)] = []string{"prefixed-mode"}
	receiver.Header()[strings.ToUpper(http.TrailerPrefix+attemptwire.TrailerResult)] = []string{"prefixed-result"}
	receiver.WriteHeader(http.StatusCreated)
	_, err = receiver.Write([]byte("provider-body"))
	require.NoError(t, err)
	delete(receiver.Header(), attemptwire.HeaderMode)
	receiver.Header()[strings.ToLower(attemptwire.HeaderMode)] = []string{"dynamic-mode"}
	receiver.Header().Set(attemptwire.TrailerResult, encoded)
	receiver.Header().Set("X-Provider-Trailer", "complete")

	outcome := receiver.Finish("target-a", app.RoutePathRelay, tunnel.Committed, nil)

	require.Equal(t, AttemptSucceeded, outcome.Kind)
	require.Equal(t, 7, outcome.Result.PromptTokens)
	require.Equal(t, 3, outcome.Result.CompletionTokens)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, http.StatusCreated, client.Code)
	require.Equal(t, "provider-body", client.Body.String())
	require.Equal(t, "upstream", client.Header().Get("X-Provider"))
	require.Equal(t, "complete", client.Header().Get("X-Provider-Trailer"))
	require.Equal(t, "X-Provider-Trailer", client.Header().Get("Trailer"))
	for _, name := range []string{attemptwire.HeaderMode, attemptwire.TrailerResult} {
		require.Empty(t, headerValuesCaseInsensitive(client.Header(), name))
		require.Empty(t, headerValuesCaseInsensitive(client.Header(), http.TrailerPrefix+name))
	}
}

func TestAttemptResponseReceiverFlushesOnlyProviderResponses(t *testing.T) {
	client := httptest.NewRecorder()
	receiver := newAttemptResponseReceiver(client)
	result := mustEncodeAttemptResult(t, attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
	})
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	receiver.Header().Add("Trailer", attemptwire.TrailerResult)
	receiver.WriteHeader(http.StatusOK)
	receiver.Flush()
	receiver.Header().Set(attemptwire.TrailerResult, result)

	outcome := receiver.Finish("target-a", app.RoutePathDirect, tunnel.Committed, nil)
	require.Equal(t, AttemptSucceeded, outcome.Kind)
	require.True(t, client.Flushed)

	controlClient := httptest.NewRecorder()
	control := newAttemptResponseReceiver(controlClient)
	control.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	control.WriteHeader(http.StatusOK)
	control.Flush()
	require.False(t, controlClient.Flushed)
}

func TestAttemptResponseReceiverRecognizesPreHandlerRejectionWithoutMode(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			client := httptest.NewRecorder()
			receiver := newAttemptResponseReceiver(client)
			receiver.WriteHeader(status)
			body, err := json.Marshal(attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultProxyRejected, HTTPStatus: status,
				ReasonCode: "attempt_ingress_rejected", ErrorMessage: "attempt proxy ingress rejected",
			})
			require.NoError(t, err)
			_, err = receiver.Write(body)
			require.NoError(t, err)

			outcome := receiver.Finish("target-a", app.RoutePathDirect, tunnel.Committed, nil)
			require.Equal(t, AttemptProxyRejected, outcome.Kind)
			require.Equal(t, "attempt_ingress_rejected", outcome.ReasonCode)
			require.False(t, outcome.ProviderResultKnown)
			require.Empty(t, client.Body.String())
		})
	}
}

func TestAttemptResponseReceiverMissingOrInvalidResultIsCommitUncertain(t *testing.T) {
	validResult, err := attemptwire.EncodeResult(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
	})
	require.NoError(t, err)

	tests := []struct {
		name   string
		status int
		mode   string
		before func(*attemptResponseReceiver)
		after  func(*attemptResponseReceiver)
	}{
		{name: "unknown 2xx without mode", status: http.StatusOK},
		{name: "control must be 200", status: http.StatusBadGateway, mode: attemptwire.ModeControl, after: writeControlForReceiver(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProviderFailed})},
		{name: "response trailer undeclared", status: http.StatusOK, mode: attemptwire.ModeResponse, after: setResultTrailer(validResult)},
		{name: "response trailer missing", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver()},
		{name: "response trailer damaged", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver(), after: setResultTrailer("not-base64")},
		{name: "response trailer too large", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver(), after: setResultTrailer(strings.Repeat("a", attemptwire.MaxResultWireBytes+1))},
		{name: "response result contradicts started body", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver(), after: setResultTrailer(mustEncodeAttemptResult(t, attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true}))},
		{name: "response proxy result contradicts body", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver(), after: setResultTrailer(mustEncodeAttemptResult(t, attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProxyRejected, ResponseStarted: true}))},
		{name: "duplicate result trailer", status: http.StatusOK, mode: attemptwire.ModeResponse, before: declareResultTrailerForReceiver(), after: func(receiver *attemptResponseReceiver) {
			receiver.Header()[attemptwire.TrailerResult] = []string{validResult, validResult}
		}},
		{name: "control success contradicts no response", status: http.StatusOK, mode: attemptwire.ModeControl, after: writeControlForReceiver(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true})},
		{name: "control started contradicts buffered response", status: http.StatusOK, mode: attemptwire.ModeControl, after: writeControlForReceiver(attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ResponseStarted: true})},
		{name: "unknown mode", status: http.StatusOK, mode: "future"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := httptest.NewRecorder()
			receiver := newAttemptResponseReceiver(client)
			if tt.mode != "" {
				receiver.Header().Set(attemptwire.HeaderMode, tt.mode)
			}
			if tt.before != nil {
				tt.before(receiver)
			}
			receiver.WriteHeader(tt.status)
			if tt.mode == attemptwire.ModeResponse {
				_, err := receiver.Write([]byte("body"))
				require.NoError(t, err)
			}
			if tt.after != nil {
				tt.after(receiver)
			}

			outcome := receiver.Finish("target-a", app.RoutePathDirect, tunnel.Committed, nil)
			require.Equal(t, AttemptCommitUncertain, outcome.Kind)
			require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
			require.Equal(t, "attempt_result_missing", outcome.ReasonCode)
			require.False(t, outcome.ProviderResultKnown)
		})
	}
}

func TestAttemptResponseReceiverCancellationAndInterruptionNeverBecomeReplayable(t *testing.T) {
	tests := []struct {
		name      string
		transport error
		started   bool
		wantKind  AttemptOutcomeKind
	}{
		{name: "canceled before response", transport: context.Canceled, wantKind: AttemptCanceled},
		{name: "deadline before response", transport: context.DeadlineExceeded, wantKind: AttemptCanceled},
		{name: "interrupted before result", transport: errors.New("response interrupted"), wantKind: AttemptCommitUncertain},
		{name: "canceled after response started", transport: context.Canceled, started: true, wantKind: AttemptCanceled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := httptest.NewRecorder()
			receiver := newAttemptResponseReceiver(client)
			if tt.started {
				receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
				receiver.Header().Add("Trailer", attemptwire.TrailerResult)
				receiver.WriteHeader(http.StatusOK)
				_, err := receiver.Write([]byte("partial"))
				require.NoError(t, err)
			}
			outcome := receiver.Finish("target-a", app.RoutePathRelay, tunnel.Committed, tt.transport)
			require.Equal(t, tt.wantKind, outcome.Kind)
			require.Equal(t, tt.started, outcome.ResponseStarted)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{
				CurrentPath: app.RoutePathRelay, HasNextTarget: true, HasLocalTarget: true, HasNextAttempt: true, Outcome: outcome,
			}))
		})
	}
}

func TestAttemptResponseReceiverPreservesDeclaredCommitUncertain(t *testing.T) {
	receiver := newAttemptResponseReceiver(httptest.NewRecorder())
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	receiver.WriteHeader(http.StatusOK)
	require.NoError(t, json.NewEncoder(receiver).Encode(attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultCommitUncertain, ProviderResultKnown: true,
		ReasonCode: "result_trailer_unsupported",
	}))

	outcome := receiver.Finish("target-a", app.RoutePathDirect, tunnel.Committed, nil)
	require.Equal(t, AttemptCommitUncertain, outcome.Kind)
	require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
	require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{HasNextTarget: true, HasNextAttempt: true, Outcome: outcome}))
}

func writeControlForReceiver(result attemptwire.AttemptProxyResult) func(*attemptResponseReceiver) {
	return func(receiver *attemptResponseReceiver) {
		body, _ := json.Marshal(result)
		_, _ = receiver.Write(body)
	}
}

func setResultTrailer(value string) func(*attemptResponseReceiver) {
	return func(receiver *attemptResponseReceiver) { receiver.Header().Set(attemptwire.TrailerResult, value) }
}

func declareResultTrailerForReceiver() func(*attemptResponseReceiver) {
	return func(receiver *attemptResponseReceiver) { receiver.Header().Add("Trailer", attemptwire.TrailerResult) }
}

func mustEncodeAttemptResult(t *testing.T, result attemptwire.AttemptProxyResult) string {
	t.Helper()
	encoded, err := attemptwire.EncodeResult(result)
	require.NoError(t, err)
	return encoded
}
