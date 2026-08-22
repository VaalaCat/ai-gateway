package genericapi

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/VaalaCat/ai-gateway/internal/agent/cache"
	relaylimiter "github.com/VaalaCat/ai-gateway/internal/agent/relay/limiter"
	agenttunnel "github.com/VaalaCat/ai-gateway/internal/agent/tunnel"
	"github.com/VaalaCat/ai-gateway/internal/models"
	"github.com/VaalaCat/ai-gateway/internal/pkg/apiattempt"
	"github.com/VaalaCat/ai-gateway/internal/pkg/protocol"
	wire "github.com/VaalaCat/ai-gateway/internal/pkg/tunnel"
	"github.com/stretchr/testify/require"
)

type frozenServiceRouteFinder struct {
	value ServiceRoute
	calls atomic.Int32
}

func (f *frozenServiceRouteFinder) FindServiceRouteByID(serviceID, routeID uint) (ServiceRoute, error) {
	f.calls.Add(1)
	if f.value.Service.ID != serviceID || f.value.Route.ID != routeID {
		return ServiceRoute{}, ErrExecutionUnavailable
	}
	return f.value, nil
}

type targetHTTPStreamStub struct {
	open       wire.Open
	requests   []agenttunnel.APIRequestEvent
	headers    []wire.Headers
	body       bytes.Buffer
	ends       []wire.Trailers
	results    []apiattempt.APIExecutionResult
	receivePos int
}

type excessRateHitProtocolHandler struct{}

func (excessRateHitProtocolHandler) Serve(_ context.Context, rc *RequestContext) error {
	rc.Execution.ProviderDispatchKnown = true
	for limiterID := uint(65); limiterID > 0; limiterID-- {
		rc.Execution.RateLimitHits = append(rc.Execution.RateLimitHits, models.RateLimitHit{
			LimiterID: limiterID,
			Name:      "rate",
			Dimension: "rate/shared",
			Bucket:    "api_route:9:shared",
			Decision:  "allow",
		})
	}
	return nil
}

func (s *targetHTTPStreamStub) OpenMetadata() wire.Open { return s.open }

func (s *targetHTTPStreamStub) ReceiveRequest(context.Context) (agenttunnel.APIRequestEvent, error) {
	if s.receivePos >= len(s.requests) {
		return agenttunnel.APIRequestEvent{}, errors.New("unexpected request receive")
	}
	event := s.requests[s.receivePos]
	s.receivePos++
	return event, nil
}

func (s *targetHTTPStreamStub) SendHeaders(_ context.Context, value wire.Headers) error {
	s.headers = append(s.headers, value)
	return nil
}

func (s *targetHTTPStreamStub) SendResponseData(_ context.Context, value []byte) error {
	_, _ = s.body.Write(value)
	return nil
}

func (s *targetHTTPStreamStub) EndResponse(_ context.Context, value wire.Trailers) error {
	s.ends = append(s.ends, value)
	return nil
}

func (s *targetHTTPStreamStub) SendResult(_ context.Context, value apiattempt.APIExecutionResult) error {
	s.results = append(s.results, value)
	return nil
}

func newTargetHTTPStream(body string) *targetHTTPStreamStub {
	meta := apiattempt.APIAttemptMeta{
		APIServiceID: 7, APIRouteID: 9, Protocol: apiattempt.APIProtocolHTTP,
		Method: http.MethodPost, Subpath: "/current", RawQuery: "units=c",
	}
	return &targetHTTPStreamStub{
		open: wire.Open{
			Method: http.MethodPost, Path: "/v1/api/weather/forecast/current", Header: map[string][]string{"Content-Type": {"text/plain"}},
			BodyLength: int64(len(body)), RequestID: "request-remote", SourceAgentID: "source-a", TargetAgentID: "execution-a", API: &meta,
		},
		requests: []agenttunnel.APIRequestEvent{
			{Kind: agenttunnel.APIRequestData, Data: []byte(body)},
			{Kind: agenttunnel.APIRequestEnd, Trailers: wire.Trailers{}},
		},
	}
}

func TestAPITargetHandlerBoundsExcessLimiterHitsBeforeResult(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7},
		Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7},
	}}
	handler := NewAPITargetHandler(finder, excessRateHitProtocolHandler{})
	stream := newTargetHTTPStream("")
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Len(t, stream.results, 1)
	result := stream.results[0]
	require.Len(t, result.RateLimitHits, 64)
	require.Equal(t, 65, result.RateLimitHitTotal)
	require.True(t, result.RateLimitHitsTruncated)
	require.Equal(t, uint(1), result.RateLimitHits[0].LimiterID)
	require.Equal(t, uint(64), result.RateLimitHits[63].LimiterID)
	require.NoError(t, result.Validate())
}

func TestAPITargetHandlerSendsPrepareErrorMessageWithoutCredential(t *testing.T) {
	finder := &frozenServiceRouteFinder{}
	handler := NewAPITargetHandler(finder, excessRateHitProtocolHandler{})
	stream := newTargetHTTPStream("")
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Len(t, stream.results, 1)
	require.Equal(t, "execution", stream.results[0].ErrorStage)
	require.NotEmpty(t, stream.results[0].ErrorMessage)
}

type fixedExecutionResultHandler struct{ result apiattempt.APIExecutionResult }

func (h fixedExecutionResultHandler) Serve(_ context.Context, rc *RequestContext) error {
	rc.Execution = h.result
	return nil
}

func TestAPITargetHandlerPreservesLocalExecutionErrorMessage(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7},
	}}
	handler := NewAPITargetHandler(finder, fixedExecutionResultHandler{result: apiattempt.APIExecutionResult{
		ProviderDispatchKnown: true, ErrorStage: "transport", ErrorCode: CodeUnavailable,
		ErrorMessage: "dial tcp: connection refused",
	}})
	stream := newTargetHTTPStream("")
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Len(t, stream.results, 1)
	require.Equal(t, "dial tcp: connection refused", stream.results[0].ErrorMessage)
}

func TestAPITargetHandlerRejectsInvalidErrorMessageResult(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7},
	}}
	for _, message := range []string{strings.Repeat("x", apiattempt.MaxAPIErrorMessageBytes+1), "line\nbreak"} {
		stream := newTargetHTTPStream("")
		stream.open.BodyLength = 0
		stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}
		handler := NewAPITargetHandler(finder, fixedExecutionResultHandler{result: apiattempt.APIExecutionResult{
			ProviderDispatchKnown: true, ErrorMessage: message,
		}})

		require.ErrorIs(t, handler.serveStream(t.Context(), stream), apiattempt.ErrInvalidExecutionResult)
		require.Empty(t, stream.results)
	}
}

func TestAPITargetHandlerPicksOneUpstreamGatesThenDispatchesOnce(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7, Name: "Weather"},
		Route:   protocol.SyncedAPIRoute{ID: 9, ServiceID: 7, Slug: "forecast", ForwardSubpath: true},
	}}
	breakerPermit := newLocalHTTPPermit()
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, Name: "Primary", BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   breakerPermit,
	}}
	var providerCalls atomic.Int32
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(request *http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		body := make([]byte, len("payload"))
		_, err := request.Body.Read(body)
		require.NoError(t, err)
		require.Equal(t, "payload", string(body))
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"X-Upstream": {"one"}}, Body: http.NoBody, Request: request}, nil
	}))
	limiter := &executionLimiterSpy{}
	handler := NewAPITargetHandler(finder, NewHTTPHandler(picker, transport, limiter))
	stream := newTargetHTTPStream("payload")

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Equal(t, int32(1), finder.calls.Load())
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, 1, limiter.calls)
	require.Equal(t, uint(11), limiter.facts.APIUpstreamID)
	require.Equal(t, int32(1), providerCalls.Load())
	require.Len(t, stream.headers, 1)
	require.Equal(t, http.StatusCreated, stream.headers[0].StatusCode)
	require.Len(t, stream.ends, 1)
	require.Len(t, stream.results, 1)
	require.Equal(t, uint(11), stream.results[0].APIUpstreamID)
	require.Equal(t, "Primary", stream.results[0].APIUpstreamName)
	require.True(t, stream.results[0].ProviderDispatchKnown)
	require.True(t, stream.results[0].ProviderDispatched)
}

type rejectExecutionLimiter struct{ calls atomic.Int32 }

func (l *rejectExecutionLimiter) Acquire(context.Context, APIRequestFacts) (APIPermit, error) {
	l.calls.Add(1)
	return nil, ErrAPIRateLimited
}

func TestAPITargetHandlerLimiterRejectSendsResultWithoutProviderDispatch(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7},
	}}
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	var providerCalls atomic.Int32
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		return nil, errors.New("must not dispatch")
	}))
	limiter := &rejectExecutionLimiter{}
	handler := NewAPITargetHandler(finder, NewHTTPHandler(picker, transport, limiter))
	stream := newTargetHTTPStream("")
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Equal(t, int32(1), picker.calls.Load())
	require.Equal(t, int32(1), limiter.calls.Load())
	require.Zero(t, providerCalls.Load())
	require.Len(t, stream.headers, 1)
	require.Equal(t, http.StatusTooManyRequests, stream.headers[0].StatusCode)
	require.Len(t, stream.ends, 1)
	require.Len(t, stream.results, 1)
	require.Equal(t, "limiter", stream.results[0].ErrorStage)
	require.Equal(t, CodeRateLimited, stream.results[0].ErrorCode)
	require.True(t, stream.results[0].ProviderDispatchKnown)
	require.False(t, stream.results[0].ProviderDispatched)
	require.NoError(t, stream.results[0].Validate())
}

func TestAPITargetHandlerPreservesExecutionLimiterTerminalFacts(t *testing.T) {
	finder := &frozenServiceRouteFinder{value: ServiceRoute{
		Service: protocol.SyncedAPIService{ID: 7}, Route: protocol.SyncedAPIRoute{ID: 9, ServiceID: 7},
	}}
	picker := &localHTTPPicker{lease: &APIUpstreamLease{
		Upstream: protocol.SyncedAPIUpstream{ID: 11, BackendID: 7, BaseURL: "http://upstream.invalid", AuthType: "none"},
		permit:   newLocalHTTPPermit(),
	}}
	var providerCalls atomic.Int32
	transport := newHTTPTransportWithRoundTripper(roundTripFunc(func(*http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		return nil, errors.New("must not dispatch")
	}))
	idx := cache.NewLimiterIndex()
	idx.LoadLimiters([]models.RequestLimiter{{
		ID: 3, Name: "upstream-rate", Enabled: true, Metric: models.LimiterMetricRate,
		Capacity: 1, WindowMs: 60_000, KeyBy: models.LimiterKeyShared, Action: models.LimiterActionReject,
	}})
	idx.LoadBindings([]models.LimiterBinding{{
		ID: 3, LimiterID: 3, TargetType: models.LimiterTargetAPIUpstream, TargetID: 11, Enabled: true,
	}})
	store := relaylimiter.NewMemStore()
	require.True(t, store.TryRate(relaylimiter.BucketKey{LimiterID: 3, Bucket: "api_upstream:11:shared"}, 1, 60_000))
	handler := NewAPITargetHandler(finder, NewHTTPHandler(picker, transport, NewLimiterGate(idx, store)))
	stream := newTargetHTTPStream("")
	stream.open.BodyLength = 0
	stream.requests = []agenttunnel.APIRequestEvent{{Kind: agenttunnel.APIRequestEnd}}

	require.NoError(t, handler.serveStream(t.Context(), stream))
	require.Zero(t, providerCalls.Load())
	require.Len(t, stream.results, 1)
	result := stream.results[0]
	require.Equal(t, "rejected", result.RateLimitDecision)
	require.Equal(t, CodeRateLimited, result.RateLimitReason)
	require.Len(t, result.RateLimitHits, 1)
	require.Equal(t, uint(3), result.RateLimitHits[0].LimiterID)
	require.Equal(t, "rejected", result.RateLimitHits[0].Decision)
	require.NoError(t, result.Validate())
}
