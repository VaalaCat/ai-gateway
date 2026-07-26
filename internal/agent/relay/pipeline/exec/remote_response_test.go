package exec

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type partialTraceResponseWriter struct {
	header http.Header
	limit  int
}

func (w *partialTraceResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *partialTraceResponseWriter) WriteHeader(int) {}

func (w *partialTraceResponseWriter) Write(body []byte) (int, error) {
	n := min(w.limit, len(body))
	return n, errors.New("client write interrupted")
}

func TestRemoteTraceResponseWriterCorrectsClientFallback(t *testing.T) {
	t.Run("partial write", func(t *testing.T) {
		writer := newRemoteTraceResponseWriter(&partialTraceResponseWriter{limit: 4})
		n, err := writer.Write([]byte("response"))
		require.Equal(t, 4, n)
		require.Error(t, err)
		record := &trace.TraceRecord{ClientResponseBody: "response"}
		writer.CorrectClientResponseBody(record)
		require.Equal(t, "resp", record.ClientResponseBody)
	})

	t.Run("masked secret preserves raw partial offsets", func(t *testing.T) {
		const secret = "channel-secret"
		raw := "前|" + secret + "|后-suffix"
		masked := "前|" + strings.Repeat("*", len(secret)) + "|后-suffix"
		for _, tc := range []struct {
			name    string
			written int
		}{
			{name: "before secret", written: len("前|")},
			{name: "inside secret", written: len("前|channel")},
			{name: "after secret and multibyte", written: len("前|" + secret + "|后")},
			{name: "full", written: len(raw)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				writer := newRemoteTraceResponseWriter(&partialTraceResponseWriter{limit: tc.written})
				_, _ = writer.Write([]byte(raw))
				record := &trace.TraceRecord{ClientResponseBody: masked}
				writer.CorrectClientResponseBody(record)
				require.Equal(t, masked[:tc.written], record.ClientResponseBody)
				require.NotContains(t, record.ClientResponseBody, secret)
				if tc.written < len(raw) {
					require.NotContains(t, record.ClientResponseBody, "suffix")
				}
			})
		}
	})

	t.Run("zero byte write", func(t *testing.T) {
		writer := newRemoteTraceResponseWriter(&partialTraceResponseWriter{limit: 0})
		_, _ = writer.Write([]byte("response"))
		record := &trace.TraceRecord{ClientResponseBody: "response"}
		writer.CorrectClientResponseBody(record)
		require.Empty(t, record.ClientResponseBody)
	})

	t.Run("nil and unobserved", func(t *testing.T) {
		require.Nil(t, newRemoteTraceResponseWriter(nil))
		writer := newRemoteTraceResponseWriter(httptest.NewRecorder())
		record := &trace.TraceRecord{ClientResponseBody: "target-client"}
		writer.CorrectClientResponseBody(record)
		require.Equal(t, "target-client", record.ClientResponseBody)
	})
}

func TestAttemptResponseReceiverForwardsProviderResponseAndExplicitResult(t *testing.T) {
	client := httptest.NewRecorder()
	receiver := newAttemptResponseReceiver(client)
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	receiver.Header().Set("X-Provider", "upstream")
	receiver.Header().Add("Trailer", "X-Provider-Trailer")
	receiver.WriteHeader(http.StatusCreated)
	_, err := receiver.Write([]byte("provider-body"))
	require.NoError(t, err)
	receiver.Header().Set("X-Provider-Trailer", "complete")
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, PromptTokens: 7, CompletionTokens: 3,
		ProviderDispatched: true, ProviderResultKnown: true, ResponseStarted: true,
	}

	outcome := receiver.FinishWithResult("target-a", app.RoutePathRelay, tunnel.Committed, result, "", nil)

	require.Equal(t, AttemptSucceeded, outcome.Kind)
	require.Equal(t, 7, outcome.Result.PromptTokens)
	require.Equal(t, 3, outcome.Result.CompletionTokens)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, http.StatusCreated, client.Code)
	require.Equal(t, "provider-body", client.Body.String())
	require.Equal(t, "upstream", client.Header().Get("X-Provider"))
	require.Equal(t, "complete", client.Header().Get("X-Provider-Trailer"))
	require.Equal(t, "X-Provider-Trailer", client.Header().Get("Trailer"))
	require.Empty(t, client.Header().Get(attemptwire.HeaderMode))
}

func TestAttemptResponseReceiverAcceptsExplicitControlResults(t *testing.T) {
	for _, result := range []attemptwire.AttemptProxyResult{
		{Kind: attemptwire.ResultExecutionRejected, ProviderResultKnown: true, PlanAdvanceAllowed: true},
		{Kind: attemptwire.ResultProxyRejected, ProviderResultKnown: true, ReasonCode: "attempt_ingress_rejected"},
		{Kind: attemptwire.ResultCommitUncertain, ProviderResultKnown: true, ReasonCode: "response_commit_uncertain"},
	} {
		t.Run(string(result.Kind), func(t *testing.T) {
			receiver := newAttemptResponseReceiver(httptest.NewRecorder())
			receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
			receiver.WriteHeader(http.StatusOK)

			outcome := receiver.FinishWithResult("target-a", app.RoutePathDirect, tunnel.Committed, result, "", nil)
			require.Equal(t, result.Kind, outcome.Kind)
			require.Equal(t, result.PlanAdvanceAllowed, outcome.PlanAdvanceAllowed)
			if result.Kind == attemptwire.ResultCommitUncertain {
				require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
			}
		})
	}
}

func TestAttemptResponseReceiverInterruptedResultNeverReplays(t *testing.T) {
	interrupted := errors.New("End frame missing")
	receiver := newAttemptResponseReceiver(httptest.NewRecorder())
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
	receiver.WriteHeader(http.StatusOK)
	_, err := receiver.Write([]byte("partial"))
	require.NoError(t, err)
	result := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultSucceeded, ProviderDispatched: true, ProviderResultKnown: true,
		Dispatches: 2, PromptTokens: 17, ResponseStarted: true,
	}

	outcome := receiver.FinishWithResult(
		"target-a", app.RoutePathDirect, tunnel.Committed, result,
		agentproxy.CodeRelayResponseInterrupted, interrupted,
	)

	require.Equal(t, AttemptCommitUncertain, outcome.Kind)
	require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
	require.True(t, outcome.ResponseStarted)
	require.True(t, outcome.ProviderResultKnown)
	require.Equal(t, 2, outcome.Dispatches)
	require.Equal(t, 17, outcome.Result.PromptTokens)
	require.ErrorIs(t, outcome.Result.Err, interrupted)
	require.Equal(t, agentproxy.CodeRelayResponseInterrupted, outcome.ReasonCode)
	require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{HasNextAttempt: true, Outcome: outcome}))
}

func TestAttemptResponseReceiverInvalidOrMissingResultUsesTransportStage(t *testing.T) {
	tests := []struct {
		name   string
		result attemptwire.AttemptProxyResult
		code   string
		err    error
	}{
		{name: "invalid result", result: attemptwire.AttemptProxyResult{}, code: agentproxy.CodeRelayResponseInterrupted, err: errors.New("Result frame invalid")},
		{name: "zero result and no transport detail", result: attemptwire.AttemptProxyResult{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			receiver := newAttemptResponseReceiver(httptest.NewRecorder())
			receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
			receiver.WriteHeader(http.StatusOK)

			outcome := receiver.FinishWithResult(
				"target-a", app.RoutePathRelay, tunnel.Committed, test.result, test.code, test.err,
			)
			require.Equal(t, AttemptCommitUncertain, outcome.Kind)
			require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
			if test.code != "" {
				require.Equal(t, test.code, outcome.ReasonCode)
			} else {
				require.Equal(t, reasonAttemptResultInterrupted, outcome.ReasonCode)
			}
		})
	}
}

func TestAttemptResponseReceiverCancellationPreservesTerminalSemantics(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		t.Run(cause.Error(), func(t *testing.T) {
			receiver := newAttemptResponseReceiver(httptest.NewRecorder())
			receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
			receiver.WriteHeader(http.StatusOK)
			result := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultCanceled, ProviderResultKnown: true}

			outcome := receiver.FinishWithResult(
				"target-a", app.RoutePathRelay, tunnel.Committed, result,
				agentproxy.CodeRequestCancelled, cause,
			)
			require.Equal(t, AttemptCommitUncertain, outcome.Kind)
			require.ErrorIs(t, outcome.Result.Err, cause)
			require.Equal(t, ActionStop, nextAttemptAction(AttemptDecisionInput{HasNextAttempt: true, Outcome: outcome}))
		})
	}
}
