package exec

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestAttemptResponseReceiverControlHasNoBody(t *testing.T) {
	client := httptest.NewRecorder()
	receiver := newAttemptResponseReceiver(client)
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	receiver.WriteHeader(http.StatusOK)

	n, err := receiver.Write([]byte(`{"must":"not-be-a-result-carrier"}`))

	require.Zero(t, n)
	require.Error(t, err)
	require.Empty(t, client.Header())
	require.Empty(t, client.Body.String())
}

func TestFinishRemoteTransportResultBeforeEndLossPreservesDiagnostics(t *testing.T) {
	receiver := newAttemptResponseReceiver(httptest.NewRecorder())
	receiver.Header().Set(attemptwire.HeaderMode, attemptwire.ModeControl)
	receiver.WriteHeader(http.StatusOK)
	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, ProviderDispatched: true, ProviderResultKnown: true,
		HTTPStatus: http.StatusTooManyRequests, ReasonCode: "provider_http_error",
		ErrorMessage: "provider returned HTTP 429", PromptTokens: 7,
	}
	transportErr := errors.New("END frame lost")

	outcome := finishRemoteTransport(receiver, "target-a", app.RoutePathRelay, agentproxy.AttemptTransportOutcome{
		Commit: tunnel.Committed, Stage: "response", Code: "relay_response_interrupted", Err: transportErr,
		AttemptResult: &want,
	})

	require.Equal(t, AttemptCommitUncertain, outcome.Kind)
	require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
	require.Equal(t, 7, outcome.Result.PromptTokens)
	require.True(t, outcome.ProviderResultKnown)
	require.ErrorIs(t, outcome.Result.Err, transportErr)
}
