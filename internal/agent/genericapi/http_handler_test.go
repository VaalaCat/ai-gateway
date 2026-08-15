package genericapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	"github.com/VaalaCat/ai-gateway/internal/settings"
	"github.com/gin-gonic/gin"
	"github.com/sourcegraph/conc/pool"
	"github.com/stretchr/testify/require"
)

type localHTTPPicker struct {
	lease     *APIUpstreamLease
	err       error
	calls     atomic.Int32
	backendID atomic.Uint32
}

type mutableAgentSettingsFinder struct {
	mu       sync.RWMutex
	settings settings.AgentSettings
}

func (f *mutableAgentSettingsFinder) Settings() settings.AgentSettings {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.settings
}

func (f *mutableAgentSettingsFinder) Update(next settings.AgentSettings) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings = next
}

func (p *localHTTPPicker) Pick(backendID uint, _ apiattempt.APIProtocol, _ string) (*APIUpstreamLease, error) {
	p.calls.Add(1)
	p.backendID.Store(uint32(backendID))
	return p.lease, p.err
}

type localHTTPPermit struct {
	calls       atomic.Int32
	completions chan APIBreakerCompletion
}

type executionLimiterSpy struct {
	calls int
	facts APIRequestFacts
}

type executionPermit struct{}

func (executionPermit) Release() {}

func (g *executionLimiterSpy) Acquire(_ context.Context, facts APIRequestFacts) (APIPermit, error) {
	g.calls++
	g.facts = facts
	return executionPermit{}, nil
}

func TestGenericHTTPUsesOneExecutionAgentAndOneUpstream(t *testing.T) {
	breakerPermit := newLocalHTTPPermit()
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   breakerPermit,
	}}
	var providerCalls atomic.Int32
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		return &http.Response{StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody, Request: request}, nil
	}))
	limiter := &executionLimiterSpy{}
	handler := NewHTTPHandler(picker, transport, limiter)
	request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/one", nil)
	rc, _ := newLocalHTTPRequestContext(request)
	rc.UserID, rc.GroupID, rc.TokenID = 5, 3, 2

	require.NoError(t, handler.Serve(t.Context(), rc))
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, 1, limiter.calls)
	require.Equal(t, uint(11), limiter.facts.APIUpstreamID)
	require.Equal(t, int32(1), providerCalls.Load())
	require.True(t, rc.Execution.ProviderDispatchKnown)
	require.True(t, rc.Execution.ProviderDispatched)
	require.Equal(t, "Primary", rc.UpstreamName)
}

func TestHTTPHandlerPicksTheRouteBackendAndReportsNoCandidateAsUnavailable(t *testing.T) {
	picker := &localHTTPPicker{err: unavailableUpstream("no candidates for route backend")}
	handler := NewHTTPHandler(picker, NewHTTPTransport(""))
	rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodGet, "http://gateway.invalid/forecast", nil))
	rc.Route.BackendID = 10

	err := handler.Serve(t.Context(), rc)

	require.Error(t, err)
	require.Equal(t, uint32(10), picker.backendID.Load())
	require.Equal(t, int32(1), picker.calls.Load(), "one HTTP request must make one frozen upstream pick")
	require.Equal(t, "upstream_pick", rc.Execution.ErrorStage)
	require.Equal(t, CodeUnavailable, rc.Execution.ErrorCode)
}

func TestHTTPHandlerReadsLatestTransportSettingsForEachRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	transport := NewHTTPTransport("")
	handler, _, _ := newLocalHTTPHandler(upstream.URL, transport)
	settingsFinder := &mutableAgentSettingsFinder{}
	handler.WithSettings(settingsFinder)

	for _, test := range []struct {
		name            string
		dialTimeoutMs   int
		tlsTimeoutMs    int
		headerTimeoutMs int
	}{
		{name: "first snapshot", dialTimeoutMs: 11, tlsTimeoutMs: 12, headerTimeoutMs: 13},
		{name: "updated snapshot", dialTimeoutMs: 21, tlsTimeoutMs: 22, headerTimeoutMs: 23},
	} {
		t.Run(test.name, func(t *testing.T) {
			settingsFinder.Update(settings.AgentSettings{
				APIUpstreamDialTimeoutMs:           test.dialTimeoutMs,
				APIUpstreamTLSHandshakeTimeoutMs:   test.tlsTimeoutMs,
				APIUpstreamResponseHeaderTimeoutMs: test.headerTimeoutMs,
			})
			rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodGet, "http://gateway.invalid/test", nil))
			require.NoError(t, handler.Serve(t.Context(), rc))

			configured := underlyingHTTPTransport(t, transport)
			require.Equal(t, time.Duration(test.tlsTimeoutMs)*time.Millisecond, configured.TLSHandshakeTimeout)
			require.Equal(t, time.Duration(test.headerTimeoutMs)*time.Millisecond, configured.ResponseHeaderTimeout)
		})
	}
}

func TestStreamingRequestBodyUploadIdleTimeout(t *testing.T) {
	t.Run("zero disables idle timeout", func(t *testing.T) {
		body := newCloseBlockingBody()
		streaming := newStreamingRequestBody(body, nil, nil, 0)
		readResult := make(chan error, 1)
		go func() {
			_, err := streaming.Read(make([]byte, 1))
			readResult <- err
		}()
		select {
		case <-body.readStarted:
		case <-time.After(time.Second):
			t.Fatal("request body read did not start")
		}
		select {
		case err := <-readResult:
			t.Fatalf("zero timeout unexpectedly ended upload: %v", err)
		case <-time.After(40 * time.Millisecond):
		}
		require.NoError(t, streaming.Close())
		require.NotErrorIs(t, <-readResult, ErrUploadIdleTimeout)
	})

	t.Run("closes a body with no upload progress", func(t *testing.T) {
		body := newCloseBlockingBody()
		streaming := newStreamingRequestBody(body, nil, nil, 10*time.Millisecond)
		readResult := make(chan error, 1)
		go func() {
			_, err := streaming.Read(make([]byte, 1))
			readResult <- err
		}()
		require.ErrorIs(t, <-readResult, ErrUploadIdleTimeout)
		requireClosed(t, body.closed, "idle upload body")
	})

	t.Run("progress resets the timer beyond the total timeout", func(t *testing.T) {
		const idleTimeout = 250 * time.Millisecond
		body := &pacedRequestBody{remaining: 12, delay: 25 * time.Millisecond}
		streaming := newStreamingRequestBody(body, nil, nil, idleTimeout)
		startedAt := time.Now()
		read, err := io.ReadAll(streaming)
		require.NoError(t, err)
		require.Equal(t, "xxxxxxxxxxxx", string(read))
		require.Greater(t, time.Since(startedAt), idleTimeout)
		require.False(t, body.closed.Load())
	})
}

func TestHTTPHandlerClassifiesUploadIdleTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		writer.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	settingsFinder := &mutableAgentSettingsFinder{settings: settings.AgentSettings{APIUploadIdleTimeoutMs: 10}}
	handler, _, _ := newLocalHTTPHandler(upstream.URL, nil)
	handler.WithSettings(settingsFinder)
	requestBody := newCloseBlockingBody()
	rc, _ := newLocalHTTPRequestContext(httptest.NewRequest(http.MethodPost, "http://gateway.invalid/upload", requestBody))

	err := handler.Serve(t.Context(), rc)
	require.ErrorIs(t, err, ErrUploadIdleTimeout)
	require.Equal(t, "upload", rc.Execution.ErrorStage)
	requireClosed(t, requestBody.closed, "handler upload body")
}

func newLocalHTTPPermit() *localHTTPPermit {
	return &localHTTPPermit{completions: make(chan APIBreakerCompletion, 4)}
}

func (p *localHTTPPermit) Finish(completion APIBreakerCompletion) {
	p.calls.Add(1)
	p.completions <- completion
}

func (p *localHTTPPermit) completion(t *testing.T) APIBreakerCompletion {
	t.Helper()
	select {
	case completion := <-p.completions:
		return completion
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for API breaker completion")
		return APIBreakerCompletion{}
	}
}

func newLocalHTTPHandler(upstreamURL string, transport *HTTPTransport) (*HTTPHandler, *localHTTPPicker, *localHTTPPermit) {
	permit := newLocalHTTPPermit()
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{
			ID: 11, BackendID: 7, BaseURL: upstreamURL, AuthType: "none", Status: 1, Weight: 1,
		},
		permit: permit,
	}}
	if transport == nil {
		transport = NewHTTPTransport("")
	}
	return NewHTTPHandler(picker, transport), picker, permit
}

func newLocalHTTPRequestContext(request *http.Request) (*RequestContext, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ginContext, _ := gin.CreateTestContext(recorder)
	ginContext.Request = request
	return &RequestContext{
		Context: ginContext,
		Service: protocol.SyncedAPIService{ID: 7},
		Route: protocol.SyncedAPIRoute{
			ID: 9,
		},
		Protocol: ProtocolHTTP, RequestID: "request-1",
	}, recorder
}

func newLocalHTTPGateway(t *testing.T, handler *HTTPHandler) (*httptest.Server, <-chan error) {
	t.Helper()
	results := make(chan error, 1)
	router := gin.New()
	router.Any("/*path", func(c *gin.Context) {
		err := handler.Serve(c.Request.Context(), &RequestContext{
			Context: c,
			Service: protocol.SyncedAPIService{ID: 7},
			Route: protocol.SyncedAPIRoute{
				ID: 9,
			},
			Protocol: ProtocolHTTP, RequestID: "request-1",
		})
		results <- err
		if err != nil && !c.Writer.Written() {
			c.String(http.StatusServiceUnavailable, "gateway-error")
		}
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return server, results
}

func TestLocalHTTPStreamsResponseBeforeUploadCompletes(t *testing.T) {
	firstUpload := bytes.Repeat([]byte("a"), 64*1024)
	uploadFinished := make(chan string, 1)
	var upstreamBytes atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, http.NewResponseController(w).EnableFullDuplex())
		first := make([]byte, len(firstUpload))
		count, err := io.ReadFull(r.Body, first)
		upstreamBytes.Store(int64(count))
		require.NoError(t, err)
		w.WriteHeader(http.StatusOK)
		_, err = io.WriteString(w, "early")
		require.NoError(t, err)
		w.(http.Flusher).Flush()
		rest, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		uploadFinished <- fmt.Sprintf("%x", sha256.Sum256(append(first, rest...)))
		_, err = io.WriteString(w, "-late")
		require.NoError(t, err)
	}))
	defer upstream.Close()

	handler, picker, permit := newLocalHTTPHandler(upstream.URL, nil)
	gateway, serveResults := newLocalHTTPGateway(t, handler)
	upload := newStagedRequestBody(firstUpload, []byte("-world"))
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.URL+"/upload", upload)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	early := make([]byte, len("early"))
	_, err = io.ReadFull(response.Body, early)
	require.NoError(t, err)
	require.Equal(t, "early", string(early), "first response bytes must arrive while the upload body is still blocked")
	upload.Finish()
	rest, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, "-late", string(rest))
	require.NoError(t, <-serveResults)
	wantUpload := append(append([]byte(nil), firstUpload...), []byte("-world")...)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(wantUpload)), <-uploadFinished)
	require.Equal(t, int32(1), picker.calls.Load())
	completion := permit.completion(t)
	require.NoError(t, completion.Err)
	require.Equal(t, http.StatusOK, completion.Result.UpstreamStatus)
}

func TestLocalHTTPForwardsRequestAndResponseTrailers(t *testing.T) {
	requestTrailers := make(chan http.Header, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		require.NoError(t, err)
		requestTrailers <- r.Trailer.Clone()
		w.Header().Set("Trailer", "X-Response-Digest")
		w.WriteHeader(http.StatusOK)
		_, err = io.WriteString(w, "trailer-body")
		require.NoError(t, err)
		w.Header().Set("X-Response-Digest", "response-final")
	}))
	defer upstream.Close()

	handler, _, permit := newLocalHTTPHandler(upstream.URL, nil)
	gateway, serveResults := newLocalHTTPGateway(t, handler)
	reader, writer := io.Pipe()
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/trailers", reader)
	require.NoError(t, err)
	request.Trailer = http.Header{"X-Upload-Digest": nil}
	writers := pool.New().WithErrors()
	writers.Go(func() error {
		if _, writeErr := io.WriteString(writer, "request-body"); writeErr != nil {
			return writeErr
		}
		request.Trailer.Set("X-Upload-Digest", "request-final")
		return writer.Close()
	})

	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.NoError(t, writers.Wait())
	require.Equal(t, "trailer-body", string(body))
	require.Equal(t, "request-final", (<-requestTrailers).Get("X-Upload-Digest"))
	require.Equal(t, "response-final", response.Trailer.Get("X-Response-Digest"))
	require.NoError(t, <-serveResults)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPPreservesLargeChunkedBodyWithoutBodyStore(t *testing.T) {
	wantBody := bytes.Repeat([]byte("streaming-body-0123456789"), 100_000)
	wantHash := fmt.Sprintf("%x", sha256.Sum256(wantBody))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hash := sha256.New()
		_, err := io.Copy(hash, r.Body)
		require.NoError(t, err)
		require.Equal(t, int64(-1), r.ContentLength)
		_, err = fmt.Fprintf(w, "%x", hash.Sum(nil))
		require.NoError(t, err)
	}))
	defer upstream.Close()

	handler, _, permit := newLocalHTTPHandler(upstream.URL, nil)
	gateway, serveResults := newLocalHTTPGateway(t, handler)
	request, err := http.NewRequest(http.MethodPost, gateway.URL+"/large", bytes.NewReader(wantBody))
	require.NoError(t, err)
	request.ContentLength = -1
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	gotHash, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, wantHash, string(gotHash))
	require.NoError(t, <-serveResults)
	completion := permit.completion(t)
	require.Equal(t, int64(len(wantBody)), completion.Result.RequestBytes)
	require.Equal(t, int64(len(gotHash)), completion.Result.ResponseBytes)
}

func TestLocalHTTPPropagatesCancelAndStopsBothCopyDirections(t *testing.T) {
	requestBody := newCloseBlockingBody()
	responseBlock := newCloseBlockingBody()
	responseBody := &prefixThenBlockingBody{prefix: []byte("start"), blocker: responseBlock}
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       responseBody,
			Request:    request,
		}, nil
	}))
	handler, _, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway.invalid/cancel", requestBody)
	require.NoError(t, err)
	rc, _ := newLocalHTTPRequestContext(request)
	serveResult := make(chan error, 1)
	workers := pool.New()
	workers.Go(func() { serveResult <- handler.Serve(ctx, rc) })

	select {
	case <-responseBlock.readStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("response copy never started")
	}
	cancel()
	select {
	case err = <-serveResult:
		require.NoError(t, err, "a committed canceled stream must not reach the outer JSON error writer")
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after client cancellation")
	}
	workers.Wait()
	requireClosed(t, requestBody.closed, "request body")
	requireClosed(t, responseBlock.closed, "upstream response body")
	completion := permit.completion(t)
	require.Error(t, completion.Err)
	require.Equal(t, APIClientAbortCanceled, completion.ClientAbort)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPCancelClosesRequestBodyWhileDispatchIsBlocked(t *testing.T) {
	requestBody := newCloseBlockingBody()
	dispatchStarted := make(chan struct{})
	var closedBeforeReturn atomic.Bool
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		close(dispatchStarted)
		select {
		case <-requestBody.closed:
			closedBeforeReturn.Store(true)
			return nil, context.Canceled
		case <-time.After(500 * time.Millisecond):
			return nil, errors.New("request body remained open while dispatch was blocked")
		}
	}))
	handler, _, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
	ctx, cancel := context.WithCancel(t.Context())
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway.invalid/cancel", requestBody)
	require.NoError(t, err)
	rc, _ := newLocalHTTPRequestContext(request)
	serveResult := make(chan error, 1)
	workers := pool.New()
	workers.Go(func() { serveResult <- handler.Serve(ctx, rc) })
	<-dispatchStarted
	cancel()

	select {
	case err = <-serveResult:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after canceling a blocked dispatch")
	}
	workers.Wait()
	require.True(t, closedBeforeReturn.Load(), "cancel must close the request body before waiting for RoundTrip to return")
	requireClosed(t, requestBody.closed, "request body")
	completion := permit.completion(t)
	require.Equal(t, APIClientAbortCanceled, completion.ClientAbort)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPDeadlineUsesDeadlineClientAbortFact(t *testing.T) {
	requestBody := newCloseBlockingBody()
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		<-requestBody.closed
		return nil, context.DeadlineExceeded
	}))
	handler, _, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://gateway.invalid/deadline", requestBody)
	require.NoError(t, err)
	rc, _ := newLocalHTTPRequestContext(request)

	err = handler.Serve(ctx, rc)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	completion := permit.completion(t)
	require.Equal(t, APIClientAbortDeadlineExceeded, completion.ClientAbort)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPReturnsUpstream4xxAnd5xxUnchanged(t *testing.T) {
	for _, status := range []int{http.StatusTeapot, http.StatusBadGateway} {
		t.Run(fmt.Sprintf("status_%d", status), func(t *testing.T) {
			transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: status,
					Header: http.Header{
						"Content-Type": []string{"application/problem+json"},
						"X-Upstream":   []string{"preserved"},
					},
					Body:    io.NopCloser(strings.NewReader(fmt.Sprintf("status-%d", status))),
					Request: request,
				}, nil
			}))
			handler, picker, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
			request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/status", nil)
			rc, recorder := newLocalHTTPRequestContext(request)

			err := handler.Serve(request.Context(), rc)
			require.NoError(t, err)
			require.Equal(t, status, recorder.Code)
			require.Equal(t, "application/problem+json", recorder.Header().Get("Content-Type"))
			require.Equal(t, "preserved", recorder.Header().Get("X-Upstream"))
			require.Equal(t, fmt.Sprintf("status-%d", status), recorder.Body.String())
			require.Equal(t, int32(1), picker.calls.Load())
			completion := permit.completion(t)
			require.Equal(t, status, completion.Result.UpstreamStatus)
			require.NoError(t, completion.Err)
		})
	}
}

func TestLocalHTTPFailureAfterResponseCommitOnlyTerminatesStream(t *testing.T) {
	streamFailure := errors.New("upstream response interrupted")
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode:    http.StatusOK,
			Header:        http.Header{"Content-Type": []string{"text/plain"}},
			Body:          &readThenErrorBody{value: []byte("partial"), err: streamFailure},
			ContentLength: 100,
			Request:       request,
		}, nil
	}))
	handler, _, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
	request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/interrupted", nil)
	rc, recorder := newLocalHTTPRequestContext(request)

	err := handler.Serve(request.Context(), rc)
	require.NoError(t, err, "committed response errors must not trigger the outer Generic API error writer")
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "partial", recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "gateway-error")
	completion := permit.completion(t)
	require.ErrorIs(t, completion.Err, streamFailure)
	require.Equal(t, "response_body", completion.Result.ErrorStage)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPFirstBodyReadFailureKeepsCommittedUpstreamResponse(t *testing.T) {
	streamFailure := errors.New("upstream failed before first response byte")
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Encoding": []string{"gzip"},
				"Content-Length":   []string{"321"},
				"Content-Type":     []string{"application/octet-stream"},
				"Set-Cookie":       []string{"one=1; Path=/", "two=2; Path=/"},
			},
			Body:          &readThenErrorBody{err: streamFailure},
			ContentLength: 321,
			Request:       request,
		}, nil
	}))
	handler, _, permit := newLocalHTTPHandler("http://upstream.invalid", transport)
	request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/first-read-failure", nil)
	rc, recorder := newLocalHTTPRequestContext(request)
	outerFailureWriterRan := false

	if err := handler.Serve(request.Context(), rc); err != nil {
		outerFailureWriterRan = true
		rc.Context.String(http.StatusServiceUnavailable, "gateway-error")
	}

	require.False(t, outerFailureWriterRan, "a valid upstream status must be committed before the first body read")
	require.True(t, rc.Context.Writer.Written())
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "gzip", recorder.Header().Get("Content-Encoding"))
	require.Equal(t, "321", recorder.Header().Get("Content-Length"))
	require.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))
	require.Equal(t, []string{"one=1; Path=/", "two=2; Path=/"}, recorder.Header().Values("Set-Cookie"))
	require.Empty(t, recorder.Body.String())
	require.NotContains(t, recorder.Body.String(), "gateway-error")
	completion := permit.completion(t)
	require.Equal(t, http.StatusOK, completion.Result.UpstreamStatus)
	require.Equal(t, "response_body", completion.Result.ErrorStage)
	require.NoError(t, completion.Result.Validate())
	require.ErrorIs(t, completion.Err, streamFailure)
	require.Equal(t, int32(1), permit.calls.Load())
}

func TestLocalHTTPRejectsInvalidPickedLeasesWithoutDispatch(t *testing.T) {
	validUpstream := protocol.SyncedAPIUpstream{
		ID: 41, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "none",
	}
	tests := []struct {
		name             string
		lease            func(*localHTTPPermit) *APIUpstreamLease
		wantPermitFinish bool
	}{
		{name: "nil lease", lease: func(*localHTTPPermit) *APIUpstreamLease { return nil }},
		{name: "zero value lease", lease: func(*localHTTPPermit) *APIUpstreamLease { return &APIUpstreamLease{} }},
		{
			name: "zero upstream id with permit",
			lease: func(permit *localHTTPPermit) *APIUpstreamLease {
				upstream := validUpstream
				upstream.ID = 0
				return &APIUpstreamLease{Upstream: upstream, permit: permit}
			},
			wantPermitFinish: true,
		},
		{
			name: "valid upstream id with nil permit",
			lease: func(*localHTTPPermit) *APIUpstreamLease {
				return &APIUpstreamLease{Upstream: validUpstream}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permit := newLocalHTTPPermit()
			picker := &localHTTPPicker{lease: test.lease(permit)}
			var transportCalls atomic.Int32
			transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				transportCalls.Add(1)
				return &http.Response{
					StatusCode: http.StatusNoContent, Header: make(http.Header), Body: http.NoBody, Request: request,
				}, nil
			}))
			handler := NewHTTPHandler(picker, transport)
			request := httptest.NewRequest(http.MethodGet, "http://gateway.invalid/invalid-lease", nil)
			rc, _ := newLocalHTTPRequestContext(request)
			var serveErr error

			require.NotPanics(t, func() { serveErr = handler.Serve(request.Context(), rc) })
			require.ErrorIs(t, serveErr, ErrExecutionUnavailable)
			require.Equal(t, int32(1), picker.calls.Load())
			require.Zero(t, transportCalls.Load(), "an invalid frozen lease must never dispatch")
			if test.wantPermitFinish {
				completion := permit.completion(t)
				require.ErrorIs(t, completion.Err, ErrExecutionUnavailable)
				require.False(t, completion.Result.ProviderDispatched)
				require.Equal(t, int32(1), permit.calls.Load())
			} else {
				require.Zero(t, permit.calls.Load())
			}
		})
	}
}

func TestLocalHTTPFinalizesFrozenLeaseExactlyOnceOnBuildAndTransportFailure(t *testing.T) {
	tests := []struct {
		name      string
		upstream  protocol.SyncedAPIUpstream
		transport *HTTPTransport
		stage     string
	}{
		{
			name: "request build failure",
			upstream: protocol.SyncedAPIUpstream{
				ID: 31, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "bearer",
			},
			transport: NewHTTPTransport(""),
			stage:     "request_build",
		},
		{
			name: "transport failure",
			upstream: protocol.SyncedAPIUpstream{
				ID: 32, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "none",
			},
			transport: newHTTPTransportWithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, errors.New("dial failed")
			})),
			stage: "transport",
		},
		{
			name: "invalid response before commit",
			upstream: protocol.SyncedAPIUpstream{
				ID: 33, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "none",
			},
			transport: newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{Header: make(http.Header), Body: http.NoBody, Request: request}, nil
			})),
			stage: "response_header",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			permit := newLocalHTTPPermit()
			picker := &localHTTPPicker{lease: &APIUpstreamLease{Upstream: test.upstream, permit: permit}}
			handler := NewHTTPHandler(picker, test.transport)
			request := httptest.NewRequest(http.MethodPost, "http://gateway.invalid/fail", strings.NewReader("body"))
			rc, recorder := newLocalHTTPRequestContext(request)

			err := handler.Serve(request.Context(), rc)
			require.Error(t, err)
			require.False(t, recorder.Flushed)
			require.Equal(t, int32(1), picker.calls.Load(), "one request must never re-pick or fall back")
			completion := permit.completion(t)
			require.Equal(t, test.upstream.ID, completion.Result.APIUpstreamID)
			require.Equal(t, test.stage, completion.Result.ErrorStage)
			require.Equal(t, int32(1), permit.calls.Load())
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type closeBlockingBody struct {
	readStarted chan struct{}
	closed      chan struct{}
	release     chan struct{}
	started     atomic.Bool
	closedOnce  atomic.Bool
}

type stagedRequestBody struct {
	first     *bytes.Reader
	rest      *bytes.Reader
	finish    chan struct{}
	finished  atomic.Bool
	closed    atomic.Bool
	reads     atomic.Int32
	bytesRead atomic.Int64
	waited    bool
}

type pacedRequestBody struct {
	remaining int
	delay     time.Duration
	closed    atomic.Bool
}

func (b *pacedRequestBody) Read(value []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	time.Sleep(b.delay)
	value[0] = 'x'
	b.remaining--
	return 1, nil
}

func (b *pacedRequestBody) Close() error {
	b.closed.Store(true)
	return nil
}

func newStagedRequestBody(first, rest []byte) *stagedRequestBody {
	return &stagedRequestBody{
		first: bytes.NewReader(first), rest: bytes.NewReader(rest), finish: make(chan struct{}),
	}
}

func (b *stagedRequestBody) Read(value []byte) (int, error) {
	b.reads.Add(1)
	if b.first.Len() > 0 {
		count, err := b.first.Read(value)
		b.bytesRead.Add(int64(count))
		return count, err
	}
	if !b.waited {
		<-b.finish
		b.waited = true
	}
	count, err := b.rest.Read(value)
	b.bytesRead.Add(int64(count))
	return count, err
}

func (b *stagedRequestBody) Finish() {
	if b.finished.CompareAndSwap(false, true) {
		close(b.finish)
	}
}

func (b *stagedRequestBody) Close() error {
	b.Finish()
	b.closed.Store(true)
	return nil
}

func newCloseBlockingBody() *closeBlockingBody {
	return &closeBlockingBody{
		readStarted: make(chan struct{}),
		closed:      make(chan struct{}),
		release:     make(chan struct{}),
	}
}

func (b *closeBlockingBody) Read([]byte) (int, error) {
	if b.started.CompareAndSwap(false, true) {
		close(b.readStarted)
	}
	<-b.release
	return 0, context.Canceled
}

func (b *closeBlockingBody) Close() error {
	if b.closedOnce.CompareAndSwap(false, true) {
		close(b.closed)
		close(b.release)
	}
	return nil
}

type readThenErrorBody struct {
	value []byte
	err   error
}

func (b *readThenErrorBody) Read(target []byte) (int, error) {
	if len(b.value) > 0 {
		count := copy(target, b.value)
		b.value = b.value[count:]
		return count, nil
	}
	return 0, b.err
}

func (*readThenErrorBody) Close() error { return nil }

type prefixThenBlockingBody struct {
	prefix  []byte
	blocker *closeBlockingBody
}

func (b *prefixThenBlockingBody) Read(target []byte) (int, error) {
	if len(b.prefix) > 0 {
		count := copy(target, b.prefix)
		b.prefix = b.prefix[count:]
		return count, nil
	}
	return b.blocker.Read(target)
}

func (b *prefixThenBlockingBody) Close() error { return b.blocker.Close() }

func requireClosed(t *testing.T, closed <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s was not closed", name)
	}
}
