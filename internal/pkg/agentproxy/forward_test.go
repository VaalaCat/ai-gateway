package agentproxy_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

type directReplayBody struct {
	data       []byte
	openErr    error
	readErr    error
	opens      atomic.Int64
	closes     atomic.Int64
	bodyCloses atomic.Int64
}

type nilReaderReplayBody struct{ directReplayBody }

func (*nilReaderReplayBody) Open() (io.ReadCloser, error) { return nil, nil }

func (b *directReplayBody) Size() int64 { return int64(len(b.data)) }
func (b *directReplayBody) Open() (io.ReadCloser, error) {
	b.opens.Add(1)
	if b.openErr != nil {
		return nil, b.openErr
	}
	reader := io.Reader(bytes.NewReader(b.data))
	if b.readErr != nil {
		reader = io.MultiReader(reader, errorReader{err: b.readErr})
	}
	return &countedReadCloser{Reader: reader, closed: &b.closes}, nil
}
func (b *directReplayBody) Bytes(limit int64) ([]byte, error) {
	if int64(len(b.data)) > limit {
		return nil, errors.New("limit")
	}
	return append([]byte(nil), b.data...), nil
}
func (b *directReplayBody) Close() error { b.bodyCloses.Add(1); return nil }

type countedReadCloser struct {
	io.Reader
	closed *atomic.Int64
}

func (r *countedReadCloser) Close() error { r.closed.Add(1); return nil }

type blockingDirectReadCloser struct {
	started chan struct{}
	closed  chan struct{}
	once    sync.Once
}

func newBlockingDirectReadCloser() *blockingDirectReadCloser {
	return &blockingDirectReadCloser{started: make(chan struct{}), closed: make(chan struct{})}
}

func (r *blockingDirectReadCloser) Read([]byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	<-r.closed
	return 0, context.Canceled
}

func (r *blockingDirectReadCloser) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

type blockingDirectReplayBody struct {
	directReplayBody
	reader *blockingDirectReadCloser
}

func (b *blockingDirectReplayBody) Open() (io.ReadCloser, error) {
	b.opens.Add(1)
	return b.reader, nil
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

type failingResponseWriter struct {
	header http.Header
	err    error
}

func (w *failingResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (*failingResponseWriter) WriteHeader(int) {}
func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, w.err
}

type cancellationAwareAttemptStream struct {
	helperRelayStream
	sourceClosable chan bool
}

type directAttemptStreamOpenerFunc func(
	context.Context, agentproxy.DirectSessionTarget, agentproxy.AttemptStreamRequest,
) (agentproxy.AttemptStream, error)

type directAttemptTransportFunc struct {
	open               func(context.Context, agentproxy.AttemptStreamRequest) (agentproxy.AttemptStream, error)
	addressFingerprint string
}

func (directAttemptTransportFunc) TransportIdentity() agentproxy.DirectTransportIdentity {
	return agentproxy.DirectTransportIdentity{1}
}

func (f directAttemptTransportFunc) AcquireAttemptStream(
	context.Context,
) (agentproxy.DirectAttemptStreamReservation, error) {
	return &directAttemptStreamReservationFunc{
		identity: f.TransportIdentity(), addressFingerprint: f.addressFingerprint, open: f.open,
	}, nil
}

type directAttemptStreamReservationFunc struct {
	identity           agentproxy.DirectTransportIdentity
	addressFingerprint string
	open               func(context.Context, agentproxy.AttemptStreamRequest) (agentproxy.AttemptStream, error)
	release            func()
	releaseOnce        sync.Once
}

func (r *directAttemptStreamReservationFunc) TransportIdentity() agentproxy.DirectTransportIdentity {
	return r.identity
}

func (r *directAttemptStreamReservationFunc) AddressFingerprint() string {
	return r.addressFingerprint
}

func (r *directAttemptStreamReservationFunc) OpenAttemptStream(
	ctx context.Context, request agentproxy.AttemptStreamRequest,
) (agentproxy.AttemptStream, error) {
	return r.open(ctx, request)
}

func (r *directAttemptStreamReservationFunc) Release() {
	r.releaseOnce.Do(func() {
		if r.release != nil {
			r.release()
		}
	})
}

func (f directAttemptStreamOpenerFunc) BuildDirectAttemptTransport(
	_ context.Context, target agentproxy.DirectSessionTarget,
) (agentproxy.DirectAttemptTransport, error) {
	return directAttemptTransportFunc{
		addressFingerprint: target.AddressFingerprint,
		open: func(ctx context.Context, request agentproxy.AttemptStreamRequest) (agentproxy.AttemptStream, error) {
			return f(ctx, target, request)
		},
	}, nil
}

func (f directAttemptStreamOpenerFunc) OpenAttemptStream(
	ctx context.Context, target agentproxy.DirectSessionTarget, request agentproxy.AttemptStreamRequest,
) (agentproxy.AttemptStream, error) {
	return f(ctx, target, request)
}

func (s *cancellationAwareAttemptStream) Upload(ctx context.Context, source io.Reader) error {
	*s.order = append(*s.order, "upload")
	closer, ok := source.(io.Closer)
	s.sourceClosable <- ok
	if !ok {
		return errors.New("attempt upload source is not closable")
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := source.Read(make([]byte, 1))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		return err
	case <-ctx.Done():
		if err := closer.Close(); err != nil {
			return err
		}
		return <-readDone
	}
}

func directAttemptMeta(path string) attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModePassthrough,
		},
		RequestPath: path,
	}
}

func parseURLForForwardTest(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	require.NoError(t, err)
	return parsed
}

func frozenDirectTarget(t *testing.T) agentproxy.DirectSessionTarget {
	return agentproxy.DirectSessionTarget{
		TargetAgentID: "target-a", AddressFingerprint: "fp-a",
		WebSocketURL: parseURLForForwardTest(t, "https://target-a.example:8443"),
	}
}

// directRequest builds a DirectRequest carrying forged managed headers so tests
// can prove they are stripped before the request reaches the attempt stream.
func directRequest(t *testing.T, body *directReplayBody) agentproxy.DirectRequest {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "http://source/v1/messages?q=1", nil)
	request.Header.Set("Authorization", "Bearer original")
	request.Header.Set(consts.HeaderXAgentID, "forged")
	request.Header.Set(consts.HeaderXAgentForwardTicket, "forged-ticket")
	request.Header.Set(consts.HeaderXAgentRouteID, "forged-route")
	request.Header.Set("Connection", "keep-alive, X-Hop")
	request.Header.Set("X-Hop", "secret")
	request.Header.Set("Content-Length", "999")
	request.ContentLength = 999
	return agentproxy.DirectRequest{
		Target: frozenDirectTarget(t), RouteID: 7, RequestID: "request-a", Hop: 1,
		Request: request, Body: body, Attempt: directAttemptMeta("/v1/messages"),
	}
}

// fakeDirectOpener is the frozen-target attempt stream opener the pool provides.
type fakeDirectOpener struct {
	target                   agentproxy.DirectSessionTarget
	request                  agentproxy.AttemptStreamRequest
	stream                   *helperRelayStream
	err                      error
	buildErr                 error
	buildNil                 bool
	acquireErr               error
	returnReservationOnError bool
	afterAcquire             func()
	identity                 agentproxy.DirectTransportIdentity
	actualIdentity           agentproxy.DirectTransportIdentity
	actualAddressFingerprint string
	omitActualIdentity       bool
	panicOpen                bool
	calls                    atomic.Int64
	releases                 atomic.Int64
}

func (o *fakeDirectOpener) BuildDirectAttemptTransport(
	_ context.Context, target agentproxy.DirectSessionTarget,
) (agentproxy.DirectAttemptTransport, error) {
	if o.buildErr != nil {
		return nil, o.buildErr
	}
	if o.buildNil {
		return nil, nil
	}
	o.target = target
	identity := o.identity
	if identity == (agentproxy.DirectTransportIdentity{}) {
		identity = agentproxy.DirectTransportIdentity{1}
	}
	actualIdentity := o.actualIdentity
	actualAddressFingerprint := o.actualAddressFingerprint
	if !o.omitActualIdentity {
		if actualIdentity == (agentproxy.DirectTransportIdentity{}) {
			actualIdentity = identity
		}
		if actualAddressFingerprint == "" {
			actualAddressFingerprint = target.AddressFingerprint
		}
	}
	return &fakeDirectTransport{
		opener: o, target: target, identity: identity,
		actualIdentity: actualIdentity, actualAddressFingerprint: actualAddressFingerprint,
	}, nil
}

type fakeDirectTransport struct {
	opener                   *fakeDirectOpener
	target                   agentproxy.DirectSessionTarget
	identity                 agentproxy.DirectTransportIdentity
	actualIdentity           agentproxy.DirectTransportIdentity
	actualAddressFingerprint string
}

func (t *fakeDirectTransport) TransportIdentity() agentproxy.DirectTransportIdentity {
	return t.identity
}

func (t *fakeDirectTransport) AcquireAttemptStream(
	context.Context,
) (agentproxy.DirectAttemptStreamReservation, error) {
	reservation := &fakeDirectReservation{
		opener: t.opener, target: t.target,
		identity: t.actualIdentity, addressFingerprint: t.actualAddressFingerprint,
	}
	if t.opener.afterAcquire != nil {
		t.opener.afterAcquire()
	}
	if t.opener.acquireErr != nil && !t.opener.returnReservationOnError {
		return nil, t.opener.acquireErr
	}
	return reservation, t.opener.acquireErr
}

type fakeDirectReservation struct {
	opener             *fakeDirectOpener
	target             agentproxy.DirectSessionTarget
	identity           agentproxy.DirectTransportIdentity
	addressFingerprint string
	releaseOnce        sync.Once
}

func (r *fakeDirectReservation) TransportIdentity() agentproxy.DirectTransportIdentity {
	return r.identity
}

func (r *fakeDirectReservation) AddressFingerprint() string {
	return r.addressFingerprint
}

func (r *fakeDirectReservation) OpenAttemptStream(
	ctx context.Context, req agentproxy.AttemptStreamRequest,
) (agentproxy.AttemptStream, error) {
	if r.opener.panicOpen {
		panic("fake direct reservation open panic")
	}
	return r.opener.OpenAttemptStream(ctx, r.target, req)
}

func (r *fakeDirectReservation) Release() {
	r.releaseOnce.Do(func() { r.opener.releases.Add(1) })
}

func (o *fakeDirectOpener) OpenAttemptStream(
	_ context.Context, target agentproxy.DirectSessionTarget, req agentproxy.AttemptStreamRequest,
) (agentproxy.AttemptStream, error) {
	o.calls.Add(1)
	o.target = target
	o.request = req
	if o.err != nil {
		return nil, o.err
	}
	return o.stream, nil
}

// fakeOpenError implements the directOpenFailure interface for classification.
type fakeOpenError struct {
	stage     string
	code      string
	counts    bool
	countOnce bool
	claimed   atomic.Bool
}

type policyResetError string

func (e policyResetError) Error() string     { return string(e) }
func (e policyResetError) ResetCode() string { return string(e) }

func (e *fakeOpenError) Error() string      { return "direct open failed: " + e.code }
func (e *fakeOpenError) Stage() string      { return e.stage }
func (e *fakeOpenError) ReasonCode() string { return e.code }
func (e *fakeOpenError) CountsForCircuit() bool {
	return e.counts && (!e.countOnce || e.claimed.CompareAndSwap(false, true))
}

func ownedDirectForTest(t *testing.T, builder agentproxy.DirectAttemptTransportBuilder) *agentproxy.DirectForwarder {
	t.Helper()
	direct := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: builder})
	t.Cleanup(func() { require.NoError(t, direct.Close(context.Background())) })
	return direct
}

func TestDirectForwardOpensStreamWithoutPerRequestCredentials(t *testing.T) {
	order := make([]string, 0, 5)
	opener := &fakeDirectOpener{stream: &helperRelayStream{order: &order, commit: tunnel.Committed}}
	f := ownedDirectForTest(t, opener)
	body := &directReplayBody{data: []byte("fresh")}
	recorder := httptest.NewRecorder()

	outcome := f.Forward(context.Background(), directRequest(t, body), recorder)

	require.NoError(t, outcome.Err)
	require.Equal(t, tunnel.Committed, outcome.Commit)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, int64(1), opener.calls.Load())
	require.Equal(t, int64(1), opener.releases.Load())
	require.Equal(t, "target-a", opener.target.TargetAgentID)
	require.Equal(t, http.MethodPost, opener.request.Method)
	require.Equal(t, attemptwire.EndpointPath, opener.request.Path)
	require.Equal(t, uint8(1), opener.request.Hop)
	require.Equal(t, directAttemptMeta("/v1/messages"), opener.request.Attempt)
	require.Equal(t, "Bearer original", opener.request.Header.Get("Authorization"))
	require.Empty(t, opener.request.Header.Get(consts.HeaderXAgentForwardTicket), "forward ticket must not ride the request")
	require.Empty(t, opener.request.Header.Get(consts.HeaderXAgentRouteID))
	require.Empty(t, opener.request.Header.Get(consts.HeaderXAgentID))
	require.Empty(t, opener.request.Header.Get("X-Hop"), "hop-by-hop headers must be stripped")
	require.Equal(t, []string{"commit", "upload", "copy", "close"}, order)
	require.Equal(t, "fresh", opener.stream.uploaded)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Zero(t, body.bodyCloses.Load(), "Forward must not close shared ReplayBody")
}

func TestDirectForwardOpenFailureStaysPreCommitForRelayFallback(t *testing.T) {
	opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: "direct_connect", counts: true}}
	f := ownedDirectForTest(t, opener)

	outcome := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())

	require.Error(t, outcome.Err)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.False(t, outcome.ResponseStarted)
	require.Nil(t, outcome.AttemptResult)
}

func TestDirectForwardTransportBuildFailureStaysOutsideCircuit(t *testing.T) {
	builder := &fakeDirectOpener{buildErr: &fakeOpenError{stage: "credentials", code: "unavailable", counts: true}}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: builder, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	for range 3 {
		outcome := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.Equal(t, agentproxy.CodeDirectAuthUnavailable, outcome.Code)
	}
	require.Zero(t, forwarder.ResourceCount(), "local build failures must not allocate circuit state")
	require.Zero(t, builder.calls.Load())

	builder.buildErr = nil
	builder.err = &fakeOpenError{stage: "dial", code: "failed", counts: true}
	failed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
	blocked := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code)
}

func TestDirectForwardAcquireFailureUsesPlannedCircuitAndReleasesReservation(t *testing.T) {
	builder := &fakeDirectOpener{
		identity: agentproxy.DirectTransportIdentity{2}, actualIdentity: agentproxy.DirectTransportIdentity{1},
		acquireErr: &fakeOpenError{stage: "dial", code: "failed", counts: true}, returnReservationOnError: true,
	}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: builder, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	failed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
	require.Zero(t, builder.calls.Load())
	require.Equal(t, int64(1), builder.releases.Load())

	plannedBlocked := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, plannedBlocked.Code)
	builder.identity = agentproxy.DirectTransportIdentity{1}
	otherPlannedAllowed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, otherPlannedAllowed.Code)
	require.Equal(t, int64(2), builder.releases.Load())
}

func TestDirectForwardRejectsMissingOrNilTransportBuilder(t *testing.T) {
	for _, test := range []struct {
		name    string
		builder agentproxy.DirectAttemptTransportBuilder
	}{
		{name: "missing builder"},
		{name: "nil transport", builder: &fakeDirectOpener{buildNil: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: test.builder})
			t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

			outcome := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
			require.Equal(t, agentproxy.CodeDirectDisabled, outcome.Code)
			require.Zero(t, forwarder.ResourceCount())
		})
	}
}

func TestDirectForwardFallbackOutcomeUpdatesActualTransportCircuit(t *testing.T) {
	transportErr := errors.New("fallback transport interrupted")
	order := make([]string, 0, 8)
	builder := &fakeDirectOpener{
		identity:       agentproxy.DirectTransportIdentity{2},
		actualIdentity: agentproxy.DirectTransportIdentity{1},
		stream: &helperRelayStream{
			order: &order, commit: tunnel.CommitUncertain, commitErr: transportErr,
		},
	}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: builder, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	fallback := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.ErrorIs(t, fallback.Err, transportErr)

	builder.identity = agentproxy.DirectTransportIdentity{1}
	builder.actualIdentity = agentproxy.DirectTransportIdentity{1}
	builder.err = &fakeOpenError{stage: "dial", code: "failed", counts: true}
	actualBlocked := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, actualBlocked.Code, "fallback outcome must update actual identity A")

	builder.identity = agentproxy.DirectTransportIdentity{2}
	builder.actualIdentity = agentproxy.DirectTransportIdentity{2}
	plannedAllowed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, plannedAllowed.Code, "planned identity B permit must be canceled after fallback to A")
}

func TestDirectForwardRejectsFallbackWhenActualTransportCircuitIsOpen(t *testing.T) {
	order := make([]string, 0, 8)
	builder := &fakeDirectOpener{
		identity: agentproxy.DirectTransportIdentity{1},
		err:      &fakeOpenError{stage: "dial", code: "failed", counts: true},
	}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: builder, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	failed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)

	builder.identity = agentproxy.DirectTransportIdentity{2}
	builder.actualIdentity = agentproxy.DirectTransportIdentity{1}
	builder.err = nil
	builder.stream = &helperRelayStream{order: &order, commit: tunnel.Committed}
	fallback := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, fallback.Code)
	require.Empty(t, order, "denied fallback must not open a stream")
	require.Equal(t, int64(1), builder.calls.Load(), "actual OPEN circuit must reject before OpenAttemptStream")
	require.Equal(t, int64(2), builder.releases.Load())
}

func TestDirectForwardFallbackOpenFailureUpdatesActualTransportCircuit(t *testing.T) {
	builder := &fakeDirectOpener{
		identity:       agentproxy.DirectTransportIdentity{2},
		actualIdentity: agentproxy.DirectTransportIdentity{1},
		err:            &fakeOpenError{stage: "stream", code: "failed", counts: true},
	}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: builder, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	fallbackFailed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, fallbackFailed.Code)

	builder.identity = agentproxy.DirectTransportIdentity{1}
	builder.actualIdentity = agentproxy.DirectTransportIdentity{1}
	actualBlocked := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectCircuitOpen, actualBlocked.Code)

	builder.identity = agentproxy.DirectTransportIdentity{2}
	builder.actualIdentity = agentproxy.DirectTransportIdentity{2}
	plannedAllowed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, plannedAllowed.Code)
	require.Equal(t, int64(2), builder.releases.Load())
}

func TestDirectForwardRejectsIncompleteActualTransportIdentityBeforeOpen(t *testing.T) {
	for _, test := range []struct {
		name               string
		identity           agentproxy.DirectTransportIdentity
		addressFingerprint string
	}{
		{name: "missing identity and address"},
		{name: "zero identity with address", addressFingerprint: "fp-a"},
		{name: "identity without address", identity: agentproxy.DirectTransportIdentity{2}},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := make([]string, 0, 4)
			body := &directReplayBody{data: []byte("must-not-open")}
			builder := &fakeDirectOpener{
				identity: agentproxy.DirectTransportIdentity{2}, omitActualIdentity: true,
				actualIdentity: test.identity, actualAddressFingerprint: test.addressFingerprint,
				stream: &helperRelayStream{order: &order, commit: tunnel.Committed},
			}
			forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: builder})
			t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

			outcome := forwarder.Forward(t.Context(), directRequest(t, body), httptest.NewRecorder())
			require.Equal(t, agentproxy.CodeDirectDisabled, outcome.Code)
			require.Error(t, outcome.Err)
			require.Zero(t, builder.calls.Load(), "identity validation must precede OpenAttemptStream")
			require.Equal(t, int64(1), builder.releases.Load())
			require.Empty(t, order)
			require.Zero(t, body.opens.Load(), "identity validation must precede request body and COMMIT")
			require.Zero(t, forwarder.ResourceCount(), "planned circuit permit must be released")
		})
	}
}

func TestDirectForwardCancellationAfterAcquireReleasesWithoutOpen(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	builder := &fakeDirectOpener{afterAcquire: cancel}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: builder})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	outcome := forwarder.Forward(ctx, directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.ErrorIs(t, outcome.Err, context.Canceled)
	require.Zero(t, builder.calls.Load())
	require.Equal(t, int64(1), builder.releases.Load())
	require.Zero(t, forwarder.ResourceCount())
}

func TestDirectForwardOpenPanicReleasesReservationAndCircuitPermit(t *testing.T) {
	builder := &fakeDirectOpener{panicOpen: true}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: builder})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	func() {
		defer func() { require.Equal(t, "fake direct reservation open panic", recover()) }()
		forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	}()
	require.Equal(t, int64(1), builder.releases.Load())
	require.Zero(t, forwarder.ResourceCount(), "panic must release the active circuit permit")
}

func TestDirectForwardCircuitOpensOnlyForCountingOpenFailures(t *testing.T) {
	t.Run("one-shot classification keeps connect code and opens circuit", func(t *testing.T) {
		opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: "failed", counts: true, countOnce: true}}
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
		})
		t.Cleanup(func() { require.NoError(t, f.Close(context.Background())) })

		failed := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
		blocked := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code)
	})

	t.Run("connection failures open the circuit", func(t *testing.T) {
		opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: "direct_connect", counts: true}}
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			Transports: opener, CircuitFailureThreshold: 3, CircuitOpenDuration: time.Minute,
		})
		t.Cleanup(func() { require.NoError(t, f.Close(context.Background())) })
		for range 3 {
			require.Error(t, f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
		}
		blocked := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code)
		f.ResetCircuit("target-a", "fp-a")
		require.NotEqual(t, agentproxy.CodeDirectCircuitOpen,
			f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Code)
	})

	t.Run("capacity and invalid target never open the circuit", func(t *testing.T) {
		opener := &fakeDirectOpener{err: &fakeOpenError{stage: "pool", code: "direct_session_capacity", counts: false}}
		f := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
			Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Minute,
		})
		t.Cleanup(func() { require.NoError(t, f.Close(context.Background())) })
		for range 3 {
			require.Error(t, f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
		}
		after := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.NotEqual(t, agentproxy.CodeDirectCircuitOpen, after.Code, "capacity rejection must not count as a circuit failure")
	})
}

func TestDirectForwardPostOpenTransportFailuresOpenCircuit(t *testing.T) {
	transportErr := errors.New("direct transport interrupted")
	for _, test := range []struct {
		name   string
		stream func(*[]string) *helperRelayStream
	}{
		{name: "commit acknowledgement", stream: func(order *[]string) *helperRelayStream {
			return &helperRelayStream{order: order, commit: tunnel.CommitUncertain, commitErr: transportErr}
		}},
		{name: "upload", stream: func(order *[]string) *helperRelayStream {
			return &helperRelayStream{order: order, commit: tunnel.Committed, uploadErr: transportErr}
		}},
		{name: "response result", stream: func(order *[]string) *helperRelayStream {
			return &helperRelayStream{order: order, commit: tunnel.Committed, copyErr: transportErr}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			order := make([]string, 0, 10)
			opener := &fakeDirectOpener{stream: test.stream(&order)}
			forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
				Transports: opener, CircuitFailureThreshold: 2, CircuitOpenDuration: time.Minute,
			})
			t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

			for range 2 {
				outcome := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
				require.ErrorIs(t, outcome.Err, transportErr)
			}
			blocked := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
			require.Equal(t, agentproxy.CodeDirectCircuitOpen, blocked.Code)
			require.Equal(t, int64(2), opener.calls.Load(), "an open circuit must not open another stream")
		})
	}
}

func TestDirectForwardPreservesBodyCloserDuringCancellation(t *testing.T) {
	order := make([]string, 0, 5)
	stream := &cancellationAwareAttemptStream{
		helperRelayStream: helperRelayStream{order: &order, commit: tunnel.Committed},
		sourceClosable:    make(chan bool, 1),
	}
	openerWithStream := directAttemptStreamOpenerFunc(func(
		context.Context, agentproxy.DirectSessionTarget, agentproxy.AttemptStreamRequest,
	) (agentproxy.AttemptStream, error) {
		return stream, nil
	})
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{Transports: openerWithStream})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	body := &blockingDirectReplayBody{reader: newBlockingDirectReadCloser()}
	request := directRequest(t, &body.directReplayBody)
	request.Body = body
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan agentproxy.AttemptTransportOutcome, 1)
	go func() { done <- forwarder.Forward(ctx, request, httptest.NewRecorder()) }()

	require.True(t, <-stream.sourceClosable, "Upload source must preserve the ReplayBody reader's Close method")
	select {
	case <-body.reader.started:
	case <-time.After(time.Second):
		t.Fatal("body read did not start")
	}
	cancel()
	select {
	case outcome := <-done:
		require.ErrorIs(t, outcome.Err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("Forward remained blocked after request cancellation")
	}
	select {
	case <-body.reader.closed:
	case <-time.After(time.Second):
		t.Fatal("request body reader was not closed on cancellation")
	}
	require.Equal(t, []string{"commit", "upload", "close"}, order)
}

func TestDirectForwardHalfOpenBodyFailureDoesNotRecoverOrCount(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	transitions := make([]agentproxy.DirectCircuitTransition, 0, 4)
	opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: agentproxy.CodeDirectConnect, counts: true}}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Second,
		Now: func() time.Time { return now },
		OnCircuitTransition: func(transition agentproxy.DirectCircuitTransition) {
			transitions = append(transitions, transition)
		},
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	require.Error(t, forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
	now = now.Add(time.Second)
	order := make([]string, 0, 4)
	opener.err = nil
	opener.stream = &helperRelayStream{order: &order, commit: tunnel.PreCommit}
	bodyErr := errors.New("local replay body unavailable")
	for range 2 {
		request := directRequest(t, &directReplayBody{openErr: bodyErr})
		outcome := forwarder.Forward(t.Context(), request, httptest.NewRecorder())
		require.ErrorIs(t, outcome.Err, bodyErr)
		require.NotEqual(t, agentproxy.CodeDirectCircuitOpen, outcome.Code)
	}
	require.Equal(t, []agentproxy.DirectCircuitTransition{
		{TargetAgentID: "target-a", State: "open"},
		{TargetAgentID: "target-a", State: "half_open"},
		{TargetAgentID: "target-a", State: "half_open"},
	}, transitions, "a local body failure must neither recover nor re-open the half-open circuit")
}

func TestDirectForwardHalfOpenLocalIOFailureDoesNotRecoverOrCount(t *testing.T) {
	for _, test := range []struct {
		name string
		body *directReplayBody
		dst  func(error) http.ResponseWriter
	}{
		{
			name: "request body read",
			body: &directReplayBody{data: []byte("prefix"), readErr: errors.New("local request body read failed")},
			dst:  func(error) http.ResponseWriter { return httptest.NewRecorder() },
		},
		{
			name: "client response write",
			body: &directReplayBody{},
			dst: func(err error) http.ResponseWriter {
				return &failingResponseWriter{err: err}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_700_000_000, 0)
			transitions := make([]agentproxy.DirectCircuitTransition, 0, 4)
			opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: agentproxy.CodeDirectConnect, counts: true}}
			forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
				Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Second,
				Now: func() time.Time { return now },
				OnCircuitTransition: func(transition agentproxy.DirectCircuitTransition) {
					transitions = append(transitions, transition)
				},
			})
			t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

			require.Error(t, forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
			now = now.Add(time.Second)
			order := make([]string, 0, 8)
			opener.err = nil
			opener.stream = &helperRelayStream{order: &order, commit: tunnel.Committed}
			localErr := test.body.readErr
			if localErr == nil {
				localErr = errors.New("local client response write failed")
			}

			for range 2 {
				outcome := forwarder.Forward(t.Context(), directRequest(t, test.body), test.dst(localErr))
				require.ErrorIs(t, outcome.Err, localErr)
				require.NotEqual(t, agentproxy.CodeDirectCircuitOpen, outcome.Code)
			}
			require.Equal(t, []agentproxy.DirectCircuitTransition{
				{TargetAgentID: "target-a", State: "open"},
				{TargetAgentID: "target-a", State: "half_open"},
				{TargetAgentID: "target-a", State: "half_open"},
			}, transitions)
		})
	}
}

func TestDirectForwardSuccessfulHTTPResultRecoversHalfOpenCircuit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	transitions := make([]agentproxy.DirectCircuitTransition, 0, 4)
	opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: agentproxy.CodeDirectConnect, counts: true}}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Second,
		Now: func() time.Time { return now },
		OnCircuitTransition: func(transition agentproxy.DirectCircuitTransition) {
			transitions = append(transitions, transition)
		},
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	require.Error(t, forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
	now = now.Add(time.Second)
	order := make([]string, 0, 8)
	opener.err = nil
	opener.stream = &helperRelayStream{
		order: &order, commit: tunnel.Committed,
		copyResult: attemptwire.AttemptProxyResult{Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true},
	}
	recovered := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.NoError(t, recovered.Err)
	require.Equal(t, attemptwire.ResultProviderFailed, recovered.AttemptResult.Kind,
		"a valid provider failure Result still proves the Direct transport is healthy")
	require.NoError(t, forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder()).Err)
	require.Equal(t, []agentproxy.DirectCircuitTransition{
		{TargetAgentID: "target-a", State: "open"},
		{TargetAgentID: "target-a", State: "half_open"},
		{TargetAgentID: "target-a", State: "closed"},
	}, transitions)
}

func TestDirectForwardTargetInboundPolicyResetPreservesHalfOpenCircuit(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	transitions := make([]agentproxy.DirectCircuitTransition, 0, 4)
	opener := &fakeDirectOpener{err: &fakeOpenError{stage: "dial", code: agentproxy.CodeDirectConnect, counts: true}}
	forwarder := agentproxy.NewDirectForwarder(agentproxy.DirectForwarderOptions{
		Transports: opener, CircuitFailureThreshold: 1, CircuitOpenDuration: time.Second,
		Now: func() time.Time { return now },
		OnCircuitTransition: func(transition agentproxy.DirectCircuitTransition) {
			transitions = append(transitions, transition)
		},
	})
	t.Cleanup(func() { require.NoError(t, forwarder.Close(context.Background())) })

	failed := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
	require.Equal(t, agentproxy.CodeDirectConnect, failed.Code)
	require.Equal(t, []agentproxy.DirectCircuitTransition{{TargetAgentID: "target-a", State: "open"}}, transitions)

	now = now.Add(time.Second)
	order := make([]string, 0, 3)
	opener.err = nil
	opener.stream = &helperRelayStream{
		order: &order, commit: tunnel.PreCommit,
		commitErr: policyResetError(consts.RouteErrorTargetDirectInboundDisabled),
	}
	for range 2 {
		outcome := forwarder.Forward(t.Context(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())
		require.Equal(t, tunnel.PreCommit, outcome.Commit)
		require.Equal(t, consts.RouteErrorTargetDirectInboundDisabled, outcome.Code)
	}

	require.Equal(t, []agentproxy.DirectCircuitTransition{
		{TargetAgentID: "target-a", State: "open"},
		{TargetAgentID: "target-a", State: "half_open"},
		{TargetAgentID: "target-a", State: "half_open"},
	}, transitions, "policy RESET must release permits without recovering transport health")
}

func TestDirectForwardCommitUncertainDoesNotSignalFallback(t *testing.T) {
	order := make([]string, 0, 5)
	opener := &fakeDirectOpener{stream: &helperRelayStream{
		order: &order, commit: tunnel.CommitUncertain, commitErr: errors.New("lost ack"),
	}}
	f := ownedDirectForTest(t, opener)

	outcome := f.Forward(context.Background(), directRequest(t, &directReplayBody{}), httptest.NewRecorder())

	require.Error(t, outcome.Err)
	require.Equal(t, tunnel.CommitUncertain, outcome.Commit)
}

func TestDirectForwardRejectsNilOpenedReaderBeforeCommit(t *testing.T) {
	order := make([]string, 0, 5)
	opener := &fakeDirectOpener{stream: &helperRelayStream{order: &order, commit: tunnel.PreCommit}}
	f := ownedDirectForTest(t, opener)
	request := directRequest(t, &directReplayBody{})
	request.Body = &nilReaderReplayBody{}

	outcome := f.Forward(context.Background(), request, httptest.NewRecorder())

	require.Error(t, outcome.Err)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.NotContains(t, order, "commit", "body failure must precede commit")
}

func TestDirectForwardRejectsInvalidInputWithoutOpening(t *testing.T) {
	opener := &fakeDirectOpener{stream: &helperRelayStream{commit: tunnel.Committed}}
	f := ownedDirectForTest(t, opener)
	request := directRequest(t, &directReplayBody{})
	request.Attempt = attemptwire.AttemptProxyMeta{} // invalid

	outcome := f.Forward(context.Background(), request, httptest.NewRecorder())

	require.Error(t, outcome.Err)
	require.Equal(t, tunnel.PreCommit, outcome.Commit)
	require.Equal(t, agentproxy.CodeDirectInvalidInput, outcome.Code)
	require.Zero(t, opener.calls.Load(), "invalid attempt must not open a stream")
}

type routeDirectStub func(context.Context, agentproxy.DirectRequest, http.ResponseWriter) agentproxy.AttemptTransportOutcome

func (f routeDirectStub) Forward(ctx context.Context, req agentproxy.DirectRequest, dst http.ResponseWriter) agentproxy.AttemptTransportOutcome {
	return f(ctx, req, dst)
}

func TestPrepareDirectTargetFreezesOneAddressSnapshot(t *testing.T) {
	prepared, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
		AgentID:       "target-a",
		HTTPAddresses: `[{"url":"http://private.example:8139","tag":"private"},{"url":"https://public.example:8140","tag":"public"}]`,
		AgentProxyURL: "http://target-proxy.example:3128", GlobalProxyURL: "http://global-proxy.example:3128",
		AddressTag: "public", PreferredTag: "private",
	})
	require.NoError(t, err)
	require.Equal(t, "target-a", prepared.TargetAgentID)
	require.Equal(t, "https://public.example:8140", prepared.WebSocketURL.String())
	require.Equal(t, "http://target-proxy.example:3128", prepared.ProxyURL.String())
	require.NotEmpty(t, prepared.AddressFingerprint)

	_, err = agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{AgentID: "target-a", HTTPAddresses: `[{"url":"://bad","tag":"public"}]`, AddressTag: "public"})
	require.Error(t, err)
}

func TestPrepareDirectTargetMalformedProxyDoesNotExposeSecrets(t *testing.T) {
	_, err := agentproxy.PrepareDirectTarget(agentproxy.DirectTargetSnapshot{
		AgentID:       "target-a",
		HTTPAddresses: `[{"url":"https://target.example","tag":"public"}]`,
		AddressTag:    "public",
		AgentProxyURL: "http://proxy-user-secret:proxy-password-secret@proxy.example/%zz?token=proxy-query-secret",
	})
	require.Error(t, err)
	for _, secret := range []string{"proxy-user-secret", "proxy-password-secret", "proxy-query-secret"} {
		require.NotContains(t, err.Error(), secret)
	}
}

func TestExecuteDirectTransportBuildsRequestForOneFrozenTarget(t *testing.T) {
	var got agentproxy.DirectRequest
	direct := routeDirectStub(func(_ context.Context, request agentproxy.DirectRequest, _ http.ResponseWriter) agentproxy.AttemptTransportOutcome {
		got = request
		return agentproxy.AttemptTransportOutcome{Commit: tunnel.Committed}
	})
	body := &directReplayBody{data: []byte("body")}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	meta := directAttemptMeta("/v1/responses")
	prepared := frozenDirectTarget(t)

	outcome := agentproxy.ExecuteDirectTransport(t.Context(), direct, agentproxy.DirectTransportRequest{
		TargetAgentID: "target-a", RouteID: 4, RequestID: "request-a", Hop: 1, PreparedTarget: prepared,
		Request: request, Body: body, Attempt: meta,
	}, httptest.NewRecorder())

	require.NoError(t, outcome.Err)
	require.Equal(t, prepared, got.Target)
	require.Equal(t, uint(4), got.RouteID)
	require.Equal(t, "request-a", got.RequestID)
	require.Same(t, request, got.Request)
	require.Same(t, body, got.Body)
	require.Equal(t, meta, got.Attempt)
}

type helperRelayLink struct {
	request agentproxy.AttemptStreamRequest
	stream  *helperRelayStream
	order   *[]string
	err     error
}

func (l *helperRelayLink) OpenAttemptStream(_ context.Context, request agentproxy.AttemptStreamRequest) (agentproxy.AttemptStream, error) {
	*l.order = append(*l.order, "open")
	l.request = request
	if l.err != nil {
		return nil, l.err
	}
	return l.stream, nil
}

type helperRelayStream struct {
	order      *[]string
	commit     tunnel.CommitState
	commitErr  error
	uploadErr  error
	copyErr    error
	copyResult attemptwire.AttemptProxyResult
	afterCopy  func()
	uploaded   string
	cancelErr  error
}

func TestExecuteRelayTransportRejectsMissingAttemptBeforeSideEffects(t *testing.T) {
	order := make([]string, 0, 5)
	stream := &helperRelayStream{order: &order, commit: tunnel.Committed}
	link := &helperRelayLink{stream: stream, order: &order}
	body := &directReplayBody{data: []byte("request-body")}

	outcome := agentproxy.ExecuteRelayTransport(t.Context(), link, agentproxy.RelayTransportRequest{
		TargetAgentID: "target-a", RequestID: "request-a",
		Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Body: body,
	}, httptest.NewRecorder())

	assert.Error(t, outcome.Err)
	assert.Equal(t, tunnel.PreCommit, outcome.Commit)
	assert.Equal(t, "validate", outcome.Stage)
	assert.Equal(t, agentproxy.CodeRelayNotReady, outcome.Code)
	assert.Empty(t, order, "OpenStream and provider lifecycle must not start")
	assert.Zero(t, body.opens.Load(), "request body must not be opened")
	assert.Empty(t, stream.uploaded, "provider body must not be dispatched")
}

func (s *helperRelayStream) Commit(context.Context) error {
	*s.order = append(*s.order, "commit")
	return s.commitErr
}

func (s *helperRelayStream) Upload(_ context.Context, source io.Reader) error {
	*s.order = append(*s.order, "upload")
	body, err := io.ReadAll(source)
	s.uploaded = string(body)
	if err != nil {
		return err
	}
	return s.uploadErr
}

func (s *helperRelayStream) CopyAttemptResponse(_ context.Context, dst http.ResponseWriter) (attemptwire.AttemptProxyResult, error) {
	*s.order = append(*s.order, "copy")
	dst.Header().Set("X-Provider", "ok")
	dst.WriteHeader(http.StatusAccepted)
	result := s.copyResult
	if result.Kind == "" {
		result.Kind = attemptwire.ResultSucceeded
	}
	if _, err := dst.Write([]byte("response")); err != nil {
		return result, err
	}
	if s.afterCopy != nil {
		s.afterCopy()
	}
	return result, s.copyErr
}

func (s *helperRelayStream) CommitState() tunnel.CommitState { return s.commit }
func (s *helperRelayStream) Cancel(err error)                { s.cancelErr = err }
func (s *helperRelayStream) Close() error {
	*s.order = append(*s.order, "close")
	return nil
}

func TestExecuteRelayTransportPreservesAttemptRequestAndLifecycle(t *testing.T) {
	order := make([]string, 0, 5)
	stream := &helperRelayStream{order: &order, commit: tunnel.Committed}
	link := &helperRelayLink{stream: stream, order: &order}
	body := &directReplayBody{data: []byte("request-body")}
	request := httptest.NewRequest(http.MethodPost, "/v1/responses?stream=true", nil)
	request.Header.Set("Authorization", "Bearer original")
	request.Header.Set(consts.HeaderXAgentForwardTicket, "forged")
	meta := attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "gpt-4o", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
	recorder := httptest.NewRecorder()

	outcome := agentproxy.ExecuteRelayTransport(t.Context(), link, agentproxy.RelayTransportRequest{
		TargetAgentID: "target-a", RouteID: 0, RequestID: "request-a",
		Request: request, Body: body, Attempt: &meta,
	}, recorder)

	require.NoError(t, outcome.Err)
	require.Equal(t, tunnel.Committed, outcome.Commit)
	require.True(t, outcome.ResponseStarted)
	require.Equal(t, []string{"open", "commit", "upload", "copy", "close"}, order)
	require.Equal(t, http.MethodPost, link.request.Method)
	require.Equal(t, attemptwire.EndpointPath, link.request.Path)
	require.Equal(t, uint8(1), link.request.Hop)
	require.Zero(t, link.request.RouteID)
	require.Equal(t, meta, link.request.Attempt)
	require.Equal(t, "Bearer original", link.request.Header.Get("Authorization"))
	require.Empty(t, link.request.Header.Get(consts.HeaderXAgentForwardTicket))
	require.Equal(t, "request-body", stream.uploaded)
	require.Equal(t, http.StatusAccepted, recorder.Code)
	require.Equal(t, "response", recorder.Body.String())
}

func TestExecuteRelayTransportPreservesAttemptResultOnInterruptedResponse(t *testing.T) {
	order := make([]string, 0, 5)
	interrupted := errors.New("result received but End was lost")
	want := attemptwire.AttemptProxyResult{
		Kind: attemptwire.ResultProviderFailed, ProviderResultKnown: true, ProviderDispatched: true,
		Dispatches: 2, PromptTokens: 17, CompletionTokens: 3,
	}
	stream := &helperRelayStream{
		order: &order, commit: tunnel.Committed, copyErr: interrupted, copyResult: want,
	}
	link := &helperRelayLink{stream: stream, order: &order}
	meta := directAttemptMeta("/v1/responses")

	outcome := agentproxy.ExecuteRelayTransport(t.Context(), link, agentproxy.RelayTransportRequest{
		TargetAgentID: "target-a", RequestID: "request-a",
		Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil),
		Body:    &directReplayBody{}, Attempt: &meta,
	}, httptest.NewRecorder())

	require.ErrorIs(t, outcome.Err, interrupted)
	require.Equal(t, tunnel.Committed, outcome.Commit)
	require.Equal(t, "response", outcome.Stage)
	require.Equal(t, agentproxy.CodeRelayResponseInterrupted, outcome.Code)
	require.True(t, outcome.ResponseStarted)
	require.NotNil(t, outcome.AttemptResult)
	require.Equal(t, want, *outcome.AttemptResult)
}

func TestExecuteRelayTransportTreatsCompletedResultAsSuccessWhenContextEndsInsideCopy(t *testing.T) {
	for _, test := range []struct {
		name  string
		cause error
	}{
		{name: "canceled", cause: context.Canceled},
		{name: "deadline", cause: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(t.Context())
			want := attemptwire.AttemptProxyResult{
				Kind: attemptwire.ResultSucceeded, ProviderResultKnown: true, ProviderDispatched: true,
				Dispatches: 1, PromptTokens: 19, ResponseStarted: true,
			}
			order := make([]string, 0, 5)
			stream := &helperRelayStream{
				order: &order, commit: tunnel.Committed, copyResult: want,
				afterCopy: func() { cancel(test.cause) },
			}
			link := &helperRelayLink{stream: stream, order: &order}
			meta := directAttemptMeta("/v1/responses")

			outcome := agentproxy.ExecuteRelayTransport(ctx, link, agentproxy.RelayTransportRequest{
				TargetAgentID: "target-a", RequestID: "request-a",
				Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil),
				Body:    &directReplayBody{}, Attempt: &meta,
			}, httptest.NewRecorder())

			require.NoError(t, outcome.Err)
			require.Equal(t, "response", outcome.Stage)
			require.Empty(t, outcome.Code)
			require.True(t, outcome.ResponseStarted)
			require.NotNil(t, outcome.AttemptResult)
			require.Equal(t, want, *outcome.AttemptResult)
		})
	}
}

func TestExecuteRelayTransportClassifiesPreCommitUncertainAndCancellation(t *testing.T) {
	tests := []struct {
		name       string
		linkError  error
		stream     *helperRelayStream
		cancel     bool
		wantCommit tunnel.CommitState
		wantCode   string
	}{
		{name: "open unavailable", linkError: errors.New("not ready"), wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRelayNotReady},
		{name: "commit unavailable", stream: &helperRelayStream{commit: tunnel.PreCommit, commitErr: errors.New("not ready")}, wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRelayNotReady},
		{name: "commit uncertain", stream: &helperRelayStream{commit: tunnel.CommitUncertain, commitErr: errors.New("lost ack")}, wantCommit: tunnel.CommitUncertain, wantCode: agentproxy.CodeRelayCommitUncertain},
		{name: "already canceled", cancel: true, wantCommit: tunnel.PreCommit, wantCode: agentproxy.CodeRequestCancelled},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if tt.cancel {
				cancel()
			}
			order := make([]string, 0, 5)
			stream := tt.stream
			if stream == nil {
				stream = &helperRelayStream{commit: tunnel.Committed}
			}
			stream.order = &order
			link := &helperRelayLink{order: &order, stream: stream, err: tt.linkError}
			meta := directAttemptMeta("/v1/responses")

			outcome := agentproxy.ExecuteRelayTransport(ctx, link, agentproxy.RelayTransportRequest{
				TargetAgentID: "target-a", RequestID: "request-a",
				Request: httptest.NewRequest(http.MethodPost, "/v1/responses", nil), Body: &directReplayBody{}, Attempt: &meta,
			}, httptest.NewRecorder())

			require.Error(t, outcome.Err)
			require.Equal(t, tt.wantCommit, outcome.Commit)
			require.Equal(t, tt.wantCode, outcome.Code)
		})
	}
}
