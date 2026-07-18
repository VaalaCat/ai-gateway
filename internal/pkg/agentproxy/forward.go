package agentproxy

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentauth"
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

type DirectOutcome struct {
	ResponseStarted bool
	Commit          tunnel.CommitState
	Stage           string
	Code            string
	Err             error
}

// ReplayBody is the request-owned body contract consumed by DirectForwarder.
type ReplayBody interface {
	Size() int64
	Open() (io.ReadCloser, error)
	Bytes(limit int64) ([]byte, error)
	Close() error
}

type DirectRequestForwarder interface {
	Forward(context.Context, DirectRequest, http.ResponseWriter) DirectOutcome
}

type DirectRequest struct {
	TargetAgentID      string
	RouteID            uint
	Hop                uint8
	AddressFingerprint string
	TargetURL          *url.URL
	ProxyURL           *url.URL
	Request            *http.Request
	Body               ReplayBody
	ForwardTicket      agentauth.ForwardTicket
	Attempt            *attemptwire.AttemptProxyMeta
}

type DirectTargetSnapshot struct {
	AgentID        string
	HTTPAddresses  string
	AgentProxyURL  string
	GlobalProxyURL string
	AddressTag     string
	PreferredTag   string
}

type PreparedDirectTarget struct {
	AddressFingerprint string
	TargetURL          *url.URL
	ProxyURL           *url.URL
}

type DirectTransportRequest struct {
	TargetAgentID  string
	RouteID        uint
	Hop            uint8
	PreparedTarget PreparedDirectTarget
	Request        *http.Request
	Body           ReplayBody
	ForwardTicket  agentauth.ForwardTicket
	Attempt        *attemptwire.AttemptProxyMeta
}

func ExecuteDirectTransport(
	ctx context.Context,
	direct DirectRequestForwarder,
	req DirectTransportRequest,
	dst http.ResponseWriter,
) DirectOutcome {
	if direct == nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectDisabled, Err: errors.New(CodeDirectDisabled)}
	}
	return direct.Forward(ctx, DirectRequest{
		TargetAgentID: req.TargetAgentID, RouteID: req.RouteID, Hop: req.Hop,
		AddressFingerprint: req.PreparedTarget.AddressFingerprint,
		TargetURL:          req.PreparedTarget.TargetURL, ProxyURL: req.PreparedTarget.ProxyURL,
		Request: req.Request, Body: req.Body, ForwardTicket: req.ForwardTicket, Attempt: req.Attempt,
	}, dst)
}

func PrepareDirectTarget(snapshot DirectTargetSnapshot) (PreparedDirectTarget, error) {
	addresses := ParseAddresses(snapshot.HTTPAddresses)
	prepared := PreparedDirectTarget{AddressFingerprint: CanonicalAddressFingerprint(addresses)}
	targetRaw, err := ResolveAddress(addresses, snapshot.AddressTag, snapshot.PreferredTag, snapshot.AgentID)
	if err != nil {
		return PreparedDirectTarget{}, err
	}
	prepared.TargetURL, err = url.Parse(targetRaw)
	if err != nil || prepared.TargetURL.Host == "" ||
		(prepared.TargetURL.Scheme != "http" && prepared.TargetURL.Scheme != "https") {
		return PreparedDirectTarget{}, errors.Join(errors.New(CodeDirectDisabled), err)
	}
	proxyRaw := ResolveProxyURL(snapshot.AgentProxyURL, snapshot.GlobalProxyURL)
	if proxyRaw == "" {
		return prepared, nil
	}
	prepared.ProxyURL, err = url.Parse(proxyRaw)
	if err != nil || prepared.ProxyURL.Host == "" {
		return PreparedDirectTarget{}, errors.Join(errors.New(CodeDirectDisabled), err)
	}
	return prepared, nil
}

type RelayTransportRequest struct {
	Purpose       tunnel.StreamPurpose
	TargetAgentID string
	RouteID       uint
	RequestID     string
	Request       *http.Request
	Body          ReplayBody
	Attempt       *attemptwire.AttemptProxyMeta
}

func ExecuteRelayTransport(
	ctx context.Context,
	link RelayLink,
	req RelayTransportRequest,
	dst http.ResponseWriter,
) DirectOutcome {
	if ctx == nil || link == nil || req.Request == nil || req.Request.URL == nil || req.Body == nil || dst == nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeRelayNotReady, Err: errors.New("relay transport: required input is nil")}
	}
	if err := context.Cause(ctx); err != nil {
		return canceledRelayOutcome(tunnel.PreCommit, err)
	}
	open, err := buildRelayOpenRequest(ctx, req)
	if err != nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeRelayNotReady, Err: err}
	}
	stream, err := link.OpenStream(ctx, open)
	if err != nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: relayFailureCode(ctx, err, CodeRelayNotReady), Err: err}
	}
	if stream == nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "open", Code: CodeRelayNotReady, Err: errors.New("relay transport returned a nil stream")}
	}
	defer stream.Close()
	return executeRelayStream(ctx, stream, req.Body, dst)
}

func executeRelayStream(ctx context.Context, stream RelayStream, replay ReplayBody, dst http.ResponseWriter) DirectOutcome {
	if err := cancelRelayBetweenStages(ctx, stream); err != nil {
		return canceledRelayOutcome(stream.CommitState(), err)
	}
	body, err := replay.Open()
	if err != nil {
		stream.Cancel(err)
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "body", Code: CodeRelayNotReady, Err: err}
	}
	if body == nil {
		err = errors.New("relay transport body returned a nil reader")
		stream.Cancel(err)
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "body", Code: CodeRelayNotReady, Err: err}
	}
	defer body.Close()
	if err := cancelRelayBetweenStages(ctx, stream); err != nil {
		return canceledRelayOutcome(stream.CommitState(), err)
	}
	return commitAndUploadRelay(ctx, stream, body, dst)
}

func commitAndUploadRelay(ctx context.Context, stream RelayStream, body io.Reader, dst http.ResponseWriter) DirectOutcome {
	if err := stream.Commit(ctx); err != nil {
		return classifyRelayCommitFailure(ctx, stream, err)
	}
	if err := cancelRelayBetweenStages(ctx, stream); err != nil {
		return canceledRelayOutcome(stream.CommitState(), err)
	}
	if err := stream.Upload(ctx, body); err != nil {
		return classifyRelayCommittedFailure(ctx, stream, "upload", CodeRelayCommitUncertain, false, err)
	}
	if err := cancelRelayBetweenStages(ctx, stream); err != nil {
		return canceledRelayOutcome(stream.CommitState(), err)
	}
	return copyRelayTransportResponse(ctx, stream, dst)
}

func copyRelayTransportResponse(ctx context.Context, stream RelayStream, dst http.ResponseWriter) DirectOutcome {
	responseWriter := &responseStartWriter{ResponseWriter: dst}
	var copyWriter http.ResponseWriter = responseWriter
	if flusher, ok := dst.(http.Flusher); ok {
		copyWriter = &flushingResponseStartWriter{responseStartWriter: responseWriter, flusher: flusher}
	}
	if err := stream.CopyResponse(ctx, copyWriter); err != nil {
		return classifyRelayCommittedFailure(ctx, stream, "response", CodeRelayResponseInterrupted, responseWriter.started, err)
	}
	if err := context.Cause(ctx); err != nil {
		stream.Cancel(err)
		outcome := canceledRelayOutcome(stream.CommitState(), err)
		outcome.ResponseStarted = responseWriter.started
		return outcome
	}
	return DirectOutcome{Commit: stream.CommitState(), Stage: "response", ResponseStarted: responseWriter.started}
}

func buildRelayOpenRequest(ctx context.Context, req RelayTransportRequest) (RelayRequest, error) {
	if req.Attempt == nil || req.Attempt.Validate() != nil ||
		!attemptwire.ProviderPathAllowed(http.MethodPost, req.Attempt.RequestPath) {
		return RelayRequest{}, errDirectAttemptInvalid
	}
	attempt := *req.Attempt
	return RelayRequest{
		Purpose: req.Purpose, TargetAgentID: req.TargetAgentID, RouteID: req.RouteID, RequestID: req.RequestID,
		Method: http.MethodPost, Path: attemptwire.EndpointPath, Header: cloneDirectRequestHeaders(req.Request.Header),
		BodyLength: req.Body.Size(), Remaining: remainingDuration(ctx), Hop: 1, Attempt: &attempt,
	}, nil
}

func cancelRelayBetweenStages(ctx context.Context, stream RelayStream) error {
	if err := context.Cause(ctx); err != nil {
		stream.Cancel(err)
		return err
	}
	return nil
}

func classifyRelayCommitFailure(ctx context.Context, stream RelayStream, err error) DirectOutcome {
	commit := stream.CommitState()
	if commit == tunnel.PreCommit {
		return DirectOutcome{Commit: commit, Stage: "commit", Code: relayFailureCode(ctx, err, CodeRelayNotReady), Err: err}
	}
	return classifyRelayCommittedFailure(ctx, stream, "commit", CodeRelayCommitUncertain, false, err)
}

func classifyRelayCommittedFailure(
	ctx context.Context,
	stream RelayStream,
	stage, fallback string,
	started bool,
	err error,
) DirectOutcome {
	return DirectOutcome{
		ResponseStarted: started, Commit: stream.CommitState(), Stage: stage,
		Code: relayFailureCode(ctx, err, fallback), Err: err,
	}
}

func canceledRelayOutcome(commit tunnel.CommitState, err error) DirectOutcome {
	return DirectOutcome{Commit: commit, Stage: "cancel", Code: relayFailureCode(context.Background(), err, CodeRequestCancelled), Err: err}
}

type preparedDirectAttempt struct {
	encodedMeta string
}

type DirectForwarderOptions struct {
	TransportLimit          int
	CircuitStateLimit       int
	CircuitFailureThreshold int
	CircuitOpenDuration     time.Duration
	ResponseHeaderTimeout   time.Duration
	TLSHandshakeTimeout     time.Duration
	DialContext             func(context.Context, string, string) (net.Conn, error)
	TLSClientConfig         *tls.Config
	Now                     func() time.Time
	OnCircuitTransition     func(DirectCircuitTransition)
}

type DirectForwarder struct {
	transports *directTransportPool
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
		transports: newDirectTransportPool(directTransportPoolOptions{
			Limit: opts.TransportLimit, DialContext: opts.DialContext,
			TLSClientConfig: opts.TLSClientConfig, ResponseHeaderTimeout: opts.ResponseHeaderTimeout,
			TLSHandshakeTimeout: opts.TLSHandshakeTimeout,
		}),
		circuit: newDirectCircuit(directCircuitOptions{
			FailureThreshold: opts.CircuitFailureThreshold, OpenFor: opts.CircuitOpenDuration,
			Now: opts.Now, Limit: opts.CircuitStateLimit, OnTransition: opts.OnCircuitTransition,
		}),
		rootCtx: rootCtx, rootCancel: rootCancel, done: make(chan struct{}),
	}
}

func (f *DirectForwarder) Forward(ctx context.Context, req DirectRequest, dst http.ResponseWriter) DirectOutcome {
	if ctx == nil || req.Request == nil || req.TargetURL == nil || req.Body == nil || dst == nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectInvalidInput, Err: errors.New("direct forward: required input is nil")}
	}
	preparedAttempt, err := prepareDirectAttempt(req)
	if err != nil {
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "validate", Code: CodeDirectInvalidInput, Err: errDirectAttemptInvalid}
	}
	callCtx, finish, ok := f.begin(ctx)
	if !ok {
		// behavior change: keep the lifecycle cause internal and expose the stable disabled code.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	}
	defer finish()

	key := directCircuitKey{TargetAgentID: req.TargetAgentID, AddressFingerprint: req.AddressFingerprint}
	permit, denied := f.circuit.admit(key)
	if denied != directCircuitAllowed {
		return directCircuitDeniedOutcome(denied)
	}

	ownedReader, err := openOwnedReader(req.Body)
	if err != nil {
		f.circuit.cancelled(permit)
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "body", Code: CodeDirectBody, Err: err}
	}
	defer ownedReader.Close()

	outbound := buildDirectRequest(callCtx, req, ownedReader, preparedAttempt)
	transportKey := directTransportKey{
		TargetAgentID: req.TargetAgentID, AddressFingerprint: req.AddressFingerprint,
		Scheme: strings.ToLower(req.TargetURL.Scheme), Proxy: canonicalProxyURL(req.ProxyURL),
	}
	transport := f.transports.get(transportKey, req.ProxyURL)
	if transport == nil {
		f.circuit.cancelled(permit)
		// behavior change: keep the lifecycle cause internal and expose the stable disabled code.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	}

	response, roundTripErr := transport.RoundTrip(outbound)
	if response == nil {
		outcome := classifyRoundTripFailure(roundTripErr)
		if context.Cause(callCtx) != nil {
			f.circuit.cancelled(permit)
		} else {
			f.circuit.transportFailed(permit)
		}
		return outcome
	}
	f.circuit.httpResponded(permit)
	outcome := classifyHTTPResponse(response.StatusCode)
	copyErr := copyDirectResponse(dst, response, &outcome.ResponseStarted)
	outcome.Err = errors.Join(roundTripErr, copyErr)
	if copyErr != nil {
		outcome.Stage = "response_body"
		outcome.Code = CodeDirectResponseCopy
	}
	return outcome
}

func directCircuitDeniedOutcome(reason directCircuitDenyReason) DirectOutcome {
	switch reason {
	case directCircuitDeniedOpen:
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitOpen}
	case directCircuitDeniedHalfOpen:
		// behavior change: expose the stable public circuit state while preserving the precise internal cause.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitHalfOpen}
	case directCircuitDeniedCapacity:
		// behavior change: capacity is an unavailable circuit, not a new public wire code.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: CodeDirectCircuitOpen, Err: errDirectCircuitCapacity}
	case directCircuitDeniedClosed:
		// behavior change: a closed local forwarder is publicly equivalent to direct routing being disabled.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "lifecycle", Code: CodeDirectDisabled, Err: errDirectClosed}
	default:
		// behavior change: unknown internal states fail closed to the stable protocol error.
		return DirectOutcome{Commit: tunnel.PreCommit, Stage: "circuit", Code: consts.RouteErrorRelayProtocol, Err: errors.New("invalid direct circuit admission result")}
	}
}

type onceDirectReadCloser struct {
	io.ReadCloser
	once sync.Once
	err  error
}

func (r *onceDirectReadCloser) Close() error {
	r.once.Do(func() { r.err = r.ReadCloser.Close() })
	return r.err
}

func openOwnedReader(body ReplayBody) (*onceDirectReadCloser, error) {
	reader, err := body.Open()
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("direct forward: replay body returned a nil reader")
	}
	return &onceDirectReadCloser{ReadCloser: reader}, nil
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
	f.transports.closeIdleConnections()
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
	return f.transports.resourceCount() + f.circuit.resourceCount()
}

func (f *DirectForwarder) ResetCircuit(targetAgentID, addressFingerprint string) {
	if f != nil {
		f.circuit.reset(targetAgentID, addressFingerprint)
	}
}

func prepareDirectAttempt(req DirectRequest) (preparedDirectAttempt, error) {
	if req.Attempt == nil {
		return preparedDirectAttempt{}, errDirectAttemptInvalid
	}
	if req.ForwardTicket == "" || req.Request.Method != http.MethodPost || req.Attempt.Validate() != nil ||
		!attemptwire.ProviderPathAllowed(http.MethodPost, req.Attempt.RequestPath) {
		return preparedDirectAttempt{}, errDirectAttemptInvalid
	}
	encodedMeta, err := attemptwire.EncodeMeta(*req.Attempt)
	if err != nil {
		return preparedDirectAttempt{}, errDirectAttemptInvalid
	}
	return preparedDirectAttempt{encodedMeta: encodedMeta}, nil
}

func buildDirectRequest(ctx context.Context, req DirectRequest, body io.ReadCloser, prepared preparedDirectAttempt) *http.Request {
	outbound := req.Request.Clone(ctx)
	target := *req.TargetURL
	target.Path = attemptwire.EndpointPath
	target.RawPath = ""
	if target.RawQuery == "" {
		target.RawQuery = outbound.URL.RawQuery
	} else if outbound.URL.RawQuery != "" {
		target.RawQuery += "&" + outbound.URL.RawQuery
	}
	outbound.URL = &target
	outbound.RequestURI = ""
	outbound.Host = target.Host
	outbound.Body = body
	outbound.GetBody = func() (io.ReadCloser, error) { return openOwnedReader(req.Body) }
	outbound.ContentLength = req.Body.Size()
	if outbound.ContentLength == 0 {
		outbound.Body = http.NoBody
		outbound.GetBody = func() (io.ReadCloser, error) { return http.NoBody, nil }
	}
	outbound.TransferEncoding = nil
	outbound.Trailer = nil
	outbound.Header = cloneDirectRequestHeaders(req.Request.Header)
	outbound.Header.Set(consts.HeaderXAgentHop, "1")
	outbound.Header.Set(attemptwire.HeaderMeta, prepared.encodedMeta)
	if req.ForwardTicket != "" {
		outbound.Header.Set(consts.HeaderXAgentForwardTicket, string(req.ForwardTicket))
		outbound.Header.Set(consts.HeaderXAgentRouteID, strconv.FormatUint(uint64(req.RouteID), 10))
	}
	return outbound
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
		attemptwire.HeaderMeta,
	} {
		header.Del(name)
	}
	return header
}

func copyDirectResponse(dst http.ResponseWriter, response *http.Response, started *bool) error {
	header := response.Header.Clone()
	removeHopHeaders(header)
	for _, name := range []string{
		"Connection", "Proxy-Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade",
	} {
		header.Del(name)
	}
	removeReservedForwardHeaders(header)
	copyHeader(dst.Header(), header)
	removeReservedForwardHeaders(response.Trailer)
	declaredTrailers := declareResponseTrailers(dst.Header(), response.Trailer)
	*started = true
	dst.WriteHeader(response.StatusCode)
	writer := io.Writer(dst)
	if flusher, ok := dst.(http.Flusher); ok {
		writer = flushAfterWrite{Writer: dst, Flusher: flusher}
	}
	_, copyErr := io.Copy(writer, response.Body)
	removeReservedForwardHeaders(response.Trailer)
	copyResponseTrailers(dst.Header(), response.Trailer, declaredTrailers)
	return errors.Join(copyErr, response.Body.Close())
}

func removeReservedForwardHeaders(header http.Header) {
	header.Del(consts.HeaderXAgentForwardTicket)
	header.Del(consts.HeaderXAgentRouteID)
}

type flushAfterWrite struct {
	io.Writer
	http.Flusher
}

func (w flushAfterWrite) Write(p []byte) (int, error) {
	n, err := w.Writer.Write(p)
	w.Flusher.Flush()
	return n, err
}

func copyHeader(dst, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func declareResponseTrailers(header http.Header, trailers http.Header) map[string]struct{} {
	keys := make([]string, 0, len(trailers))
	declared := make(map[string]struct{}, len(trailers))
	for key := range trailers {
		canonical := http.CanonicalHeaderKey(key)
		keys = append(keys, canonical)
		declared[canonical] = struct{}{}
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		header.Set("Trailer", strings.Join(keys, ", "))
	}
	return declared
}

func copyResponseTrailers(header http.Header, trailers http.Header, declared map[string]struct{}) {
	for key, values := range trailers {
		canonical := http.CanonicalHeaderKey(key)
		if _, ok := declared[canonical]; ok {
			header[canonical] = append([]string(nil), values...)
			continue
		}
		header[http.TrailerPrefix+canonical] = append([]string(nil), values...)
	}
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
	return w.ResponseWriter.Write(p)
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
