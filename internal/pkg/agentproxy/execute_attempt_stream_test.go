package agentproxy

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

// replayBody returns an app.ReplayBody backed by an in-memory buffer. Direct and
// Relay share one execution path, so the stub is intentionally minimal.
func replayBody(data string) app.ReplayBody {
	return &stubReplayBody{data: []byte(data)}
}

type stubReplayBody struct {
	data    []byte
	openErr error
}

func (b *stubReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *stubReplayBody) Open() (io.ReadCloser, error) {
	if b.openErr != nil {
		return nil, b.openErr
	}
	return io.NopCloser(bytes.NewReader(b.data)), nil
}
func (b *stubReplayBody) Bytes(int64) ([]byte, error) { return b.data, nil }
func (b *stubReplayBody) Close() error                { return nil }

// attemptStreamStub records the ordered lifecycle calls made by
// executeAttemptStream so both Direct and Relay can be proven identical.
type attemptStreamStub struct {
	commit    tunnel.CommitState
	result    attemptwire.AttemptProxyResult
	commitErr error
	uploadErr error
	copyErr   error
	copyHook  func()
	calls     []string
	uploaded  string
}

func (s *attemptStreamStub) Commit(context.Context) error {
	s.calls = append(s.calls, "commit")
	return s.commitErr
}

func (s *attemptStreamStub) Upload(_ context.Context, src io.Reader) error {
	s.calls = append(s.calls, "upload")
	body, _ := io.ReadAll(src)
	s.uploaded = string(body)
	return s.uploadErr
}

func (s *attemptStreamStub) CopyAttemptResponse(_ context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	s.calls = append(s.calls, "copy")
	dst.WriteHeader(200)
	_, _ = dst.Write([]byte("ok"))
	if s.copyHook != nil {
		s.copyHook()
	}
	return s.result, s.copyErr
}

func (s *attemptStreamStub) CommitState() tunnel.CommitState { return s.commit }
func (s *attemptStreamStub) Cancel(error)                    {}
func (s *attemptStreamStub) Close() error                    { return nil }

func TestDirectAndRelayShareAttemptStreamExecution(t *testing.T) {
	for _, path := range []string{"direct", "relay"} {
		t.Run(path, func(t *testing.T) {
			stream := &attemptStreamStub{
				commit: tunnel.Committed,
				result: attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded},
			}
			outcome := executeAttemptStream(t.Context(), stream, replayBody("body"), httptest.NewRecorder())
			require.NoError(t, outcome.Err)
			require.Equal(t, []string{"commit", "upload", "copy"}, stream.calls)
			require.Equal(t, "body", stream.uploaded)
			require.NotNil(t, outcome.AttemptResult)
			require.Equal(t, attemptwire.ResultSucceeded, outcome.AttemptResult.Kind)
		})
	}
}

func TestExecuteAttemptStreamLeavesMissingResultNilOnCancellation(t *testing.T) {
	stream := &attemptStreamStub{commit: tunnel.Committed, copyErr: context.Canceled}

	outcome := executeAttemptStream(t.Context(), stream, replayBody("body"), httptest.NewRecorder())

	require.Nil(t, outcome.AttemptResult)
	require.ErrorIs(t, outcome.Err, context.Canceled)
	require.NotErrorIs(t, outcome.Err, attemptwire.ErrInvalidContract)
	require.Equal(t, CodeRequestCancelled, outcome.Code)
}

func TestExecuteAttemptStreamKeepsCompletedResultWhenCallerCancelsInsideCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultSucceeded}
	stream := &attemptStreamStub{commit: tunnel.Committed, result: want, copyHook: cancel}

	outcome := executeAttemptStream(ctx, stream, replayBody("body"), httptest.NewRecorder())

	require.NoError(t, outcome.Err)
	require.NotNil(t, outcome.AttemptResult)
	require.Equal(t, want, *outcome.AttemptResult)
	require.Equal(t, "response", outcome.Stage)
	require.Empty(t, outcome.Code)
}

func TestExecuteAttemptStreamPreservesCompletedResultWithCopyCancellation(t *testing.T) {
	want := attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true}
	stream := &attemptStreamStub{
		commit: tunnel.Committed, result: want, copyErr: context.Canceled,
	}

	outcome := executeAttemptStream(t.Context(), stream, replayBody("body"), httptest.NewRecorder())

	require.ErrorIs(t, outcome.Err, context.Canceled)
	require.NotNil(t, outcome.AttemptResult)
	require.Equal(t, want, *outcome.AttemptResult)
	require.Equal(t, CodeRequestCancelled, outcome.Code)
}
