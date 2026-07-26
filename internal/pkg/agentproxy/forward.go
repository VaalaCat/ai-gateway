package agentproxy

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/app"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	"github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

var (
	errDirectCircuitOpen     = errors.New("direct circuit is open")
	errDirectCircuitHalfOpen = errors.New("direct circuit half-open request is already active")
	errDirectCircuitCapacity = errors.New("direct circuit state capacity is occupied by active requests")
	errDirectClosed          = errors.New("direct forwarder is closed")
	errDirectAttemptInvalid  = errors.New("direct forward: invalid attempt proxy request")
)

type AttemptTransportOutcome struct {
	ResponseStarted bool
	Commit          tunnel.CommitState
	Stage           string
	Code            string
	AttemptResult   *attemptwire.AttemptProxyResult
	Err             error
	circuitEffect   directCircuitEffect
}

type directCircuitEffect uint8

const (
	directCircuitEffectCancelled directCircuitEffect = iota + 1
	directCircuitEffectTransportFailed
	directCircuitEffectHTTPResponded
)

type DirectRequestForwarder interface {
	Forward(context.Context, DirectRequest, http.ResponseWriter) AttemptTransportOutcome
}

// DirectRequest carries only what the tunnel attempt stream needs. The frozen
// target owns the address; credentials belong to the session pool.
type DirectRequest struct {
	Target    DirectSessionTarget
	RouteID   uint
	RequestID string
	Hop       uint8
	Request   *http.Request
	Body      app.ReplayBody
	Attempt   attemptwire.AttemptProxyMeta
}

type DirectTargetSnapshot struct {
	AgentID        string
	HTTPAddresses  string
	AgentProxyURL  string
	GlobalProxyURL string
	AddressTag     string
	PreferredTag   string
}

type DirectTransportRequest struct {
	TargetAgentID  string
	RouteID        uint
	RequestID      string
	Hop            uint8
	PreparedTarget DirectSessionTarget
	Request        *http.Request
	Body           app.ReplayBody
	Attempt        attemptwire.AttemptProxyMeta
}

func ExecuteDirectTransport(
	ctx context.Context,
	direct DirectRequestForwarder,
	req DirectTransportRequest,
	dst http.ResponseWriter,
) AttemptTransportOutcome {
	if direct == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectDisabled, Err: errors.New(CodeDirectDisabled)}
	}
	return direct.Forward(ctx, DirectRequest{
		Target: req.PreparedTarget, RouteID: req.RouteID, RequestID: req.RequestID, Hop: req.Hop,
		Request: req.Request, Body: req.Body, Attempt: req.Attempt,
	}, dst)
}

// PrepareDirectTarget resolves and freezes the direct peer address once. The
// returned target holds the canonical http/https base URL; the session pool and
// dialer adapt it to ws/wss at the dial boundary.
func PrepareDirectTarget(snapshot DirectTargetSnapshot) (DirectSessionTarget, error) {
	addresses := ParseAddresses(snapshot.HTTPAddresses)
	fingerprint := CanonicalAddressFingerprint(addresses)
	targetRaw, err := ResolveAddress(addresses, snapshot.AddressTag, snapshot.PreferredTag, snapshot.AgentID)
	if err != nil {
		return DirectSessionTarget{}, err
	}
	targetURL, err := url.Parse(targetRaw)
	if err != nil || targetURL.Host == "" ||
		(targetURL.Scheme != "http" && targetURL.Scheme != "https") {
		return DirectSessionTarget{}, errors.Join(errors.New(CodeDirectDisabled), err)
	}
	prepared := DirectSessionTarget{
		TargetAgentID: snapshot.AgentID, AddressFingerprint: fingerprint, WebSocketURL: targetURL,
	}
	proxyRaw := ResolveProxyURL(snapshot.AgentProxyURL, snapshot.GlobalProxyURL)
	if proxyRaw == "" {
		return prepared, nil
	}
	prepared.ProxyURL, err = url.Parse(proxyRaw)
	if err != nil || prepared.ProxyURL.Host == "" {
		// behavior change: net/url errors contain the raw proxy URL, including secrets.
		return DirectSessionTarget{}, errors.New(CodeDirectDisabled)
	}
	return prepared, nil
}

type RelayTransportRequest struct {
	TargetAgentID string
	RouteID       uint
	RequestID     string
	Request       *http.Request
	Body          app.ReplayBody
	Attempt       *attemptwire.AttemptProxyMeta
}

func ExecuteRelayTransport(
	ctx context.Context,
	link AttemptStreamOpener,
	req RelayTransportRequest,
	dst http.ResponseWriter,
) AttemptTransportOutcome {
	if ctx == nil || link == nil || req.Request == nil || req.Request.URL == nil || req.Body == nil || dst == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeRelayNotReady, Err: errors.New("relay transport: required input is nil")}
	}
	if err := context.Cause(ctx); err != nil {
		return canceledTransportOutcome(tunnel.PreCommit, err)
	}
	open, err := buildRelayOpenRequest(ctx, req)
	if err != nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeRelayNotReady, Err: err}
	}
	stream, err := link.OpenAttemptStream(ctx, open)
	if err != nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: relayFailureCode(ctx, err, CodeRelayNotReady), Err: err}
	}
	if stream == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: CodeRelayNotReady, Err: errors.New("relay transport returned a nil stream")}
	}
	defer stream.Close()
	return executeAttemptStream(ctx, stream, req.Body, dst)
}

// executeAttemptStream is the single execution path shared by Direct and Relay.
// It opens the request body, commits the stream, uploads the body, and copies
// the provider response, classifying failures into no-replay outcomes.
func executeAttemptStream(
	ctx context.Context,
	stream app.AttemptStream,
	body app.ReplayBody,
	dst http.ResponseWriter,
) AttemptTransportOutcome {
	if err := cancelAttemptBetweenStages(ctx, stream); err != nil {
		return canceledTransportOutcome(stream.CommitState(), err)
	}
	reader, err := body.Open()
	if err != nil || reader == nil {
		if err == nil {
			err = errors.New("attempt transport body returned a nil reader")
		}
		stream.Cancel(err)
		return preCommitBodyFailure(err)
	}
	defer reader.Close()
	if err := cancelAttemptBetweenStages(ctx, stream); err != nil {
		return canceledTransportOutcome(stream.CommitState(), err)
	}
	if err := stream.Commit(ctx); err != nil {
		return classifyCommitFailure(ctx, stream, err)
	}
	if err := cancelAttemptBetweenStages(ctx, stream); err != nil {
		return canceledTransportOutcome(stream.CommitState(), err)
	}
	if err := stream.Upload(ctx, &localAttemptBodyReader{ReadCloser: reader}); err != nil {
		return classifyCommittedFailure(ctx, stream, "upload", err)
	}
	if err := cancelAttemptBetweenStages(ctx, stream); err != nil {
		return canceledTransportOutcome(stream.CommitState(), err)
	}
	tracker, writer := responseStartWriterFor(dst)
	result, err := stream.CopyAttemptResponse(ctx, writer)
	resultValid := result.Validate() == nil
	outcome := classifyAttemptResponse(ctx, stream, tracker.started, resultValid, err)
	// behavior change: an absent or invalid remote result must remain nil.
	if resultValid {
		outcome.AttemptResult = &result
	}
	return outcome
}

func buildRelayOpenRequest(ctx context.Context, req RelayTransportRequest) (AttemptStreamRequest, error) {
	if req.Attempt == nil || req.Attempt.Validate() != nil ||
		!attemptwire.ProviderPathAllowed(http.MethodPost, req.Attempt.RequestPath) {
		return AttemptStreamRequest{}, errDirectAttemptInvalid
	}
	attempt := *req.Attempt
	return AttemptStreamRequest{
		TargetAgentID: req.TargetAgentID, RouteID: req.RouteID, RequestID: req.RequestID,
		Method: http.MethodPost, Path: attemptwire.EndpointPath, Header: cloneDirectRequestHeaders(req.Request.Header),
		BodyLength: req.Body.Size(), Remaining: remainingDuration(ctx), Hop: 1, Attempt: attempt,
	}, nil
}

func buildDirectOpenRequest(ctx context.Context, req DirectRequest) (AttemptStreamRequest, error) {
	if req.Request.Method != http.MethodPost || req.Attempt.Validate() != nil ||
		!attemptwire.ProviderPathAllowed(http.MethodPost, req.Attempt.RequestPath) {
		return AttemptStreamRequest{}, errDirectAttemptInvalid
	}
	return AttemptStreamRequest{
		TargetAgentID: req.Target.TargetAgentID, RouteID: req.RouteID, RequestID: req.RequestID,
		Method: http.MethodPost, Path: attemptwire.EndpointPath, Header: cloneDirectRequestHeaders(req.Request.Header),
		BodyLength: req.Body.Size(), Remaining: remainingDuration(ctx), Hop: 1, Attempt: req.Attempt,
	}, nil
}

func cancelAttemptBetweenStages(ctx context.Context, stream app.AttemptStream) error {
	if err := context.Cause(ctx); err != nil {
		stream.Cancel(err)
		return err
	}
	return nil
}

func classifyCommitFailure(ctx context.Context, stream app.AttemptStream, err error) AttemptTransportOutcome {
	commit := stream.CommitState()
	if commit == tunnel.PreCommit {
		code := relayFailureCode(ctx, err, CodeRelayNotReady)
		return AttemptTransportOutcome{
			Commit: commit, Stage: "commit", Code: code, Err: err,
			circuitEffect: directFailureCircuitEffect(ctx, "commit", code, err),
		}
	}
	return classifyCommittedFailure(ctx, stream, "commit", err)
}

func classifyCommittedFailure(ctx context.Context, stream app.AttemptStream, stage string, err error) AttemptTransportOutcome {
	code := relayFailureCode(ctx, err, CodeRelayCommitUncertain)
	return AttemptTransportOutcome{
		Commit: stream.CommitState(), Stage: stage,
		Code: code, Err: err, circuitEffect: directFailureCircuitEffect(ctx, stage, code, err),
	}
}

func classifyAttemptResponse(
	ctx context.Context, stream app.AttemptStream, started, resultValid bool, err error,
) AttemptTransportOutcome {
	if err != nil {
		code := relayFailureCode(ctx, err, CodeRelayResponseInterrupted)
		return AttemptTransportOutcome{
			ResponseStarted: started, Commit: stream.CommitState(), Stage: "response",
			Code: code, Err: err, circuitEffect: directFailureCircuitEffect(ctx, "response", code, err),
		}
	}
	if cause := context.Cause(ctx); cause != nil && !resultValid {
		stream.Cancel(cause)
		outcome := canceledTransportOutcome(stream.CommitState(), cause)
		outcome.ResponseStarted = started
		return outcome
	}
	return AttemptTransportOutcome{
		Commit: stream.CommitState(), Stage: "response", ResponseStarted: started,
		circuitEffect: directCircuitEffectHTTPResponded,
	}
}

func preCommitBodyFailure(err error) AttemptTransportOutcome {
	return AttemptTransportOutcome{
		Commit: tunnel.PreCommit, Stage: "body", Code: CodeRelayNotReady, Err: err,
		circuitEffect: directCircuitEffectCancelled,
	}
}

func canceledTransportOutcome(commit tunnel.CommitState, err error) AttemptTransportOutcome {
	return AttemptTransportOutcome{
		Commit: commit, Stage: "cancel", Code: relayFailureCode(context.Background(), err, CodeRequestCancelled), Err: err,
		circuitEffect: directCircuitEffectCancelled,
	}
}

func directFailureCircuitEffect(ctx context.Context, stage, code string, err error) directCircuitEffect {
	var localIO *localAttemptIOError
	if stage == "body" || context.Cause(ctx) != nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.ErrNoProgress) || errors.As(err, &localIO) ||
		code == CodeRequestCancelled || code == CodeRequestDeadline ||
		consts.IsDirectedTransportPolicyErrorCode(code) {
		return directCircuitEffectCancelled
	}
	return directCircuitEffectTransportFailed
}

// responseStartWriterFor returns a writer that tracks whether the provider
// response has started, plus the tracker itself for classification.
func responseStartWriterFor(dst http.ResponseWriter) (*responseStartWriter, http.ResponseWriter) {
	tracker := &responseStartWriter{ResponseWriter: dst}
	if flusher, ok := dst.(http.Flusher); ok {
		return tracker, &flushingResponseStartWriter{responseStartWriter: tracker, flusher: flusher}
	}
	return tracker, tracker
}

type localAttemptIOError struct{ err error }

func (e *localAttemptIOError) Error() string { return e.err.Error() }
func (e *localAttemptIOError) Unwrap() error { return e.err }

// behavior change: retain Close so stream cancellation can interrupt a blocked local body read.
type localAttemptBodyReader struct{ io.ReadCloser }

func (r *localAttemptBodyReader) Read(buffer []byte) (int, error) {
	read, err := r.ReadCloser.Read(buffer)
	if read < 0 || read > len(buffer) {
		return 0, &localAttemptIOError{err: fmt.Errorf("invalid local request body read count: %d", read)}
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return read, &localAttemptIOError{err: err}
	}
	return read, err
}

type DirectForwarderOptions struct {
	Transports              DirectAttemptTransportBuilder
	CircuitStateLimit       int
	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration
	Now                     func() time.Time
	OnCircuitTransition     func(DirectCircuitTransition)
}

type DirectForwarder struct {
	transports DirectAttemptTransportBuilder
	circuit    *directCircuit

	lifecycleMu sync.Mutex
	rootCtx     context.Context
	rootCancel  context.CancelCauseFunc
	done        chan struct{}
	active      int
	closing     bool
	doneClosed  bool
}

func NewDirectForwarder(opts DirectForwarderOptions) *DirectForwarder {
	rootCtx, rootCancel := context.WithCancelCause(context.Background())
	return &DirectForwarder{
		transports: opts.Transports,
		circuit: newDirectCircuit(directCircuitOptions{
			FailureThreshold: opts.CircuitFailureThreshold, OpenFor: opts.CircuitOpenDuration,
			Now: opts.Now, Limit: opts.CircuitStateLimit, OnTransition: opts.OnCircuitTransition,
		}),
		rootCtx: rootCtx, rootCancel: rootCancel, done: make(chan struct{}),
	}
}

func (f *DirectForwarder) Forward(ctx context.Context, req DirectRequest, dst http.ResponseWriter) AttemptTransportOutcome {
	if ctx == nil || req.Request == nil || req.Target.WebSocketURL == nil || req.Body == nil || dst == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectInvalidInput, Err: errors.New("direct forward: required input is nil")}
	}
	open, err := buildDirectOpenRequest(ctx, req)
	if err != nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectInvalidInput, Err: errDirectAttemptInvalid}
	}
	if f.transports == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	}
	callCtx, finish, ok := f.begin(ctx)
	if !ok {
		// behavior change: keep the lifecycle cause internal and expose the stable disabled code.
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	}
	defer finish()

	transport, err := f.transports.BuildDirectAttemptTransport(callCtx, req.Target)
	if err != nil {
		return classifyDirectTransportBuildFailure(callCtx, err)
	}
	if transport == nil {
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: CodeDirectDisabled, Err: errors.New("direct forward: transport builder returned nil")}
	}
	key := directCircuitKey{
		TargetAgentID: req.Target.TargetAgentID, AddressFingerprint: req.Target.AddressFingerprint,
		TransportIdentity: transport.TransportIdentity(),
	}
	permit, denied := f.circuit.admit(key)
	if denied != directCircuitAllowed {
		return directCircuitDeniedOutcome(denied)
	}
	permitOwned := true
	defer func() {
		if permitOwned {
			f.circuit.cancelled(permit)
		}
	}()

	reservation, err := transport.AcquireAttemptStream(callCtx)
	if err != nil {
		if reservation != nil {
			reservation.Release()
		}
		outcome := f.classifyDirectOpenFailure(callCtx, permit, err)
		permitOwned = false
		return outcome
	}
	if reservation == nil {
		return AttemptTransportOutcome{
			Commit: tunnel.PreCommit, Stage: "open", Code: CodeDirectDisabled,
			Err: errors.New("direct forward: transport returned nil stream reservation"),
		}
	}
	defer reservation.Release()
	if canceled := f.cancelDirectPermitIfContextDone(callCtx, permit); canceled != nil {
		permitOwned = false
		return *canceled
	}
	permit, rejected := f.bindDirectCircuitToReservedTransport(permit, key, reservation)
	if rejected != nil {
		permitOwned = false
		return *rejected
	}
	if canceled := f.cancelDirectPermitIfContextDone(callCtx, permit); canceled != nil {
		permitOwned = false
		return *canceled
	}
	stream, err := reservation.OpenAttemptStream(callCtx, open)
	if err != nil {
		if stream != nil {
			_ = stream.Close()
		}
		outcome := f.classifyDirectOpenFailure(callCtx, permit, err)
		permitOwned = false
		return outcome
	}
	if stream == nil {
		f.circuit.cancelled(permit)
		permitOwned = false
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: CodeDirectDisabled, Err: errors.New("direct forward: opener returned a nil stream")}
	}
	defer stream.Close()
	outcome := executeAttemptStream(callCtx, stream, req.Body, dst)
	f.observeDirectOutcome(permit, outcome)
	permitOwned = false
	return outcome
}

func (f *DirectForwarder) bindDirectCircuitToReservedTransport(
	plannedPermit directCircuitPermit,
	plannedKey directCircuitKey,
	reservation DirectAttemptStreamReservation,
) (directCircuitPermit, *AttemptTransportOutcome) {
	actualKey, resolved := directCircuitKeyForReservation(plannedKey.TargetAgentID, reservation)
	if !resolved {
		f.circuit.cancelled(plannedPermit)
		outcome := AttemptTransportOutcome{
			Commit: tunnel.PreCommit, Stage: "open", Code: CodeDirectDisabled,
			Err: errors.New("direct forward: stream reservation has incomplete transport identity"),
		}
		return directCircuitPermit{}, &outcome
	}
	if actualKey == plannedKey {
		return plannedPermit, nil
	}
	f.circuit.cancelled(plannedPermit)
	actualPermit, denied := f.circuit.admit(actualKey)
	if denied == directCircuitAllowed {
		return actualPermit, nil
	}
	outcome := directCircuitDeniedOutcome(denied)
	return directCircuitPermit{}, &outcome
}

func directCircuitKeyForReservation(
	targetAgentID string, reservation DirectAttemptStreamReservation,
) (directCircuitKey, bool) {
	if reservation == nil {
		return directCircuitKey{}, false
	}
	transportIdentity := reservation.TransportIdentity()
	addressFingerprint := reservation.AddressFingerprint()
	if transportIdentity == (DirectTransportIdentity{}) || addressFingerprint == "" {
		return directCircuitKey{}, false
	}
	return directCircuitKey{
		TargetAgentID: targetAgentID, AddressFingerprint: addressFingerprint,
		TransportIdentity: transportIdentity,
	}, true
}

func (f *DirectForwarder) cancelDirectPermitIfContextDone(
	ctx context.Context, permit directCircuitPermit,
) *AttemptTransportOutcome {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	f.circuit.cancelled(permit)
	outcome := canceledTransportOutcome(tunnel.PreCommit, cause)
	return &outcome
}

func classifyDirectTransportBuildFailure(ctx context.Context, err error) AttemptTransportOutcome {
	if cause := context.Cause(ctx); cause != nil {
		return canceledTransportOutcome(tunnel.PreCommit, cause)
	}
	stage, code := "open", CodeDirectDisabled
	var failure directOpenFailure
	if errors.As(err, &failure) {
		stage = failure.Stage()
		code = directOpenPublicCode(failure, false)
	}
	return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: stage, Code: code, Err: err}
}

// observeDirectOutcome records connection health for the circuit. A target
// policy RESET happens after OPEN but before admission, so it releases only the
// permit and cannot recover existing transport failures.
func (f *DirectForwarder) observeDirectOutcome(permit directCircuitPermit, outcome AttemptTransportOutcome) {
	// behavior change: only peer/session transport failures affect Direct health;
	// caller I/O and valid HTTP Results release or recover the circuit permit.
	switch outcome.circuitEffect {
	case directCircuitEffectTransportFailed:
		f.circuit.transportFailed(permit)
	case directCircuitEffectHTTPResponded:
		f.circuit.httpResponded(permit)
	default:
		f.circuit.cancelled(permit)
	}
}

// directOpenFailure is implemented by the session pool and dialer typed errors.
// It exposes a stable stage/code plus whether the failure should count toward
// the direct circuit breaker.
type directOpenFailure interface {
	Stage() string
	ReasonCode() string
	CountsForCircuit() bool
}

func (f *DirectForwarder) classifyDirectOpenFailure(ctx context.Context, permit directCircuitPermit, err error) AttemptTransportOutcome {
	if cause := context.Cause(ctx); cause != nil {
		f.circuit.cancelled(permit)
		outcome := canceledTransportOutcome(tunnel.PreCommit, cause)
		return outcome
	}
	stage, code, counts := "open", CodeDirectConnect, true
	var failure directOpenFailure
	if errors.As(err, &failure) {
		stage, counts = failure.Stage(), failure.CountsForCircuit()
		code = directOpenPublicCode(failure, counts)
	}
	if counts {
		f.circuit.transportFailed(permit)
	} else {
		f.circuit.cancelled(permit)
	}
	return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: stage, Code: code, Err: err}
}

// directOpenPublicCode maps typed dial/pool reason codes to the stable public
// codes the pipeline understands. Auth failures surface CodeDirectAuthUnavailable
// so the stage classifier reports the authenticate stage; everything else maps
// to a connect/disabled code without leaking internal reason strings.
func directOpenPublicCode(failure directOpenFailure, counts bool) string {
	if failure.Stage() == "policy" && consts.IsPublicRouteErrorCode(failure.ReasonCode()) {
		return failure.ReasonCode()
	}
	switch failure.Stage() {
	case "credentials":
		return CodeDirectAuthUnavailable
	case "target", "url":
		return CodeDirectDisabled
	case "pool":
		return CodeDirectDisabled
	default:
		if counts {
			return CodeDirectConnect
		}
		return CodeDirectDisabled
	}
}

func directCircuitDeniedOutcome(reason directCircuitDenyReason) AttemptTransportOutcome {
	switch reason {
	case directCircuitDeniedOpen:
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitOpen}
	case directCircuitDeniedHalfOpen:
		// behavior change: expose the stable public circuit state while preserving the precise internal cause.
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitHalfOpen}
	case directCircuitDeniedCapacity:
		// behavior change: capacity is an unavailable circuit, not a new public wire code.
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitCapacity}
	case directCircuitDeniedClosed:
		// behavior change: a closed local forwarder is publicly equivalent to direct routing being disabled.
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	default:
		// behavior change: unknown internal states fail closed to the stable protocol error.
		return AttemptTransportOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: consts.RouteErrorRelayProtocol, Err: errors.New("invalid direct circuit admission result")}
	}
}

func (f *DirectForwarder) begin(ctx context.Context) (context.Context, func(), bool) {
	f.lifecycleMu.Lock()
	if f.closing {
		f.lifecycleMu.Unlock()
		return nil, nil, false
	}
	f.active++
	rootCtx := f.rootCtx
	f.lifecycleMu.Unlock()

	callCtx, cancel := context.WithCancelCause(ctx)
	stopRoot := context.AfterFunc(rootCtx, func() { cancel(context.Cause(rootCtx)) })
	finish := func() {
		stopRoot()
		cancel(context.Canceled)
		f.finishCall()
	}
	return callCtx, finish, true
}

func (f *DirectForwarder) finishCall() {
	f.lifecycleMu.Lock()
	f.active--
	closeDone := f.markDoneLocked()
	f.lifecycleMu.Unlock()
	if closeDone {
		close(f.done)
	}
}

func (f *DirectForwarder) Cancel() {
	if f == nil {
		return
	}
	f.lifecycleMu.Lock()
	f.closing = true
	closeDone := f.markDoneLocked()
	f.lifecycleMu.Unlock()
	f.rootCancel(errDirectClosed)
	if closeDone {
		close(f.done)
	}
}

func (f *DirectForwarder) Close(ctx context.Context) error {
	if f == nil {
		return nil
	}
	if ctx == nil {
		return errors.New("direct forwarder: nil close context")
	}
	f.Cancel()
	f.circuit.close()
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (f *DirectForwarder) markDoneLocked() bool {
	if !f.closing || f.active != 0 || f.doneClosed {
		return false
	}
	f.doneClosed = true
	return true
}

func (f *DirectForwarder) Done() <-chan struct{} {
	if f == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	return f.done
}

func (f *DirectForwarder) ResourceCount() int {
	if f == nil {
		return 0
	}
	return f.circuit.resourceCount()
}

func (f *DirectForwarder) ResetCircuit(targetAgentID, addressFingerprint string) {
	if f != nil {
		f.circuit.reset(targetAgentID, addressFingerprint)
	}
}

func cloneDirectRequestHeaders(source http.Header) http.Header {
	header := source.Clone()
	removeHopHeaders(header)
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag,
		consts.HeaderXAgentAddressTag, consts.HeaderXAgentHop,
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID,
	} {
		header.Del(name)
	}
	return header
}

func removeHopHeaders(header http.Header) {
	for _, value := range header.Values("Connection") {
		for _, token := range strings.Split(value, ",") {
			header.Del(strings.TrimSpace(token))
		}
	}
}

type responseStartWriter struct {
	http.ResponseWriter
	started bool
}

func (w *responseStartWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *responseStartWriter) Write(p []byte) (int, error) {
	w.started = true
	written, err := w.ResponseWriter.Write(p)
	if err != nil {
		return written, &localAttemptIOError{err: err}
	}
	return written, nil
}

type flushingResponseStartWriter struct {
	*responseStartWriter
	flusher http.Flusher
}

func (w *flushingResponseStartWriter) Flush() {
	w.started = true
	w.flusher.Flush()
}

func (w *responseStartWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func remainingDuration(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return 0
	}
	remaining := time.Until(deadline)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func relayFailureCode(ctx context.Context, err error, fallback string) string {
	cause := context.Cause(ctx)
	if errors.Is(cause, context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		return CodeRequestDeadline
	}
	if cause != nil || errors.Is(err, context.Canceled) {
		return CodeRequestCancelled
	}
	var coded interface{ ResetCode() string }
	if errors.As(err, &coded) {
		if code := coded.ResetCode(); consts.IsPublicRouteErrorCode(code) {
			return code
		}
		return consts.RouteErrorRelayProtocol
	}
	if !consts.IsPublicRouteErrorCode(fallback) {
		return consts.RouteErrorRelayProtocol
	}
	return fallback
}

func CanonicalAddressFingerprint(addresses []Address) string {
	canonical := make([]string, 0, len(addresses))
	for _, address := range addresses {
		parsed, err := url.Parse(strings.TrimSpace(address.URL))
		if err != nil {
			canonical = append(canonical, address.Tag+"\x00"+strings.TrimSpace(address.URL))
			continue
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Fragment = ""
		canonical = append(canonical, address.Tag+"\x00"+parsed.String())
	}
	sort.Strings(canonical)
	return fmt.Sprintf("%x", sha256.Sum256([]byte(strings.Join(canonical, "\x00"))))
}
