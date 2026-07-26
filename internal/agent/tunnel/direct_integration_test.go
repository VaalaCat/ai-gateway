package tunnel

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

func TestDirectIntegrationAttemptUsesBoundIdentityAndExplicitResult(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	requestSeen := make(chan struct{}, 1)
	targetRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Equal(t, []byte("abc"), body)
		ingress, ok := agentproxy.IngressMetaFromContext(r.Context())
		require.True(t, ok)
		require.Equal(t, agentproxy.IngressKindDirectTunnel, ingress.Kind)
		require.Equal(t, "source-a", ingress.SourceAgentID)
		resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
		require.True(t, ok)

		w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
		w.Header().Set("X-Direct", "yes")
		w.WriteHeader(http.StatusCreated)
		_, err = w.Write([]byte("xyz"))
		require.NoError(t, err)
		require.NoError(t, resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
		}))
		requestSeen <- struct{}{}
	})
	ingress, server := fixture.startWithRouter(t, targetRouter)

	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	runDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { runDone <- session.Run(ctx) }()

	request := validBoundRelayRequest("/v1/responses")
	request.BodyLength = 3
	stream, err := session.OpenAttemptStream(t.Context(), request)
	require.NoError(t, err)
	require.NoError(t, stream.Commit(t.Context()))
	fixture.source.Status = consts.StatusDisabled
	require.NoError(t, stream.Upload(t.Context(), bytes.NewBufferString("abc")))

	recorder := httptest.NewRecorder()
	result, err := stream.CopyAttemptResponse(t.Context(), recorder)
	require.NoError(t, err)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.True(t, result.ProviderResultKnown)
	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, "yes", recorder.Header().Get("X-Direct"))
	require.Equal(t, "xyz", recorder.Body.String())
	receiveWithDirectTimeout(t, requestSeen)
	require.NoError(t, stream.Close())

	require.NoError(t, session.Close(t.Context()))
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("outgoing direct session did not stop")
	}
	require.NoError(t, ingress.Close(t.Context()))
}

func TestDirectIncomingChecksTargetStatusForEachNewOpen(t *testing.T) {
	fixture := newDirectIngressFixture(t)
	var targetEnabled atomic.Bool
	targetEnabled.Store(true)
	fixture.findAgentByID = func(agentID string) *models.Agent {
		switch agentID {
		case "source-a":
			return &models.Agent{AgentID: "source-a", Status: consts.StatusEnabled}
		case "target-a":
			status := consts.StatusDisabled
			if targetEnabled.Load() {
				status = consts.StatusEnabled
			}
			return &models.Agent{AgentID: "target-a", Status: status}
		default:
			return nil
		}
	}
	firstCompleted := make(chan struct{}, 1)
	targetRouter := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		resultWriter, ok := attemptwire.AttemptResultWriterFromContext(r.Context())
		if !ok {
			return
		}
		w.Header().Set(attemptwire.HeaderMode, attemptwire.ModeResponse)
		w.WriteHeader(http.StatusNoContent)
		_ = resultWriter.WriteAttemptResult(attemptwire.AttemptProxyResult{
			Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ResponseStarted: true,
		})
		select {
		case firstCompleted <- struct{}{}:
		default:
		}
	})
	ingress, server := fixture.startWithRouter(t, targetRouter)

	session, err := NewDirectDialer(DirectDialerOptions{HandshakeTimeout: time.Second}).DialDirectSession(
		t.Context(), fixture.dialRequest(server.URL, testLimits(2)),
	)
	require.NoError(t, err)
	runDone := make(chan error, 1)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() { runDone <- session.Run(ctx) }()

	request := validBoundRelayRequest("/v1/responses")
	request.BodyLength = 0
	first, err := session.OpenAttemptStream(t.Context(), request)
	require.NoError(t, err)
	require.NoError(t, first.Commit(t.Context()))
	targetEnabled.Store(false)
	require.NoError(t, first.Upload(t.Context(), bytes.NewReader(nil)))
	recorder := httptest.NewRecorder()
	result, err := first.CopyAttemptResponse(t.Context(), recorder)
	require.NoError(t, err)
	require.Equal(t, attemptwire.ResultSucceeded, result.Kind)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	receiveWithDirectTimeout(t, firstCompleted)
	require.NoError(t, first.Close())

	second, err := session.OpenAttemptStream(t.Context(), request)
	require.NoError(t, err)
	err = second.Commit(t.Context())
	require.ErrorIs(t, err, errStreamClosed)
	require.Equal(t, wire.PreCommit, second.CommitState())
	require.NoError(t, second.Close())

	require.NoError(t, session.Close(t.Context()))
	select {
	case <-runDone:
	case <-time.After(time.Second):
		t.Fatal("outgoing direct session did not stop")
	}
	require.NoError(t, ingress.Close(t.Context()))
}
