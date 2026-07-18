package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	pkgmetrics "github.com/VaalaCat/ai-gateway/internal/pkg/metrics"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type relayProbeLinkStub struct {
	mu       sync.Mutex
	requests []app.RelayRequest
	stream   *relayProbeStreamStub
	err      error
}

func (l *relayProbeLinkStub) OpenStream(_ context.Context, request app.RelayRequest) (app.RelayStream, error) {
	l.mu.Lock()
	l.requests = append(l.requests, request)
	l.mu.Unlock()
	if l.err != nil {
		return nil, l.err
	}
	return l.stream, nil
}

func (l *relayProbeLinkStub) requestSnapshot() []app.RelayRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]app.RelayRequest(nil), l.requests...)
}

type relayProbeStreamStub struct {
	commitState wire.CommitState
	commitErr   error
	uploadErr   error
	responseErr error
	status      int
	body        string
	blockCommit bool
	afterCopy   func()

	mu          sync.Mutex
	operations  []string
	cancelCause error
	closeCalls  int
}

func (s *relayProbeStreamStub) Commit(ctx context.Context) error {
	s.record("commit")
	if s.blockCommit {
		<-ctx.Done()
		return context.Cause(ctx)
	}
	return s.commitErr
}

func (s *relayProbeStreamStub) Upload(context.Context, io.Reader) error {
	s.record("upload")
	return s.uploadErr
}

func (s *relayProbeStreamStub) CopyResponse(_ context.Context, dst http.ResponseWriter) error {
	s.record("response")
	if s.responseErr != nil {
		return s.responseErr
	}
	status := s.status
	if status == 0 {
		status = http.StatusOK
	}
	dst.WriteHeader(status)
	_, err := io.WriteString(dst, s.body)
	if s.afterCopy != nil {
		s.afterCopy()
	}
	return err
}

func (s *relayProbeStreamStub) CommitState() wire.CommitState { return s.commitState }

func (s *relayProbeStreamStub) Cancel(cause error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, "cancel")
	s.cancelCause = cause
}

func (s *relayProbeStreamStub) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, "close")
	s.closeCalls++
	return nil
}

func (s *relayProbeStreamStub) record(operation string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.operations = append(s.operations, operation)
}

func (s *relayProbeStreamStub) snapshot() ([]string, error, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.operations...), s.cancelCause, s.closeCalls
}

type relayProbeResetError struct{ code string }

func (e relayProbeResetError) Error() string     { return "relay reset" }
func (e relayProbeResetError) ResetCode() string { return e.code }

type relayProbeMetricStub struct {
	paths   []pkgmetrics.PathKind
	results []pkgmetrics.ProbeResult
}

func (m *relayProbeMetricStub) IncConnectivityProbe(path pkgmetrics.PathKind, result pkgmetrics.ProbeResult) {
	m.paths = append(m.paths, path)
	m.results = append(m.results, result)
}

func TestRelayProbeRecordsOneBoundedMetricPerResult(t *testing.T) {
	metric := &relayProbeMetricStub{}
	prober := NewRelayProber(RelayProberOptions{
		Link: &relayProbeLinkStub{stream: &relayProbeStreamStub{
			commitState: wire.Committed, body: `{"status":"ok"}`,
		}},
		RelayGeneration: func() uint64 { return 11 }, Metrics: metric,
	})
	prober.Probe(t.Context(), relayProbeTargetForTest(11, 22))
	prober.Probe(t.Context(), protocol.RelayProbeTarget{})
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	NewRelayProber(RelayProberOptions{
		Link: &relayProbeLinkStub{stream: &relayProbeStreamStub{
			commitState: wire.PreCommit, blockCommit: true,
		}},
		RelayGeneration: func() uint64 { return 11 }, Metrics: metric,
	}).Probe(cancelled, relayProbeTargetForTest(11, 22))

	require.Equal(t, []pkgmetrics.PathKind{
		pkgmetrics.PathRelay, pkgmetrics.PathRelay, pkgmetrics.PathRelay,
	}, metric.paths)
	require.Equal(t, []pkgmetrics.ProbeResult{
		pkgmetrics.ProbeReachable, pkgmetrics.ProbeInvalid, pkgmetrics.ProbeCancelled,
	}, metric.results)
}

func TestRelayProbeUsesEndToEndPingAndAcceptsMasterRole(t *testing.T) {
	stream := &relayProbeStreamStub{
		commitState: wire.Committed,
		body:        `{"status":"ok","role":"master"}`,
	}
	link := &relayProbeLinkStub{stream: stream}
	result := NewRelayProber(RelayProberOptions{
		Link: link, RelayGeneration: func() uint64 { return 11 },
	}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

	require.Equal(t, protocol.RelayProbeReachable, result.State)
	require.Empty(t, result.ReasonCode)
	require.Equal(t, protocol.RelayProbeStageResponse, result.Stage)
	require.GreaterOrEqual(t, result.LatencyMS, int64(0))
	require.Positive(t, result.CheckedAt)

	requests := link.requestSnapshot()
	require.Len(t, requests, 1)
	require.Equal(t, "agent-b", requests[0].TargetAgentID)
	require.Equal(t, http.MethodGet, requests[0].Method)
	require.Equal(t, "/ping", requests[0].Path)
	require.Equal(t, wire.StreamPurposeConnectivityProbe, requests[0].Purpose)
	require.Empty(t, requests[0].Header)
	require.Zero(t, requests[0].BodyLength)
	require.Positive(t, requests[0].Remaining)
	require.LessOrEqual(t, requests[0].Remaining, defaultRelayProbeTimeout)
	operations, cancelCause, closeCalls := stream.snapshot()
	require.Equal(t, []string{"commit", "upload", "response", "close"}, operations)
	require.NoError(t, cancelCause)
	require.Equal(t, 1, closeCalls)
}

func TestRelayProbeRejectsHTTPAndInvalidBodies(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		reason string
	}{
		{name: "http status", status: http.StatusServiceUnavailable, body: `{"status":"ok"}`, reason: consts.RouteErrorRelayProbeHTTPStatus},
		{name: "malformed", body: `{`, reason: consts.RouteErrorRelayProbeInvalidResponse},
		{name: "wrong status", body: `{"status":"degraded"}`, reason: consts.RouteErrorRelayProbeInvalidResponse},
		{name: "empty", body: "", reason: consts.RouteErrorRelayProbeInvalidResponse},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stream := &relayProbeStreamStub{commitState: wire.Committed, status: test.status, body: test.body}
			result := NewRelayProber(RelayProberOptions{
				Link: &relayProbeLinkStub{stream: stream}, RelayGeneration: func() uint64 { return 11 },
			}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

			require.Equal(t, protocol.RelayProbeUnreachable, result.State)
			require.Equal(t, protocol.RelayProbeStageResponse, result.Stage)
			require.Equal(t, test.reason, result.ReasonCode)
			operations, cancelCause, closeCalls := stream.snapshot()
			require.Equal(t, []string{"commit", "upload", "response", "close"}, operations)
			require.NoError(t, cancelCause)
			require.Equal(t, 1, closeCalls)
		})
	}
}

func TestRelayProbeBoundsResponseBodyAndCancelsStream(t *testing.T) {
	stream := &relayProbeStreamStub{commitState: wire.Committed, body: strings.Repeat("x", relayProbeBodyLimit+1)}
	result := NewRelayProber(RelayProberOptions{
		Link: &relayProbeLinkStub{stream: stream}, RelayGeneration: func() uint64 { return 11 },
	}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

	require.Equal(t, protocol.RelayProbeUnreachable, result.State)
	require.Equal(t, protocol.RelayProbeStageResponse, result.Stage)
	require.Equal(t, consts.RouteErrorRelayProbeBodyTooLarge, result.ReasonCode)
	operations, cancelCause, closeCalls := stream.snapshot()
	require.Equal(t, []string{"commit", "upload", "response", "cancel", "close"}, operations)
	require.ErrorIs(t, cancelCause, errRelayProbeBodyTooLarge)
	require.Equal(t, 1, closeCalls)
}

func TestRelayProbeRejectsInvalidOrStaleGenerationWithoutOpeningStream(t *testing.T) {
	tests := []struct {
		name       string
		generation uint64
		target     protocol.RelayProbeTarget
		reason     string
	}{
		{name: "empty target", generation: 11, target: protocol.RelayProbeTarget{SourceRelayGeneration: 11, TargetRelayGeneration: 22}, reason: consts.RouteErrorRelayProbeInvalidResult},
		{name: "zero source generation", generation: 11, target: relayProbeTargetForTest(0, 22), reason: consts.RouteErrorRelayProbeInvalidResult},
		{name: "zero target generation", generation: 11, target: relayProbeTargetForTest(11, 0), reason: consts.RouteErrorRelayProbeInvalidResult},
		{name: "stale source generation", generation: 12, target: relayProbeTargetForTest(11, 22)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link := &relayProbeLinkStub{stream: &relayProbeStreamStub{commitState: wire.Committed, body: `{"status":"ok"}`}}
			result := NewRelayProber(RelayProberOptions{
				Link: link, RelayGeneration: func() uint64 { return test.generation },
			}).Probe(t.Context(), test.target)

			require.Equal(t, protocol.RelayProbeUnknown, result.State)
			require.Equal(t, test.reason, result.ReasonCode)
			require.Empty(t, link.requestSnapshot())
		})
	}
}

func TestRelayProbeNilReceiverReturnsInvalidResult(t *testing.T) {
	var prober *RelayProber
	result := prober.Probe(t.Context(), relayProbeTargetForTest(11, 22))

	require.Equal(t, "agent-b", result.TargetAgentID)
	require.Equal(t, protocol.RelayProbeUnknown, result.State)
	require.Equal(t, consts.RouteErrorRelayProbeInvalidResult, result.ReasonCode)
	require.NotZero(t, result.CheckedAt)
}

func TestRelayProbeDiscardsResultWhenSourceGenerationChanges(t *testing.T) {
	var generation atomic.Uint64
	generation.Store(11)
	stream := &relayProbeStreamStub{
		commitState: wire.Committed,
		body:        `{"status":"ok"}`,
		afterCopy:   func() { generation.Store(12) },
	}
	result := NewRelayProber(RelayProberOptions{
		Link: &relayProbeLinkStub{stream: stream}, RelayGeneration: generation.Load,
	}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

	require.Equal(t, protocol.RelayProbeUnknown, result.State)
	require.Empty(t, result.ReasonCode)
	_, _, closeCalls := stream.snapshot()
	require.Equal(t, 1, closeCalls)
}

func TestRelayProbeClassifiesStageFailuresAndAlwaysClosesOpenedStream(t *testing.T) {
	tests := []struct {
		name        string
		linkError   error
		stream      *relayProbeStreamStub
		state       protocol.RelayProbeState
		stage       protocol.RelayProbeStage
		reason      string
		opened      bool
		wantOps     []string
		cancelCause error
	}{
		{name: "open stable reset", linkError: relayProbeResetError{code: consts.RouteErrorRelayOverloaded}, state: protocol.RelayProbeUnavailable, stage: protocol.RelayProbeStageOpen, reason: consts.RouteErrorRelayOverloaded},
		{name: "open private reset", linkError: relayProbeResetError{code: "private_detail"}, state: protocol.RelayProbeUnavailable, stage: protocol.RelayProbeStageOpen, reason: consts.RouteErrorRelayProtocol},
		{name: "commit precommit", stream: &relayProbeStreamStub{commitState: wire.PreCommit, commitErr: errors.New("commit failed")}, state: protocol.RelayProbeUnavailable, stage: protocol.RelayProbeStageCommit, reason: consts.RouteErrorRelayNotReady, opened: true, wantOps: []string{"commit", "cancel", "close"}},
		{name: "commit uncertain", stream: &relayProbeStreamStub{commitState: wire.CommitUncertain, commitErr: errors.New("commit failed")}, state: protocol.RelayProbeUnavailable, stage: protocol.RelayProbeStageCommit, reason: consts.RouteErrorRelayCommitUncertain, opened: true, wantOps: []string{"commit", "cancel", "close"}},
		{name: "upload", stream: &relayProbeStreamStub{commitState: wire.Committed, uploadErr: errors.New("upload failed")}, state: protocol.RelayProbeUnreachable, stage: protocol.RelayProbeStageCommit, reason: consts.RouteErrorRelayCommitUncertain, opened: true, wantOps: []string{"commit", "upload", "cancel", "close"}},
		{name: "response", stream: &relayProbeStreamStub{commitState: wire.Committed, responseErr: errors.New("response failed")}, state: protocol.RelayProbeUnreachable, stage: protocol.RelayProbeStageResponse, reason: consts.RouteErrorRelayResponseInterrupted, opened: true, wantOps: []string{"commit", "upload", "response", "cancel", "close"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			link := &relayProbeLinkStub{stream: test.stream, err: test.linkError}
			result := NewRelayProber(RelayProberOptions{
				Link: link, RelayGeneration: func() uint64 { return 11 },
			}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

			require.Equal(t, test.state, result.State)
			require.Equal(t, test.stage, result.Stage)
			require.Equal(t, test.reason, result.ReasonCode)
			require.Len(t, link.requestSnapshot(), 1)
			if !test.opened {
				return
			}
			operations, cancelCause, closeCalls := test.stream.snapshot()
			require.Equal(t, test.wantOps, operations)
			require.Error(t, cancelCause)
			require.Equal(t, 1, closeCalls)
		})
	}
}

func TestRelayProbeHonorsCancellationAndTimeout(t *testing.T) {
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		stream := &relayProbeStreamStub{commitState: wire.PreCommit, blockCommit: true}
		resultC := make(chan protocol.RelayProbeResult, 1)
		go func() {
			resultC <- NewRelayProber(RelayProberOptions{
				Link: &relayProbeLinkStub{stream: stream}, RelayGeneration: func() uint64 { return 11 },
			}).Probe(ctx, relayProbeTargetForTest(11, 22))
		}()
		require.Eventually(t, func() bool {
			operations, _, _ := stream.snapshot()
			return len(operations) == 1
		}, time.Second, time.Millisecond)
		cancel()
		select {
		case result := <-resultC:
			require.Equal(t, protocol.RelayProbeUnknown, result.State)
			require.Equal(t, consts.RouteErrorRequestCancelled, result.ReasonCode)
		case <-time.After(time.Second):
			t.Fatal("relay probe ignored context cancellation")
		}
	})

	t.Run("timeout", func(t *testing.T) {
		stream := &relayProbeStreamStub{commitState: wire.PreCommit, blockCommit: true}
		result := NewRelayProber(RelayProberOptions{
			Link: &relayProbeLinkStub{stream: stream}, RelayGeneration: func() uint64 { return 11 }, Timeout: 10 * time.Millisecond,
		}).Probe(t.Context(), relayProbeTargetForTest(11, 22))

		require.Equal(t, protocol.RelayProbeUnavailable, result.State)
		require.Equal(t, consts.RouteErrorRequestDeadline, result.ReasonCode)
		operations, cancelCause, closeCalls := stream.snapshot()
		require.Equal(t, []string{"commit", "cancel", "close"}, operations)
		require.Error(t, cancelCause)
		require.Equal(t, 1, closeCalls)
	})
}

func TestRelayProbeHandlesUnavailableLinkAndInvalidRPCInput(t *testing.T) {
	result := NewRelayProber(RelayProberOptions{
		RelayGeneration: func() uint64 { return 11 },
	}).Probe(t.Context(), relayProbeTargetForTest(11, 22))
	require.Equal(t, protocol.RelayProbeUnavailable, result.State)
	require.Equal(t, protocol.RelayProbeStageOpen, result.Stage)
	require.Equal(t, consts.RouteErrorRelayNotReady, result.ReasonCode)

	_, err := HandleRelayProbe(nil, json.RawMessage(`{}`), NewRelayProber(RelayProberOptions{}))
	require.ErrorContains(t, err, "nil context")
	_, err = HandleRelayProbe(t.Context(), json.RawMessage(`{}`), nil)
	require.ErrorContains(t, err, "prober is required")
	_, err = HandleRelayProbe(t.Context(), json.RawMessage(`{`), NewRelayProber(RelayProberOptions{}))
	require.ErrorContains(t, err, "invalid relay probe params")
}

func relayProbeTargetForTest(sourceGeneration, targetGeneration uint64) protocol.RelayProbeTarget {
	return protocol.RelayProbeTarget{
		TargetAgentID:         "agent-b",
		SourceRelayGeneration: sourceGeneration,
		TargetRelayGeneration: targetGeneration,
	}
}
