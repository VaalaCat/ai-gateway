package attemptexec_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/attemptexec"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/backend/common"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/resilience"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/settings"
)

type resilienceSettings struct{ value settings.AgentSettings }

func (s resilienceSettings) Settings() settings.AgentSettings { return s.value }

type failingReplayBody struct{ err error }

func (b failingReplayBody) Size() int64                  { return 0 }
func (b failingReplayBody) Open() (io.ReadCloser, error) { return nil, b.err }
func (b failingReplayBody) Bytes(int64) ([]byte, error)  { return nil, b.err }
func (b failingReplayBody) Close() error                 { return nil }

type fixedReplayStore struct{ body app.ReplayBody }

func (s fixedReplayStore) Capture(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
	return s.body, nil
}

type provider500Dispatcher struct{ calls int }

func (d *provider500Dispatcher) Dispatch(*state.RelayContext, state.Attempt) state.AttemptResult {
	d.calls++
	return state.AttemptResult{Err: &common.UpstreamError{Status: http.StatusInternalServerError}}
}

func autoBanProviderContext(t *testing.T, body app.ReplayBody) *state.RelayContext {
	t.Helper()
	writer := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(nil))
	rctx := &state.RelayContext{Context: c, State: &state.RelayState{}}
	if body == nil {
		return rctx
	}
	resources := &state.RequestResources{}
	require.NoError(t, resources.Replace(t.Context(), fixedReplayStore{body: body}, bytes.NewReader(nil), app.BodyLimits{}))
	t.Cleanup(func() { require.NoError(t, resources.Close()) })
	rctx.Resources = resources
	return rctx
}

func autoBanProviderAttempt() state.Attempt {
	channel := &models.Channel{ChannelCore: models.ChannelCore{ID: 7, AutoBan: 1, AutoBanRevision: 4}}
	return state.Attempt{Channel: channel, Source: state.SourceAdmin, SourceID: 7}
}

func autoBanRunner(maxRetries, threshold int) *resilience.Runner {
	return &resilience.Runner{
		Settings: resilienceSettings{value: settings.AgentSettings{
			MaxRetriesPerChannel: maxRetries, RetryBackoffBaseMs: 1, RetryBackoffMaxMs: 1,
			BreakerThreshold: threshold, BreakerCooldownMs: 1000, BreakerEnabled: 0,
		}},
		Breakers: resilience.NewRegistry(), AutoBan: resilience.NewAutoBanTracker(),
	}
}

func TestProviderExecutorPreflightFailureDoesNotTriggerAutoBan(t *testing.T) {
	wantErr := errors.New("replay open failed")
	rctx := autoBanProviderContext(t, failingReplayBody{err: wantErr})
	dispatcher := &provider500Dispatcher{}
	executor := attemptexec.NewProviderExecutor(dispatcher, autoBanRunner(2, 2), nil)

	result := executor.Execute(rctx, autoBanProviderAttempt())

	require.ErrorIs(t, result.Outcome.Err, wantErr)
	require.False(t, result.ProviderDispatched)
	require.Zero(t, result.Dispatches)
	require.Zero(t, dispatcher.calls)
	require.Empty(t, rctx.State.AutoDisableTriggers)
}

func TestProviderExecutorRealRetriesTriggerAutoBanOnce(t *testing.T) {
	rctx := autoBanProviderContext(t, nil)
	dispatcher := &provider500Dispatcher{}
	executor := attemptexec.NewProviderExecutor(dispatcher, autoBanRunner(2, 2), nil)

	result := executor.Execute(rctx, autoBanProviderAttempt())

	require.Error(t, result.Outcome.Err)
	require.True(t, result.ProviderDispatched)
	require.Equal(t, 3, result.Dispatches)
	require.Equal(t, 3, dispatcher.calls)
	require.Len(t, rctx.State.AutoDisableTriggers, 1)
}
