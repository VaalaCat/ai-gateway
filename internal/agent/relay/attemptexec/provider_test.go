package attemptexec

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/relay/state"
	"github.com/VaalaCat/ai-gateway/internal/agent/relay/trace"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	"github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type countingReplayBody struct {
	content   []byte
	openErr   error
	failAfter int32

	opens  atomic.Int32
	closes atomic.Int32
}

func (b *countingReplayBody) Size() int64 { return int64(len(b.content)) }

func (b *countingReplayBody) Open() (io.ReadCloser, error) {
	call := b.opens.Add(1)
	if b.openErr != nil && (b.failAfter == 0 || call > b.failAfter) {
		return nil, b.openErr
	}
	return io.NopCloser(bytes.NewReader(b.content)), nil
}

func (b *countingReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.content)) > limit {
		return nil, io.ErrShortBuffer
	}
	return append([]byte(nil), b.content...), nil
}

func (b *countingReplayBody) Close() error {
	b.closes.Add(1)
	return nil
}

func (b *countingReplayBody) openCount() int {
	return int(b.opens.Load())
}

type fixedBodyStore struct{ body app.ReplayBody }

func (s fixedBodyStore) Capture(context.Context, io.Reader, app.BodyLimits) (app.ReplayBody, error) {
	return s.body, nil
}

type bodyReadingDispatcher struct {
	results          []state.AttemptResult
	bodies           [][]byte
	modes            []attemptproxy.ExecutionMode
	contentLengths   []int64
	getBodyInstalled []bool
}

func (d *bodyReadingDispatcher) Dispatch(rctx *state.RelayContext, a state.Attempt) state.AttemptResult {
	body, err := io.ReadAll(rctx.Context.Request.Body)
	if err != nil {
		return state.AttemptResult{Err: err}
	}
	d.bodies = append(d.bodies, body)
	d.modes = append(d.modes, a.Mode)
	d.contentLengths = append(d.contentLengths, rctx.Context.Request.ContentLength)
	d.getBodyInstalled = append(d.getBodyInstalled, rctx.Context.Request.GetBody != nil)
	if len(d.bodies) > len(d.results) {
		return state.AttemptResult{}
	}
	return d.results[len(d.bodies)-1]
}

type recordingDispatcher struct {
	attempts []state.Attempt
	result   state.AttemptResult
}

type traceFailingDispatcher struct{ calls int }

func (d *traceFailingDispatcher) Dispatch(rctx *state.RelayContext, _ state.Attempt) state.AttemptResult {
	d.calls++
	err := errors.New("provider failed")
	rctx.State.Recorder.WithFail(trace.StageUpstreamStatus, err)
	return state.AttemptResult{Err: err}
}

func (d *recordingDispatcher) Dispatch(_ *state.RelayContext, a state.Attempt) state.AttemptResult {
	d.attempts = append(d.attempts, a)
	return d.result
}

type retryOnceRunner struct{}

func (retryOnceRunner) Run(_ *state.RelayContext, _ state.Attempt, dispatch func() state.AttemptResult) state.AttemptResult {
	result := dispatch()
	if result.Err != nil && !result.Written {
		return dispatch()
	}
	return result
}

type greedyRetryRunner struct{}

func (greedyRetryRunner) Run(_ *state.RelayContext, _ state.Attempt, dispatch func() state.AttemptResult) state.AttemptResult {
	_ = dispatch()
	return dispatch()
}

type trackingGate struct {
	err      error
	acquires int
	releases int
}

func (g *trackingGate) AcquireRequest(*state.RelayContext) (state.RateLease, error) {
	return &trackingLease{}, nil
}

func (g *trackingGate) AcquireAttempt(*state.RelayContext, state.Attempt) (state.RateLease, error) {
	g.acquires++
	if g.err != nil {
		return nil, g.err
	}
	return &trackingLease{release: func() { g.releases++ }}, nil
}

type trackingLease struct{ release func() }

func (l *trackingLease) Release() {
	if l.release != nil {
		l.release()
	}
}

func providerTestContext(t *testing.T, body app.ReplayBody) *state.RelayContext {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString("stale"))
	rctx := &state.RelayContext{
		Context: c,
		Input: state.RelayInput{
			Body: []byte(`{"model":"fallback"}`),
		},
		State: &state.RelayState{Recorder: trace.NewRecorder(false, 0)},
	}
	if body == nil {
		return rctx
	}
	resources := &state.RequestResources{}
	require.NoError(t, resources.Replace(context.Background(), fixedBodyStore{body: body}, bytes.NewReader(nil), app.BodyLimits{}))
	t.Cleanup(func() { require.NoError(t, resources.Close()) })
	rctx.Resources = resources
	return rctx
}

func providerTestAttempt(mode attemptproxy.ExecutionMode) state.Attempt {
	return state.Attempt{
		Channel:   &models.Channel{ChannelCore: models.ChannelCore{ID: 7}},
		RealModel: "gpt-4o",
		Mode:      mode,
		Source:    state.SourceAdmin,
		SourceID:  7,
	}
}

func TestProviderExecutorReopensBodyForEveryDispatch(t *testing.T) {
	body := &countingReplayBody{content: []byte(`{"model":"gpt-4o"}`)}
	rctx := providerTestContext(t, body)
	dispatcher := &bodyReadingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("first")},
		{PromptTokens: 3},
	}}
	gate := &trackingGate{}
	executor := NewProviderExecutor(dispatcher, retryOnceRunner{}, gate)

	result := executor.Execute(rctx, providerTestAttempt(attemptproxy.ModeNative))

	require.NoError(t, result.Outcome.Err)
	require.Equal(t, 2, result.Dispatches)
	require.True(t, result.ProviderDispatched)
	require.Equal(t, 2, body.openCount())
	require.Equal(t, [][]byte{body.content, body.content}, dispatcher.bodies)
	require.Equal(t, []int64{int64(len(body.content)), int64(len(body.content))}, dispatcher.contentLengths)
	require.Equal(t, []bool{true, true}, dispatcher.getBodyInstalled)
	require.Equal(t, 1, gate.acquires)
	require.Equal(t, 1, gate.releases)
}

func TestProviderExecutorReopensMultipartAudioBodyForRetry(t *testing.T) {
	var payload bytes.Buffer
	w := multipart.NewWriter(&payload)
	part, err := w.CreateFormFile("file", "clip.wav")
	require.NoError(t, err)
	_, err = part.Write([]byte{'R', 'I', 'F', 'F', 0x00, 0xff, 0x10, 0x80})
	require.NoError(t, err)
	require.NoError(t, w.WriteField("model", "whisper-1"))
	require.NoError(t, w.Close())

	body := &countingReplayBody{content: append([]byte(nil), payload.Bytes()...)}
	dispatcher := &bodyReadingDispatcher{results: []state.AttemptResult{
		{Err: errors.New("retry audio")},
		{},
	}}
	executor := NewProviderExecutor(dispatcher, retryOnceRunner{}, nil)

	result := executor.Execute(providerTestContext(t, body), providerTestAttempt(attemptproxy.ModeNative))

	require.NoError(t, result.Outcome.Err)
	require.Equal(t, 2, result.Dispatches)
	require.Equal(t, 2, body.openCount())
	require.Equal(t, [][]byte{body.content, body.content}, dispatcher.bodies)
}

func TestProviderExecutorAttemptLimiterRejectsBeforeDispatch(t *testing.T) {
	gate := &trackingGate{err: state.ErrRateLimited}
	dispatcher := &recordingDispatcher{}
	executor := NewProviderExecutor(dispatcher, retryOnceRunner{}, gate)

	result := executor.Execute(providerTestContext(t, nil), providerTestAttempt(attemptproxy.ModeNative))

	require.ErrorIs(t, result.Outcome.Err, state.ErrRateLimited)
	require.Zero(t, result.Dispatches)
	require.False(t, result.ProviderDispatched)
	require.Empty(t, dispatcher.attempts)
	require.Equal(t, 1, gate.acquires)
	require.Zero(t, gate.releases)
}

func TestProviderExecutorCanceledRequestSkipsDispatch(t *testing.T) {
	rctx := providerTestContext(t, nil)
	ctx, cancel := context.WithCancel(rctx.Context.Request.Context())
	cancel()
	rctx.Context.Request = rctx.Context.Request.WithContext(ctx)
	dispatcher := &recordingDispatcher{}
	executor := NewProviderExecutor(dispatcher, retryOnceRunner{}, nil)

	result := executor.Execute(rctx, providerTestAttempt(attemptproxy.ModeNative))

	require.ErrorIs(t, result.Outcome.Err, context.Canceled)
	require.Zero(t, result.Dispatches)
	require.False(t, result.ProviderDispatched)
	require.Empty(t, dispatcher.attempts)
}

func TestProviderExecutorWrittenResultPreventsAnotherDispatch(t *testing.T) {
	wantErr := errors.New("stream interrupted")
	dispatcher := &recordingDispatcher{result: state.AttemptResult{Written: true, Err: wantErr}}
	executor := NewProviderExecutor(dispatcher, greedyRetryRunner{}, nil)

	result := executor.Execute(providerTestContext(t, nil), providerTestAttempt(attemptproxy.ModeNative))

	require.ErrorIs(t, result.Outcome.Err, wantErr)
	require.True(t, result.Outcome.Written)
	require.Equal(t, 1, result.Dispatches)
	require.True(t, result.ProviderDispatched)
	require.Len(t, dispatcher.attempts, 1)
}

func TestProviderExecutorPassesExecutionModeToDispatcher(t *testing.T) {
	for _, mode := range []attemptproxy.ExecutionMode{
		attemptproxy.ModeNative,
		attemptproxy.ModePassthrough,
		attemptproxy.ModeLegacy,
	} {
		t.Run(string(mode), func(t *testing.T) {
			dispatcher := &recordingDispatcher{}
			executor := NewProviderExecutor(dispatcher, nil, nil)

			result := executor.Execute(providerTestContext(t, nil), providerTestAttempt(mode))

			require.NoError(t, result.Outcome.Err)
			require.Equal(t, 1, result.Dispatches)
			require.True(t, result.ProviderDispatched)
			require.Len(t, dispatcher.attempts, 1)
			require.Equal(t, mode, dispatcher.attempts[0].Mode)
		})
	}
}

func TestProviderExecutorEmptyBodyWithoutReplayResource(t *testing.T) {
	rctx := providerTestContext(t, nil)
	rctx.Input.Body = nil
	dispatcher := &bodyReadingDispatcher{results: []state.AttemptResult{{}}}
	executor := NewProviderExecutor(dispatcher, nil, nil)

	result := executor.Execute(rctx, providerTestAttempt(attemptproxy.ModeNative))

	require.NoError(t, result.Outcome.Err)
	require.Equal(t, 1, result.Dispatches)
	require.True(t, result.ProviderDispatched)
	require.Equal(t, [][]byte{{}}, dispatcher.bodies)
}

func TestProviderExecutorReplayOpenFailureSkipsDispatch(t *testing.T) {
	wantErr := errors.New("replay open failed")
	body := &countingReplayBody{openErr: wantErr}
	dispatcher := &recordingDispatcher{}
	executor := NewProviderExecutor(dispatcher, nil, nil)

	result := executor.Execute(providerTestContext(t, body), providerTestAttempt(attemptproxy.ModeNative))

	require.ErrorIs(t, result.Outcome.Err, wantErr)
	require.Zero(t, result.Dispatches)
	require.False(t, result.ProviderDispatched)
	require.Empty(t, dispatcher.attempts)
	require.Equal(t, 1, body.openCount())
}

func TestProviderExecutorResetsRecorderBeforeRetryBodyOpen(t *testing.T) {
	wantErr := errors.New("second open failed")
	body := &countingReplayBody{
		content:   []byte(`{"model":"gpt-4o"}`),
		openErr:   wantErr,
		failAfter: 1,
	}
	dispatcher := &traceFailingDispatcher{}
	rctx := providerTestContext(t, body)
	executor := NewProviderExecutor(dispatcher, retryOnceRunner{}, nil)

	result := executor.Execute(rctx, providerTestAttempt(attemptproxy.ModeNative))

	require.ErrorIs(t, result.Outcome.Err, wantErr)
	require.Equal(t, 1, result.Dispatches)
	require.True(t, result.ProviderDispatched)
	require.Equal(t, 1, dispatcher.calls)
	rctx.State.Recorder.SnapshotAttempt()
	require.Equal(t, trace.StageNone, rctx.State.Recorder.Attempts()[0].FailStage)
}

func TestProviderExecutorAllowsMissingHTTPRequest(t *testing.T) {
	rctx := providerTestContext(t, nil)
	rctx.Context = nil
	dispatcher := &recordingDispatcher{}
	executor := NewProviderExecutor(dispatcher, nil, nil)

	result := executor.Execute(rctx, providerTestAttempt(attemptproxy.ModeNative))

	require.NoError(t, result.Outcome.Err)
	require.Equal(t, 1, result.Dispatches)
	require.True(t, result.ProviderDispatched)
	require.Len(t, dispatcher.attempts, 1)
}
