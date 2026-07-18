package tunnel

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/VaalaCat/ai-gateway/internal/consts"
	"github.com/VaalaCat/ai-gateway/internal/pkg/agentproxy"
	attemptwire "github.com/VaalaCat/ai-gateway/internal/pkg/attemptproxy"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
)

func TestTargetHandlerAllowsBoundAttemptsForProviderEndpoints(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	allowed := []string{
		"/v1/chat/completions",
		"/v1/completions",
		"/v1/responses",
		"/v1/responses/resp_123",
		"/v1/messages",
		"/v1/embeddings",
		"/v1/images/generations",
		"/v1/audio/transcriptions",
		"/v1/audio/translations",
		"/v1/audio/speech",
	}
	for _, path := range allowed {
		t.Run(path, func(t *testing.T) {
			err := handler.ValidateOpen(validBoundTunnelOpen(path))
			require.NoError(t, err)
		})
	}
}

func TestTargetHandlerRequiresBoundAttemptBeforeBusinessRouter(t *testing.T) {
	meta := validTunnelAttemptMeta()
	tests := []struct {
		name      string
		open      wire.Open
		wantError bool
		wantCalls int
	}{
		{
			name: "unbound business open",
			open: wire.Open{
				Method: http.MethodPost, Path: "/v1/chat/completions",
				SourceAgentID: "source-a", TargetAgentID: "target-a", ResponseWindow: 1,
			},
			wantError: true,
		},
		{
			name: "connectivity probe without attempt",
			open: wire.Open{
				Purpose: wire.StreamPurposeConnectivityProbe, Method: http.MethodGet, Path: "/ping",
				TargetAgentID: "target-a", RemainingNanos: 1, ResponseWindow: 1,
			},
			wantCalls: 1,
		},
		{
			name: "bound business open",
			open: wire.Open{
				Method: http.MethodPost, Path: attemptwire.EndpointPath,
				SourceAgentID: "source-a", TargetAgentID: "target-a", Hop: 1,
				ResponseWindow: 1, Attempt: &meta,
			},
			wantCalls: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				require.Equal(t, test.open.Method, r.Method)
				require.Equal(t, test.open.Path, r.URL.Path)
				w.WriteHeader(http.StatusNoContent)
			}))

			err := handler.ValidateOpen(test.open)
			if err == nil {
				req, buildErr := handler.BuildRequest(t.Context(), test.open, wire.StreamID{1}, http.NoBody)
				require.NoError(t, buildErr)
				handler.ServeHTTP(httptest.NewRecorder(), req)
			}

			if test.wantError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.Equal(t, test.wantCalls, calls)
		})
	}
}

func TestTargetHandlerRejectsUntrustedOpenMetadata(t *testing.T) {
	enabled := true
	handler := NewTargetHandler("target-a", func() bool { return enabled }, http.NotFoundHandler())
	tests := []struct {
		name string
		open wire.Open
	}{
		{name: "unknown method", open: wire.Open{Method: http.MethodPut, Path: "/v1/models", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "wrong method", open: wire.Open{Method: http.MethodGet, Path: "/v1/chat/completions", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "unknown path", open: wire.Open{Method: http.MethodPost, Path: "/admin", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "absolute url", open: wire.Open{Method: http.MethodPost, Path: "http://127.0.0.1/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "protocol relative url", open: wire.Open{Method: http.MethodPost, Path: "//127.0.0.1/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "extra response slash", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses/a/b", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "empty response id", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses/", TargetAgentID: "target-a", ResponseWindow: 1}},
		{name: "wrong target", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-b", ResponseWindow: 1}},
		{name: "upgrade", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1, Header: map[string][]string{"Upgrade": {"websocket"}}}},
		{name: "bad body length", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1, BodyLength: -2}},
		{name: "GET body is forbidden", open: wire.Open{Method: http.MethodGet, Path: "/ping", TargetAgentID: "target-a", ResponseWindow: 1, BodyLength: 1}},
		{name: "bad deadline", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1, RemainingNanos: -1}},
		{name: "bad response window", open: wire.Open{Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 0}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) { require.Error(t, handler.ValidateOpen(test.open)) })
	}

	enabled = false
	require.Error(t, handler.ValidateOpen(wire.Open{
		Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1,
	}))
}

func TestTargetHandlerValidatesBoundAttemptEnvelopeAndProviderPath(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	valid := validTunnelAttemptMeta()
	base := wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, TargetAgentID: "target-a",
		ResponseWindow: 1, RouteID: 0, Hop: 1, Attempt: &valid,
	}
	require.NoError(t, handler.ValidateOpen(base), "RouteID zero is valid for a hard route")

	invalid := validTunnelAttemptMeta()
	invalid.Attempt.RealModel = ""
	tests := []struct {
		name string
		open wire.Open
		err  error
	}{
		{name: "invalid attempt", open: func() wire.Open { value := base; value.Attempt = &invalid; return value }(), err: errTargetMetadata},
		{name: "bound provider path", open: func() wire.Open { value := base; value.Path = valid.RequestPath; return value }(), err: errTargetPath},
		{name: "bound GET", open: func() wire.Open { value := base; value.Method = http.MethodGet; return value }(), err: errTargetMethod},
		{name: "disallowed request path", open: func() wire.Open {
			value := base
			meta := valid
			meta.RequestPath = attemptwire.EndpointPath
			value.Attempt = &meta
			return value
		}(), err: errTargetMetadata},
		{name: "endpoint without attempt", open: func() wire.Open { value := base; value.Attempt = nil; return value }(), err: errTargetPath},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorIs(t, handler.ValidateOpen(test.open), test.err)
		})
	}
}

func TestTargetHandlerAllowsOnlyBoundedConnectivityProbeWhenBusinessRelayIsDisabled(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return false }, http.NotFoundHandler())
	tests := []struct {
		name    string
		open    wire.Open
		allowed bool
	}{
		{
			name: "bounded connectivity probe",
			open: wire.Open{
				Purpose: wire.StreamPurposeConnectivityProbe, Method: http.MethodGet, Path: "/ping",
				TargetAgentID: "target-a", RemainingNanos: 1, ResponseWindow: 1,
			},
			allowed: true,
		},
		{
			name: "probe purpose on another path",
			open: wire.Open{
				Purpose: wire.StreamPurposeConnectivityProbe, Method: http.MethodGet, Path: "/v1/models",
				TargetAgentID: "target-a", RemainingNanos: 1, ResponseWindow: 1,
			},
		},
		{
			name: "unmarked ping",
			open: wire.Open{
				Method: http.MethodGet, Path: "/ping", TargetAgentID: "target-a", RemainingNanos: 1, ResponseWindow: 1,
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := handler.ValidateOpen(test.open)
			if test.allowed {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, errTargetUnavailable)
		})
	}
}

func TestTargetHandlerValidatesEveryOpenHeader(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	base := validBoundTunnelOpen("/v1/responses")
	valid := []map[string][]string{
		nil,
		{"X-Empty": {""}},
		{"X-Multi": {"first", "second"}},
		{"X-Token_~": {"\tboundary value\t"}},
	}
	for _, header := range valid {
		open := base
		open.Header = header
		require.NoError(t, handler.ValidateOpen(open), "valid header %#v", header)
	}

	invalid := []map[string][]string{
		{"": {"empty name"}},
		{"Bad Header": {"space in name"}},
		{"Bad:Header": {"colon in name"}},
		{"X-Test": {"good", "bad\r\nInjected: yes"}},
		{"X-Test": {"bad\x00value"}},
	}
	for _, header := range invalid {
		open := base
		open.Header = header
		require.ErrorIs(t, handler.ValidateOpen(open), errTargetMetadata, "invalid header %#v", header)
	}
}

func TestTargetHandlerRejectsUpgradeHeadersRegardlessOfCase(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	base := wire.Open{
		Method: http.MethodPost, Path: "/v1/responses", TargetAgentID: "target-a", ResponseWindow: 1,
	}
	tests := []struct {
		name   string
		header map[string][]string
	}{
		{name: "lowercase upgrade", header: map[string][]string{"upgrade": {"websocket"}}},
		{name: "mixed case upgrade", header: map[string][]string{"uPgRaDe": {"websocket"}}},
		{name: "lowercase connection", header: map[string][]string{"connection": {"keep-alive, upgrade"}}},
		{name: "mixed case connection", header: map[string][]string{"cOnNeCtIoN": {"keep-alive, UpGrAdE"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			open := base
			open.Header = test.header
			require.ErrorIs(t, handler.ValidateOpen(open), errTargetMetadata)
		})
	}
}

func TestTargetHandlerBuildRequestCanonicalizesAndMergesBusinessHeaders(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	open := validBoundTunnelOpen("/v1/responses")
	open.Header = map[string][]string{
		"authorization": {"Bearer business-token"},
		"content-type":  {"application/json"},
		"x-request-id":  {"req-a"},
		"X-Business":    {"canonical"},
		"X-bUsInEsS":    {"mixed"},
		"x-business":    {"lowercase"},
	}

	req, err := handler.BuildRequest(t.Context(), open, wire.StreamID{7}, io.NopCloser(strings.NewReader("")))
	require.NoError(t, err)
	require.Equal(t, "Bearer business-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "req-a", req.Header.Get("X-Request-ID"))
	require.Equal(t, []string{"canonical", "mixed", "lowercase"}, req.Header.Values("X-Business"))
	for key := range req.Header {
		require.Equal(t, http.CanonicalHeaderKey(key), key)
	}
}

func TestTargetHandlerBuildRequestStripsCaseInsensitiveTransportAndAgentHeaders(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	open := validBoundTunnelOpen("/v1/responses")
	open.Header = map[string][]string{
		"connection":                            {"x-extension, X-Other-Extension"},
		"x-extension":                           {"remove"},
		"X-oThEr-ExTeNsIoN":                     {"remove"},
		"pRoXy-CoNnEcTiOn":                      {"keep-alive"},
		"keep-alive":                            {"timeout=5"},
		"tE":                                    {"trailers"},
		"tRaIlEr":                               {"X-Checksum"},
		"transfer-encoding":                     {"chunked"},
		"uPgRaDe":                               {""},
		"content-length":                        {"999"},
		strings.ToLower(consts.HeaderXAgentID):  {"forged"},
		"x-vAaLa-aGeNt-sEcReT":                  {"secret"},
		strings.ToLower(consts.HeaderXAgentTag): {"forged"},
		"x-vAaLa-aGeNt-aDdReSs-tAg":             {"forged"},
		strings.ToLower(consts.HeaderXAgentHop): {"99"},
		strings.ToLower(consts.HeaderXAgentForwardTicket): {"forged-ticket"},
		strings.ToLower(consts.HeaderXAgentRouteID):       {"999"},
	}

	req, err := handler.BuildRequest(t.Context(), open, wire.StreamID{8}, io.NopCloser(strings.NewReader("")))
	require.NoError(t, err)
	for _, key := range []string{
		"Connection", "X-Extension", "X-Other-Extension", "Proxy-Connection", "Keep-Alive",
		"Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length",
		consts.HeaderXAgentID, consts.HeaderXAgentSecret, consts.HeaderXAgentTag,
		consts.HeaderXAgentAddressTag, consts.HeaderXAgentHop,
		consts.HeaderXAgentForwardTicket, consts.HeaderXAgentRouteID,
	} {
		require.Empty(t, req.Header.Values(key), key)
	}
	require.Equal(t, int64(0), req.ContentLength)
}

func TestTargetHandlerBuildRequestStripsTransportAndAgentHeaders(t *testing.T) {
	var captured *http.Request
	handler := NewTargetHandler("target-a", func() bool { return true }, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = r
	}))
	open := validBoundTunnelOpen("/v1/responses")
	open.RouteID = 9
	open.SourceAgentID = "source-a"
	open.RequestID = "req-a"
	open.BodyLength = 3
	open.Header = map[string][]string{
		"Authorization": {"Bearer business-token"}, "Content-Type": {"application/json"},
		"X-Request-Id": {"req-a"}, "Connection": {"keep-alive, X-Hop"}, "X-Hop": {"secret"},
		"Content-Length": {"999"}, consts.HeaderXAgentID: {"forged"}, consts.HeaderXAgentTag: {"forged"},
		consts.HeaderXAgentAddressTag: {"forged"}, consts.HeaderXAgentHop: {"99"},
	}
	body := io.NopCloser(strings.NewReader("abc"))
	ctx := context.WithValue(t.Context(), struct{}{}, "parent")
	req, err := handler.BuildRequest(ctx, open, wire.StreamID{7}, body)
	require.NoError(t, err)
	handler.ServeHTTP(http.ResponseWriter(&discardResponseWriter{header: make(http.Header)}), req)

	require.Same(t, req, captured)
	require.Equal(t, "Bearer business-token", req.Header.Get("Authorization"))
	require.Equal(t, "application/json", req.Header.Get("Content-Type"))
	require.Equal(t, "req-a", req.Header.Get("X-Request-Id"))
	for _, key := range []string{"Connection", "X-Hop", "Content-Length", consts.HeaderXAgentID, consts.HeaderXAgentTag, consts.HeaderXAgentAddressTag, consts.HeaderXAgentHop} {
		require.Empty(t, req.Header.Values(key), key)
	}
	require.Equal(t, int64(3), req.ContentLength)
	require.Empty(t, req.Host)
	require.Empty(t, req.URL.Host)
	require.Empty(t, req.URL.Scheme)
	meta, ok := agentproxy.IngressMetaFromContext(req.Context())
	require.True(t, ok)
	require.Equal(t, agentproxy.IngressMeta{
		Kind: "tunnel", SourceAgentID: "source-a", RouteID: 9, StreamID: wire.StreamID{7}, Hop: 1,
		Attempt: open.Attempt,
	}, meta)
}

func TestTargetHandlerBuildRequestInstallsTrustedBoundAttemptContext(t *testing.T) {
	handler := NewTargetHandler("target-a", func() bool { return true }, http.NotFoundHandler())
	meta := validTunnelAttemptMeta()
	wantMeta := meta
	open := wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath, TargetAgentID: "target-a", RouteID: 0,
		SourceAgentID: "source-a", Hop: 1, BodyLength: 0, ResponseWindow: 1, Attempt: &meta,
		Header: map[string][]string{
			attemptwire.HeaderMeta: {`{"attempt":"forged"}`},
			consts.HeaderXAgentID:  {"forged-source"},
		},
	}
	streamID := wire.StreamID{9}
	req, err := handler.BuildRequest(t.Context(), open, streamID, http.NoBody)
	require.NoError(t, err)
	require.Equal(t, http.MethodPost, req.Method)
	require.Equal(t, attemptwire.EndpointPath, req.URL.Path)
	require.Empty(t, req.Header.Get(attemptwire.HeaderMeta))

	ingress, ok := agentproxy.IngressMetaFromContext(req.Context())
	require.True(t, ok)
	require.Equal(t, agentproxy.IngressKindTunnel, ingress.Kind)
	require.Equal(t, "source-a", ingress.SourceAgentID)
	require.Zero(t, ingress.RouteID)
	require.Equal(t, streamID, ingress.StreamID)
	require.EqualValues(t, 1, ingress.Hop)
	require.NotNil(t, ingress.Attempt)
	require.Equal(t, wantMeta, *ingress.Attempt)
	require.NotSame(t, open.Attempt, ingress.Attempt)
	contextMeta, ok := attemptwire.MetaFromContext(req.Context())
	require.True(t, ok)
	require.Equal(t, wantMeta, contextMeta)

	open.Attempt.Attempt.RealModel = "mutated"
	require.Equal(t, "provider-model", ingress.Attempt.Attempt.RealModel)
	contextMeta, ok = attemptwire.MetaFromContext(req.Context())
	require.True(t, ok)
	require.Equal(t, "provider-model", contextMeta.Attempt.RealModel)
}

func validTunnelAttemptMeta() attemptwire.AttemptProxyMeta {
	return attemptwire.AttemptProxyMeta{
		Attempt: attemptwire.BoundAttempt{
			Channel:   attemptwire.ChannelRef{Source: attemptwire.SourceAdmin, ID: 7},
			RealModel: "provider-model", Mode: attemptwire.ModeNative,
		},
		RequestPath: "/v1/responses",
	}
}

func validBoundTunnelOpen(requestPath string) wire.Open {
	meta := validTunnelAttemptMeta()
	meta.RequestPath = requestPath
	return wire.Open{
		Method: http.MethodPost, Path: attemptwire.EndpointPath,
		SourceAgentID: "source-a", TargetAgentID: "target-a", Hop: 1,
		ResponseWindow: 1, Attempt: &meta,
	}
}

func TestTargetFinalizeReleasesAcceptedInboundBudget(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	limits := testLimits(1)
	writer := newFairWriter(ctx, limits.MaxQueuedSessionBytes, time.Second, func(wire.Frame) error { return nil })
	session := &Session{
		generation: 1, limits: limits, opts: defaultSessionOptions(SessionOptions{}), ctx: ctx, writer: writer,
		streams: make(map[wire.StreamID]*Stream), targets: make(map[wire.StreamID]*targetStream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	target := newTargetStream(session, testStreamID(78), wire.Open{ResponseWindow: limits.InitialStreamWindow})
	session.targets[target.id] = target
	for i := 0; i < 2; i++ {
		require.NoError(t, session.reserveIncoming(1))
		target.inbound <- targetFrame{frame: wire.Frame{Type: wire.FrameRequestData, Payload: []byte{1}}, reserved: 1}
	}

	target.finalize()
	require.Zero(t, session.incomingSize())
}

func TestTargetDeliverySaturationDoesNotDeadlockFinalize(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	limits := testLimits(2)
	writer := newFairWriter(ctx, limits.MaxQueuedSessionBytes, time.Second, func(wire.Frame) error { return nil })
	session := &Session{
		generation: 1, limits: limits, opts: defaultSessionOptions(SessionOptions{}), ctx: ctx, writer: writer,
		streams: make(map[wire.StreamID]*Stream), targets: make(map[wire.StreamID]*targetStream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	target := newTargetStream(session, testStreamID(82), wire.Open{ResponseWindow: limits.InitialStreamWindow})
	sibling := &Stream{id: testStreamID(83), generation: session.generation, inbound: make(chan wire.Frame, 1), done: make(chan struct{})}
	session.targets[target.id] = target
	session.streams[sibling.id] = sibling

	for i := 0; i < cap(target.inbound); i++ {
		require.NoError(t, session.reserveIncoming(1))
		target.inbound <- targetFrame{
			frame:    wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: target.id, Payload: []byte{1}},
			reserved: 1,
		}
	}
	dispatchDone := make(chan error, 1)
	go func() {
		dispatchDone <- session.dispatch(ctx, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData, StreamID: target.id, Payload: []byte{2},
		})
	}()
	require.Eventually(t, func() bool {
		target.deliveries.mu.Lock()
		active := target.deliveries.active
		target.deliveries.mu.Unlock()
		return active == 1 && session.incomingSize() == int64(cap(target.inbound)+1)
	}, time.Second, time.Millisecond)

	finalizeDone := make(chan struct{})
	go func() { target.finalize(); close(finalizeDone) }()
	waitHandlerTestSignal(t, target.deliveryStop, "target finalize did not stop new deliveries")
	waitHandlerTestSignal(t, target.ctx.Done(), "target finalize did not cancel blocked deliveries")
	require.NoError(t, waitHandlerTestResult(t, dispatchDone, "blocked target delivery did not return"))
	waitHandlerTestSignal(t, finalizeDone, "target finalize did not return after stopping deliveries")
	waitHandlerTestSignal(t, target.Done(), "target done did not close after finalize")
	require.Zero(t, session.incomingSize())

	siblingFrame := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: sibling.id}
	require.NoError(t, session.dispatch(ctx, siblingFrame))
	require.Equal(t, siblingFrame, waitHandlerTestResult(t, sibling.inbound, "sibling stream did not receive a frame after target finalize"))
}

func TestTargetCancelUnblocksSaturatedDeliveryBeforeFinalize(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	limits := testLimits(2)
	limits.MaxQueuedSessionBytes = 512
	writer := newFairWriter(ctx, limits.MaxQueuedSessionBytes, 15*time.Second, func(wire.Frame) error { return nil })
	session := &Session{
		generation: 1, limits: limits, opts: defaultSessionOptions(SessionOptions{}), ctx: ctx, writer: writer,
		streams: make(map[wire.StreamID]*Stream), targets: make(map[wire.StreamID]*targetStream),
		tombstones: newTombstoneStore(8, time.Second, time.Now),
	}
	target := newTargetStream(session, testStreamID(86), wire.Open{ResponseWindow: limits.InitialStreamWindow})
	sibling := &Stream{id: testStreamID(87), generation: session.generation, inbound: make(chan wire.Frame, 1), done: make(chan struct{})}
	session.targets[target.id] = target
	session.streams[sibling.id] = sibling

	fill := wire.Frame{
		Version: wire.ProtocolVersion, Type: wire.FrameResponseData, StreamID: sibling.id,
		Payload: make([]byte, limits.MaxQueuedSessionBytes-wire.HeaderSize),
	}
	require.NoError(t, writer.Enqueue(t.Context(), fill, nil))
	ownerDone := make(chan struct{})
	go func() {
		<-target.ctx.Done()
		target.sendReset("cancel", context.Cause(target.ctx))
		target.finalize()
		close(ownerDone)
	}()
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			writer.discard(sibling.id)
			target.Cancel(context.Canceled)
			timer := time.NewTimer(5 * time.Second)
			defer timer.Stop()
			select {
			case <-ownerDone:
			case <-timer.C:
				t.Errorf("target owner did not stop during cleanup")
			}
			cancel()
		})
	}
	t.Cleanup(cleanup)

	for i := 0; i < cap(target.inbound); i++ {
		require.NoError(t, session.reserveIncoming(1))
		target.inbound <- targetFrame{
			frame: wire.Frame{
				Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
				StreamID: target.id, Payload: []byte{1},
			},
			reserved: 1,
		}
	}
	dataDone := make(chan error, 1)
	go func() {
		dataDone <- session.dispatch(ctx, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameRequestData,
			StreamID: target.id, Payload: []byte{2},
		})
	}()
	require.Eventually(t, func() bool {
		target.deliveries.mu.Lock()
		active := target.deliveries.active
		target.deliveries.mu.Unlock()
		return active == 1 && session.incomingSize() == int64(cap(target.inbound)+1)
	}, time.Second, time.Millisecond)

	cancelDone := make(chan error, 1)
	go func() {
		cancelDone <- session.dispatch(ctx, wire.Frame{
			Version: wire.ProtocolVersion, Type: wire.FrameCancel, StreamID: target.id,
		})
	}()
	require.Eventually(t, func() bool {
		writer.mu.Lock()
		replacing := writer.replacing[target.id]
		writer.mu.Unlock()
		return replacing
	}, time.Second, time.Millisecond)
	waitHandlerTestSignal(t, target.ctx.Done(), "target cancel did not close the target context")
	select {
	case <-target.Done():
		t.Fatal("target finalized before its blocked RESET was admitted")
	default:
	}

	require.NoError(t, waitHandlerTestResult(t, cancelDone, "cancel dispatch waited for target finalize after target context cancellation"))
	require.NoError(t, waitHandlerTestResult(t, dataDone, "reserved target delivery waited for finalize after target context cancellation"))
	require.EqualValues(t, cap(target.inbound), session.incomingSize())

	siblingFrame := wire.Frame{Version: wire.ProtocolVersion, Type: wire.FrameReady, StreamID: sibling.id}
	require.NoError(t, session.dispatch(ctx, siblingFrame))
	require.Equal(t, siblingFrame, waitHandlerTestResult(t, sibling.inbound, "sibling stream did not receive a frame after target cancellation"))

	cleanup()
	require.Zero(t, session.incomingSize())
}

func waitHandlerTestSignal(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	waitHandlerTestResult(t, signal, failure)
}

func waitHandlerTestResult[T any](t *testing.T, result <-chan T, failure string) T {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		t.Fatal(failure)
		var zero T
		return zero
	}
}

type discardResponseWriter struct{ header http.Header }

func (w *discardResponseWriter) Header() http.Header       { return w.header }
func (*discardResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (*discardResponseWriter) WriteHeader(_ int)           {}
